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

func TestRenderIssuePage(t *testing.T) {
	r, err := NewRenderer("MIR")
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
	}

	for _, check := range checks {
		if !strings.Contains(html, check) {
			t.Errorf("output missing %q", check)
		}
	}
}

func TestRenderStubPage(t *testing.T) {
	r, err := NewRenderer("MIR")
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
	r, err := NewRenderer("MIR")
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
	r, err := NewRenderer("MIR")
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
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/css") {
		t.Errorf("expected Content-Type text/css, got %q", ct)
	}
}

func TestRenderMarkdown(t *testing.T) {
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
			result := string(renderMarkdown(tt.input))
			if !strings.Contains(result, tt.contains) {
				t.Errorf("renderMarkdown(%q) = %q, missing %q", tt.input, result, tt.contains)
			}
		})
	}
}
