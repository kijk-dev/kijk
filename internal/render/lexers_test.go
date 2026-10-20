package render

import (
	"strings"
	"testing"

	"github.com/alecthomas/chroma/v2/lexers"
)

// Every file type of today has colors. TestSamples and the browser journey check a source as
// colored only when kijk has a lexer for it, so this keeps them checking today's types as colored.
func TestLexersFound(t *testing.T) {
	for file, want := range map[string]string{"docs/guide.adoc": "AsciiDoc", "README.ASCIIDOC": "AsciiDoc",
		"login.puml": "PlantUML", "a.plantuml": "PlantUML", "a.pu": "PlantUML", "a.wsd": "PlantUML", "docs/common.iuml": "PlantUML",
		"flow.mmd": "Mermaid", "flow.mermaid": "Mermaid", "docs/arch.excalidraw": "JSON", "README.md": "markdown", "a.markdown": "markdown",
		"logo.svg": "XML"} {
		if l := LexerFor(strings.ToLower(file)); l == nil || l.Config().Name != want {
			t.Errorf("%s: %v", file, l)
		}
	}
	for name, want := range map[string]string{"asciidoc": "AsciiDoc", "adoc": "AsciiDoc", "plantuml": "PlantUML", "puml": "PlantUML", "mermaid": "Mermaid"} {
		if l := lexers.Get(name); l == nil || l.Config().Name != want {
			t.Errorf("%s: %v", name, l)
		}
	}
}

// lexed colors src as the file view does and returns the class of each piece of text.
func lexed(t *testing.T, file, src string) map[string]string {
	t.Helper()
	got := map[string]string{}
	lines := strings.Split(src, "\n")
	for i, toks := range Highlight(LexerFor(file), lines) {
		if joined(toks) != lines[i] {
			t.Fatalf("%s:%d: got %q, want %q", file, i+1, joined(toks), lines[i])
		}
		for _, tk := range toks {
			got[strings.TrimSpace(tk[1])] = tk[0]
		}
	}
	return got
}

func TestLexerClasses(t *testing.T) {
	for _, c := range []struct {
		file, src string
		want      map[string]string
	}{
		{"guide.adoc", "= Guide\n:toc: left\n\n== Usage\n\n// note to self\n.Main code\n[source,go]\n----\nx\n----\n" +
			"WARNING: Back up.\nSee <<_usage>> and link:x.pdf[x], image::logo.png[].\nifdef::env-github[]\n////\nhidden\n////\n",
			map[string]string{"= Guide": "k", ":toc:": "a", "left": "s", "== Usage": "k", "// note to self": "c", ".Main code": "k",
				"[source,go]": "a", "----": "t", "WARNING:": "b", "<<_usage>>": "f", "link:": "f", "image::": "f",
				"ifdef::env-github[]": "f", "////": "c", "hidden": "c"}},
		{"login.puml", "@startuml\n' a comment\n!include common.iuml\nparticipant Alice #LightBlue\nAlice -> Bob: \"hi\"\nBob --> Alice\n/' block\ncomment '/\n@enduml",
			map[string]string{"@startuml": "d", "' a comment": "c", "!include": "b", "participant": "k", "#LightBlue": "n",
				"->": "f", `"hi"`: "s", "-->": "f", "/' block": "c", "comment '/": "c", "@enduml": "d", "Alice": ""}},
		{"flow.mmd", "%% a comment\nflowchart LR\n  A[\"Start\"] --> B\n  subgraph one\n  B -.-> C\n  end\nsequenceDiagram\n  A->>B: hi",
			map[string]string{"%% a comment": "c", "flowchart": "t", "LR": "b", `"Start"`: "s", "-->": "f", "subgraph": "k",
				"-.->": "f", "end": "k", "sequenceDiagram": "t", "->>": "f"}},
		{"README.md", "# Title\n\n## Part\n", map[string]string{"# Title": "k", "## Part": "k"}},
	} {
		got := lexed(t, c.file, c.src)
		for text, want := range c.want {
			if got[text] != want {
				t.Errorf("%s: %q has class %q, want %q (all: %v)", c.file, text, got[text], want, got)
			}
		}
	}
}
