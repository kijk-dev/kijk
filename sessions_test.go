package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kijk-dev/kijk/internal/agents"
	"github.com/kijk-dev/kijk/internal/agentstest"
	"github.com/kijk-dev/kijk/internal/gittest"
)

// The tests and the journeys call the gittest helpers by these names.
func write(t *testing.T, path, content string) {
	t.Helper()
	gittest.Write(t, path, content)
}

func gitRepo(t *testing.T) string { return gittest.Repo(t) }

func isolatedGit(t *testing.T) { gittest.Isolate(t) }

func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	gittest.Run(t, dir, args...)
}

// testEnv is a machine with an empty home folder and only the environment variables in kv.
func testEnv(t *testing.T, kv ...string) *agents.Env {
	vars := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		vars[kv[i]] = kv[i+1]
	}
	return &agents.Env{Home: t.TempDir(), Getenv: func(k string) string { return vars[k] }, Version: "dev"}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func samePath(t *testing.T, got, want string) {
	t.Helper()
	g, _ := filepath.EvalSymlinks(got)
	w, _ := filepath.EvalSymlinks(want)
	if g != w {
		t.Fatalf("path = %q, want %q", got, want)
	}
}

func TestGroupRows(t *testing.T) {
	now := time.Now()
	rows := groupRows([]session{
		{Agent: "claude", Name: "old", Root: "/a", Last: now.Add(-time.Hour)},
		{Agent: "claude", Name: "docs", Root: "/b", Last: now.Add(-time.Minute)},
		{Agent: "codex", Name: "auth-fix", Root: "/b", Busy: true, Last: now.Add(-2 * time.Minute)},
	})
	if len(rows) != 2 {
		t.Fatalf("got %d rows", len(rows))
	}
	if rows[0].Root != "/b" || !rows[0].Busy || strings.Join(rows[0].Names, ",") != "docs,auth-fix" ||
		!slices.Equal(rows[0].Agents, []RowAgent{{"claude", false}, {"codex", true}}) {
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
	claude := t.TempDir()
	repo := gitRepo(t)
	os.MkdirAll(filepath.Join(repo, "src"), 0o755)
	write(t, filepath.Join(claude, "sessions", "1.json"), agentstest.SessionJSON(os.Getpid(), "s1", filepath.Join(repo, "src"), "review the code", "busy", time.Now().UnixMilli()))
	rows, errs, _ := newScanner(agents.Types, testEnv(t, "CLAUDE_CONFIG_DIR", claude)).scan()
	if errs != nil {
		t.Fatal(errs)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows: %+v", len(rows), rows)
	}
	samePath(t, rows[0].Root, repo)
	if rows[0].Names[0] != "review the code" || !rows[0].Busy || rows[0].Branch != "main" || rows[0].Cwd != "src" ||
		!slices.Equal(rows[0].Agents, []RowAgent{{"claude", true}}) {
		t.Fatalf("row = %+v", rows[0])
	}
}

func TestScanClaudeAndCodexInOneFolder(t *testing.T) {
	claude := t.TempDir()
	repo := gitRepo(t)
	write(t, filepath.Join(claude, "sessions", "1.json"),
		strings.Replace(agentstest.SessionJSON(os.Getpid(), "s1", repo, "auth-fix", "idle", time.Now().UnixMilli()),
			`"futureField"`, fmt.Sprintf(`"statusUpdatedAt":%d,"futureField"`, time.Now().Add(-time.Hour).UnixMilli()), 1))
	codexHome, f := agentstest.CodexHome(t)
	f.Set(agentstest.Thread("t1", repo, "active", "name", "fix the login bug"))
	rows, errs, _ := newScanner(agents.Types, testEnv(t, "CLAUDE_CONFIG_DIR", claude, "CODEX_HOME", codexHome)).scan()
	if errs != nil || len(rows) != 1 {
		t.Fatalf("rows = %+v, errs = %v", rows, errs)
	}
	r := rows[0]
	if !slices.Equal(r.Names, []string{"fix the login bug", "auth-fix"}) || !slices.Equal(r.Agents, []RowAgent{{"codex", true}, {"claude", false}}) || !r.Busy {
		t.Fatalf("row = %+v, want the busy Codex agent first", r)
	}
}

// fakeType is an agent type for scanner tests, with the sessions and error its reader gives.
func fakeType(name string, err error, sessions ...agents.Session) agents.AgentType {
	return agents.AgentType{Name: name, Label: name, Sessions: func(*agents.Env) ([]agents.Session, error) { return sessions, err }}
}

func TestScanFailingTypeHidesOnlyItsAgents(t *testing.T) {
	now := time.Now()
	types := []agents.AgentType{
		fakeType("one", nil, agents.Session{Name: "a", Cwd: t.TempDir(), Last: now}),
		fakeType("two", errors.New("unreadable")),
		fakeType("three", nil, agents.Session{Name: "b", Cwd: t.TempDir(), Last: now}),
		fakeType("four", errors.New("gone")),
	}
	rows, errs, _ := newScanner(types, testEnv(t)).scan()
	if len(rows) != 2 || rows[0].Agents[0].Type != "one" || rows[1].Agents[0].Type != "three" {
		t.Fatalf("rows = %+v, want the agents of one and three", rows)
	}
	if !slices.Equal(errs, []typeError{{"two", "unreadable"}, {"four", "gone"}}) {
		t.Fatalf("errs = %v", errs)
	}
}

func TestScanBrokenCodexKeepsClaude(t *testing.T) {
	claude := t.TempDir()
	write(t, filepath.Join(claude, "sessions", "1.json"), agentstest.SessionJSON(os.Getpid(), "s1", t.TempDir(), "docs", "idle", time.Now().UnixMilli()))
	codexHome, f := agentstest.CodexHome(t)
	f.Reply(func(c *agentstest.Conn, id int, _ string) { c.Send(0x1, []byte("{oops")) })
	rows, errs, _ := newScanner(agents.Types, testEnv(t, "CLAUDE_CONFIG_DIR", claude, "CODEX_HOME", codexHome)).scan()
	if len(errs) != 1 || errs[0].Type != "codex" || len(rows) != 1 || rows[0].Names[0] != "docs" {
		t.Fatalf("rows = %+v, errs = %v", rows, errs)
	}
}

func TestScanBrokenClaudeKeepsCodex(t *testing.T) {
	claude := t.TempDir()
	write(t, filepath.Join(claude, "sessions", "9.json"), "{broken")
	codexHome, f := agentstest.CodexHome(t)
	f.Set(agentstest.Thread("t1", t.TempDir(), "idle", "name", "fix the login bug"))
	rows, errs, _ := newScanner(agents.Types, testEnv(t, "CLAUDE_CONFIG_DIR", claude, "CODEX_HOME", codexHome)).scan()
	if len(errs) != 1 || errs[0].Type != "claude" || !strings.Contains(errs[0].Error, "9.json") {
		t.Fatalf("errs = %v, want the Claude Code error naming the file", errs)
	}
	if len(rows) != 1 || rows[0].Names[0] != "fix the login bug" {
		t.Fatalf("rows = %+v, want the Codex agent", rows)
	}
}

// A missing tool folder is no error, and the scan says whether any tool is installed.
func TestScanInstalled(t *testing.T) {
	claude := t.TempDir()
	os.Mkdir(filepath.Join(claude, "sessions"), 0o755)
	missing := filepath.Join(t.TempDir(), "missing")
	for _, c := range []struct {
		name          string
		claude, codex string
		installed     bool
	}{
		{"Codex only", missing, agentstest.ShortTempDir(t), true},
		{"Claude Code only", claude, missing, true},
		{"Claude Code without sessions, and Codex", t.TempDir(), agentstest.ShortTempDir(t), true},
		{"neither", missing, missing, false},
	} {
		rows, errs, installed := newScanner(agents.Types, testEnv(t, "CLAUDE_CONFIG_DIR", c.claude, "CODEX_HOME", c.codex)).scan()
		if len(rows) != 0 || errs != nil || installed != c.installed {
			t.Errorf("%s: got %v, %v, installed %v", c.name, rows, errs, installed)
		}
	}
}

func TestRelCwd(t *testing.T) {
	root := t.TempDir()
	real, _ := filepath.EvalSymlinks(root)
	os.MkdirAll(filepath.Join(root, "services", "api"), 0o755)
	if got := relCwd(real, filepath.Join(root, "services", "api")); got != "services/api" {
		t.Fatalf("got %q", got)
	}
	if got := relCwd(real, filepath.Dir(root)); got != "" {
		t.Fatalf("outside: %q", got)
	}
}
