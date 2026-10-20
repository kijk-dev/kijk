package viewer

import (
	"os"
	"path/filepath"

	"github.com/kijk-dev/kijk/internal/git"
)

// diffReply adds the two pictures of a changed image or diagram to its diff.
type diffReply struct {
	git.DiffResult
	Image   *imageSides   `json:"image,omitempty"`
	Diagram *diagramSides `json:"diagram,omitempty"`
}

// diff is git.Diff, plus mode "untracked": git has nothing to compare a new file with, so its
// lines come from disk.
func diff(root, path, orig, mode, hash string, context int) (git.DiffResult, error) {
	if mode != "untracked" {
		return git.Diff(root, path, orig, mode, hash, context)
	}
	if !filepath.IsLocal(filepath.FromSlash(path)) || (orig != "" && !filepath.IsLocal(filepath.FromSlash(orig))) {
		return git.DiffResult{}, git.ErrOutside
	}
	return untrackedDiff(root, path)
}

func untrackedDiff(root, path string) (git.DiffResult, error) {
	p, err := resolve(root, path)
	if err != nil {
		return git.DiffResult{}, err
	}
	st, err := os.Stat(p)
	if err != nil {
		return git.DiffResult{}, err
	}
	if st.IsDir() {
		return git.DiffResult{Folder: true, Hunks: []git.Hunk{}}, nil
	}
	idx, err := buildIndex(p, st)
	if err != nil {
		return git.DiffResult{}, err
	}
	if idx.Binary || idx.TooLarge {
		return git.DiffResult{Binary: true, Hunks: []git.Hunk{}}, nil
	}
	lines, err := readLines(p, idx, 0, idx.Lines)
	if err != nil {
		return git.DiffResult{}, err
	}
	h := git.Hunk{OldStart: 0, NewStart: 1, Lines: make([]git.DiffLine, len(lines))}
	for i, l := range lines {
		h.Lines[i] = git.DiffLine{Kind: "+", New: i + 1, Text: l}
	}
	return git.DiffResult{Hunks: []git.Hunk{h}}, nil
}
