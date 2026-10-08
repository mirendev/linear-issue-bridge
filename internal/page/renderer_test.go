package page

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"miren.dev/linear-issue-bridge/internal/linearapi"
)

func TestRenderIndexPage(t *testing.T) {
	r, err := NewRenderer("MIR", "", "https://linear.miren.garden")
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}

	var buf bytes.Buffer
	if err := r.RenderIndexPage(&buf); err != nil {
		t.Fatalf("RenderIndexPage: %v", err)
	}

	html := buf.String()

	checks := []string{
		"Public Issues",
		"Linear",
		"develops in the open",
		"github.com/mirendev/runtime",
		"github.com/mirendev/linear-issue-bridge",
		"miren.dev",
	}

	for _, check := range checks {
		if !strings.Contains(html, check) {
			t.Errorf("output missing %q", check)
		}
	}
}

func TestRenderIssuePage(t *testing.T) {
	r, err := NewRenderer("MIR", "", "https://linear.miren.garden")
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}

	issue := &linearapi.Issue{
		Identifier:  "MIR-42",
		Title:       "Test Issue Title",
		Description: "This is a **bold** description.",
		State:       linearapi.State{Name: "In Progress", Color: "#f2c94c", Type: "started"},
		Labels: []linearapi.Label{
			{Name: "public", Color: "#5e6ad2"},
		},
		Attachments: []linearapi.Attachment{
			{URL: "https://github.com/mirendev/linear-issue-bridge/pull/1", Title: "feat: add PR links"},
		},
		URL:       "https://linear.app/miren/issue/MIR-42",
		CreatedAt: time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC),
	}

	var buf bytes.Buffer
	if err := r.RenderIssuePage(&buf, issue); err != nil {
		t.Fatalf("RenderIssuePage: %v", err)
	}

	html := buf.String()

	checks := []string{
		"MIR-42",
		"Test Issue Title",
		"<strong>bold</strong>",
		"In Progress",
		"public",
		"github.com/mirendev/linear-issue-bridge/pull/1",
		"feat: add PR links",
		"github-pr-link",
		// OpenGraph / Twitter card meta (text-only, no og:image by design)
		`<meta property="og:title" content="MIR-42: Test Issue Title">`,
		`<meta property="og:type" content="article">`,
		`<meta property="og:url" content="https://linear.miren.garden/MIR-42">`,
		`<meta property="og:description" content="This is a bold description.">`,
		`<meta name="twitter:card" content="summary">`,
	}

	for _, check := range checks {
		if !strings.Contains(html, check) {
			t.Errorf("output missing %q", check)
		}
	}

	// og:image is intentionally omitted: big-card platforms crop it badly.
	if strings.Contains(html, "og:image") {
		t.Error("output unexpectedly contains og:image")
	}
}

func TestRenderStubPage(t *testing.T) {
	r, err := NewRenderer("MIR", "", "https://linear.miren.garden")
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}

	var buf bytes.Buffer
	if err := r.RenderStubPage(&buf, "MIR-42"); err != nil {
		t.Fatalf("RenderStubPage: %v", err)
	}

	html := buf.String()
	if !strings.Contains(html, "MIR-42") {
		t.Error("stub page missing identifier")
	}
	if !strings.Contains(html, "not currently shared publicly") {
		t.Error("stub page missing explanation text")
	}
}

func TestRenderNotFound(t *testing.T) {
	r, err := NewRenderer("MIR", "", "https://linear.miren.garden")
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}

	var buf bytes.Buffer
	if err := r.RenderNotFound(&buf); err != nil {
		t.Fatalf("RenderNotFound: %v", err)
	}

	html := buf.String()
	if !strings.Contains(html, "not found") {
		t.Error("not found page missing expected text")
	}
}

func TestStaticHandlerContentType(t *testing.T) {
	r, err := NewRenderer("MIR", "", "https://linear.miren.garden")
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}

	handler := http.StripPrefix("/static/", r.StaticHandler())
	srv := httptest.NewServer(handler)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/static/style.css")
	if err != nil {
		t.Fatalf("GET /static/style.css: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/css") {
		t.Errorf("expected Content-Type text/css, got %q", ct)
	}
}

func TestRenderMarkdown(t *testing.T) {
	md := newMarkdown("MIR")
	tests := []struct {
		name     string
		input    string
		contains string
	}{
		{"bold", "**bold**", "<strong>bold</strong>"},
		{"code", "`code`", "<code>code</code>"},
		{"link", "[link](https://example.com)", `href="https://example.com"`},
		{"list", "- item 1\n- item 2", "<li>item 1</li>"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := string(renderMarkdown(md, tt.input))
			if !strings.Contains(result, tt.contains) {
				t.Errorf("renderMarkdown(%q) = %q, missing %q", tt.input, result, tt.contains)
			}
		})
	}
}

func TestRenderIssueShell(t *testing.T) {
	r, err := NewRenderer("MIR", "", "https://linear.miren.garden")
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}

	var buf bytes.Buffer
	if err := r.RenderIssueShell(&buf, "MIR-42"); err != nil {
		t.Fatalf("RenderIssueShell: %v", err)
	}

	html := buf.String()
	for _, check := range []string{
		`data-issue-shell="MIR-42"`,
		`class="linear-status"`,
		"Talking to Linear",
		"/static/linear.js",
		`content="https://linear.miren.garden/MIR-42"`,
	} {
		if !strings.Contains(html, check) {
			t.Errorf("shell missing %q", check)
		}
	}
}

func TestIssueFragmentMatchesFullPage(t *testing.T) {
	r, err := NewRenderer("MIR", "", "https://linear.miren.garden")
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}

	issue := &linearapi.Issue{
		Identifier:  "MIR-42",
		Title:       "Fragment Title",
		Description: "Some **bold** text.",
		State:       linearapi.State{Name: "Todo", Color: "#e2e2e2", Type: "unstarted"},
	}

	frag, err := r.IssueFragment(issue)
	if err != nil {
		t.Fatalf("IssueFragment: %v", err)
	}
	if frag.Title != "MIR-42: Fragment Title — Miren" {
		t.Errorf("Title = %q", frag.Title)
	}
	if strings.Contains(frag.HTML, "<html") || strings.Contains(frag.HTML, "<header") {
		t.Error("fragment should be <main> content only")
	}

	// The shell swaps the fragment into <main>, so it must be exactly what
	// the full page renders there.
	var page bytes.Buffer
	if err := r.RenderIssuePage(&page, issue); err != nil {
		t.Fatalf("RenderIssuePage: %v", err)
	}
	if !strings.Contains(page.String(), frag.HTML) {
		t.Error("full page does not contain the fragment verbatim")
	}
}

func TestStubAndNotFoundFragments(t *testing.T) {
	r, err := NewRenderer("MIR", "", "https://linear.miren.garden")
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}

	stub, err := r.StubFragment("MIR-42")
	if err != nil {
		t.Fatalf("StubFragment: %v", err)
	}
	if !strings.Contains(stub.HTML, "not currently shared publicly") || stub.Title != "MIR-42 — Miren" {
		t.Errorf("stub fragment = %+v", stub)
	}

	nf, err := r.NotFoundFragment()
	if err != nil {
		t.Fatalf("NotFoundFragment: %v", err)
	}
	if !strings.Contains(nf.HTML, "Issue not found") {
		t.Errorf("not-found fragment = %+v", nf)
	}
}
