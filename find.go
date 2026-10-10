package main

import (
	"bufio"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf16"
)

const maxMatches = 10000

// match is one hit of find in file. From and To count UTF-16 units, as JavaScript strings do.
type match struct {
	Line int `json:"l"` // 1-based
	From int `json:"f"`
	To   int `json:"t"`
}

type findResult struct {
	Matches []match `json:"matches"`
	More    bool    `json:"more,omitempty"` // stopped at maxMatches
}

// findInFile searches a text file for q, ignoring case.
func findInFile(p string, idx *fileIndex, q string) (findResult, error) {
	res := findResult{Matches: []match{}}
	if q == "" || idx.Binary || idx.TooLarge {
		return res, nil
	}
	f, err := os.Open(p)
	if err != nil {
		return res, err
	}
	defer f.Close()
	lq := strings.ToLower(q)
	needle := utf16.Encode([]rune(lowerRunes(q)))
	r := bufio.NewReaderSize(f, 64<<10)
	for n := 1; ; n++ {
		line, err := r.ReadString('\n')
		line = shownLine(line)
		if strings.Contains(strings.ToLower(line), lq) {
			hay := utf16.Encode([]rune(lowerRunes(line)))
			for i := 0; i+len(needle) <= len(hay); {
				if equal16(hay[i:i+len(needle)], needle) {
					if len(res.Matches) == maxMatches {
						res.More = true
						return res, nil
					}
					res.Matches = append(res.Matches, match{n, i, i + len(needle)})
					i += len(needle)
				} else {
					i++
				}
			}
		}
		if err != nil {
			return res, nil
		}
	}
}

// lowerRunes lowers each rune on its own, so the rune count and the offsets stay the same.
func lowerRunes(s string) string {
	return strings.Map(unicode.ToLower, s)
}

func equal16(a, b []uint16) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

const maxFiles = 100000

var fileLists = struct {
	sync.Mutex
	m map[string]fileList
}{m: map[string]fileList{}}

type fileList struct {
	at    time.Time
	paths []string
}

// folderFiles lists the files go to file can open: what git tracks or would track, else every
// file outside .git. A short cache spares git on every key press.
func folderFiles(root string) []string {
	fileLists.Lock()
	l, ok := fileLists.m[root]
	fileLists.Unlock()
	if ok && time.Since(l.at) < 3*time.Second {
		return l.paths
	}
	var paths []string
	if out, err := gitRaw(root, "ls-files", "-z", "--cached", "--others", "--exclude-standard"); err == nil {
		seen := map[string]bool{}
		for _, p := range strings.Split(string(out), "\x00") {
			if p == "" || seen[p] || strings.HasSuffix(p, "/") {
				continue
			}
			seen[p] = true
			if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(p))); err == nil { // a deleted tracked file stays in the index
				paths = append(paths, p)
			}
			if len(paths) == maxFiles {
				break
			}
		}
	} else {
		filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() && d.Name() == ".git" {
				return filepath.SkipDir
			}
			if !d.IsDir() {
				rel, _ := filepath.Rel(root, p)
				paths = append(paths, filepath.ToSlash(rel))
			}
			if len(paths) == maxFiles {
				return filepath.SkipAll
			}
			return nil
		})
	}
	sort.Strings(paths)
	fileLists.Lock()
	if len(fileLists.m) > 50 {
		clear(fileLists.m)
	}
	fileLists.m[root] = fileList{time.Now(), paths}
	fileLists.Unlock()
	return paths
}

type fileHit struct {
	Path string `json:"path"`
	Hits []int  `json:"hits"` // indexes of the matched characters in Path, for bold letters
}

// gotoFile ranks the folder's files against a typed query, best first.
func gotoFile(paths []string, q string, limit int) []fileHit {
	type scored struct {
		fileHit
		score int
	}
	var all []scored
	for _, p := range paths {
		if hits, score, ok := fuzzy(q, p); ok {
			all = append(all, scored{fileHit{p, hits}, score})
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].score != all[j].score {
			return all[i].score > all[j].score
		}
		if len(all[i].Path) != len(all[j].Path) {
			return len(all[i].Path) < len(all[j].Path)
		}
		return all[i].Path < all[j].Path
	})
	out := []fileHit{}
	for i := 0; i < len(all) && i < limit; i++ {
		out = append(out, all[i].fileHit)
	}
	return out
}

// fuzzy matches the letters of q in order, ignoring case and spaces. It tries the file name
// first, then the whole path. Letters that start a word or follow the previous one score more,
// so "usrsvc" finds src/user/UserService.kt. A "/" in q matches against the whole path only.
func fuzzy(q, p string) ([]int, int, bool) {
	q = strings.ToLower(strings.ReplaceAll(q, " ", ""))
	if q == "" {
		return nil, 0, false
	}
	rq, rp := []rune(q), []rune(p)
	try := func(from int) ([]int, int, bool) {
		if hits, score, ok := subsequence(rq, rp, from, true); ok {
			return hits, score, true
		}
		return subsequence(rq, rp, from, false)
	}
	if !strings.Contains(q, "/") {
		if hits, score, ok := try(len([]rune(p[:strings.LastIndexByte(p, '/')+1]))); ok {
			return hits, score + 1000, true
		}
	}
	return try(0)
}

// subsequence finds q's letters in p from index from on. With starts, a word start ahead beats the
// next plain letter; that can miss a match the plain way finds, so callers try both.
func subsequence(q, p []rune, from int, starts bool) ([]int, int, bool) {
	hits := make([]int, 0, len(q))
	score, prev, j := 0, -2, from
	for _, c := range q {
		at := -1
		for k := j; starts && k < len(p); k++ {
			if unicode.ToLower(p[k]) == c && wordStart(p, k) {
				at = k
				break
			}
		}
		next := -1
		for k := j; k < len(p); k++ {
			if unicode.ToLower(p[k]) == c {
				next = k
				break
			}
		}
		if next < 0 {
			return nil, 0, false
		}
		if at < 0 || next == prev+1 {
			at = next
		}
		switch {
		case at == prev+1:
			score += 15
		case wordStart(p, at):
			score += 10
		default:
			score -= at - j
		}
		hits = append(hits, at)
		prev, j = at, at+1
	}
	return hits, score, true
}

func wordStart(p []rune, k int) bool {
	if k == 0 {
		return true
	}
	a, b := p[k-1], p[k]
	return strings.ContainsRune("/_-. ", a) || (unicode.IsLower(a) && unicode.IsUpper(b)) || (unicode.IsLetter(a) && unicode.IsDigit(b))
}
