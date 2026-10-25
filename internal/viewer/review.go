package viewer

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/kijk-dev/kijk/internal/git"
)

// The writes of a review. Each runs on the folder's machine, with its gh or glab.

const maxCommentBytes = 64 << 10

type writeRequest struct {
	Root string `json:"root"`
	N    int    `json:"n"`
	At   string `json:"at"` // the head on screen, sent with a write that depends on it
	git.NewComment
	ID       string `json:"id"`
	Resolved bool   `json:"resolved"`
	Verdict  string `json:"verdict"`
	merge    bool   // set by the route, not the page
}

type writeRun func(req writeRequest, p openedPR) (any, error)

// write reads a write's request and checks its folder, then runs it. A refused request is 400, a
// head that moved 409, no diff kept 428, and a forge that failed 502 with the tool's words.
func (a *api) write(run writeRun) http.HandlerFunc { return a.writeAs(false, run) }

// merge is write for Squash and merge, which kijk refuses by what the forge said about merging.
func (a *api) merge(run writeRun) http.HandlerFunc { return a.writeAs(true, run) }

func (a *api) writeAs(merge bool, run writeRun) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		req := writeRequest{merge: merge}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxCommentBytes+4<<10)).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if !a.roots.Has(req.Root) {
			http.Error(w, "This folder is not open in kijk", http.StatusForbidden)
			return
		}
		if req.N <= 0 || len(req.Body) > maxCommentBytes {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		p, known := a.opened.get(req.Root, req.N)
		if why := refused(p, known, req); why != "" {
			http.Error(w, why, http.StatusConflict)
			return
		}
		out, err := run(req, p)
		switch {
		case errors.Is(err, git.ErrBadInput):
			http.Error(w, err.Error(), http.StatusBadRequest)
		case errors.Is(err, git.ErrHeadMoved):
			http.Error(w, err.Error(), http.StatusConflict)
		case errors.Is(err, errNotShown):
			http.Error(w, err.Error(), http.StatusPreconditionRequired)
		case err != nil:
			http.Error(w, err.Error(), http.StatusBadGateway)
		default:
			reply(w, out)
		}
	}
}

// refused says why kijk does not ask the forge: the forge would refuse the write, by what it last
// said kijk may write or merge, or another page loaded a newer head than the one on screen.
func refused(p openedPR, known bool, req writeRequest) string {
	can, moved := p.write, git.ErrHeadMoved
	if req.merge {
		moved = git.ErrMergeMoved
	}
	switch {
	case !known:
		return ""
	case req.merge && !p.merge.OK:
		return cmp.Or(p.merge.Why, "kijk cannot merge this pull request")
	case !req.merge && !can.OK:
		return cmp.Or(can.Note, "kijk cannot write to this pull request")
	case req.Verdict == "approve" && !can.Approve, req.Verdict == "changes" && !can.Changes:
		return can.Why
	case req.At != "" && p.stale != "":
		return p.stale
	case req.At != "" && p.head != "" && req.At != p.head:
		return p.ref + " " + moved.Error()
	}
	return ""
}

// errNotShown: kijk has no diff of the pull request to put a comment on, as after a restart.
var errNotShown = errors.New("Open the pull request again")

// prComment needs the head on screen for a reply too, as the reply may start your pending review.
func prComment(req writeRequest, p openedPR) (any, error) {
	head, err := shownHead(req, p)
	if err != nil {
		return nil, err
	}
	id, err := git.AddComment(req.Root, req.N, head, req.NewComment)
	return map[string]string{"id": id}, err
}

func prEdit(req writeRequest, _ openedPR) (any, error) {
	return struct{}{}, git.EditComment(req.Root, req.N, req.ID, req.Body)
}

func prDelete(req writeRequest, _ openedPR) (any, error) {
	return struct{}{}, git.DeleteComment(req.Root, req.N, req.ID)
}

func prResolve(req writeRequest, _ openedPR) (any, error) {
	return struct{}{}, git.Resolve(req.Root, req.N, req.Thread, req.Resolved)
}

// prSummary sets the summary of your review on the forge.
func prSummary(req writeRequest, p openedPR) (any, error) {
	head, err := shownHead(req, p) // a new pending review would start on it
	if err != nil {
		return nil, err
	}
	return struct{}{}, git.SetSummary(req.Root, req.N, head, req.Body)
}

func prSubmit(req writeRequest, p openedPR) (any, error) {
	// A verdict is for the head on screen, so kijk must know which one that is.
	if req.Verdict != "comment" {
		if _, err := shownHead(req, p); err != nil {
			return nil, err
		}
	}
	return struct{}{}, git.SubmitReview(req.Root, req.N, p.head, req.Verdict)
}

// prMerge squashes and merges the pull request, only at the head on screen.
func prMerge(req writeRequest, p openedPR) (any, error) {
	head, err := shownHead(req, p)
	if err != nil {
		return nil, err
	}
	return struct{}{}, git.SquashMerge(req.Root, req.N, head)
}

// shownHead is the head a write goes to. The page must say it shows it: else the write would go to
// the last head any page loaded.
func shownHead(req writeRequest, p openedPR) (string, error) {
	if p.head == "" {
		return "", errNotShown
	}
	if req.At == "" {
		return "", fmt.Errorf("%w: the write does not say which head it is for", git.ErrBadInput)
	}
	return p.head, nil
}
