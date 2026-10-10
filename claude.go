package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

// All knowledge of Claude Code's internal file formats lives in this file.

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

func claudeDir() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
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

// lastCwd reads the transcript backwards in growing windows, so large transcripts stay cheap.
func lastCwd(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return ""
	}
	size := st.Size()
	for n := int64(64 << 10); ; n *= 4 {
		n = min(n, size)
		buf := make([]byte, n)
		if _, err := f.ReadAt(buf, size-n); err != nil && err != io.EOF {
			return ""
		}
		if ms := cwdRe.FindAllSubmatch(buf, -1); len(ms) > 0 {
			return unquote(ms[len(ms)-1][1])
		}
		if n == size {
			return ""
		}
	}
}

// ponytail: first prompt is looked for in the first 1 MB only.
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

func unquote(b []byte) string {
	var s string
	if json.Unmarshal(append(append([]byte{'"'}, b...), '"'), &s) != nil {
		return ""
	}
	return s
}
