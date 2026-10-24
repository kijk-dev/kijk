package git

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// MaxFileSize is the largest file kijk reads; a larger file is not shown or counted.
const MaxFileSize = 50 << 20

type lineCount struct{ add, del int }

// parseNumstat reads `git diff --numstat -z` into counts by path. A rename is keyed by its new
// path. Binary files show "-", which counts as zero.
func parseNumstat(out []byte) map[string]lineCount {
	m := map[string]lineCount{}
	f := strings.Split(string(out), "\x00")
	for i := 0; i < len(f); i++ {
		parts := strings.SplitN(strings.TrimLeft(f[i], "\n"), "\t", 3)
		if len(parts) < 3 {
			continue
		}
		path := parts[2]
		if path == "" && i+2 < len(f) { // a rename: "add\tdel\t", then the old and the new path
			path = f[i+2]
			i += 2
		}
		a, _ := strconv.Atoi(parts[0])
		d, _ := strconv.Atoi(parts[1])
		m[path] = lineCount{a, d}
	}
	return m
}

func numstat(root string, args ...string) map[string]lineCount {
	out, err := Raw(root, append([]string{"diff", "--no-ext-diff", "--numstat", "-z"}, args...)...)
	if err != nil {
		return map[string]lineCount{}
	}
	return parseNumstat(out)
}

// numstatOf diffs only the files git status listed, so git does not look at every file on disk.
func numstatOf(root string, cs []change, args ...string) map[string]lineCount {
	if len(cs) == 0 {
		return map[string]lineCount{}
	}
	if len(cs) <= 500 { // beyond that a full diff is cheaper than a huge argument list
		args = append(args, "--")
		for _, c := range cs {
			args = append(args, ":(literal)"+c.Path)
			if c.Orig != "" {
				args = append(args, ":(literal)"+c.Orig)
			}
		}
	}
	return numstat(root, args...)
}

func setCounts(cs []change, m map[string]lineCount) {
	for i := range cs {
		n := m[cs[i].Path]
		cs[i].Add, cs[i].Del = n.add, n.del
	}
}

// Uncommitted gives the branch and the lines added and removed since the last commit, with one
// git status and one git diff. Outside git it gives nothing.
func Uncommitted(root string) (branch string, add, del int) {
	out, err := Raw(root, "status", "--porcelain=v2", "-z", "--branch", "-uall")
	if err != nil {
		return "", 0, 0
	}
	var st Status
	parseStatus(out, &st)
	branch = st.Branch
	if branch == "" {
		branch, _ = Run(root, "rev-parse", "--short", "HEAD")
	}
	for _, n := range numstatOf(root, append(st.Staged, st.Changes...), "-M", "HEAD") {
		add += n.add
		del += n.del
	}
	for _, c := range st.Untracked {
		if !strings.HasSuffix(c.Path, "/") {
			add += countLines(filepath.Join(root, filepath.FromSlash(c.Path)))
		}
	}
	return branch, add, del
}

type lineCacheEntry struct {
	size, mtime int64
	lines       int
}

var lineCache = struct {
	sync.Mutex
	m map[string]lineCacheEntry
}{m: map[string]lineCacheEntry{}}

// countLines counts a new file's lines the way git does: a last line without a newline counts.
// Binary files, files over MaxFileSize and folders count zero.
func countLines(path string) int {
	st, err := os.Stat(path)
	if err != nil || !st.Mode().IsRegular() || st.Size() > MaxFileSize {
		return 0
	}
	lineCache.Lock()
	e, ok := lineCache.m[path]
	lineCache.Unlock()
	if ok && e.size == st.Size() && e.mtime == st.ModTime().UnixNano() {
		return e.lines
	}
	n := lineTotal(path)
	lineCache.Lock()
	if len(lineCache.m) > 2000 {
		clear(lineCache.m)
	}
	lineCache.m[path] = lineCacheEntry{st.Size(), st.ModTime().UnixNano(), n}
	lineCache.Unlock()
	return n
}

func lineTotal(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	buf := make([]byte, 256<<10)
	n, last, first := 0, byte('\n'), true
	for {
		k, err := f.Read(buf)
		if first && IsBinary(buf[:k]) {
			return 0
		}
		first = false
		if k > 0 {
			n += bytes.Count(buf[:k], []byte{'\n'})
			last = buf[k-1]
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0
		}
	}
	if last != '\n' {
		n++
	}
	return n
}

type branchTotal struct {
	Add  int    `json:"add"`
	Del  int    `json:"del"`
	Base string `json:"base"` // the PR target or the main line
}

// The split point only moves when HEAD or the base does, and merge-base is slow on big histories.
var splitCache = struct {
	sync.Mutex
	m map[string]string
}{m: map[string]string{}}

func splitPoint(root, base string) string {
	key, err := Run(root, "rev-parse", "HEAD", base)
	if err != nil {
		return ""
	}
	splitCache.Lock()
	fork, ok := splitCache.m[key]
	splitCache.Unlock()
	if ok {
		return fork
	}
	fork, _ = Run(root, "merge-base", "HEAD", base)
	splitCache.Lock()
	if len(splitCache.m) > 100 {
		clear(splitCache.m)
	}
	splitCache.m[key] = fork
	splitCache.Unlock()
	return fork
}

// branchBase finds what a branch is compared with: the PR target, else the main line, and where
// HEAD split from it. Each is empty when there is none.
func branchBase(root, target string) (main, base, fork string) {
	main = mainRef(root)
	base = main
	if tr := targetRef(root, target); tr != "" {
		base = tr
	}
	if base != "" {
		fork = splitPoint(root, base)
	}
	return main, base, fork
}

// onBase tells whether HEAD is on the main line or the base itself, or on no branch: then it has
// no branch total.
func onBase(branch, main, base string) bool {
	return branch == "" || branch == strings.TrimPrefix(main, "origin/") || branch == strings.TrimPrefix(base, "origin/")
}

// startTotal counts the branch total next to git status. The returned func waits for it, hides it
// where a folder has none, and adds the untracked counts, which need the status first.
func startTotal(root, target string) func(branch string, untracked []change) *branchTotal {
	var wg sync.WaitGroup
	var main, base string
	var t *branchTotal
	wg.Add(1)
	go func() {
		defer wg.Done()
		var fork string
		main, base, fork = branchBase(root, target)
		if fork == "" {
			return
		}
		t = &branchTotal{Base: base}
		for _, n := range numstat(root, "-M", fork) {
			t.Add += n.add
			t.Del += n.del
		}
	}()
	return func(branch string, untracked []change) *branchTotal {
		wg.Wait()
		if t == nil || onBase(branch, main, base) {
			return nil
		}
		for _, c := range untracked {
			t.Add += c.Add
		}
		return t
	}
}

// IsBinary is git's own test: a NUL byte in the first 8000 bytes.
func IsBinary(head []byte) bool {
	return bytes.IndexByte(head[:min(len(head), 8000)], 0) >= 0
}
