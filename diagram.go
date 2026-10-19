package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/yuin/goldmark/util"
)

// diagramKinds are the diagram files, by extension. The page has the same list in view.js.
var diagramKinds = map[string]string{
	".mmd":     "mermaid",
	".mermaid": "mermaid",
}

// diagramKind returns the engine of a slash-separated path, or "" when it is not a diagram file.
func diagramKind(p string) string {
	return diagramKinds[strings.ToLower(path.Ext(p))]
}

// diagramMaxChars is Mermaid's maxTextSize: a longer diagram cannot be drawn.
const diagramMaxChars = 50000

// writeDiagram writes a diagram block as uncolored, escaped text: the page draws it. Markdown
// cannot set a class, so the page can trust the class.
func writeDiagram(w util.BufWriter, kind string, attrs func(), text []byte) {
	fmt.Fprintf(w, `<pre class="diagram %s"`, kind)
	attrs()
	w.WriteString("><code>")
	w.Write(util.EscapeHTML(text))
	w.WriteString("</code></pre>\n")
}

// renderDiagramFile renders a diagram file as one diagram block over all its lines, so change
// marks and live reload work as for markdown.
func renderDiagramFile(src []byte, kind string) string {
	lines := bytes.Count(src, []byte("\n"))
	if len(src) == 0 || src[len(src)-1] != '\n' {
		lines++
	}
	var out bytes.Buffer
	w := bufio.NewWriter(&out)
	writeDiagram(w, kind, func() { fmt.Fprintf(w, ` data-lines="1-%d"`, lines) }, bytes.TrimRight(src, "\r\n"))
	w.Flush()
	return out.String()
}

// diagramSide is one version of a changed diagram file.
type diagramSide struct {
	Text     string `json:"text,omitempty"`
	TooLarge bool   `json:"tooLarge,omitempty"`
}

type diagramSides struct {
	Kind   string       `json:"kind"`
	Before *diagramSide `json:"before"`
	After  *diagramSide `json:"after"`
	Parent string       `json:"parent,omitempty"` // a commit's first parent, short, to name the before side
}

func diffDiagram(root, kind, path, orig, mode, hash string) *diagramSides {
	before, after, err := changeSides(root, path, orig, mode, hash)
	if err != nil {
		return nil
	}
	res := &diagramSides{Kind: kind, Before: diagramText(root, before), After: diagramText(root, after)}
	if mode == "commit" && before != nil {
		res.Parent, _ = git(root, "rev-parse", "--short", hash+"^")
	}
	return res
}

func diagramText(root string, b *blobRef) *diagramSide {
	if b == nil {
		return nil
	}
	var src []byte
	if b.oid == "" {
		p, err := resolve(root, b.path)
		if err != nil {
			return nil
		}
		st, err := os.Stat(p)
		if err != nil || st.IsDir() {
			return nil
		}
		if st.Size() > 4*diagramMaxChars { // at most 4 bytes a character
			return &diagramSide{TooLarge: true}
		}
		if src, err = os.ReadFile(p); err != nil {
			return nil
		}
	} else {
		size, err := blobSize(root, b.oid)
		if err != nil {
			return nil
		}
		if size > 4*diagramMaxChars {
			return &diagramSide{TooLarge: true}
		}
		if src, err = gitRaw(root, "cat-file", "blob", b.oid); err != nil {
			return nil
		}
	}
	if utf8.RuneCount(src) > diagramMaxChars {
		return &diagramSide{TooLarge: true}
	}
	return &diagramSide{Text: string(src)}
}

// frameCSP lets Mermaid's inline styles in, which the page's CSP blocks, and runs only kijk's
// own scripts. The frame has no network: no connect-src, and images only as data: URLs.
const frameCSP = "default-src 'none'; script-src 'self'; style-src 'unsafe-inline'; img-src data:; base-uri 'none'; form-action 'none'; frame-ancestors 'self'"

// serveFrame answers GET /diagram: the frame that draws diagrams, with the engine URLs filled in.
func serveFrame(web fs.FS) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		page, err := fs.ReadFile(web, "diagram.html")
		if err != nil {
			fail(w, err)
			return
		}
		for name, e := range engines {
			page = bytes.ReplaceAll(page, []byte("{{"+name+"}}"), []byte(e.src()))
		}
		w.Header().Set("Content-Security-Policy", frameCSP)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(page)
	}
}
