package main

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/kijk-dev/kijk/internal/agents"
	"github.com/kijk-dev/kijk/internal/viewer"
)

// cookieName carries the port: browsers share cookies across the ports of one host, so two
// viewers on different ports would otherwise log each other out.
func cookieName(port int) string { return fmt.Sprintf("kijk-%d", port) }

// newServer builds the routes. kijk serve passes no web files, updater, machines or desk: it has
// no window or folders list, does not update itself, and does not reach other machines.
func newServer(token string, port int, web fs.FS, h *hub, rts *viewer.Roots, u *updater, mm *machineManager, d *openDesk) http.Handler {
	mux := http.NewServeMux()
	if web != nil {
		mux.Handle("GET /", http.FileServerFS(web))
	}
	viewer.Register(mux, rts, viewer.Options{Web: web, CacheDir: cacheDir, Version: kijkVersion})
	if mm != nil {
		mux.HandleFunc("GET /m/{machine}/api/", mm.serveMachine)
		mux.HandleFunc("POST /m/{machine}/api/", mm.serveMachine)
	}
	mux.HandleFunc("GET /api/ping", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("kijk")) })
	mux.HandleFunc("GET /api/agenttypes", func(w http.ResponseWriter, r *http.Request) { reply(w, agents.Types) })
	mux.HandleFunc("GET /api/events", h.serveEvents)
	mux.HandleFunc("POST /api/count", countUse)
	if d != nil {
		mux.HandleFunc("POST /api/open", d.serve)
		mux.HandleFunc("POST /api/folders/remove", changeFolder(d.fl.remove))
		mux.HandleFunc("POST /api/folders/restore", changeFolder(d.fl.restore))
		mux.HandleFunc("POST /api/folders/refresh", changeFolder(d.fl.refresh))
	}
	if u == nil {
		return guard(token, port, secure(mux))
	}
	mux.HandleFunc("POST /api/update", func(w http.ResponseWriter, r *http.Request) {
		if !isRelease(u.version) {
			http.Error(w, errDev.Error(), http.StatusConflict)
			return
		}
		u.startUpdate() // a second click while one runs joins the running update
		accepted(w)
	})
	mux.HandleFunc("POST /api/restart", func(w http.ResponseWriter, r *http.Request) {
		if !canRestart {
			http.NotFound(w, r)
			return
		}
		if !u.startRestart() {
			http.Error(w, "no newer kijk is installed, or an update is still running", http.StatusConflict)
			return
		}
		accepted(w)
	})
	return guard(token, port, secure(mux))
}

// secure sets a Content-Security-Policy, a second guard should rendered markdown ever carry script.
func secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; img-src 'self' data: https:; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

// accepted answers an action that goes on in the background; its progress comes over /api/events.
func accepted(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	w.Write([]byte("{}"))
}

func reply(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func changeFolder(change func(root string) bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Root string `json:"root"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if !change(req.Root) {
			http.Error(w, "This folder is not in the list", http.StatusConflict)
			return
		}
		reply(w, struct{}{})
	}
}

// guard blocks DNS rebinding (Host check) and other web pages (token cookie).
func guard(token string, port int, next http.Handler) http.Handler {
	want := []byte(token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, p, err := net.SplitHostPort(r.Host)
		if err != nil || p != strconv.Itoa(port) || (host != "localhost" && host != "127.0.0.1") {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if t := r.URL.Query().Get("token"); t != "" {
			if subtle.ConstantTimeCompare([]byte(t), want) != 1 {
				http.Error(w, "wrong token", http.StatusForbidden)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: cookieName(port), Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
			q := r.URL.Query()
			q.Del("token")
			to := r.URL.Path
			if len(q) > 0 {
				to += "?" + q.Encode()
			}
			http.Redirect(w, r, to, http.StatusSeeOther)
			return
		}
		if o := r.Header.Get("Origin"); r.Method != http.MethodGet && o != "" && o != "http://"+r.Host {
			http.Error(w, "forbidden origin", http.StatusForbidden)
			return
		}
		c, err := r.Cookie(cookieName(port))
		if err != nil || subtle.ConstantTimeCompare([]byte(c.Value), want) != 1 {
			http.Error(w, "open kijk from the command line", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// hub keeps the latest rail message and pushes it to every open window.
type hub struct {
	mu   sync.Mutex
	last []byte
	subs map[chan []byte]struct{}
	left time.Time     // when the last window closed
	wake chan struct{} // closed when a window opens, then replaced
}

func newHub() *hub {
	return &hub{subs: map[chan []byte]struct{}{}, wake: make(chan struct{})}
}

func (h *hub) publish(b []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if bytes.Equal(b, h.last) {
		return
	}
	h.last = b
	for c := range h.subs {
		select { // keep only the newest message for a slow client
		case <-c:
		default:
		}
		c <- b
	}
}

// open is the number of windows open now.
func (h *hub) open() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}

// wanted reports whether a window is open, or closed less than grace ago, so a reload does not
// stop and start the work behind it.
func (h *hub) wanted(now time.Time, grace time.Duration) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs) > 0 || now.Sub(h.left) < grace
}

// opened gives a channel that closes when the next window opens. Every goroutine that waits on it
// wakes: the agent scan and the folder line counts.
func (h *hub) opened() <-chan struct{} {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.wake
}

// serveEvents ignores ?window=: there is one kind of window.
func (h *hub) serveEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	c := make(chan []byte, 1)
	h.mu.Lock()
	h.subs[c] = struct{}{}
	if h.last != nil {
		c <- h.last
	}
	close(h.wake)
	h.wake = make(chan struct{})
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.subs, c)
		h.left = time.Now()
		h.mu.Unlock()
	}()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher.Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case b := <-c:
			fmt.Fprintf(w, "data: %s\n\n", b)
			flusher.Flush()
		}
	}
}
