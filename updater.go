package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// kijkState is what the status bar shows, sent with every message on /api/events.
type kijkState struct {
	Version    string `json:"version"`
	Latest     string `json:"latest,omitempty"`
	Installed  string `json:"installed,omitempty"`
	Busy       string `json:"busy,omitempty"` // "updating" or "restarting"
	Error      string `json:"error,omitempty"`
	CanRestart bool   `json:"canRestart"`
}

type updater struct {
	mu      sync.Mutex
	version string
	s       kijkState
	exe     string
	size    int64
	mod     time.Time
	printed string
	out     io.Writer
	restart func() error
	changed chan struct{}
}

func newUpdater(version, exe string, out io.Writer) *updater {
	u := &updater{version: version, s: kijkState{Version: version, CanRestart: canRestart}, exe: exe, out: out, changed: make(chan struct{}, 1)}
	if st, err := os.Stat(exe); err == nil {
		u.size, u.mod = st.Size(), st.ModTime()
	}
	return u
}

func (u *updater) state() kijkState {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.s
}

func (u *updater) set(f func(*kijkState)) {
	u.mu.Lock()
	before := u.s
	f(&u.s)
	after := u.s
	u.mu.Unlock()
	if after != before {
		u.notify()
	}
}

func (u *updater) notify() {
	select {
	case u.changed <- struct{}{}:
	default:
	}
}

// run checks for a new release a few seconds after start and then hourly (the shared check file
// limits GitHub to once a day), and watches the binary on disk every 5 seconds.
func (u *updater) run(check bool) {
	time.Sleep(5 * time.Second)
	var lastCheck time.Time
	for {
		if check && time.Since(lastCheck) >= time.Hour {
			lastCheck = time.Now()
			if v, err := latestKnown(lastCheck); err == nil {
				u.foundLatest(v)
			}
		}
		u.watchBinary()
		time.Sleep(5 * time.Second)
	}
}

func (u *updater) foundLatest(v string) {
	if !newer(v, u.version) {
		return
	}
	u.set(func(s *kijkState) { s.Latest = v })
	u.mu.Lock()
	first := u.printed != v
	u.printed = v
	u.mu.Unlock()
	if first {
		fmt.Fprintf(u.out, "kijk %s is out. Run kijk update to install it.\n", v)
	}
}

// watchBinary notices when the file kijk started from is replaced by a newer kijk, however that
// happened: the Update button, kijk update, an install script or go install.
func (u *updater) watchBinary() {
	st, err := os.Stat(u.exe)
	if err != nil {
		return
	}
	u.mu.Lock()
	same := st.Size() == u.size && st.ModTime().Equal(u.mod)
	u.size, u.mod = st.Size(), st.ModTime()
	u.mu.Unlock()
	if same {
		return
	}
	if v := binaryVersion(u.exe); newer(v, u.version) {
		u.set(func(s *kijkState) { s.Installed, s.Error = v, "" }) // an earlier failed attempt no longer matters
	}
}

// startUpdate runs the update in the background. It returns false when an action already runs.
func (u *updater) startUpdate() bool {
	if !u.begin("updating") {
		return false
	}
	go func() {
		_, err := installLatest(u.version, u.exe)
		if err == nil {
			u.watchBinary() // before Busy clears, so the bar goes straight from "Updating" to "installed"
		}
		u.set(func(s *kijkState) {
			s.Busy = ""
			if err != nil {
				s.Error = err.Error()
			}
		})
	}()
	return true
}

// startRestart replaces this process with the installed newer kijk. It returns false when there is
// nothing to restart into or an action already runs.
func (u *updater) startRestart() bool {
	if st := u.state(); !st.CanRestart || st.Installed == "" || u.restart == nil || !u.begin("restarting") {
		return false
	}
	go func() {
		time.Sleep(300 * time.Millisecond) // let the windows show "Restarting…" first
		err := u.restart()
		u.set(func(s *kijkState) { s.Busy, s.Error = "", "restart failed: "+err.Error() })
	}()
	return true
}

func (u *updater) begin(busy string) bool {
	u.mu.Lock()
	if u.s.Busy != "" {
		u.mu.Unlock()
		return false
	}
	u.s.Busy, u.s.Error = busy, ""
	u.mu.Unlock()
	u.notify()
	return true
}

func updateCheckOn(flagOff bool) bool {
	return !flagOff && os.Getenv("KIJK_NO_UPDATE_CHECK") == ""
}

type checkFile struct {
	Checked time.Time `json:"checked"`
	Latest  string    `json:"latest"`
}

func checkPath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "kijk", "update-check.json"), nil
}

// latestKnown gives the newest release, asking GitHub only when no kijk of this user has asked in
// the last 24 hours.
func latestKnown(now time.Time) (string, error) {
	p, err := checkPath()
	if err != nil {
		return "", err
	}
	var c checkFile
	if b, err := os.ReadFile(p); err == nil && json.Unmarshal(b, &c) == nil && isRelease(c.Latest) {
		if age := now.Sub(c.Checked); age >= 0 && age < 24*time.Hour {
			return c.Latest, nil
		}
	}
	v, err := latestVersion()
	if err != nil {
		return "", err
	}
	b, _ := json.Marshal(checkFile{now, v})
	if os.MkdirAll(filepath.Dir(p), 0o700) == nil {
		tmp := p + ".tmp"
		if os.WriteFile(tmp, b, 0o600) == nil {
			os.Rename(tmp, p)
		}
	}
	return v, nil
}
