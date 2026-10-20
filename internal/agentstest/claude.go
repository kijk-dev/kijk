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
		b := fmt.Sprintf(`{"pid":%d,"sessionId":"s1","cwd":%q,"name":%q,"status":"idle","startedAt":%d}`, os.Getpid(), cwd, name, time.Now().UnixMilli())
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte(b), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
