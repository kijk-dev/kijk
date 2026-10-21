package git

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kijk-dev/kijk/internal/gittest"
)

// recorded reads the data part of a recorded gh or glab answer.
func recorded(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "forge", name))
	if err != nil {
		t.Fatal(err)
	}
	var env struct{ Data json.RawMessage }
	if err := json.Unmarshal(b, &env); err != nil {
		t.Fatal(err)
	}
	return env.Data
}

func rows(prs []PRRow) string {
	var s []string
	for _, p := range prs {
		s = append(s, fmt.Sprintf("%d %q %s draft=%v %s→%s +%d-%d ci=%s req=%v url=%s", p.Number, p.Title, p.Author, p.Draft, p.Head, p.Base, p.Add, p.Del, p.CI, p.Requested, p.URL))
	}
	return strings.Join(s, "\n")
}

// A search of several words cannot be an author or a branch.
func TestSearchQualifiers(t *testing.T) {
	f := &forge{Path: "acme/shop"}
	if v := githubSearchVars(f, "retry webhooks"); v["author"] != v["text"] || v["branch"] != v["text"] {
		t.Errorf("vars %v", v)
	}
}

func TestParseGithubList(t *testing.T) {
	var res PRList
	if err := parseGithubList(recorded(t, "github-list.json"), &res); err != nil {
		t.Fatal(err)
	}
	want := `477 "Retry webhook delivery with backoff" sanne draft=false retry-webhooks→main +312-40 ci=failed req=true url=https://github.com/acme/shop/pull/477
463 "Drop the v1 export endpoint" priya draft=true drop-v1→main +12-406 ci=running req=true url=
455 "Audit log for admin actions" you draft=false audit-log→main +241-9 ci= req=false url=https://github.com/acme/shop/pull/455
440 "Old branch of a deleted user"  draft=false old→main +1-0 ci= req=false url=https://github.com/acme/shop/pull/440
120 "Old but waiting for you" tim draft=false old-wait→main +5-1 ci= req=true url=https://github.com/acme/shop/pull/120`
	if got := rows(res.PRs); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	if res.Me != "you" || res.Total != 41 {
		t.Errorf("me %q, total %d", res.Me, res.Total)
	}
}

func TestParseGithubSearch(t *testing.T) {
	var res PRList
	if err := parseGithubSearch(recorded(t, "github-search.json"), &res); err != nil {
		t.Fatal(err)
	}
	// Hits by text, by author and by head branch, each once.
	want := `301 "Webhook signing" tim draft=false sign-webhooks→main +5-1 ci=passed req=false url=https://github.com/acme/shop/pull/301
250 "Speed up export" webhook draft=false faster-export→main +5-1 ci= req=false url=https://github.com/acme/shop/pull/250
260 "Retry more" priya draft=false webhook-retry→main +5-1 ci= req=false url=https://github.com/acme/shop/pull/260`
	if got := rows(res.PRs); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestParseGitlabSearch(t *testing.T) {
	var res PRList
	if err := parseGitlabSearch(recorded(t, "gitlab-search.json"), &res); err != nil {
		t.Fatal(err)
	}
	var got []int
	for _, p := range res.PRs {
		got = append(got, p.Number)
	}
	if fmt.Sprint(got) != "[12 7 8]" {
		t.Errorf("hits %v", got)
	}
}

func TestParseGitlabList(t *testing.T) {
	var res PRList
	if err := parseGitlabList(recorded(t, "gitlab-list.json"), &res); err != nil {
		t.Fatal(err)
	}
	want := `12 "Retry webhook delivery" sanne draft=true retry-webhooks→main +30-4 ci=running req=true url=https://gitlab.com/acme/shop/-/merge_requests/12
11 "Move rate limits into config" tim draft=false rate-config→main +88-61 ci=passed req=false url=https://gitlab.com/acme/shop/-/merge_requests/11
9 "Bump Go" you draft=false go-bump→main +0-0 ci=failed req=false url=https://gitlab.com/acme/shop/-/merge_requests/9
3 "Old but waiting for you" tim draft=false old-wait→main +3-0 ci= req=true url=https://gitlab.com/acme/shop/-/merge_requests/3`
	if got := rows(res.PRs); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	if res.Me != "you" || res.Total != 3 {
		t.Errorf("me %q, total %d", res.Me, res.Total)
	}
}

func reviewers(rs []Reviewer) string {
	var s []string
	for _, r := range rs {
		s = append(s, r.Name+":"+r.State)
	}
	return strings.Join(s, " ")
}

func comments(cs []Comment) string {
	var s []string
	for _, c := range cs {
		s = append(s, c.Author+": "+c.Body)
	}
	return strings.Join(s, " | ")
}

func threads(ts []Thread) string {
	var s []string
	for _, t := range ts {
		s = append(s, fmt.Sprintf("%s %s:%d old=%v orig=%d resolved=%v outdated=%v was=%q [%s]", t.ID, t.Path, t.Line, t.Old, t.OrigLine, t.Resolved, t.Outdated, t.Was, comments(t.Comments)))
	}
	return strings.Join(s, "\n")
}

func TestParseGithubPRView(t *testing.T) {
	var pr PR
	if err := parseGithubPRView(recorded(t, "github-pr.json"), &pr); err != nil {
		t.Fatal(err)
	}
	got := fmt.Sprintf("%d %s %s %s→%s head=%s base=%s commits=%d ci=%s approvals=%d me=%s", pr.Number, pr.State, pr.Author, pr.Head, pr.Base, pr.HeadOid[:4], pr.BaseOid[:4], pr.Commits, pr.CI, pr.Approvals, pr.Me)
	if want := "477 open sanne retry-webhooks→main head=1111 base=2222 commits=3 ci=failed approvals=1 me=you"; got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	// The author's own review is not a reviewer; a request after a review counts as requested.
	if got, want := reviewers(pr.Reviewers), "priya:approved tim:commented you:requested backend:requested"; got != want {
		t.Errorf("reviewers %s, want %s", got, want)
	}
	// Comments and review bodies in time order; empty and pending review bodies left out.
	if got, want := comments(pr.Comments), "tim: Do we want the attempt number in the log? | ghost: From a deleted account | priya: Looks good."; got != want {
		t.Errorf("conversation %s\nwant %s", got, want)
	}
	want := `T1 internal/webhook/retry.go:27 old=false orig=27 resolved=false outdated=false was="\t\tif !retryable(err) {" [tim: Should a 429 count as retryable? | sanne: Good point.]
T2 internal/webhook/retry.go:31 old=false orig=31 resolved=true outdated=false was="\t\tselect {" [priya: Check ctx before the first sleep?]
T3 internal/webhook/retry.go:0 old=false orig=40 resolved=false outdated=true was="\ttime.Sleep(500 * time.Millisecond)" [tim: Name this delay.]
T4 internal/webhook/legacy.go:3 old=true orig=3 resolved=false outdated=false was="func old() {}" [tim: Was anyone still calling this?]`
	if got := threads(pr.Threads); got != want {
		t.Errorf("threads\n%s\nwant\n%s", got, want)
	}
}

func TestParseGitlabPRView(t *testing.T) {
	var pr PR
	if err := parseGitlabPR(recorded(t, "gitlab-pr.json"), &pr); err != nil {
		t.Fatal(err)
	}
	got := fmt.Sprintf("%d %q %s %s head=%s mergebase=%s commits=%d approvals=%d body=%q", pr.Number, pr.Title, pr.State, pr.Author, pr.HeadOid[:4], pr.MergeBase[:4], pr.Commits, pr.Approvals, pr.Body)
	if want := `12 "Retry webhook delivery" draft sanne head=3333 mergebase=4444 commits=2 approvals=2 body="Retry *up to* five times."`; got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	if got, want := reviewers(pr.Reviewers), "you:requested priya:approved tim:commented ana:changes lead:approved"; got != want {
		t.Errorf("reviewers %s, want %s", got, want)
	}
	if got, want := comments(pr.Comments), "tim: Do we want the attempt number in the log?"; got != want {
		t.Errorf("conversation %s", got)
	}
	// A note written on another head is outdated; a note on the old side is on a removed line.
	want := `gid://gitlab/Discussion/a retry.go:27 old=false orig=27 resolved=false outdated=false was="" [tim: Should a 429 count? | sanne: Yes.]
gid://gitlab/Discussion/b retry.go:40 old=false orig=40 resolved=false outdated=true was="" [tim: Name this delay.]
gid://gitlab/Discussion/c legacy.go:3 old=true orig=3 resolved=true outdated=false was="" [tim: Was anyone calling this?]`
	if got := threads(pr.Threads); got != want {
		t.Errorf("threads\n%s\nwant\n%s", got, want)
	}
}

// fakeTool puts a gh or glab on PATH that saves its arguments, one per line, and prints answer.
func fakeTool(t *testing.T, tool, answer string) (args func() []string) {
	t.Helper()
	dir := t.TempDir()
	out := filepath.Join(dir, "args")
	ans := filepath.Join(dir, "answer.json")
	os.WriteFile(ans, []byte(answer), 0o644)
	script := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\"; done > '" + out + "'\ncat '" + ans + "'\n"
	if err := os.WriteFile(filepath.Join(dir, tool), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() []string {
		b, _ := os.ReadFile(out)
		return strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	}
}

func TestListPRsSearch(t *testing.T) {
	gittest.Isolate(t)
	repo := gittest.Repo(t)
	gittest.Run(t, repo, "remote", "add", "origin", "git@github.com:acme/shop.git")
	b, _ := os.ReadFile(filepath.Join("testdata", "forge", "github-search.json"))
	args := fakeTool(t, "gh", string(b))
	res := ListPRs(repo, "webhook")
	if res.Note != "" || len(res.PRs) != 3 || res.PRs[0].Number != 301 {
		t.Fatalf("res = %+v", res)
	}
	a := strings.Join(args(), " ")
	if !strings.Contains(a, "-f text=repo:acme/shop is:pr is:open webhook") || !strings.Contains(a, "-f author=repo:acme/shop is:pr is:open author:webhook") ||
		!strings.Contains(a, "-f branch=repo:acme/shop is:pr is:open head:webhook") || !strings.Contains(a, "--hostname github.com") {
		t.Errorf("gh was called with %s", a)
	}
}

func TestListPRsNotLoggedIn(t *testing.T) {
	gittest.Isolate(t)
	repo := gittest.Repo(t)
	gittest.Run(t, repo, "remote", "add", "origin", "https://gitlab.com/acme/shop.git")
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "glab"), []byte("#!/bin/sh\necho 'error: 401 Unauthorized' >&2\nexit 1\n"), 0o755)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if res := ListPRs(repo, ""); res.Note != "Log in with glab auth login to see the status" || res.Tool != "glab" {
		t.Errorf("res = %+v", res)
	}
}

// Both forges' words for a pull request's state read the same.
func TestPRState(t *testing.T) {
	for _, c := range []struct {
		state string
		draft bool
		want  string
	}{{"MERGED", false, "merged"}, {"merged", true, "merged"}, {"CLOSED", false, "closed"}, {"locked", false, "closed"},
		{"OPEN", true, "draft"}, {"opened", false, "open"}} {
		if got := prState(c.state, c.draft); got != c.want {
			t.Errorf("prState(%s, %v) = %s, want %s", c.state, c.draft, got, c.want)
		}
	}
}
