package viewer

import (
	"path/filepath"
	"testing"

	"github.com/kijk-dev/kijk/internal/git"
	"github.com/kijk-dev/kijk/internal/gittest"
)

func TestUntrackedDiff(t *testing.T) {
	gittest.Isolate(t)
	repo := gittest.Repo(t)
	gittest.Write(t, filepath.Join(repo, "new.go"), "a\nb\n")
	d, err := diff(repo, "new.go", "", "untracked", "", 3)
	if err != nil || len(d.Hunks) != 1 || len(d.Hunks[0].Lines) != 2 || d.Hunks[0].Lines[1].Kind != "+" || d.Hunks[0].Lines[1].New != 2 || d.Hunks[0].Lines[1].Text != "b" {
		t.Fatalf("untracked = %+v, %v", d, err)
	}

	nested := filepath.Join(repo, "fastmcp")
	gittest.Run(t, repo, "init", "-q", nested)
	gittest.Write(t, filepath.Join(nested, "x.py"), "x")
	d, err = diff(repo, "fastmcp/", "", "untracked", "", 3)
	if err != nil || !d.Folder {
		t.Fatalf("diff = %+v, %v", d, err)
	}
	if _, err := diff(repo, "../x", "", "untracked", "", 3); err != git.ErrOutside {
		t.Fatalf("outside: %v", err)
	}
}
