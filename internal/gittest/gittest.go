// Package gittest helps tests build files and git repositories in t.TempDir(). Only tests import it.
package gittest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Isolate hides the user's git config (signing, hooks) from the test repos.
func Isolate(t *testing.T) {
	empty := filepath.Join(t.TempDir(), "gitconfig")
	os.WriteFile(empty, nil, 0o644)
	t.Setenv("GIT_CONFIG_GLOBAL", empty)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, k := range []string{"GIT_AUTHOR_NAME", "GIT_COMMITTER_NAME"} {
		t.Setenv(k, "Test")
	}
	for _, k := range []string{"GIT_AUTHOR_EMAIL", "GIT_COMMITTER_EMAIL"} {
		t.Setenv(k, "test@example.com")
	}
}

// Repo makes an empty repository on branch main.
func Repo(t *testing.T) string {
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", "-b", "main", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	return dir
}

// Run runs git in dir and fails the test when git fails.
func Run(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// Write writes a file, and the folders above it.
func Write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Logins has gh and glab logged in to these hosts only.
func Logins(t *testing.T, gh, glab []string) {
	t.Helper()
	ghDir, glabDir := t.TempDir(), t.TempDir()
	t.Setenv("GH_CONFIG_DIR", ghDir)
	t.Setenv("GLAB_CONFIG_DIR", glabDir)
	var ghHosts, glabHosts strings.Builder
	for _, h := range gh {
		fmt.Fprintf(&ghHosts, "%s:\n    user: sanne\n", h)
	}
	glabHosts.WriteString("hosts:\n")
	for _, h := range glab {
		fmt.Fprintf(&glabHosts, "    %s:\n        token: x\n", h)
	}
	Write(t, filepath.Join(ghDir, "hosts.yml"), ghHosts.String())
	Write(t, filepath.Join(glabDir, "config.yml"), glabHosts.String())
}
