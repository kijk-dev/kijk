package viewer

import (
	"cmp"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/kijk-dev/kijk/internal/git"
)

// The writes of a review. Each runs on the folder's machine, with its gh or glab.

const maxCommentBytes = 64 << 10

type writeRequest struct {
	Root string `json:"root"`
	N    int    `json:"n"`
	git.NewComment
	ID       string `json:"id"`
	Resolved bool   `json:"resolved"`
	Verdict  string `json:"verdict"`
}

// write reads a write's request and checks its folder, then runs it. A refused request is 400, a
// head that moved 409, no diff kept 428, and a forge that failed 502 with the tool's words.
func (a *api) write(run func(req writeRequest) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req writeRequest
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
		if why := refused(a.opened, req); why != "" {
			http.Error(w, why, http.StatusConflict) // the forge would refuse it, so kijk does not ask
			return
		}
		out, err := run(req)
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

// refused says why the forge would refuse a write, by what it last said kijk may write.
func refused(o *openedPRs, req writeRequest) string {
	p, known := o.get(req.Root, req.N)
	can := p.write
	switch {
	case !known:
		return ""
	case !can.OK:
		return cmp.Or(can.Note, "kijk cannot write to this pull request")
	case req.Verdict == "approve" && !can.Approve, req.Verdict == "changes" && !can.Changes:
		return can.Why
	}
	return ""
}

// errNotShown: kijk has no diff of the pull request to put a comment on, as after a restart.
var errNotShown = errors.New("Open the pull request again")

func (a *api) prComment(req writeRequest) (any, error) {
	head := ""
	if req.Thread == "" {
		p, _ := a.opened.get(req.Root, req.N)
		if p.head == "" {
			return nil, errNotShown
		}
		head = p.head
	}
	id, err := git.AddComment(req.Root, req.N, head, req.NewComment)
	return map[string]string{"id": id}, err
}

func (a *api) prEdit(req writeRequest) (any, error) {
	return struct{}{}, git.EditComment(req.Root, req.N, req.ID, req.Body)
}

func (a *api) prDelete(req writeRequest) (any, error) {
	return struct{}{}, git.DeleteComment(req.Root, req.N, req.ID)
}

func (a *api) prResolve(req writeRequest) (any, error) {
	return struct{}{}, git.Resolve(req.Root, req.N, req.Thread, req.Resolved)
}

// prSummary sets the summary of your review on the forge.
func (a *api) prSummary(req writeRequest) (any, error) {
	p, _ := a.opened.get(req.Root, req.N)
	if p.head == "" {
		return nil, errNotShown // a new pending review would start on a head not on screen
	}
	return struct{}{}, git.SetSummary(req.Root, req.N, p.head, req.Body)
}

func (a *api) prSubmit(req writeRequest) (any, error) {
	p, _ := a.opened.get(req.Root, req.N)
	// A verdict is for the head on screen, so kijk must know which one that is.
	if p.head == "" && req.Verdict != "comment" {
		return nil, errNotShown
	}
	return struct{}{}, git.SubmitReview(req.Root, req.N, p.head, req.Verdict)
}
