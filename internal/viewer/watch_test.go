package viewer

import (
	"bufio"
	"context"
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

	"github.com/kijk-dev/kijk/internal/gittest"
)

// watchFolder opens the folder event stream for root and returns its messages.
func watchFolder(t *testing.T, root string) <-chan folderChange {
	rts := &Roots{}
	rts.Set([]string{root})
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
	gittest.Isolate(t)
	repo := gittest.Repo(t)
	ch := watchFolder(t, repo)

	for i := 0; i < 200; i++ {
		gittest.Write(t, filepath.Join(repo, "gen", fmt.Sprintf("f%03d.go", i)), "x")
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

	gittest.Write(t, filepath.Join(repo, "a.go"), "a")
	gittest.Run(t, repo, "add", "a.go")
	gittest.Run(t, repo, "commit", "-qm", "a")
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
	gittest.Isolate(t)
	repo := gittest.Repo(t)
	gittest.Write(t, filepath.Join(repo, "a.go"), "a")
	gittest.Run(t, repo, "add", ".")
	gittest.Run(t, repo, "commit", "-qm", "init")
	wt := filepath.Join(t.TempDir(), "wt")
	gittest.Run(t, repo, "worktree", "add", "-q", "-b", "feature", wt)
	if len(outsideGitDirs(wt)) == 0 {
		t.Fatal("worktree git folders not found")
	}

	ch := watchFolder(t, wt)
	gittest.Run(t, wt, "commit", "-q", "--allow-empty", "-m", "empty")
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

// A folder a window shows stays readable after its row is gone, until no window shows it.
func TestShownFolderStaysReadable(t *testing.T) {
	gittest.Isolate(t)
	root := gittest.Repo(t)
	rts := &Roots{}
	srv := httptest.NewServer(http.HandlerFunc((&api{roots: rts}).folderEvents))
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := func() int {
		req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"?root="+url.QueryEscape(root), nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { resp.Body.Close() })
		return resp.StatusCode
	}
	if c := stream(); c != http.StatusForbidden {
		t.Fatalf("a folder in no row: %d", c)
	}
	rts.Set([]string{root})
	if c := stream(); c != http.StatusOK {
		t.Fatalf("a listed folder: %d", c)
	}
	rts.Set(nil)
	if !rts.Has(root) {
		t.Fatal("the shown folder went with its row")
	}
	if c := stream(); c != http.StatusOK {
		t.Fatalf("a second stream on the shown folder: %d", c)
	}
	if rts.Has(filepath.Join(root, "sub")) || rts.Has(filepath.Dir(root)) {
		t.Fatal("showing a folder made another one readable")
	}
	cancel()
	for deadline := time.Now().Add(2 * time.Second); rts.Has(root); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the folder stayed readable after its streams closed")
		}
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	if c := stream(); c != http.StatusForbidden {
		t.Fatalf("a stream after the row and the streams are gone: %d", c)
	}
}
