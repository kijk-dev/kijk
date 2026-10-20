package agents

import (
	"encoding/json"
	"fmt"
	"maps"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kijk-dev/kijk/internal/agentstest"
)

var thread = agentstest.Thread

// codexEnv is a machine with Codex at home and no Claude Code.
func codexEnv(t *testing.T, home string) *Env {
	return testEnv(t, "CODEX_HOME", home)
}

func TestCodexFolder(t *testing.T) {
	env := testEnv(t)
	if sessions, err := codexSessions(env); err != nil || len(sessions) != 0 || env.Installed() {
		t.Fatalf("no Codex home: got %v, %v; want nothing, no error, and Codex not installed", sessions, err)
	}
	if want := filepath.Join("/custom", "app-server-control", "app-server-control.sock"); codexSocket("/custom") != want || agentstest.CodexSocket("/custom") != want {
		t.Fatal("the fake Codex listens where kijk does not look")
	}
	home, f := agentstest.CodexHome(t)
	f.Set(thread("t1", "/a", "idle"))
	if sessions, err := codexSessions(codexEnv(t, home)); err != nil || len(sessions) != 1 {
		t.Fatalf("CODEX_HOME: got %v, %v", sessions, err)
	}
}

func TestReadCodexThreads(t *testing.T) {
	f := agentstest.NewFakeCodex(t)
	f.Set(thread("t1", "/a", "idle"), thread("t2", "/b", "active", "name", "named"))
	threads, err := readCodexThreads(f.Socket, codexCache{}, "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) != 2 || threads[0].ID != "t1" || threads[0].Cwd != "/a" || threads[0].busy() || !threads[1].busy() || *threads[1].Name != "named" {
		t.Fatalf("threads = %+v", threads)
	}
	reqs := f.Take()
	var methods []string
	for _, r := range reqs {
		methods = append(methods, r.Method)
	}
	if !slices.Equal(methods, []string{"initialize", "thread/loaded/list", "thread/read", "thread/read"}) {
		t.Fatalf("methods = %v, want only the three read requests", methods)
	}
	var init struct {
		ClientInfo   map[string]string `json:"clientInfo"`
		Capabilities any               `json:"capabilities"`
	}
	json.Unmarshal(reqs[0].Params, &init)
	if init.ClientInfo["name"] != "kijk" || init.ClientInfo["version"] != "1.2.3" || init.Capabilities != nil || strings.Contains(string(reqs[0].Params), "capabilities") {
		t.Fatalf("initialize params = %s, want clientInfo only", reqs[0].Params)
	}
}

func TestReadCodexThreadsNoServer(t *testing.T) {
	dir := agentstest.ShortTempDir(t)
	stale := filepath.Join(dir, "stale.sock")
	ln, err := net.Listen("unix", stale)
	if err != nil {
		t.Skipf("no Unix sockets here: %v", err)
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	ln.Close()
	for _, socket := range []string{filepath.Join(dir, "missing.sock"), stale} {
		threads, err := readCodexThreads(socket, codexCache{}, "dev")
		if threads != nil || err != nil {
			t.Fatalf("%s: got %v, %v; want no threads and no error", socket, threads, err)
		}
	}
}

func TestReadCodexThreadsForbiddenSocket(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("file modes do not stop this user from connecting")
	}
	f := agentstest.NewFakeCodex(t)
	if err := os.Chmod(f.Socket, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := readCodexThreads(f.Socket, codexCache{}, "dev"); err == nil {
		t.Fatal("an app server kijk may not reach is an error, not the absence of Codex")
	}
}

func TestReadCodexThreadsBadServers(t *testing.T) {
	cases := map[string]func(c *agentstest.Conn, id int, method string){
		"hangs":     func(*agentstest.Conn, int, string) {},
		"closes":    func(c *agentstest.Conn, _ int, _ string) { c.Send(0x8, nil) },
		"too large": func(c *agentstest.Conn, _ int, _ string) { c.Send(0x1, make([]byte, maxCodexFrame+1)) },
		"huge length": func(c *agentstest.Conn, _ int, _ string) {
			c.Write([]byte{0x01, 1, 'x', 0x80, 127, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff})
		},
		"not json": func(c *agentstest.Conn, _ int, _ string) { c.Send(0x1, []byte("{oops")) },
		"error reply": func(c *agentstest.Conn, id int, _ string) {
			c.JSON(map[string]any{"id": id, "error": map[string]any{"code": 1, "message": "nope"}})
		},
		"wrong shape": func(c *agentstest.Conn, id int, method string) {
			if method == "initialize" {
				c.JSON(map[string]any{"id": id, "result": map[string]any{}})
			} else {
				c.JSON(map[string]any{"id": id, "result": map[string]any{"data": "not a list"}})
			}
		},
	}
	for name, reply := range cases {
		t.Run(name, func(t *testing.T) {
			f := agentstest.NewFakeCodex(t)
			f.Reply(reply)
			start := time.Now()
			if _, err := readCodexThreads(f.Socket, codexCache{}, "dev"); err == nil {
				t.Fatal("want an error")
			}
			if d := time.Since(start); d > 2*codexTimeout {
				t.Fatalf("took %v, want at most about %v", d, codexTimeout)
			}
		})
	}
}

func TestReadCodexThreadsPingAndNotifications(t *testing.T) {
	f := agentstest.NewFakeCodex(t)
	f.Set(thread("t1", "/a", "idle"))
	f.Reply(func(c *agentstest.Conn, id int, method string) {
		c.Send(0x9, []byte("hi"))
		c.JSON(map[string]any{"method": "thread/status/changed", "params": map[string]any{}})
		f.Reply(nil) // the next requests get the normal answers
		c.JSON(map[string]any{"id": id, "result": map[string]any{}})
	})
	threads, err := readCodexThreads(f.Socket, codexCache{}, "dev")
	if err != nil || len(threads) != 1 {
		t.Fatalf("got %v, %v", threads, err)
	}
}

func TestCodexSessions(t *testing.T) {
	home, f := agentstest.CodexHome(t)
	f.Set(
		thread("busy-one", "/repo/src", "active"),
		thread("named-one", "/b", "idle", "name", "fix the login bug"),
		thread("waiting", "/c", "active", "status", map[string]any{"type": "active", "activeFlags": []string{"waitingOnApproval"}}),
		thread("failed", "/d", "systemError", "preview", ""),
		thread("hidden", "/e", "active", "ephemeral", true),
		thread("sub", "/f", "active", "parentThreadId", "busy-one"),
	)
	sessions, err := codexSessions(codexEnv(t, home))
	if err != nil {
		t.Fatal(err)
	}
	type agent struct {
		cwd  string
		busy bool
	}
	got := map[string]agent{}
	for _, s := range sessions {
		got[s.Name] = agent{s.Cwd, s.Busy}
	}
	want := map[string]agent{
		"prompt of busy-one": {"/repo/src", true},
		"fix the login bug":  {"/b", false},
		"prompt of waiting":  {"/c", false},
		"failed":             {"/d", false}, // no name and no preview: the id
	}
	if len(sessions) != len(want) || !maps.Equal(got, want) {
		t.Fatalf("sessions = %v, want %v", got, want)
	}
}

func TestNoCodexAppServer(t *testing.T) {
	env := codexEnv(t, agentstest.ShortTempDir(t)) // a Codex home, but no app server listens
	if sessions, err := codexSessions(env); len(sessions) != 0 || err != nil || !env.Installed() {
		t.Fatalf("got %v, %v; want nothing, no error, and Codex installed", sessions, err)
	}
}

func TestCodexLastActivity(t *testing.T) {
	home, f := agentstest.CodexHome(t)
	env := codexEnv(t, home)
	last := func() time.Time {
		t.Helper()
		sessions, err := codexSessions(env)
		if err != nil || len(sessions) != 1 {
			t.Fatalf("sessions = %+v, err = %v", sessions, err)
		}
		return sessions[0].Last
	}

	f.Set(thread("t1", "/a", "idle"))
	if got := last(); !got.Equal(time.Unix(1791994219, 0)) {
		t.Fatalf("first seen Idle: %v, want the thread's updatedAt", got)
	}
	f.Set(thread("t1", "/a", "active"))
	if got := last(); time.Since(got) > time.Second {
		t.Fatalf("Busy: %v, want now", got)
	}
	f.Set(thread("t1", "/a", "idle"))
	wentIdle := last()
	if time.Since(wentIdle) > time.Second {
		t.Fatalf("went Idle: %v, want now", wentIdle)
	}
	time.Sleep(10 * time.Millisecond)
	if got := last(); !got.Equal(wentIdle) {
		t.Fatalf("still Idle: %v, want the time it went Idle, %v", got, wentIdle)
	}
	f.Set()
	codexSessions(env)
	if seen := state(env, "codex", func() *codexState { return nil }).seen; len(seen) != 0 {
		t.Fatalf("seen keeps %v after the thread unloaded", seen)
	}
}

func TestThreadsWithoutPromptAreReadOnce(t *testing.T) {
	f := agentstest.NewFakeCodex(t)
	f.Set(thread("prompted", "/a", "active"), thread("new1", "/b", "idle", "preview", ""), thread("new2", "/c", "idle", "preview", ""))
	sent := func() (methods, reads []string) {
		t.Helper()
		for _, r := range f.Take() {
			methods = append(methods, r.Method)
			if r.Method == "thread/read" {
				var p struct {
					ThreadID string `json:"threadId"`
				}
				json.Unmarshal(r.Params, &p)
				reads = append(reads, p.ThreadID)
			}
		}
		return methods, reads
	}
	cache := codexCache{}
	scan := func() []codexThread {
		t.Helper()
		threads, err := readCodexThreads(f.Socket, cache, "dev")
		if err != nil || len(threads) != 3 {
			t.Fatalf("threads = %+v, err = %v", threads, err)
		}
		return threads
	}

	scan()
	if _, reads := sent(); len(reads) != 3 {
		t.Fatalf("first scan read %v, want every thread once", reads)
	}
	if threads := scan(); threads[1].Cwd != "/b" {
		t.Fatalf("a waiting thread keeps its last read: %+v", threads[1])
	}
	methods, reads := sent()
	if !slices.Equal(reads, []string{"prompted"}) || !slices.Contains(methods, "thread/list") {
		t.Fatalf("second scan sent %v and read %v; want one thread/list and only the prompted thread read", methods, reads)
	}
	for _, m := range methods {
		if !slices.Contains([]string{"initialize", "thread/loaded/list", "thread/list", "thread/read"}, m) {
			t.Fatalf("sent %s, a request that is not a read", m)
		}
	}

	f.Set(thread("prompted", "/a", "active"), thread("new1", "/b", "idle", "preview", ""), thread("new2", "/c", "active", "preview", "first prompt"))
	if threads := scan(); !threads[2].busy() || threads[2].Preview != "first prompt" {
		t.Fatalf("after its first prompt new2 = %+v, want it read again", threads[2])
	}
	if _, reads := sent(); !slices.Contains(reads, "new2") || slices.Contains(reads, "new1") {
		t.Fatalf("read %v, want new2 and not new1", reads)
	}

	f.Set(thread("prompted", "/a", "idle"))
	if _, err := readCodexThreads(f.Socket, cache, "dev"); err != nil || len(cache) != 1 {
		t.Fatalf("cache = %v after threads unloaded, err = %v", cache, err)
	}
}

func TestThreadWithoutPromptIsReadAfterNewerStoredThreads(t *testing.T) {
	f := agentstest.NewFakeCodex(t)
	f.Set(thread("new", "/a", "idle", "preview", ""))
	for i := range 3 {
		f.Store(thread(fmt.Sprintf("exec%d", i), "/x", "notLoaded"))
	}
	cache := codexCache{}
	if _, err := readCodexThreads(f.Socket, cache, "dev"); err != nil {
		t.Fatal(err)
	}
	f.Set(thread("new", "/a", "active", "preview", "first prompt"))
	threads, err := readCodexThreads(f.Socket, cache, "dev")
	if err != nil || len(threads) != 1 || !threads[0].busy() {
		t.Fatalf("threads = %+v, err = %v; want new read again after its first prompt", threads, err)
	}
}

func TestCodexBusy(t *testing.T) {
	cases := []struct {
		status string
		flags  []string
		busy   bool
	}{
		{"active", nil, true},
		{"active", []string{"aFutureFlag"}, true},
		{"active", []string{"waitingOnApproval"}, false},
		{"active", []string{"waitingOnUserInput"}, false},
		{"idle", nil, false},
		{"notLoaded", nil, false},
		{"systemError", nil, false},
	}
	for _, c := range cases {
		var th codexThread
		th.Status.Type, th.Status.ActiveFlags = c.status, c.flags
		if th.busy() != c.busy {
			t.Errorf("%s %v: busy = %v, want %v", c.status, c.flags, th.busy(), c.busy)
		}
	}
}
