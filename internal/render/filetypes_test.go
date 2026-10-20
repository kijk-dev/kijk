package render

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/alecthomas/chroma/v2/lexers"
)

func TestTypeOf(t *testing.T) {
	typesAre(t, map[string]string{"a/flow.mmd": "mermaid", "FLOW.MMD": "mermaid", "x.Mermaid": "mermaid", "x.md": "markdown", "mmd": "",
		"docs/Login.PUML": "plantuml", "a.plantuml": "plantuml", "a.pu": "plantuml", "a.wsd": "plantuml", "common.iuml": "",
		"docs/Arch.EXCALIDRAW": "excalidraw", "shapes.excalidrawlib": "", "arch.excalidraw.svg": "svg", "Guide.ADOC": "asciidoc",
		"photo.JPeG": "jpeg", "a.jpg": "jpeg", "png": "", "notes.png.txt": "", "assets.png/data.go": "", ".png": "png"})
}

func TestTypeOfName(t *testing.T) {
	byName["Dockerfile"] = &FileType{Name: "dockerfile"}
	byExt[".dockerfile"] = &FileType{Name: "other"}
	t.Cleanup(func() {
		delete(byName, "Dockerfile")
		delete(byExt, ".dockerfile")
	})
	typesAre(t, map[string]string{"build/Dockerfile": "dockerfile", "dockerfile": "", "x.Dockerfile": "other"})
}

// typesAre checks the name of the type TypeOf gives each path; "" is no type.
func typesAre(t *testing.T, want map[string]string) {
	t.Helper()
	for p, w := range want {
		got := ""
		if ft := TypeOf(p); ft != nil {
			got = ft.Name
		}
		if got != w {
			t.Errorf("%s: %q, want %q", p, got, w)
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
		if ft.Lexer != "" {
			want, files := lexers.Get(ft.Lexer), slices.Clone(ft.Names)
			for _, e := range ft.Exts {
				files = append(files, "x"+e)
			}
			for _, f := range files {
				if l := LexerFor(f); want == nil || l == nil || l.Config().Name != want.Config().Name {
					t.Errorf("%s: the source of %s is not colored by chroma's lexer %q", ft.Name, f, ft.Lexer)
				}
			}
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

// testdata/filetypes holds one sample per file type, for the API and browser tests.
func TestEveryTypeHasASample(t *testing.T) {
	files, err := os.ReadDir("../../testdata/filetypes")
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]string{}
	for _, f := range files {
		ft := TypeOf(f.Name())
		switch {
		case ft == nil:
			t.Errorf("the sample %s has no file type", f.Name())
		case found[ft.Name] != "":
			t.Errorf("%s and %s are both samples of %s", found[ft.Name], f.Name(), ft.Name)
		default:
			found[ft.Name] = f.Name()
		}
	}
	for _, ft := range FileTypes {
		if found[ft.Name] == "" {
			t.Errorf("%s has no sample in testdata/filetypes", ft.Name)
		}
	}
}
