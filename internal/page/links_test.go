package page

import (
	"strings"
	"testing"
)

func TestRewriteLink(t *testing.T) {
	tests := []struct {
		name     string
		dest     string
		linkText string
		teamKey  string
		want     string
		wantOK   bool
	}{
		{
			name:     "linear issue for our team becomes relative bridge link",
			dest:     "https://linear.app/miren/issue/MIR-1242/ruby-stack-ignores-ruby-version",
			linkText: "MIR-1242",
			teamKey:  "MIR",
			want:     "/MIR-1242",
			wantOK:   true,
		},
		{
			name:     "linear issue for another team is left alone",
			dest:     "https://linear.app/miren/issue/ENG-99/something",
			linkText: "ENG-99",
			teamKey:  "MIR",
			wantOK:   false,
		},
		{
			name:     "github pr ref via linear review url uses the link text",
			dest:     "https://linear.app/miren/review/detect-ruby-version-89d7f3d243f1",
			linkText: "mirendev/runtime#858",
			teamKey:  "MIR",
			want:     "https://github.com/mirendev/runtime/pull/858",
			wantOK:   true,
		},
		{
			name:     "linear review url without a github-shaped text is left alone",
			dest:     "https://linear.app/miren/review/some-review",
			linkText: "see the review",
			teamKey:  "MIR",
			wantOK:   false,
		},
		{
			name:     "plain external link is left alone",
			dest:     "https://example.com",
			linkText: "example",
			teamKey:  "MIR",
			wantOK:   false,
		},
		{
			name:     "real github link is left alone",
			dest:     "https://github.com/mirendev/runtime/pull/858",
			linkText: "mirendev/runtime#858",
			teamKey:  "MIR",
			wantOK:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := rewriteLink(tt.dest, tt.linkText, tt.teamKey)
			if ok != tt.wantOK {
				t.Fatalf("rewriteLink(%q, %q) ok = %v, want %v", tt.dest, tt.linkText, ok, tt.wantOK)
			}
			if ok && got != tt.want {
				t.Errorf("rewriteLink(%q, %q) = %q, want %q", tt.dest, tt.linkText, got, tt.want)
			}
		})
	}
}

// TestRewriteLinkEndToEnd renders a description shaped like Linear's real
// output and checks both rules fire through the goldmark pipeline.
func TestRewriteLinkEndToEnd(t *testing.T) {
	md := newMarkdown("MIR")
	src := "Fixed in [mirendev/runtime#858](https://linear.app/miren/review/detect-ruby-89d7f3d243f1), " +
		"surfaced by [MIR-1242](https://linear.app/miren/issue/MIR-1242/ruby-stack)."

	html := string(renderMarkdown(md, src))

	if !strings.Contains(html, `href="https://github.com/mirendev/runtime/pull/858"`) {
		t.Errorf("expected rewritten GitHub PR link, got: %s", html)
	}
	if !strings.Contains(html, `href="/MIR-1242"`) {
		t.Errorf("expected rewritten bridge issue link, got: %s", html)
	}
	if strings.Contains(html, "linear.app") {
		t.Errorf("expected no linear.app links to remain, got: %s", html)
	}
}
