package gittest

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// Rule is one answer of a fake tool: the first rule whose Has is in the arguments or stdin gives
// what the tool prints to stdout and stderr and its exit status. An empty Has matches every call.
// With Clone, the tool first clones that repo into its last argument, as gh repo clone does; with
// Wait, it then waits that long, so a test can stop it while it runs.
type Rule struct {
	Has   string
	Out   string
	Err   string
	Exit  int
	Clone string
	Wait  time.Duration
}

var fakeNames = []string{"gh", "glab"}

// FakeTool puts a gh or glab first on PATH that answers by rules. It is the test binary itself, so
// it runs on Windows too; RunFakeTool in TestMain makes it act as the tool. args gives the
// arguments of the last call.
func FakeTool(t *testing.T, name string, rules ...Rule) (args func() []string) {
	t.Helper()
	calls := FakeToolCalls(t, name, rules...)
	return func() []string {
		all := calls()
		if len(all) == 0 {
			return nil
		}
		return all[len(all)-1].Args
	}
}

// Call is one call of a fake tool: its arguments, what it read with --input -, and the GitLab
// host it was given.
type Call struct {
	Args       []string
	Stdin      string
	GitlabHost string `json:",omitempty"`
}

// FakeToolCalls is FakeTool, and gives every call so far.
func FakeToolCalls(t *testing.T, name string, rules ...Rule) (calls func() []Call) {
	t.Helper()
	dir := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	file := name
	if runtime.GOOS == "windows" {
		file += ".exe"
	}
	if err := copyFile(exe, filepath.Join(dir, file)); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(rules)
	if err := os.WriteFile(filepath.Join(dir, "rules.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() []Call {
		b, _ := os.ReadFile(filepath.Join(dir, "calls.jsonl"))
		var out []Call
		for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			var c Call
			if json.Unmarshal([]byte(l), &c) == nil {
				out = append(out, c)
			}
		}
		return out
	}
}

// Has says whether the call holds each of the arguments, in any order. A GraphQL call with its
// variables on stdin holds each variable as name=value too.
func (c Call) Has(args ...string) bool {
	all := slices.Clone(c.Args)
	var in struct{ Variables map[string]any }
	if json.Unmarshal([]byte(c.Stdin), &in) == nil {
		for k, v := range in.Variables {
			all = append(all, fmt.Sprintf("%s=%v", k, v))
		}
	}
	for _, a := range args {
		if !slices.Contains(all, a) {
			return false
		}
	}
	return true
}

func copyFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// RunFakeTool acts as the fake tool and exits when the test binary was started as one.
func RunFakeTool() {
	exe, err := os.Executable()
	name := strings.TrimSuffix(filepath.Base(exe), ".exe")
	if err != nil || !contains(fakeNames, name) {
		return
	}
	dir := filepath.Dir(exe)
	args := os.Args[1:]
	call := Call{Args: args, GitlabHost: os.Getenv("GITLAB_HOST")}
	for i, a := range args {
		if a == "--input" && i+1 < len(args) && args[i+1] == "-" {
			in, _ := io.ReadAll(os.Stdin)
			call.Stdin = string(in)
		}
	}
	b, _ := json.Marshal(call)
	if f, err := os.OpenFile(filepath.Join(dir, "calls.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
		f.Write(append(b, '\n'))
		f.Close()
	}
	var rules []Rule
	if b, err := os.ReadFile(filepath.Join(dir, "rules.json")); err == nil {
		json.Unmarshal(b, &rules)
	}
	all := strings.Join(args, " ") + " " + call.Stdin
	for _, r := range rules {
		if strings.Contains(all, r.Has) {
			if r.Clone != "" {
				cmd := exec.Command("git", "clone", "-q", r.Clone, args[len(args)-1])
				cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
				if cmd.Run() != nil {
					os.Exit(1)
				}
			}
			time.Sleep(r.Wait)
			fmt.Print(r.Out)
			fmt.Fprint(os.Stderr, r.Err)
			os.Exit(r.Exit)
		}
	}
	fmt.Fprintf(os.Stderr, "no answer for: %s\n", all)
	os.Exit(1)
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
