package page

import (
	"bytes"
	"embed"
	"html/template"
	"io"
	"io/fs"
	"net/http"
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
	md        goldmark.Markdown
}

func NewRenderer(teamKey string, fathomSiteID string) (*Renderer, error) {
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
		md:        md,
	}, nil
}

func (r *Renderer) StaticHandler() http.Handler {
	sub, _ := fs.Sub(staticFS, "static")
	return http.FileServerFS(sub)
}

func (r *Renderer) RenderIndexPage(w io.Writer) error {
	return r.templates.ExecuteTemplate(w, "index.html", nil)
}

type issuePageData struct {
	Issue           *linearapi.Issue
	DescriptionHTML template.HTML
	GitHubPRs       []linearapi.Attachment
	DuplicateOf     *linearapi.Relation
	TeamKey         string
}

func (r *Renderer) RenderIssuePage(w io.Writer, issue *linearapi.Issue) error {
	descHTML := renderMarkdown(r.md, issue.Description)
	return r.templates.ExecuteTemplate(w, "issue.html", issuePageData{
		Issue:           issue,
		DescriptionHTML: descHTML,
		GitHubPRs:       issue.GitHubPRs(),
		DuplicateOf:     issue.DuplicateOf(),
		TeamKey:         r.teamKey,
	})
}

type stubPageData struct {
	Identifier string
	TeamKey    string
}

func (r *Renderer) RenderStubPage(w io.Writer, identifier string) error {
	return r.templates.ExecuteTemplate(w, "stub.html", stubPageData{
		Identifier: identifier,
		TeamKey:    r.teamKey,
	})
}

type issuesPageData struct {
	TeamKey string
}

func (r *Renderer) RenderIssuesPage(w io.Writer) error {
	return r.templates.ExecuteTemplate(w, "issues.html", issuesPageData{
		TeamKey: r.teamKey,
	})
}

type SuggestPageData struct {
	Title       string
	Description string
	Contact     string
	Error       string
	Success     bool
	Identifier  string
}

func (r *Renderer) RenderSuggestPage(w io.Writer, data SuggestPageData) error {
	return r.templates.ExecuteTemplate(w, "suggest.html", data)
}

func (r *Renderer) RenderNotFound(w io.Writer) error {
	return r.templates.ExecuteTemplate(w, "notfound.html", nil)
}

func renderMarkdown(md goldmark.Markdown, src string) template.HTML {
	var buf bytes.Buffer
	if err := md.Convert([]byte(src), &buf); err != nil {
		return template.HTML("<p>" + template.HTMLEscapeString(src) + "</p>")
	}
	return template.HTML(buf.String())
}
