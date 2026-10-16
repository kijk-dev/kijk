package main

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestFoldersFileRoundTrip(t *testing.T) {
	useConfigDir(t)
	p, _ := foldersPath()
	if l, err := readFolders(p); err != nil || l != nil {
		t.Fatalf("missing file: %v %v", l, err)
	}
	want := []savedFolder{{Path: "/code/api"}, {Path: "/code/kijk", Open: "/code/kijk/web"}}
	if err := writeFolders(p, want); err != nil {
		t.Fatal(err)
	}
	got, err := readFolders(p)
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("read back %v %v", got, err)
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
			t.Errorf("mode %v", st.Mode())
		}
	}
}

func TestBrokenFoldersFileMovesAway(t *testing.T) {
	useConfigDir(t)
	p, _ := foldersPath()
	os.MkdirAll(filepath.Dir(p), 0o700)
	os.WriteFile(p, []byte(`{"folders": [`), 0o600)
	l, err := readFolders(p)
	if err == nil || l != nil {
		t.Fatalf("got %v %v", l, err)
	}
	if b, _ := os.ReadFile(p + ".bad"); string(b) != `{"folders": [` {
		t.Errorf("bad file %q", b)
	}
	if exists(p) {
		t.Error("broken file still in place")
	}
}

func TestAddFolderKeepsOrder(t *testing.T) {
	var l []savedFolder
	l, _ = addFolder(l, savedFolder{Path: "/a"})
	l, _ = addFolder(l, savedFolder{Path: "/k", Open: "/k/web"})
	l, _ = addFolder(l, savedFolder{Path: "/b"})
	l, added := addFolder(l, savedFolder{Path: "/k", Open: "/k/docs"})
	want := []savedFolder{{Path: "/a"}, {Path: "/k", Open: "/k/docs"}, {Path: "/b"}}
	if added || !slices.Equal(l, want) {
		t.Fatalf("added %v, list %v", added, l)
	}
}

func TestCheckFolder(t *testing.T) {
	isolatedGit(t)
	home, _ := filepath.EvalSymlinks(t.TempDir())
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	repo := filepath.Join(home, "code", "kijk")
	os.MkdirAll(filepath.Join(repo, "web"), 0o755)
	run(t, repo, "init", "-q")
	plain := filepath.Join(home, "notes")
	os.MkdirAll(plain, 0o755)
	os.WriteFile(filepath.Join(repo, "go.mod"), nil, 0o644)
	link := filepath.Join(home, "link")
	if err := os.Symlink(plain, link); err != nil {
		t.Skip("no symlinks here")
	}
	sep := string(filepath.Separator)

	for _, c := range []struct {
		arg, cwd string
		want     savedFolder
		err      string
	}{
		{arg: ".", cwd: plain, want: savedFolder{Path: plain}},
		{arg: "~/notes", cwd: "/", want: savedFolder{Path: plain}},
		{arg: link, want: savedFolder{Path: plain}},
		{arg: repo, want: savedFolder{Path: repo}},
		{arg: "web", cwd: repo, want: savedFolder{Path: repo, Open: filepath.Join(repo, "web")}},
		{arg: "~", err: "~ is too big to open. Pick a folder inside it"},
		{arg: home, err: "~ is too big to open"},
		{arg: sep, err: sep + " is too big to open"},
		{arg: "nope", cwd: home, err: "~" + sep + "nope: no such folder. Nothing was added"},
		{arg: filepath.Join(repo, "go.mod"), err: "~" + sep + filepath.Join("code", "kijk", "go.mod") + " is a file. Pass a folder"},
	} {
		got, err := checkFolder(c.arg, c.cwd)
		if c.err != "" {
			if err == nil || !strings.HasPrefix(err.Error(), c.err) {
				t.Errorf("%q: want error %q, got %v %v", c.arg, c.err, got, err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%q: got %v %v, want %v", c.arg, got, err, c.want)
		}
	}
}

func TestCheckFoldersAddsNothingOnError(t *testing.T) {
	dir := t.TempDir()
	if l, err := checkFolders([]string{dir, filepath.Join(dir, "nope")}, dir); err == nil || l != nil {
		t.Fatalf("got %v %v", l, err)
	}
}

func TestFolderListUndo(t *testing.T) {
	useConfigDir(t)
	p, _ := foldersPath()
	rts := &roots{}
	l := newFolderList(p, rts)
	l.add([]savedFolder{{Path: "/api"}, {Path: "/k", Open: "/k/web"}, {Path: "/notes"}})
	if !l.remove("/k") || !l.remove("/notes") || rts.has("/k") {
		t.Fatal("remove")
	}
	if l.restore("/k") {
		t.Error("restored a removal that was not the last")
	}
	if !l.restore("/notes") || l.restore("/notes") {
		t.Error("restore the last removal once")
	}
	list, _, open, _ := l.snapshot()
	if !slices.Equal(list, []savedFolder{{Path: "/api"}, {Path: "/notes"}}) || open.Root != "/notes" || !rts.has("/notes") {
		t.Fatalf("list %v open %v", list, open)
	}
	if saved, _ := readFolders(p); !slices.Equal(saved, list) {
		t.Fatalf("saved %v", saved)
	}
}

func TestUncommitted(t *testing.T) {
	isolatedGit(t)
	dir := t.TempDir()
	run(t, dir, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("1\n2\n3\n"), 0o644)
	run(t, dir, "add", ".")
	run(t, dir, "commit", "-q", "-m", "one")
	if b, a, d := uncommitted(dir); b != "main" || a != 0 || d != 0 {
		t.Fatalf("clean: %s +%d -%d", b, a, d)
	}
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("1\nx\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "new.txt"), []byte("n\nn\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "staged.txt"), []byte("s\n"), 0o644)
	run(t, dir, "add", "staged.txt")
	if _, a, d := uncommitted(dir); a != 4 || d != 2 {
		t.Fatalf("got +%d -%d, want +4 -2", a, d)
	}
	if b, a, d := uncommitted(t.TempDir()); b != "" || a != 0 || d != 0 {
		t.Fatal("outside git")
	}
	gone := filepath.Join(t.TempDir(), "gone")
	if r := folderRowOf(savedFolder{Path: gone}); !r.Missing || r.Repo != "gone" {
		t.Fatalf("missing row %+v", r)
	}
}

// A row the window asks for is refreshed at once, not on its turn.
func TestFolderRowRefreshWhenAsked(t *testing.T) {
	isolatedGit(t)
	useConfigDir(t)
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	run(t, dir, "init", "-q")
	other := t.TempDir()
	p, _ := foldersPath()
	h := newHub()
	l := newFolderList(p, &roots{})
	go l.watch(h, nil)
	srv := httptest.NewServer(http.HandlerFunc(h.serveEvents))
	t.Cleanup(srv.Close)
	resp, err := http.Get(srv.URL + "?window=folders")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	rd := bufio.NewReader(resp.Body)
	next := func(want string) {
		t.Helper()
		got := make(chan string, 1)
		go func() {
			for {
				line, err := rd.ReadString('\n')
				if err != nil || strings.Contains(line, want) {
					got <- line
					return
				}
			}
		}()
		select {
		case line := <-got:
			if !strings.Contains(line, want) {
				t.Fatalf("stream ended without %s", want)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("no message with %s", want)
		}
	}
	l.add([]savedFolder{{Path: dir}, {Path: other}})
	next(`"add":0`)
	os.WriteFile(filepath.Join(dir, "new.txt"), []byte("a\nb\n"), 0o644)
	if !l.refresh(dir) || l.refresh("/nope") {
		t.Fatal("refresh")
	}
	next(`"add":2`)
}
