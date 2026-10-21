package viewer

import (
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

// errNotShown: kijk has no diff of the pull request to put a comment on, as after a restart.
var errNotShown = errors.New("Open the pull request again")

func (a *api) prComment(req writeRequest) (any, error) {
	head := ""
	if req.Thread == "" {
		r, ok := a.ranges.get(req.Root, req.N)
		if !ok {
			return nil, errNotShown
		}
		head = r[1]
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

type submitReply struct {
	Summary string `json:"summary,omitempty"` // the summary's draft on GitLab
	Error   string `json:"error,omitempty"`
}

// prSubmit answers a failure after the summary went to the forge with both, so the page does not
// send the summary twice.
func (a *api) prSubmit(req writeRequest) (any, error) {
	r, _ := a.ranges.get(req.Root, req.N)
	summary, err := git.SubmitReview(req.Root, req.N, r[1], req.Verdict, req.Body)
	if err != nil && summary != "" {
		return submitReply{summary, err.Error()}, nil
	}
	return submitReply{Summary: summary}, err
}
