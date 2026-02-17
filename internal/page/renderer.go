package page

import (
	"bytes"
	"embed"
	"html/template"
	"io"
	"io/fs"
	"net/http"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"

	"miren.dev/linear-issue-bridge/internal/linearapi"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

var md = goldmark.New(
	goldmark.WithExtensions(
		extension.GFM,
	),
	goldmark.WithRendererOptions(
		html.WithUnsafe(),
	),
)

type Renderer struct {
	templates *template.Template
	teamKey   string
}

func NewRenderer(teamKey string) (*Renderer, error) {
	funcMap := template.FuncMap{
		"markdown": renderMarkdown,
	}

	tmpl, err := template.New("").Funcs(funcMap).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, err
	}

	return &Renderer{
		templates: tmpl,
		teamKey:   teamKey,
	}, nil
}

func (r *Renderer) StaticHandler() http.Handler {
	sub, _ := fs.Sub(staticFS, "static")
	return http.FileServerFS(sub)
}

type issuePageData struct {
	Issue           *linearapi.Issue
	DescriptionHTML template.HTML
	TeamKey         string
}

func (r *Renderer) RenderIssuePage(w io.Writer, issue *linearapi.Issue) error {
	descHTML := renderMarkdown(issue.Description)
	return r.templates.ExecuteTemplate(w, "issue.html", issuePageData{
		Issue:           issue,
		DescriptionHTML: descHTML,
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

func (r *Renderer) RenderNotFound(w io.Writer) error {
	return r.templates.ExecuteTemplate(w, "notfound.html", nil)
}

func renderMarkdown(src string) template.HTML {
	var buf bytes.Buffer
	if err := md.Convert([]byte(src), &buf); err != nil {
		return template.HTML("<p>" + template.HTMLEscapeString(src) + "</p>")
	}
	return template.HTML(buf.String())
}
