package agents

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// All knowledge of Claude Code's internal file formats lives in this file.

// The Claude mark, from Simple Icons (CC0).
//
//go:embed claude.svg
var claudeIcon string

func init() {
	add(AgentType{
		Name:     "claude",
		Label:    "Claude Code",
		Icon:     strings.TrimSpace(claudeIcon),
		Color:    "#d97757", // Claude's brand orange
		Sessions: claudeSessions,
	})
}

type sessionFile struct {
	PID       int    `json:"pid"`
	SessionID string `json:"sessionId"`
	Cwd       string `json:"cwd"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	StartedAt int64  `json:"startedAt"` // unix ms
	// StatusUpdatedAt is when status last changed, in unix ms: for an idle agent, when it went idle.
	StatusUpdatedAt int64 `json:"statusUpdatedAt"`
}

type transcript struct {
	path   string
	mtime  time.Time
	cwd    string
	prompt string
}

// claudeState keeps each session's transcript between scans.
type claudeState struct {
	transcripts map[string]transcript
}

func claudeSessions(env *Env) ([]Session, error) {
	dir, ok := env.folder("CLAUDE_CONFIG_DIR", ".claude")
	if !ok {
		return nil, nil
	}
	st := state(env, "claude", func() *claudeState { return &claudeState{transcripts: map[string]transcript{}} })
	sessDir := filepath.Join(dir, "sessions")
	des, err := os.ReadDir(sessDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil // Claude Code has written no session file here yet
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read Claude sessions folder %s: %w", sessDir, err)
	}
	var sessions []Session
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
		if !env.running(sf.PID, sf.StartedAt) {
			continue
		}
		sessions = append(sessions, st.session(dir, f, sf))
	}
	if parsed == 0 && firstErr != nil {
		return nil, firstErr
	}
	return sessions, nil
}

func (st *claudeState) session(dir, file string, sf sessionFile) Session {
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
	t := st.transcript(dir, sf.SessionID)
	if t.cwd != "" {
		cwd = t.cwd
	}
	if name == "" {
		name = t.prompt
	}
	if name == "" {
		name = sf.SessionID[:min(8, len(sf.SessionID))]
	}
	return Session{Name: name, Cwd: cwd, Busy: sf.Status == "busy", Last: last}
}

func (st *claudeState) transcript(dir, id string) transcript {
	t := st.transcripts[id]
	if t.path == "" {
		if t.path = findTranscript(dir, id); t.path == "" {
			return t
		}
	}
	if m := modTime(t.path); !m.Equal(t.mtime) {
		t.mtime = m
		t.cwd = lastCwd(t.path)
		if t.prompt == "" {
			t.prompt = firstPrompt(t.path)
		}
		st.transcripts[id] = t
	}
	return t
}

func readSessionFile(path string) (sessionFile, error) {
	var s sessionFile
	b, err := os.ReadFile(path)
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return s, err
	}
	if s.PID <= 0 || s.SessionID == "" {
		return s, errors.New("missing pid or sessionId")
	}
	return s, nil
}

func findTranscript(dir, sessionID string) string {
	m, _ := filepath.Glob(filepath.Join(dir, "projects", "*", sessionID+".jsonl"))
	if len(m) == 0 {
		return ""
	}
	return m[0]
}

// A key inside an escaped string (tool output quoting JSON) reads \"cwd\":\" and does not match.
var (
	cwdRe    = regexp.MustCompile(`"cwd":"((?:[^"\\]|\\.)*)"`)
	promptRe = regexp.MustCompile(`"lastPrompt":"((?:[^"\\]|\\.)*)"`)
)

func lastCwd(path string) string {
	return unquote(lastMatch(path, cwdRe))
}

func firstPrompt(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	buf, _ := io.ReadAll(io.LimitReader(f, 1<<20))
	if m := promptRe.FindSubmatch(buf); m != nil {
		return unquote(m[1])
	}
	return ""
}
