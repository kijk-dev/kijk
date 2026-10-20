package render

import (
	"bufio"
	"bytes"
	"fmt"
	"path"
	"strings"

	"github.com/yuin/goldmark/util"
)

// diagramKinds are the diagram files, by extension. The page has the same list in view.js.
var diagramKinds = map[string]string{
	".mmd":        "mermaid",
	".mermaid":    "mermaid",
	".puml":       "plantuml",
	".plantuml":   "plantuml",
	".pu":         "plantuml",
	".wsd":        "plantuml",
	".excalidraw": "excalidraw",
}

// diagramFences are the languages of fenced blocks that hold a diagram, and its kind.
var diagramFences = map[string]string{
	"mermaid":  "mermaid",
	"plantuml": "plantuml",
	"puml":     "plantuml",
}

// DiagramKind returns the engine of a slash-separated path, or "" when it is not a diagram file.
func DiagramKind(p string) string {
	return diagramKinds[strings.ToLower(path.Ext(p))]
}

// writeDiagram writes a diagram block as uncolored, escaped text: the page draws it. Markdown
// cannot set a class, so the page can trust the class.
func writeDiagram(w util.BufWriter, kind string, attrs func(), text []byte) {
	fmt.Fprintf(w, `<pre class="diagram %s"`, kind)
	attrs()
	w.WriteString("><code>")
	w.Write(util.EscapeHTML(text))
	w.WriteString("</code></pre>\n")
}

// DiagramFile renders a diagram file as one diagram block over all its lines, so change
// marks and live reload work as for markdown.
func DiagramFile(src []byte, kind string) string {
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
