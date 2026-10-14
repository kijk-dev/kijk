package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// isolatedGit hides the user's git config (signing, hooks) from the test repos.
func isolatedGit(t *testing.T) {
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

func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

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
	isolatedGit(t)
	repo := gitRepo(t)
	write(t, filepath.Join(repo, "a.go"), "a\n")
	write(t, filepath.Join(repo, "b.go"), "b\n")
	write(t, filepath.Join(repo, "gone.go"), "g\n")
	write(t, filepath.Join(repo, "old name.go"), "r\n")
	write(t, filepath.Join(repo, ".gitignore"), "build/\n*.log\n")
	run(t, repo, "add", ".")
	run(t, repo, "commit", "-qm", "init")

	write(t, filepath.Join(repo, "a.go"), "a2\n")
	run(t, repo, "add", "a.go")
	write(t, filepath.Join(repo, "b.go"), "b2\n")
	os.Remove(filepath.Join(repo, "gone.go"))
	run(t, repo, "mv", "old name.go", "new name.go")
	write(t, filepath.Join(repo, "src", "c.go"), "c\n")
	write(t, filepath.Join(repo, "build", "out.bin"), "x")
	write(t, filepath.Join(repo, "debug.log"), "x")

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
	isolatedGit(t)
	repo := gitRepo(t)
	write(t, filepath.Join(repo, "d.go"), "base\n")
	run(t, repo, "add", ".")
	run(t, repo, "commit", "-qm", "base")
	run(t, repo, "checkout", "-qb", "other")
	write(t, filepath.Join(repo, "d.go"), "theirs\n")
	run(t, repo, "commit", "-qam", "theirs")
	run(t, repo, "checkout", "-q", "main")
	write(t, filepath.Join(repo, "d.go"), "ours\n")
	run(t, repo, "commit", "-qam", "ours")
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
	isolatedGit(t)
	repo := gitRepo(t)
	var b strings.Builder
	for i := 1; i <= 20; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	write(t, filepath.Join(repo, "f.txt"), b.String())
	run(t, repo, "add", ".")
	run(t, repo, "commit", "-qm", "init")
	write(t, filepath.Join(repo, "f.txt"), strings.Replace(b.String(), "line 1\n", "changed\n", 1)+"-- dashes\n")

	d, err := diff(repo, "f.txt", "", "unstaged", "", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Hunks) != 2 {
		t.Fatalf("hunks = %+v", d.Hunks)
	}
	first := d.Hunks[0].Lines
	if !eqLine(first[0], diffLine{Kind: "-", Old: 1, Text: "line 1"}) || !eqLine(first[1], diffLine{Kind: "+", New: 1, Text: "changed"}) {
		t.Fatalf("first hunk = %+v", first)
	}
	last := d.Hunks[1].Lines
	if !eqLine(last[len(last)-1], diffLine{Kind: "+", New: 21, Text: "-- dashes"}) {
		t.Fatalf("last line = %+v", last[len(last)-1])
	}

	full, _ := diff(repo, "f.txt", "", "unstaged", "", 1000000)
	if len(full.Hunks) != 1 || len(full.Hunks[0].Lines) != 22 {
		t.Fatalf("full file: %d hunks", len(full.Hunks))
	}

	run(t, repo, "add", ".")
	if staged, _ := diff(repo, "f.txt", "", "staged", "", 3); len(staged.Hunks) != 2 {
		t.Fatalf("staged hunks = %d", len(staged.Hunks))
	}
	if _, err := diff(repo, "../x", "", "unstaged", "", 3); err == nil {
		t.Fatal("want error for a path outside the repo")
	}
}

func TestUntrackedAndBinaryDiff(t *testing.T) {
	isolatedGit(t)
	repo := gitRepo(t)
	write(t, filepath.Join(repo, "new.go"), "a\nb\n")
	d, err := diff(repo, "new.go", "", "untracked", "", 3)
	if err != nil || len(d.Hunks) != 1 || len(d.Hunks[0].Lines) != 2 || !eqLine(d.Hunks[0].Lines[1], diffLine{Kind: "+", New: 2, Text: "b"}) {
		t.Fatalf("untracked = %+v, %v", d, err)
	}

	write(t, filepath.Join(repo, "img.png"), "\x00\x01")
	run(t, repo, "add", ".")
	run(t, repo, "commit", "-qm", "img")
	write(t, filepath.Join(repo, "img.png"), "\x00\x02")
	if d, _ := diff(repo, "img.png", "", "unstaged", "", 3); !d.Binary {
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
	isolatedGit(t)
	repo := gitRepo(t)
	write(t, filepath.Join(repo, "a.go"), "a\n")
	write(t, filepath.Join(repo, "b.go"), "b\n")

	if _, err := stage(repo, []string{"a.go", "b.go"}); err != nil {
		t.Fatal(err)
	}
	if got := paths(status(repo).Staged); got != "A a.go, A b.go" {
		t.Fatalf("staged: %s", got)
	}
	if _, err := unstage(repo, []string{"b.go"}); err != nil { // no commit yet: uses git rm --cached
		t.Fatal(err)
	}
	if _, err := commit(repo, "first\n\nbody"); err != nil {
		t.Fatal(err)
	}
	if msg, _ := git(repo, "log", "-1", "--format=%B"); msg != "first\n\nbody" {
		t.Fatalf("message = %q", msg)
	}
	if files, _ := git(repo, "show", "--name-only", "--format=", "HEAD"); files != "a.go" {
		t.Fatalf("committed files = %q", files)
	}

	write(t, filepath.Join(repo, "a.go"), "a2\n")
	stage(repo, []string{"a.go"})
	if _, err := unstage(repo, []string{"a.go"}); err != nil { // with a commit: uses git restore --staged
		t.Fatal(err)
	}
	st := status(repo)
	if paths(st.Staged) != "" || paths(st.Changes) != "M a.go" {
		t.Fatalf("after unstage: staged %q, changes %q", paths(st.Staged), paths(st.Changes))
	}

	if _, err := commit(repo, "  \n"); err == nil {
		t.Fatal("empty message should fail")
	}
	if _, err := stage(repo, []string{"../x"}); err == nil {
		t.Fatal("path outside the repo should fail")
	}
}

func TestCommitHookFails(t *testing.T) {
	isolatedGit(t)
	repo := gitRepo(t)
	hook := filepath.Join(repo, ".git", "hooks", "pre-commit")
	write(t, hook, "#!/bin/sh\necho 'lint failed: a.go'\nexit 1\n")
	os.Chmod(hook, 0o755)
	write(t, filepath.Join(repo, "a.go"), "a\n")
	stage(repo, []string{"a.go"})

	out, err := commit(repo, "msg")
	if err == nil || !strings.Contains(out, "lint failed: a.go") {
		t.Fatalf("got %q, %v", out, err)
	}
	if _, err := git(repo, "rev-parse", "--verify", "-q", "HEAD"); err == nil {
		t.Fatal("a commit was created despite the failing hook")
	}
}

func TestStageConflictRefused(t *testing.T) {
	isolatedGit(t)
	repo := gitRepo(t)
	write(t, filepath.Join(repo, "d.go"), "base\n")
	run(t, repo, "add", ".")
	run(t, repo, "commit", "-qm", "base")
	run(t, repo, "checkout", "-qb", "other")
	write(t, filepath.Join(repo, "d.go"), "theirs\n")
	run(t, repo, "commit", "-qam", "theirs")
	run(t, repo, "checkout", "-q", "main")
	write(t, filepath.Join(repo, "d.go"), "ours\n")
	run(t, repo, "commit", "-qam", "ours")
	exec.Command("git", "-C", repo, "merge", "other").Run()

	if _, err := stage(repo, []string{"d.go"}); err == nil || !strings.Contains(err.Error(), "conflict") {
		t.Fatalf("got %v", err)
	}
}

func TestNestedRepoUntracked(t *testing.T) {
	isolatedGit(t)
	repo := gitRepo(t)
	nested := filepath.Join(repo, "fastmcp")
	run(t, repo, "init", "-q", nested)
	write(t, filepath.Join(nested, "x.py"), "x")

	if got := paths(status(repo).Untracked); got != "? fastmcp/" {
		t.Fatalf("untracked: %s", got)
	}
	d, err := diff(repo, "fastmcp/", "", "untracked", "", 3)
	if err != nil || !d.Folder {
		t.Fatalf("diff = %+v, %v", d, err)
	}
}

// repoWithOrigin returns a clone whose origin is a local bare repository with one commit on main.
func repoWithOrigin(t *testing.T) string {
	bare := filepath.Join(t.TempDir(), "origin.git")
	run(t, t.TempDir(), "init", "-q", "--bare", "-b", "main", bare)
	seed := gitRepo(t)
	write(t, filepath.Join(seed, "a.go"), "a\n")
	run(t, seed, "add", ".")
	run(t, seed, "commit", "-qm", "init")
	run(t, seed, "push", "-q", bare, "main")
	clone := filepath.Join(t.TempDir(), "clone")
	run(t, t.TempDir(), "clone", "-q", bare, clone)
	return clone
}

func TestCommitDiffAndPush(t *testing.T) {
	isolatedGit(t)
	repo := repoWithOrigin(t)
	write(t, filepath.Join(repo, "b.go"), "b\n")
	run(t, repo, "add", ".")
	run(t, repo, "commit", "-qm", "add b")
	run(t, repo, "mv", "a.go", "renamed.go")
	run(t, repo, "commit", "-qm", "rename a")

	st := status(repo)
	if st.Branch != "main" || st.Upstream != "origin/main" || st.Ahead != 2 || !st.Remote {
		t.Fatalf("status = %+v", st)
	}
	head, _ := git(repo, "rev-parse", "HEAD")
	parent, _ := git(repo, "rev-parse", "HEAD~1")
	files, err := commitFiles(repo, head)
	if err != nil || paths(files) != "R renamed.go <- a.go" {
		t.Fatalf("files = %q, %v", paths(files), err)
	}
	d, err := diff(repo, "b.go", "", "commit", parent, 3)
	if err != nil || len(d.Hunks) != 1 || !eqLine(d.Hunks[0].Lines[0], diffLine{Kind: "+", New: 1, Text: "b"}) {
		t.Fatalf("commit diff = %+v, %v", d, err)
	}

	if out, err := push(repo); err != nil {
		t.Fatalf("push: %v %s", err, out)
	}
	if st := status(repo); st.Ahead != 0 {
		t.Fatalf("ahead after push = %d", st.Ahead)
	}
}

func TestPublishBranch(t *testing.T) {
	isolatedGit(t)
	repo := repoWithOrigin(t)
	run(t, repo, "checkout", "-qb", "feature")
	run(t, repo, "commit", "-q", "--allow-empty", "-m", "one")
	run(t, repo, "commit", "-q", "--allow-empty", "-m", "two")

	st := status(repo)
	if st.Upstream != "" || st.Base != "origin/HEAD" || st.Ahead != 2 {
		t.Fatalf("status = %+v", st)
	}
	if out, err := push(repo); err != nil {
		t.Fatalf("publish: %v %s", err, out)
	}
	if st := status(repo); st.Upstream != "origin/feature" || st.Ahead != 0 {
		t.Fatalf("after publish = %+v", st)
	}
}

func TestMainLineAndGraph(t *testing.T) {
	isolatedGit(t)
	repo := repoWithOrigin(t)
	fork, _ := git(repo, "rev-parse", "HEAD")
	// The graph orders commits by time, and commits made in the same second have no fixed order.
	base := time.Now().Unix() + 60
	commitAt := func(sec int64) { t.Setenv("GIT_COMMITTER_DATE", fmt.Sprintf("@%d +0000", base+sec)) }
	run(t, repo, "checkout", "-qb", "feature")
	write(t, filepath.Join(repo, "a.go"), "feature\n")
	commitAt(2)
	run(t, repo, "commit", "-qam", "on feature")
	run(t, repo, "checkout", "-q", "main")
	write(t, filepath.Join(repo, "a.go"), "main\n")
	commitAt(1)
	run(t, repo, "commit", "-qam", "on main")
	run(t, repo, "push", "-q")
	if st := status(repo); st.Main != "" || st.Behind != 0 {
		t.Fatalf("main compared with itself: %+v", st)
	}
	run(t, repo, "checkout", "-q", "feature")

	st := status(repo)
	if st.Main != "origin/main" || st.MainBehind != 1 || strings.Join(st.MainConflicts, ",") != "a.go" {
		t.Fatalf("status = %+v", st)
	}
	g, err := graph(repo, "")
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
	run(t, repo, "checkout", "-qb", "release", fork)
	commitAt(3)
	run(t, repo, "commit", "-q", "--allow-empty", "-m", "on release")
	run(t, repo, "push", "-q", "origin", "release")
	run(t, repo, "checkout", "-q", "feature")
	if st := statusFor(repo, "release"); st.Main != "origin/release" || st.MainBehind != 1 {
		t.Fatalf("status against the target = %+v", st)
	}
	if st := statusFor(repo, "--output=x"); st.Main != "origin/main" {
		t.Fatalf("a bad target should fall back to main: %+v", st)
	}
	g, _ = graph(repo, "release")
	lanes := map[string]int{}
	for _, r := range g.Rows {
		if r.Commit != nil {
			lanes[r.Commit.Subject] = r.Lane
		}
	}
	if fmt.Sprint(lanes) != "map[init:0 on feature:0 on main:2 on release:1]" {
		t.Fatalf("lanes = %v", lanes)
	}

	if out, err := fetch(repo); err != nil {
		t.Fatalf("fetch: %v %s", err, out)
	}
	if status(repo).FetchedAt == 0 {
		t.Fatal("no fetch time after a fetch")
	}
}

func TestPushWithoutRemote(t *testing.T) {
	isolatedGit(t)
	repo := gitRepo(t)
	run(t, repo, "commit", "-q", "--allow-empty", "-m", "x")
	if g, err := graph(gitRepo(t), ""); err != nil || len(g.Rows) != 0 {
		t.Fatalf("graph before the first commit = %v, %v", g, err)
	}
	st := status(repo)
	if st.Remote || st.Base != "" || st.Ahead != 0 {
		t.Fatalf("status = %+v", st)
	}
	if _, err := push(repo); err == nil {
		t.Fatal("push without a remote should fail")
	}
	if _, err := commitFiles(repo, "--output=/tmp/x"); err == nil {
		t.Fatal("a hash that looks like a flag should be refused")
	}
}

func eqLine(a, b diffLine) bool {
	return a.Kind == b.Kind && a.Old == b.Old && a.New == b.New && a.Text == b.Text
}

func TestLineChanges(t *testing.T) {
	isolatedGit(t)
	repo := gitRepo(t)
	f := filepath.Join(repo, "a.txt")
	write(t, f, "1\n2\n3\n4\n5\n6\n")
	run(t, repo, "add", ".")
	run(t, repo, "commit", "-qm", "init")

	write(t, f, "0\n1\n2x\n3\n5\n6\n7\n8\n") // add at top, change 2, remove 4, add at end
	got, err := lineChanges(repo, "a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if s := fmt.Sprint(got); s != "[{add 1 1} {mod 3 1} {del 4 0} {add 7 2}]" {
		t.Errorf("changes = %s", s)
	}

	write(t, filepath.Join(repo, "new.txt"), "n\n")
	if got, _ := lineChanges(repo, "new.txt"); len(got) != 0 {
		t.Errorf("untracked file has marks: %v", got)
	}
}
