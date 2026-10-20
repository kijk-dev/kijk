package render

import (
	"strings"
	"testing"
)

func TestRenderMarkdownLines(t *testing.T) {
	src := "# Title\n\nSome text\non two lines.\n\n- one\n- two\n  - nested\n\n```go\nfunc main() {}\n```\n\n| a | b |\n|---|:-:|\n| 1 | 2 |\n"
	out, _, err := Markdown([]byte(src), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`<h1 data-lines="1-2">Title</h1>`,
		`<p data-lines="3-5">Some text`,
		`<li data-lines="6-6">one</li>`,
		`<li data-lines="7-9">two`,
		`<li data-lines="8-9">nested</li>`,
		`<pre data-lines="10-13"><code><span class="hk">func</span>`,
		`<table data-lines="14-16">`,
		`<th align="center">b</th>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in\n%s", want, out)
		}
	}
	if strings.Contains(out, "<ul data-lines") {
		t.Errorf("lists are marked by their items, not as a whole:\n%s", out)
	}
}

func TestRenderMarkdownDropsHTML(t *testing.T) {
	src := "<script>alert(1)</script>\n\nHi <img src=x onerror=alert(1)> [x](javascript:alert(1)) [y](https://example.com)\n"
	out, _, err := Markdown([]byte(src), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"<script", "onerror", "javascript:"} {
		if strings.Contains(out, bad) {
			t.Errorf("%s kept in\n%s", bad, out)
		}
	}
	if !strings.Contains(out, `<a href="https://example.com">y</a>`) {
		t.Errorf("safe link lost:\n%s", out)
	}
}

func TestRenderMarkdownTaskList(t *testing.T) {
	out, _, err := Markdown([]byte("- [x] done\n- [ ] todo\n"), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(out, `type="checkbox"`) != 2 || !strings.Contains(out, "checked") {
		t.Errorf("task list:\n%s", out)
	}
}
