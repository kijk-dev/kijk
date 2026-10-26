package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
)

// openAsk is what a kijk command asks the kijk that shows it. An empty ask only asks whether a
// window is open.
type openAsk struct {
	Folders []string `json:"folders,omitempty"` // absolute; the server checks them again
	PR      *prLink  `json:"pr,omitempty"`      // the server looks for a clone of its repo
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
	NoClone bool   `json:"noClone,omitempty"` // no clone of the pull request's repo is known
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
	out, err io.Writer
}

// deliver hands the ask to the kijk that shows it and prints what happened. It opens a window
// only when none is open: kijk cannot bring an open window to the front. window is nil with
// -no-open.
func deliver(open opener, ask openAsk, window func(seq int), t terminal) int {
	rep, err := open(ask)
	if err == nil && rep.NoClone {
		err = noClone(*ask.PR)
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
	switch {
	case a.PR != nil && len(a.Folders) > 0:
		return errors.New("a pull request link takes no folder")
	case a.PR != nil && a.PR.Number <= 0:
		return errors.New("no pull request number")
	case a.List != "" && a.List != listFolders:
		return fmt.Errorf("no list %q", a.List)
	}
	return nil
}

func (d *openDesk) open(a openAsk) (openReply, error) {
	rep := openReply{Window: d.h.open() > 0}
	switch {
	case a.PR != nil:
		root := d.findClone(*a.PR)
		if root == "" {
			rep.NoClone = true
			return rep, nil
		}
		return d.openPR(root, a.PR.Number, rep)
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
func (d *openDesk) openPR(root string, n int, rep openReply) (openReply, error) {
	f, err := checkFolder(root, "")
	if err != nil {
		return rep, err
	}
	name := filepath.Base(f.Path)
	q := openRequest{Root: f.Path, PR: n}
	if d.rl.has(f.Path) {
		d.fl.refresh(f.Path)
		rep.Said = fmt.Sprintf("Opened #%d in %s.", n, name)
	} else {
		if _, err := d.fl.add([]savedFolder{f}); err != nil {
			rep.Error = err.Error()
		}
		q.Added = true
		rep.Said = fmt.Sprintf("Added %s and opened #%d.", name, n)
	}
	rep.Seq = d.rl.request(q)
	if rep.Window {
		rep.Said += " It is open in the kijk window."
	}
	return rep, nil
}

// findClone gives the main clone of l's repo among the saved folders, then the folders of the local
// agents, or "". While no window looks for agents, it scans for them once.
func (d *openDesk) findClone(l prLink) string {
	return prClone(l, d.fl.paths(), func() []string {
		if roots, ok := d.rl.agentRoots(); ok {
			return roots
		}
		return localAgentFolders()
	})
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
