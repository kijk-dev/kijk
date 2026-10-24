package main

import (
	"bufio"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kijk-dev/kijk/internal/viewer"
)

// abs makes a slash path absolute on this system: Windows needs a drive letter.
func abs(p string) string {
	return filepath.VolumeName(os.TempDir()) + filepath.FromSlash(p)
}

func TestFoldersFileRoundTrip(t *testing.T) {
	useConfigDir(t)
	p, _ := foldersPath()
	if l, err := readFolders(p); err != nil || l != nil {
		t.Fatalf("missing file: %v %v", l, err)
	}
	want := []savedFolder{{Path: abs("/code/api")}, {Path: abs("/code/kijk"), Open: abs("/code/kijk/web")}}
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
	if !errors.Is(err, errMovedAway) || l != nil {
		t.Fatalf("got %v %v", l, err)
	}
	if b, _ := os.ReadFile(p + ".bad"); string(b) != `{"folders": [` {
		t.Errorf("bad file %q", b)
	}
	if exists(p) {
		t.Error("broken file still in place")
	}
}

func TestFolderListKeepsBrokenFileErrorAfterSave(t *testing.T) {
	useConfigDir(t)
	p, _ := foldersPath()
	os.MkdirAll(filepath.Dir(p), 0o700)
	os.WriteFile(p, []byte(`{"folders": [`), 0o600)
	l := newFolderList(p, &viewer.Roots{})
	if _, err := l.add([]savedFolder{{Path: abs("/api")}}); err != nil {
		t.Fatal(err)
	}
	if saved, _ := readFolders(p); !slices.Equal(saved, []savedFolder{{Path: abs("/api")}}) {
		t.Errorf("saved %v", saved)
	}
	if _, _, _, err := l.snapshot(); err == nil || !strings.Contains(err.Error(), "folders.json.bad") {
		t.Errorf("pane error %v", err)
	}
}

func TestFolderListDoesNotOverwriteUnreadableFile(t *testing.T) {
	if runtime.GOOS == "windows" || os.Getuid() == 0 {
		t.Skip("needs file modes that keep the owner out")
	}
	useConfigDir(t)
	p, _ := foldersPath()
	writeFolders(p, []savedFolder{{Path: "/keep"}})
	os.Chmod(p, 0)
	t.Cleanup(func() { os.Chmod(p, 0o600) })
	l := newFolderList(p, &viewer.Roots{})
	if _, err := l.add([]savedFolder{{Path: "/api"}}); err == nil {
		t.Error("add saved over a file it could not read")
	}
	os.Chmod(p, 0o600)
	if saved, _ := readFolders(p); !slices.Equal(saved, []savedFolder{{Path: "/keep"}}) {
		t.Errorf("file now %v", saved)
	}
	if _, _, _, err := l.snapshot(); err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("pane error %v", err)
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
	rts := &viewer.Roots{}
	l := newFolderList(p, rts)
	l.add([]savedFolder{{Path: abs("/api")}, {Path: abs("/k"), Open: abs("/k/web")}, {Path: abs("/notes")}})
	if !l.remove(abs("/k")) || !l.remove(abs("/notes")) || rts.Has(abs("/k")) {
		t.Fatal("remove")
	}
	if l.restore(abs("/k")) {
		t.Error("restored a removal that was not the last")
	}
	if !l.restore(abs("/notes")) || l.restore(abs("/notes")) {
		t.Error("restore the last removal once")
	}
	list, open, _, _ := l.snapshot()
	if !slices.Equal(list, []savedFolder{{Path: abs("/api")}, {Path: abs("/notes")}}) || open.Root != abs("/notes") || !rts.Has(abs("/notes")) {
		t.Fatalf("list %v open %v", list, open)
	}
	if saved, _ := readFolders(p); !slices.Equal(saved, list) {
		t.Fatalf("saved %v", saved)
	}
}

func TestFolderRowMissing(t *testing.T) {
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
	l := newFolderList(p, &viewer.Roots{})
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

// kijk <PR link> asks the folders window to open the clone on that pull request.
func TestFolderListOpenPR(t *testing.T) {
	useConfigDir(t)
	p, _ := foldersPath()
	l := newFolderList(p, &viewer.Roots{})
	l.addFor([]savedFolder{{Path: abs("/shop")}}, 477)
	if _, open, _, _ := l.snapshot(); open.Root != abs("/shop") || open.PR != 477 {
		t.Fatalf("open %+v", open)
	}
	l.add([]savedFolder{{Path: abs("/notes")}})
	if _, open, _, _ := l.snapshot(); open.Root != abs("/notes") || open.PR != 0 {
		t.Fatalf("a folder alone opens no pull request: %+v", open)
	}
	if u := foldersURL(7717, "t", []savedFolder{{Path: "/shop"}}, 477); !strings.HasSuffix(u, "&window=folders&open=%2Fshop&pr=477") {
		t.Errorf("url %s", u)
	}
}
