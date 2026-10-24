package git

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/kijk-dev/kijk/internal/gittest"
)

func forgeRepo(t *testing.T, remote string) string {
	t.Helper()
	gittest.Isolate(t)
	repo := gittest.Repo(t)
	gittest.Run(t, repo, "remote", "add", "origin", remote)
	return repo
}

// ghTarget is gh's answer about pull request 7: its node, its head, and a pending review when
// review is set.
func ghTarget(head, review string) gittest.Rule { return ghTargetAt(head, review, head, "") }

// ghTargetAt is ghTarget with the pending review made on commit, with body as its summary.
func ghTargetAt(head, review, commit, body string) gittest.Rule {
	nodes := "[]"
	if review != "" {
		nodes = fmt.Sprintf(`[{"id":%q,"viewerDidAuthor":true,"commit":{"oid":%q},"body":%q}]`, review, commit, body)
	}
	return gittest.Rule{Has: "headRefOid reviews(states: PENDING", Out: fmt.Sprintf(`{"data":{"repository":{"pullRequest":{"id":"PR_7","headRefOid":%q,"reviews":{"nodes":%s}}}}}`, head, nodes)}
}

// has says whether a call holds each of the arguments, in any order.
func has(c gittest.Call, args ...string) bool {
	all := "\x00" + strings.Join(c.Args, "\x00") + "\x00"
	for _, a := range args {
		if !strings.Contains(all, "\x00"+a+"\x00") {
			return false
		}
	}
	return true
}

func TestGithubComment(t *testing.T) {
	repo := forgeRepo(t, "git@github.com:acme/shop.git")
	thread := gittest.Rule{Has: "addPullRequestReviewThread(input", Out: `{"data":{"addPullRequestReviewThread":{"thread":{"id":"T9","comments":{"nodes":[{"id":"C9"}]}}}}}`}
	review := gittest.Rule{Has: "addPullRequestReview(input", Out: `{"data":{"addPullRequestReview":{"pullRequestReview":{"id":"PRR_new"}}}}`}

	// No pending review yet: kijk makes one at the head it showed, then adds the thread to it.
	calls := gittest.FakeToolCalls(t, "gh", thread, review, ghTarget("aaa", ""))
	id, err := AddComment(repo, 7, "aaa", NewComment{Path: "retry.go", Line: 36, Body: "Add the event type."})
	if err != nil || id != "C9" {
		t.Fatalf("id %q, err %v", id, err)
	}
	c := calls()
	if len(c) != 3 || !has(c[0], "-F", "n=7", "-f", "owner=acme", "name=shop") ||
		!has(c[1], "pr=PR_7", "commit=aaa") || strings.Contains(strings.Join(c[1].Args, " "), "event") ||
		!has(c[2], "review=PRR_new", "path=retry.go", "line=36", "side=RIGHT", "body=Add the event type.") {
		t.Fatalf("calls %q", c)
	}

	// With a pending review, a comment on a removed row goes into it, on the left side.
	calls = gittest.FakeToolCalls(t, "gh", thread, ghTarget("aaa", "PRR_9"))
	if _, err := AddComment(repo, 7, "aaa", NewComment{Path: "legacy.go", Line: 3, Old: true, Body: "Why?"}); err != nil {
		t.Fatal(err)
	}
	if c := calls(); len(c) != 2 || !has(c[1], "review=PRR_9", "line=3", "side=LEFT") {
		t.Fatalf("calls %q", c)
	}

	// Your pending review is on an older commit, whose lines may be other lines: nothing is written.
	calls = gittest.FakeToolCalls(t, "gh", thread, ghTargetAt("aaa", "PRR_9", "old", ""))
	_, err = AddComment(repo, 7, "aaa", NewComment{Path: "retry.go", Line: 36, Body: "x"})
	if err == nil || err.Error() != "Your pending review on #7 is on an older commit. Post it first, or finish it on GitHub." || len(calls()) != 1 {
		t.Fatalf("err %v, calls %q", err, calls())
	}

	// Someone pushed since kijk showed the diff: nothing is written.
	calls = gittest.FakeToolCalls(t, "gh", thread, review, ghTarget("bbb", ""))
	_, err = AddComment(repo, 7, "aaa", NewComment{Path: "retry.go", Line: 36, Body: "x"})
	if !errors.Is(err, ErrHeadMoved) || err.Error() != "#7 has new commits. kijk shows them now; add your comment there." || len(calls()) != 1 {
		t.Fatalf("err %v, calls %q", err, calls())
	}
}

// Windows caps a command line at 32,767 characters, and a comment may hold 64 KiB: its text goes to
// gh on stdin.
func TestGithubLongComment(t *testing.T) {
	repo := forgeRepo(t, "git@github.com:acme/shop.git")
	calls := gittest.FakeToolCalls(t, "gh",
		gittest.Rule{Has: "addPullRequestReviewThread(input", Out: `{"data":{"addPullRequestReviewThread":{"thread":{"id":"T9","comments":{"nodes":[{"id":"C9"}]}}}}}`},
		ghTarget("aaa", "PRR_9"))
	body := strings.Repeat("x", 64<<10)
	if _, err := AddComment(repo, 7, "aaa", NewComment{Path: "retry.go", Line: 36, Body: body}); err != nil {
		t.Fatal(err)
	}
	c := calls()[1]
	if n := len(strings.Join(c.Args, " ")); n > 1000 || !strings.Contains(c.Stdin, body) {
		t.Fatalf("command line of %d characters, stdin of %d", n, len(c.Stdin))
	}
}

func TestGithubReply(t *testing.T) {
	repo := forgeRepo(t, "git@github.com:acme/shop.git")
	calls := gittest.FakeToolCalls(t, "gh",
		gittest.Rule{Has: "addPullRequestReviewThreadReply(input", Out: `{"data":{"addPullRequestReviewThreadReply":{"comment":{"id":"C10"}}}}`},
		ghTarget("bbb", "PRR_9"))
	// A reply is not on a line, so a new head does not matter; it goes into the pending review, or
	// GitHub would post it at once.
	id, err := AddComment(repo, 7, "aaa", NewComment{Thread: "PRRT_1", Body: "Cap it at MaxDelay."})
	if err != nil || id != "C10" {
		t.Fatalf("id %q, err %v", id, err)
	}
	if c := calls(); len(c) != 2 || !has(c[1], "review=PRR_9", "thread=PRRT_1", "body=Cap it at MaxDelay.") {
		t.Fatalf("calls %q", c)
	}

	// A reply with no pending review starts one, at the head on screen, so not when that moved.
	reply := gittest.Rule{Has: "addPullRequestReviewThreadReply(input", Out: `{"data":{"addPullRequestReviewThreadReply":{"comment":{"id":"C10"}}}}`}
	review := gittest.Rule{Has: "addPullRequestReview(input", Out: `{"data":{"addPullRequestReview":{"pullRequestReview":{"id":"PRR_new"}}}}`}
	calls = gittest.FakeToolCalls(t, "gh", reply, review, ghTarget("bbb", ""))
	if _, err := AddComment(repo, 7, "aaa", NewComment{Thread: "PRRT_1", Body: "Cap it."}); !errors.Is(err, ErrHeadMoved) || len(calls()) != 1 {
		t.Fatalf("head moved: %v, calls %q", err, calls())
	}
	calls = gittest.FakeToolCalls(t, "gh", reply, review, ghTarget("aaa", ""))
	if _, err := AddComment(repo, 7, "aaa", NewComment{Thread: "PRRT_1", Body: "Cap it."}); err != nil {
		t.Fatal(err)
	}
	if c := calls(); len(c) != 3 || !has(c[1], "pr=PR_7", "commit=aaa") || !has(c[2], "review=PRR_new", "thread=PRRT_1") {
		t.Fatalf("calls %q", c)
	}
}

func TestGithubEditDeleteResolve(t *testing.T) {
	repo := forgeRepo(t, "git@github.com:acme/shop.git")
	calls := gittest.FakeToolCalls(t, "gh",
		gittest.Rule{Has: "updatePullRequestReviewComment(input", Out: `{"data":{"updatePullRequestReviewComment":{"pullRequestReviewComment":{"id":"C9"}}}}`},
		gittest.Rule{Has: "deletePullRequestReviewComment(input", Out: `{"data":{"deletePullRequestReviewComment":{"clientMutationId":null}}}`},
		gittest.Rule{Has: "unresolveReviewThread(input", Out: `{"data":{"unresolveReviewThread":{"thread":{"id":"T1"}}}}`},
		gittest.Rule{Has: "resolveReviewThread(input", Out: `{"data":{"resolveReviewThread":{"thread":{"id":"T1"}}}}`})
	if err := EditComment(repo, 7, "C9", "Better words."); err != nil {
		t.Fatal(err)
	}
	if err := DeleteComment(repo, 7, "C9"); err != nil {
		t.Fatal(err)
	}
	if err := Resolve(repo, 7, "T1", true); err != nil {
		t.Fatal(err)
	}
	if err := Resolve(repo, 7, "T1", false); err != nil {
		t.Fatal(err)
	}
	c := calls()
	q := func(i int) string { return strings.Join(c[i].Args, " ") }
	if len(c) != 4 || !has(c[0], "id=C9", "body=Better words.") || !has(c[1], "id=C9") ||
		!strings.Contains(q(2), " resolveReviewThread(") || !has(c[2], "thread=T1") || !strings.Contains(q(3), "unresolveReviewThread(") {
		t.Fatalf("calls %q", c)
	}
	// An id kijk did not get from the forge is refused before any call.
	if err := DeleteComment(repo, 7, "C9 --hostname evil"); err == nil || len(calls()) != 4 {
		t.Fatalf("a bad id: %v", err)
	}
}

func TestGithubSubmit(t *testing.T) {
	repo := forgeRepo(t, "git@github.com:acme/shop.git")
	submit := gittest.Rule{Has: "submitPullRequestReview(input", Out: `{"data":{"submitPullRequestReview":{"pullRequestReview":{"id":"PRR_9"}}}}`}
	add := gittest.Rule{Has: "addPullRequestReview(input", Out: `{"data":{"addPullRequestReview":{"pullRequestReview":{"id":"PRR_new"}}}}`}

	// The summary is the body of the pending review on GitHub, sent again so it is not lost.
	calls := gittest.FakeToolCalls(t, "gh", submit, add, ghTargetAt("aaa", "PRR_9", "aaa", "Looks good."))
	if err := SubmitReview(repo, 7, "aaa", "approve"); err != nil {
		t.Fatal(err)
	}
	if c := calls(); len(c) != 2 || !has(c[1], "review=PRR_9", "event=APPROVE", "body=Looks good.") {
		t.Fatalf("calls %q", c)
	}

	// No drafts: one call makes the review and submits it, on the head kijk showed.
	calls = gittest.FakeToolCalls(t, "gh", submit, add, ghTarget("bbb", ""))
	if err := SubmitReview(repo, 7, "aaa", "changes"); err != nil {
		t.Fatal(err)
	}
	if c := calls(); len(c) != 2 || !has(c[1], "pr=PR_7", "commit=aaa", "event=REQUEST_CHANGES", "body=") {
		t.Fatalf("calls %q", c)
	}

	// GitHub records a submitted review on its own commit: a verdict through a pending review on
	// another commit than the one on screen is refused.
	calls = gittest.FakeToolCalls(t, "gh", submit, ghTargetAt("bbb", "PRR_9", "old", ""))
	err := SubmitReview(repo, 7, "bbb", "approve")
	if err == nil || err.Error() != "Your pending review on #7 is on an older commit. Post it first, or finish it on GitHub." || len(calls()) != 1 {
		t.Fatalf("approve through an old review: %v, calls %q", err, calls())
	}
	calls = gittest.FakeToolCalls(t, "gh", submit, ghTargetAt("bbb", "PRR_9", "old", ""))
	if err := SubmitReview(repo, 7, "bbb", "comment"); err != nil || len(calls()) != 2 {
		t.Fatalf("comment through an old review: %v", err)
	}

	calls = gittest.FakeToolCalls(t, "gh", submit, ghTarget("aaa", "PRR_9"))
	if err := SubmitReview(repo, 7, "aaa", "comment"); err != nil {
		t.Fatal(err)
	}
	if c := calls(); !has(c[1], "event=COMMENT", "body=") {
		t.Fatalf("calls %q", c)
	}
	if err := SubmitReview(repo, 7, "aaa", "merge"); err == nil || len(calls()) != 2 {
		t.Fatalf("a verdict kijk does not know: %v", err)
	}
}

// A mutation that GitHub refuses answers data with nulls and an error; kijk says the error.
func TestGithubWriteRefused(t *testing.T) {
	repo := forgeRepo(t, "git@github.com:acme/shop.git")
	gittest.FakeToolCalls(t, "gh",
		gittest.Rule{Has: "addPullRequestReviewThread(input", Out: `{"data":{"addPullRequestReviewThread":null},"errors":[{"message":"pull_request_review_thread.line must be part of the diff"}]}`},
		ghTarget("aaa", "PRR_9"))
	_, err := AddComment(repo, 7, "aaa", NewComment{Path: "retry.go", Line: 900, Body: "x"})
	if err == nil || err.Error() != "gh: pull_request_review_thread.line must be part of the diff" {
		t.Fatalf("err %v", err)
	}
}

// The summary is the body of your pending review: kijk makes the review with it, or changes it.
func TestGithubSummary(t *testing.T) {
	repo := forgeRepo(t, "git@github.com:acme/shop.git")
	add := gittest.Rule{Has: "addPullRequestReview(input", Out: `{"data":{"addPullRequestReview":{"pullRequestReview":{"id":"PRR_new"}}}}`}
	update := gittest.Rule{Has: "updatePullRequestReview(input", Out: `{"data":{"updatePullRequestReview":{"pullRequestReview":{"id":"PRR_9"}}}}`}
	// A new pending review starts at the head on screen, so it is refused when that moved.
	calls := gittest.FakeToolCalls(t, "gh", add, update, ghTarget("bbb", ""))
	if err := SetSummary(repo, 7, "aaa", "Looks good."); !errors.Is(err, ErrHeadMoved) || len(calls()) != 1 {
		t.Fatalf("head moved: %v, calls %q", err, calls())
	}
	calls = gittest.FakeToolCalls(t, "gh", add, update, ghTarget("aaa", ""))
	if err := SetSummary(repo, 7, "aaa", "Looks good."); err != nil {
		t.Fatal(err)
	}
	if c := calls(); len(c) != 2 || !has(c[1], "pr=PR_7", "commit=aaa", "body=Looks good.") || strings.Contains(strings.Join(c[1].Args, " "), "event") {
		t.Fatalf("calls %q", c)
	}
	calls = gittest.FakeToolCalls(t, "gh", add, update, ghTarget("aaa", "PRR_9"))
	if err := SetSummary(repo, 7, "aaa", "Looks good, one question."); err != nil {
		t.Fatal(err)
	}
	if c := calls(); len(c) != 2 || !has(c[1], "review=PRR_9", "body=Looks good, one question.") {
		t.Fatalf("calls %q", c)
	}
	// No summary and no review: nothing to write.
	calls = gittest.FakeToolCalls(t, "gh", add, update, ghTarget("aaa", ""))
	if err := SetSummary(repo, 7, "aaa", "  "); err != nil || len(calls()) != 1 {
		t.Fatalf("err %v, calls %q", err, calls())
	}
}
