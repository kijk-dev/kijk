package git

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// A pull request's head lives in kijk's own ref, out of the user's branches; all worktrees of a
// repo share it. Its base, when kijk had to fetch it, has its own ref too.
func prRef(n int) string   { return "refs/kijk/pr/" + strconv.Itoa(n) }
func baseRef(n int) string { return "refs/kijk/base/" + strconv.Itoa(n) }

// FetchPR makes refs/kijk/pr/<n> point at head. On GitHub a missing base commit goes to
// refs/kijk/base/<n>, as moving origin/<base> would change the Git tab's behind counts and graph.
func FetchPR(root, kind string, n int, head, base, baseBranch string) (fetched bool, err error) {
	if (head != "" && !ValidHash(head)) || (base != "" && !ValidHash(base)) || n <= 0 {
		return false, ErrBadHash
	}
	// Only GitHub's base is fetched, so only there does a missing base call for a fetch.
	if head != "" && (kind != "github" || base == "" || hasCommit(root, base)) {
		if at, _ := Run(root, "rev-parse", "--verify", "-q", prRef(n)); at == head {
			return false, nil
		}
	}
	src := "refs/pull/" + strconv.Itoa(n) + "/head"
	if kind == "gitlab" {
		src = "refs/merge-requests/" + strconv.Itoa(n) + "/head"
	}
	specs := []string{"+" + src + ":" + prRef(n)}
	if kind == "github" && base != "" && !hasCommit(root, base) && validBranch(root, baseBranch) {
		specs = append(specs, "+refs/heads/"+baseBranch+":"+baseRef(n))
	}
	// An empty refmap keeps git from also moving origin/<base> when it fetches that branch.
	out, err := network(root, append([]string{"fetch", "--no-tags", "--no-write-fetch-head", "--refmap=", "origin"}, specs...)...)
	if err != nil {
		first, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
		if first == "" {
			first = err.Error()
		}
		return false, errors.New(first)
	}
	return true, nil
}

func hasCommit(root, hash string) bool {
	_, err := Run(root, "cat-file", "-e", hash+"^{commit}")
	return err == nil
}

func validBranch(root, name string) bool {
	if name == "" || strings.HasPrefix(name, "-") {
		return false
	}
	_, err := Run(root, "check-ref-format", "--branch", name)
	return err == nil
}

// MergeBase is where head split from base, the start of a forge diff.
func MergeBase(root, base, head string) (string, error) {
	if !ValidHash(base) || !ValidHash(head) {
		return "", ErrBadHash
	}
	key := "pr " + base + " " + head
	splitCache.Lock()
	fork, ok := splitCache.m[key]
	splitCache.Unlock()
	if ok {
		return fork, nil
	}
	fork, err := Run(root, "merge-base", base, head)
	if err != nil {
		return "", err
	}
	keepSplit(key, fork)
	return fork, nil
}

// PRHead is the commit refs/kijk/pr/<n> points at; empty before the first fetch.
func PRHead(root string, n int) string {
	h, _ := Run(root, "rev-parse", "--verify", "-q", prRef(n)+"^{commit}")
	return h
}

// PRFiles lists the files of a forge diff, with their line counts and blobs.
func PRFiles(root, base, head string) ([]change, error) {
	if !ValidHash(base) || !ValidHash(head) {
		return nil, ErrBadHash
	}
	raw, err := Raw(root, "diff", "--no-ext-diff", "--raw", "--no-abbrev", "-z", "-M", base, head)
	if err != nil {
		return nil, err
	}
	cs := parseRaw(raw)
	setCounts(cs, numstat(root, "-M", base, head))
	slices.SortFunc(cs, func(x, y change) int { return strings.Compare(x.Path, y.Path) })
	return cs, nil
}

// DeletePRRef removes the refs of a pull request that closed.
func DeletePRRef(root string, n int) {
	Run(root, "update-ref", "-d", prRef(n))
	Run(root, "update-ref", "-d", baseRef(n))
}

// SweepPRRefs removes the refs of pull requests that are not open any more.
func SweepPRRefs(root string, open []int) {
	out, err := Run(root, "for-each-ref", "--format=%(refname)", "refs/kijk/pr/", "refs/kijk/base/")
	if err != nil || out == "" {
		return
	}
	for _, ref := range strings.Split(out, "\n") {
		n, err := strconv.Atoi(ref[strings.LastIndex(ref, "/")+1:])
		if err == nil && !slices.Contains(open, n) {
			DeletePRRef(root, n)
		}
	}
}

// PRFile is a file as it is at pull request n's head, written once into dir by its blob hash, so
// it can be read like a file on disk.
func PRFile(root string, n int, path, dir string) (string, error) {
	if !filepath.IsLocal(filepath.FromSlash(path)) {
		return "", ErrOutside
	}
	blob, err := Run(root, "rev-parse", "--verify", "-q", prRef(n)+":"+path)
	if err != nil || !ValidHash(blob) {
		return "", os.ErrNotExist
	}
	if t, _ := Run(root, "cat-file", "-t", blob); t != "blob" {
		return "", os.ErrNotExist
	}
	// The file keeps its name, so it shows and colors as the original does.
	p := filepath.Join(dir, blob, filepath.Base(filepath.FromSlash(path)))
	if _, err := os.Stat(p); err == nil {
		now := time.Now()
		os.Chtimes(filepath.Dir(p), now, now) // still in use: not pruned
		return p, nil
	}
	pruneFiles(dir)
	if size, _ := Run(root, "cat-file", "-s", blob); len(size) > 0 {
		if n, _ := strconv.Atoi(size); n > MaxFileSize {
			return "", errors.New("file too large")
		}
	}
	b, err := Raw(root, "cat-file", "blob", blob)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return "", err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return "", err
	}
	return p, os.Rename(tmp, p)
}

// PRSplit is where a pull request's forge diff starts: GitLab's own merge base when the repo has
// it, else the merge base of the target's commit and head.
func PRSplit(root string, pr *PR, head string) (string, error) {
	if ValidHash(pr.MergeBase) && hasCommit(root, pr.MergeBase) {
		return pr.MergeBase, nil
	}
	base := pr.BaseOid
	if !ValidHash(base) || !hasCommit(root, base) {
		// kijk's own fetch of the target is newer than the user's origin/<base>.
		base, _ = Run(root, "rev-parse", "--verify", "-q", baseRef(pr.Number)+"^{commit}")
	}
	if base == "" && validBranch(root, pr.Base) {
		base, _ = Run(root, "rev-parse", "--verify", "-q", "refs/remotes/origin/"+pr.Base+"^{commit}")
	}
	if base == "" {
		return "", errors.New("the target branch " + pr.Base + " is not in this repo")
	}
	return MergeBase(root, base, head)
}

// FileAt is a file's text at a commit.
func FileAt(root, commit, path string) (string, error) {
	if !ValidHash(commit) {
		return "", ErrBadHash
	}
	if !filepath.IsLocal(filepath.FromSlash(path)) {
		return "", ErrOutside
	}
	b, err := Raw(root, "show", commit+":"+path)
	return string(b), err
}

// pruneFiles removes the files of pull requests that were not read for a day.
func pruneFiles(dir string) {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		info, err := e.Info()
		if err == nil && e.IsDir() && ValidHash(e.Name()) && time.Since(info.ModTime()) > 24*time.Hour {
			os.RemoveAll(filepath.Join(dir, e.Name()))
		}
	}
}
