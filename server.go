package main

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/kijk-dev/kijk/internal/viewer"
)

// cookieName carries the port: browsers share cookies across the ports of one host, so two
// viewers on different ports would otherwise log each other out.
func cookieName(port int) string { return fmt.Sprintf("kijk-%d", port) }

// newServer builds the routes. kijk serve passes no web files, updater, machines or folders: it
// has no window, does not update itself, and does not reach other machines.
func newServer(token string, port int, web fs.FS, h *hub, rts *viewer.Roots, u *updater, mm *machineManager, fl *folderList) http.Handler {
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
	mux.HandleFunc("GET /api/events", h.serveEvents)
	if fl != nil {
		mux.HandleFunc("GET /api/folders", func(w http.ResponseWriter, r *http.Request) {
			reply(w, foldersReply{Window: h.count(windowFolders) > 0})
		})
		mux.HandleFunc("POST /api/folders/add", addFolders(fl, h))
		mux.HandleFunc("POST /api/folders/remove", changeFolder(fl.remove))
		mux.HandleFunc("POST /api/folders/restore", changeFolder(fl.restore))
		mux.HandleFunc("POST /api/folders/refresh", changeFolder(fl.refresh))
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

// foldersReply tells kijk <folder> and kijk folders what happened, and whether a folders window
// is open.
type foldersReply struct {
	Added  []bool `json:"added"`
	Window bool   `json:"window"`
	Error  string `json:"error,omitempty"`
}

// addFolders checks the folders again: the list decides what the server may read.
func addFolders(fl *folderList, h *hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Folders []string `json:"folders"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil || len(req.Folders) == 0 {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		list := make([]savedFolder, 0, len(req.Folders))
		for _, p := range req.Folders {
			if !filepath.IsAbs(p) {
				http.Error(w, p+" is not an absolute path", http.StatusBadRequest)
				return
			}
			f, err := checkFolder(p, "")
			if err != nil {
				http.Error(w, err.Error(), http.StatusUnprocessableEntity)
				return
			}
			list = append(list, f)
		}
		res := foldersReply{Window: h.count(windowFolders) > 0}
		var err error
		if res.Added, err = fl.add(list); err != nil {
			res.Error = err.Error()
		}
		reply(w, res)
	}
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

// Window kinds. An /api/events stream without a kind is an agents window, as a local kijk that
// follows a remote one is.
const (
	windowAgents  = "agents"
	windowFolders = "folders"
)

// hub keeps the latest message per window kind and pushes it to every open window of that kind.
type hub struct {
	mu    sync.Mutex
	last  map[string][]byte
	subs  map[chan []byte]string
	left  map[string]time.Time // when the last window of a kind closed
	wakes map[string]chan struct{}
}

func newHub() *hub {
	return &hub{last: map[string][]byte{}, subs: map[chan []byte]string{}, left: map[string]time.Time{},
		wakes: map[string]chan struct{}{windowAgents: make(chan struct{}, 1), windowFolders: make(chan struct{}, 1)}}
}

func (h *hub) publish(kind string, b []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if bytes.Equal(b, h.last[kind]) {
		return
	}
	h.last[kind] = b
	for c, k := range h.subs {
		if k != kind {
			continue
		}
		select { // keep only the newest message for a slow client
		case <-c:
		default:
		}
		c <- b
	}
}

// forget drops the latest message, so a window that opens later waits for a fresh one.
func (h *hub) forget(kind string) {
	h.mu.Lock()
	delete(h.last, kind)
	h.mu.Unlock()
}

func (h *hub) count(kind string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, k := range h.subs {
		if k == kind {
			n++
		}
	}
	return n
}

// wanted reports whether a window of kind is open, or closed less than grace ago, so a reload
// does not stop and start the work behind it.
func (h *hub) wanted(kind string, now time.Time, grace time.Duration) bool {
	if h.count(kind) > 0 {
		return true
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	left, ok := h.left[kind]
	return ok && now.Sub(left) < grace
}

// wake fires when a window of kind opens.
func (h *hub) wake(kind string) <-chan struct{} { return h.wakes[kind] }

func (h *hub) serveEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	kind := windowAgents
	if r.URL.Query().Get("window") == windowFolders {
		kind = windowFolders
	}
	c := make(chan []byte, 1)
	h.mu.Lock()
	h.subs[c] = kind
	if h.last[kind] != nil {
		c <- h.last[kind]
	}
	h.mu.Unlock()
	select {
	case h.wakes[kind] <- struct{}{}:
	default:
	}
	defer func() {
		h.mu.Lock()
		delete(h.subs, c)
		h.left[kind] = time.Now()
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
