package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/kijk-dev/kijk/internal/git"
)

// savedFolder is one folder of the folders list: its row's folder, and the folder given last
// inside it.
type savedFolder struct {
	Path string `json:"path"`
	Open string `json:"open,omitempty"`
}

type foldersFile struct {
	Version int           `json:"version"`
	Folders []savedFolder `json:"folders"`
}

func foldersPath() (string, error) {
	dir, err := userConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "kijk", "folders.json"), nil
}

// errMovedAway marks a broken folders.json that is now folders.json.bad, so a save loses nothing.
var errMovedAway = errors.New("moved it to folders.json.bad")

// readFolders reads folders.json. A missing file is an empty list. A broken file moves to
// folders.json.bad, so the next save cannot lose what was in it.
func readFolders(path string) ([]savedFolder, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var f foldersFile
	if err := json.Unmarshal(b, &f); err != nil {
		if rerr := os.Rename(path, path+".bad"); rerr != nil {
			return nil, fmt.Errorf("%s is not valid JSON, and kijk cannot move it away: %w", path, rerr)
		}
		return nil, fmt.Errorf("%s was not valid JSON, so kijk %w and starts with an empty list", path, errMovedAway)
	}
	var list []savedFolder
	for _, sf := range f.Folders {
		if filepath.IsAbs(sf.Path) {
			list, _ = addFolder(list, sf)
		}
	}
	return list, nil
}

func writeFolders(path string, list []savedFolder) error {
	if list == nil {
		list = []savedFolder{}
	}
	return writeJSON(path, foldersFile{Version: 1, Folders: list})
}

// addFolder puts f at the end of the list. When its row is listed already, the row keeps its
// place and opens where f says. added is false then.
func addFolder(list []savedFolder, f savedFolder) (out []savedFolder, added bool) {
	if i := indexOf(list, f.Path); i >= 0 {
		list[i].Open = f.Open
		return list, false
	}
	return append(list, f), true
}

func indexOf(list []savedFolder, path string) int {
	return slices.IndexFunc(list, func(f savedFolder) bool { return f.Path == path })
}

// tilde shortens a path in the home folder for the user to read.
func tilde(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if rest, ok := strings.CutPrefix(p, home+string(filepath.Separator)); ok {
		return "~" + string(filepath.Separator) + rest
	}
	return p
}

// checkFolder turns a folder given on the command line into its row. Inside a git repository the
// row is the repository's top folder, and Open is the folder given.
func checkFolder(arg, cwd string) (savedFolder, error) {
	home, _ := os.UserHomeDir()
	p := arg
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		p = filepath.Join(home, p[1:])
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(cwd, p)
	}
	p = filepath.Clean(p)
	show := tilde(p)
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return savedFolder{}, fmt.Errorf("%s: no such folder. Nothing was added", show)
	}
	st, err := os.Stat(real)
	if err != nil {
		return savedFolder{}, fmt.Errorf("%s: no such folder. Nothing was added", show)
	}
	if !st.IsDir() {
		return savedFolder{}, fmt.Errorf("%s is a file. Pass a folder", show)
	}
	if tooBig(real, home) {
		return savedFolder{}, fmt.Errorf("%s is too big to open. Pick a folder inside it", show)
	}
	row := real
	if top, err := git.Run(real, "rev-parse", "--show-toplevel"); err == nil && top != "" {
		row = filepath.FromSlash(top)
		if tooBig(row, home) {
			return savedFolder{}, fmt.Errorf("%s is inside a git repository at %s, which is too big to open", show, tilde(row))
		}
	}
	f := savedFolder{Path: row}
	if real != row {
		f.Open = real
	}
	return f, nil
}

// tooBig refuses the home folder and the root of a drive: kijk would watch every folder in them.
func tooBig(p, home string) bool {
	if p == filepath.Dir(p) {
		return true
	}
	if home == "" {
		return false
	}
	if real, err := filepath.EvalSymlinks(home); err == nil {
		home = real
	}
	return p == home
}

// checkFolders checks every folder before any is added, so a typo adds nothing.
func checkFolders(args []string, cwd string) ([]savedFolder, error) {
	var out []savedFolder
	for _, a := range args {
		f, err := checkFolder(a, cwd)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, nil
}

// folderList is the saved folders list of a running kijk, which is the only writer of
// folders.json while it runs.
type folderList struct {
	path    string
	rl      *rail
	changed chan struct{}

	mu      sync.Mutex
	list    []savedFolder
	readErr error // kept for the Folders list; unless the file moved away, changes are not saved
	saveErr error
	removed *removedFolder
	asked   map[string]bool // folders to count now: the open folder changed
}

// removedFolder is the last removal, so Undo can put it back in its place.
type removedFolder struct {
	f  savedFolder
	at int
}

// newFolderList reads the list at path. pathErr says why there is no path.
func newFolderList(path string, pathErr error, rl *rail) *folderList {
	l := &folderList{path: path, rl: rl, changed: make(chan struct{}, 1)}
	if pathErr != nil {
		l.readErr = fmt.Errorf("cannot find the folders list: %w", pathErr)
	} else {
		l.list, l.readErr = readFolders(path)
	}
	rl.setSaved(l.list, l.readErr)
	return l
}

// changedLocked saves the list and gives it to the rail. l.mu is held.
func (l *folderList) changedLocked() {
	l.saveErr = nil
	if l.readErr != nil && !errors.Is(l.readErr, errMovedAway) {
		l.saveErr = errors.New("changes to the folders list are not saved, so kijk does not overwrite a file it could not read")
	} else if err := writeFolders(l.path, l.list); err != nil {
		l.saveErr = fmt.Errorf("cannot save the folders list: %w", err)
	}
	l.rl.setSaved(l.list, errors.Join(l.readErr, l.saveErr))
	l.wakeLocked()
}

func (l *folderList) wakeLocked() {
	select {
	case l.changed <- struct{}{}:
	default:
	}
}

// add adds fs, which the caller just checked, so they are counted again at once: an old count may
// have found one missing.
func (l *folderList) add(fs []savedFolder) (added []bool, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, f := range fs {
		var a bool
		l.list, a = addFolder(l.list, f)
		added = append(added, a)
		l.askLocked(f.Path)
	}
	l.changedLocked()
	return added, l.saveErr
}

func (l *folderList) remove(root string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	i := indexOf(l.list, root)
	if i < 0 {
		return false
	}
	l.removed = &removedFolder{l.list[i], i}
	l.list = slices.Delete(l.list, i, i+1)
	l.changedLocked()
	return true
}

// restore undoes the last removal, if root is what it removed.
func (l *folderList) restore(root string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	r := l.removed
	if r == nil || r.f.Path != root || indexOf(l.list, root) >= 0 {
		return false
	}
	l.removed = nil
	l.list = slices.Insert(l.list, min(r.at, len(l.list)), r.f)
	l.changedLocked()
	return true
}

// paths are the saved folders, in list order.
func (l *folderList) paths() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	paths := make([]string, len(l.list))
	for i, f := range l.list {
		paths[i] = f.Path
	}
	return paths
}

func (l *folderList) count() int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.list)
}

// refresh asks for one folder's line counts now, so the open folder's counts follow live updates.
func (l *folderList) refresh(root string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if indexOf(l.list, root) < 0 {
		return false
	}
	l.askLocked(root)
	l.wakeLocked()
	return true
}

// askLocked has watch count root now. l.mu is held.
func (l *folderList) askLocked(root string) {
	if l.asked == nil {
		l.asked = map[string]bool{}
	}
	l.asked[root] = true
}

// snapshot gives the list, and takes the folders asked for since the last call.
func (l *folderList) snapshot() ([]savedFolder, map[string]bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	asked := l.asked
	l.asked = nil
	return slices.Clone(l.list), asked
}

func folderFactsOf(root string) folderFacts {
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		return folderFacts{missing: true}
	}
	var ff folderFacts
	ff.branch, ff.add, ff.del = git.Uncommitted(root)
	return ff
}

// folderRefresh is how often each folder is counted. Folders take turns, one git status at a time,
// so many folders or one big repo never start a burst of git processes.
const folderRefresh = 10 * time.Second

// watch counts the saved folders' lines while a window is open, and for a minute after, so a
// reload does not start a burst. A new folder, or one the window asks for, is counted at once.
func (l *folderList) watch(h *hub) {
	facts := map[string]folderFacts{}
	turn := 0
	var due time.Time
	for {
		opened := h.opened()
		wait := time.Second
		if !h.wanted(time.Now(), time.Minute) {
			if len(facts) > 0 {
				clear(facts)
				l.rl.setFacts(nil)
			}
		} else {
			list, asked := l.snapshot()
			var stale []string
			for _, f := range list {
				if _, ok := facts[f.Path]; !ok || asked[f.Path] {
					stale = append(stale, f.Path)
				}
			}
			now := time.Now()
			if len(stale) == 0 && len(list) > 0 && !now.Before(due) {
				stale = append(stale, list[turn%len(list)].Path)
				turn++
				due = now.Add(folderRefresh / time.Duration(len(list)))
			}
			changed := len(stale) > 0
			countFolders(stale, facts)
			for p := range facts {
				if indexOf(list, p) < 0 {
					delete(facts, p)
					changed = true
				}
			}
			if changed {
				l.rl.setFacts(maps.Clone(facts))
			}
			if len(list) > 0 {
				wait = min(wait, max(time.Until(due), 0))
			}
		}
		select {
		case <-l.changed:
		case <-opened:
		case <-time.After(wait):
		}
	}
}

// countFolders counts the lines of roots, in parallel when a window first opens with many folders.
func countFolders(roots []string, facts map[string]folderFacts) {
	got := make([]folderFacts, len(roots))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for i, root := range roots {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			got[i] = folderFactsOf(root)
			<-sem
		}()
	}
	wg.Wait()
	for i, root := range roots {
		facts[root] = got[i]
	}
}
