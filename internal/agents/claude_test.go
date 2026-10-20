package agents

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sessionJSON(pid int, id, cwd, name, status string, startedAt int64) string {
	return fmt.Sprintf(`{"pid":%d,"sessionId":%q,"cwd":%q,"name":%q,"status":%q,"startedAt":%d,"futureField":{"x":1}}`,
		pid, id, cwd, name, status, startedAt)
}

// claudeEnv is a machine with Claude Code's folder at dir and no Codex.
func claudeEnv(t *testing.T, dir string) *Env {
	return testEnv(t, "CLAUDE_CONFIG_DIR", dir)
}

func TestReadSessionFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "1.json")
	write(t, p, sessionJSON(42, "abc", "/x", "fix", "busy", 5))
	s, err := readSessionFile(p)
	if err != nil || s.PID != 42 || s.SessionID != "abc" || s.Status != "busy" || s.StartedAt != 5 {
		t.Fatalf("got %+v, %v", s, err)
	}
	write(t, p, `{"status":"idle"}`)
	if _, err := readSessionFile(p); err == nil {
		t.Fatal("want error for a file without pid")
	}
}

func TestLastCwd(t *testing.T) {
	p := filepath.Join(t.TempDir(), "t.jsonl")
	write(t, p, `{"type":"user","cwd":"/repo"}`+"\n"+
		`{"type":"user","cwd":"/tmp/wt-feature"}`+"\n"+
		`{"type":"user","message":{"content":"{\"cwd\":\"/evil\"}"}}`+"\n")
	if got := lastCwd(p); got != "/tmp/wt-feature" {
		t.Fatalf("got %q", got)
	}

	// cwd only near the start of a file much larger than the first read window
	write(t, p, `{"cwd":"C:\\Users\\me\\repo"}`+"\n"+strings.Repeat(`{"type":"x","data":"yyyyyyyy"}`+"\n", 20000))
	if got := lastCwd(p); got != `C:\Users\me\repo` {
		t.Fatalf("got %q", got)
	}
}

func TestFirstPrompt(t *testing.T) {
	p := filepath.Join(t.TempDir(), "t.jsonl")
	write(t, p, `{"type":"last-prompt","leafUuid":"a"}`+"\n"+
		`{"type":"last-prompt","lastPrompt":"fix the \"login\" bug\u2026"}`+"\n"+
		`{"type":"last-prompt","lastPrompt":"later prompt"}`+"\n")
	if got := firstPrompt(p); got != `fix the "login" bug…` {
		t.Fatalf("got %q", got)
	}
}

func TestClaudeSessions(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UnixMilli()
	write(t, filepath.Join(dir, "sessions", "1.json"), sessionJSON(os.Getpid(), "s1", "/started/here", "", "busy", now))
	write(t, filepath.Join(dir, "projects", "enc", "s1.jsonl"),
		`{"type":"last-prompt","lastPrompt":"review the code"}`+"\n"+`{"cwd":"/repo/src"}`)
	write(t, filepath.Join(dir, "sessions", "2.json"), sessionJSON(deadPID(t), "s2", "/repo", "stale", "idle", now))
	write(t, filepath.Join(dir, "sessions", "3.json"), "not json")

	sessions, err := claudeSessions(claudeEnv(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions: %+v", len(sessions), sessions)
	}
	if s := sessions[0]; s.Name != "review the code" || !s.Busy || s.Cwd != "/repo/src" || time.Since(s.Last) > time.Second {
		t.Fatalf("session = %+v", s)
	}
}

func TestClaudeErrors(t *testing.T) {
	dir := t.TempDir()
	if _, err := claudeSessions(claudeEnv(t, dir)); err == nil {
		t.Fatal("want error for a missing sessions folder")
	}
	write(t, filepath.Join(dir, "sessions", "9.json"), "{broken")
	_, err := claudeSessions(claudeEnv(t, dir))
	if err == nil || !strings.Contains(err.Error(), "9.json") {
		t.Fatalf("want error naming the file, got %v", err)
	}
	os.WriteFile(filepath.Join(dir, "sessions", "9.json"), []byte(sessionJSON(deadPID(t), "x", "/", "", "", 1)), 0o644)
	if sessions, err := claudeSessions(claudeEnv(t, dir)); err != nil || len(sessions) != 0 {
		t.Fatalf("got %v, %v", sessions, err)
	}
}

func TestClaudeFolder(t *testing.T) {
	env := testEnv(t)
	if sessions, err := claudeSessions(env); err != nil || len(sessions) != 0 || env.Installed() {
		t.Fatalf("no Claude folder: got %v, %v; want nothing, no error, and Claude Code not installed", sessions, err)
	}
	write(t, filepath.Join(env.Home, ".claude", "sessions", "1.json"), sessionJSON(os.Getpid(), "s1", "/a", "home", "idle", time.Now().UnixMilli()))
	if sessions, err := claudeSessions(env); err != nil || len(sessions) != 1 || sessions[0].Name != "home" || !env.Installed() {
		t.Fatalf("~/.claude: got %v, %v", sessions, err)
	}
	dir := t.TempDir()
	write(t, filepath.Join(dir, "sessions", "1.json"), sessionJSON(os.Getpid(), "s1", "/a", "custom", "idle", time.Now().UnixMilli()))
	if sessions, err := claudeSessions(claudeEnv(t, dir)); err != nil || len(sessions) != 1 || sessions[0].Name != "custom" {
		t.Fatalf("CLAUDE_CONFIG_DIR: got %v, %v", sessions, err)
	}
}

func TestClaudeIdleAge(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	idleSince := now.Add(-48 * time.Hour).UnixMilli()
	withField := strings.Replace(sessionJSON(os.Getpid(), "s1", t.TempDir(), "a", "idle", now.UnixMilli()),
		`"futureField"`, fmt.Sprintf(`"statusUpdatedAt":%d,"futureField"`, idleSince), 1)
	write(t, filepath.Join(dir, "sessions", "1.json"), withField)
	write(t, filepath.Join(dir, "projects", "enc", "s1.jsonl"), `{"type":"mode"}`+"\n") // fresh mtime, no new messages
	old := filepath.Join(dir, "sessions", "2.json")
	write(t, old, sessionJSON(os.Getpid(), "s2", t.TempDir(), "b", "idle", now.UnixMilli()))
	os.Chtimes(old, now.Add(-3*time.Hour), now.Add(-3*time.Hour))

	sessions, err := claudeSessions(claudeEnv(t, dir))
	if err != nil || len(sessions) != 2 {
		t.Fatalf("sessions = %+v, err = %v", sessions, err)
	}
	ages := map[string]time.Duration{}
	for _, s := range sessions {
		ages[s.Name] = now.Sub(s.Last).Round(time.Hour)
	}
	if ages["a"] != 48*time.Hour || ages["b"] != 3*time.Hour {
		t.Fatalf("ages = %v, want a 48h (statusUpdatedAt) and b 3h (session file)", ages)
	}
}
