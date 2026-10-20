package git

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kijk-dev/kijk/internal/gittest"
)

func paths(cs []change) string {
	var s []string
	for _, c := range cs {
		p := c.Status + " " + c.Path
		if c.Orig != "" {
			p += " <- " + c.Orig
		}
		s = append(s, p)
	}
	return strings.Join(s, ", ")
}

func TestStatus(t *testing.T) {
	gittest.Isolate(t)
	repo := gittest.Repo(t)
	gittest.Write(t, filepath.Join(repo, "a.go"), "a\n")
	gittest.Write(t, filepath.Join(repo, "b.go"), "b\n")
	gittest.Write(t, filepath.Join(repo, "gone.go"), "g\n")
	gittest.Write(t, filepath.Join(repo, "old name.go"), "r\n")
	gittest.Write(t, filepath.Join(repo, ".gitignore"), "build/\n*.log\n")
	gittest.Run(t, repo, "add", ".")
	gittest.Run(t, repo, "commit", "-qm", "init")

	gittest.Write(t, filepath.Join(repo, "a.go"), "a2\n")
	gittest.Run(t, repo, "add", "a.go")
	gittest.Write(t, filepath.Join(repo, "b.go"), "b2\n")
	os.Remove(filepath.Join(repo, "gone.go"))
	gittest.Run(t, repo, "mv", "old name.go", "new name.go")
	gittest.Write(t, filepath.Join(repo, "src", "c.go"), "c\n")
	gittest.Write(t, filepath.Join(repo, "build", "out.bin"), "x")
	gittest.Write(t, filepath.Join(repo, "debug.log"), "x")

	st := status(repo)
	if !st.Git || st.Error != "" {
		t.Fatalf("status = %+v", st)
	}
	if got := paths(st.Staged); got != "M a.go, R new name.go <- old name.go" {
		t.Errorf("staged: %s", got)
	}
	if got := paths(st.Changes); got != "M b.go, D gone.go" {
		t.Errorf("changes: %s", got)
	}
	if got := paths(st.Untracked); got != "? src/c.go" {
		t.Errorf("untracked: %s", got)
	}
	if got := strings.Join(st.Ignored, ","); got != "build/,debug.log" {
		t.Errorf("ignored: %s", got)
	}
}

func TestStatusConflict(t *testing.T) {
	gittest.Isolate(t)
	repo := gittest.Repo(t)
	gittest.Write(t, filepath.Join(repo, "d.go"), "base\n")
	gittest.Run(t, repo, "add", ".")
	gittest.Run(t, repo, "commit", "-qm", "base")
	gittest.Run(t, repo, "checkout", "-qb", "other")
	gittest.Write(t, filepath.Join(repo, "d.go"), "theirs\n")
	gittest.Run(t, repo, "commit", "-qam", "theirs")
	gittest.Run(t, repo, "checkout", "-q", "main")
	gittest.Write(t, filepath.Join(repo, "d.go"), "ours\n")
	gittest.Run(t, repo, "commit", "-qam", "ours")
	exec.Command("git", "-C", repo, "merge", "other").Run() // fails with a conflict on purpose

	if got := paths(status(repo).Conflicts); got != "U d.go" {
		t.Fatalf("conflicts: %s", got)
	}
}

func TestStatusOutsideGit(t *testing.T) {
	st := status(t.TempDir())
	if st.Git || st.Error != "" {
		t.Fatalf("status = %+v", st)
	}
}

func TestDiff(t *testing.T) {
	gittest.Isolate(t)
	repo := gittest.Repo(t)
	var b strings.Builder
	for i := 1; i <= 20; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	gittest.Write(t, filepath.Join(repo, "f.txt"), b.String())
	gittest.Run(t, repo, "add", ".")
	gittest.Run(t, repo, "commit", "-qm", "init")
	gittest.Write(t, filepath.Join(repo, "f.txt"), strings.Replace(b.String(), "line 1\n", "changed\n", 1)+"-- dashes\n")

	d, err := Diff(repo, "f.txt", "", "unstaged", "", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Hunks) != 2 {
		t.Fatalf("hunks = %+v", d.Hunks)
	}
	first := d.Hunks[0].Lines
	if !eqLine(first[0], DiffLine{Kind: "-", Old: 1, Text: "line 1"}) || !eqLine(first[1], DiffLine{Kind: "+", New: 1, Text: "changed"}) {
		t.Fatalf("first hunk = %+v", first)
	}
	last := d.Hunks[1].Lines
	if !eqLine(last[len(last)-1], DiffLine{Kind: "+", New: 21, Text: "-- dashes"}) {
		t.Fatalf("last line = %+v", last[len(last)-1])
	}

	full, _ := Diff(repo, "f.txt", "", "unstaged", "", 1000000)
	if len(full.Hunks) != 1 || len(full.Hunks[0].Lines) != 22 {
		t.Fatalf("full file: %d hunks", len(full.Hunks))
	}

	gittest.Run(t, repo, "add", ".")
	if staged, _ := Diff(repo, "f.txt", "", "staged", "", 3); len(staged.Hunks) != 2 {
		t.Fatalf("staged hunks = %d", len(staged.Hunks))
	}
	if _, err := Diff(repo, "../x", "", "unstaged", "", 3); err == nil {
		t.Fatal("want error for a path outside the repo")
	}
}

func TestBinaryDiff(t *testing.T) {
	gittest.Isolate(t)
	repo := gittest.Repo(t)
	gittest.Write(t, filepath.Join(repo, "img.png"), "\x00\x01")
	gittest.Run(t, repo, "add", ".")
	gittest.Run(t, repo, "commit", "-qm", "img")
	gittest.Write(t, filepath.Join(repo, "img.png"), "\x00\x02")
	if d, _ := Diff(repo, "img.png", "", "unstaged", "", 3); !d.Binary {
		t.Fatalf("binary = %+v", d)
	}
}

func TestParseDiffHeaders(t *testing.T) {
	d := parseDiff("@@ -0,0 +1 @@\n+only\n@@ -5 +6,2 @@ func x\n x\n+y\n\\ No newline at end of file\n")
	if len(d.Hunks) != 2 || d.Hunks[0].NewStart != 1 || d.Hunks[1].OldStart != 5 || d.Hunks[1].NewStart != 6 || len(d.Hunks[1].Lines) != 2 {
		t.Fatalf("got %+v", d)
	}
}

func TestGitMissing(t *testing.T) {
	t.Setenv("PATH", "")
	st := status(t.TempDir())
	if st.Git || st.Error != "git was not found" {
		t.Fatalf("status = %+v", st)
	}
}

func TestStageUnstageCommit(t *testing.T) {
	gittest.Isolate(t)
	repo := gittest.Repo(t)
	gittest.Write(t, filepath.Join(repo, "a.go"), "a\n")
	gittest.Write(t, filepath.Join(repo, "b.go"), "b\n")

	if _, err := Stage(repo, []string{"a.go", "b.go"}); err != nil {
		t.Fatal(err)
	}
	if got := paths(status(repo).Staged); got != "A a.go, A b.go" {
		t.Fatalf("staged: %s", got)
	}
	if _, err := Unstage(repo, []string{"b.go"}); err != nil { // no commit yet: uses git rm --cached
		t.Fatal(err)
	}
	if _, err := Commit(repo, "first\n\nbody"); err != nil {
		t.Fatal(err)
	}
	if msg, _ := Run(repo, "log", "-1", "--format=%B"); msg != "first\n\nbody" {
		t.Fatalf("message = %q", msg)
	}
	if files, _ := Run(repo, "show", "--name-only", "--format=", "HEAD"); files != "a.go" {
		t.Fatalf("committed files = %q", files)
	}

	gittest.Write(t, filepath.Join(repo, "a.go"), "a2\n")
	Stage(repo, []string{"a.go"})
	if _, err := Unstage(repo, []string{"a.go"}); err != nil { // with a commit: uses git restore --staged
		t.Fatal(err)
	}
	st := status(repo)
	if paths(st.Staged) != "" || paths(st.Changes) != "M a.go" {
		t.Fatalf("after unstage: staged %q, changes %q", paths(st.Staged), paths(st.Changes))
	}

	if _, err := Commit(repo, "  \n"); err == nil {
		t.Fatal("empty message should fail")
	}
	if _, err := Stage(repo, []string{"../x"}); err == nil {
		t.Fatal("path outside the repo should fail")
	}
}

func TestCommitHookFails(t *testing.T) {
	gittest.Isolate(t)
	repo := gittest.Repo(t)
	hook := filepath.Join(repo, ".git", "hooks", "pre-commit")
	gittest.Write(t, hook, "#!/bin/sh\necho 'lint failed: a.go'\nexit 1\n")
	os.Chmod(hook, 0o755)
	gittest.Write(t, filepath.Join(repo, "a.go"), "a\n")
	Stage(repo, []string{"a.go"})

	out, err := Commit(repo, "msg")
	if err == nil || !strings.Contains(out, "lint failed: a.go") {
		t.Fatalf("got %q, %v", out, err)
	}
	if _, err := Run(repo, "rev-parse", "--verify", "-q", "HEAD"); err == nil {
		t.Fatal("a commit was created despite the failing hook")
	}
}

func TestStageConflictRefused(t *testing.T) {
	gittest.Isolate(t)
	repo := gittest.Repo(t)
	gittest.Write(t, filepath.Join(repo, "d.go"), "base\n")
	gittest.Run(t, repo, "add", ".")
	gittest.Run(t, repo, "commit", "-qm", "base")
	gittest.Run(t, repo, "checkout", "-qb", "other")
	gittest.Write(t, filepath.Join(repo, "d.go"), "theirs\n")
	gittest.Run(t, repo, "commit", "-qam", "theirs")
	gittest.Run(t, repo, "checkout", "-q", "main")
	gittest.Write(t, filepath.Join(repo, "d.go"), "ours\n")
	gittest.Run(t, repo, "commit", "-qam", "ours")
	exec.Command("git", "-C", repo, "merge", "other").Run()

	if _, err := Stage(repo, []string{"d.go"}); err == nil || !strings.Contains(err.Error(), "conflict") {
		t.Fatalf("got %v", err)
	}
}

func TestNestedRepoUntracked(t *testing.T) {
	gittest.Isolate(t)
	repo := gittest.Repo(t)
	nested := filepath.Join(repo, "fastmcp")
	gittest.Run(t, repo, "init", "-q", nested)
	gittest.Write(t, filepath.Join(nested, "x.py"), "x")

	if got := paths(status(repo).Untracked); got != "? fastmcp/" {
		t.Fatalf("untracked: %s", got)
	}
}

// repoWithOrigin returns a clone whose origin is a local bare repository with one commit on main.
func repoWithOrigin(t *testing.T) string {
	bare := filepath.Join(t.TempDir(), "origin.git")
	gittest.Run(t, t.TempDir(), "init", "-q", "--bare", "-b", "main", bare)
	seed := gittest.Repo(t)
	gittest.Write(t, filepath.Join(seed, "a.go"), "a\n")
	gittest.Run(t, seed, "add", ".")
	gittest.Run(t, seed, "commit", "-qm", "init")
	gittest.Run(t, seed, "push", "-q", bare, "main")
	clone := filepath.Join(t.TempDir(), "clone")
	gittest.Run(t, t.TempDir(), "clone", "-q", bare, clone)
	return clone
}

func TestCommitDiffAndPush(t *testing.T) {
	gittest.Isolate(t)
	repo := repoWithOrigin(t)
	gittest.Write(t, filepath.Join(repo, "b.go"), "b\n")
	gittest.Run(t, repo, "add", ".")
	gittest.Run(t, repo, "commit", "-qm", "add b")
	gittest.Run(t, repo, "mv", "a.go", "renamed.go")
	gittest.Run(t, repo, "commit", "-qm", "rename a")

	st := status(repo)
	if st.Branch != "main" || st.Upstream != "origin/main" || st.Ahead != 2 || !st.Remote {
		t.Fatalf("status = %+v", st)
	}
	head, _ := Run(repo, "rev-parse", "HEAD")
	parent, _ := Run(repo, "rev-parse", "HEAD~1")
	files, err := CommitFiles(repo, head)
	if err != nil || paths(files) != "R renamed.go <- a.go" {
		t.Fatalf("files = %q, %v", paths(files), err)
	}
	d, err := Diff(repo, "b.go", "", "commit", parent, 3)
	if err != nil || len(d.Hunks) != 1 || !eqLine(d.Hunks[0].Lines[0], DiffLine{Kind: "+", New: 1, Text: "b"}) {
		t.Fatalf("commit diff = %+v, %v", d, err)
	}

	if out, err := Push(repo); err != nil {
		t.Fatalf("push: %v %s", err, out)
	}
	if st := status(repo); st.Ahead != 0 {
		t.Fatalf("ahead after push = %d", st.Ahead)
	}
}

func TestPublishBranch(t *testing.T) {
	gittest.Isolate(t)
	repo := repoWithOrigin(t)
	gittest.Run(t, repo, "checkout", "-qb", "feature")
	gittest.Run(t, repo, "commit", "-q", "--allow-empty", "-m", "one")
	gittest.Run(t, repo, "commit", "-q", "--allow-empty", "-m", "two")

	st := status(repo)
	if st.Upstream != "" || st.Base != "origin/HEAD" || st.Ahead != 2 {
		t.Fatalf("status = %+v", st)
	}
	if out, err := Push(repo); err != nil {
		t.Fatalf("publish: %v %s", err, out)
	}
	if st := status(repo); st.Upstream != "origin/feature" || st.Ahead != 0 {
		t.Fatalf("after publish = %+v", st)
	}
}

func TestMainLineAndGraph(t *testing.T) {
	gittest.Isolate(t)
	repo := repoWithOrigin(t)
	fork, _ := Run(repo, "rev-parse", "HEAD")
	// The graph orders commits by time, and commits made in the same second have no fixed order.
	base := time.Now().Unix() + 60
	commitAt := func(sec int64) { t.Setenv("GIT_COMMITTER_DATE", fmt.Sprintf("@%d +0000", base+sec)) }
	gittest.Run(t, repo, "checkout", "-qb", "feature")
	gittest.Write(t, filepath.Join(repo, "a.go"), "feature\n")
	commitAt(2)
	gittest.Run(t, repo, "commit", "-qam", "on feature")
	gittest.Run(t, repo, "checkout", "-q", "main")
	gittest.Write(t, filepath.Join(repo, "a.go"), "main\n")
	commitAt(1)
	gittest.Run(t, repo, "commit", "-qam", "on main")
	gittest.Run(t, repo, "push", "-q")
	if st := status(repo); st.Main != "" || st.Behind != 0 {
		t.Fatalf("main compared with itself: %+v", st)
	}
	gittest.Run(t, repo, "checkout", "-q", "feature")

	st := status(repo)
	if st.Main != "origin/main" || st.MainBehind != 1 || strings.Join(st.MainConflicts, ",") != "a.go" {
		t.Fatalf("status = %+v", st)
	}
	g, err := Graph(repo, "")
	if err != nil || g.Fork != fork {
		t.Fatalf("graph = %+v, %v", g, err)
	}
	var got []string
	for _, r := range g.Rows {
		if r.Commit != nil {
			got = append(got, r.Graph+" "+r.Commit.Subject+" "+strings.Join(r.Commit.Refs, ","))
		} else {
			got = append(got, r.Graph)
		}
	}
	want := "* on feature HEAD -> feature; | * on main origin/main,main; |/; * init "
	if strings.Join(got, "; ") != want {
		t.Fatalf("graph rows = %q", strings.Join(got, "; "))
	}

	// A pull request into release: compare with it, and give each branch its own lane colour.
	gittest.Run(t, repo, "checkout", "-qb", "release", fork)
	commitAt(3)
	gittest.Run(t, repo, "commit", "-q", "--allow-empty", "-m", "on release")
	gittest.Run(t, repo, "push", "-q", "origin", "release")
	gittest.Run(t, repo, "checkout", "-q", "feature")
	if st := StatusFor(repo, "release"); st.Main != "origin/release" || st.MainBehind != 1 {
		t.Fatalf("status against the target = %+v", st)
	}
	if st := StatusFor(repo, "--output=x"); st.Main != "origin/main" {
		t.Fatalf("a bad target should fall back to main: %+v", st)
	}
	g, _ = Graph(repo, "release")
	lanes := map[string]int{}
	for _, r := range g.Rows {
		if r.Commit != nil {
			lanes[r.Commit.Subject] = r.Lane
		}
	}
	if fmt.Sprint(lanes) != "map[init:0 on feature:0 on main:2 on release:1]" {
		t.Fatalf("lanes = %v", lanes)
	}

	if out, err := Fetch(repo); err != nil {
		t.Fatalf("fetch: %v %s", err, out)
	}
	if status(repo).FetchedAt == 0 {
		t.Fatal("no fetch time after a fetch")
	}
}

func TestPushWithoutRemote(t *testing.T) {
	gittest.Isolate(t)
	repo := gittest.Repo(t)
	gittest.Run(t, repo, "commit", "-q", "--allow-empty", "-m", "x")
	if g, err := Graph(gittest.Repo(t), ""); err != nil || len(g.Rows) != 0 {
		t.Fatalf("graph before the first commit = %v, %v", g, err)
	}
	st := status(repo)
	if st.Remote || st.Base != "" || st.Ahead != 0 {
		t.Fatalf("status = %+v", st)
	}
	if _, err := Push(repo); err == nil {
		t.Fatal("push without a remote should fail")
	}
	if _, err := CommitFiles(repo, "--output=/tmp/x"); err == nil {
		t.Fatal("a hash that looks like a flag should be refused")
	}
}

func eqLine(a, b DiffLine) bool {
	return a.Kind == b.Kind && a.Old == b.Old && a.New == b.New && a.Text == b.Text
}

func TestLineChanges(t *testing.T) {
	gittest.Isolate(t)
	repo := gittest.Repo(t)
	f := filepath.Join(repo, "a.txt")
	gittest.Write(t, f, "1\n2\n3\n4\n5\n6\n")
	gittest.Run(t, repo, "add", ".")
	gittest.Run(t, repo, "commit", "-qm", "init")

	gittest.Write(t, f, "0\n1\n2x\n3\n5\n6\n7\n8\n") // add at top, change 2, remove 4, add at end
	got, err := LineChanges(repo, "a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if s := fmt.Sprint(got); s != "[{add 1 1} {mod 3 1} {del 4 0} {add 7 2}]" {
		t.Errorf("changes = %s", s)
	}

	gittest.Write(t, filepath.Join(repo, "new.txt"), "n\n")
	if got, _ := LineChanges(repo, "new.txt"); len(got) != 0 {
		t.Errorf("untracked file has marks: %v", got)
	}
}
