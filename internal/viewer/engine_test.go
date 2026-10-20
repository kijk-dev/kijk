package viewer

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/kijk-dev/kijk/internal/gittest"
)

// cacheDir is the user cache folder of the engine caches in these tests; fakeEngine sets it.
var cacheDir func() (string, error)

// fakeEngine serves body as the engine "test", pinned to the SHA-256 of pinned, with the cache
// in a temporary folder. It returns the engine folder and the request count.
func fakeEngine(t *testing.T, body, pinned string, gate chan struct{}) (string, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		if !strings.HasPrefix(r.UserAgent(), "kijk/") {
			t.Errorf("User-Agent %q", r.UserAgent())
		}
		if gate != nil {
			<-gate
		}
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	sum := sha256.Sum256([]byte(pinned))
	oldEngines, oldCache := engines, cacheDir
	engines = map[string]engine{"test": {name: "test", label: "Test", version: "2.0.0", url: srv.URL + "/test.js", sum: hex.EncodeToString(sum[:])}}
	dir := t.TempDir()
	cacheDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { engines, cacheDir = oldEngines, oldCache })
	return filepath.Join(dir, "kijk", "engines"), &n
}

func TestEngineDownload(t *testing.T) {
	dir, n := fakeEngine(t, "draw()", "draw()", nil)
	gittest.Write(t, filepath.Join(dir, "test-1.0.0.js"), "old")
	gittest.Write(t, filepath.Join(dir, "test-themes-2.0.0.js"), "another engine")
	c := newEngineCache(cacheDir, "test")
	p, err := c.path(engines["test"])
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "draw()" || p != filepath.Join(dir, "test-2.0.0.js") {
		t.Fatalf("%s: %q", p, b)
	}
	if _, err := os.Stat(filepath.Join(dir, "test-1.0.0.js")); !os.IsNotExist(err) {
		t.Errorf("old version kept: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "test-themes-2.0.0.js")); err != nil {
		t.Errorf("an engine whose name starts the same was deleted: %v", err)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".download-*")); len(left) > 0 {
		t.Errorf("temp files left: %v", left)
	}
	if _, err := newEngineCache(cacheDir, "test").path(engines["test"]); err != nil || n.Load() != 1 {
		t.Errorf("cached engine downloaded again: %v, %d requests", err, n.Load())
	}
}

func TestEngineWrongSum(t *testing.T) {
	dir, n := fakeEngine(t, "evil()", "draw()", nil)
	now := time.Now()
	c := newEngineCache(cacheDir, "test")
	c.now = func() time.Time { return now }
	if _, err := c.path(engines["test"]); err == nil {
		t.Fatal("no error")
	}
	if files, _ := os.ReadDir(dir); len(files) != 0 {
		t.Errorf("saved: %v", files)
	}
	now = now.Add(30 * time.Second)
	if _, err := c.path(engines["test"]); err == nil || n.Load() != 1 {
		t.Errorf("a retry within the minute: %v, %d requests", err, n.Load())
	}
	now = now.Add(31 * time.Second)
	c.path(engines["test"])
	if n.Load() != 2 {
		t.Errorf("no retry after the minute: %d requests", n.Load())
	}
}

func TestEngineTooLarge(t *testing.T) {
	big := strings.Repeat("x", engineMaxBytes+1)
	dir, _ := fakeEngine(t, big, big, nil)
	if _, err := newEngineCache(cacheDir, "test").path(engines["test"]); err == nil || !strings.Contains(err.Error(), "larger") {
		t.Fatalf("err = %v", err)
	}
	if files, _ := os.ReadDir(dir); len(files) != 0 {
		t.Errorf("saved: %v", files)
	}
}

func TestEngineOneDownloadAtATime(t *testing.T) {
	gate := make(chan struct{})
	_, n := fakeEngine(t, "draw()", "draw()", gate)
	c := newEngineCache(cacheDir, "test")
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Go(func() { _, errs[i] = c.path(engines["test"]) })
	}
	time.Sleep(50 * time.Millisecond)
	close(gate)
	wg.Wait()
	if errs[0] != nil || errs[1] != nil || n.Load() != 1 {
		t.Errorf("errors %v, %d requests", errs, n.Load())
	}
}

func engineServer(t *testing.T) func(target string) *httptest.ResponseRecorder {
	web := fstest.MapFS{"index.html": {Data: []byte("hi")}, "diagram.html": {Data: []byte(`<html data-test="{{test}}" data-missing="{{missing}}">`)}}
	srv := http.NewServeMux()
	Register(srv, &Roots{}, Options{Web: web, CacheDir: cacheDir, Version: "test"})
	return func(target string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", target, nil)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		return w
	}
}

func TestEngineRoute(t *testing.T) {
	dir, n := fakeEngine(t, "draw()", "draw()", nil)
	gittest.Write(t, filepath.Join(dir, "test-2.0.0.js"), "cached()")
	get := engineServer(t)
	w := get("/api/engine/test?v=2.0.0")
	h := w.Header()
	if w.Code != http.StatusOK || w.Body.String() != "cached()" || n.Load() != 0 {
		t.Fatalf("%d %q, %d requests", w.Code, w.Body.String(), n.Load())
	}
	if h.Get("Content-Type") != "text/javascript" || h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Cache-Control") != "private, max-age=31536000, immutable" {
		t.Errorf("headers %v", h)
	}
	if w := get("/api/engine/nope"); w.Code != http.StatusNotFound {
		t.Errorf("unknown engine: %d", w.Code)
	}
}

func TestEngineRouteFails(t *testing.T) {
	fakeEngine(t, "evil()", "draw()", nil)
	if w := engineServer(t)("/api/engine/test"); w.Code != http.StatusBadGateway || strings.TrimSpace(w.Body.String()) != "Could not download Test" {
		t.Errorf("%d %q", w.Code, w.Body.String())
	}
}

func TestDiagramFrame(t *testing.T) {
	dir, _ := fakeEngine(t, "", "", nil)
	get := engineServer(t)
	w := get("/diagram")
	if w.Code != http.StatusOK || w.Body.String() != `<html data-test="/api/engine/test?v=2.0.0" data-missing="test">` {
		t.Fatalf("%d %q", w.Code, w.Body.String())
	}
	gittest.Write(t, filepath.Join(dir, "test-2.0.0.js"), "cached()")
	if body := get("/diagram").Body.String(); body != `<html data-test="/api/engine/test?v=2.0.0" data-missing="">` {
		t.Errorf("cached engine still missing: %q", body)
	}
	if csp := w.Header().Values("Content-Security-Policy"); len(csp) != 1 || csp[0] != frameCSP {
		t.Errorf("frame CSP = %q", csp)
	}
	for _, want := range []string{"script-src 'self' 'wasm-unsafe-eval';", "connect-src 'self';", "default-src 'none';"} {
		if !strings.Contains(frameCSP, want) {
			t.Errorf("frame CSP lacks %s", want)
		}
	}
}

func TestNoEnginesOnRemote(t *testing.T) {
	srv := http.NewServeMux()
	Register(srv, &Roots{}, Options{})
	for _, target := range []string{"/api/engine/mermaid", "/diagram"} {
		r := httptest.NewRequest("GET", target, nil)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		if w.Code != http.StatusNotFound {
			t.Errorf("%s: %d", target, w.Code)
		}
	}
}
