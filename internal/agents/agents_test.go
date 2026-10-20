package agents

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kijk-dev/kijk/internal/agentstest"
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

func TestFolder(t *testing.T) {
	env := testEnv(t, "TOOL_HOME", "/custom")
	if d, ok := env.folder("TOOL_HOME", ".tool"); d != "/custom" || ok {
		t.Fatalf("got %q, %v; want the folder of the variable, not there", d, ok)
	}
	d, ok := env.folder("OTHER_HOME", ".other")
	if d != filepath.Join(env.Home, ".other") || ok || env.Installed() {
		t.Fatalf("got %q, %v; want the folder in the home folder, not there", d, ok)
	}
	os.Mkdir(d, 0o755)
	if _, ok := env.folder("OTHER_HOME", ".other"); !ok || !env.Installed() {
		t.Fatal("a tool folder that is there is an installed tool")
	}
	os.Remove(d)
	if env.folder("OTHER_HOME", ".other"); env.Installed() {
		t.Fatal("a tool folder removed since is no installed tool")
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

// live makes a copy of a test folder look like a machine where the agents run now, which a
// recording cannot: such as this test's pid in a session file. A type that needs it adds its own.
var live = map[string]func(t *testing.T, home string){}

// Every type reads testdata/<name>/home, a home folder with its tool's folder copied from a real
// run, and finds the agents of testdata/<name>/want.json.
func TestTypesReadTheirTestFolders(t *testing.T) {
	for _, at := range Types {
		t.Run(at.Name, func(t *testing.T) {
			dir := filepath.Join("testdata", at.Name)
			b, err := os.ReadFile(filepath.Join(dir, "want.json"))
			if err != nil {
				t.Fatalf("%s has no test folder: copy its tool's folder from a real run into %s and its agents into %s: %v",
					at.Label, filepath.Join(dir, "home"), filepath.Join(dir, "want.json"), err)
			}
			var want []struct {
				Name, Cwd string
				Busy      bool
				Last      string // RFC 3339, or "now" for an agent whose last activity is now
			}
			if err := json.Unmarshal(b, &want); err != nil {
				t.Fatal(err)
			}
			home := agentstest.ShortTempDir(t) // the Codex socket is in it
			agentstest.CopyDir(t, filepath.Join(dir, "home"), home)
			if f := live[at.Name]; f != nil {
				f(t, home)
			}
			env := &Env{Home: home, Getenv: func(string) string { return "" }, Version: "dev"}
			got, err := at.Sessions(env)
			if err != nil {
				t.Fatal(err)
			}
			if !env.Installed() {
				t.Error("the reader found no tool folder in the test folder")
			}
			if len(got) != len(want) {
				t.Fatalf("got %+v, want %+v", got, want)
			}
			for i, w := range want {
				g := got[i]
				last := g.Last.UTC().Format(time.RFC3339)
				if w.Last == "now" && time.Since(g.Last) < time.Minute {
					last = "now"
				}
				if g.Name != w.Name || g.Cwd != w.Cwd || g.Busy != w.Busy || last != w.Last {
					t.Errorf("agent %d = %+v, last %s; want %+v", i, g, last, w)
				}
			}
		})
	}
	dirs, _ := os.ReadDir("testdata")
	for _, d := range dirs {
		if !slices.ContainsFunc(Types, func(at AgentType) bool { return at.Name == d.Name() }) {
			t.Errorf("testdata/%s is the test folder of no agent type", d.Name())
		}
	}
}
