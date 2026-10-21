package git

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// A pull request's head lives in kijk's own ref, out of the user's branches; all worktrees of a
// repo share it.
func prRef(n int) string { return "refs/kijk/pr/" + strconv.Itoa(n) }

// FetchPR makes refs/kijk/pr/<n> point at head, and fetches only when it does not. On GitHub the
// forge diff starts at the target's tip, so when that base commit is missing its branch comes too.
func FetchPR(root, kind string, n int, head, base, baseBranch string) (fetched bool, err error) {
	if (head != "" && !ValidHash(head)) || (base != "" && !ValidHash(base)) || n <= 0 {
		return false, ErrBadHash
	}
	if head != "" {
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
		specs = append(specs, "+refs/heads/"+baseBranch+":refs/remotes/origin/"+baseBranch)
	}
	out, err := network(root, append([]string{"fetch", "--no-tags", "--no-write-fetch-head", "origin"}, specs...)...)
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
	splitCache.Lock()
	splitCache.m[key] = fork
	splitCache.Unlock()
	return fork, nil
}

// PRHead is the commit refs/kijk/pr/<n> points at; empty before the first fetch.
func PRHead(root string, n int) string {
	h, _ := Run(root, "rev-parse", "--verify", "-q", prRef(n)+"^{commit}")
	return h
}

// PRFiles lists the files of a forge diff, with their line counts.
func PRFiles(root, base, head string) ([]change, error) {
	if !ValidHash(base) || !ValidHash(head) {
		return nil, ErrBadHash
	}
	raw, err := Raw(root, "diff", "--no-ext-diff", "--name-status", "-z", "-M", base, head)
	if err != nil {
		return nil, err
	}
	cs := parseNameStatus(raw)
	setCounts(cs, numstat(root, "-M", base, head))
	slices.SortFunc(cs, func(x, y change) int { return strings.Compare(x.Path, y.Path) })
	return cs, nil
}

// DeletePRRef removes the ref of a pull request that closed.
func DeletePRRef(root string, n int) {
	Run(root, "update-ref", "-d", prRef(n))
}

// SweepPRRefs removes the refs of pull requests that are not open any more.
func SweepPRRefs(root string, open []int) {
	out, err := Run(root, "for-each-ref", "--format=%(refname)", "refs/kijk/pr/")
	if err != nil || out == "" {
		return
	}
	for _, ref := range strings.Split(out, "\n") {
		n, err := strconv.Atoi(strings.TrimPrefix(ref, "refs/kijk/pr/"))
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
		return p, nil
	}
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
