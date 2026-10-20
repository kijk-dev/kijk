package viewer

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/kijk-dev/kijk/internal/git"
	"github.com/rjeczalik/notify"
)

const (
	quietPeriod = 300 * time.Millisecond
	maxDelay    = time.Second
	maxPaths    = 500
)

type folderChange struct {
	Paths []string `json:"paths"`         // changed paths relative to the root, slash-separated
	All   bool     `json:"all,omitempty"` // too many paths to list: treat everything as changed
	Git   bool     `json:"git,omitempty"` // something under .git changed (stage, commit, checkout)
}

// folderEvents watches one folder for as long as the browser keeps this stream open.
func (a *api) folderEvents(w http.ResponseWriter, r *http.Request) {
	root, real, ok := a.target(w, r)
	if !ok {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	events := make(chan notify.EventInfo, 4096) // notify drops events when this is full
	if err := notify.Watch(filepath.Join(real, "..."), events, notify.All); err != nil {
		http.Error(w, fmt.Sprintf("cannot watch %s: %v", root, err), http.StatusInternalServerError)
		return
	}
	defer notify.Stop(events)
	gitDirs := outsideGitDirs(real)
	for _, d := range gitDirs {
		if err := notify.Watch(filepath.Join(d, "..."), events, notify.All); err != nil {
			http.Error(w, fmt.Sprintf("cannot watch %s: %v", d, err), http.StatusInternalServerError)
			return
		}
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher.Flush()

	var batch *folderChange
	var seen map[string]bool
	var first time.Time
	quiet := time.NewTimer(time.Hour)
	quiet.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev := <-events:
			if batch == nil {
				batch = &folderChange{Paths: []string{}}
				seen = map[string]bool{}
				first = time.Now()
			}
			addChange(batch, seen, real, gitDirs, ev.Path())
			quiet.Reset(min(quietPeriod, maxDelay-time.Since(first)))
		case <-quiet.C:
			b, _ := json.Marshal(batch)
			fmt.Fprintf(w, "data: %s\n\n", b)
			flusher.Flush()
			batch = nil
		}
	}
}

// outsideGitDirs finds a worktree's git folders, which live in the main repository, not under root.
func outsideGitDirs(root string) []string {
	out, err := git.Run(root, "rev-parse", "--absolute-git-dir", "--git-common-dir")
	if err != nil {
		return nil
	}
	var dirs []string
	for _, d := range strings.Split(out, "\n") {
		d = filepath.FromSlash(d)
		if !filepath.IsAbs(d) {
			d = filepath.Join(root, d)
		}
		if real, err := filepath.EvalSymlinks(d); err == nil && !Inside(root, real) && !slices.Contains(dirs, real) {
			dirs = append(dirs, real)
		}
	}
	return dirs
}

func addChange(batch *folderChange, seen map[string]bool, root string, gitDirs []string, path string) {
	for _, d := range gitDirs {
		if Inside(d, path) {
			batch.Git = true
			return
		}
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || !Inside(root, path) {
		return
	}
	rel = filepath.ToSlash(rel)
	if rel == ".git" || strings.HasPrefix(rel, ".git/") {
		batch.Git = true
		return
	}
	if batch.All || seen[rel] {
		return
	}
	if len(batch.Paths) >= maxPaths {
		batch.All, batch.Paths = true, []string{}
		return
	}
	seen[rel] = true
	batch.Paths = append(batch.Paths, rel)
}
