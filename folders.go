package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/kijk-dev/kijk/internal/viewer"

	kgit "github.com/kijk-dev/kijk/internal/git"
)

// savedFolder is one row of the folders window: its folder, and the folder given last inside it.
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
	if top, err := kgit.Run(real, "rev-parse", "--show-toplevel"); err == nil && top != "" {
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
	roots   *viewer.Roots
	changed chan struct{}

	mu      sync.Mutex
	list    []savedFolder
	readErr error // kept for the Folders pane; unless the file moved away, changes are not saved
	saveErr error
	removed *removedFolder
	open    openRequest
	asked   map[string]bool // rows to refresh now: the open folder changed
}

// removedFolder is the last removal, so Undo can put it back in its place.
type removedFolder struct {
	f  savedFolder
	at int
}

// openRequest asks the folders window to open a folder. Seq grows, so a window acts on each once.
type openRequest struct {
	Seq  int    `json:"seq"`
	Root string `json:"root,omitempty"`
}

func newFolderList(path string, rts *viewer.Roots) *folderList {
	l := &folderList{path: path, roots: rts, changed: make(chan struct{}, 1)}
	l.list, l.readErr = readFolders(path)
	rts.SetFolders(folderPaths(l.list))
	return l
}

func folderPaths(list []savedFolder) []string {
	paths := make([]string, len(list))
	for i, f := range list {
		paths[i] = f.Path
	}
	return paths
}

// changedLocked saves the list and tells the server and the windows. l.mu is held.
func (l *folderList) changedLocked() {
	l.roots.SetFolders(folderPaths(l.list))
	l.saveErr = nil
	if l.readErr != nil && !errors.Is(l.readErr, errMovedAway) {
		l.saveErr = errors.New("changes to the folders list are not saved, so kijk does not overwrite a file it could not read")
	} else if err := writeFolders(l.path, l.list); err != nil {
		l.saveErr = fmt.Errorf("cannot save the folders list: %w", err)
	}
	l.wakeLocked()
}

func (l *folderList) wakeLocked() {
	select {
	case l.changed <- struct{}{}:
	default:
	}
}

// add adds the folders and asks the folders window to open the last one.
func (l *folderList) add(fs []savedFolder) (added []bool, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, f := range fs {
		var a bool
		l.list, a = addFolder(l.list, f)
		added = append(added, a)
	}
	if len(fs) > 0 {
		l.open = openRequest{l.open.Seq + 1, fs[len(fs)-1].Path}
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

func (l *folderList) count() int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.list)
}

// refresh asks for one row now, so the open folder's line counts follow live updates.
func (l *folderList) refresh(root string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if indexOf(l.list, root) < 0 {
		return false
	}
	if l.asked == nil {
		l.asked = map[string]bool{}
	}
	l.asked[root] = true
	l.wakeLocked()
	return true
}

// snapshot gives the list, and takes the rows asked for since the last call.
func (l *folderList) snapshot() ([]savedFolder, openRequest, map[string]bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	asked := l.asked
	l.asked = nil
	return slices.Clone(l.list), l.open, asked, errors.Join(l.readErr, l.saveErr)
}

// folderRow is one row of the folders window.
type folderRow struct {
	Root    string `json:"root"`
	Repo    string `json:"repo"`
	Parent  string `json:"parent"` // the folder that holds it, with ~ for the home folder
	Branch  string `json:"branch,omitempty"`
	Cwd     string `json:"cwd,omitempty"` // where it opens, relative to Root with slashes
	Add     int    `json:"add"`
	Del     int    `json:"del"`
	Missing bool   `json:"missing,omitempty"`
}

func folderRowOf(f savedFolder) folderRow {
	r := folderRow{Root: f.Path, Repo: filepath.Base(f.Path), Parent: tilde(filepath.Dir(f.Path))}
	if st, err := os.Stat(f.Path); err != nil || !st.IsDir() {
		r.Missing = true
		return r
	}
	if f.Open != "" {
		r.Cwd = relCwd(f.Path, f.Open)
	}
	r.Branch, r.Add, r.Del = uncommitted(f.Path)
	return r
}

// uncommitted gives the branch and the lines added and removed since the last commit, with one
// git status and one git diff. Outside git it gives nothing.
func uncommitted(root string) (branch string, add, del int) {
	out, err := kgit.Raw(root, "status", "--porcelain=v2", "-z", "--branch", "-uall")
	if err != nil {
		return "", 0, 0
	}
	var st kgit.Status
	kgit.ParseStatus(out, &st)
	branch = st.Branch
	if branch == "" {
		branch, _ = kgit.Run(root, "rev-parse", "--short", "HEAD")
	}
	for _, n := range kgit.NumstatOf(root, append(st.Staged, st.Changes...), "-M", "HEAD") {
		add += n.Add
		del += n.Del
	}
	for _, c := range st.Untracked {
		if !strings.HasSuffix(c.Path, "/") {
			add += kgit.CountLines(filepath.Join(root, filepath.FromSlash(c.Path)))
		}
	}
	return branch, add, del
}

func folderMessage(rows []folderRow, err error, open openRequest, k kijkState) []byte {
	msg := struct {
		Folders []folderRow `json:"folders"`
		Error   string      `json:"error,omitempty"`
		Open    openRequest `json:"open"`
		Kijk    kijkState   `json:"kijk"`
	}{Folders: rows, Open: open, Kijk: k}
	if err != nil {
		msg.Error = err.Error()
	}
	b, _ := json.Marshal(msg)
	return b
}

// folderRefresh is how often each row is refreshed. Rows take turns, one git status at a time,
// so many folders or one big repo never start a burst of git processes.
const folderRefresh = 10 * time.Second

// watch pushes the rows to the folders windows. A new or changed folder gets its row at once, so
// an open request never comes before its row.
func (l *folderList) watch(h *hub, u *updater) {
	made := map[string]folderRow{}
	from := map[string]savedFolder{} // the entry each row was made from
	turn := 0
	var due time.Time
	for {
		wait := time.Second
		if h.count(windowFolders) == 0 {
			h.forget(windowFolders)
			clear(made)
			clear(from)
		} else {
			list, open, asked, err := l.snapshot()
			var stale []savedFolder
			for _, f := range list {
				if old, ok := from[f.Path]; !ok || old != f || asked[f.Path] {
					stale = append(stale, f)
				}
			}
			now := time.Now()
			if len(stale) == 0 && len(list) > 0 && !now.Before(due) {
				stale = append(stale, list[turn%len(list)])
				turn++
				due = now.Add(folderRefresh / time.Duration(len(list)))
			}
			makeRows(stale, made, from)
			for p := range from {
				if indexOf(list, p) < 0 {
					delete(made, p)
					delete(from, p)
				}
			}
			rows := make([]folderRow, len(list))
			for i, f := range list {
				rows[i] = made[f.Path]
			}
			k := kijkState{Version: kijkVersion}
			if u != nil {
				k = u.state()
			}
			h.publish(windowFolders, folderMessage(rows, err, open, k))
			if len(list) > 0 {
				wait = min(wait, max(time.Until(due), 0))
			}
		}
		select {
		case <-l.changed:
		case <-h.wake(windowFolders):
		case <-time.After(wait):
		}
	}
}

// makeRows refreshes the rows of list, in parallel when a window first opens with many folders.
func makeRows(list []savedFolder, made map[string]folderRow, from map[string]savedFolder) {
	rows := make([]folderRow, len(list))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for i, f := range list {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			rows[i] = folderRowOf(f)
			<-sem
		}()
	}
	wg.Wait()
	for i, f := range list {
		made[f.Path], from[f.Path] = rows[i], f
	}
}
