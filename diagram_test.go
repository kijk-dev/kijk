package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMermaidBlock(t *testing.T) {
	src := "# Plan\n\n```mermaid\ngraph TD\n  A[\"<script>x</script>\"] --> B\n```\n\n```go\nx\n```\n"
	out, err := renderMarkdown([]byte(src))
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

func TestDiagramKind(t *testing.T) {
	for p, want := range map[string]string{"a/flow.mmd": "mermaid", "FLOW.MMD": "mermaid", "x.Mermaid": "mermaid", "x.md": "", "mmd": ""} {
		if got := diagramKind(p); got != want {
			t.Errorf("%s: %q", p, got)
		}
	}
}

func TestDiagramFileRender(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "docs", "FLOW.MMD"), "graph TD\nA-->B\n")
	write(t, filepath.Join(dir, "big.mmd"), "graph TD\n"+strings.Repeat("é", diagramMaxChars))
	get := imageServer(t, dir)
	w := get("/api/markdown", url.Values{"path": {"docs/FLOW.MMD"}})
	var res struct{ HTML string }
	json.Unmarshal(w.Body.Bytes(), &res)
	if w.Code != http.StatusOK || res.HTML != "<pre class=\"diagram mermaid\" data-lines=\"1-2\"><code>graph TD\nA--&gt;B</code></pre>\n" {
		t.Errorf("%d %q", w.Code, res.HTML)
	}
	if w := get("/api/markdown", url.Values{"path": {"big.mmd"}}); w.Code != http.StatusRequestEntityTooLarge || strings.TrimSpace(w.Body.String()) != "Diagram too large to draw" {
		t.Errorf("too large: %d %q", w.Code, w.Body.String())
	}
}

func TestDiagramDiff(t *testing.T) {
	isolatedGit(t)
	repo := gitRepo(t)
	write(t, filepath.Join(repo, "flow.mmd"), "A1")
	write(t, filepath.Join(repo, "gone.mmd"), "G1")
	run(t, repo, "add", ".")
	run(t, repo, "commit", "-qm", "first")
	write(t, filepath.Join(repo, "flow.mmd"), "A2")
	run(t, repo, "add", "flow.mmd")
	run(t, repo, "rm", "-q", "gone.mmd")
	run(t, repo, "commit", "-qm", "second")
	hash := strings.TrimSpace(string(must(exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output())))
	write(t, filepath.Join(repo, "flow.mmd"), "A3")
	run(t, repo, "add", "flow.mmd")
	write(t, filepath.Join(repo, "flow.mmd"), "A4")
	write(t, filepath.Join(repo, "new.mmd"), "N1")
	write(t, filepath.Join(repo, "big.mmd"), strings.Repeat("x", diagramMaxChars+1))

	get := imageServer(t, repo)
	side := func(s *diagramSide) string {
		switch {
		case s == nil:
			return "none"
		case s.TooLarge:
			return "too large"
		}
		return s.Text
	}
	for _, c := range []struct{ path, mode, hash, want string }{
		{"flow.mmd", "unstaged", "", "A3 | A4"},
		{"flow.mmd", "staged", "", "A2 | A3"},
		{"new.mmd", "untracked", "", "none | N1"},
		{"big.mmd", "untracked", "", "none | too large"},
		{"flow.mmd", "commit", hash, "A1 | A2"},
		{"gone.mmd", "commit", hash, "G1 | none"},
	} {
		w := get("/api/diff", url.Values{"path": {c.path}, "mode": {c.mode}, "hash": {c.hash}})
		var res diffResult
		if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil || res.Diagram == nil {
			t.Errorf("%s %s: %d %s", c.path, c.mode, w.Code, w.Body.String())
			continue
		}
		if got := side(res.Diagram.Before) + " | " + side(res.Diagram.After); got != c.want || res.Diagram.Kind != "mermaid" || len(res.Hunks) == 0 {
			t.Errorf("%s %s: %s, %d hunks, want %s", c.path, c.mode, got, len(res.Hunks), c.want)
		}
	}
}
