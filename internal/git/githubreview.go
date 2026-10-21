package git

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
)

// GitHub holds a review as your pending review, one per user and pull request: every text goes
// through it, as a reply outside it would post at once.

// githubTarget is what a write on GitHub needs: the pull request's node, its head, and your
// pending review, asked fresh so a review changed on the website is never lost or sent twice.
type githubTarget struct{ id, head, review, reviewAt, summary string }

const githubTargetQuery = `query($owner: String!, $name: String!, $n: Int!) {
  repository(owner: $owner, name: $name) { pullRequest(number: $n) { id headRefOid reviews(states: PENDING, first: 1) { nodes { id viewerDidAuthor body commit { oid } } } } }
}`

func githubTargetOf(f *forge, n int) (githubTarget, error) {
	var t githubTarget
	err := graphQL("gh", f, githubTargetQuery, map[string]any{"owner": owner(f), "name": name(f), "n": n}, &t, func(b []byte, t *githubTarget) error {
		var v struct {
			Repository *struct {
				PullRequest *struct {
					ID, HeadRefOid string
					Reviews        struct {
						Nodes []struct {
							ID              string
							ViewerDidAuthor bool
							Body            string
							Commit          *struct{ Oid string }
						}
					}
				}
			}
		}
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		if v.Repository == nil || v.Repository.PullRequest == nil {
			return errors.New("pull request not found")
		}
		p := v.Repository.PullRequest
		t.id, t.head = p.ID, p.HeadRefOid
		for _, r := range p.Reviews.Nodes {
			if r.ViewerDidAuthor {
				t.review, t.summary = r.ID, r.Body
				if r.Commit != nil {
					t.reviewAt = r.Commit.Oid
				}
			}
		}
		return nil
	})
	return t, err
}

// pending is your pending review, made at commit when there is none yet.
func (t *githubTarget) pending(f *forge, commit string) (string, error) {
	if t.review != "" {
		return t.review, nil
	}
	var v struct {
		AddPullRequestReview struct{ PullRequestReview struct{ ID string } }
	}
	err := mutate("gh", f, `mutation($pr: ID!, $commit: GitObjectID!) { addPullRequestReview(input: {pullRequestId: $pr, commitOID: $commit}) { pullRequestReview { id } } }`,
		map[string]any{"pr": t.id, "commit": commit}, &v)
	t.review = v.AddPullRequestReview.PullRequestReview.ID
	if err == nil && t.review == "" {
		err = errors.New("GitHub made no pending review")
	}
	return t.review, err
}

func githubComment(f *forge, n int, head string, c NewComment) (string, error) {
	t, err := githubTargetOf(f, n)
	if err != nil {
		return "", err
	}
	if c.Thread == "" && t.head != head {
		return "", headMoved(f, n)
	}
	// GitHub places a new thread by the commit of the review it goes into.
	if c.Thread == "" && t.review != "" && t.reviewAt != head {
		return "", olderReview(n)
	}
	review, err := t.pending(f, cmp.Or(head, t.head))
	if err != nil {
		return "", err
	}
	if c.Thread != "" {
		var v struct {
			AddPullRequestReviewThreadReply struct{ Comment struct{ ID string } }
		}
		err := mutate("gh", f, `mutation($review: ID!, $thread: ID!, $body: String!) { addPullRequestReviewThreadReply(input: {pullRequestReviewId: $review, pullRequestReviewThreadId: $thread, body: $body}) { comment { id } } }`,
			map[string]any{"review": review, "thread": c.Thread, "body": c.Body}, &v)
		return v.AddPullRequestReviewThreadReply.Comment.ID, err
	}
	side := "RIGHT"
	if c.Old {
		side = "LEFT"
	}
	var v struct {
		AddPullRequestReviewThread struct {
			Thread struct {
				Comments struct{ Nodes []struct{ ID string } }
			}
		}
	}
	err = mutate("gh", f, `mutation($review: ID!, $path: String!, $line: Int!, $side: DiffSide!, $body: String!) { addPullRequestReviewThread(input: {pullRequestReviewId: $review, path: $path, line: $line, side: $side, body: $body}) { thread { id comments(first: 1) { nodes { id } } } } }`,
		map[string]any{"review": review, "path": c.Path, "line": c.Line, "side": side, "body": c.Body}, &v)
	if err != nil {
		return "", err
	}
	if ns := v.AddPullRequestReviewThread.Thread.Comments.Nodes; len(ns) > 0 {
		return ns[0].ID, nil
	}
	return "", nil
}

func olderReview(n int) error {
	return sayErr(fmt.Sprintf("Your pending review on #%d is on an older commit. Post it first, or finish it on GitHub.", n))
}

// githubSummary sets the body of your pending review, making the review when there is none.
func githubSummary(f *forge, n int, head, body string) error {
	t, err := githubTargetOf(f, n)
	if err != nil {
		return err
	}
	if t.review == "" {
		if body == "" {
			return nil
		}
		if t.head != head {
			return headMoved(f, n) // the new review would start on a head that is not on screen
		}
		return mutate("gh", f, `mutation($pr: ID!, $commit: GitObjectID!, $body: String!) { addPullRequestReview(input: {pullRequestId: $pr, commitOID: $commit, body: $body}) { pullRequestReview { id } } }`,
			map[string]any{"pr": t.id, "commit": cmp.Or(head, t.head), "body": body}, nil)
	}
	return mutate("gh", f, `mutation($review: ID!, $body: String!) { updatePullRequestReview(input: {pullRequestReviewId: $review, body: $body}) { pullRequestReview { id } } }`,
		map[string]any{"review": t.review, "body": body}, nil)
}

// githubSubmit sends the pending review's own body along: the submit's body sets the review's text.
func githubSubmit(f *forge, n int, head, verdict string) error {
	t, err := githubTargetOf(f, n)
	if err != nil {
		return err
	}
	event := Verdicts[verdict]
	// GitHub records a submitted review on its own commit, and a verdict is for the head on screen.
	if t.review != "" && verdict != "comment" && t.reviewAt != head {
		return olderReview(n)
	}
	if t.review != "" {
		return mutate("gh", f, `mutation($review: ID!, $event: PullRequestReviewEvent!, $body: String!) { submitPullRequestReview(input: {pullRequestReviewId: $review, event: $event, body: $body}) { pullRequestReview { id } } }`,
			map[string]any{"review": t.review, "event": event, "body": t.summary}, nil)
	}
	return mutate("gh", f, `mutation($pr: ID!, $commit: GitObjectID!, $event: PullRequestReviewEvent!, $body: String!) { addPullRequestReview(input: {pullRequestId: $pr, commitOID: $commit, event: $event, body: $body}) { pullRequestReview { id } } }`,
		map[string]any{"pr": t.id, "commit": cmp.Or(head, t.head), "event": event, "body": ""}, nil)
}

func githubEdit(f *forge, id, body string) error {
	return mutate("gh", f, `mutation($id: ID!, $body: String!) { updatePullRequestReviewComment(input: {pullRequestReviewCommentId: $id, body: $body}) { pullRequestReviewComment { id } } }`,
		map[string]any{"id": id, "body": body}, nil)
}

func githubDelete(f *forge, id string) error {
	return mutate("gh", f, `mutation($id: ID!) { deletePullRequestReviewComment(input: {id: $id}) { clientMutationId } }`, map[string]any{"id": id}, nil)
}

func githubResolve(f *forge, thread string, resolved bool) error {
	op := "resolveReviewThread"
	if !resolved {
		op = "unresolveReviewThread"
	}
	return mutate("gh", f, `mutation($thread: ID!) { `+op+`(input: {threadId: $thread}) { thread { id } } }`, map[string]any{"thread": thread}, nil)
}
