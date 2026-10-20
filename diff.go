package main

import (
	"os"
	"path/filepath"

	kgit "github.com/kijk-dev/kijk/internal/git"
)

// diffReply adds the two pictures of a changed image or diagram to its diff.
type diffReply struct {
	kgit.DiffResult
	Image   *imageSides   `json:"image,omitempty"`
	Diagram *diagramSides `json:"diagram,omitempty"`
}

// diff is kgit.Diff, plus mode "untracked": git has nothing to compare a new file with, so its
// lines come from disk.
func diff(root, path, orig, mode, hash string, context int) (kgit.DiffResult, error) {
	if mode != "untracked" {
		return kgit.Diff(root, path, orig, mode, hash, context)
	}
	if !filepath.IsLocal(filepath.FromSlash(path)) || (orig != "" && !filepath.IsLocal(filepath.FromSlash(orig))) {
		return kgit.DiffResult{}, kgit.ErrOutside
	}
	return untrackedDiff(root, path)
}

func untrackedDiff(root, path string) (kgit.DiffResult, error) {
	p, err := resolve(root, path)
	if err != nil {
		return kgit.DiffResult{}, err
	}
	st, err := os.Stat(p)
	if err != nil {
		return kgit.DiffResult{}, err
	}
	if st.IsDir() {
		return kgit.DiffResult{Folder: true, Hunks: []kgit.Hunk{}}, nil
	}
	idx, err := buildIndex(p, st)
	if err != nil {
		return kgit.DiffResult{}, err
	}
	if idx.Binary || idx.TooLarge {
		return kgit.DiffResult{Binary: true, Hunks: []kgit.Hunk{}}, nil
	}
	lines, err := readLines(p, idx, 0, idx.Lines)
	if err != nil {
		return kgit.DiffResult{}, err
	}
	h := kgit.Hunk{OldStart: 0, NewStart: 1, Lines: make([]kgit.DiffLine, len(lines))}
	for i, l := range lines {
		h.Lines[i] = kgit.DiffLine{Kind: "+", New: i + 1, Text: l}
	}
	return kgit.DiffResult{Hunks: []kgit.Hunk{h}}, nil
}
