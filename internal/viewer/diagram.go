package viewer

import (
	"bytes"
	"encoding/json"
	"html"
	"io/fs"
	"net/http"
	"os"
	"slices"
	"strings"

	"github.com/kijk-dev/kijk/internal/git"
	"github.com/kijk-dev/kijk/internal/render"
)

const diagramTooLargeText = "Diagram too large to draw"

// diagramSide is one version of a changed diagram file.
type diagramSide struct {
	Text     string `json:"text,omitempty"`
	TooLarge bool   `json:"tooLarge,omitempty"`
}

type diagramSides struct {
	Type   string       `json:"type"` // the name of the file type
	Before *diagramSide `json:"before"`
	After  *diagramSide `json:"after"`
	Parent string       `json:"parent,omitempty"` // a commit's first parent, short, to name the before side
}

func diffDiagram(root string, t *render.FileType, path, orig, mode, hash string) *diagramSides {
	before, after, err := changeSides(root, path, orig, mode, hash)
	if err != nil {
		return nil
	}
	res := &diagramSides{Type: t.Name, Before: diagramText(root, t, before), After: diagramText(root, t, after)}
	if mode == "commit" && before != nil {
		res.Parent, _ = git.Run(root, "rev-parse", "--short", hash+"^")
	}
	return res
}

func diagramText(root string, t *render.FileType, b *blobRef) *diagramSide {
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
		if st.Size() > t.MaxBytes {
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
		if size > t.MaxBytes {
			return &diagramSide{TooLarge: true}
		}
		if src, err = git.Raw(root, "cat-file", "blob", b.oid); err != nil {
			return nil
		}
	}
	if t.TooLarge(src) {
		return &diagramSide{TooLarge: true}
	}
	return &diagramSide{Text: string(src)}
}

// frameCSP lets in Mermaid's inline styles and PlantUML's WebAssembly, which the page's CSP
// blocks. Its only network is kijk's own API, for PlantUML includes.
const frameCSP = "default-src 'none'; script-src 'self' 'wasm-unsafe-eval'; style-src 'unsafe-inline'; img-src data:; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'"

// serveFrame answers GET /diagram: the frame that draws diagrams, with each engine's label and
// the URLs of its files, and the files not in the cache yet, for which it says it is downloading.
func serveFrame(web fs.FS, c *engineCache) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		page, err := fs.ReadFile(web, "diagram.html")
		if err != nil {
			fail(w, err)
			return
		}
		type frameEngine struct {
			Label string            `json:"label"`
			Files map[string]string `json:"files"`
		}
		engines := map[string]frameEngine{}
		var missing []string
		for _, t := range render.FileTypes {
			if t.Engine == nil {
				continue
			}
			e := frameEngine{t.Engine.Label, map[string]string{}}
			for _, f := range t.Engine.Files {
				d := c.files[f.Name]
				e.Files[f.Name] = d.src()
				if !c.cached(d) {
					missing = append(missing, f.Name)
				}
			}
			engines[t.Name] = e
		}
		data, err := json.Marshal(engines)
		if err != nil {
			fail(w, err)
			return
		}
		slices.Sort(missing)
		page = bytes.ReplaceAll(page, []byte("{{engines}}"), []byte(html.EscapeString(string(data))))
		page = bytes.ReplaceAll(page, []byte("{{missing}}"), []byte(strings.Join(missing, " ")))
		w.Header().Set("Content-Security-Policy", frameCSP)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(page)
	}
}
