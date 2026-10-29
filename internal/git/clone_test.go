package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/kijk-dev/kijk/internal/gittest"
)

// cloneSource is a repo with one commit, for the fake gh and glab to clone.
func cloneSource(t *testing.T) (repo, head string) {
	t.Helper()
	gittest.Isolate(t)
	repo = gittest.Repo(t)
	gittest.Write(t, filepath.Join(repo, "README.md"), "# Billing\n")
	gittest.Run(t, repo, "add", ".")
	gittest.Run(t, repo, "commit", "-qm", "Start")
	return repo, rev(t, repo, "HEAD")
}

// parts are the part folders next to dir.
func parts(dir string) []string {
	found, _ := filepath.Glob(dir + "+part-*")
	return found
}

func TestClone(t *testing.T) {
	src, head := cloneSource(t)
	gittest.Logins(t, nil, nil)
	calls := gittest.FakeToolCalls(t, "gh", gittest.Rule{Has: "repo clone", Clone: src})
	dir := filepath.Join(t.TempDir(), "github.com", "acme", "billing")
	if err := Clone(context.Background(), "github", "github.com", "acme/billing", dir, nil, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	want := []string{"repo", "clone", "github.com/acme/billing", fmt.Sprintf("%s+part-%d", dir, os.Getpid())}
	if c := calls(); len(c) != 1 || !slices.Equal(c[0].Args, want) {
		t.Fatalf("calls %+v, want one with %q", c, want)
	}
	if got := rev(t, dir, "HEAD"); got != head || len(parts(dir)) > 0 {
		t.Fatalf("clone at %s, parts %v", got, parts(dir))
	}
	if _, err := os.Stat(filepath.Join(dir, "README.md")); err != nil {
		t.Fatal("no checkout:", err)
	}
	if err := Clone(context.Background(), "github", "github.com", "acme/billing", dir, nil, io.Discard, io.Discard); err != nil || len(calls()) != 1 {
		t.Fatalf("a second clone: %v, %d calls", err, len(calls()))
	}
}

func TestCloneGitLab(t *testing.T) {
	src, head := cloneSource(t)
	gittest.Logins(t, nil, []string{"gitlab.example.org"})
	calls := gittest.FakeToolCalls(t, "glab", gittest.Rule{Has: "repo clone", Clone: src})
	dir := filepath.Join(t.TempDir(), "api")
	if err := Clone(context.Background(), "gitlab", "gitlab.example.org", "team/api", dir, nil, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	c := calls()
	if len(c) != 1 || !c[0].Has("repo", "clone", "team/api") || c[0].GitlabHost != "gitlab.example.org" {
		t.Fatalf("calls %+v", c)
	}
	if rev(t, dir, "HEAD") != head {
		t.Fatal("no clone")
	}
}

// glab takes a forge's port in GITLAB_HOST; gh takes none in a repo name.
func TestCloneOnItsOwnPort(t *testing.T) {
	gittest.Logins(t, []string{"github.example.org"}, []string{"gitlab.example.org:8443"})
	glab := gittest.FakeToolCalls(t, "glab", gittest.Rule{Has: "repo clone", Exit: 1})
	gh := gittest.FakeToolCalls(t, "gh", gittest.Rule{Has: "repo clone", Exit: 1})
	dir := filepath.Join(t.TempDir(), "api")
	Clone(context.Background(), "gitlab", "gitlab.example.org:8443", "team/api", dir, nil, io.Discard, io.Discard)
	Clone(context.Background(), "github", "github.example.org:8443", "acme/api", dir, nil, io.Discard, io.Discard)
	if c := glab(); len(c) != 1 || c[0].GitlabHost != "gitlab.example.org:8443" || !c[0].Has("team/api") {
		t.Errorf("glab calls %+v", c)
	}
	if c := gh(); len(c) != 1 || !c[0].Has("github.example.org/acme/api") {
		t.Errorf("gh calls %+v", c)
	}
}

func TestCloneFails(t *testing.T) {
	gittest.FakeTool(t, "gh", gittest.Rule{Has: "repo clone", Err: "GraphQL: Could not resolve to a Repository\n", Exit: 1})
	dir := filepath.Join(t.TempDir(), "billing")
	var errOut bytes.Buffer
	err := Clone(context.Background(), "github", "github.com", "acme/billing", dir, nil, io.Discard, &errOut)
	if err == nil || err.Error() != "gh repo clone: exit status 1" || errOut.String() != "GraphQL: Could not resolve to a Repository\n" {
		t.Fatalf("error %v, tool said %q", err, errOut.String())
	}
	if _, err := os.Stat(dir); err == nil || len(parts(dir)) > 0 {
		t.Fatalf("left %s or %v", dir, parts(dir))
	}
}

// Ctrl+C stops the tool and removes what it cloned so far.
func TestCloneStopped(t *testing.T) {
	src, _ := cloneSource(t)
	gittest.FakeTool(t, "gh", gittest.Rule{Has: "repo clone", Clone: src, Wait: time.Minute})
	dir := filepath.Join(t.TempDir(), "billing")
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for len(parts(dir)) == 0 || !isClone(parts(dir)[0]) {
			time.Sleep(20 * time.Millisecond)
		}
		cancel()
	}()
	start := time.Now()
	err := Clone(ctx, "github", "github.com", "acme/billing", dir, nil, io.Discard, io.Discard)
	if !errors.Is(err, context.Canceled) || time.Since(start) > 30*time.Second {
		t.Fatalf("error %v after %s", err, time.Since(start))
	}
	if _, err := os.Stat(dir); err == nil || len(parts(dir)) > 0 {
		t.Fatalf("left %s or %v", dir, parts(dir))
	}
}

// A part folder of a run that is gone and over an hour old is a crashed run's. A run that still
// clones keeps its part folder, and so does another repo, such as acme/billing.part-5.
func TestCloneSweepsOldParts(t *testing.T) {
	src, _ := cloneSource(t)
	gittest.Logins(t, nil, nil)
	gittest.FakeTool(t, "gh", gittest.Rule{Has: "repo clone", Clone: src})
	exited := func() int {
		cmd := exec.Command(os.Args[0], "-test.run=^$")
		if err := cmd.Run(); err != nil {
			t.Fatal(err)
		}
		return cmd.Process.Pid
	}
	gone := exited()
	dir := filepath.Join(t.TempDir(), "billing")
	crashed, slow, young := partOf(dir, gone), partOf(dir, os.Getppid()), partOf(dir, exited())
	repo, other := fmt.Sprintf("%s.part-%d", dir, gone), dir+"+part-old"
	for _, p := range []string{crashed, slow, young, repo, other} {
		gittest.Write(t, filepath.Join(p, ".git", "HEAD"), "ref: refs/heads/main\n")
		if p != young {
			hourAgo := time.Now().Add(-61 * time.Minute)
			os.Chtimes(p, hourAgo, hourAgo)
		}
	}
	if err := Clone(context.Background(), "github", "github.com", "acme/billing", dir, nil, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{slow, young, repo, other} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("swept %s", filepath.Base(p))
		}
	}
	if _, err := os.Stat(crashed); err == nil {
		t.Error("kept the crashed run's part folder")
	}
}

// A link can name any host, and gh and glab send the user's token to it. kijk clones only from
// github.com, gitlab.com, or a host the tool is logged in to, with its port for glab.
func TestCloneOnlyFromAKnownHost(t *testing.T) {
	gittest.Isolate(t)
	gittest.Logins(t, []string{"github.example.org"}, []string{"gitlab.example.org:8443", "code.example.org"})
	gh := gittest.FakeToolCalls(t, "gh", gittest.Rule{Has: "repo clone", Exit: 1})
	glab := gittest.FakeToolCalls(t, "glab", gittest.Rule{Has: "repo clone", Exit: 1})
	for _, c := range []struct {
		kind, host string
		ok         bool
	}{
		{"github", "github.com", true},
		{"github", "github.example.org", true},
		{"github", "github.example.org:8443", true},
		{"github", "evil.example", false},
		{"github", "github.com.evil.example", false},
		{"github", "gitlab.com", false},
		{"gitlab", "gitlab.com", true},
		{"gitlab", "gitlab.example.org:8443", true},
		{"gitlab", "gitlab.example.org", false},
		{"gitlab", "code.example.org:8443", false},
		{"gitlab", "gitlab.com.evil.example", false},
		{"gitlab", "github.com", false},
	} {
		before := len(gh()) + len(glab())
		err := Clone(context.Background(), c.kind, c.host, "acme/api", filepath.Join(t.TempDir(), "api"), nil, io.Discard, io.Discard)
		ran := len(gh())+len(glab()) > before
		if c.ok != CanClone(c.kind, c.host) || c.ok != ran || !c.ok && !errors.Is(err, ErrUnknownHost) {
			t.Errorf("%s from %s: CanClone %v, ran %v, error %v; want %v", c.kind, c.host, CanClone(c.kind, c.host), ran, err, c.ok)
		}
	}
}

// Two kijk commands for the same link, at once: both clone, one rename wins, and both use its clone.
func TestCloneTwoRunsAtOnce(t *testing.T) {
	src, head := cloneSource(t)
	calls := gittest.FakeToolCalls(t, "gh", gittest.Rule{Has: "repo clone", Clone: src, Wait: time.Second})
	dir := filepath.Join(t.TempDir(), "github.com", "acme", "billing")
	var runs []*exec.Cmd
	var outs []*bytes.Buffer
	for range 2 {
		cmd := exec.Command(os.Args[0], "-test.run=^TestCloneRun$")
		cmd.Env = append(os.Environ(), "KIJK_TEST_CLONE_DIR="+dir)
		out := &bytes.Buffer{}
		cmd.Stdout, cmd.Stderr = out, out
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		runs, outs = append(runs, cmd), append(outs, out)
	}
	for i, cmd := range runs {
		if err := cmd.Wait(); err != nil {
			t.Fatalf("run %d: %v\n%s", i, err, outs[i])
		}
	}
	if n := len(calls()); n != 2 {
		t.Fatalf("%d clones, want both runs to clone", n)
	}
	if rev(t, dir, "HEAD") != head || len(parts(dir)) > 0 {
		t.Fatalf("no clone at %s, or parts left: %v", dir, parts(dir))
	}
}

// TestCloneRun is one kijk command's clone, in a process of its own, for TestCloneTwoRunsAtOnce.
func TestCloneRun(t *testing.T) {
	dir := os.Getenv("KIJK_TEST_CLONE_DIR")
	if dir == "" {
		t.Skip("runs as a process of TestCloneTwoRunsAtOnce")
	}
	if err := Clone(context.Background(), "github", "github.com", "acme/billing", dir, nil, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !isClone(dir) {
		t.Fatal("no clone at", dir)
	}
}
