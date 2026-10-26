package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"maps"
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

	"github.com/kijk-dev/kijk/internal/agents"
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
	rl, h, rts := testRail()
	fl := newFolderList(p, nil, rl)
	srv := newServer("secret", 7717, nil, h, rts, nil, nil, &openDesk{h, rl, fl})
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
	body, _ := json.Marshal(openAsk{Folders: []string{dir}})
	if c, b := do("POST", "/api/open", string(body)); c != http.StatusOK || b != `{"said":"Added `+filepath.Base(dir)+`.","window":false,"seq":1}`+"\n" {
		t.Fatalf("add: %d %s", c, b)
	}
	if c, b := do("GET", file, ""); c != http.StatusOK {
		t.Fatalf("saved folder: %d %s", c, b)
	}
	escape := "/api/file?" + url.Values{"root": {dir}, "path": {"../../etc/passwd"}}.Encode()
	if c, _ := do("GET", escape, ""); c != http.StatusForbidden && c != http.StatusNotFound {
		t.Fatalf("path escape: %d", c)
	}
	for _, bad := range []string{`{"folders":["relative"]}`, `{"list":"agents"}`, `{"pr":{"kind":"github","host":"github.com","path":"a/b","number":0}}`,
		`{"pr":{"kind":"github","host":"github.com","path":"../../x","number":1}}`, `{"pr":{"kind":"bitbucket","host":"github.com","path":"a/b","number":1}}`, `{`} {
		if c, b := do("POST", "/api/open", bad); c != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", bad, c, b)
		}
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
	if c, b := do("POST", "/api/open", `{}`); c != http.StatusOK || b != `{"window":false}`+"\n" {
		t.Fatalf("plain kijk: %d %s", c, b)
	}
	serve := newServer("secret", 7717, nil, newHub(), &viewer.Roots{}, nil, nil, nil)
	r := httptest.NewRequest("POST", "/api/open", strings.NewReader(`{}`))
	r.Host = "127.0.0.1:7717"
	r.AddCookie(&http.Cookie{Name: cookieName(7717), Value: "secret"})
	w := httptest.NewRecorder()
	if serve.ServeHTTP(w, r); w.Code != http.StatusNotFound {
		t.Fatalf("kijk serve has no folders list, but /api/open answers %d", w.Code)
	}
}

// Every window gets the one message, whatever ?window= says. Opening one wakes every waiter, and
// the work goes on for one minute after the last window closes.
func TestHubWindowsAndGrace(t *testing.T) {
	h := newHub()
	srv := httptest.NewServer(http.HandlerFunc(h.serveEvents))
	t.Cleanup(srv.Close)
	if h.wanted(time.Now(), time.Minute) {
		t.Fatal("wanted before any window")
	}
	woke := make(chan string, 2)
	for _, who := range []string{"the agent scan", "the folder counts"} {
		opened := h.opened()
		go func() {
			<-opened
			woke <- who
		}()
	}
	ctx, cancel := context.WithCancel(context.Background())
	var bodies []*bufio.Reader
	for _, q := range []string{"", "?window=folders"} {
		req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+q, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		bodies = append(bodies, bufio.NewReader(resp.Body))
	}
	for range 2 {
		select {
		case <-woke:
		case <-time.After(time.Second):
			t.Fatal("a waiter did not wake when a window opened")
		}
	}
	if h.open() != 2 || !h.wanted(time.Now(), time.Minute) {
		t.Fatalf("%d windows open", h.open())
	}
	h.publish([]byte(`{"rows":[]}`))
	for i, rd := range bodies {
		if line, _ := rd.ReadString('\n'); line != "data: {\"rows\":[]}\n" {
			t.Fatalf("window %d got %q", i, line)
		}
	}
	cancel()
	for deadline := time.Now().Add(2 * time.Second); h.open() > 0; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("windows still open after they closed")
		}
	}
	closed := time.Now()
	if !h.wanted(closed.Add(59*time.Second), time.Minute) {
		t.Error("stopped within the minute")
	}
	if h.wanted(closed.Add(61*time.Second), time.Minute) {
		t.Error("still wanted after a minute")
	}
}

func TestAgentTypesRoute(t *testing.T) {
	srv := newServer("secret", 7717, nil, newHub(), &viewer.Roots{}, nil, nil, nil)
	r := httptest.NewRequest("GET", "/api/agenttypes", nil)
	r.Host = "127.0.0.1:7717"
	r.AddCookie(&http.Cookie{Name: cookieName(7717), Value: "secret"})
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	var types []map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &types); err != nil || len(types) != len(agents.Types) {
		t.Fatalf("%d %q: %v", w.Code, w.Body.String(), err)
	}
	for i, at := range agents.Types {
		want := map[string]string{"name": at.Name, "label": at.Label, "icon": at.Icon}
		if at.Color != "" {
			want["color"] = at.Color
		}
		if !maps.Equal(types[i], want) {
			t.Errorf("type %d = %v, want %v", i, types[i], want)
		}
	}
}
