package git

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kijk-dev/kijk/internal/gittest"
)

// featureRepo is a clone on branch feature, split from origin/main, with nothing changed yet.
func featureRepo(t *testing.T) string {
	t.Helper()
	gittest.Isolate(t)
	repo := repoWithOrigin(t)
	gittest.Write(t, filepath.Join(repo, "keep.go"), "one\ntwo\nthree\n")
	gittest.Write(t, filepath.Join(repo, "gone.go"), "x\ny\n")
	gittest.Write(t, filepath.Join(repo, "move.go"), "m1\nm2\nm3\nm4\nm5\n")
	gittest.Run(t, repo, "add", ".")
	gittest.Run(t, repo, "commit", "-qm", "base files")
	gittest.Run(t, repo, "push", "-q", "origin", "main")
	gittest.Run(t, repo, "checkout", "-qb", "feature")
	return repo
}

func branchSplit(t *testing.T, repo, target string) string {
	t.Helper()
	split := BranchSplit(repo, target)
	if !ValidHash(split) {
		t.Fatalf("split point = %q", split)
	}
	return split
}

func hunkText(d DiffResult) string {
	var s []string
	for _, h := range d.Hunks {
		for _, l := range h.Lines {
			s = append(s, l.Kind+l.Text)
		}
	}
	return strings.Join(s, "|")
}

func TestBranchDiff(t *testing.T) {
	repo := featureRepo(t)
	gittest.Write(t, filepath.Join(repo, "keep.go"), "one\nTWO\nthree\n")
	gittest.Run(t, repo, "commit", "-qam", "commit only")
	split := branchSplit(t, repo, "")

	d, err := Diff(repo, "keep.go", "", "branch", split, 3)
	if err != nil || hunkText(d) != " one|-two|+TWO| three" {
		t.Fatalf("commits only: %q, %v", hunkText(d), err)
	}

	gittest.Write(t, filepath.Join(repo, "keep.go"), "one\nTWO\nthree\nfour\n")
	gittest.Run(t, repo, "add", "keep.go")
	gittest.Write(t, filepath.Join(repo, "keep.go"), "zero\none\nTWO\nthree\nfour\n")
	d, err = Diff(repo, "keep.go", "", "branch", split, 3)
	if err != nil || hunkText(d) != "+zero| one|-two|+TWO| three|+four" {
		t.Fatalf("commits, staged and unstaged: %q, %v", hunkText(d), err)
	}

	gittest.Run(t, repo, "mv", "move.go", "moved.go")
	gittest.Run(t, repo, "rm", "-q", "gone.go")
	gittest.Run(t, repo, "commit", "-qm", "rename and delete")
	d, err = Diff(repo, "moved.go", "move.go", "branch", split, 3)
	if err != nil || len(d.Hunks) != 0 {
		t.Fatalf("a pure rename has no hunks: %+v, %v", d, err)
	}
	d, err = Diff(repo, "gone.go", "", "branch", split, 3)
	if err != nil || hunkText(d) != "-x|-y" {
		t.Fatalf("deleted: %q, %v", hunkText(d), err)
	}

	if _, err := Diff(repo, "keep.go", "", "branch", "not a hash", 3); !errors.Is(err, ErrBadHash) {
		t.Fatalf("a bad split point: %v", err)
	}
	if _, err := Diff(repo, "keep.go", "", "sideways", "", 3); !errors.Is(err, ErrBadMode) {
		t.Fatalf("an unknown mode: %v", err)
	}
}

// A PR that merges into another branch than main is compared with that branch.
func TestBranchDiffAgainstTarget(t *testing.T) {
	repo := featureRepo(t)
	gittest.Run(t, repo, "checkout", "-qb", "release")
	gittest.Write(t, filepath.Join(repo, "keep.go"), "one\ntwo\nthree\nrelease\n")
	gittest.Run(t, repo, "commit", "-qam", "on release")
	gittest.Run(t, repo, "push", "-q", "origin", "release")
	gittest.Run(t, repo, "checkout", "-qb", "fix")
	gittest.Write(t, filepath.Join(repo, "keep.go"), "one\ntwo\nthree\nrelease\nfix\n")
	gittest.Run(t, repo, "commit", "-qam", "the fix")

	d, err := Diff(repo, "keep.go", "", "branch", branchSplit(t, repo, "release"), 0)
	if err != nil || hunkText(d) != "+fix" {
		t.Fatalf("against release: %q, %v", hunkText(d), err)
	}
	d, err = Diff(repo, "keep.go", "", "branch", branchSplit(t, repo, ""), 0)
	if err != nil || hunkText(d) != "+release|+fix" {
		t.Fatalf("against main: %q, %v", hunkText(d), err)
	}
	b, err := BranchChanges(repo, "release")
	if err != nil || b.Base != "origin/release" {
		t.Fatalf("base = %+v, %v", b, err)
	}
}

func files(b Branch) string {
	var s []string
	for _, f := range b.Files {
		p := fmt.Sprintf("%s %s +%d -%d", f.Status, f.Path, f.Add, f.Del)
		if f.Orig != "" {
			p += " <- " + f.Orig
		}
		if f.NotPushed {
			p += " (not pushed)"
		}
		s = append(s, p)
	}
	return strings.Join(s, ", ")
}

func TestBranchChanges(t *testing.T) {
	repo := featureRepo(t)
	if b, err := BranchChanges(repo, ""); err != nil || len(b.Files) != 0 {
		t.Fatalf("a new branch has no files yet: %+v, %v", b, err)
	}
	gittest.Write(t, filepath.Join(repo, "keep.go"), "one\nTWO\nthree\n")
	gittest.Run(t, repo, "mv", "move.go", "moved.go")
	gittest.Run(t, repo, "rm", "-q", "gone.go")
	gittest.Run(t, repo, "commit", "-qam", "edit, rename, delete")
	gittest.Run(t, repo, "push", "-q", "-u", "origin", "feature")
	gittest.Write(t, filepath.Join(repo, "added.go"), "a\nb\n")
	gittest.Run(t, repo, "add", "added.go")
	gittest.Run(t, repo, "commit", "-qm", "add a file")
	gittest.Write(t, filepath.Join(repo, "keep.go"), "one\nTWO\nthree\nfour\n")
	gittest.Write(t, filepath.Join(repo, "new.go"), "n1\nn2\nn3")

	knownPRs.Store(repo+"\nfeature", &pullRequest{Number: 7, Title: "Feature"})
	b, err := BranchChanges(repo, "")
	if err != nil {
		t.Fatal(err)
	}
	if b.PR == nil || b.PR.Number != 7 {
		t.Fatalf("the PR the PR line found: %+v", b.PR)
	}
	want := "A added.go +2 -0 (not pushed), D gone.go +0 -2, M keep.go +2 -1, R moved.go +0 -0 <- move.go, ? new.go +3 -0 (not pushed)"
	if got := files(b); got != want {
		t.Fatalf("files:\n%s\nwant\n%s", got, want)
	}
	if b.Branch != "feature" || b.Base != "origin/main" || b.Upstream != "origin/feature" || b.Ahead != 1 || b.Uncommitted != 2 || !ValidHash(b.Split) || !ValidHash(b.UpHash) {
		t.Fatalf("branch = %+v", b)
	}

	var add, del int
	for _, f := range b.Files {
		add += f.Add
		del += f.Del
	}
	total := StatusFor(repo, "").Total
	if total == nil || total.Add != add || total.Del != del {
		t.Fatalf("the files count +%d -%d, the branch total is %+v", add, del, total)
	}

	gittest.Run(t, repo, "checkout", "-qf", "main")
	if _, err := BranchChanges(repo, ""); !errors.Is(err, ErrNoBranch) {
		t.Fatalf("main has no branch changes: %v", err)
	}
}

func TestNotPushed(t *testing.T) {
	repo := featureRepo(t)
	var b strings.Builder
	for i := 1; i <= 10; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	gittest.Write(t, filepath.Join(repo, "keep.go"), b.String())
	gittest.Run(t, repo, "commit", "-qam", "ten lines")
	if got, err := NotPushed(repo, "keep.go"); err != nil || fmt.Sprint(got) != "[[1 10]]" {
		t.Fatalf("no upstream, so all of it: %v, %v", got, err)
	}

	gittest.Run(t, repo, "push", "-q", "-u", "origin", "feature")
	if got, err := NotPushed(repo, "keep.go"); err != nil || fmt.Sprint(got) != "[]" {
		t.Fatalf("all pushed: %v, %v", got, err)
	}
	gittest.Write(t, filepath.Join(repo, "keep.go"), strings.Replace(b.String(), "line 3\n", "line 3\nnew a\nnew b\n", 1))
	gittest.Run(t, repo, "commit", "-qam", "not pushed")
	gittest.Write(t, filepath.Join(repo, "keep.go"), strings.Replace(strings.Replace(b.String(), "line 3\n", "line 3\nnew a\nnew b\n", 1), "line 9\n", "LINE 9\n", 1))
	if got, err := NotPushed(repo, "keep.go"); err != nil || fmt.Sprint(got) != "[[4 5] [11 11]]" {
		t.Fatalf("a pushed and an unpushed commit, and an edit: %v, %v", got, err)
	}

	gittest.Write(t, filepath.Join(repo, "fresh.go"), "f1\nf2\n")
	if got, err := NotPushed(repo, "fresh.go"); err != nil || fmt.Sprint(got) != "[[1 2]]" {
		t.Fatalf("an untracked file: %v, %v", got, err)
	}
	gittest.Run(t, repo, "add", "fresh.go")
	gittest.Run(t, repo, "commit", "-qm", "fresh")
	if got, err := NotPushed(repo, "fresh.go"); err != nil || fmt.Sprint(got) != "[[1 2]]" {
		t.Fatalf("a file the upstream does not have: %v, %v", got, err)
	}
	if _, err := NotPushed(repo, "../x"); !errors.Is(err, ErrOutside) {
		t.Fatalf("outside: %v", err)
	}
}
