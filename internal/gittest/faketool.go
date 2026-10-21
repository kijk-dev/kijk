package gittest

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Rule is one answer of a fake tool: the first rule whose Has is in the arguments gives what the
// tool prints to stdout and stderr and its exit status. An empty Has matches every call.
type Rule struct {
	Has  string
	Out  string
	Err  string
	Exit int
}

var fakeNames = []string{"gh", "glab"}

// FakeTool puts a gh or glab first on PATH that answers by rules. It is the test binary itself, so
// it runs on Windows too; RunFakeTool in TestMain makes it act as the tool. args gives the
// arguments of the last call.
func FakeTool(t *testing.T, name string, rules ...Rule) (args func() []string) {
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
	return func() []string {
		b, _ := os.ReadFile(filepath.Join(dir, "args.json"))
		var a []string
		json.Unmarshal(b, &a)
		return a
	}
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
	b, _ := json.Marshal(args)
	os.WriteFile(filepath.Join(dir, "args.json"), b, 0o644)
	var rules []Rule
	if b, err := os.ReadFile(filepath.Join(dir, "rules.json")); err == nil {
		json.Unmarshal(b, &rules)
	}
	all := strings.Join(args, " ")
	for _, r := range rules {
		if strings.Contains(all, r.Has) {
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
