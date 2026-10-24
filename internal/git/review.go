package git

import (
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
	return fmt.Errorf("%s %w", f.Ref(n), ErrHeadMoved)
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
		err = githubEdit(f, id, body)
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
		err = githubDelete(f, id)
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
		err = githubResolve(f, thread, resolved)
	} else {
		err = gitlabResolve(f, n, thread, resolved)
	}
	return toolErr(tool, err)
}

// Verdicts are the verdicts of a review, by the names the page sends.
var Verdicts = map[string]string{"comment": "COMMENT", "approve": "APPROVE", "changes": "REQUEST_CHANGES"}

// SetSummary sets the summary of your review, a draft on the forge like the rest. head is the
// head kijk showed, for a review that does not exist yet; an empty body clears the summary.
func SetSummary(root string, n int, head, body string) error {
	f, tool, err := writer(root)
	if err != nil {
		return err
	}
	if tool == "gh" {
		err = githubSummary(f, n, head, strings.TrimSpace(body))
	} else {
		err = gitlabSummary(f, n, strings.TrimSpace(body))
	}
	return toolErr(tool, err)
}

// SubmitReview sends every draft, the summary and the verdict, for head, the head kijk showed.
func SubmitReview(root string, n int, head, verdict string) error {
	if Verdicts[verdict] == "" {
		return badInput("unknown verdict " + verdict)
	}
	f, tool, err := writer(root)
	if err != nil {
		return err
	}
	if tool == "gh" {
		err = githubSubmit(f, n, head, verdict)
	} else {
		err = gitlabSubmit(f, n, head, verdict)
	}
	return toolErr(tool, err)
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
