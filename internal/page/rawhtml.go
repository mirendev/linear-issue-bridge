package page

import (
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/util"
)

// Issue descriptions can come from anyone through /suggest, so raw HTML in
// them must never reach the page as markup. goldmark's safe mode would drop
// it as "<!-- raw HTML omitted -->", but people paste things like
// "Deploying: <app>" in logs and expect to see them. So we print raw HTML
// back out as the literal text it was written as.
type escapedHTMLRenderer struct{}

func (escapedHTMLRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindRawHTML, renderEscapedRawHTML)
	reg.Register(ast.KindHTMLBlock, renderEscapedHTMLBlock)
}

func renderEscapedRawHTML(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkSkipChildren, nil
	}
	segs := node.(*ast.RawHTML).Segments
	for i := 0; i < segs.Len(); i++ {
		seg := segs.At(i)
		_, _ = w.Write(util.EscapeHTML(seg.Value(source)))
	}
	return ast.WalkSkipChildren, nil
}

func renderEscapedHTMLBlock(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	n := node.(*ast.HTMLBlock)
	if entering {
		_, _ = w.WriteString("<p>")
		lines := n.Lines()
		for i := 0; i < lines.Len(); i++ {
			line := lines.At(i)
			_, _ = w.Write(util.EscapeHTML(line.Value(source)))
		}
		return ast.WalkContinue, nil
	}
	if n.HasClosure() {
		_, _ = w.Write(util.EscapeHTML(n.ClosureLine.Value(source)))
	}
	_, _ = w.WriteString("</p>\n")
	return ast.WalkContinue, nil
}
