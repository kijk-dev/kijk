package main

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// cookieName carries the port: browsers share cookies across the ports of one host, so two
// viewers on different ports would otherwise log each other out.
func cookieName(port int) string { return fmt.Sprintf("kijk-%d", port) }

// newServer builds the routes. kijk serve passes no web files, updater, machines or folders: it
// has no window, does not update itself, and does not reach other machines.
func newServer(token string, port int, web fs.FS, h *hub, rts *roots, u *updater, mm *machineManager, fl *folderList) http.Handler {
	a := &api{roots: rts, index: newIndexCache(), hl: newHlCache()}
	mux := http.NewServeMux()
	if web != nil {
		mux.Handle("GET /", http.FileServerFS(web))
	}
	if mm != nil {
		mux.HandleFunc("GET /m/{machine}/api/", mm.serveMachine)
		mux.HandleFunc("POST /m/{machine}/api/", mm.serveMachine)
	}
	mux.HandleFunc("GET /api/ping", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("kijk")) })
	mux.HandleFunc("GET /api/events", h.serveEvents)
	mux.HandleFunc("GET /api/tree", a.tree)
	mux.HandleFunc("GET /api/file", a.file)
	mux.HandleFunc("GET /api/lines", a.lines)
	mux.HandleFunc("GET /api/image", a.image)
	mux.HandleFunc("GET /api/markdown", a.markdown)
	mux.HandleFunc("GET /api/find", a.find)
	mux.HandleFunc("GET /api/goto", a.gotoFile)
	mux.HandleFunc("GET /api/status", a.status)
	mux.HandleFunc("GET /api/diff", a.diff)
	mux.HandleFunc("GET /api/folder-events", a.folderEvents)
	mux.HandleFunc("POST /api/stage", a.gitAction(func(req actionRequest) (string, error) { return stage(req.Root, req.Paths) }))
	mux.HandleFunc("POST /api/unstage", a.gitAction(func(req actionRequest) (string, error) { return unstage(req.Root, req.Paths) }))
	mux.HandleFunc("GET /api/graph", a.graph)
	mux.HandleFunc("GET /api/pr", a.pr)
	mux.HandleFunc("GET /api/commit-files", a.commitFiles)
	mux.HandleFunc("POST /api/fetch", a.gitAction(func(req actionRequest) (string, error) { return fetch(req.Root) }))
	mux.HandleFunc("POST /api/push", a.gitAction(func(req actionRequest) (string, error) { return push(req.Root) }))
	mux.HandleFunc("POST /api/commit", a.gitAction(func(req actionRequest) (string, error) { return commit(req.Root, req.Message) }))
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

type api struct {
	roots *roots
	index *indexCache
	hl    *hlCache
}

// target resolves the root and path query parameters, or writes the error.
func (a *api) target(w http.ResponseWriter, r *http.Request) (root, path string, ok bool) {
	root = r.URL.Query().Get("root")
	if !a.roots.has(root) {
		http.Error(w, "This folder is not open in kijk", http.StatusForbidden)
		return "", "", false
	}
	path, err := resolve(root, r.URL.Query().Get("path"))
	if err != nil {
		fail(w, err)
		return "", "", false
	}
	return root, path, true
}

func fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errOutside):
		http.Error(w, "Path is outside this folder", http.StatusForbidden)
	case errors.Is(err, fs.ErrNotExist):
		http.Error(w, "Not found", http.StatusNotFound)
	case errors.Is(err, errBadHash), errors.Is(err, errBadMode):
		http.Error(w, err.Error(), http.StatusBadRequest)
	default:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
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

func (a *api) tree(w http.ResponseWriter, r *http.Request) {
	root, _, ok := a.target(w, r)
	if !ok {
		return
	}
	entries, err := listDir(root, r.URL.Query().Get("path"))
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, entries)
}

func (a *api) file(w http.ResponseWriter, r *http.Request) {
	root, path, ok := a.target(w, r)
	if !ok {
		return
	}
	idx, err := a.index.get(path)
	if err != nil {
		fail(w, err)
		return
	}
	rel := r.URL.Query().Get("path")
	changes, err := lineChanges(root, rel)
	if err != nil {
		changes = []lineChange{} // no marks outside git or before the first commit
	}
	var lfs int64
	if imageType(rel) != "" {
		lfs = fileLFS(path, idx.Size)
	}
	reply(w, struct {
		fileInfo
		LFS     int64        `json:"lfs,omitempty"`
		Changes []lineChange `json:"changes"`
	}{idx.fileInfo, lfs, changes})
}

func (a *api) find(w http.ResponseWriter, r *http.Request) {
	_, path, ok := a.target(w, r)
	if !ok {
		return
	}
	idx, err := a.index.get(path)
	if err != nil {
		fail(w, err)
		return
	}
	res, err := findInFile(path, idx, r.URL.Query().Get("q"))
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, res)
}

func (a *api) gotoFile(w http.ResponseWriter, r *http.Request) {
	root, _, ok := a.target(w, r)
	if !ok {
		return
	}
	reply(w, gotoFile(folderFiles(root), r.URL.Query().Get("q"), 50))
}

func (a *api) lines(w http.ResponseWriter, r *http.Request) {
	_, path, ok := a.target(w, r)
	if !ok {
		return
	}
	from, _ := strconv.Atoi(r.URL.Query().Get("from"))
	to, _ := strconv.Atoi(r.URL.Query().Get("to"))
	from = max(from, 0)
	to = min(to, from+2000)
	idx, err := a.index.get(path)
	if err != nil {
		fail(w, err)
		return
	}
	lines, err := a.hl.lines(path, idx, from, to)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, lines)
}

func (a *api) markdown(w http.ResponseWriter, r *http.Request) {
	_, path, ok := a.target(w, r)
	if !ok {
		return
	}
	st, err := os.Stat(path)
	if err != nil {
		fail(w, err)
		return
	}
	if st.Size() > mdMaxBytes {
		http.Error(w, "Too large to render", http.StatusRequestEntityTooLarge)
		return
	}
	src, err := os.ReadFile(path)
	if err != nil {
		fail(w, err)
		return
	}
	out, err := renderMarkdown(src)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, map[string]string{"html": out})
}

func (a *api) status(w http.ResponseWriter, r *http.Request) {
	if root, _, ok := a.target(w, r); ok {
		reply(w, statusFor(root, r.URL.Query().Get("target")))
	}
}

func (a *api) graph(w http.ResponseWriter, r *http.Request) {
	root, _, ok := a.target(w, r)
	if !ok {
		return
	}
	g, err := graph(root, r.URL.Query().Get("target"))
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, g)
}

func (a *api) pr(w http.ResponseWriter, r *http.Request) {
	if root, _, ok := a.target(w, r); ok {
		reply(w, prFor(root))
	}
}

func (a *api) commitFiles(w http.ResponseWriter, r *http.Request) {
	root, _, ok := a.target(w, r)
	if !ok {
		return
	}
	files, err := commitFiles(root, r.URL.Query().Get("hash"))
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, files)
}

func (a *api) diff(w http.ResponseWriter, r *http.Request) {
	root := r.URL.Query().Get("root")
	if !a.roots.has(root) {
		http.Error(w, "This folder is not open in kijk", http.StatusForbidden)
		return
	}
	q := r.URL.Query()
	context, err := strconv.Atoi(q.Get("context"))
	if err != nil || context < 0 {
		context = 3
	}
	res, err := diff(root, q.Get("path"), q.Get("orig"), q.Get("mode"), q.Get("hash"), context)
	if err != nil {
		fail(w, err)
		return
	}
	highlightDiff(q.Get("path"), &res)
	if imageType(q.Get("path")) != "" {
		res.Image = diffImage(root, q.Get("path"), q.Get("orig"), q.Get("mode"), q.Get("hash"))
	}
	reply(w, res)
}

type actionRequest struct {
	Root    string   `json:"root"`
	Paths   []string `json:"paths"`
	Message string   `json:"message"`
}

// gitAction runs a git command that changes the repository. On failure the reply body is git's output.
func (a *api) gitAction(run func(req actionRequest) (string, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req actionRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if !a.roots.has(req.Root) {
			http.Error(w, "This folder is not open in kijk", http.StatusForbidden)
			return
		}
		out, err := run(req)
		if err != nil {
			if out == "" {
				out = err.Error()
			}
			http.Error(w, out, http.StatusUnprocessableEntity)
			return
		}
		reply(w, map[string]string{"output": out})
	}
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
