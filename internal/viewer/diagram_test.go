package viewer

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kijk-dev/kijk/internal/gittest"
	"github.com/kijk-dev/kijk/internal/render"
)

func TestDiagramFileRender(t *testing.T) {
	dir := t.TempDir()
	gittest.Write(t, filepath.Join(dir, "docs", "FLOW.MMD"), "graph TD\nA-->B\n")
	gittest.Write(t, filepath.Join(dir, "big.mmd"), "graph TD\n"+strings.Repeat("é", diagramMaxChars))
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

// A pasted image makes a drawing megabytes, larger than any markdown file kijk renders.
func TestExcalidrawFileSize(t *testing.T) {
	dir := t.TempDir()
	drawing := func(n int) string { return `{"type":"excalidraw","files":{"a":"` + strings.Repeat("x", n) + `"}}` }
	gittest.Write(t, filepath.Join(dir, "shot.excalidraw"), drawing(3<<20))
	gittest.Write(t, filepath.Join(dir, "huge.excalidraw"), drawing(excalidrawMaxBytes))
	get := imageServer(t, dir)
	if w := get("/api/markdown", url.Values{"path": {"shot.excalidraw"}}); w.Code != http.StatusOK || !strings.HasPrefix(w.Body.String(), `{"html":"\u003cpre class=\"diagram excalidraw\" data-lines=\"1-1\"`) {
		t.Errorf("3 MB: %d %.80q", w.Code, w.Body.String())
	}
	if w := get("/api/markdown", url.Values{"path": {"huge.excalidraw"}}); w.Code != http.StatusRequestEntityTooLarge || strings.TrimSpace(w.Body.String()) != "Diagram too large to draw" {
		t.Errorf("over 8 MB: %d %q", w.Code, w.Body.String())
	}
}

func TestDiagramDiff(t *testing.T) {
	gittest.Isolate(t)
	repo := gittest.Repo(t)
	gittest.Write(t, filepath.Join(repo, "flow.mmd"), "A1")
	gittest.Write(t, filepath.Join(repo, "gone.mmd"), "G1")
	gittest.Run(t, repo, "add", ".")
	gittest.Run(t, repo, "commit", "-qm", "first")
	gittest.Write(t, filepath.Join(repo, "flow.mmd"), "A2")
	gittest.Run(t, repo, "add", "flow.mmd")
	gittest.Run(t, repo, "rm", "-q", "gone.mmd")
	gittest.Run(t, repo, "commit", "-qm", "second")
	hash := strings.TrimSpace(string(must(exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output())))
	gittest.Write(t, filepath.Join(repo, "flow.mmd"), "A3")
	gittest.Run(t, repo, "add", "flow.mmd")
	gittest.Write(t, filepath.Join(repo, "flow.mmd"), "A4")
	gittest.Write(t, filepath.Join(repo, "new.mmd"), "N1")
	gittest.Write(t, filepath.Join(repo, "big.mmd"), strings.Repeat("x", diagramMaxChars+1))
	gittest.Write(t, filepath.Join(repo, "shot.excalidraw"), strings.Repeat("x", diagramMaxChars+1))
	gittest.Write(t, filepath.Join(repo, "huge.excalidraw"), strings.Repeat("x", excalidrawMaxBytes+1))

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
		{"shot.excalidraw", "untracked", "", "none | " + strings.Repeat("x", diagramMaxChars+1)},
		{"huge.excalidraw", "untracked", "", "none | too large"},
		{"flow.mmd", "commit", hash, "A1 | A2"},
		{"gone.mmd", "commit", hash, "G1 | none"},
	} {
		w := get("/api/diff", url.Values{"path": {c.path}, "mode": {c.mode}, "hash": {c.hash}})
		var res diffReply
		if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil || res.Diagram == nil {
			t.Errorf("%s %s: %d %s", c.path, c.mode, w.Code, w.Body.String())
			continue
		}
		if got := side(res.Diagram.Before) + " | " + side(res.Diagram.After); got != c.want || res.Diagram.Kind != render.DiagramKind(c.path) || len(res.Hunks) == 0 {
			t.Errorf("%s %s: %s, %d hunks, want %s", c.path, c.mode, got, len(res.Hunks), c.want)
		}
	}
}

func TestFileText(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	gittest.Write(t, filepath.Join(dir, "docs", "styles.puml"), "skinparam x y\n")
	gittest.Write(t, filepath.Join(dir, "logo.png"), "PNG\x00\x01")
	gittest.Write(t, filepath.Join(dir, "big.puml"), strings.Repeat("x", textMaxBytes+1))
	gittest.Write(t, filepath.Join(outside, "secret"), "key")
	get := imageServer(t, dir)
	w := get("/api/text", url.Values{"path": {"docs/styles.puml"}})
	var res struct{ Text string }
	if json.Unmarshal(w.Body.Bytes(), &res); w.Code != http.StatusOK || res.Text != "skinparam x y\n" {
		t.Errorf("text file: %d %q", w.Code, w.Body.String())
	}
	rel, _ := filepath.Rel(dir, filepath.Join(outside, "secret"))
	for _, c := range []struct {
		path string
		code int
	}{
		{filepath.ToSlash(rel), http.StatusForbidden},
		{"logo.png", http.StatusUnsupportedMediaType},
		{"big.puml", http.StatusRequestEntityTooLarge},
		{"docs", http.StatusNotFound},
		{"nope.puml", http.StatusNotFound},
	} {
		if w := get("/api/text", url.Values{"path": {c.path}}); w.Code != c.code || strings.Contains(w.Body.String(), "key") {
			t.Errorf("%s: %d %q, want %d", c.path, w.Code, w.Body.String(), c.code)
		}
	}
}
