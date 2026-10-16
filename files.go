package main

import (
	"bufio"
	"bytes"
	"cmp"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"
)

const (
	maxFileSize = 50 << 20
	maxLineLen  = 20000 // longer lines are cut, so one minified line cannot flood the page
	indexStride = 64    // the index keeps the start of every 64th line
)

var errOutside = errors.New("path is outside this folder")

// roots are the folders the server may read: row folders of running agents and saved folders.
type roots struct {
	mu      sync.RWMutex
	m       map[string]bool
	folders map[string]bool
}

func (r *roots) set(rows []Row) {
	m := map[string]bool{}
	for _, row := range rows {
		m[row.Root] = true
	}
	r.mu.Lock()
	r.m = m
	r.mu.Unlock()
}

func (r *roots) setFolders(list []savedFolder) {
	m := map[string]bool{}
	for _, f := range list {
		m[f.Path] = true
	}
	r.mu.Lock()
	r.folders = m
	r.mu.Unlock()
}

func (r *roots) has(root string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.m[root] || r.folders[root]
}

// resolve turns a slash-separated path relative to root into a real path inside root.
func resolve(root, rel string) (string, error) {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	p, err := filepath.EvalSymlinks(filepath.Join(realRoot, filepath.FromSlash(rel)))
	if err != nil {
		return "", err
	}
	if !inside(realRoot, p) {
		return "", errOutside
	}
	return p, nil
}

func inside(root, p string) bool {
	r, err := filepath.Rel(root, p)
	return err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator))
}

type entry struct {
	Name    string `json:"name"`
	Dir     bool   `json:"dir"`
	Link    bool   `json:"link,omitempty"`
	Outside bool   `json:"outside,omitempty"`
}

func listDir(root, rel string) ([]entry, error) {
	dir, err := resolve(root, rel)
	if err != nil {
		return nil, err
	}
	realRoot, _ := filepath.EvalSymlinks(root)
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := []entry{}
	for _, de := range des {
		if de.Name() == ".git" {
			continue
		}
		e := entry{Name: de.Name(), Dir: de.IsDir()}
		if de.Type()&os.ModeSymlink != 0 {
			e.Link = true
			full := filepath.Join(dir, de.Name())
			if st, err := os.Stat(full); err == nil {
				e.Dir = st.IsDir()
			}
			if target, err := filepath.EvalSymlinks(full); err == nil && !inside(realRoot, target) {
				e.Outside = true
			}
		}
		out = append(out, e)
	}
	slices.SortFunc(out, func(a, b entry) int {
		if a.Dir != b.Dir {
			if a.Dir {
				return -1
			}
			return 1
		}
		return naturalCompare(a.Name, b.Name)
	})
	return out, nil
}

// shownLine is a raw line as the view shows it. Find uses it too, so its offsets match the view.
func shownLine(line string) string {
	line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	if len(line) > maxLineLen {
		line = strings.ToValidUTF8(line[:maxLineLen], "") + "…"
	}
	if !utf8.ValidString(line) {
		line = strings.ToValidUTF8(line, "�")
	}
	return line
}

// naturalCompare orders names as people count: file2 before file10, ignoring case first.
func naturalCompare(a, b string) int {
	x, y := strings.ToLower(a), strings.ToLower(b)
	for x != "" && y != "" {
		nx, ny := digits(x), digits(y)
		if nx > 0 && ny > 0 {
			p, q := strings.TrimLeft(x[:nx], "0"), strings.TrimLeft(y[:ny], "0")
			if c := cmp.Compare(len(p), len(q)); c != 0 {
				return c
			}
			if c := strings.Compare(p, q); c != 0 {
				return c
			}
			x, y = x[nx:], y[ny:]
			continue
		}
		if x[0] != y[0] {
			return cmp.Compare(x[0], y[0])
		}
		x, y = x[1:], y[1:]
	}
	if c := cmp.Compare(len(x), len(y)); c != 0 {
		return c
	}
	if c := strings.Compare(strings.ToLower(a), strings.ToLower(b)); c != 0 {
		return c
	}
	return strings.Compare(a, b)
}

func digits(s string) int {
	n := 0
	for n < len(s) && s[n] >= '0' && s[n] <= '9' {
		n++
	}
	return n
}

type fileInfo struct {
	Size     int64 `json:"size"`
	Mtime    int64 `json:"mtime"` // unix ns; tells the browser whether a reload changed anything
	Binary   bool  `json:"binary,omitempty"`
	TooLarge bool  `json:"tooLarge,omitempty"`
	Lines    int   `json:"lines"`
	MaxCols  int   `json:"maxCols"`
}

type fileIndex struct {
	fileInfo
	marks []uint32 // byte offset of line i*indexStride
}

// indexCache keeps the line index of recently opened files.
type indexCache struct {
	mu sync.Mutex
	m  map[string]*fileIndex
}

func newIndexCache() *indexCache { return &indexCache{m: map[string]*fileIndex{}} }

func (c *indexCache) get(path string) (*fileIndex, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if st.IsDir() {
		return nil, errors.New("is a folder")
	}
	c.mu.Lock()
	idx, ok := c.m[path]
	c.mu.Unlock()
	if ok && idx.Size == st.Size() && idx.Mtime == st.ModTime().UnixNano() {
		return idx, nil
	}
	idx, err = buildIndex(path, st)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	if len(c.m) >= 16 {
		for k := range c.m {
			delete(c.m, k)
			break
		}
	}
	c.m[path] = idx
	c.mu.Unlock()
	return idx, nil
}

func buildIndex(path string, st os.FileInfo) (*fileIndex, error) {
	idx := &fileIndex{fileInfo: fileInfo{Size: st.Size(), Mtime: st.ModTime().UnixNano()}}
	if st.Size() > maxFileSize {
		idx.TooLarge = true
		return idx, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	head := make([]byte, 8000)
	n, _ := io.ReadFull(f, head)
	if bytes.IndexByte(head[:n], 0) >= 0 { // git's own binary test
		idx.Binary = true
		return idx, nil
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}

	idx.marks = []uint32{0}
	buf := make([]byte, 256<<10)
	var off, lineStart int64
	for {
		n, err := f.Read(buf)
		for i := 0; i < n; {
			j := bytes.IndexByte(buf[i:n], '\n')
			if j < 0 {
				break
			}
			end := off + int64(i+j)
			idx.MaxCols = max(idx.MaxCols, int(end-lineStart))
			idx.Lines++
			lineStart = end + 1
			if idx.Lines%indexStride == 0 {
				idx.marks = append(idx.marks, uint32(lineStart))
			}
			i += j + 1
		}
		off += int64(n)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	if lineStart < off {
		idx.Lines++
		idx.MaxCols = max(idx.MaxCols, int(off-lineStart))
	}
	idx.MaxCols = min(idx.MaxCols, maxLineLen)
	return idx, nil
}

// readLines returns lines [from, to) using the index to seek close to from.
func readLines(path string, idx *fileIndex, from, to int) ([]string, error) {
	to = min(to, idx.Lines)
	if idx.Binary || idx.TooLarge || from >= to {
		return []string{}, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	k := from / indexStride
	if _, err := f.Seek(int64(idx.marks[k]), io.SeekStart); err != nil {
		return nil, err
	}
	r := bufio.NewReaderSize(f, 64<<10)
	out := make([]string, 0, to-from)
	for n := k * indexStride; n < to; n++ {
		line, err := r.ReadString('\n')
		if n >= from {
			out = append(out, shownLine(line))
		}
		if err != nil {
			break
		}
	}
	return out, nil
}
