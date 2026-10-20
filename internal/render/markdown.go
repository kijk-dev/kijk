// Package render turns text into HTML: markdown, AsciiDoc, colored code and diagram blocks.
package render

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

const MaxSourceBytes = 2 << 20 // bigger markdown and AsciiDoc files are shown as source only

// HTML inside markdown is dropped and dangerous link schemes are removed: goldmark's defaults
// without html.WithUnsafe. A rendered file could otherwise run script with kijk's cookie.
var md = goldmark.New(
	goldmark.WithExtensions(
		extension.NewTable(extension.WithTableCellAlignMethod(extension.TableCellAlignAttribute)), // CSP blocks style attributes
		extension.Strikethrough,
		extension.Linkify,
		extension.TaskList,
	),
	goldmark.WithRendererOptions(renderer.WithNodeRenderers(util.Prioritized(codeRenderer{}, 100))),
)

// Markdown returns the HTML of a markdown file. Each top-level block and list item carries
// data-lines="first-last", the 1-based source lines it covers, for change marks and switching views.
func Markdown(src []byte) (string, error) {
	doc := md.Parser().Parse(text.NewReader(src))
	starts := []int{0}
	for i, b := range src {
		if b == '\n' && i+1 < len(src) {
			starts = append(starts, i+1)
		}
	}
	lineOf := func(off int) int { return sort.SearchInts(starts, off+1) }
	markLines(doc, len(starts), true, lineOf)
	var out bytes.Buffer
	if err := md.Renderer().Render(&out, src, doc); err != nil {
		return "", err
	}
	return out.String(), nil
}

// markLines marks top-level blocks and list items with the lines of lineRanges.
func markLines(parent ast.Node, end int, top bool, lineOf func(int) int) {
	var kids []ast.Node
	var firsts []int
	for n := parent.FirstChild(); n != nil; n = n.NextSibling() {
		if n.Type() == ast.TypeBlock && n.Pos() >= 0 {
			kids = append(kids, n)
			firsts = append(firsts, lineOf(n.Pos()))
		}
	}
	for i, last := range lineRanges(firsts, end) {
		n, kind := kids[i], kids[i].Kind()
		if (top && kind != ast.KindList) || kind == ast.KindListItem {
			n.SetAttributeString("data-lines", fmt.Sprintf("%d-%d", firsts[i], last))
		}
		if kind == ast.KindList || kind == ast.KindListItem || kind == ast.KindBlockquote {
			markLines(n, last, false, lineOf)
		}
	}
}

// lineRanges gives each block the lines up to the next block, so blank lines and deletions
// between blocks still belong to one of them. It returns the last line of each block.
func lineRanges(firsts []int, end int) []int {
	lasts := make([]int, len(firsts))
	for i, first := range firsts {
		lasts[i] = end
		if i+1 < len(firsts) {
			lasts[i] = max(first, firsts[i+1]-1)
		}
	}
	return lasts
}

// codeRenderer colors code blocks with chroma, in the same classes as the file view, and marks
// diagram blocks for the page to draw.
type codeRenderer struct{}

func (codeRenderer) RegisterFuncs(r renderer.NodeRendererFuncRegisterer) {
	r.Register(ast.KindFencedCodeBlock, renderCode)
	r.Register(ast.KindCodeBlock, renderCode)
}

func renderCode(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	lines := make([]string, n.Lines().Len())
	for i := range lines {
		seg := n.Lines().At(i)
		lines[i] = strings.Repeat(" ", seg.Padding) + strings.TrimRight(string(seg.Value(src)), "\r\n")
	}
	lang := ""
	if f, ok := n.(*ast.FencedCodeBlock); ok {
		if l := f.Language(src); l != nil {
			lang = string(l)
		}
	}
	writeCode(w, lang, lines, func() { html.RenderAttributes(w, n, nil) })
	return ast.WalkSkipChildren, nil
}

// writeCode writes a diagram block when lang names a diagram, else the lines colored for lang.
func writeCode(w util.BufWriter, lang string, lines []string, attrs func()) {
	if kind, ok := diagramFences[lang]; ok {
		writeDiagram(w, kind, attrs, []byte(strings.Join(lines, "\n")))
		return
	}
	writeColored(w, lang, lines, attrs)
}

// writeColored writes a pre block colored with chroma, in the same classes as the file view.
func writeColored(w util.BufWriter, lang string, lines []string, attrs func()) {
	var lexer chroma.Lexer
	if lang != "" {
		if l := lexers.Get(lang); l != nil {
			lexer = chroma.Coalesce(l)
		}
	}
	w.WriteString("<pre")
	attrs()
	w.WriteString("><code>")
	for i, toks := range Highlight(lexer, lines) {
		if i > 0 {
			w.WriteByte('\n')
		}
		for _, t := range toks {
			if t[0] != "" {
				fmt.Fprintf(w, `<span class="h%s">`, t[0])
			}
			w.Write(util.EscapeHTML([]byte(t[1])))
			if t[0] != "" {
				w.WriteString("</span>")
			}
		}
	}
	w.WriteString("</code></pre>\n")
}
