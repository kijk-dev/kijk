package render

import (
	"strings"
	"testing"
)

func TestMermaidBlock(t *testing.T) {
	src := "# Plan\n\n```mermaid\ngraph TD\n  A[\"<script>x</script>\"] --> B\n```\n\n```go\nx\n```\n"
	out, err := Markdown([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	want := `<pre class="diagram mermaid" data-lines="3-7"><code>graph TD` + "\n" + `  A[&quot;&lt;script&gt;x&lt;/script&gt;&quot;] --&gt; B</code></pre>`
	if !strings.Contains(out, want) {
		t.Errorf("missing\n%s\nin\n%s", want, out)
	}
	if strings.Contains(out, "<script") || strings.Count(out, "diagram") != 1 {
		t.Errorf("out:\n%s", out)
	}
}

func TestPlantUMLBlock(t *testing.T) {
	src := "```puml\nAlice -> Bob\n```\n\n```plantuml\n@startuml\nA -> B\n@enduml\n```\n\n```graphviz\ndigraph { a -> b }\n```\n"
	out, err := Markdown([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`<pre class="diagram plantuml" data-lines="1-4"><code>Alice -&gt; Bob</code></pre>`,
		`<pre class="diagram plantuml" data-lines="5-10"><code>@startuml`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing\n%s\nin\n%s", want, out)
		}
	}
	if strings.Count(out, "diagram") != 2 || !strings.Contains(out, "<pre data-lines=\"11-13\"><code>") {
		t.Errorf("a graphviz block is not colored code:\n%s", out)
	}
}

func TestDiagramKind(t *testing.T) {
	for p, want := range map[string]string{"a/flow.mmd": "mermaid", "FLOW.MMD": "mermaid", "x.Mermaid": "mermaid", "x.md": "", "mmd": "",
		"docs/Login.PUML": "plantuml", "a.plantuml": "plantuml", "a.pu": "plantuml", "a.wsd": "plantuml", "common.iuml": "",
		"docs/Arch.EXCALIDRAW": "excalidraw", "shapes.excalidrawlib": "", "arch.excalidraw.svg": ""} {
		if got := DiagramKind(p); got != want {
			t.Errorf("%s: %q", p, got)
		}
	}
}
