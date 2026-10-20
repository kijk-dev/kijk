package viewer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kijk-dev/kijk/internal/gittest"
)

func findIn(t *testing.T, content, q string) findResult {
	t.Helper()
	p := filepath.Join(t.TempDir(), "f.txt")
	gittest.Write(t, p, content)
	st, _ := os.Stat(p)
	idx, err := buildIndex(p, st)
	if err != nil {
		t.Fatal(err)
	}
	res, err := findInFile(p, idx, q)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestFindFarDown(t *testing.T) {
	var b strings.Builder
	for i := 1; i < 9000; i++ {
		b.WriteString("filler line\n")
	}
	b.WriteString("the Needle is here\n")
	res := findIn(t, b.String(), "needle")
	if fmt.Sprint(res.Matches) != "[{9000 4 10}]" || res.More {
		t.Fatalf("matches = %v", res)
	}
}

func TestFindOffsets(t *testing.T) {
	// "é" is one UTF-16 unit, "😀" is two; offsets must match JavaScript's.
	res := findIn(t, "café café\n😀 Café\r\nno\n", "CAFÉ")
	if got := fmt.Sprint(res.Matches); got != "[{1 0 4} {1 5 9} {2 3 7}]" {
		t.Fatalf("matches = %s", got)
	}
	if res := findIn(t, "aaaa\n", "aa"); fmt.Sprint(res.Matches) != "[{1 0 2} {1 2 4}]" {
		t.Fatalf("overlapping = %v", res.Matches)
	}
	if res := findIn(t, "abc", ""); len(res.Matches) != 0 {
		t.Fatalf("empty query = %v", res.Matches)
	}
}

func TestFindStopsAtLimit(t *testing.T) {
	res := findIn(t, strings.Repeat("x x x x x\n", maxMatches/5+10), "x")
	if len(res.Matches) != maxMatches || !res.More {
		t.Fatalf("%d matches, more=%v", len(res.Matches), res.More)
	}
}

func TestGotoFile(t *testing.T) {
	paths := []string{
		"src/user/UserService.kt",
		"src/user/UserServiceTest.kt",
		"src/sync/SyncRunner.kt",
		"docs/usage/services.md",
		"README.md",
		"x_a_c_A",
	}
	got := gotoFile(paths, "usrsvc", 10)
	if len(got) == 0 || got[0].Path != "src/user/UserService.kt" {
		t.Fatalf("usrsvc = %v", got)
	}
	if fmt.Sprint(got[0].Hits) != "[9 10 12 13 16 18]" {
		t.Fatalf("hits = %v", got[0].Hits)
	}
	if got := gotoFile(paths, "readme", 10); len(got) != 1 || got[0].Path != "README.md" {
		t.Fatalf("readme = %v", got)
	}
	if got := gotoFile(paths, "xac", 10); len(got) != 1 || got[0].Path != "x_a_c_A" {
		t.Fatalf("a word start that leads nowhere: %v", got)
	}
	if got := gotoFile(paths, "user/test", 10); len(got) != 1 || got[0].Path != "src/user/UserServiceTest.kt" {
		t.Fatalf("with a folder: %v", got)
	}
	if got := gotoFile(paths, "zzz", 10); len(got) != 0 {
		t.Fatalf("no match: %v", got)
	}
}

func TestFolderFiles(t *testing.T) {
	gittest.Isolate(t)
	repo := gittest.Repo(t)
	gittest.Write(t, filepath.Join(repo, ".gitignore"), "build/\n")
	gittest.Write(t, filepath.Join(repo, "a.go"), "a\n")
	gittest.Write(t, filepath.Join(repo, "gone.go"), "g\n")
	gittest.Run(t, repo, "add", ".")
	gittest.Run(t, repo, "commit", "-qm", "init")
	gittest.Write(t, filepath.Join(repo, "src", "new.go"), "n\n")
	gittest.Write(t, filepath.Join(repo, "build", "out.bin"), "b\n")
	os.Remove(filepath.Join(repo, "gone.go"))
	if got := strings.Join(folderFiles(repo), ","); got != ".gitignore,a.go,src/new.go" {
		t.Fatalf("git folder = %s", got)
	}

	plain := t.TempDir()
	gittest.Write(t, filepath.Join(plain, "x.txt"), "x\n")
	gittest.Write(t, filepath.Join(plain, "sub", "y.txt"), "y\n")
	if got := strings.Join(folderFiles(plain), ","); got != "sub/y.txt,x.txt" {
		t.Fatalf("plain folder = %s", got)
	}
}

func TestFindMatchesShownLine(t *testing.T) {
	// The view shows a run of bad bytes as one "�" and cuts long lines, so find must too.
	if res := findIn(t, "Gr\xfc\xdfe needle\n", "needle"); fmt.Sprint(res.Matches) != "[{1 5 11}]" {
		t.Fatalf("invalid UTF-8 = %v", res.Matches)
	}
	if res := findIn(t, strings.Repeat("x", maxLineLen)+" needle\n", "needle"); len(res.Matches) != 0 {
		t.Fatalf("past the cut = %v", res.Matches)
	}
}
