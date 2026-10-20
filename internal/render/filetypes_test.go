package render

import (
	"strings"
	"testing"

	"github.com/alecthomas/chroma/v2/lexers"
)

func TestTypeOf(t *testing.T) {
	for p, want := range map[string]string{"a/flow.mmd": "mermaid", "FLOW.MMD": "mermaid", "x.Mermaid": "mermaid", "x.md": "markdown", "mmd": "",
		"docs/Login.PUML": "plantuml", "a.plantuml": "plantuml", "a.pu": "plantuml", "a.wsd": "plantuml", "common.iuml": "",
		"docs/Arch.EXCALIDRAW": "excalidraw", "shapes.excalidrawlib": "", "arch.excalidraw.svg": "svg", "Guide.ADOC": "asciidoc",
		"photo.JPeG": "jpeg", "a.jpg": "jpeg", "png": "", "notes.png.txt": "", "assets.png/data.go": "", ".png": "png"} {
		got := ""
		if ft := TypeOf(p); ft != nil {
			got = ft.Name
		}
		if got != want {
			t.Errorf("%s: %q, want %q", p, got, want)
		}
	}
}

func TestTypeOfName(t *testing.T) {
	byName["Dockerfile"] = &FileType{Name: "dockerfile"}
	byExt[".dockerfile"] = &FileType{Name: "other"}
	t.Cleanup(func() {
		delete(byName, "Dockerfile")
		delete(byExt, ".dockerfile")
	})
	for p, want := range map[string]string{"build/Dockerfile": "dockerfile", "dockerfile": "", "x.Dockerfile": "other"} {
		got := ""
		if ft := TypeOf(p); ft != nil {
			got = ft.Name
		}
		if got != want {
			t.Errorf("%s: %q, want %q", p, got, want)
		}
	}
}

// The page and the engines rely on these; a new entry that breaks one fails here.
func TestFileTypeList(t *testing.T) {
	names, files := map[string]bool{}, map[string]bool{}
	for _, ft := range FileTypes {
		if ft.Name == "" || names[ft.Name] || strings.ToLower(ft.Name) != ft.Name {
			t.Errorf("%q: names are lower case and unique", ft.Name)
		}
		names[ft.Name] = true
		if len(ft.Exts)+len(ft.Names) == 0 {
			t.Errorf("%s: no extension or file name", ft.Name)
		}
		for _, e := range ft.Exts {
			if e != strings.ToLower(e) || !strings.HasPrefix(e, ".") || byExt[e].Name != ft.Name {
				t.Errorf("%s: extension %q is not lower case with a dot, or another type has it", ft.Name, e)
			}
		}
		for _, f := range ft.Fences {
			if FenceType(f).Name != ft.Name {
				t.Errorf("%s: another type has the fence %q", ft.Name, f)
			}
		}
		if ft.Lexer != "" && lexers.Get(ft.Lexer) == nil {
			t.Errorf("%s: chroma has no lexer %q", ft.Name, ft.Lexer)
		}
		if ft.Diff != DiffLines && ft.Diff != DiffPictures {
			t.Errorf("%s: diff %q", ft.Name, ft.Diff)
		}
		if (ft.MIME != "") != (ft.Show == ShowImage) || (ft.Render != nil) != (ft.Show == ShowRendered) ||
			(ft.Engine != nil) != (ft.Show == ShowDrawn) || (len(ft.Fences) > 0 && ft.Show != ShowDrawn) {
			t.Errorf("%s: an image has a MIME type, a rendered type a Render and a drawn type an engine; only drawn types have fences", ft.Name)
		}
		if (ft.Show == ShowRendered || ft.Show == ShowDrawn) && ft.MaxBytes <= 0 {
			t.Errorf("%s: no MaxBytes", ft.Name)
		}
		if ft.Engine == nil {
			continue
		}
		if ft.Engine.Label == "" || len(ft.Engine.Files) == 0 {
			t.Errorf("%s: an engine has a label and files", ft.Name)
		}
		for _, f := range ft.Engine.Files {
			if f.Name == "" || files[f.Name] || f.Version == "" || !strings.HasPrefix(f.URL, "https://") || len(f.SHA256) != 64 {
				t.Errorf("%s: engine file %+v", ft.Name, f)
			}
			files[f.Name] = true
		}
	}
}
