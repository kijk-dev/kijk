package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"
)

type Row struct {
	Root         string   `json:"root"`
	Repo         string   `json:"repo"`
	Branch       string   `json:"branch"`
	Cwd          string   `json:"cwd"`   // newest session's working folder, relative to Root with slashes
	Agent        string   `json:"agent"` // which agent runs here: "claude" (later also others, such as "codex")
	Names        []string `json:"names"`
	Busy         bool     `json:"busy"`
	LastActivity int64    `json:"lastActivity"` // unix ms
}

type session struct {
	Agent, Name, Root, Branch, Cwd string
	Busy                           bool
	Last                           time.Time
}

type transcript struct {
	path   string
	mtime  time.Time
	cwd    string
	prompt string
}

type gitEntry struct {
	root, branch string
	at           time.Time
}

// scanner is used from one goroutine only.
type scanner struct {
	dir         string
	transcripts map[string]transcript
	git         map[string]gitEntry
}

func newScanner(dir string) *scanner {
	return &scanner{dir: dir, transcripts: map[string]transcript{}, git: map[string]gitEntry{}}
}

func (s *scanner) scan() ([]Row, error) {
	sessDir := filepath.Join(s.dir, "sessions")
	des, err := os.ReadDir(sessDir)
	if err != nil {
		return nil, fmt.Errorf("cannot read Claude sessions folder %s: %w", sessDir, err)
	}
	var sessions []session
	var firstErr error
	parsed := 0
	for _, de := range des {
		if de.IsDir() || filepath.Ext(de.Name()) != ".json" {
			continue
		}
		f := filepath.Join(sessDir, de.Name())
		sf, err := readSessionFile(f)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("cannot read session file %s: %w", f, err)
			}
			continue
		}
		parsed++
		if !isRunning(sf.PID, sf.StartedAt) {
			continue
		}
		sessions = append(sessions, s.session(f, sf))
	}
	if parsed == 0 && firstErr != nil {
		return nil, firstErr
	}
	return groupRows(sessions), nil
}

func (s *scanner) session(file string, sf sessionFile) session {
	// Not the transcript's mtime: Claude rewrites idle transcripts without adding messages.
	last := time.UnixMilli(sf.StatusUpdatedAt)
	switch {
	case sf.Status == "busy":
		last = time.Now()
	case sf.StatusUpdatedAt == 0: // Claude versions without the field
		last = modTime(file)
	}
	cwd := sf.Cwd
	name := sf.Name
	t := s.transcript(sf.SessionID)
	if t.cwd != "" {
		cwd = t.cwd
	}
	if name == "" {
		name = t.prompt
	}
	if name == "" {
		name = sf.SessionID[:min(8, len(sf.SessionID))]
	}
	root, branch := s.gitInfo(cwd)
	return session{Agent: "claude", Name: name, Root: root, Branch: branch, Cwd: cwd, Busy: sf.Status == "busy", Last: last}
}

func (s *scanner) transcript(id string) transcript {
	t := s.transcripts[id]
	if t.path == "" {
		if t.path = findTranscript(s.dir, id); t.path == "" {
			return t
		}
	}
	if m := modTime(t.path); !m.Equal(t.mtime) {
		t.mtime = m
		t.cwd = lastCwd(t.path)
		if t.prompt == "" {
			t.prompt = firstPrompt(t.path)
		}
		s.transcripts[id] = t
	}
	return t
}

func (s *scanner) gitInfo(cwd string) (string, string) {
	if e, ok := s.git[cwd]; ok && time.Since(e.at) < 5*time.Second {
		return e.root, e.branch
	}
	root, branch := gitInfo(cwd)
	s.git[cwd] = gitEntry{root, branch, time.Now()}
	return root, branch
}

// isRunning rejects a reused pid: that process started after the session file was written.
func isRunning(pid int, startedAt int64) bool {
	created, err := processStart(pid)
	if err != nil {
		return false
	}
	return startedAt == 0 || created <= startedAt+2000
}

func gitInfo(cwd string) (root, branch string) {
	out, err := git(cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return cwd, ""
	}
	root = filepath.FromSlash(out)
	if branch, err = git(cwd, "symbolic-ref", "--short", "-q", "HEAD"); err != nil {
		branch, _ = git(cwd, "rev-parse", "--short", "HEAD")
	}
	return root, branch
}

func groupRows(sessions []session) []Row {
	slices.SortStableFunc(sessions, func(a, b session) int { return b.Last.Compare(a.Last) })
	var rows []Row
	index := map[string]int{}
	for _, s := range sessions {
		i, ok := index[s.Root]
		if !ok {
			i = len(rows)
			index[s.Root] = i
			rows = append(rows, Row{Agent: s.Agent, Root: s.Root, Repo: filepath.Base(s.Root), Branch: s.Branch, Cwd: relCwd(s.Root, s.Cwd), LastActivity: s.Last.UnixMilli()})
		}
		rows[i].Names = append(rows[i].Names, s.Name)
		rows[i].Busy = rows[i].Busy || s.Busy
	}
	return rows
}

func modTime(path string) time.Time {
	st, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return st.ModTime()
}

func relCwd(root, cwd string) string {
	if real, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = real
	}
	r, err := filepath.Rel(root, cwd)
	if err != nil || r == "." || !inside(root, cwd) {
		return ""
	}
	return filepath.ToSlash(r)
}
