package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func counts(cs []change) string {
	var s []string
	for _, c := range cs {
		s = append(s, fmt.Sprintf("%s +%d -%d", c.Path, c.Add, c.Del))
	}
	return strings.Join(s, ", ")
}

func TestStatusCounts(t *testing.T) {
	isolatedGit(t)
	repo := gitRepo(t)
	write(t, filepath.Join(repo, "a.go"), "1\n2\n3\n4\n5\n")
	write(t, filepath.Join(repo, "old.go"), "x\ny\n")
	write(t, filepath.Join(repo, "logo.png"), "\x89PNG\x00\x01")
	run(t, repo, "add", ".")
	run(t, repo, "commit", "-qm", "init")

	write(t, filepath.Join(repo, "a.go"), "1\n2\nthree\n4\n5\nsix\nseven\n")
	run(t, repo, "add", "a.go")
	write(t, filepath.Join(repo, "a.go"), "1\n2\nthree\n4\n5\nsix\nseven\neight\n")
	run(t, repo, "mv", "old.go", "new.go")
	write(t, filepath.Join(repo, "logo.png"), "\x89PNG\x00\x02")
	write(t, filepath.Join(repo, "c.go"), "one\ntwo\nthree") // no newline at the end
	write(t, filepath.Join(repo, "blob.bin"), "a\x00b\n")

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
	write(t, filepath.Join(repo, "c.go"), "one\ntwo\nthree\nfour\n")
	if got := counts(status(repo).Untracked); got != "blob.bin +0 -0, c.go +4 -0" {
		t.Fatalf("untracked after an edit: %s", got)
	}
}

func TestConflictHasNoCounts(t *testing.T) {
	isolatedGit(t)
	repo := gitRepo(t)
	write(t, filepath.Join(repo, "d.go"), "base\n")
	run(t, repo, "add", ".")
	run(t, repo, "commit", "-qm", "base")
	run(t, repo, "checkout", "-qb", "other")
	write(t, filepath.Join(repo, "d.go"), "theirs\n")
	run(t, repo, "commit", "-qam", "theirs")
	run(t, repo, "checkout", "-q", "main")
	write(t, filepath.Join(repo, "d.go"), "ours\n")
	run(t, repo, "commit", "-qam", "ours")
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
		write(t, p, c.content)
		if got := countLines(p); got != c.want {
			t.Errorf("%s: %d lines, want %d", name, got, c.want)
		}
	}
	if got := countLines(dir); got != 0 {
		t.Errorf("a folder counts %d lines", got)
	}
}

func TestBranchTotal(t *testing.T) {
	isolatedGit(t)
	repo := repoWithOrigin(t) // a.go has 1 line on main
	if st := status(repo); st.Total != nil {
		t.Fatalf("total on main: %+v", st.Total)
	}

	run(t, repo, "checkout", "-qb", "feature")
	write(t, filepath.Join(repo, "b.go"), "1\n2\n3\n")
	run(t, repo, "add", ".")
	run(t, repo, "commit", "-qm", "add b")
	write(t, filepath.Join(repo, "a.go"), "A\n") // unstaged: +1 -1
	write(t, filepath.Join(repo, "s.go"), "s\n") // staged new file: +1
	run(t, repo, "add", "s.go")
	write(t, filepath.Join(repo, "u.go"), "u1\nu2\n") // untracked: +2

	st := status(repo)
	if st.Total == nil || *st.Total != (branchTotal{Add: 7, Del: 1, Base: "origin/main"}) {
		t.Fatalf("total = %+v", st.Total)
	}
	run(t, repo, "add", ".")
	run(t, repo, "commit", "-qm", "the rest")
	if st := status(repo); st.Total == nil || st.Total.Add != 7 || st.Total.Del != 1 {
		t.Fatalf("total after commit = %+v", st.Total)
	}

	// A pull request into release compares with release.
	fork, _ := git(repo, "rev-parse", "origin/main")
	run(t, repo, "branch", "release", fork)
	run(t, repo, "push", "-q", "origin", "release")
	if st := statusFor(repo, "release"); st.Total == nil || st.Total.Base != "origin/release" || st.Total.Add != 7 {
		t.Fatalf("total against the target = %+v", st.Total)
	}

	head, _ := git(repo, "rev-parse", "HEAD")
	run(t, repo, "checkout", "-q", head)
	if st := status(repo); st.Total != nil {
		t.Fatalf("total on a detached HEAD: %+v", st.Total)
	}
}

func TestBranchTotalWorktreeTrackingMain(t *testing.T) {
	isolatedGit(t)
	repo := repoWithOrigin(t)
	wt := filepath.Join(t.TempDir(), "wt")
	run(t, repo, "worktree", "add", "-q", "-b", "agent", wt, "origin/main")
	write(t, filepath.Join(wt, "n.go"), "n\n")

	st := status(wt)
	if st.Upstream != "origin/main" || st.Main != "" {
		t.Fatalf("setup: %+v", st)
	}
	if st.Total == nil || st.Total.Add != 1 || st.Total.Base != "origin/main" {
		t.Fatalf("total = %+v", st.Total)
	}
}

func TestBranchTotalWithoutMainLine(t *testing.T) {
	isolatedGit(t)
	repo := t.TempDir()
	run(t, repo, "init", "-q", "-b", "trunk")
	write(t, filepath.Join(repo, "a.go"), "a\n")
	run(t, repo, "add", ".")
	run(t, repo, "commit", "-qm", "init")
	run(t, repo, "checkout", "-qb", "feature")
	write(t, filepath.Join(repo, "a.go"), "b\n")
	if st := status(repo); st.Total != nil {
		t.Fatalf("total without a main line: %+v", st.Total)
	}
}

func TestCommitFileCounts(t *testing.T) {
	isolatedGit(t)
	repo := gitRepo(t)
	write(t, filepath.Join(repo, "x.go"), "1\n2\n3\n")
	run(t, repo, "add", ".")
	run(t, repo, "commit", "-qm", "init")
	run(t, repo, "checkout", "-qb", "side")
	write(t, filepath.Join(repo, "z.go"), "z1\nz2\n")
	run(t, repo, "add", ".")
	run(t, repo, "commit", "-qm", "z")
	run(t, repo, "checkout", "-q", "main")
	write(t, filepath.Join(repo, "x.go"), "1\ntwo\n3\n4\n")
	write(t, filepath.Join(repo, "y.go"), "y\n")
	run(t, repo, "add", ".")
	run(t, repo, "commit", "-qm", "x and y")

	head, _ := git(repo, "rev-parse", "HEAD")
	files, err := commitFiles(repo, head)
	if err != nil || counts(files) != "x.go +2 -1, y.go +1 -0" {
		t.Fatalf("files = %s, %v", counts(files), err)
	}

	run(t, repo, "merge", "-q", "--no-edit", "side")
	merge, _ := git(repo, "rev-parse", "HEAD")
	files, err = commitFiles(repo, merge)
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
