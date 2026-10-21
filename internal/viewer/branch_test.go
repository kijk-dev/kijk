package viewer

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/kijk-dev/kijk/internal/gittest"
)

// branchRepo is a clone on branch feature: one commit pushed, one not, an edit and a new file.
func branchRepo(t *testing.T) string {
	t.Helper()
	gittest.Isolate(t)
	bare := filepath.Join(t.TempDir(), "origin.git")
	gittest.Run(t, t.TempDir(), "init", "-q", "--bare", "-b", "main", bare)
	repo := gittest.Repo(t)
	gittest.Write(t, filepath.Join(repo, "a.go"), "a1\na2\na3\n")
	gittest.Run(t, repo, "add", ".")
	gittest.Run(t, repo, "commit", "-qm", "init")
	gittest.Run(t, repo, "remote", "add", "origin", bare)
	gittest.Run(t, repo, "push", "-q", "-u", "origin", "main")
	gittest.Run(t, repo, "checkout", "-qb", "feature")
	gittest.Write(t, filepath.Join(repo, "a.go"), "a1\npushed\na2\na3\n")
	gittest.Run(t, repo, "commit", "-qam", "pushed")
	gittest.Run(t, repo, "push", "-q", "-u", "origin", "feature")
	gittest.Write(t, filepath.Join(repo, "a.go"), "a1\npushed\na2\nlocal\na3\n")
	gittest.Run(t, repo, "commit", "-qam", "not pushed")
	gittest.Write(t, filepath.Join(repo, "a.go"), "a1\npushed\na2\nlocal\na3\nedit\n")
	gittest.Write(t, filepath.Join(repo, "new.go"), "n1\nn2\n")
	return repo
}

func TestBranchRoute(t *testing.T) {
	repo := branchRepo(t)
	get := imageServer(t, repo)
	w := get("/api/branch", url.Values{})
	var b struct {
		Branch, Base, Split, Upstream string
		Ahead, Uncommitted            int
		Files                         []struct {
			Path, Status string
			Add, Del     int
			NotPushed    bool
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &b); w.Code != http.StatusOK || err != nil {
		t.Fatalf("%d %v %s", w.Code, err, w.Body.String())
	}
	got := fmt.Sprintf("%s %s %s ahead %d uncommitted %d %+v", b.Branch, b.Base, b.Upstream, b.Ahead, b.Uncommitted, b.Files)
	if want := "feature origin/main origin/feature ahead 1 uncommitted 2 [{Path:a.go Status:M Add:3 Del:0 NotPushed:false} {Path:new.go Status:? Add:2 Del:0 NotPushed:true}]"; got != want || b.Split == "" {
		t.Fatalf("got  %s\nwant %s", got, want)
	}

	gittest.Run(t, repo, "checkout", "-qf", "main")
	if w := get("/api/branch", url.Values{}); w.Code != http.StatusNotFound {
		t.Fatalf("on main: %d %s", w.Code, w.Body.String())
	}
	if w := get("/api/diff", url.Values{"path": {"a.go"}, "mode": {"branch"}}); w.Code != http.StatusNotFound {
		t.Fatalf("a branch diff on main: %d %s", w.Code, w.Body.String())
	}
}

func TestBranchDiffRoute(t *testing.T) {
	repo := branchRepo(t)
	get := imageServer(t, repo)
	type reply struct {
		Hunks []struct {
			Lines []struct {
				K string
				N int
				T string
			}
		}
		NotPushed [][2]int
	}
	diff := func(path string) (string, string) {
		t.Helper()
		w := get("/api/diff", url.Values{"path": {path}, "mode": {"branch"}, "context": {"0"}})
		var r reply
		if err := json.Unmarshal(w.Body.Bytes(), &r); w.Code != http.StatusOK || err != nil {
			t.Fatalf("%s: %d %v %s", path, w.Code, err, w.Body.String())
		}
		var lines string
		for _, h := range r.Hunks {
			for _, l := range h.Lines {
				lines += fmt.Sprintf("%s%d %s|", l.K, l.N, l.T)
			}
		}
		return lines, fmt.Sprint(r.NotPushed)
	}
	if lines, np := diff("a.go"); lines != "+2 pushed|+4 local|+6 edit|" || np != "[[4 4] [6 6]]" {
		t.Fatalf("a.go: %q, not pushed %s", lines, np)
	}
	if lines, np := diff("new.go"); lines != "+1 n1|+2 n2|" || np != "[[1 2]]" {
		t.Fatalf("the untracked new.go: %q, not pushed %s", lines, np)
	}
}
