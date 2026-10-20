package agents

import (
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kijk-dev/kijk/internal/gittest"
)

// testEnv is a machine with an empty home folder and only the environment variables in kv.
func testEnv(t *testing.T, kv ...string) *Env {
	vars := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		vars[kv[i]] = kv[i+1]
	}
	return &Env{Home: t.TempDir(), Getenv: func(k string) string { return vars[k] }, Version: "dev"}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	gittest.Write(t, path, content)
}

func deadPID(t *testing.T) int {
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return cmd.Process.Pid
}

var startTime = time.Now()

func TestRunning(t *testing.T) {
	env := testEnv(t)
	now := time.Now().UnixMilli()
	if !env.running(os.Getpid(), now) {
		t.Error("own process should be running")
	}
	if env.running(os.Getpid(), now-time.Hour.Milliseconds()-time.Since(startTime).Milliseconds()) {
		t.Error("a process that started after the session file is a reused pid")
	}
	if env.running(deadPID(t), now) {
		t.Error("dead process should not be running")
	}
}

func TestTypeList(t *testing.T) {
	names := map[string]bool{}
	for _, at := range Types {
		if !regexp.MustCompile(`^[a-z][a-z0-9]*$`).MatchString(at.Name) || names[at.Name] {
			t.Errorf("%q: want a unique name of small letters and digits, as telemetry names it <name>_agents", at.Name)
		}
		names[at.Name] = true
		if at.Label == "" || !strings.HasPrefix(at.Icon, "<svg") || at.Sessions == nil {
			t.Errorf("%s: want a label, an SVG icon and a reader", at.Name)
		}
	}
	if !slices.IsSortedFunc(Types, func(a, b AgentType) int { return strings.Compare(a.Label, b.Label) }) {
		t.Error("Types is not in label order")
	}
}
