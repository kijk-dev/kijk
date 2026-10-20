package render

import (
	"strings"
	"testing"
)

func TestMermaidBlock(t *testing.T) {
	src := "# Plan\n\n```mermaid\ngraph TD\n  A[\"<script>x</script>\"] --> B\n```\n\n```go\nx\n```\n"
	out, _, err := Markdown([]byte(src), "", nil)
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
	out, _, err := Markdown([]byte(src), "", nil)
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
