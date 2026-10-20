package main

import (
	"path/filepath"
	"testing"

	kgit "github.com/kijk-dev/kijk/internal/git"
)

func TestUntrackedDiff(t *testing.T) {
	isolatedGit(t)
	repo := gitRepo(t)
	write(t, filepath.Join(repo, "new.go"), "a\nb\n")
	d, err := diff(repo, "new.go", "", "untracked", "", 3)
	if err != nil || len(d.Hunks) != 1 || len(d.Hunks[0].Lines) != 2 || d.Hunks[0].Lines[1].Kind != "+" || d.Hunks[0].Lines[1].New != 2 || d.Hunks[0].Lines[1].Text != "b" {
		t.Fatalf("untracked = %+v, %v", d, err)
	}

	nested := filepath.Join(repo, "fastmcp")
	run(t, repo, "init", "-q", nested)
	write(t, filepath.Join(nested, "x.py"), "x")
	d, err = diff(repo, "fastmcp/", "", "untracked", "", 3)
	if err != nil || !d.Folder {
		t.Fatalf("diff = %+v, %v", d, err)
	}
	if _, err := diff(repo, "../x", "", "untracked", "", 3); err != kgit.ErrOutside {
		t.Fatalf("outside: %v", err)
	}
}
