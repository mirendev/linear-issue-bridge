package page

import (
	"bytes"
	"embed"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/util"

	"miren.dev/linear-issue-bridge/internal/linearapi"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

// newMarkdown builds a goldmark instance that rewrites Linear's authenticated
// cross-reference links into public bridge / GitHub links as it renders.
func newMarkdown(teamKey string) goldmark.Markdown {
	return goldmark.New(
		goldmark.WithExtensions(
			extension.GFM,
		),
		goldmark.WithParserOptions(
			parser.WithASTTransformers(
				util.Prioritized(&linkRewriter{teamKey: teamKey}, 100),
			),
		),
		goldmark.WithRendererOptions(
			html.WithUnsafe(),
		),
	)
}

type Renderer struct {
	templates *template.Template
	teamKey   string
	baseURL   string
	md        goldmark.Markdown
}

func NewRenderer(teamKey string, fathomSiteID string, baseURL string) (*Renderer, error) {
	md := newMarkdown(teamKey)

	funcMap := template.FuncMap{
		"markdown":     func(src string) template.HTML { return renderMarkdown(md, src) },
		"fathomSiteID": func() string { return fathomSiteID },
		"formatDate": func(t time.Time) string {
			return t.Format("Jan 2, 2006")
		},
	}

	tmpl, err := template.New("").Funcs(funcMap).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, err
	}

	return &Renderer{
		templates: tmpl,
		teamKey:   teamKey,
		baseURL:   strings.TrimRight(baseURL, "/"),
		md:        md,
	}, nil
}

// Meta carries OpenGraph / Twitter card values for a page's <head>.
//
// We deliberately omit og:image. The big-card platforms (Bluesky, Facebook,
// LinkedIn, Slack) always render a wide 1.91:1 banner and crop whatever image
// we give them, and none of them honor twitter:card=summary, so any logo or
// icon ends up stretched or sliced. A text-only card (title + description +
// domain) reads cleanly everywhere instead.
type Meta struct {
	Title       string
	Description string
	URL         string
	Type        string // "website" or "article"
	SiteName    string
}

// meta builds the OG metadata shared by every page. path is the absolute path
// (e.g. "/MIR-42"); title/desc/ogType vary per page.
func (r *Renderer) meta(path, title, desc, ogType string) Meta {
	return Meta{
		Title:       title,
		Description: desc,
		URL:         r.baseURL + path,
		Type:        ogType,
		SiteName:    "Miren Issues",
	}
}

func (r *Renderer) StaticHandler() http.Handler {
	sub, _ := fs.Sub(staticFS, "static")
	return http.FileServerFS(sub)
}

type pageData struct {
	Meta Meta
}

func (r *Renderer) RenderIndexPage(w io.Writer) error {
	return r.templates.ExecuteTemplate(w, "index.html", pageData{
		Meta: r.meta("/", "Miren Issues", "Public issues for Miren, straight from the source.", "website"),
	})
}

type issuePageData struct {
	Issue           *linearapi.Issue
	DescriptionHTML template.HTML
	GitHubPRs       []linearapi.Attachment
	DuplicateOf     *linearapi.Relation
	TeamKey         string
	Meta            Meta
}

func (r *Renderer) issueData(issue *linearapi.Issue) issuePageData {
	return issuePageData{
		Issue:           issue,
		DescriptionHTML: renderMarkdown(r.md, issue.Description),
		GitHubPRs:       issue.GitHubPRs(),
		DuplicateOf:     issue.DuplicateOf(),
		TeamKey:         r.teamKey,
		Meta: r.meta(
			"/"+issue.Identifier,
			issue.Identifier+": "+issue.Title,
			summarize(issue.Description, 200),
			"article",
		),
	}
}

func (r *Renderer) RenderIssuePage(w io.Writer, issue *linearapi.Issue) error {
	return r.templates.ExecuteTemplate(w, "issue.html", r.issueData(issue))
}

type issueShellData struct {
	Identifier string
	Meta       Meta
}

// RenderIssueShell renders an issue page with nothing from Linear in it yet,
// for when Linear is too slow to wait on. Its script fills it in from the
// /api/issue endpoint, saying what it's doing while it waits.
func (r *Renderer) RenderIssueShell(w io.Writer, identifier string) error {
	return r.templates.ExecuteTemplate(w, "issue_shell.html", issueShellData{
		Identifier: identifier,
		Meta:       r.meta("/"+identifier, identifier+" — Miren", "A Miren issue, shared from Linear.", "website"),
	})
}

// Fragment is a page's <main> content plus its document title, for the issue
// shell to swap in once Linear answers.
type Fragment struct {
	Title string `json:"title"`
	HTML  string `json:"html"`
}

func (r *Renderer) fragment(name, title string, data any) (Fragment, error) {
	var buf bytes.Buffer
	if err := r.templates.ExecuteTemplate(&buf, name, data); err != nil {
		return Fragment{}, err
	}
	return Fragment{Title: title, HTML: buf.String()}, nil
}

func (r *Renderer) IssueFragment(issue *linearapi.Issue) (Fragment, error) {
	return r.fragment("issue-main", issue.Identifier+": "+issue.Title+" — Miren", r.issueData(issue))
}

func (r *Renderer) StubFragment(identifier string) (Fragment, error) {
	return r.fragment("stub-main", identifier+" — Miren", stubPageData{Identifier: identifier, TeamKey: r.teamKey})
}

func (r *Renderer) NotFoundFragment() (Fragment, error) {
	return r.fragment("notfound-main", "Not Found — Miren", pageData{})
}

type stubPageData struct {
	Identifier string
	TeamKey    string
	Meta       Meta
}

func (r *Renderer) RenderStubPage(w io.Writer, identifier string) error {
	return r.templates.ExecuteTemplate(w, "stub.html", stubPageData{
		Identifier: identifier,
		TeamKey:    r.teamKey,
		Meta:       r.meta("/"+identifier, identifier+" — Miren", "This issue isn't public yet, or doesn't exist.", "website"),
	})
}

type issuesPageData struct {
	TeamKey string
	Meta    Meta
}

func (r *Renderer) RenderIssuesPage(w io.Writer) error {
	return r.templates.ExecuteTemplate(w, "issues.html", issuesPageData{
		TeamKey: r.teamKey,
		Meta:    r.meta("/issues", "Issues — Miren", "Browse public issues for Miren.", "website"),
	})
}

type SuggestPageData struct {
	Title       string
	Description string
	Contact     string
	Error       string
	Success     bool
	Identifier  string
	Meta        Meta
}

func (r *Renderer) RenderSuggestPage(w io.Writer, data SuggestPageData) error {
	data.Meta = r.meta("/suggest", "Submit an Issue — Miren", "Suggest an issue to the Miren team.", "website")
	return r.templates.ExecuteTemplate(w, "suggest.html", data)
}

func (r *Renderer) RenderNotFound(w io.Writer) error {
	return r.templates.ExecuteTemplate(w, "notfound.html", pageData{
		Meta: r.meta("/", "Not Found — Miren", "This page couldn't be found.", "website"),
	})
}

// summarize turns issue-description markdown into a plain-text snippet suitable
// for an og:description: links/images collapse to their text, formatting and
// block markers are stripped, whitespace is collapsed, and the result is cut to
// roughly max characters on a word boundary.
func summarize(src string, max int) string {
	s := src
	s = mdImageRe.ReplaceAllString(s, "$1")
	s = mdLinkRe.ReplaceAllString(s, "$1")
	s = mdBlockRe.ReplaceAllString(s, "")
	s = mdInlineRe.ReplaceAllString(s, "")
	s = strings.Join(strings.Fields(s), " ")

	if len(s) <= max {
		return s
	}
	cut := s[:max]
	if i := strings.LastIndex(cut, " "); i > 0 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " .,;:") + "…"
}

var (
	mdImageRe = regexp.MustCompile(`!\[([^\]]*)\]\([^)]*\)`)
	mdLinkRe  = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	// Drop line-leading block markers (headings, blockquotes, list bullets).
	mdBlockRe = regexp.MustCompile(`(?m)^\s*(#{1,6}|>+|[-+*]|\d+\.)\s+`)
	// Drop inline emphasis/code markers, leaving their text.
	mdInlineRe = regexp.MustCompile("[`*_~]")
)

func renderMarkdown(md goldmark.Markdown, src string) template.HTML {
	var buf bytes.Buffer
	if err := md.Convert([]byte(src), &buf); err != nil {
		return template.HTML("<p>" + template.HTMLEscapeString(src) + "</p>")
	}
	return template.HTML(buf.String())
}
