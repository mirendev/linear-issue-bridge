package page

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

// Linear embeds cross-references in issue descriptions as ordinary markdown
// links that point back at the authenticated Linear app. Those links are
// useless to the public:
//
//   - Issue refs like [MIR-1242](https://linear.app/ws/issue/MIR-1242/...)
//     require a Linear login. We have a public page for them already.
//   - PR refs like [org/repo#858](https://linear.app/ws/review/...) point at a
//     Linear review URL. The real GitHub coordinates only live in the link text.
//
// linkRewriter walks the parsed markdown and rewrites those destinations to
// public bridge / GitHub URLs.
var (
	linearIssueRe = regexp.MustCompile(`^https?://linear\.app/[^/]+/issue/([A-Za-z]+-\d+)`)
	ghRefRe       = regexp.MustCompile(`^([\w.-]+/[\w.-]+)#(\d+)$`)
)

type linkRewriter struct {
	teamKey string
}

func (lr *linkRewriter) Transform(doc *ast.Document, reader text.Reader, _ parser.Context) {
	source := reader.Source()
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		link, ok := n.(*ast.Link)
		if !ok {
			return ast.WalkContinue, nil
		}
		if newDest, ok := rewriteLink(string(link.Destination), linkText(link, source), lr.teamKey); ok {
			link.Destination = []byte(newDest)
		}
		return ast.WalkContinue, nil
	})
}

// rewriteLink returns the rewritten destination for a link, or ok=false to
// leave it unchanged. It's a pure function so the rules are easy to test.
func rewriteLink(dest, linkText, teamKey string) (string, bool) {
	// Rule: Linear issue links -> relative bridge page, but only for our team
	// (we don't serve pages for other teams/workspaces).
	if m := linearIssueRe.FindStringSubmatch(dest); m != nil {
		identifier := m[1]
		if teamKey != "" && strings.HasPrefix(identifier, teamKey+"-") {
			return "/" + identifier, true
		}
		return "", false
	}

	// Rule: GitHub PR refs that Linear points at its own review URL. The real
	// coordinates (org/repo#num) only exist in the link text. We always emit
	// /pull/N: GitHub shares one numbering space per repo and 302-redirects
	// /pull/N -> /issues/N (and back) server-side, so the link resolves even if
	// a ref ever turns out to be an issue.
	if strings.Contains(dest, "linear.app/") && strings.Contains(dest, "/review/") {
		if m := ghRefRe.FindStringSubmatch(strings.TrimSpace(linkText)); m != nil {
			return fmt.Sprintf("https://github.com/%s/pull/%s", m[1], m[2]), true
		}
	}

	return "", false
}

// linkText concatenates the rendered text content of a link node's children.
func linkText(n ast.Node, source []byte) string {
	var b strings.Builder
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		switch t := c.(type) {
		case *ast.Text:
			b.Write(t.Segment.Value(source))
		case *ast.String:
			b.Write(t.Value)
		default:
			b.WriteString(linkText(c, source))
		}
	}
	return b.String()
}
