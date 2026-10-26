package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/kijk-dev/kijk/internal/git"
)

// openAsk is what a kijk command asks the kijk that shows it. An empty ask only asks whether a
// window is open.
type openAsk struct {
	Folders []string `json:"folders,omitempty"` // absolute; the server checks them again
	PR      *prLink  `json:"pr,omitempty"`      // with no folder, the server looks for a clone of its repo
	Cwd     string   `json:"cwd,omitempty"`     // where the command ran: one place to look for the clone
	List    string   `json:"list,omitempty"`    // "folders" for kijk folders
}

// plain asks for nothing but the window.
func (a openAsk) plain() bool {
	return len(a.Folders) == 0 && a.PR == nil && a.List == ""
}

// openReply says what happened, in the sentence the terminal prints.
type openReply struct {
	Said    string `json:"said,omitempty"`
	Window  bool   `json:"window"`            // a window is open now, so the command opens none
	Seq     int    `json:"seq,omitempty"`     // the request a new window acts on
	NoClone bool   `json:"noClone,omitempty"` // no clone of the pull request's repo was found
	Error   string `json:"error,omitempty"`   // the list could not be saved; the open still happened
}

// opener is the kijk that shows what a command asks for: this process's own desk at first start,
// or the kijk already running on the port.
type opener func(openAsk) (openReply, error)

func runningOpener(port int, token string) opener {
	return func(a openAsk) (rep openReply, err error) {
		err = callRunning(port, token, "POST", "/api/open", a, &rep)
		if errors.Is(err, errTooOld) && a.plain() {
			// Right after kijk update the old kijk still runs, and a plain kijk only needs its window.
			return openReply{}, nil
		}
		return rep, err
	}
}

// windowURL is the address of a new window that acts on request seq.
func windowURL(port int, token string, seq int) string {
	u := uiURL(port, token)
	if seq > 0 {
		u += "&open=" + strconv.Itoa(seq)
	}
	return u
}

type terminal struct {
	in       io.Reader
	out, err io.Writer
}

// deliver hands the ask to the kijk that shows it and prints what happened. A repo with no clone
// is cloned here, where the terminal is. It opens a window only when none is open: kijk cannot
// bring an open window to the front. window is nil with -no-open.
func deliver(open opener, ask openAsk, window func(seq int), t terminal) int {
	rep, err := open(ask)
	if err == nil && rep.NoClone {
		fmt.Fprintln(t.out, rep.Said)
		dir, code := clone(*ask.PR, t)
		if code != 0 {
			return code
		}
		rep, err = open(openAsk{Folders: []string{dir}, PR: ask.PR})
	}
	if err != nil {
		fmt.Fprintln(t.err, "kijk:", err)
		return 1
	}
	if rep.Said != "" {
		fmt.Fprintln(t.out, rep.Said)
	}
	if rep.Error != "" {
		fmt.Fprintln(t.err, "kijk:", rep.Error)
	}
	if !rep.Window && window != nil {
		window(rep.Seq)
	}
	return 0
}

// clone clones l's repo into kijk's cache folder with gh or glab, and gives the exit code: when it
// is not 0, clone said why, or the user pressed Ctrl+C, or kijk was stopped.
func clone(l prLink, t terminal) (dir string, code int) {
	repo, tool := l.Host+"/"+l.Path, git.Tool(l.Kind)
	if _, err := exec.LookPath(tool); err != nil {
		fmt.Fprintf(t.err, "kijk: %s is not installed, so kijk cannot clone %s. Clone it, then run this command again.\n", tool, repo)
		return "", 1
	}
	cache, err := cacheDir()
	if err == nil {
		dir = cloneDir(cache, l)
		fmt.Fprintf(t.out, "Cloning it with %s into %s …\n", tool, tilde(dir))
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		err = git.Clone(ctx, l.Kind, l.server(), l.Path, dir, t.in, t.out, t.err)
		stop()
	}
	switch {
	case errors.Is(err, context.Canceled):
		return "", 130
	case err != nil:
		first, _, _ := strings.Cut(err.Error(), "\n")
		fmt.Fprintf(t.err, "kijk: cannot clone %s: %s. Clone it, then run kijk <folder>.\n", repo, first)
		return "", 1
	}
	return dir, 0
}

// openDesk is the server side of every hand-off from the command line. The route and the first
// start both call open.
type openDesk struct {
	h  *hub
	rl *rail
	fl *folderList
}

func (d *openDesk) serve(w http.ResponseWriter, r *http.Request) {
	var a openAsk
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&a); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := a.check(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	rep, err := d.open(a)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	reply(w, rep)
}

// check refuses what no kijk command asks.
func (a openAsk) check() error {
	for _, p := range a.Folders {
		if !filepath.IsAbs(p) {
			return fmt.Errorf("%s is not an absolute path", p)
		}
	}
	if a.Cwd != "" && !filepath.IsAbs(a.Cwd) {
		return fmt.Errorf("%s is not an absolute path", a.Cwd)
	}
	switch {
	case a.PR != nil && len(a.Folders) > 1:
		return errors.New("a pull request opens in one folder")
	case a.PR != nil && !a.PR.valid():
		return errors.New("not a pull request link")
	case a.List != "" && a.List != listFolders:
		return fmt.Errorf("no list %q", a.List)
	}
	return nil
}

func (d *openDesk) open(a openAsk) (openReply, error) {
	rep := openReply{Window: d.h.open() > 0}
	switch {
	case a.PR != nil && len(a.Folders) == 1: // the command cloned the repo there
		return d.openPR(a.Folders[0], a.PR.Number, false, false, rep)
	case a.PR != nil:
		root, checked, agent := d.findClone(*a.PR, a.Cwd)
		if root == "" {
			rep.NoClone = true
			rep.Said = noCloneText(*a.PR, checked)
			return rep, nil
		}
		return d.openPR(root, a.PR.Number, true, agent, rep)
	case len(a.Folders) > 0:
		fs := make([]savedFolder, len(a.Folders))
		for i, p := range a.Folders {
			f, err := checkFolder(p, "") // the list decides what the server may read
			if err != nil {
				return rep, err
			}
			fs[i] = f
		}
		added, err := d.fl.add(fs)
		if err != nil {
			rep.Error = err.Error()
		}
		last := fs[len(fs)-1]
		q := openRequest{Root: last.Path, List: listFolders, Added: added[len(added)-1]}
		if last.Open != "" {
			q.Cwd = relCwd(last.Path, last.Open)
		}
		rep.Seq = d.rl.request(q)
		rep.Said = addedText(fs, added, rep.Window)
	case a.List != "":
		rep.Seq = d.rl.request(openRequest{List: a.List})
		if rep.Window {
			rep.Said = "The kijk window shows the Folders list."
		}
	case rep.Window:
		rep.Said = "Its window is open already."
	}
	return rep, nil
}

// openPR opens pull request n in root's row, adding root to the folders list when no row has it.
// found says kijk found root itself, so the user learns where. agent says an agent works in root,
// so it has a row once kijk looks for agents.
func (d *openDesk) openPR(root string, n int, found, agent bool, rep openReply) (openReply, error) {
	f, err := checkFolder(root, "")
	if err != nil {
		return rep, err
	}
	name := filepath.Base(f.Path)
	q := openRequest{Root: f.Path, PR: n}
	has := agent || d.rl.has(f.Path)
	switch {
	case has:
		d.fl.refresh(f.Path)
		rep.Said = fmt.Sprintf("Opened #%d in %s.", n, name)
	case found:
		rep.Said = fmt.Sprintf("Found %s at %s. Added it and opened #%d.", name, tilde(f.Path), n)
	default:
		rep.Said = fmt.Sprintf("Added %s and opened #%d.", name, n)
	}
	if !has {
		if _, err := d.fl.add([]savedFolder{f}); err != nil {
			rep.Error = err.Error()
		}
		q.Added = true
	}
	rep.Seq = d.rl.request(q)
	if rep.Window {
		rep.Said += " It is open in the kijk window."
	}
	return rep, nil
}

// findClone looks for a local clone of l's repo and gives its main clone, or "", how many places
// it checked, and whether an agent works in it. While no window looks for agents, it scans for them
// once, when it needs them.
func (d *openDesk) findClone(l prLink, cwd string) (root string, checked int, agent bool) {
	agents := sync.OnceValue(func() []string {
		if roots, ok := d.rl.agentRoots(); ok {
			return roots
		}
		return localAgentFolders()
	})
	home, _ := os.UserHomeDir()
	cache, _ := cacheDir()
	root, checked = matchClone(l, prCandidates(l, d.fl.paths(), agents, cwd, home, cache))
	if root != "" && !d.rl.has(root) {
		agent = slices.Contains(agents(), root)
	}
	return root, checked, agent
}

func noCloneText(l prLink, checked int) string {
	if checked == 0 {
		return fmt.Sprintf("No clone of %s/%s on this machine.", l.Host, l.Path)
	}
	return fmt.Sprintf("No clone of %s/%s in %s on this machine.", l.Host, l.Path, plural(checked, "folder"))
}

// addedText says what kijk <folder>… did; window says the kijk window shows it.
func addedText(adds []savedFolder, added []bool, window bool) string {
	if len(adds) == 1 {
		f := adds[0]
		name := filepath.Base(f.Path)
		text := "Added " + name
		if !added[0] {
			text = name + " is already in the Folders list"
		}
		if f.Open != "" {
			text += ", opened at " + relCwd(f.Path, f.Open) + "/"
		}
		if window {
			return text + ". It is open in the kijk window."
		}
		return text + "."
	}
	n := 0
	for _, a := range added {
		if a {
			n++
		}
	}
	var text string
	switch old := len(adds) - n; {
	case n == 0:
		text = fmt.Sprintf("All %d folders are already in the Folders list.", len(adds))
	case old == 0:
		text = fmt.Sprintf("Added %s to the Folders list.", plural(n, "folder"))
	default:
		were := "were"
		if old == 1 {
			were = "was"
		}
		text = fmt.Sprintf("Added %s to the Folders list. %d %s already in it.", plural(n, "folder"), old, were)
	}
	if window {
		text += " The last one is open in the kijk window."
	}
	return text
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}
