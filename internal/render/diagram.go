package render

import (
	"bufio"
	"bytes"
	"fmt"

	"github.com/yuin/goldmark/util"
)

// writeDiagram writes a diagram block as uncolored, escaped text: the page draws it. Markdown
// cannot set a class, so the page can trust the class.
func writeDiagram(w util.BufWriter, name string, attrs func(), text []byte) {
	fmt.Fprintf(w, `<pre class="diagram %s"`, name)
	attrs()
	w.WriteString("><code>")
	w.Write(util.EscapeHTML(text))
	w.WriteString("</code></pre>\n")
}

// DiagramFile renders a diagram file as one diagram block over all its lines, so change
// marks and live reload work as for markdown.
func DiagramFile(src []byte, name string) string {
	lines := bytes.Count(src, []byte("\n"))
	if len(src) == 0 || src[len(src)-1] != '\n' {
		lines++
	}
	var out bytes.Buffer
	w := bufio.NewWriter(&out)
	writeDiagram(w, name, func() { fmt.Fprintf(w, ` data-lines="1-%d"`, lines) }, bytes.TrimRight(src, "\r\n"))
	w.Flush()
	return out.String()
}
