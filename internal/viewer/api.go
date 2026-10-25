// Package viewer serves one folder's views over HTTP: its files, find, rendered text, images,
// diagrams, and the git status, graph and diffs.
package viewer

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/kijk-dev/kijk/internal/git"
	"github.com/kijk-dev/kijk/internal/render"
)

// Options is what the folder views take from the program around them.
type Options struct {
	Web      fs.FS                  // the page's files; without them there is no diagram frame and no engines
	CacheDir func() (string, error) // the user cache folder, where diagram engines are kept
	Version  string                 // kijk's version, for the User-Agent of engine downloads
}

// Register adds the routes of one folder's views to mux. The caller guards mux.
func Register(mux *http.ServeMux, rts *Roots, o Options) {
	a := &api{roots: rts, index: newIndexCache(), hl: newHlCache(), cacheDir: o.CacheDir, opened: &openedPRs{m: map[string]openedPR{}}}
	if o.Web != nil {
		ec := newEngineCache(o.CacheDir, o.Version)
		mux.HandleFunc("GET /diagram", serveFrame(o.Web, ec))
		mux.HandleFunc("GET /api/engine/{name}", ec.serve)
	}
	mux.HandleFunc("GET /api/filetypes", func(w http.ResponseWriter, r *http.Request) { reply(w, render.FileTypes) })
	mux.HandleFunc("GET /api/tree", a.tree)
	mux.HandleFunc("GET /api/file", a.file)
	mux.HandleFunc("GET /api/lines", a.lines)
	mux.HandleFunc("GET /api/image", a.image)
	mux.HandleFunc("GET /api/markdown", a.markdown)
	mux.HandleFunc("GET /api/text", a.text)
	mux.HandleFunc("GET /api/find", a.find)
	mux.HandleFunc("GET /api/goto", a.gotoFile)
	mux.HandleFunc("GET /api/status", a.status)
	mux.HandleFunc("GET /api/diff", a.diff)
	mux.HandleFunc("GET /api/branch", a.branch)
	mux.HandleFunc("GET /api/folder-events", a.folderEvents)
	mux.HandleFunc("POST /api/stage", a.gitAction(func(req actionRequest) (string, error) { return git.Stage(req.Root, req.Paths) }))
	mux.HandleFunc("POST /api/unstage", a.gitAction(func(req actionRequest) (string, error) { return git.Unstage(req.Root, req.Paths) }))
	mux.HandleFunc("GET /api/graph", a.graph)
	mux.HandleFunc("GET /api/pr", a.pr)
	mux.HandleFunc("GET /api/prs", a.prs)
	mux.HandleFunc("GET /api/pr/view", a.prView)
	mux.HandleFunc("GET /api/pr/log", a.prLog)
	mux.HandleFunc("GET /api/pr/since", a.prSince)
	mux.HandleFunc("POST /api/pr/comment", a.write(prComment))
	mux.HandleFunc("POST /api/pr/comment/edit", a.write(prEdit))
	mux.HandleFunc("POST /api/pr/comment/delete", a.write(prDelete))
	mux.HandleFunc("POST /api/pr/resolve", a.write(prResolve))
	mux.HandleFunc("POST /api/pr/summary", a.write(prSummary))
	mux.HandleFunc("POST /api/pr/submit", a.write(prSubmit))
	mux.HandleFunc("POST /api/pr/merge", a.merge(prMerge))
	mux.HandleFunc("GET /api/commit-files", a.commitFiles)
	mux.HandleFunc("POST /api/fetch", a.gitAction(func(req actionRequest) (string, error) { return git.Fetch(req.Root) }))
	mux.HandleFunc("POST /api/push", a.gitAction(func(req actionRequest) (string, error) { return git.Push(req.Root, req.Lease) }))
	mux.HandleFunc("POST /api/commit", a.gitAction(func(req actionRequest) (string, error) { return git.Commit(req.Root, req.Message) }))
}

type api struct {
	roots    *Roots
	index    *indexCache
	hl       *hlCache
	cacheDir func() (string, error)
	opened   *openedPRs
}

// target resolves the root and path query parameters, or writes the error.
func (a *api) target(w http.ResponseWriter, r *http.Request) (root, path string, ok bool) {
	root = r.URL.Query().Get("root")
	if !a.roots.Has(root) {
		http.Error(w, "This folder is not open in kijk", http.StatusForbidden)
		return "", "", false
	}
	// With pr, the file is as it is at that pull request's head, not on disk.
	if pr := r.URL.Query().Get("pr"); pr != "" {
		n, ok := prNumber(w, pr)
		if !ok {
			return "", "", false
		}
		p, err := a.prFile(root, n, r.URL.Query().Get("path"))
		if err != nil {
			fail(w, err)
			return "", "", false
		}
		return root, p, true
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
	case errors.Is(err, git.ErrOutside):
		http.Error(w, "Path is outside this folder", http.StatusForbidden)
	case errors.Is(err, fs.ErrNotExist):
		http.Error(w, "Not found", http.StatusNotFound)
	case errors.Is(err, git.ErrNoBranch):
		http.Error(w, "This branch has no branch changes", http.StatusNotFound)
	case errors.Is(err, git.ErrBadHash), errors.Is(err, git.ErrBadMode):
		http.Error(w, err.Error(), http.StatusBadRequest)
	default:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
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
	changes := []git.LineChange{} // no marks at a pull request's head, outside git or before the first commit
	if r.URL.Query().Get("pr") == "" {
		if c, err := git.LineChanges(root, rel); err == nil {
			changes = c
		}
	}
	var lfs int64
	if imageType(rel) != "" {
		lfs = fileLFS(path, idx.Size)
	}
	reply(w, struct {
		fileInfo
		LFS     int64            `json:"lfs,omitempty"`
		Changes []git.LineChange `json:"changes"`
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

// markdown answers the rendered view of a file: the HTML of a rendered type, or the block of a drawn one.
func (a *api) markdown(w http.ResponseWriter, r *http.Request) {
	root, path, ok := a.target(w, r)
	if !ok {
		return
	}
	rel := r.URL.Query().Get("path")
	t := render.TypeOf(rel)
	if t == nil || (t.Show != render.ShowRendered && t.Show != render.ShowDrawn) {
		http.Error(w, "No rendered view of this file", http.StatusNotFound)
		return
	}
	tooLarge := "Too large to render"
	if t.Show == render.ShowDrawn {
		tooLarge = diagramTooLargeText
	}
	st, err := os.Stat(path)
	if err != nil {
		fail(w, err)
		return
	}
	if st.Size() > t.MaxBytes {
		http.Error(w, tooLarge, http.StatusRequestEntityTooLarge)
		return
	}
	src, err := os.ReadFile(path)
	if err != nil {
		fail(w, err)
		return
	}
	if t.TooLarge(src) {
		http.Error(w, tooLarge, http.StatusRequestEntityTooLarge)
		return
	}
	if t.Show == render.ShowDrawn {
		reply(w, rendered{HTML: render.DiagramFile(src, t.Name)})
		return
	}
	read := func(inc string) ([]byte, error) { return readIncluded(root, inc) }
	if pr := r.URL.Query().Get("pr"); pr != "" {
		n, _ := strconv.Atoi(pr) // target checked it
		read = func(inc string) ([]byte, error) { return a.readAtPR(root, n, inc) }
	}
	html, includes, err := t.Render(src, rel, read)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, rendered{html, includes})
}

type rendered struct {
	HTML     string   `json:"html"`
	Includes []string `json:"includes,omitempty"` // the files it shows in place, so it redraws when one changes
}

// readAtPR reads a file an AsciiDoc file at a pull request's head includes, at that head.
func (a *api) readAtPR(root string, n int, rel string) ([]byte, error) {
	p, err := a.prFile(root, n, rel)
	if err != nil {
		return nil, err
	}
	return readIncluded(filepath.Dir(p), filepath.Base(p))
}

// readIncluded reads a file an AsciiDoc file includes, by the same path rule as every route.
func readIncluded(root, rel string) ([]byte, error) {
	p, err := resolve(root, rel)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Size() > render.MaxSourceBytes {
		return nil, errors.New("not a file to include")
	}
	return os.ReadFile(p)
}

const textMaxBytes = 256 << 10

// text answers a whole file's text, for the files a PlantUML diagram includes.
func (a *api) text(w http.ResponseWriter, r *http.Request) {
	_, path, ok := a.target(w, r)
	if !ok {
		return
	}
	st, err := os.Stat(path)
	if err != nil {
		fail(w, err)
		return
	}
	if st.IsDir() {
		fail(w, fs.ErrNotExist)
		return
	}
	if st.Size() > textMaxBytes {
		http.Error(w, "File too large", http.StatusRequestEntityTooLarge)
		return
	}
	src, err := os.ReadFile(path)
	if err != nil {
		fail(w, err)
		return
	}
	if git.IsBinary(src) {
		http.Error(w, "Binary file", http.StatusUnsupportedMediaType)
		return
	}
	reply(w, map[string]string{"text": string(src)})
}

func (a *api) status(w http.ResponseWriter, r *http.Request) {
	if root, _, ok := a.target(w, r); ok {
		reply(w, git.StatusFor(root, r.URL.Query().Get("target")))
	}
}

func (a *api) graph(w http.ResponseWriter, r *http.Request) {
	root, _, ok := a.target(w, r)
	if !ok {
		return
	}
	g, err := git.Graph(root, r.URL.Query().Get("target"))
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, g)
}

func (a *api) pr(w http.ResponseWriter, r *http.Request) {
	if root, _, ok := a.target(w, r); ok {
		reply(w, git.PRFor(root))
	}
}

func (a *api) commitFiles(w http.ResponseWriter, r *http.Request) {
	root, _, ok := a.target(w, r)
	if !ok {
		return
	}
	files, err := git.CommitFiles(root, r.URL.Query().Get("hash"))
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, files)
}

func (a *api) diff(w http.ResponseWriter, r *http.Request) {
	root := r.URL.Query().Get("root")
	if !a.roots.Has(root) {
		http.Error(w, "This folder is not open in kijk", http.StatusForbidden)
		return
	}
	q := r.URL.Query()
	context, err := strconv.Atoi(q.Get("context"))
	if err != nil || context < 0 {
		context = 3
	}
	mode, hash := q.Get("mode"), q.Get("hash")
	switch mode {
	case "branch":
		if hash = git.BranchSplit(root, q.Get("target")); hash == "" {
			fail(w, git.ErrNoBranch)
			return
		}
	case "pr":
		n, ok := prNumber(w, q.Get("pr"))
		if !ok {
			return
		}
		p, _ := a.opened.get(root, n)
		if p.head == "" {
			http.Error(w, "Open the pull request again", http.StatusConflict)
			return
		}
		hash = p.split + ".." + p.head
		if from := q.Get("from"); from != "" {
			if !git.FullHash(from) {
				fail(w, git.ErrBadHash)
				return
			}
			hash = from + ".." + p.head // since the head of your last review
		}
	}
	res, err := diff(root, q.Get("path"), q.Get("orig"), mode, hash, context)
	if err != nil {
		fail(w, err)
		return
	}
	highlightDiff(q.Get("path"), &res)
	out := diffReply{DiffResult: res}
	if mode == "branch" {
		out.NotPushed, _ = git.NotPushed(root, q.Get("path"))
	}
	// The pictures of a pull request's change are not served: they would need a revision.
	if t := render.TypeOf(q.Get("path")); t != nil && t.Diff == render.DiffPictures && mode != "pr" {
		switch t.Show {
		case render.ShowImage:
			out.Image = diffImage(root, q.Get("path"), q.Get("orig"), q.Get("mode"), q.Get("hash"))
		case render.ShowDrawn:
			out.Diagram = diffDiagram(root, t, q.Get("path"), q.Get("orig"), q.Get("mode"), q.Get("hash"))
		}
	}
	reply(w, out)
}

// branch answers the branch changes. A branch on the main line has none: 404.
func (a *api) branch(w http.ResponseWriter, r *http.Request) {
	root, _, ok := a.target(w, r)
	if !ok {
		return
	}
	b, err := git.BranchChanges(root, r.URL.Query().Get("target"))
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, b)
}

type actionRequest struct {
	Root    string   `json:"root"`
	Paths   []string `json:"paths"`
	Message string   `json:"message"`
	Lease   string   `json:"lease"` // a force push: the upstream commit the page showed
}

// gitAction runs a git command that changes the repository. On failure the reply body is git's output.
func (a *api) gitAction(run func(req actionRequest) (string, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req actionRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if !a.roots.Has(req.Root) {
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
