package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kijk-dev/kijk/internal/git"
)

// fakeForge is a folder with a gh and a glab that answer from the files in it, picked by the
// question they get, and log every call. A question with no file fails, as a tool would; with
// gh-error.txt, gh fails every call with its text. A write m (such as gh-thread.json for "thread")
// first copies the files in after-m/ over the answers, once, so the next read shows what it did.
type fakeForge struct{ dir string }

func newFakeForge(t *testing.T) *fakeForge {
	t.Helper()
	f := &fakeForge{dir: t.TempDir()}
	gh := `#!/bin/sh
d=%[1]q
echo "home=$HOME gh $*" | tr '\n' ' ' >> "$d/calls.log"; echo >> "$d/calls.log"
if [ -f "$d/gh-error.txt" ]; then cat "$d/gh-error.txt" >&2; exit 1; fi
f=
m=
if [ "$1" = pr ]; then
  [ -f "$d/gh-pr-view.json" ] || { echo "no pull requests found for branch" >&2; exit 1; }
  f=gh-pr-view.json
fi
case "$*" in
*"headRefOid reviews(states: PENDING"*) f=gh-target.json ;;
*"addPullRequestReviewThreadReply("*) m=reply ;;
*"addPullRequestReviewThread("*) m=thread ;;
*"addPullRequestReview("*) m=review ;;
*"submitPullRequestReview("*) m=submit ;;
*"updatePullRequestReviewComment("*) m=edit ;;
*"updatePullRequestReview("*) m=update ;;
*"deletePullRequestReviewComment("*) m=delete ;;
*"unresolveReviewThread("*) m=unresolve ;;
*"resolveReviewThread("*) m=resolve ;;
*"pullRequests(states"*) f=gh-list.json ;;
*"search(query"*) f=gh-search.json ;;
*"pullRequest(number"*) for a; do case "$a" in n=*) f="gh-pr-${a#n=}.json" ;; esac; done ;;
*/actions/jobs/*) for a; do case "$a" in */actions/jobs/*) id=${a#*/actions/jobs/}; f="gh-log-${id%%/logs}.txt" ;; esac; done ;;
esac
if [ -n "$m" ]; then
  f="gh-$m.json"
  if [ -d "$d/after-$m" ]; then cp "$d/after-$m"/* "$d/" && rm -r "$d/after-$m"; fi
fi
if [ -n "$f" ] && [ -f "$d/$f" ]; then cat "$d/$f"; else echo "no answer for: $*" >&2; exit 1; fi
`
	glab := `#!/bin/sh
d=%[1]q
echo "home=$HOME glab $*" | tr '\n' ' ' >> "$d/calls.log"; echo >> "$d/calls.log"
f=
m=
host=
if [ "$1" = mr ]; then f=glab-mr-view.json; fi
prev=
for a; do [ "$prev" = --hostname ] && host=$a; prev=$a; done
case "$*" in
*"mergeRequests(state"*) f=glab-list.json ;;
*"mergeRequest(iid"*) for a; do case "$a" in iid=*) f="glab-pr-${a#iid=}.json" ;; esac; done ;;
*"mergeRequestRequestChanges("*) m=changes ;;
*" version") f="glab-version-$host.json" ;;
*/draft_notes/bulk_publish*) m=publish ;;
*"--method POST "*/approve) m=approve ;;
*"--method POST "*/draft_notes*) m=draft ;;
*/draft_notes) f=glab-drafts.json ;;
esac
if [ -n "$m" ]; then
  f="glab-$m.json"
  if [ -d "$d/after-$m" ]; then cp "$d/after-$m"/* "$d/" && rm -r "$d/after-$m"; fi
fi
if [ -n "$f" ] && [ -f "$d/$f" ]; then cat "$d/$f"; else echo '{}'; fi
`
	for name, script := range map[string]string{"gh": gh, "glab": glab} {
		if err := os.WriteFile(filepath.Join(f.dir, name), []byte(fmt.Sprintf(script, f.dir)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

// env puts the fake tools first on the PATH of a kijk.
func (f *fakeForge) env() string {
	return "PATH=" + f.dir + string(os.PathListSeparator) + os.Getenv("PATH")
}

func (f *fakeForge) set(t *testing.T, name string, answer any) {
	t.Helper()
	writeAnswer(t, filepath.Join(f.dir, name), answer)
}

// after makes the answer in name change to answer once the write m came, such as "thread".
func (f *fakeForge) after(t *testing.T, m, name string, answer any) {
	t.Helper()
	dir := filepath.Join(f.dir, "after-"+m)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeAnswer(t, filepath.Join(dir, name), answer)
}

func writeAnswer(t *testing.T, path string, answer any) {
	t.Helper()
	b, ok := answer.([]byte)
	if s, isText := answer.(string); isText {
		b, ok = []byte(s), true
	}
	if !ok {
		var err error
		if b, err = json.Marshal(answer); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// waitFor waits until a call after the first n holds text.
func (f *fakeForge) waitFor(t *testing.T, n int, text string) {
	t.Helper()
	for end := time.Now().Add(10 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		if calls := f.calls(); len(calls) > n && strings.Contains(strings.Join(calls[n:], "\n"), text) {
			return
		}
	}
	t.Fatalf("no call with %q after call %d:\n%s", text, n, strings.Join(f.calls(), "\n"))
}

// settle waits until no new call came for half a second, so a call still running is done.
func (f *fakeForge) settle() int {
	n := len(f.calls())
	for end := time.Now().Add(10 * time.Second); time.Now().Before(end); {
		time.Sleep(500 * time.Millisecond)
		m := len(f.calls())
		if m == n {
			break
		}
		n = m
	}
	return n
}

// calls are the tool calls so far, one line each.
func (f *fakeForge) calls() []string {
	b, _ := os.ReadFile(filepath.Join(f.dir, "calls.log"))
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

type obj = map[string]any

func ghData(v any) obj { return obj{"data": v} }

func ghUser(name string) obj { return obj{"login": name} }

// ghRow is a row of the PR list.
func ghRow(n int, title, author, head string, draft bool, add, del int, ci string) obj {
	var rollup any
	if ci != "" {
		rollup = obj{"state": ci}
	}
	return obj{"number": n, "title": title, "isDraft": draft, "url": fmt.Sprintf("https://github.com/acme/review/pull/%d", n),
		"createdAt": "2026-10-19T09:00:00Z", "headRefName": head, "baseRefName": "main", "additions": add, "deletions": del,
		"author": ghUser(author), "commits": obj{"nodes": []obj{{"commit": obj{"statusCheckRollup": rollup}}}}}
}

func ghComment(author, body string) obj {
	return obj{"author": ghUser(author), "body": body, "createdAt": "2026-10-20T08:00:00Z", "url": "https://github.com/acme/review/pull/1", "state": "SUBMITTED", "diffHunk": ""}
}

func ghThread(id, path string, line any, orig int, resolved, outdated bool, hunk string, cs ...obj) obj {
	if hunk != "" {
		cs[0]["diffHunk"] = hunk
	}
	return obj{"id": id, "isResolved": resolved, "isOutdated": outdated, "path": path, "line": line, "originalLine": orig, "diffSide": "RIGHT", "comments": obj{"nodes": cs},
		"viewerCanReply": true, "viewerCanResolve": !resolved, "viewerCanUnresolve": resolved}
}

// ghDraft is a comment of your pending review.
func ghDraft(id, body string) obj {
	c := ghComment("you", body)
	c["id"], c["state"] = id, "PENDING"
	return c
}

func ghCheck(id int, name, status, conclusion string, run int, file string) obj {
	suite := obj{"app": obj{"slug": "vercel", "name": "Vercel"}, "workflowRun": nil}
	if run != 0 {
		suite = obj{"app": obj{"slug": "github-actions"}, "workflowRun": obj{"databaseId": run, "runNumber": run % 1000, "url": "https://github.com/acme/review/actions/runs/1",
			"file": obj{"path": file}, "workflow": obj{"name": "CI"}}}
	}
	return obj{"__typename": "CheckRun", "databaseId": id, "name": name, "status": status, "conclusion": conclusion,
		"startedAt": "2026-10-21T10:00:00Z", "completedAt": "2026-10-21T10:02:05Z", "detailsUrl": "https://github.com/acme/review/actions/runs/1/job/1", "checkSuite": suite}
}

// ghPR is the answer about one pull request.
func ghPR(row obj, body, head, base string, reviews, requests, comments, threads, checks []obj) obj {
	pr := obj{}
	for k, v := range row {
		pr[k] = v
	}
	state := "OPEN"
	pr["state"], pr["body"], pr["headRefOid"], pr["baseRefOid"] = state, body, head, base
	pr["commits"] = obj{"totalCount": 2}
	pr["latestReviews"] = obj{"nodes": reviews}
	pr["reviewRequests"] = obj{"nodes": requests}
	pr["reviews"] = obj{"nodes": []obj{}}
	pr["comments"] = obj{"nodes": comments}
	pr["reviewThreads"] = obj{"nodes": threads}
	ci := "SUCCESS"
	for _, c := range checks {
		if c["conclusion"] == "FAILURE" {
			ci = "FAILURE"
		}
	}
	pr["head"] = obj{"nodes": []obj{{"commit": obj{"oid": head, "statusCheckRollup": obj{"state": ci, "contexts": obj{"nodes": checks}}}}}}
	return ghData(obj{"viewer": ghUser("you"), "repository": obj{"pullRequest": pr}})
}

// retryGo is the file the teammate's pull request #477 adds.
const retryGo = `package webhook

import (
	"context"
	"time"
)

// deliver posts an event, and tries again when the post fails.
func (s *Sender) deliver(ctx context.Context, ev Event) error {
	var err error
	for attempt := 0; attempt < s.cfg.MaxAttempts; attempt++ {
		if err = s.post(ctx, ev); err == nil {
			return nil
		}
		if !retryable(err) {
			return err
		}
		select {
		case <-time.After(backoff(attempt)):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return err
}
`

const ciYml = `name: CI
on: [pull_request]
jobs:
  build:
    runs-on: ubuntu-latest
  test:
    needs: build
    strategy:
      matrix:
        os: [linux, windows]
`

// teammatePR pushes sanne's branch retry-webhooks to origin as pull request #477, from main of
// repo, and gives its head.
func teammatePR(t *testing.T, repo, origin string) string {
	t.Helper()
	work := filepath.Join(t.TempDir(), "sanne")
	run(t, t.TempDir(), "clone", "-q", origin, work)
	run(t, work, "checkout", "-qb", "retry-webhooks", "origin/main")
	write(t, filepath.Join(work, "internal", "webhook", "retry.go"), retryGo)
	write(t, filepath.Join(work, ".github", "workflows", "ci.yml"), ciYml)
	readme, _ := os.ReadFile(filepath.Join(work, "README.md"))
	write(t, filepath.Join(work, "README.md"), string(readme)+"\nWebhooks retry five times.\n")
	run(t, work, "add", "-A")
	run(t, work, "commit", "-qm", "Retry webhook delivery")
	run(t, work, "push", "-q", "origin", "HEAD:refs/pull/477/head")
	return gitOut(t, work, "rev-parse", "HEAD")
}

// reviewForge answers for the review repo: its list, the agent's own #482 and sanne's #477.
// ownThread puts sanne's question on #482.
func reviewForge(t *testing.T, f *fakeForge, repo, origin string, ownThread bool) {
	t.Helper()
	own := gitOut(t, repo, "rev-parse", "origin/fix-login")
	main := gitOut(t, repo, "rev-parse", "origin/main")
	if gitOut(t, origin, "rev-parse", "fix-login") != own {
		t.Fatal("origin/fix-login is not on origin")
	}
	run(t, repo, "push", "-q", "origin", own+":refs/pull/482/head")
	theirs := teammatePR(t, repo, origin)
	f.set(t, "gh-pr-view.json", `{"number":482,"title":"Fix login redirect loop","state":"OPEN","isDraft":false,"url":"https://github.com/acme/review/pull/482","baseRefName":"main","reviewDecision":"","statusCheckRollup":[],"latestReviews":[]}`)
	f.set(t, "glab-mr-view.json", `{"iid":12,"title":"Fix login redirect loop","state":"opened","draft":false,"web_url":"https://gitlab.com/acme/review/-/merge_requests/12","target_branch":"main"}`)
	r482 := ghRow(482, "Fix login redirect loop", "you", "fix-login", false, 60, 1, "SUCCESS")
	r477 := ghRow(477, "Retry webhook delivery with backoff", "sanne", "retry-webhooks", false, 37, 0, "FAILURE")
	f.set(t, "gh-list.json", ghData(obj{"viewer": ghUser("you"),
		"repository": obj{"pullRequests": obj{"totalCount": 5, "nodes": []obj{
			r482, r477,
			ghRow(471, "Move rate limits into config", "tim", "rate-config", false, 88, 61, "SUCCESS"),
			ghRow(463, "Drop the v1 export endpoint", "priya", "drop-v1", true, 12, 406, "PENDING"),
			ghRow(455, "Audit log for admin actions", "you", "audit-log", false, 241, 9, ""),
		}}},
		"asked": obj{"nodes": []obj{{"number": 477}, {"number": 471}}}}))

	session := sessionGo("sess, ok := s.lookup(c.Value)", "")
	var threads []obj
	if ownThread {
		threads = []obj{ghThread("S1", "internal/auth/session.go", lineOf(session, "\tsess, ok := s.lookup(c.Value)"), 0, false, false, "",
			ghComment("sanne", "Does lookup also check the expiry?"))}
	}
	f.set(t, "gh-pr-482.json", ghPR(r482, "After an SSO login, an expired session looped.", own, main,
		[]obj{{"author": ghUser("sanne"), "state": "COMMENTED"}}, nil, nil, threads,
		[]obj{ghCheck(401, "build", "COMPLETED", "SUCCESS", 2001, ".github/workflows/none.yml"), ghCheck(402, "test (linux)", "COMPLETED", "FAILURE", 2001, ".github/workflows/none.yml")}))
	f.set(t, "gh-log-402.txt", "2026-10-21T10:00:01.0000000Z === RUN   TestResume\n2026-10-21T10:00:02.0000000Z     session_test.go:12: got nil session\n2026-10-21T10:00:03.0000000Z --- FAIL: TestResume (0.00s)\n")

	f.set(t, "gh-pr-477.json", pr477(theirs, main, func(ts []obj) []obj { return ts }))
	f.set(t, "gh-log-303.txt", "2026-10-21T10:00:01.0000000Z === RUN   TestBackoffJitter\n2026-10-21T10:00:02.0000000Z     backoff_test.go:41: delay 1.204s outside [0.5s, 1.2s]\n2026-10-21T10:00:03.0000000Z --- FAIL: TestBackoffJitter (0.00s)\n")
}

// pr477 is gh's answer about sanne's #477; edit changes its threads.
func pr477(theirs, main string, edit func([]obj) []obj) obj {
	r477 := ghRow(477, "Retry webhook delivery with backoff", "sanne", "retry-webhooks", false, 37, 0, "FAILURE")
	return ghPR(r477, "Webhook delivery gave up after **one** failed POST.\n\n- Retry up to five times.", theirs, main,
		[]obj{{"author": ghUser("priya"), "state": "APPROVED"}, {"author": ghUser("tim"), "state": "COMMENTED"}},
		[]obj{{"requestedReviewer": obj{"__typename": "User", "login": "you"}}},
		[]obj{ghComment("tim", "Do we want the attempt number in the delivery log?")},
		edit([]obj{
			ghThread("T1", "internal/webhook/retry.go", lineOf(retryGo, "\t\tif !retryable(err) {"), 0, false, false, "",
				ghComment("tim", "Should a 429 count as retryable?"), ghComment("sanne", "Good point, I'll add it."), ghComment("priya", "Agreed.")),
			ghThread("T2", "internal/webhook/retry.go", lineOf(retryGo, "\t\tselect {"), 0, true, false, "",
				ghComment("priya", "Check ctx before the first sleep?"), ghComment("sanne", "Done."), ghComment("priya", "Thanks.")),
			ghThread("T3", "internal/webhook/retry.go", nil, 20, false, true, "@@ -0,0 +1,20 @@\n+\ttime.Sleep(time.Second)",
				ghComment("tim", "Name this delay.")),
			ghThread("T6", "internal/webhook/retry.go", nil, 21, true, true, "@@ -0,0 +1,21 @@\n+\tlog.Print(err)",
				ghComment("priya", "Log less here."), ghComment("sanne", "Removed.")),
		}),
		[]obj{
			ghCheck(301, "build", "COMPLETED", "SUCCESS", 1284, ".github/workflows/ci.yml"),
			ghCheck(302, "test (linux)", "COMPLETED", "SUCCESS", 1284, ".github/workflows/ci.yml"),
			ghCheck(303, "test (windows)", "COMPLETED", "FAILURE", 1284, ".github/workflows/ci.yml"),
			ghCheck(304, "Vercel – Preview", "COMPLETED", "SUCCESS", 0, ""),
			ghCheck(305, "Docs preview", "COMPLETED", "CANCELLED", 0, ""),
		})
}

// gitOut runs git in dir and gives its output.
func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := git.Run(dir, args...)
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return out
}
