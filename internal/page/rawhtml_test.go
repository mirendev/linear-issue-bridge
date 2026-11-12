package page

import (
	"regexp"
	"strings"
	"testing"
)

// The probes filed through /suggest as MIR-2201..2204, in the shapes Linear
// hands them back to us.
var xssProbes = []struct {
	name  string
	input string
}{
	{
		"MIR-2201 img onerror",
		`Please document the expected sequence. <img src=x onerror="document.title='miren-proof-7f3a'"><b id="miren-proof-7f3a">marker-miren-proof-7f3a</b>`,
	},
	{
		// /suggest appends attachments as ![filename](url), so a hostile
		// filename lands inside image alt text.
		"MIR-2202 attachment filename in alt",
		`![x.png"><img src=x onerror=document.title="mfn-1">.png](https://uploads.linear.app/abc/def)`,
	},
	{
		"MIR-2202 linear-image markup",
		`<linear-image>{"type":"image","attrs":{"src":"https://uploads.linear.app/abc","alt":"x.png%22><img src=x onerror=document.title=%22mfn-1%22>.png"}}</linear-image>`,
	},
	{
		"MIR-2203 svg onload",
		`Second renderer check. <svg onload="document.title='mfn-replay-42'"></svg><i id="mfn-replay-42">replay-marker</i>`,
	},
	{
		"MIR-2204 img onerror",
		`The rollback section does not say. <img src=y onerror="document.title='vrf-6c1e'"><b id="vrf-6c1e">verifier-marker-6c1e</b>`,
	},
	{
		"html block on its own line",
		"Intro.\n\n<div onmouseover=\"alert(1)\">\nhover\n</div>\n\nOutro.",
	},
	{
		"javascript link",
		`[click me](javascript:alert(document.domain))`,
	},
}

var (
	tagRe        = regexp.MustCompile(`<([a-zA-Z][\w-]*)([^>]*)>`)
	eventAttrRe  = regexp.MustCompile(`(?i)\son\w+\s*=`)
	attrValueRe  = regexp.MustCompile(`"[^"]*"`)
	markdownTags = map[string]bool{
		"p": true, "a": true, "img": true, "strong": true, "em": true,
		"code": true, "pre": true, "ul": true, "ol": true, "li": true,
		"blockquote": true, "del": true, "br": true, "hr": true,
	}
)

func TestRenderMarkdownNeutralizesXSSProbes(t *testing.T) {
	md := newMarkdown("MIR")
	for _, tt := range xssProbes {
		t.Run(tt.name, func(t *testing.T) {
			out := string(renderMarkdown(md, tt.input))
			for _, m := range tagRe.FindAllStringSubmatch(out, -1) {
				tag, attrs := strings.ToLower(m[1]), m[2]
				if !markdownTags[tag] {
					t.Errorf("unexpected <%s> tag in output: %s", tag, out)
				}
				// Escaped text inside a quoted value (like alt) is inert, so
				// only look at attribute names.
				if eventAttrRe.MatchString(attrValueRe.ReplaceAllString(attrs, `""`)) {
					t.Errorf("event handler attribute on <%s>: %s", tag, out)
				}
				if strings.Contains(strings.ToLower(attrs), "javascript:") {
					t.Errorf("javascript: URL on <%s>: %s", tag, out)
				}
			}
		})
	}
}

func TestRenderMarkdownShowsRawHTMLAsText(t *testing.T) {
	md := newMarkdown("MIR")

	// MIR-939: placeholders in a pasted log used to vanish as unknown tags.
	out := string(renderMarkdown(md, "Deploying: <app> → club"))
	if !strings.Contains(out, "Deploying: &lt;app&gt; → club") {
		t.Errorf("placeholder not shown as text: %s", out)
	}

	out = string(renderMarkdown(md, "<details>\n<summary>more</summary>\n</details>"))
	if !strings.Contains(out, "&lt;details&gt;") || !strings.Contains(out, "&lt;/details&gt;") {
		t.Errorf("html block not shown as text: %s", out)
	}
}
