package main

import (
	"bytes"
	"io/fs"
	"net/http"
	"os"
	"slices"
	"strings"
	"unicode/utf8"

	kgit "github.com/kijk-dev/kijk/internal/git"
)

// diagramMaxChars is Mermaid's maxTextSize; diagram.js holds it too, for PlantUML.
const diagramMaxChars = 50000

// excalidrawMaxBytes is larger, as pasted images live inside the drawing. diagram.js holds it too.
const excalidrawMaxBytes = 8 << 20

const diagramTooLargeText = "Diagram too large to draw"

// diagramMaxBytes is the most a diagram's text can take, so a larger file need not be read.
func diagramMaxBytes(kind string) int64 {
	if kind == "excalidraw" {
		return excalidrawMaxBytes
	}
	return 4 * diagramMaxChars // at most 4 bytes a character
}

func diagramTooLarge(kind string, src []byte) bool {
	if kind == "excalidraw" {
		return len(src) > excalidrawMaxBytes
	}
	return utf8.RuneCount(src) > diagramMaxChars
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
	res := &diagramSides{Kind: kind, Before: diagramText(root, kind, before), After: diagramText(root, kind, after)}
	if mode == "commit" && before != nil {
		res.Parent, _ = kgit.Run(root, "rev-parse", "--short", hash+"^")
	}
	return res
}

func diagramText(root, kind string, b *blobRef) *diagramSide {
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
		if st.Size() > diagramMaxBytes(kind) {
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
		if size > diagramMaxBytes(kind) {
			return &diagramSide{TooLarge: true}
		}
		if src, err = kgit.Raw(root, "cat-file", "blob", b.oid); err != nil {
			return nil
		}
	}
	if diagramTooLarge(kind, src) {
		return &diagramSide{TooLarge: true}
	}
	return &diagramSide{Text: string(src)}
}

// frameCSP lets in Mermaid's inline styles and PlantUML's WebAssembly, which the page's CSP
// blocks. Its only network is kijk's own API, for PlantUML includes.
const frameCSP = "default-src 'none'; script-src 'self' 'wasm-unsafe-eval'; style-src 'unsafe-inline'; img-src data:; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'"

// serveFrame answers GET /diagram: the frame that draws diagrams, with the engine URLs filled in
// and the engines not in the cache yet, for which it says it is downloading.
func serveFrame(web fs.FS) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		page, err := fs.ReadFile(web, "diagram.html")
		if err != nil {
			fail(w, err)
			return
		}
		var missing []string
		for name, e := range engines {
			page = bytes.ReplaceAll(page, []byte("{{"+name+"}}"), []byte(e.src()))
			if !engineCached(e) {
				missing = append(missing, name)
			}
		}
		slices.Sort(missing)
		page = bytes.ReplaceAll(page, []byte("{{missing}}"), []byte(strings.Join(missing, " ")))
		w.Header().Set("Content-Security-Policy", frameCSP)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(page)
	}
}
