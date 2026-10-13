package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"
)

func TestClassify(t *testing.T) {
	cases := []struct{ stderr, state, has string }{
		{"abhinav@10.129.63.250: Permission denied (publickey).", stateNeedsLogin, "kijk machine login azvm"},
		{"Host key verification failed.", stateNeedsLogin, "kijk machine login azvm"},
		// An ssh-agent, 1Password or a YubiKey that refuses to sign.
		{"sign_and_send_pubkey: signing failed for ED25519 \"/home/a/.ssh/id.pub\" from agent: agent refused operation\nabhinav@10.129.63.250: Permission denied (publickey).", stateNeedsLogin, "kijk machine login azvm"},
		{"Stdio forwarding request failed: Session open refused by peer", stateError, "AllowTcpForwarding"},
		{"channel 0: open failed: connect failed: Connection refused\nstdio forwarding failed", stateError, "kijk is not running"},
		{"ssh: connect to host 10.129.63.250 port 22: Operation timed out", stateUnreachable, "Operation timed out"},
		{"ssh: Could not resolve hostname nope: nodename nor servname provided, or not known", stateUnreachable, "Could not resolve"},
		{"", stateUnreachable, "no answer"},
	}
	for _, c := range cases {
		state, msg := classify("azvm", &sshError{255, c.stderr})
		if state != c.state || !strings.Contains(msg, c.has) {
			t.Errorf("%q: %s %q", c.stderr, state, msg)
		}
	}
	if state, msg := classify("azvm", &sshError{255, "Stdio forwarding request failed: Session open refused by peer"}); !strings.Contains(msg, "kijk is not running") || state != stateError {
		t.Errorf("forwarding refused must name both causes: %q", msg)
	}
	r := &remoteMachine{name: "azvm"}
	if state, msg := r.classify(&installError{errors.New("kijk-linux-amd64 does not match checksums.txt")}); state != stateInstallFailed || !strings.Contains(msg, "checksums.txt") {
		t.Errorf("install: %s %q", state, msg)
	}
}

func TestParseBoot(t *testing.T) {
	b, err := parseBoot("system Linux x86_64\nserve {\"version\":\"0.9.0\",\"port\":4012,\"token\":\"abc\",\"pid\":7}\n")
	if err != nil || b.system != "Linux x86_64" || b.goos != "linux" || b.goarch != "amd64" || b.serve.Port != 4012 || b.serve.Token != "abc" {
		t.Fatalf("serve: %+v %v", b, err)
	}
	b, err = parseBoot("system Darwin arm64\nneed-install\n")
	if err != nil || b.need != "need-install" || b.goos != "darwin" || b.goarch != "arm64" {
		t.Fatalf("need-install: %+v %v", b, err)
	}
	b, err = parseBoot("system FreeBSD amd64\nstart-failed\nline one\nline two\n")
	if err != nil || b.need != "start-failed" || b.goos != "" || b.log != "line one\nline two" {
		t.Fatalf("start-failed: %+v %v", b, err)
	}
	for _, out := range []string{"", "system Linux x86_64\n", "system Linux x86_64\nserve {broken\n", "system Linux x86_64\nserve {\"port\":0}\n"} {
		if _, err := parseBoot(out); err == nil {
			t.Errorf("%q: want an error", out)
		}
	}
}

func TestSessionMessageWithoutMachines(t *testing.T) {
	var mm *machineManager
	b := sessionMessage([]Row{}, nil, kijkState{Version: "0.9.0"}, mm.views())
	if string(b) != `{"rows":[],"kijk":{"version":"0.9.0","canRestart":false}}` {
		t.Fatalf("local-only message changed: %s", b)
	}
	b = sessionMessage(nil, nil, kijkState{}, []machineView{{Name: "azvm", State: stateDisabled, Rows: []Row{}}})
	if !strings.Contains(string(b), `"machines":[{"name":"azvm","state":"disabled","stale":false,"rows":[]}]`) {
		t.Fatalf("machines: %s", b)
	}
}

func TestMachineViewsAndCounts(t *testing.T) {
	mm := newMachineManager("0.9.0")
	mm.list = []*remoteMachine{
		{name: "azvm", state: stateConnected, rows: []Row{{Names: []string{"a", "b"}}}},
		{name: "old", state: stateUnreachable, rows: []Row{{Names: []string{"c"}}}},
		{name: "off", state: stateDisabled},
	}
	v := mm.views()
	if len(v) != 3 || v[0].Stale || !v[1].Stale || v[2].Stale || v[2].Rows == nil {
		t.Fatalf("views %+v", v)
	}
	if m, a := mm.counts(); m != 3 || a != 2 {
		t.Fatalf("counts = %d, %d", m, a)
	}
	if !mm.settled() {
		t.Fatal("no machine is connecting, so the list is settled")
	}
	mm.list[2].state = stateConnecting
	if mm.settled() {
		t.Fatal("a connecting machine is not settled")
	}
}

// fakeRemote is a remote kijk serve reached over a plain transport instead of ssh.
type fakeRemote struct {
	mu      sync.Mutex
	cookies []string
	origins []string
	paths   []string
}

func startFakeRemote(t *testing.T) (*fakeRemote, *http.Cookie, string) {
	f := &fakeRemote{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.cookies = append(f.cookies, r.Header.Get("Cookie"))
		f.origins = append(f.origins, r.Header.Get("Origin"))
		f.paths = append(f.paths, r.URL.RequestURI())
		f.mu.Unlock()
		switch r.URL.Path {
		case "/api/folder-events":
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, "data: {\"paths\":[\"a\"]}\n\n")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		default:
			http.SetCookie(w, &http.Cookie{Name: "evil", Value: "x"})
			io.WriteString(w, `{"ok":true}`)
		}
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	return f, &http.Cookie{Name: "kijk-4012", Value: "remote-token"}, u.Host
}

func TestBackoff(t *testing.T) {
	for _, c := range []struct{ wait, up, sleep, next time.Duration }{
		{time.Second, 0, time.Second, 2 * time.Second},
		{8 * time.Second, 2 * time.Second, 8 * time.Second, 16 * time.Second},
		{4 * time.Second, 59 * time.Second, 4 * time.Second, 8 * time.Second},
		{4 * time.Second, 60 * time.Second, time.Second, 2 * time.Second},
		{2 * time.Minute, 0, 2 * time.Minute, 2 * time.Minute},
		{2 * time.Minute, 90 * time.Second, time.Second, 2 * time.Second},
	} {
		if sleep, next := backoff(c.wait, c.up); sleep != c.sleep || next != c.next {
			t.Errorf("backoff(%v, %v) = %v, %v; want %v, %v", c.wait, c.up, sleep, next, c.sleep, c.next)
		}
	}
}

func TestDisableWinsOverLateConnect(t *testing.T) {
	mm := newMachineManager("0.9.0")
	r := &remoteMachine{name: "azvm", mm: mm, ssh: newSSHClient("azvm"), state: stateConnecting}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	close(done)
	r.stop, r.done = cancel, done
	r.setEnabled(false)
	r.set(ctx, func() { r.state, r.proxy = stateConnected, http.NotFoundHandler() })
	r.set(ctx, func() { r.rows = []Row{{}} })
	if v := r.view(); v.State != stateDisabled || r.proxy != nil || len(v.Rows) != 0 {
		t.Fatalf("a connect that ends after the disable must change nothing, got %+v", v)
	}
}

func TestMachineProxy(t *testing.T) {
	remote, cookie, addr := startFakeRemote(t)
	mm := newMachineManager("0.9.0")
	t.Cleanup(func() { http.DefaultTransport.(*http.Transport).CloseIdleConnections() })
	connected := &remoteMachine{name: "azvm", mm: mm, state: stateConnected,
		proxy: newRemoteProxy(&http.Transport{}, addr, cookie)}
	down := &remoteMachine{name: "other-vm", mm: mm, state: stateNeedsLogin, message: "The login to other-vm needs you. Run kijk machine login other-vm"}
	mm.list = []*remoteMachine{connected, down}
	srv := newServer("secret", 7717, fstest.MapFS{"index.html": {Data: []byte("hi")}}, newHub(), &roots{}, newUpdater("0.9.0", "", io.Discard), mm)
	do := func(method, target string, withCookie bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, target, strings.NewReader(`{"root":"/x"}`))
		r.Host = "127.0.0.1:7717"
		r.Header.Set("Origin", "http://127.0.0.1:7717")
		if withCookie {
			r.AddCookie(&http.Cookie{Name: cookieName(7717), Value: "secret"})
		}
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		return w
	}

	if w := do("GET", "/m/azvm/api/tree?root=/x", false); w.Code != http.StatusForbidden {
		t.Fatalf("no local token: %d", w.Code)
	}
	w := do("GET", "/m/azvm/api/tree?root=/x&path=a", true)
	if w.Code != http.StatusOK || w.Body.String() != `{"ok":true}` {
		t.Fatalf("tree: %d %q", w.Code, w.Body.String())
	}
	if w.Header().Get("Set-Cookie") != "" || w.Header().Get("X-Content-Type-Options") != "nosniff" ||
		!strings.Contains(strings.Join(w.Header().Values("Content-Security-Policy"), ";"), "sandbox") {
		t.Fatalf("headers %v", w.Header())
	}
	if w := do("POST", "/m/azvm/api/stage", true); w.Code != http.StatusOK {
		t.Fatalf("post: %d %q", w.Code, w.Body.String())
	}
	remote.mu.Lock()
	for i, c := range remote.cookies {
		if c != "kijk-4012=remote-token" || remote.origins[i] != "" {
			t.Fatalf("the remote got cookie %q, origin %q", c, remote.origins[i])
		}
	}
	if remote.paths[0] != "/api/tree?root=/x&path=a" {
		t.Fatalf("path %q", remote.paths[0])
	}
	remote.mu.Unlock()

	for _, p := range []string{"/m/azvm/api/update", "/m/azvm/api/restart", "/m/nope/api/tree"} {
		if w := do("POST", p, true); w.Code != http.StatusNotFound {
			t.Errorf("%s: %d", p, w.Code)
		}
	}
	if w := do("GET", "/m/azvm/index.html", true); w.Code == http.StatusOK && w.Body.String() != "hi" {
		t.Fatalf("a remote path outside /api/ was served: %q", w.Body.String())
	}
	if w := do("GET", "/m/other-vm/api/tree", true); w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "kijk machine login other-vm") {
		t.Fatalf("not connected: %d %q", w.Code, w.Body.String())
	}
}

func TestMachineProxyStreamsEvents(t *testing.T) {
	_, cookie, addr := startFakeRemote(t)
	mm := newMachineManager("0.9.0")
	mm.list = []*remoteMachine{{name: "azvm", mm: mm, state: stateConnected, proxy: newRemoteProxy(&http.Transport{}, addr, cookie)}}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	srv := &http.Server{Handler: newServer("secret", port, nil, newHub(), &roots{}, nil, mm)}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })

	req, _ := http.NewRequest("GET", "http://"+ln.Addr().String()+"/m/azvm/api/folder-events?root=/x", nil)
	req.AddCookie(&http.Cookie{Name: cookieName(port), Value: "secret"})
	resp, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	if err != nil || line != "data: {\"paths\":[\"a\"]}\n" {
		t.Fatalf("the event did not pass through at once: %q %v", line, err)
	}
}

func TestHeadlessServerHasNoUpdateRoutes(t *testing.T) {
	srv := newServer("secret", 7717, nil, newHub(), &roots{}, nil, nil)
	for _, p := range []string{"/api/update", "/api/restart", "/m/azvm/api/tree"} {
		r := httptest.NewRequest("POST", p, nil)
		r.Host = "127.0.0.1:7717"
		r.AddCookie(&http.Cookie{Name: cookieName(7717), Value: "secret"})
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		if w.Code != http.StatusNotFound && w.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: %d", p, w.Code)
		}
	}
	r := httptest.NewRequest("GET", "/api/ping", nil)
	r.Host = "127.0.0.1:7717"
	r.AddCookie(&http.Cookie{Name: cookieName(7717), Value: "secret"})
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	if w.Body.String() != "kijk" {
		t.Fatalf("ping: %q", w.Body.String())
	}
}

func TestServeExitsWhenIdle(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})}
	done := make(chan error, 1)
	go func() { done <- serveUntilIdle(srv, ln, 300*time.Millisecond) }()

	// An open connection keeps it running.
	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
		t.Fatal("exited with a connection open")
	case <-time.After(time.Second):
	}
	c.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("did not exit after the last connection closed")
	}
}

func TestServeLock(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("kijk serve does not run on Windows")
	}
	p := t.TempDir() + "/serve.lock"
	unlock, err := lockFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lockFile(p); !errors.Is(err, errLocked) {
		t.Fatalf("second lock: %v", err)
	}
	unlock()
	again, err := lockFile(p)
	if err != nil {
		t.Fatalf("after unlock: %v", err)
	}
	again()
}

func TestServeStateJSON(t *testing.T) {
	b, _ := json.Marshal(serveState{"0.9.0", 4012, "abc", 7})
	if string(b) != `{"version":"0.9.0","port":4012,"token":"abc","pid":7}` {
		t.Fatalf("the bootstrap script reads these keys with sed: %s", b)
	}
}

// A broken machines.json keeps the machines of a running kijk, so a typo logs none of them out.
func TestManagerKeepsMachinesOnBrokenFile(t *testing.T) {
	useConfigDir(t)
	p, _ := machinesPath()
	os.MkdirAll(filepath.Dir(p), 0o700)
	var log bytes.Buffer
	mm := newMachineManager("dev")
	mm.log = &log
	mod := time.Now()
	reload := func(content string) []string {
		os.WriteFile(p, []byte(content), 0o600)
		mod = mod.Add(time.Second)
		os.Chtimes(p, mod, mod)
		mm.reload()
		var names []string
		for _, v := range mm.views() {
			names = append(names, v.Name)
		}
		return names
	}
	if got := reload(`{"machines":[{"target":"azvm","enabled":false}]}`); !slices.Equal(got, []string{"azvm"}) {
		t.Fatalf("good file: %v", got)
	}
	for _, broken := range []string{`{"machines":`, `{"machines":[{"target":"azvm","enabled":"yes"}]}`} {
		log.Reset()
		if got := reload(broken); !slices.Equal(got, []string{"azvm"}) {
			t.Errorf("%s: machines %v", broken, got)
		}
		if !strings.Contains(log.String(), p) || !strings.Contains(log.String(), "keeps the machines") {
			t.Errorf("%s: log %q", broken, log.String())
		}
	}
	if got := reload(`{"machines":[]}`); len(got) != 0 {
		t.Fatalf("empty list: %v", got)
	}
}

func TestParseSSHConfig(t *testing.T) {
	out := "User abhinav\nhostname 10.0.0.5\nport 2222\nidentityfile ~/.ssh/id_ed25519\nproxyjump bastion\nproxycommand none\n"
	want := sshLogin{user: "abhinav", host: "10.0.0.5", port: "2222", jump: "proxyjump bastion"}
	if got := parseSSHConfig(out); got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if got := parseSSHConfig("user a\nhostname h\nport 22\n"); got != (sshLogin{user: "a", host: "h", port: "22"}) {
		t.Fatalf("no jump host: %+v", got)
	}
}
