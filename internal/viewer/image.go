package viewer

import (
	"bytes"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/kijk-dev/kijk/internal/git"
	"github.com/kijk-dev/kijk/internal/render"
)

// imageType returns the content type of a slash-separated path, or "" when it is not an image.
func imageType(p string) string {
	if t := render.TypeOf(p); t != nil {
		return t.MIME
	}
	return ""
}

func IsImageType(ct string) bool {
	return ct != "" && slices.ContainsFunc(render.FileTypes, func(t render.FileType) bool { return t.MIME == ct })
}

const lfsMaxPointer = 1024

// lfsSize returns the size of the real file when b is a Git LFS pointer, else 0.
func lfsSize(b []byte) int64 {
	if len(b) >= lfsMaxPointer || !bytes.HasPrefix(b, []byte("version https://git-lfs.github.com/spec/v1")) {
		return 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		if s, ok := strings.CutPrefix(line, "size "); ok {
			if n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64); err == nil && n > 0 {
				return n
			}
		}
	}
	return 0
}

func fileLFS(p string, size int64) int64 {
	if size >= lfsMaxPointer {
		return 0
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return 0
	}
	return lfsSize(b)
}

// blobRef is one side of a change: an object in git, or the file on disk when oid is empty.
type blobRef struct {
	oid  string
	path string // the old name on the before side of a rename
}

type rawChange struct {
	srcOID, dstOID, status, src, dst string
}

// parseRaw reads git's --raw -z output: ":srcmode dstmode srcoid dstoid status", then the
// path, then the new path for a rename or copy.
func parseRaw(out []byte) []rawChange {
	f := strings.Split(strings.TrimLeft(string(out), "\n"), "\x00")
	var res []rawChange
	for i := 0; i+1 < len(f); i++ {
		p := strings.Fields(strings.TrimPrefix(f[i], ":"))
		if !strings.HasPrefix(f[i], ":") || len(p) != 5 {
			continue
		}
		c := rawChange{srcOID: p[2], dstOID: p[3], status: p[4][:1], src: f[i+1], dst: f[i+1]}
		i++
		if (c.status == "R" || c.status == "C") && i+1 < len(f) {
			c.dst = f[i+1]
			i++
		}
		res = append(res, c)
	}
	return res
}

// changeSides finds the two versions a diff of path compares. A nil side does not exist.
func changeSides(root, path, orig, mode, hash string) (before, after *blobRef, err error) {
	if !filepath.IsLocal(filepath.FromSlash(path)) || (orig != "" && !filepath.IsLocal(filepath.FromSlash(orig))) {
		return nil, nil, git.ErrOutside
	}
	var args []string
	switch mode {
	case "untracked":
		return nil, &blobRef{path: path}, nil
	case "unstaged":
		args = []string{"diff"}
	case "staged":
		args = []string{"diff", "--cached"}
	case "commit":
		if !git.ValidHash(hash) {
			return nil, nil, git.ErrBadHash
		}
		args = []string{"show", "--format=", "--first-parent", hash}
	default:
		return nil, nil, git.ErrBadMode
	}
	args = append(args, "--raw", "-z", "--no-abbrev", "-M", "--")
	if orig != "" {
		args = append(args, orig)
	}
	out, err := git.Raw(root, append(args, path)...)
	if err != nil {
		return nil, nil, err
	}
	ref := func(oid, p string) *blobRef {
		if strings.Trim(oid, "0") == "" { // git's name for "no such side"
			return nil
		}
		return &blobRef{oid: oid, path: p}
	}
	changes := parseRaw(out)
	for _, c := range changes {
		if c.dst != path {
			continue
		}
		before, after = ref(c.srcOID, c.src), ref(c.dstOID, path)
		if mode == "unstaged" && c.status != "D" {
			after = &blobRef{path: path}
		}
		if before == nil && orig != "" { // a rename git did not pair up
			for _, d := range changes {
				if d.src == orig && d.status == "D" {
					before = ref(d.srcOID, orig)
				}
			}
		}
		return before, after, nil
	}
	return nil, nil, fs.ErrNotExist
}

// imageSide tells the page which version a side is, so a picture reloads only when it changed.
type imageSide struct {
	OID   string `json:"oid,omitempty"`
	Mtime int64  `json:"mtime,omitempty"`
	Size  int64  `json:"size"`
	LFS   int64  `json:"lfs,omitempty"`
}

type imageSides struct {
	Before *imageSide `json:"before"`
	After  *imageSide `json:"after"`
	Parent string     `json:"parent,omitempty"` // a commit's first parent, short, to name the before side
}

func diffImage(root, path, orig, mode, hash string) *imageSides {
	before, after, err := changeSides(root, path, orig, mode, hash)
	if err != nil {
		return nil
	}
	res := &imageSides{Before: sideInfo(root, before), After: sideInfo(root, after)}
	if mode == "commit" && before != nil {
		res.Parent, _ = git.Run(root, "rev-parse", "--short", hash+"^")
	}
	return res
}

func sideInfo(root string, b *blobRef) *imageSide {
	if b == nil {
		return nil
	}
	if b.oid == "" {
		p, err := resolve(root, b.path)
		if err != nil {
			return nil
		}
		st, err := os.Stat(p)
		if err != nil || st.IsDir() {
			return nil
		}
		return &imageSide{Mtime: st.ModTime().UnixNano(), Size: st.Size(), LFS: fileLFS(p, st.Size())}
	}
	size, err := blobSize(root, b.oid)
	if err != nil {
		return nil
	}
	s := &imageSide{OID: b.oid, Size: size}
	if size < lfsMaxPointer {
		if out, err := git.Raw(root, "cat-file", "blob", b.oid); err == nil {
			s.LFS = lfsSize(out)
		}
	}
	return s
}

func blobSize(root, oid string) (int64, error) {
	out, err := git.Run(root, "cat-file", "-s", oid)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(out, 10, 64)
}

// image serves a picture from disk, or one side of a change when the request has a side. The
// answer can never be HTML, and the sandbox stops an SVG opened as a page from running script.
func (a *api) image(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Add("Content-Security-Policy", "sandbox")
	w.Header().Set("Cache-Control", "no-cache")
	q := r.URL.Query()
	if q.Has("side") {
		a.imageVersion(w, r)
		return
	}
	_, p, ok := a.target(w, r)
	if !ok {
		return
	}
	ct := imageType(q.Get("path"))
	if ct == "" {
		http.Error(w, "Not an image", http.StatusNotFound)
		return
	}
	serveImageFile(w, r, p, ct)
}

func (a *api) imageVersion(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	root := q.Get("root")
	if !a.roots.Has(root) {
		http.Error(w, "This folder is not open in kijk", http.StatusForbidden)
		return
	}
	if imageType(q.Get("path")) == "" {
		http.Error(w, "Not an image", http.StatusNotFound)
		return
	}
	before, after, err := changeSides(root, q.Get("path"), q.Get("orig"), q.Get("mode"), q.Get("hash"))
	if err != nil {
		fail(w, err)
		return
	}
	var b *blobRef
	switch q.Get("side") {
	case "before":
		b = before
	case "after":
		b = after
	default:
		http.Error(w, "bad side", http.StatusBadRequest)
		return
	}
	ct := ""
	if b != nil {
		ct = imageType(b.path)
	}
	if ct == "" {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	if b.oid == "" {
		p, err := resolve(root, b.path)
		if err != nil {
			fail(w, err)
			return
		}
		serveImageFile(w, r, p, ct)
		return
	}
	size, err := blobSize(root, b.oid)
	if err != nil {
		fail(w, err)
		return
	}
	if size > git.MaxFileSize {
		http.Error(w, "Image too large", http.StatusRequestEntityTooLarge)
		return
	}
	etag := `"` + b.oid + `"`
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	cmd := git.Command(r.Context(), root, "cat-file", "blob", b.oid)
	cmd.Stdout = w
	cmd.Run() // the headers are sent; a failure here can only cut the answer short
}

func serveImageFile(w http.ResponseWriter, r *http.Request, p, ct string) {
	f, err := os.Open(p)
	if err != nil {
		fail(w, err)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		fail(w, err)
		return
	}
	if st.IsDir() {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	if st.Size() > git.MaxFileSize {
		http.Error(w, "Image too large", http.StatusRequestEntityTooLarge)
		return
	}
	w.Header().Set("Content-Type", ct)
	http.ServeContent(w, r, "", st.ModTime(), f)
}
