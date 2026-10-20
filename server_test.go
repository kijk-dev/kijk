package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/kijk-dev/kijk/internal/viewer"
)

func TestGuard(t *testing.T) {
	web := fstest.MapFS{"index.html": {Data: []byte("hi")}}
	srv := newServer("secret", 7717, web, newHub(), &viewer.Roots{}, newUpdater("0.2.0", "", io.Discard), nil, nil)
	do := func(host, target string, cookie string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", target, nil)
		r.Host = host
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: cookieName(7717), Value: cookie})
		}
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		return w
	}

	for _, host := range []string{"evil.example:7717", "127.0.0.1:9999", "127.0.0.1"} {
		if c := do(host, "/", "secret").Code; c != http.StatusForbidden {
			t.Errorf("host %q: code %d", host, c)
		}
	}
	if c := do("127.0.0.1:7717", "/", "").Code; c != http.StatusForbidden {
		t.Errorf("no cookie: code %d", c)
	}
	if c := do("127.0.0.1:7717", "/", "wrong").Code; c != http.StatusForbidden {
		t.Errorf("wrong cookie: code %d", c)
	}
	if c := do("127.0.0.1:7717", "/?token=wrong", "").Code; c != http.StatusForbidden {
		t.Errorf("wrong token: code %d", c)
	}

	w := do("localhost:7717", "/?token=secret", "")
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/" {
		t.Fatalf("token exchange: %d %q", w.Code, w.Header().Get("Location"))
	}
	if w := do("localhost:7717", "/?token=secret&window=folders&open=%2Fcode%2Fapi", ""); w.Header().Get("Location") != "/?open=%2Fcode%2Fapi&window=folders" {
		t.Errorf("other parameters lost: %q", w.Header().Get("Location"))
	}
	c := w.Result().Cookies()
	if len(c) != 1 || c[0].Name != "kijk-7717" || c[0].Value != "secret" || !c[0].HttpOnly || c[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("cookie = %+v", c)
	}
	if w := do("127.0.0.1:7717", "/", "secret"); w.Code != http.StatusOK || w.Body.String() != "hi" {
		t.Fatalf("with cookie: %d %q", w.Code, w.Body.String())
	} else if csp := w.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("CSP = %q", csp)
	}

	post := func(origin string) (int, string) {
		r := httptest.NewRequest("POST", "/api/commit", strings.NewReader(`{"root":"/nope"}`))
		r.Host = "127.0.0.1:7717"
		r.Header.Set("Origin", origin)
		r.AddCookie(&http.Cookie{Name: cookieName(7717), Value: "secret"})
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		return w.Code, w.Body.String()
	}
	if c, body := post("http://evil.example"); c != http.StatusForbidden || !strings.Contains(body, "origin") {
		t.Errorf("foreign origin: %d %q", c, body)
	}
	if _, body := post("http://127.0.0.1:7717"); !strings.Contains(body, "not open in kijk") {
		t.Errorf("own origin should pass the guard: %q", body)
	}
}

func TestSavedFolders(t *testing.T) {
	useConfigDir(t)
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hi\n"), 0o644)
	p, _ := foldersPath()
	h, rts := newHub(), &viewer.Roots{}
	fl := newFolderList(p, rts)
	srv := newServer("secret", 7717, nil, h, rts, nil, nil, fl)
	do := func(method, target, body string) (int, string) {
		r := httptest.NewRequest(method, target, strings.NewReader(body))
		r.Host = "127.0.0.1:7717"
		r.AddCookie(&http.Cookie{Name: cookieName(7717), Value: "secret"})
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		return w.Code, w.Body.String()
	}
	file := "/api/file?" + url.Values{"root": {dir}, "path": {"a.txt"}}.Encode()
	if c, _ := do("GET", file, ""); c != http.StatusForbidden {
		t.Fatalf("folder not in the list: %d", c)
	}
	body, _ := json.Marshal(map[string]any{"folders": []string{dir}})
	if c, b := do("POST", "/api/folders/add", string(body)); c != http.StatusOK || !strings.Contains(b, `"added":[true],"window":false`) {
		t.Fatalf("add: %d %s", c, b)
	}
	if c, b := do("GET", file, ""); c != http.StatusOK {
		t.Fatalf("saved folder: %d %s", c, b)
	}
	escape := "/api/file?" + url.Values{"root": {dir}, "path": {"../../etc/passwd"}}.Encode()
	if c, _ := do("GET", escape, ""); c != http.StatusForbidden && c != http.StatusNotFound {
		t.Fatalf("path escape: %d", c)
	}
	if c, b := do("POST", "/api/folders/add", `{"folders":["relative"]}`); c != http.StatusBadRequest {
		t.Fatalf("relative path: %d %s", c, b)
	}
	if c, _ := do("POST", "/api/folders/remove", `{"root":`+strconv.Quote(dir)+`}`); c != http.StatusOK {
		t.Fatalf("remove: %d", c)
	}
	if c, _ := do("GET", file, ""); c != http.StatusForbidden {
		t.Fatalf("removed folder: %d", c)
	}
	if c, _ := do("POST", "/api/folders/restore", `{"root":`+strconv.Quote(dir)+`}`); c != http.StatusOK {
		t.Fatalf("restore: %d", c)
	}
	if c, b := do("GET", "/api/folders", ""); c != http.StatusOK || !strings.Contains(b, `"window":false`) {
		t.Fatalf("list: %d %s", c, b)
	}
}

// The agent scan runs while an agents window is open, and for one minute after the last closes.
func TestHubWindowsAndGrace(t *testing.T) {
	h := newHub()
	srv := httptest.NewServer(http.HandlerFunc(h.serveEvents))
	t.Cleanup(srv.Close)
	if h.wanted(windowAgents, time.Now(), time.Minute) {
		t.Fatal("wanted before any window")
	}
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"?window=folders", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	select {
	case <-h.wake(windowFolders):
	case <-time.After(time.Second):
		t.Fatal("no wake for the folders window")
	}
	if h.count(windowFolders) != 1 || h.wanted(windowAgents, time.Now(), time.Minute) {
		t.Fatal("a folders window is not an agents window")
	}
	h.publish(windowAgents, []byte(`{"rows":[]}`))
	h.publish(windowFolders, []byte(`{"folders":[]}`))
	line, _ := bufio.NewReader(resp.Body).ReadString('\n')
	if line != "data: {\"folders\":[]}\n" {
		t.Fatalf("folders window got %q", line)
	}

	agents, _ := http.NewRequestWithContext(ctx, "GET", srv.URL, nil)
	ar, err := http.DefaultClient.Do(agents)
	if err != nil {
		t.Fatal(err)
	}
	ar.Body.Close()
	cancel()
	deadline := time.Now().Add(2 * time.Second)
	for h.count(windowAgents) > 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	closed := time.Now()
	if !h.wanted(windowAgents, closed.Add(59*time.Second), time.Minute) {
		t.Error("stopped within the minute")
	}
	if h.wanted(windowAgents, closed.Add(61*time.Second), time.Minute) {
		t.Error("still wanted after a minute")
	}
}
