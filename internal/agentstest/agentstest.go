// Package agentstest has test helpers for the agent types of internal/agents.
package agentstest

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// starters start an agent of a type on a test machine. A type adds its own when a copy of its
// test folder is not enough to look running, as when kijk checks a pid or asks a server.
var starters = map[string]func(t *testing.T, home, cwd, name string){}

// StartAgent runs an agent of the agent type typ, named name and working in cwd, on the machine
// with the home folder home. A type without a starter gets the agents of its test folder instead.
func StartAgent(t *testing.T, home, typ, cwd, name string) {
	t.Helper()
	if start := starters[typ]; start != nil {
		start(t, home, cwd, name)
		return
	}
	CopyDir(t, filepath.Join(testdata(), typ, "home"), home)
}

// CopyDir copies the files in from, if there is such a folder, into to.
func CopyDir(t *testing.T, from, to string) {
	t.Helper()
	err := filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, p)
		if err := os.MkdirAll(filepath.Dir(filepath.Join(to, rel)), 0o755); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(to, rel), b, 0o644)
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

// testdata is internal/agents/testdata, found from the folder a test runs in.
func testdata() string {
	dir, err := os.Getwd()
	for err == nil {
		if _, e := os.Stat(filepath.Join(dir, "go.mod")); e == nil {
			return filepath.Join(dir, "internal", "agents", "testdata")
		}
		if dir == filepath.Dir(dir) {
			err = fmt.Errorf("no go.mod above the test's folder")
		}
		dir = filepath.Dir(dir)
	}
	panic(err)
}
