package git

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kijk-dev/kijk/internal/gittest"
)

func rev(t *testing.T, dir, ref string) string {
	t.Helper()
	out, err := Run(dir, "rev-parse", "--verify", "-q", ref+"^{commit}")
	if err != nil {
		return ""
	}
	return out
}

// prOrigin is a bare origin whose main has two commits, with pull request 7 as refs/pull/7/head
// and no branch for it, as a PR from a fork. clone is a clone of it, made before main moved on.
func prOrigin(t *testing.T) (origin, clone, work string) {
	t.Helper()
	gittest.Isolate(t)
	origin = filepath.Join(t.TempDir(), "shop.git")
	gittest.Run(t, t.TempDir(), "init", "-q", "--bare", "-b", "main", origin)
	work = gittest.Repo(t)
	gittest.Write(t, filepath.Join(work, "retry.go"), "package shop\n\nfunc retry() {}\n")
	gittest.Write(t, filepath.Join(work, "old.go"), "package shop\n\nfunc old() {}\n")
	gittest.Write(t, filepath.Join(work, "name.go"), "package shop\n\n// A file long enough for git to see a rename.\nconst name = \"shop\"\n")
	gittest.Run(t, work, "add", ".")
	gittest.Run(t, work, "commit", "-qm", "Start")
	gittest.Run(t, work, "remote", "add", "origin", origin)
	gittest.Run(t, work, "push", "-q", "origin", "main")
	clone = filepath.Join(t.TempDir(), "clone")
	gittest.Run(t, t.TempDir(), "clone", "-q", origin, clone)

	gittest.Run(t, work, "checkout", "-qb", "retry")
	gittest.Write(t, filepath.Join(work, "retry.go"), "package shop\n\nfunc retry() {\n\tfor i := 0; i < 3; i++ {\n\t}\n}\n")
	os.Remove(filepath.Join(work, "old.go"))
	gittest.Run(t, work, "mv", "name.go", "names.go")
	gittest.Run(t, work, "add", "-A")
	gittest.Run(t, work, "commit", "-qm", "Retry three times")
	gittest.Run(t, work, "push", "-q", "origin", "retry:refs/pull/7/head")

	gittest.Run(t, work, "checkout", "-q", "main")
	gittest.Write(t, filepath.Join(work, "readme.md"), "# Shop\n")
	gittest.Run(t, work, "add", ".")
	gittest.Run(t, work, "commit", "-qm", "Add a readme")
	gittest.Run(t, work, "push", "-q", "origin", "main")
	return origin, clone, work
}

func TestFetchPR(t *testing.T) {
	_, clone, work := prOrigin(t)
	head, base := rev(t, work, "retry"), rev(t, work, "main")
	if rev(t, clone, base) != "" {
		t.Fatal("the clone should not have the new main yet")
	}
	originMain := rev(t, clone, "origin/main")
	fetched, err := FetchPR(clone, "github", 7, head, base, "main")
	if err != nil || !fetched {
		t.Fatalf("first fetch: %v %v", fetched, err)
	}
	if got := rev(t, clone, "refs/kijk/pr/7"); got != head {
		t.Errorf("refs/kijk/pr/7 = %q, want %q", got, head)
	}
	// The base commit was missing: it comes into kijk's own ref, and origin/main stays where it was.
	if got := rev(t, clone, "refs/kijk/base/7"); got != base {
		t.Errorf("refs/kijk/base/7 = %q, want %q", got, base)
	}
	if got := rev(t, clone, "origin/main"); got != originMain {
		t.Errorf("origin/main moved to %q, want %q", got, originMain)
	}
	if out, _ := Run(clone, "branch", "--list"); out != "* main" {
		t.Errorf("branches changed: %q", out)
	}

	if fetched, err := FetchPR(clone, "github", 7, head, base, "main"); err != nil || fetched {
		t.Errorf("second fetch with the same head: %v %v", fetched, err)
	}

	gittest.Run(t, work, "checkout", "-q", "retry")
	gittest.Run(t, work, "commit", "-q", "--amend", "-m", "Retry three times, amended")
	gittest.Run(t, work, "push", "-q", "-f", "origin", "retry:refs/pull/7/head")
	moved := rev(t, work, "retry")
	if fetched, err := FetchPR(clone, "github", 7, moved, base, "main"); err != nil || !fetched {
		t.Fatalf("fetch after a force push: %v %v", fetched, err)
	}
	if got := rev(t, clone, "refs/kijk/pr/7"); got != moved {
		t.Errorf("after the force push refs/kijk/pr/7 = %q, want %q", got, moved)
	}
}

func TestFetchPRGitlab(t *testing.T) {
	_, clone, work := prOrigin(t)
	gittest.Run(t, work, "push", "-q", "origin", "retry:refs/merge-requests/3/head")
	head := rev(t, work, "retry")
	if _, err := FetchPR(clone, "gitlab", 3, head, "", ""); err != nil {
		t.Fatal(err)
	}
	if got := rev(t, clone, "refs/kijk/pr/3"); got != head {
		t.Errorf("refs/kijk/pr/3 = %q, want %q", got, head)
	}
	// GitLab's base is never fetched, so a base the repo lacks must not fetch again and again.
	if fetched, err := FetchPR(clone, "gitlab", 3, head, strings.Repeat("b", 40), ""); err != nil || fetched {
		t.Errorf("second fetch with an unknown base: %v %v", fetched, err)
	}
}

func TestFetchPRMissing(t *testing.T) {
	_, clone, _ := prOrigin(t)
	if _, err := FetchPR(clone, "github", 99, strings.Repeat("a", 40), "", "main"); err == nil {
		t.Error("a pull request the origin does not have should fail")
	}
}

// A base that is missing locally still gives the forge's diff, with origin/main left alone.
func TestPRDiffMissingBase(t *testing.T) {
	_, clone, work := prOrigin(t)
	head, base := rev(t, work, "retry"), rev(t, work, "main")
	originMain := rev(t, clone, "origin/main")
	if _, err := FetchPR(clone, "github", 7, head, base, "main"); err != nil {
		t.Fatal(err)
	}
	split, err := PRSplit(clone, &PR{PRRow: PRRow{Number: 7, Base: "main"}, BaseOid: base}, head)
	if err != nil || split != rev(t, work, "main~1") {
		t.Fatalf("split %q %v", split, err)
	}
	files, err := PRFiles(clone, split, head)
	if err != nil || paths(files) != "R names.go <- name.go, D old.go, M retry.go" {
		t.Errorf("files %s %v", paths(files), err)
	}
	if got := rev(t, clone, "origin/main"); got != originMain {
		t.Errorf("origin/main moved to %q", got)
	}
	if out, _ := Run(clone, "for-each-ref", "--format=%(refname)", "refs/remotes/"); out != "refs/remotes/origin/HEAD\nrefs/remotes/origin/main" {
		t.Errorf("remote refs:\n%s", out)
	}
}

func TestPRDiff(t *testing.T) {
	_, clone, work := prOrigin(t)
	head, base := rev(t, work, "retry"), rev(t, work, "main")
	if _, err := FetchPR(clone, "github", 7, head, base, "main"); err != nil {
		t.Fatal(err)
	}
	// main moved on after the split: the forge diff starts at the merge base, so the readme is not in it.
	split, err := MergeBase(clone, base, head)
	if err != nil || split != rev(t, work, "main~1") {
		t.Fatalf("merge base = %q %v", split, err)
	}
	files, err := PRFiles(clone, split, head)
	if err != nil {
		t.Fatal(err)
	}
	if got := paths(files); got != "R names.go <- name.go, D old.go, M retry.go" {
		t.Errorf("files: %s", got)
	}
	if files[2].Add != 4 || files[2].Del != 1 {
		t.Errorf("retry.go counts: +%d -%d", files[2].Add, files[2].Del)
	}
	res, err := Diff(clone, "retry.go", "", "pr", split+".."+head, 3)
	if err != nil || len(res.Hunks) != 1 {
		t.Fatalf("diff: %+v %v", res, err)
	}
	if l := res.Hunks[0].Lines; l[len(l)-1].Text != "}" {
		t.Errorf("last line %q", l[len(l)-1].Text)
	}
	for _, bad := range []string{head, "main..retry", split + ".." + head + " --output=x"} {
		if _, err := Diff(clone, "retry.go", "", "pr", bad, 3); err != ErrBadHash {
			t.Errorf("range %q: %v, want ErrBadHash", bad, err)
		}
	}
}

func TestDeletePRRefs(t *testing.T) {
	gittest.Isolate(t)
	repo := gittest.Repo(t)
	gittest.Write(t, filepath.Join(repo, "a"), "a\n")
	gittest.Run(t, repo, "add", ".")
	gittest.Run(t, repo, "commit", "-qm", "a")
	for _, r := range []string{"refs/kijk/pr/7", "refs/kijk/pr/8", "refs/kijk/pr/9", "refs/kijk/base/7", "refs/kijk/base/8", "refs/kijk/base/9", "refs/kijk/base/10", "refs/heads/keep", "refs/kijk/other"} {
		gittest.Run(t, repo, "update-ref", r, "HEAD")
	}
	DeletePRRef(repo, 9)
	SweepPRRefs(repo, []int{8, 12})
	out, _ := Run(repo, "for-each-ref", "--format=%(refname)")
	if out != "refs/heads/keep\nrefs/heads/main\nrefs/kijk/base/8\nrefs/kijk/other\nrefs/kijk/pr/8" {
		t.Errorf("refs left:\n%s", out)
	}
}

func TestPRFile(t *testing.T) {
	_, clone, work := prOrigin(t)
	head := rev(t, work, "retry")
	if _, err := FetchPR(clone, "github", 7, head, "", "main"); err != nil {
		t.Fatal(err)
	}
	cache := t.TempDir()
	p1, err := PRFile(clone, 7, "retry.go", cache)
	if err != nil {
		t.Fatal(err)
	}
	p2, _ := PRFile(clone, 7, "retry.go", cache)
	if p1 != p2 || !strings.HasPrefix(p1, cache) {
		t.Errorf("paths %q and %q", p1, p2)
	}
	if b, _ := os.ReadFile(p1); !strings.Contains(string(b), "i < 3") {
		t.Errorf("content %q", b)
	}
	for _, bad := range []string{"../retry.go", "/etc/passwd", "missing.go"} {
		if _, err := PRFile(clone, 7, bad, cache); err == nil {
			t.Errorf("PRFile(%q) should fail", bad)
		}
	}
}

// The target moves on after the first open while the head stays: kijk fetches the new base, and
// the diff starts at the forge's base, not at an old origin/main.
func TestPRBaseMovesLater(t *testing.T) {
	_, clone, work := prOrigin(t)
	head, base := rev(t, work, "retry"), rev(t, work, "main")
	if _, err := FetchPR(clone, "github", 7, head, base, "main"); err != nil {
		t.Fatal(err)
	}
	gittest.Run(t, work, "checkout", "-q", "main")
	gittest.Write(t, filepath.Join(work, "later.md"), "# Later\n")
	gittest.Run(t, work, "add", ".")
	gittest.Run(t, work, "commit", "-qm", "Later on main")
	gittest.Run(t, work, "push", "-q", "origin", "main")
	moved := rev(t, work, "main")
	fetched, err := FetchPR(clone, "github", 7, head, moved, "main")
	if err != nil || !fetched || rev(t, clone, "refs/kijk/base/7") != moved {
		t.Fatalf("fetch of the moved base: %v %v, refs/kijk/base/7 = %q", fetched, err, rev(t, clone, "refs/kijk/base/7"))
	}
	pr := &PR{PRRow: PRRow{Number: 7, Base: "main"}, BaseOid: moved}
	if split, err := PRSplit(clone, pr, head); err != nil || split != rev(t, work, "main~2") {
		t.Errorf("split %q %v", split, err)
	}
	// Without the forge's base, kijk's own base ref comes before the user's old origin/main.
	pr.BaseOid = ""
	orphan, _ := Run(clone, "commit-tree", "-m", "unrelated", "4b825dc642cb6eb9a060e54bf8d69288fbee4904")
	gittest.Run(t, clone, "update-ref", "refs/remotes/origin/main", orphan)
	if split, err := PRSplit(clone, pr, head); err != nil || split != rev(t, work, "main~2") {
		t.Errorf("split from refs/kijk/base: %q %v", split, err)
	}
	// A merge base from the forge that is no hash is not used as one.
	pr.MergeBase = "main"
	if split, err := PRSplit(clone, pr, head); err != nil || !ValidHash(split) {
		t.Errorf("split with a bad merge base: %q %v", split, err)
	}
}

// The merge bases kijk keeps are bounded, as the split points of branches are.
func TestMergeBaseCacheBounded(t *testing.T) {
	_, clone, work := prOrigin(t)
	head, base := rev(t, work, "retry"), rev(t, clone, "main")
	if _, err := FetchPR(clone, "github", 7, head, "", "main"); err != nil {
		t.Fatal(err)
	}
	splitCache.Lock()
	clear(splitCache.m) // other tests made the same commits, in the same second
	for i := 0; i < 150; i++ {
		splitCache.m[strings.Repeat("x", i)] = "y"
	}
	splitCache.Unlock()
	if _, err := MergeBase(clone, base, head); err != nil {
		t.Fatal(err)
	}
	splitCache.Lock()
	n := len(splitCache.m)
	splitCache.Unlock()
	if n > 101 {
		t.Errorf("the cache holds %d entries", n)
	}
}

// Files kept from a pull request's head go after a day, when kijk writes another.
func TestPRFilePrunes(t *testing.T) {
	_, clone, work := prOrigin(t)
	if _, err := FetchPR(clone, "github", 7, rev(t, work, "retry"), "", "main"); err != nil {
		t.Fatal(err)
	}
	cache := t.TempDir()
	old := filepath.Join(cache, strings.Repeat("a", 40))
	gittest.Write(t, filepath.Join(old, "x.go"), "old\n")
	day := time.Now().Add(-48 * time.Hour)
	os.Chtimes(old, day, day)
	if _, err := PRFile(clone, 7, "retry.go", cache); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); err == nil {
		t.Error("the old file is still kept")
	}
}
