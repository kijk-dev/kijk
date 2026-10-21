package git

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
)

// ErrNoBranch means HEAD has no branch changes: it is on the main line, or there is no main line.
var ErrNoBranch = errors.New("this branch has no branch changes")

type BranchFile struct {
	change
	NotPushed bool `json:"notPushed,omitempty"` // the upstream does not have this file
}

// Branch is the branch changes: every file the branch changes against its base, the same files
// the branch total counts.
type Branch struct {
	Branch      string       `json:"branch"`
	Base        string       `json:"base"`  // the PR target or the main line
	Split       string       `json:"split"` // where HEAD split from the base
	Upstream    string       `json:"upstream,omitempty"`
	UpHash      string       `json:"upHash,omitempty"` // where the upstream is, so the page notices a push
	Ahead       int          `json:"ahead"`            // commits the upstream does not have
	Uncommitted int          `json:"uncommitted"`      // files with changes not committed
	PR          *pullRequest `json:"pr,omitempty"`
	Files       []BranchFile `json:"files"`
}

// BranchSplit is where HEAD split from the PR target, else the main line; empty when HEAD has no
// branch changes.
func BranchSplit(root, target string) string {
	main, base, fork := branchBase(root, target)
	if fork == "" {
		return ""
	}
	b, _ := Run(root, "symbolic-ref", "--short", "-q", "HEAD")
	if onBase(b, main, base) {
		return ""
	}
	return fork
}

// BranchChanges lists the branch changes: the commits since the split point plus the work not
// committed, untracked files included.
func BranchChanges(root, target string) (Branch, error) {
	main, base, fork := branchBase(root, target)
	b := Branch{Base: base, Split: fork, Files: []BranchFile{}}
	out, err := Raw(root, "status", "--porcelain=v2", "-z", "--branch", "-uall")
	if err != nil {
		return b, err
	}
	var st Status
	parseStatus(out, &st)
	if fork == "" || onBase(st.Branch, main, base) {
		return b, ErrNoBranch
	}
	b.Branch, b.Upstream, b.Ahead = st.Branch, st.Upstream, st.Ahead
	raw, err := Raw(root, "diff", "--no-ext-diff", "--name-status", "-z", "-M", fork)
	if err != nil {
		return b, err
	}
	cs := parseNameStatus(raw)
	setCounts(cs, numstat(root, "-M", fork))
	for _, c := range st.Untracked {
		if !strings.HasSuffix(c.Path, "/") {
			c.Add = countLines(filepath.Join(root, filepath.FromSlash(c.Path)))
			cs = append(cs, c)
		}
	}
	slices.SortFunc(cs, func(x, y change) int { return strings.Compare(x.Path, y.Path) })

	uncommitted := map[string]bool{}
	for _, c := range slices.Concat(st.Staged, st.Changes, st.Untracked) {
		if !strings.HasSuffix(c.Path, "/") {
			uncommitted[c.Path] = true
		}
	}
	b.Uncommitted = len(uncommitted)

	// A file is not pushed when the upstream lacks it; without an upstream nothing is pushed.
	added, upErr := map[string]bool{}, errors.New("no upstream")
	if b.Upstream != "" {
		b.UpHash, _ = Run(root, "rev-parse", "--verify", "-q", "@{upstream}")
		var raw []byte
		raw, upErr = Raw(root, "diff", "--no-ext-diff", "--no-renames", "--diff-filter=A", "--name-only", "-z", "@{upstream}")
		for _, p := range strings.Split(string(raw), "\x00") {
			added[p] = true
		}
	}
	for _, c := range cs {
		b.Files = append(b.Files, BranchFile{change: c, NotPushed: c.Status != "D" && (upErr != nil || c.Status == "?" || added[c.Path])})
	}
	b.PR = KnownPR(root)
	return b, nil
}

// NotPushed gives the lines of a file on disk that its upstream does not have, as ranges of line
// numbers. Removed lines are not in it: they have no line on disk.
func NotPushed(root, path string) ([][2]int, error) {
	if !filepath.IsLocal(filepath.FromSlash(path)) {
		return nil, ErrOutside
	}
	all := func() [][2]int {
		if n := countLines(filepath.Join(root, filepath.FromSlash(path))); n > 0 {
			return [][2]int{{1, n}}
		}
		return [][2]int{}
	}
	if _, err := Run(root, "rev-parse", "--verify", "-q", "@{upstream}"); err != nil {
		return all(), nil
	}
	out, err := Raw(root, "diff", "--no-color", "--no-ext-diff", "-U0", "@{upstream}", "--", path)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		if tracked, _ := Run(root, "ls-files", "--", ":(literal)"+path); tracked == "" {
			return all(), nil
		}
	}
	ranges := [][2]int{}
	for _, m := range hunkCountRe.FindAllStringSubmatch(string(out), -1) {
		if start, added := hunkCount(m[2]), hunkCount(m[3]); added > 0 {
			ranges = append(ranges, [2]int{start, start + added - 1})
		}
	}
	return ranges, nil
}
