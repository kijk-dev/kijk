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
	// Only your own pending review is visible, so its comments are your drafts.
	if res.PRs[0].Drafts != 3 || res.PRs[1].Drafts != 0 {
		t.Errorf("drafts %d %d, want 3 0", res.PRs[0].Drafts, res.PRs[1].Drafts)
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
		if c.Draft {
			s = append(s, "draft "+c.ID+" "+c.Author+": "+c.Body)
		} else {
			s = append(s, c.Author+": "+c.Body)
		}
	}
	return strings.Join(s, " | ")
}

func threads(ts []Thread) string {
	var s []string
	for _, t := range ts {
		var can []string
		for _, c := range []struct {
			on   bool
			name string
		}{{t.Draft, "draft"}, {t.CanReply, "reply"}, {t.CanResolve, "resolve"}, {t.CanUnresolve, "unresolve"}} {
			if c.on {
				can = append(can, c.name)
			}
		}
		s = append(s, fmt.Sprintf("%s %s:%d old=%v orig=%d resolved=%v outdated=%v was=%q %v [%s]", t.ID, t.Path, t.Line, t.Old, t.OrigLine, t.Resolved, t.Outdated, t.Was, can, comments(t.Comments)))
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
	// Your pending comments are drafts: a reply in T1, and T5, a new comment of only a draft.
	want := `T1 internal/webhook/retry.go:27 old=false orig=27 resolved=false outdated=false was="\t\tif !retryable(err) {" [reply resolve] [tim: Should a 429 count as retryable? | sanne: Good point. | draft C3 you: My draft]
T2 internal/webhook/retry.go:31 old=false orig=31 resolved=true outdated=false was="\t\tselect {" [reply unresolve] [priya: Check ctx before the first sleep?]
T3 internal/webhook/retry.go:0 old=false orig=40 resolved=false outdated=true was="\ttime.Sleep(500 * time.Millisecond)" [] [tim: Name this delay.]
T4 internal/webhook/legacy.go:3 old=true orig=3 resolved=false outdated=false was="func old() {}" [] [tim: Was anyone still calling this?]
T5 go.mod:4 old=false orig=4 resolved=false outdated=false was="x" [draft] [draft C9 you: Only a draft]`
	if got := threads(pr.Threads); got != want {
		t.Errorf("threads\n%s\nwant\n%s", got, want)
	}
	if pr.Drafts != 2 {
		t.Errorf("drafts %d, want 2", pr.Drafts)
	}
	// The summary is the body of your pending review.
	if pr.Summary != "Looks good, one question." {
		t.Errorf("summary %q", pr.Summary)
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
	want := `gid://gitlab/Discussion/a retry.go:27 old=false orig=27 resolved=false outdated=false was="" [reply resolve] [tim: Should a 429 count? | sanne: Yes.]
gid://gitlab/Discussion/b retry.go:40 old=false orig=40 resolved=false outdated=true was="" [reply resolve] [tim: Name this delay.]
gid://gitlab/Discussion/c legacy.go:3 old=true orig=3 resolved=true outdated=false was="" [reply unresolve] [tim: Was anyone calling this?]`
	if got := threads(pr.Threads); got != want {
		t.Errorf("threads\n%s\nwant\n%s", got, want)
	}
}

// A note on a whole file has no line to show under: it is part of the conversation, as a draft on a
// whole file is.
func TestParseGitlabFileNote(t *testing.T) {
	var pr PR
	data := `{"project":{"mergeRequest":{"iid":"12","diffHeadSha":"3333","discussions":{"nodes":[{"id":"gid://gitlab/Discussion/f","resolvable":true,"notes":{"nodes":[
{"author":{"username":"tim"},"body":"Split this file?","position":{"filePath":"retry.go","newPath":"retry.go","oldPath":"retry.go","newLine":null,"oldLine":null,"diffRefs":{"headSha":"3333"}}}]}}]}}}}`
	if err := parseGitlabPR([]byte(data), &pr); err != nil {
		t.Fatal(err)
	}
	if len(pr.Threads) != 0 || comments(pr.Comments) != "tim: Split this file?" {
		t.Errorf("threads %s, conversation %s", threads(pr.Threads), comments(pr.Comments))
	}
}

// fakeTool puts a gh or glab on PATH that prints answer, and gives the arguments of its last call.
func fakeTool(t *testing.T, tool, answer string) (args func() []string) {
	t.Helper()
	return gittest.FakeTool(t, tool, gittest.Rule{Out: answer})
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
	gittest.FakeTool(t, "glab", gittest.Rule{Err: "error: 401 Unauthorized\n", Exit: 1})
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

// GitLab keeps drafts apart: a reply goes into its thread, a draft on a line is a new comment, and
// any other is part of the conversation.
func TestGitlabDrafts(t *testing.T) {
	var pr PR
	if err := parseGitlabPR(recorded(t, "gitlab-pr.json"), &pr); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join("testdata", "forge", "gitlab-drafts.json"))
	if err != nil {
		t.Fatal(err)
	}
	var ds []gitlabDraft
	if err := json.Unmarshal(b, &ds); err != nil {
		t.Fatal(err)
	}
	addGitlabDrafts(&pr, ds)
	want := `gid://gitlab/Discussion/a retry.go:27 old=false orig=27 resolved=false outdated=false was="" [reply resolve] [tim: Should a 429 count? | sanne: Yes. | draft 51 you: Cap it at MaxDelay.]
gid://gitlab/Discussion/b retry.go:40 old=false orig=40 resolved=false outdated=true was="" [reply resolve] [tim: Name this delay.]
gid://gitlab/Discussion/c legacy.go:3 old=true orig=3 resolved=true outdated=false was="" [reply unresolve] [tim: Was anyone calling this?]
draft-52 config.go:16 old=false orig=16 resolved=false outdated=false was="" [draft] [draft 52 you: Is 5 enough?]
draft-54 legacy.go:3 old=true orig=3 resolved=false outdated=true was="" [draft] [draft 54 you: Why remove this?]`
	if got := threads(pr.Threads); got != want {
		t.Errorf("threads\n%s\nwant\n%s", got, want)
	}
	if got, want := comments(pr.Comments), "tim: Do we want the attempt number in the log? | draft 55 you: Yes, please."; got != want {
		t.Errorf("conversation %s\nwant %s", got, want)
	}
	// The first draft not on a line and not a reply is the summary of your review.
	if pr.Drafts != 4 || pr.Summary != "Looks fine overall." {
		t.Errorf("drafts %d, summary %q", pr.Drafts, pr.Summary)
	}
	// A comment on a whole file is no summary, even when it is the oldest draft not on a line.
	var file PR
	var ds2 []gitlabDraft
	if err := json.Unmarshal([]byte(`[{"id":53,"note":"Fine.","position":null},{"id":40,"note":"About a.go","position":{"position_type":"file","new_path":"a.go"}}]`), &ds2); err != nil {
		t.Fatal(err)
	}
	addGitlabDrafts(&file, ds2)
	if file.Summary != "Fine." {
		t.Errorf("summary %q", file.Summary)
	}
	// A reply to a comment not on a line says whom it answers.
	if r := pr.Comments[1].ReplyTo; r != "tim" {
		t.Errorf("reply to %q", r)
	}
}

// kijk asks a GitLab server its version once while it runs, and asks again after a failure.
func TestGitlabVersion(t *testing.T) {
	gitlabVersions.Clear()
	t.Cleanup(gitlabVersions.Clear)
	gittest.FakeTool(t, "glab", gittest.Rule{Err: "connection refused\n", Exit: 1})
	if v := gitlabVersion("gitlab.acme.dev"); v != "" {
		t.Errorf("version %q after a failure", v)
	}
	args := gittest.FakeTool(t, "glab", gittest.Rule{Has: "version", Out: `{"version":"17.4.1-ee","revision":"abc"}`})
	if v := gitlabVersion("gitlab.acme.dev"); v != "17.4.1-ee" {
		t.Errorf("version %q", v)
	}
	if a := strings.Join(args(), " "); a != "api --hostname gitlab.acme.dev version" {
		t.Errorf("glab was called with %s", a)
	}
	gittest.FakeTool(t, "glab", gittest.Rule{Err: "should not be asked\n", Exit: 1})
	if v := gitlabVersion("gitlab.acme.dev"); v != "17.4.1-ee" {
		t.Errorf("version %q the second time", v)
	}
}

func TestWriteRules(t *testing.T) {
	for _, c := range []struct {
		name string
		got  Write
		want Write
	}{
		{"teammate on GitHub", githubWrite(&PR{PRRow: PRRow{Author: "sanne"}, Me: "you"}), Write{OK: true, Approve: true, Changes: true}},
		{"own on GitHub", githubWrite(&PR{PRRow: PRRow{Author: "you"}, Me: "you"}),
			Write{OK: true, Why: "You cannot approve or request changes on your own pull request"}},
		{"GitLab 19.6", gitlabWrite("19.6.0-pre"), Write{OK: true, Approve: true, Changes: true}},
		{"GitLab 18.9", gitlabWrite("18.9.0"), Write{OK: true, Approve: true, Changes: true}},
		{"GitLab 17.4", gitlabWrite("17.4.1-ee"), Write{OK: true, Approve: true, Why: "Request changes needs GitLab 18.9. This server runs 17.4."}},
		{"GitLab 16.3", gitlabWrite("16.3.0"), Write{OK: true, Approve: true, Why: "Request changes needs GitLab 18.9. This server runs 16.3."}},
		{"GitLab 15.11", gitlabWrite("15.11.13"), Write{Note: "Reviews from kijk need GitLab 16.3. This server runs 15.11."}},
		{"GitLab unknown", gitlabWrite(""), Write{OK: true, Approve: true, Why: "Request changes needs GitLab 18.9. kijk cannot read this server's version."}},
	} {
		if c.got != c.want {
			t.Errorf("%s: %+v, want %+v", c.name, c.got, c.want)
		}
	}
}

func TestViewGitlabPR(t *testing.T) {
	gittest.Isolate(t)
	repo := gittest.Repo(t)
	gittest.Run(t, repo, "remote", "add", "origin", "https://gitlab.com/acme/shop.git")
	mr, err := os.ReadFile(filepath.Join("testdata", "forge", "gitlab-pr.json"))
	if err != nil {
		t.Fatal(err)
	}
	drafts, err := os.ReadFile(filepath.Join("testdata", "forge", "gitlab-drafts.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		version, drafts string
		want            int
		write           Write
	}{
		{"17.4.1-ee", string(drafts), 4, Write{OK: true, Approve: true, Why: "Request changes needs GitLab 18.9. This server runs 17.4."}},
		{"15.11.0", "", 0, Write{Note: "Reviews from kijk need GitLab 16.3. This server runs 15.11."}},
		{"18.9.0", "", 0, Write{Note: "Cannot read your drafts: glab: 403 Forbidden"}},
	} {
		gitlabVersions.Clear()
		rules := []gittest.Rule{{Has: "mergeRequest(iid", Out: string(mr)}, {Has: "gitlab.com version", Out: `{"version":"` + c.version + `"}`}}
		if c.drafts != "" {
			rules = append(rules, gittest.Rule{Has: "acme%2Fshop/merge_requests/12/draft_notes", Out: c.drafts})
		} else {
			rules = append(rules, gittest.Rule{Has: "draft_notes", Err: "403 Forbidden\n", Exit: 1})
		}
		gittest.FakeTool(t, "glab", rules...)
		pr, _, err := ViewPR(repo, 12)
		if err != nil {
			t.Fatal(err)
		}
		if pr.Drafts != c.want || pr.Write != c.write {
			t.Errorf("GitLab %s: drafts %d, write %+v", c.version, pr.Drafts, pr.Write)
		}
	}
	gitlabVersions.Clear()
}
