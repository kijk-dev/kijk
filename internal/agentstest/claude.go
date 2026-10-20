package agentstest

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// kijk shows a Claude Code agent only while its process runs, so the agent gets the test's pid.
func init() {
	starters["claude"] = func(t *testing.T, home, cwd, name string) {
		f := filepath.Join(home, ".claude", "sessions", "1.json")
		b := SessionJSON(os.Getpid(), "s1", cwd, name, "idle", time.Now().UnixMilli())
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte(b), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// SessionJSON is a Claude Code session file, with a field kijk does not know.
func SessionJSON(pid int, id, cwd, name, status string, startedAt int64) string {
	return fmt.Sprintf(`{"pid":%d,"sessionId":%q,"cwd":%q,"name":%q,"status":%q,"startedAt":%d,"futureField":{"x":1}}`,
		pid, id, cwd, name, status, startedAt)
}
