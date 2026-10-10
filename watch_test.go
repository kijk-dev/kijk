package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// watchFolder opens the folder event stream for root and returns its messages.
func watchFolder(t *testing.T, root string) <-chan folderChange {
	rts := &roots{m: map[string]bool{root: true}}
	srv := httptest.NewServer(http.HandlerFunc((&api{roots: rts}).folderEvents))
	t.Cleanup(srv.Close)
	resp, err := http.Get(srv.URL + "?root=" + url.QueryEscape(root))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("stream: %v %v", err, resp.Status)
	}
	t.Cleanup(func() { resp.Body.Close() })
	out := make(chan folderChange, 16)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if data, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
				var c folderChange
				json.Unmarshal([]byte(data), &c)
				out <- c
			}
		}
	}()
	time.Sleep(200 * time.Millisecond) // let the watcher start before the test writes
	return out
}

func next(t *testing.T, ch <-chan folderChange, within time.Duration) (folderChange, bool) {
	select {
	case c := <-ch:
		return c, true
	case <-time.After(within):
		return folderChange{}, false
	}
}

func TestFolderEventsBatch(t *testing.T) {
	isolatedGit(t)
	repo := gitRepo(t)
	ch := watchFolder(t, repo)

	for i := 0; i < 200; i++ {
		write(t, filepath.Join(repo, "gen", fmt.Sprintf("f%03d.go", i)), "x")
	}
	c, ok := next(t, ch, 2*time.Second)
	if !ok {
		t.Fatal("no update after 200 writes")
	}
	if !c.All && !slices.Contains(c.Paths, "gen/f199.go") && !slices.Contains(c.Paths, "gen") {
		t.Fatalf("update does not name the written files: %+v", c)
	}
	if extra, ok := next(t, ch, 700*time.Millisecond); ok {
		t.Fatalf("200 writes caused a second update: %+v", extra)
	}

	write(t, filepath.Join(repo, "a.go"), "a")
	run(t, repo, "add", "a.go")
	run(t, repo, "commit", "-qm", "a")
	gitSeen := false
	for !gitSeen {
		c, ok := next(t, ch, 2*time.Second)
		if !ok {
			t.Fatal("commit did not cause an update with git set")
		}
		gitSeen = c.Git
	}
}

func TestFolderEventsWorktree(t *testing.T) {
	isolatedGit(t)
	repo := gitRepo(t)
	write(t, filepath.Join(repo, "a.go"), "a")
	run(t, repo, "add", ".")
	run(t, repo, "commit", "-qm", "init")
	wt := filepath.Join(t.TempDir(), "wt")
	run(t, repo, "worktree", "add", "-q", "-b", "feature", wt)
	if len(outsideGitDirs(wt)) == 0 {
		t.Fatal("worktree git folders not found")
	}

	ch := watchFolder(t, wt)
	run(t, wt, "commit", "-q", "--allow-empty", "-m", "empty")
	for {
		c, ok := next(t, ch, 2*time.Second)
		if !ok {
			t.Fatal("commit in a worktree did not cause an update with git set")
		}
		if c.Git {
			return
		}
	}
}
