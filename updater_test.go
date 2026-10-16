package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"
)

func TestLatestKnownAsksOncePerDay(t *testing.T) {
	dir := useConfigDir(t)
	f := startFakeRelease(t, "0.3.0", nil)
	now := time.Now()

	if v, err := latestKnown(now); err != nil || v != "0.3.0" {
		t.Fatalf("fresh: %q, %v", v, err)
	}
	f.latest = "0.4.0"
	if v, _ := latestKnown(now.Add(23 * time.Hour)); v != "0.3.0" || f.lookups.Load() != 1 {
		t.Fatalf("within a day: %q after %d lookups", v, f.lookups.Load())
	}
	if v, _ := latestKnown(now.Add(25 * time.Hour)); v != "0.4.0" || f.lookups.Load() != 2 {
		t.Fatalf("a day later: %q after %d lookups", v, f.lookups.Load())
	}

	p := filepath.Join(dir, "kijk", "update-check.json")
	os.WriteFile(p, []byte("{broken"), 0o600)
	if v, _ := latestKnown(now); v != "0.4.0" || f.lookups.Load() != 3 {
		t.Fatalf("broken file: %q after %d lookups", v, f.lookups.Load())
	}
	var c checkFile
	if b, _ := os.ReadFile(p); json.Unmarshal(b, &c) != nil || c.Latest != "0.4.0" {
		t.Fatalf("file not rewritten: %+v", c)
	}
}

func TestLatestKnownOfflineLeavesTheFile(t *testing.T) {
	useConfigDir(t)
	t.Setenv("KIJK_RELEASES_URL", "http://127.0.0.1:1")
	if _, err := latestKnown(time.Now()); err != errOffline {
		t.Fatalf("err = %v", err)
	}
	p, _ := checkPath()
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("a failed check wrote the file, so the next check would wait a day")
	}
}

func TestTwoKijksShareOneCheck(t *testing.T) {
	useConfigDir(t)
	f := startFakeRelease(t, "0.3.0", nil)
	latestKnown(time.Now())
	latestKnown(time.Now()) // a second kijk on another port
	if f.lookups.Load() != 1 {
		t.Fatalf("lookups = %d", f.lookups.Load())
	}
}

func TestFoundLatestPrintsOncePerVersion(t *testing.T) {
	var out bytes.Buffer
	u := newUpdater("0.2.0", "", &out)
	u.foundLatest("0.3.0")
	u.foundLatest("0.3.0")
	u.foundLatest("0.2.0")
	if got := out.String(); got != "kijk 0.3.0 is out. Run kijk update to install it.\n" {
		t.Fatalf("terminal: %q", got)
	}
	if u.state().Latest != "0.3.0" {
		t.Fatalf("state: %+v", u.state())
	}
}

func TestUpdateCheckSwitches(t *testing.T) {
	t.Setenv("KIJK_NO_UPDATE_CHECK", "")
	if !updateCheckOn(false) || updateCheckOn(true) {
		t.Fatal("flag")
	}
	t.Setenv("KIJK_NO_UPDATE_CHECK", "1")
	if updateCheckOn(false) {
		t.Fatal("environment variable")
	}
}

func TestWatchBinarySeesANewerKijk(t *testing.T) {
	exe := oldKijk(t)
	u := newUpdater("0.2.0", exe, &bytes.Buffer{})
	t.Setenv("KIJK_TEST_VERSION", "0.3.0")
	u.watchBinary()
	if u.state().Installed != "" {
		t.Fatal("an unchanged binary counted as installed")
	}
	u.set(func(s *kijkState) { s.Error = "no write access to /usr/local/bin" })
	os.WriteFile(exe, selfBytes(), 0o755)
	later := time.Now().Add(time.Minute)
	os.Chtimes(exe, later, later)
	u.watchBinary()
	if st := u.state(); st.Installed != "0.3.0" || st.Error != "" {
		t.Fatalf("state: %+v", st)
	}
}

func TestOnlyOneActionStarts(t *testing.T) {
	u := newUpdater("0.2.0", "", &bytes.Buffer{})
	var started atomic.Int32
	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if u.begin("updating") {
				started.Add(1)
			}
		}()
	}
	wg.Wait()
	if n := started.Load(); n != 1 {
		t.Fatalf("%d actions started", n)
	}
}

func TestSessionMessageCarriesTheStatusBar(t *testing.T) {
	u := newUpdater("0.2.0", "", &bytes.Buffer{})
	u.foundLatest("0.3.0")
	h := newHub()
	h.publish(windowAgents, sessionMessage([]Row{}, nil, nil, u.state(), nil))
	srv := httptest.NewServer(http.HandlerFunc(h.serveEvents))
	defer srv.Close()
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	line, _ := bufio.NewReader(resp.Body).ReadString('\n')
	var msg struct {
		Rows []Row     `json:"rows"`
		Kijk kijkState `json:"kijk"`
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(strings.TrimSpace(line), "data: ")), &msg); err != nil {
		t.Fatalf("%q: %v", line, err)
	}
	if msg.Kijk.Version != "0.2.0" || msg.Kijk.Latest != "0.3.0" || msg.Kijk.CanRestart != canRestart || msg.Rows == nil {
		t.Fatalf("message: %+v", msg)
	}
}

// post sends an action to a kijk server the way the window does.
func post(srv http.Handler, path, cookie string) int {
	r := httptest.NewRequest("POST", path, nil)
	r.Host = "127.0.0.1:7717"
	if cookie != "" {
		r.AddCookie(&http.Cookie{Name: cookieName(7717), Value: cookie})
	}
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	return w.Code
}

func waitIdle(t *testing.T, u *updater) kijkState {
	t.Helper()
	for range 1000 {
		if st := u.state(); st.Busy == "" {
			return st
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the action did not finish")
	return kijkState{}
}

func TestUpdateFromTheWindow(t *testing.T) {
	f := startFakeRelease(t, "0.3.0", releaseOf(selfBytes()))
	t.Setenv("KIJK_TEST_VERSION", "0.3.0")
	exe := oldKijk(t)
	u := newUpdater("0.2.0", exe, &bytes.Buffer{})
	srv := newServer("secret", 7717, fstest.MapFS{}, newHub(), &roots{}, u, nil, nil)

	if c := post(srv, "/api/update", ""); c != http.StatusForbidden || f.lookups.Load() != 0 {
		t.Fatalf("without the token: code %d, %d lookups", c, f.lookups.Load())
	}
	if c := post(srv, "/api/update", "secret"); c != http.StatusAccepted {
		t.Fatalf("code %d", c)
	}
	if u.state().Busy != "updating" {
		t.Fatalf("state while updating: %+v", u.state())
	}
	post(srv, "/api/update", "secret") // a second click, from another window
	st := waitIdle(t, u)
	if st.Error != "" || st.Installed != "0.3.0" || f.lookups.Load() != 1 {
		t.Fatalf("state %+v after %d lookups", st, f.lookups.Load())
	}
}

func TestUpdateFromTheWindowFails(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("folder permissions do not stop this user")
	}
	startFakeRelease(t, "0.3.0", releaseOf(selfBytes()))
	exe := oldKijk(t)
	dir := filepath.Dir(exe)
	os.Chmod(dir, 0o555)
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	u := newUpdater("0.2.0", exe, &bytes.Buffer{})
	srv := newServer("secret", 7717, fstest.MapFS{}, newHub(), &roots{}, u, nil, nil)
	post(srv, "/api/update", "secret")
	if st := waitIdle(t, u); st.Error != "no write access to "+dir || st.Installed != "" {
		t.Fatalf("state: %+v", st)
	}
	stillWorks(t, exe)
}

func TestUpdateFromTheWindowRefusesDevBuild(t *testing.T) {
	u := newUpdater("dev (7715b0f)", "", &bytes.Buffer{})
	srv := newServer("secret", 7717, fstest.MapFS{}, newHub(), &roots{}, u, nil, nil)
	if c := post(srv, "/api/update", "secret"); c != http.StatusConflict {
		t.Fatalf("code %d", c)
	}
}

func TestRestartAction(t *testing.T) {
	u := newUpdater("0.2.0", "", &bytes.Buffer{})
	restarted := make(chan bool, 1)
	u.restart = func() error { restarted <- true; return os.ErrPermission }
	srv := newServer("secret", 7717, fstest.MapFS{}, newHub(), &roots{}, u, nil, nil)

	if !canRestart {
		if c := post(srv, "/api/restart", "secret"); c != http.StatusNotFound || u.state().CanRestart {
			t.Fatalf("Windows: code %d, state %+v", c, u.state())
		}
		return
	}
	if c := post(srv, "/api/restart", "secret"); c != http.StatusConflict {
		t.Fatalf("nothing installed: code %d", c)
	}
	u.set(func(s *kijkState) { s.Installed = "0.3.0" })
	if c := post(srv, "/api/restart", "secret"); c != http.StatusAccepted || u.state().Busy != "restarting" {
		t.Fatalf("code %d, state %+v", c, u.state())
	}
	<-restarted
	if st := waitIdle(t, u); !strings.HasPrefix(st.Error, "restart failed") {
		t.Fatalf("a failed restart: %+v", st)
	}
}

func TestRestartTokenDoesNotReachChildren(t *testing.T) {
	if os.Getenv("KIJK_TEST_PRINT_TOKEN") != "" {
		os.Stdout.WriteString("token=" + os.Getenv("KIJK_TOKEN"))
		return
	}
	t.Setenv("KIJK_TOKEN", "secret")
	t.Setenv("KIJK_RESTARTED", "1")
	token, restarted := takeRestartEnv()
	if token != "secret" || !restarted {
		t.Fatalf("got %q, %v", token, restarted)
	}
	self, _ := os.Executable()
	cmd := exec.Command(self, "-test.run=TestRestartTokenDoesNotReachChildren")
	cmd.Env = append(os.Environ(), "KIJK_TEST_PRINT_TOKEN=1")
	out, err := cmd.Output()
	if err != nil || !strings.HasPrefix(string(out), "token=") || strings.Contains(string(out), "secret") {
		t.Fatalf("child saw: %q, %v", out, err)
	}
}
