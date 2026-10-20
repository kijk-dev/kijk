package main

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"maps"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeCodex is a Codex app server for tests: it answers from threads and records each request.
type fakeCodex struct {
	socket  string
	mu      sync.Mutex
	threads []map[string]any
	stored  []map[string]any // stored threads this app server has not loaded, listed before its own
	methods []string
	params  []json.RawMessage
	// reply, when set, answers instead of the normal handler. It may send nothing.
	reply func(c *fakeConn, id int, method string)
}

// shortTempDir is for socket paths, which must stay under about 104 bytes; t.TempDir() is longer on macOS.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "kc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func newFakeCodex(t *testing.T) *fakeCodex {
	t.Helper()
	return newFakeCodexAt(t, filepath.Join(shortTempDir(t), "s.sock"))
}

// newFakeCodexAt listens on socket, such as the one kijk looks for in a Codex home.
func newFakeCodexAt(t *testing.T, socket string) *fakeCodex {
	t.Helper()
	f := &fakeCodex{socket: socket}
	ln, err := net.Listen("unix", f.socket)
	if err != nil {
		t.Skipf("no Unix sockets here: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(nc)
		}
	}()
	return f
}

func (f *fakeCodex) set(threads ...map[string]any) {
	f.mu.Lock()
	f.threads = threads
	f.mu.Unlock()
}

func (f *fakeCodex) seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.methods)
}

type fakeConn struct {
	nc net.Conn
	r  *bufio.Reader
}

// send writes one unmasked frame, as a server does.
func (c *fakeConn) send(op byte, payload []byte) {
	hdr := []byte{0x80 | op}
	switch n := len(payload); {
	case n < 126:
		hdr = append(hdr, byte(n))
	case n < 1<<16:
		hdr = append(hdr, 126)
		hdr = binary.BigEndian.AppendUint16(hdr, uint16(n))
	default:
		hdr = append(hdr, 127)
		hdr = binary.BigEndian.AppendUint64(hdr, uint64(n))
	}
	c.nc.Write(append(hdr, payload...))
}

func (c *fakeConn) json(v any) {
	b, _ := json.Marshal(v)
	c.send(0x1, b)
}

func (f *fakeCodex) serve(nc net.Conn) {
	defer nc.Close()
	r := bufio.NewReader(nc)
	req, err := http.ReadRequest(r)
	if err != nil {
		return
	}
	sum := sha1.Sum([]byte(req.Header.Get("Sec-WebSocket-Key") + wsGUID))
	nc.Write([]byte("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + base64.StdEncoding.EncodeToString(sum[:]) + "\r\n\r\n"))
	c := &fakeConn{nc, r}
	// codexConn.read unmasks client frames, so the fake reuses it.
	in := &codexConn{nc: nc, r: r}
	for {
		b, err := in.read()
		if err != nil {
			return
		}
		var m struct {
			ID     int             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		json.Unmarshal(b, &m)
		f.mu.Lock()
		f.methods = append(f.methods, m.Method)
		f.params = append(f.params, m.Params)
		reply, threads, stored := f.reply, f.threads, f.stored
		f.mu.Unlock()
		if reply != nil {
			reply(c, m.ID, m.Method)
			continue
		}
		switch m.Method {
		case "initialize":
			c.json(map[string]any{"id": m.ID, "result": map[string]any{"userAgent": "fake"}})
		case "thread/loaded/list":
			ids := []string{}
			for _, t := range threads {
				ids = append(ids, t["id"].(string))
			}
			c.json(map[string]any{"id": m.ID, "result": map[string]any{"data": ids, "nextCursor": nil}})
		case "thread/list":
			// Codex stores a thread at its first prompt; only stored threads are listed.
			data := slices.Clone(stored)
			for _, t := range threads {
				if t["preview"] != "" {
					data = append(data, t)
				}
			}
			var p struct {
				Limit int `json:"limit"`
			}
			json.Unmarshal(m.Params, &p)
			data = data[:min(p.Limit, len(data))]
			c.json(map[string]any{"id": m.ID, "result": map[string]any{"data": data, "nextCursor": nil}})
		case "thread/read":
			var p struct {
				ThreadID string `json:"threadId"`
			}
			json.Unmarshal(m.Params, &p)
			for _, t := range threads {
				if t["id"] == p.ThreadID {
					c.json(map[string]any{"id": m.ID, "result": map[string]any{"thread": t}})
				}
			}
		default:
			c.json(map[string]any{"id": m.ID, "error": map[string]any{"code": -32601, "message": "unknown method"}})
		}
	}
}

func thread(id, cwd, status string, extra ...any) map[string]any {
	t := map[string]any{
		"id": id, "cwd": cwd, "name": nil, "preview": "prompt of " + id, "ephemeral": false,
		"parentThreadId": nil, "updatedAt": 1791994219, "status": map[string]any{"type": status},
		"cliVersion": "0.162.1", "futureField": map[string]any{"x": 1},
	}
	if status == "active" {
		t["status"] = map[string]any{"type": "active", "activeFlags": []string{}}
	}
	for i := 0; i+1 < len(extra); i += 2 {
		t[extra[i].(string)] = extra[i+1]
	}
	return t
}

func TestCodexHome(t *testing.T) {
	t.Setenv("CODEX_HOME", "/custom")
	if codexHome() != "/custom" {
		t.Fatal(codexHome())
	}
	if want := filepath.Join("/custom", "app-server-control", "app-server-control.sock"); codexSocket(codexHome()) != want {
		t.Fatal(codexSocket(codexHome()))
	}
	t.Setenv("CODEX_HOME", "")
	if home, _ := os.UserHomeDir(); codexHome() != filepath.Join(home, ".codex") {
		t.Fatal(codexHome())
	}
}

func TestReadCodexThreads(t *testing.T) {
	f := newFakeCodex(t)
	f.set(thread("t1", "/a", "idle"), thread("t2", "/b", "active", "name", "named"))
	threads, err := readCodexThreads(f.socket, codexCache{})
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) != 2 || threads[0].ID != "t1" || threads[0].Cwd != "/a" || threads[0].busy() || !threads[1].busy() || *threads[1].Name != "named" {
		t.Fatalf("threads = %+v", threads)
	}
	if got := f.seen(); !slices.Equal(got, []string{"initialize", "thread/loaded/list", "thread/read", "thread/read"}) {
		t.Fatalf("methods = %v, want only the three read requests", got)
	}
	var init struct {
		ClientInfo   map[string]string `json:"clientInfo"`
		Capabilities any               `json:"capabilities"`
	}
	json.Unmarshal(f.params[0], &init)
	if init.ClientInfo["name"] != "kijk" || init.Capabilities != nil || strings.Contains(string(f.params[0]), "capabilities") {
		t.Fatalf("initialize params = %s, want clientInfo only", f.params[0])
	}
}

func TestReadCodexThreadsNoServer(t *testing.T) {
	dir := shortTempDir(t)
	stale := filepath.Join(dir, "stale.sock")
	ln, err := net.Listen("unix", stale)
	if err != nil {
		t.Skipf("no Unix sockets here: %v", err)
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	ln.Close()
	for _, socket := range []string{filepath.Join(dir, "missing.sock"), stale} {
		threads, err := readCodexThreads(socket, codexCache{})
		if threads != nil || err != nil {
			t.Fatalf("%s: got %v, %v; want no threads and no error", socket, threads, err)
		}
	}
}

func TestReadCodexThreadsForbiddenSocket(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("file modes do not stop this user from connecting")
	}
	f := newFakeCodex(t)
	if err := os.Chmod(f.socket, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := readCodexThreads(f.socket, codexCache{}); err == nil {
		t.Fatal("an app server kijk may not reach is an error, not the absence of Codex")
	}
}

func TestReadCodexThreadsBadServers(t *testing.T) {
	cases := map[string]func(c *fakeConn, id int, method string){
		"hangs":     func(*fakeConn, int, string) {},
		"closes":    func(c *fakeConn, _ int, _ string) { c.send(0x8, nil) },
		"too large": func(c *fakeConn, _ int, _ string) { c.send(0x1, make([]byte, maxCodexFrame+1)) },
		"huge length": func(c *fakeConn, _ int, _ string) {
			c.nc.Write([]byte{0x01, 1, 'x', 0x80, 127, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff})
		},
		"not json": func(c *fakeConn, _ int, _ string) { c.send(0x1, []byte("{oops")) },
		"error reply": func(c *fakeConn, id int, _ string) {
			c.json(map[string]any{"id": id, "error": map[string]any{"code": 1, "message": "nope"}})
		},
		"wrong shape": func(c *fakeConn, id int, method string) {
			if method == "initialize" {
				c.json(map[string]any{"id": id, "result": map[string]any{}})
			} else {
				c.json(map[string]any{"id": id, "result": map[string]any{"data": "not a list"}})
			}
		},
	}
	for name, reply := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFakeCodex(t)
			f.reply = reply
			start := time.Now()
			if _, err := readCodexThreads(f.socket, codexCache{}); err == nil {
				t.Fatal("want an error")
			}
			if d := time.Since(start); d > 2*codexTimeout {
				t.Fatalf("took %v, want at most about %v", d, codexTimeout)
			}
		})
	}
}

func TestReadCodexThreadsPingAndNotifications(t *testing.T) {
	f := newFakeCodex(t)
	f.set(thread("t1", "/a", "idle"))
	f.reply = func(c *fakeConn, id int, method string) {
		c.send(0x9, []byte("hi"))
		c.json(map[string]any{"method": "thread/status/changed", "params": map[string]any{}})
		f.reply = nil // the next requests get the normal answers
		c.json(map[string]any{"id": id, "result": map[string]any{}})
	}
	threads, err := readCodexThreads(f.socket, codexCache{})
	if err != nil || len(threads) != 1 {
		t.Fatalf("got %v, %v", threads, err)
	}
}

// codexScanner reads Codex from f and Claude Code from claude, with a Codex home that exists.
func codexScanner(t *testing.T, f *fakeCodex, claude string) *scanner {
	s := newScanner(claude, t.TempDir())
	s.codexSock = f.socket
	return s
}

func TestScanCodexOnly(t *testing.T) {
	repo := gitRepo(t)
	os.MkdirAll(filepath.Join(repo, "src"), 0o755)
	f := newFakeCodex(t)
	f.set(
		thread("busy-one", filepath.Join(repo, "src"), "active"),
		thread("named-one", t.TempDir(), "idle", "name", "fix the login bug"),
		thread("waiting", t.TempDir(), "active", "status", map[string]any{"type": "active", "activeFlags": []string{"waitingOnApproval"}}),
		thread("failed", t.TempDir(), "systemError", "preview", ""),
		thread("hidden", t.TempDir(), "active", "ephemeral", true),
		thread("sub", t.TempDir(), "active", "parentThreadId", "busy-one"),
	)
	rows, err, codexErr := codexScanner(t, f, t.TempDir()).scan()
	if err != nil || codexErr != nil {
		t.Fatalf("a missing Claude folder next to a Codex home is no error: %v, %v", err, codexErr)
	}
	got := map[string]RowAgent{}
	for _, r := range rows {
		got[r.Names[0]] = r.Agents[0]
		if r.Names[0] == "prompt of busy-one" {
			samePath(t, r.Root, repo)
			if r.Cwd != "src" || r.Branch != "main" || !r.Busy {
				t.Fatalf("busy row = %+v", r)
			}
		}
	}
	want := map[string]RowAgent{
		"prompt of busy-one": {"codex", true},
		"fix the login bug":  {"codex", false},
		"prompt of waiting":  {"codex", false},
		"failed":             {"codex", false}, // no name and no preview: the id
	}
	if len(rows) != len(want) || !maps.Equal(got, want) {
		t.Fatalf("rows = %v, want %v", got, want)
	}
}

func TestScanClaudeAndCodexInOneFolder(t *testing.T) {
	claude := t.TempDir()
	repo := gitRepo(t)
	write(t, filepath.Join(claude, "sessions", "1.json"),
		strings.Replace(sessionJSON(os.Getpid(), "s1", repo, "auth-fix", "idle", time.Now().UnixMilli()),
			`"futureField"`, fmt.Sprintf(`"statusUpdatedAt":%d,"futureField"`, time.Now().Add(-time.Hour).UnixMilli()), 1))
	f := newFakeCodex(t)
	f.set(thread("t1", repo, "active", "name", "fix the login bug"))
	rows, err, codexErr := codexScanner(t, f, claude).scan()
	if err != nil || codexErr != nil || len(rows) != 1 {
		t.Fatalf("rows = %+v, err = %v, %v", rows, err, codexErr)
	}
	r := rows[0]
	if !slices.Equal(r.Names, []string{"fix the login bug", "auth-fix"}) || !slices.Equal(r.Agents, []RowAgent{{"codex", true}, {"claude", false}}) || !r.Busy {
		t.Fatalf("row = %+v, want the busy Codex agent first", r)
	}
}

func TestScanBrokenCodexKeepsClaude(t *testing.T) {
	claude := t.TempDir()
	write(t, filepath.Join(claude, "sessions", "1.json"), sessionJSON(os.Getpid(), "s1", t.TempDir(), "docs", "idle", time.Now().UnixMilli()))
	f := newFakeCodex(t)
	f.reply = func(c *fakeConn, id int, _ string) { c.send(0x1, []byte("{oops")) }
	rows, err, codexErr := codexScanner(t, f, claude).scan()
	if err != nil || codexErr == nil || len(rows) != 1 || rows[0].Names[0] != "docs" {
		t.Fatalf("rows = %+v, err = %v, codexErr = %v", rows, err, codexErr)
	}
}

func TestScanNoCodexAppServer(t *testing.T) {
	s := newScanner(t.TempDir(), shortTempDir(t)) // a Codex home, but no app server listens
	rows, err, codexErr := s.scan()
	if len(rows) != 0 || err != nil || codexErr != nil {
		t.Fatalf("got %v, %v, %v; want nothing and no error", rows, err, codexErr)
	}
}

func TestCodexLastActivity(t *testing.T) {
	f := newFakeCodex(t)
	s := codexScanner(t, f, t.TempDir())
	cwd := t.TempDir()
	last := func() time.Time {
		t.Helper()
		rows, _, err := s.scan()
		if err != nil || len(rows) != 1 {
			t.Fatalf("rows = %+v, err = %v", rows, err)
		}
		return time.UnixMilli(rows[0].LastActivity)
	}

	f.set(thread("t1", cwd, "idle"))
	if got := last(); !got.Equal(time.Unix(1791994219, 0)) {
		t.Fatalf("first seen Idle: %v, want the thread's updatedAt", got)
	}
	f.set(thread("t1", cwd, "active"))
	if got := last(); time.Since(got) > time.Second {
		t.Fatalf("Busy: %v, want now", got)
	}
	f.set(thread("t1", cwd, "idle"))
	wentIdle := last()
	if time.Since(wentIdle) > time.Second {
		t.Fatalf("went Idle: %v, want now", wentIdle)
	}
	time.Sleep(10 * time.Millisecond)
	if got := last(); !got.Equal(wentIdle) {
		t.Fatalf("still Idle: %v, want the time it went Idle, %v", got, wentIdle)
	}
	f.set()
	s.scan()
	if len(s.codexSeen) != 0 {
		t.Fatalf("codexSeen keeps %v after the thread unloaded", s.codexSeen)
	}
}

func TestThreadsWithoutPromptAreReadOnce(t *testing.T) {
	f := newFakeCodex(t)
	f.set(thread("prompted", "/a", "active"), thread("new1", "/b", "idle", "preview", ""), thread("new2", "/c", "idle", "preview", ""))
	sent := func() (methods, reads []string) {
		t.Helper()
		f.mu.Lock()
		defer f.mu.Unlock()
		for i, m := range f.methods {
			methods = append(methods, m)
			if m == "thread/read" {
				var p struct {
					ThreadID string `json:"threadId"`
				}
				json.Unmarshal(f.params[i], &p)
				reads = append(reads, p.ThreadID)
			}
		}
		f.methods, f.params = nil, nil
		return methods, reads
	}
	cache := codexCache{}
	scan := func() []codexThread {
		t.Helper()
		threads, err := readCodexThreads(f.socket, cache)
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

	f.set(thread("prompted", "/a", "active"), thread("new1", "/b", "idle", "preview", ""), thread("new2", "/c", "active", "preview", "first prompt"))
	if threads := scan(); !threads[2].busy() || threads[2].Preview != "first prompt" {
		t.Fatalf("after its first prompt new2 = %+v, want it read again", threads[2])
	}
	if _, reads := sent(); !slices.Contains(reads, "new2") || slices.Contains(reads, "new1") {
		t.Fatalf("read %v, want new2 and not new1", reads)
	}

	f.set(thread("prompted", "/a", "idle"))
	if _, err := readCodexThreads(f.socket, cache); err != nil || len(cache) != 1 {
		t.Fatalf("cache = %v after threads unloaded, err = %v", cache, err)
	}
}

func TestThreadWithoutPromptIsReadAfterNewerStoredThreads(t *testing.T) {
	f := newFakeCodex(t)
	f.set(thread("new", "/a", "idle", "preview", ""))
	for i := range 3 {
		f.stored = append(f.stored, thread(fmt.Sprintf("exec%d", i), "/x", "notLoaded"))
	}
	cache := codexCache{}
	if _, err := readCodexThreads(f.socket, cache); err != nil {
		t.Fatal(err)
	}
	f.set(thread("new", "/a", "active", "preview", "first prompt"))
	threads, err := readCodexThreads(f.socket, cache)
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
