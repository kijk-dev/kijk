package git

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// The writes of a review. Text goes into your review as a draft on the forge, and only
// SubmitReview sends it. Resolve and unresolve happen at once.

// NewComment is a draft for your review: a comment on a line of the diff, or a reply in a thread.
type NewComment struct {
	Thread  string `json:"thread"` // a reply in this thread; the rest is then unused
	Path    string `json:"path"`
	Orig    string `json:"orig"`    // the old path of a renamed file
	Line    int    `json:"line"`    // the new line, or with Old the old line of a removed row
	Old     bool   `json:"old"`     // on a removed row
	OldLine int    `json:"oldLine"` // the old line of an unchanged row, which GitLab needs
	Body    string `json:"body"`
}

// ErrHeadMoved: the pull request has commits kijk did not show, so a line may be another line now.
var ErrHeadMoved = errors.New("has new commits. kijk shows them now; add your comment there.")

// ErrBadInput is a write the page asked for that kijk refuses before any call.
var ErrBadInput = errors.New("bad request")

func badInput(what string) error { return fmt.Errorf("%w: %s", ErrBadInput, what) }

// idRe is what ids from the forge look like; anything else is refused before a call.
var idRe = regexp.MustCompile(`^[A-Za-z0-9_=+/:.-]{1,200}$`)

func checkID(id string) error {
	if !idRe.MatchString(id) || strings.HasPrefix(id, "-") {
		return badInput("bad id")
	}
	return nil
}

// writer is the forge of a folder and its tool, for a write.
func writer(root string) (*forge, string, error) {
	f, tool, note := forgeTool(root)
	if f == nil {
		return nil, "", errors.New("origin is not on GitHub or GitLab")
	}
	if note != "" {
		return nil, "", errors.New(note)
	}
	return f, tool, nil
}

// sayErr is an error kijk words itself, passed on as it is.
type sayErr string

func (e sayErr) Error() string { return string(e) }

// toolErr says a failed gh or glab call as the PR line does, with a missing login named.
func toolErr(tool string, err error) error {
	var said sayErr
	if err == nil || errors.Is(err, ErrHeadMoved) || errors.As(err, &said) {
		return err
	}
	if note := toolNote(tool, err); note != "" {
		return errors.New(note)
	}
	return err
}

func headMoved(f *forge, n int) error {
	sign := "#"
	if f.Kind == "gitlab" {
		sign = "!"
	}
	return fmt.Errorf("%s%d %w", sign, n, ErrHeadMoved)
}

// AddComment adds a draft to your review and gives its id. A comment on a line is refused when
// the pull request's head is no longer head, the one kijk showed.
func AddComment(root string, n int, head string, c NewComment) (string, error) {
	if strings.TrimSpace(c.Body) == "" {
		return "", badInput("the comment is empty")
	}
	if c.Thread != "" {
		if err := checkID(c.Thread); err != nil {
			return "", err
		}
	} else if c.Path == "" || c.Line <= 0 || c.OldLine < 0 {
		return "", badInput("no line to comment on")
	}
	f, tool, err := writer(root)
	if err != nil {
		return "", err
	}
	var id string
	if tool == "gh" {
		id, err = githubComment(f, n, head, c)
	} else {
		id, err = gitlabComment(f, n, head, c)
	}
	return id, toolErr(tool, err)
}

// EditComment changes the text of one of your drafts.
func EditComment(root string, n int, id, body string) error {
	if err := checkID(id); err != nil {
		return err
	}
	if strings.TrimSpace(body) == "" {
		return badInput("the comment is empty")
	}
	f, tool, err := writer(root)
	if err != nil {
		return err
	}
	if tool == "gh" {
		err = mutate("gh", f, `mutation($id: ID!, $body: String!) { updatePullRequestReviewComment(input: {pullRequestReviewCommentId: $id, body: $body}) { pullRequestReviewComment { id } } }`,
			map[string]any{"id": id, "body": body}, nil)
	} else {
		err = gitlabEdit(f, n, id, body)
	}
	return toolErr(tool, err)
}

// DeleteComment deletes one of your drafts.
func DeleteComment(root string, n int, id string) error {
	if err := checkID(id); err != nil {
		return err
	}
	f, tool, err := writer(root)
	if err != nil {
		return err
	}
	if tool == "gh" {
		err = mutate("gh", f, `mutation($id: ID!) { deletePullRequestReviewComment(input: {id: $id}) { clientMutationId } }`, map[string]any{"id": id}, nil)
	} else {
		err = gitlabDelete(f, n, id)
	}
	return toolErr(tool, err)
}

// Resolve resolves or unresolves a thread, at once.
func Resolve(root string, n int, thread string, resolved bool) error {
	if err := checkID(thread); err != nil {
		return err
	}
	f, tool, err := writer(root)
	if err != nil {
		return err
	}
	if tool == "gh" {
		op := "resolveReviewThread"
		if !resolved {
			op = "unresolveReviewThread"
		}
		err = mutate("gh", f, `mutation($thread: ID!) { `+op+`(input: {threadId: $thread}) { thread { id } } }`, map[string]any{"thread": thread}, nil)
	} else {
		err = gitlabResolve(f, n, thread, resolved)
	}
	return toolErr(tool, err)
}

// Verdicts are the verdicts of a review, by the names the page sends.
var Verdicts = map[string]string{"comment": "COMMENT", "approve": "APPROVE", "changes": "REQUEST_CHANGES"}

// SubmitReview sends every draft, the verdict and the summary, for head, the head kijk showed. On
// GitLab it gives the summary's draft id: the summary is on the forge, also when a later step failed.
func SubmitReview(root string, n int, head, verdict, body string) (summary string, err error) {
	if Verdicts[verdict] == "" {
		return "", badInput("unknown verdict " + verdict)
	}
	f, tool, err := writer(root)
	if err != nil {
		return "", err
	}
	if tool == "gh" {
		err = githubSubmit(f, n, head, verdict, body)
	} else {
		summary, err = gitlabSubmit(f, n, head, verdict, body)
	}
	return summary, toolErr(tool, err)
}

// mutate runs a GraphQL mutation. Unlike a query, any error fails it: a refused mutation answers
// data that holds only nulls.
func mutate(tool string, f *forge, query string, vars map[string]any, out any) error {
	data, errs, err := callGraphQL(tool, f, query, vars)
	if err != nil {
		return err
	}
	if len(errs) > 0 {
		return errors.New(errs[0])
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
}

// ----- GitHub: everything with text goes through your pending review -----

// githubTarget is what a write on GitHub needs: the pull request's node, its head, and your
// pending review, asked fresh so a review changed on the website is never lost or sent twice.
type githubTarget struct{ id, head, review, reviewAt string }

const githubTargetQuery = `query($owner: String!, $name: String!, $n: Int!) {
  repository(owner: $owner, name: $name) { pullRequest(number: $n) { id headRefOid reviews(states: PENDING, first: 1) { nodes { id viewerDidAuthor commit { oid } } } } }
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
				t.review = r.ID
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
		return "", sayErr(fmt.Sprintf("Your pending review on #%d is on an older commit. Post it first, or finish it on GitHub.", n))
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

func githubSubmit(f *forge, n int, head, verdict, body string) error {
	t, err := githubTargetOf(f, n)
	if err != nil {
		return err
	}
	event := Verdicts[verdict]
	if t.review != "" {
		return mutate("gh", f, `mutation($review: ID!, $event: PullRequestReviewEvent!, $body: String!) { submitPullRequestReview(input: {pullRequestReviewId: $review, event: $event, body: $body}) { pullRequestReview { id } } }`,
			map[string]any{"review": t.review, "event": event, "body": body}, nil)
	}
	return mutate("gh", f, `mutation($pr: ID!, $commit: GitObjectID!, $event: PullRequestReviewEvent!, $body: String!) { addPullRequestReview(input: {pullRequestId: $pr, commitOID: $commit, event: $event, body: $body}) { pullRequestReview { id } } }`,
		map[string]any{"pr": t.id, "commit": cmp.Or(head, t.head), "event": event, "body": body}, nil)
}
