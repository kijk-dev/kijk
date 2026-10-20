package git

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kijk-dev/kijk/internal/gittest"
)

func counts(cs []change) string {
	var s []string
	for _, c := range cs {
		s = append(s, fmt.Sprintf("%s +%d -%d", c.Path, c.Add, c.Del))
	}
	return strings.Join(s, ", ")
}

func TestStatusCounts(t *testing.T) {
	gittest.Isolate(t)
	repo := gittest.Repo(t)
	gittest.Write(t, filepath.Join(repo, "a.go"), "1\n2\n3\n4\n5\n")
	gittest.Write(t, filepath.Join(repo, "old.go"), "x\ny\n")
	gittest.Write(t, filepath.Join(repo, "logo.png"), "\x89PNG\x00\x01")
	gittest.Run(t, repo, "add", ".")
	gittest.Run(t, repo, "commit", "-qm", "init")

	gittest.Write(t, filepath.Join(repo, "a.go"), "1\n2\nthree\n4\n5\nsix\nseven\n")
	gittest.Run(t, repo, "add", "a.go")
	gittest.Write(t, filepath.Join(repo, "a.go"), "1\n2\nthree\n4\n5\nsix\nseven\neight\n")
	gittest.Run(t, repo, "mv", "old.go", "new.go")
	gittest.Write(t, filepath.Join(repo, "logo.png"), "\x89PNG\x00\x02")
	gittest.Write(t, filepath.Join(repo, "c.go"), "one\ntwo\nthree") // no newline at the end
	gittest.Write(t, filepath.Join(repo, "blob.bin"), "a\x00b\n")

	st := status(repo)
	if got := counts(st.Staged); got != "a.go +3 -1, new.go +0 -0" {
		t.Fatalf("staged: %s", got)
	}
	if got := counts(st.Changes); got != "a.go +1 -0, logo.png +0 -0" {
		t.Fatalf("unstaged: %s", got)
	}
	if got := counts(st.Untracked); got != "blob.bin +0 -0, c.go +3 -0" {
		t.Fatalf("untracked: %s", got)
	}
	if st.Total != nil {
		t.Fatalf("a folder on main has no branch total: %+v", st.Total)
	}

	// The cache notices an edit.
	time.Sleep(10 * time.Millisecond)
	gittest.Write(t, filepath.Join(repo, "c.go"), "one\ntwo\nthree\nfour\n")
	if got := counts(status(repo).Untracked); got != "blob.bin +0 -0, c.go +4 -0" {
		t.Fatalf("untracked after an edit: %s", got)
	}
}

func TestConflictHasNoCounts(t *testing.T) {
	gittest.Isolate(t)
	repo := gittest.Repo(t)
	gittest.Write(t, filepath.Join(repo, "d.go"), "base\n")
	gittest.Run(t, repo, "add", ".")
	gittest.Run(t, repo, "commit", "-qm", "base")
	gittest.Run(t, repo, "checkout", "-qb", "other")
	gittest.Write(t, filepath.Join(repo, "d.go"), "theirs\n")
	gittest.Run(t, repo, "commit", "-qam", "theirs")
	gittest.Run(t, repo, "checkout", "-q", "main")
	gittest.Write(t, filepath.Join(repo, "d.go"), "ours\n")
	gittest.Run(t, repo, "commit", "-qam", "ours")
	exec.Command("git", "-C", repo, "merge", "other").Run() // fails with a conflict on purpose

	st := status(repo)
	if got := counts(st.Conflicts) + "|" + counts(st.Changes) + "|" + counts(st.Staged); got != "d.go +0 -0||" {
		t.Fatalf("conflict rows: %s", got)
	}
}

func TestCountLines(t *testing.T) {
	dir := t.TempDir()
	for name, c := range map[string]struct {
		content string
		want    int
	}{
		"empty":     {"", 0},
		"one":       {"a\n", 1},
		"no-end":    {"a\nb", 2},
		"binary":    {"a\x00\n\n", 0},
		"blank-end": {"a\n\n", 2},
	} {
		p := filepath.Join(dir, name)
		gittest.Write(t, p, c.content)
		if got := countLines(p); got != c.want {
			t.Errorf("%s: %d lines, want %d", name, got, c.want)
		}
	}
	if got := countLines(dir); got != 0 {
		t.Errorf("a folder counts %d lines", got)
	}
}

func TestUncommitted(t *testing.T) {
	gittest.Isolate(t)
	dir := gittest.Repo(t)
	gittest.Write(t, filepath.Join(dir, "a.txt"), "1\n2\n3\n")
	gittest.Run(t, dir, "add", ".")
	gittest.Run(t, dir, "commit", "-q", "-m", "one")
	if b, a, d := Uncommitted(dir); b != "main" || a != 0 || d != 0 {
		t.Fatalf("clean: %s +%d -%d", b, a, d)
	}
	gittest.Write(t, filepath.Join(dir, "a.txt"), "1\nx\n")
	gittest.Write(t, filepath.Join(dir, "new.txt"), "n\nn\n")
	gittest.Write(t, filepath.Join(dir, "staged.txt"), "s\n")
	gittest.Run(t, dir, "add", "staged.txt")
	if _, a, d := Uncommitted(dir); a != 4 || d != 2 {
		t.Fatalf("got +%d -%d, want +4 -2", a, d)
	}
	if b, a, d := Uncommitted(t.TempDir()); b != "" || a != 0 || d != 0 {
		t.Fatal("outside git")
	}
}

func TestBranchTotal(t *testing.T) {
	gittest.Isolate(t)
	repo := repoWithOrigin(t) // a.go has 1 line on main
	if st := status(repo); st.Total != nil {
		t.Fatalf("total on main: %+v", st.Total)
	}

	gittest.Run(t, repo, "checkout", "-qb", "feature")
	gittest.Write(t, filepath.Join(repo, "b.go"), "1\n2\n3\n")
	gittest.Run(t, repo, "add", ".")
	gittest.Run(t, repo, "commit", "-qm", "add b")
	gittest.Write(t, filepath.Join(repo, "a.go"), "A\n") // unstaged: +1 -1
	gittest.Write(t, filepath.Join(repo, "s.go"), "s\n") // staged new file: +1
	gittest.Run(t, repo, "add", "s.go")
	gittest.Write(t, filepath.Join(repo, "u.go"), "u1\nu2\n") // untracked: +2

	st := status(repo)
	if st.Total == nil || *st.Total != (branchTotal{Add: 7, Del: 1, Base: "origin/main"}) {
		t.Fatalf("total = %+v", st.Total)
	}
	gittest.Run(t, repo, "add", ".")
	gittest.Run(t, repo, "commit", "-qm", "the rest")
	if st := status(repo); st.Total == nil || st.Total.Add != 7 || st.Total.Del != 1 {
		t.Fatalf("total after commit = %+v", st.Total)
	}

	// A pull request into release compares with release.
	fork, _ := Run(repo, "rev-parse", "origin/main")
	gittest.Run(t, repo, "branch", "release", fork)
	gittest.Run(t, repo, "push", "-q", "origin", "release")
	if st := StatusFor(repo, "release"); st.Total == nil || st.Total.Base != "origin/release" || st.Total.Add != 7 {
		t.Fatalf("total against the target = %+v", st.Total)
	}

	head, _ := Run(repo, "rev-parse", "HEAD")
	gittest.Run(t, repo, "checkout", "-q", head)
	if st := status(repo); st.Total != nil {
		t.Fatalf("total on a detached HEAD: %+v", st.Total)
	}
}

func TestBranchTotalWorktreeTrackingMain(t *testing.T) {
	gittest.Isolate(t)
	repo := repoWithOrigin(t)
	wt := filepath.Join(t.TempDir(), "wt")
	gittest.Run(t, repo, "worktree", "add", "-q", "-b", "agent", wt, "origin/main")
	gittest.Write(t, filepath.Join(wt, "n.go"), "n\n")

	st := status(wt)
	if st.Upstream != "origin/main" || st.Main != "" {
		t.Fatalf("setup: %+v", st)
	}
	if st.Total == nil || st.Total.Add != 1 || st.Total.Base != "origin/main" {
		t.Fatalf("total = %+v", st.Total)
	}
}

func TestBranchTotalWithoutMainLine(t *testing.T) {
	gittest.Isolate(t)
	repo := t.TempDir()
	gittest.Run(t, repo, "init", "-q", "-b", "trunk")
	gittest.Write(t, filepath.Join(repo, "a.go"), "a\n")
	gittest.Run(t, repo, "add", ".")
	gittest.Run(t, repo, "commit", "-qm", "init")
	gittest.Run(t, repo, "checkout", "-qb", "feature")
	gittest.Write(t, filepath.Join(repo, "a.go"), "b\n")
	if st := status(repo); st.Total != nil {
		t.Fatalf("total without a main line: %+v", st.Total)
	}
}

func TestCommitFileCounts(t *testing.T) {
	gittest.Isolate(t)
	repo := gittest.Repo(t)
	gittest.Write(t, filepath.Join(repo, "x.go"), "1\n2\n3\n")
	gittest.Run(t, repo, "add", ".")
	gittest.Run(t, repo, "commit", "-qm", "init")
	gittest.Run(t, repo, "checkout", "-qb", "side")
	gittest.Write(t, filepath.Join(repo, "z.go"), "z1\nz2\n")
	gittest.Run(t, repo, "add", ".")
	gittest.Run(t, repo, "commit", "-qm", "z")
	gittest.Run(t, repo, "checkout", "-q", "main")
	gittest.Write(t, filepath.Join(repo, "x.go"), "1\ntwo\n3\n4\n")
	gittest.Write(t, filepath.Join(repo, "y.go"), "y\n")
	gittest.Run(t, repo, "add", ".")
	gittest.Run(t, repo, "commit", "-qm", "x and y")

	head, _ := Run(repo, "rev-parse", "HEAD")
	files, err := CommitFiles(repo, head)
	if err != nil || counts(files) != "x.go +2 -1, y.go +1 -0" {
		t.Fatalf("files = %s, %v", counts(files), err)
	}

	gittest.Run(t, repo, "merge", "-q", "--no-edit", "side")
	merge, _ := Run(repo, "rev-parse", "HEAD")
	files, err = CommitFiles(repo, merge)
	if err != nil || counts(files) != "z.go +2 -0" {
		t.Fatalf("merge files = %s, %v", counts(files), err)
	}
}

func TestParseNumstat(t *testing.T) {
	out := "\n3\t1\ta.go\x00-\t-\tlogo.png\x000\t0\t\x00old.go\x00new dir/new.go\x00"
	got := fmt.Sprint(parseNumstat([]byte(out)))
	if got != "map[a.go:{3 1} logo.png:{0 0} new dir/new.go:{0 0}]" {
		t.Fatalf("parsed = %s", got)
	}
	if len(parseNumstat(nil)) != 0 {
		t.Fatal("empty output")
	}
}
