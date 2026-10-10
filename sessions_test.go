package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func sessionJSON(pid int, id, cwd, name, status string, startedAt int64) string {
	return fmt.Sprintf(`{"pid":%d,"sessionId":%q,"cwd":%q,"name":%q,"status":%q,"startedAt":%d,"futureField":{"x":1}}`,
		pid, id, cwd, name, status, startedAt)
}

func deadPID(t *testing.T) int {
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return cmd.Process.Pid
}

func samePath(t *testing.T, got, want string) {
	t.Helper()
	g, _ := filepath.EvalSymlinks(got)
	w, _ := filepath.EvalSymlinks(want)
	if g != w {
		t.Fatalf("path = %q, want %q", got, want)
	}
}

func gitRepo(t *testing.T) string {
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", "-b", "main", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	return dir
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

func TestIsRunning(t *testing.T) {
	now := time.Now().UnixMilli()
	if !isRunning(os.Getpid(), now) {
		t.Error("own process should be running")
	}
	if isRunning(os.Getpid(), now-time.Hour.Milliseconds()-time.Since(startTime).Milliseconds()) {
		t.Error("a process that started after the session file is a reused pid")
	}
	if isRunning(deadPID(t), now) {
		t.Error("dead process should not be running")
	}
}

var startTime = time.Now()

func TestGroupRows(t *testing.T) {
	now := time.Now()
	rows := groupRows([]session{
		{Name: "old", Root: "/a", Last: now.Add(-time.Hour)},
		{Name: "docs", Root: "/b", Last: now.Add(-time.Minute)},
		{Name: "auth-fix", Root: "/b", Busy: true, Last: now.Add(-2 * time.Minute)},
	})
	if len(rows) != 2 {
		t.Fatalf("got %d rows", len(rows))
	}
	if rows[0].Root != "/b" || !rows[0].Busy || strings.Join(rows[0].Names, ",") != "docs,auth-fix" {
		t.Fatalf("first row = %+v", rows[0])
	}
	if rows[1].Root != "/a" || rows[1].Busy {
		t.Fatalf("second row = %+v", rows[1])
	}
}

func TestGitInfo(t *testing.T) {
	plain := t.TempDir()
	if root, branch := gitInfo(plain); root != plain || branch != "" {
		t.Fatalf("outside git: %q %q", root, branch)
	}
	repo := gitRepo(t)
	sub := filepath.Join(repo, "services", "api")
	os.MkdirAll(sub, 0o755)
	root, branch := gitInfo(sub)
	samePath(t, root, repo)
	if branch != "main" {
		t.Fatalf("branch = %q", branch)
	}
}

func TestScan(t *testing.T) {
	dir := t.TempDir()
	repo := gitRepo(t)
	os.MkdirAll(filepath.Join(repo, "src"), 0o755)
	now := time.Now().UnixMilli()
	write(t, filepath.Join(dir, "sessions", "1.json"), sessionJSON(os.Getpid(), "s1", "/started/here", "", "busy", now))
	write(t, filepath.Join(dir, "projects", "enc", "s1.jsonl"),
		`{"type":"last-prompt","lastPrompt":"review the code"}`+"\n"+fmt.Sprintf(`{"cwd":%q}`, filepath.Join(repo, "src")))
	write(t, filepath.Join(dir, "sessions", "2.json"), sessionJSON(deadPID(t), "s2", repo, "stale", "idle", now))
	write(t, filepath.Join(dir, "sessions", "3.json"), "not json")

	rows, err := newScanner(dir).scan()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows: %+v", len(rows), rows)
	}
	samePath(t, rows[0].Root, repo)
	if rows[0].Names[0] != "review the code" || !rows[0].Busy || rows[0].Branch != "main" {
		t.Fatalf("row = %+v", rows[0])
	}
}

func TestScanErrors(t *testing.T) {
	dir := t.TempDir()
	if _, err := newScanner(dir).scan(); err == nil {
		t.Fatal("want error for a missing sessions folder")
	}
	write(t, filepath.Join(dir, "sessions", "9.json"), "{broken")
	_, err := newScanner(dir).scan()
	if err == nil || !strings.Contains(err.Error(), "9.json") {
		t.Fatalf("want error naming the file, got %v", err)
	}
	os.WriteFile(filepath.Join(dir, "sessions", "9.json"), []byte(sessionJSON(deadPID(t), "x", "/", "", "", 1)), 0o644)
	if rows, err := newScanner(dir).scan(); err != nil || len(rows) != 0 {
		t.Fatalf("got %v, %v", rows, err)
	}
}

func TestClaudeDir(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "/custom")
	if claudeDir() != "/custom" {
		t.Fatal(claudeDir())
	}
}

func TestScanIdleAge(t *testing.T) {
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

	rows, err := newScanner(dir).scan()
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows = %+v, err = %v", rows, err)
	}
	ages := map[string]time.Duration{}
	for _, r := range rows {
		ages[r.Names[0]] = now.Sub(time.UnixMilli(r.LastActivity)).Round(time.Hour)
	}
	if ages["a"] != 48*time.Hour || ages["b"] != 3*time.Hour {
		t.Fatalf("ages = %v, want a 48h (statusUpdatedAt) and b 3h (session file)", ages)
	}
}
