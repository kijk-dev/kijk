package git

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// GitLab holds a review as draft notes, which one publish sends together. Its nested JSON goes on
// stdin: as a form field, position[new_line] would post a comment that is not on a line.

var (
	// conflictRe is how glab says the status 409, as "409 Conflict" or "(HTTP 409)".
	conflictRe         = regexp.MustCompile(`(^|\D)409 Conflict|HTTP 409\b`)
	gitlabNoteRe       = regexp.MustCompile(`^[0-9]+$`)
	gitlabDiscussionRe = regexp.MustCompile(`^[0-9a-f]+$`)
)

// gitlabREST calls GitLab's REST API; a body goes as JSON.
func gitlabREST(f *forge, method, path string, body any) ([]byte, error) {
	args := []string{"api", "--hostname", f.Host}
	if method != "GET" {
		args = append(args, "--method", method)
	}
	args = append(args, path)
	var in []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		in = b
		args = append(args, "-H", "Content-Type: application/json", "--input", "-")
	}
	return runToolIn(in, "glab", args...)
}

// gitlabCanWrite refuses a write on a server that is too old for draft notes on a line.
func gitlabCanWrite(f *forge) (Write, error) {
	w := gitlabWrite(gitlabVersion(f.Host))
	if !w.OK {
		return w, sayErr(w.Note)
	}
	return w, nil
}

// discussion is the REST id of a GitLab thread.
func discussion(thread string) (string, error) {
	id := strings.TrimPrefix(thread, "gid://gitlab/Discussion/")
	if !gitlabDiscussionRe.MatchString(id) {
		return "", badInput("bad thread id")
	}
	return id, nil
}

func gitlabComment(f *forge, n int, head string, c NewComment) (string, error) {
	if _, err := gitlabCanWrite(f); err != nil {
		return "", err
	}
	body := map[string]any{"note": c.Body}
	if c.Thread != "" {
		d, err := discussion(c.Thread)
		if err != nil {
			return "", err
		}
		body["in_reply_to_discussion_id"] = d
	} else {
		var v struct {
			Project *struct {
				MergeRequest *struct {
					DiffRefs *struct{ BaseSha, HeadSha, StartSha string }
				}
			}
		}
		err := mutate("glab", f, `query($path: ID!, $iid: String!) { project(fullPath: $path) { mergeRequest(iid: $iid) { diffRefs { baseSha headSha startSha } } } }`,
			map[string]any{"path": f.Path, "iid": strconv.Itoa(n)}, &v)
		if err != nil {
			return "", err
		}
		if v.Project == nil || v.Project.MergeRequest == nil || v.Project.MergeRequest.DiffRefs == nil {
			return "", errors.New("merge request not found")
		}
		refs := v.Project.MergeRequest.DiffRefs
		if refs.HeadSha != head {
			return "", headMoved(f, n)
		}
		pos := map[string]any{"position_type": "text", "base_sha": refs.BaseSha, "start_sha": refs.StartSha, "head_sha": refs.HeadSha,
			"new_path": c.Path, "old_path": cmp.Or(c.Orig, c.Path)}
		// GitLab places an unchanged row by both its lines, an added one by its new line, and a
		// removed one by its old line.
		switch {
		case c.Old:
			pos["old_line"] = c.Line
		case c.OldLine > 0:
			pos["new_line"], pos["old_line"] = c.Line, c.OldLine
		default:
			pos["new_line"] = c.Line
		}
		body["position"] = pos
	}
	out, err := gitlabREST(f, "POST", gitlabMR(f, n)+"/draft_notes", body)
	if err != nil {
		return "", err
	}
	var v struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return "", err
	}
	return strconv.Itoa(v.ID), nil
}

func gitlabEdit(f *forge, n int, id, body string) error {
	if !gitlabNoteRe.MatchString(id) {
		return badInput("bad draft id")
	}
	if _, err := gitlabCanWrite(f); err != nil {
		return err
	}
	_, err := gitlabREST(f, "PUT", gitlabMR(f, n)+"/draft_notes/"+id, map[string]any{"note": body})
	return err
}

func gitlabDelete(f *forge, n int, id string) error {
	if !gitlabNoteRe.MatchString(id) {
		return badInput("bad draft id")
	}
	if _, err := gitlabCanWrite(f); err != nil {
		return err
	}
	_, err := gitlabREST(f, "DELETE", gitlabMR(f, n)+"/draft_notes/"+id, nil)
	return err
}

func gitlabResolve(f *forge, n int, thread string, resolved bool) error {
	d, err := discussion(thread)
	if err != nil {
		return err
	}
	if _, err := gitlabCanWrite(f); err != nil {
		return err
	}
	_, err = gitlabREST(f, "PUT", gitlabMR(f, n)+"/discussions/"+d+"?resolved="+strconv.FormatBool(resolved), nil)
	return err
}

// gitlabSummary sets the summary, the draft summaryDraft finds; an empty body deletes it.
func gitlabSummary(f *forge, n int, body string) error {
	if _, err := gitlabCanWrite(f); err != nil {
		return err
	}
	mr := gitlabMR(f, n)
	out, err := gitlabREST(f, "GET", mr+"/draft_notes", nil)
	if err != nil {
		return err
	}
	var ds []gitlabDraft
	if err := json.Unmarshal(out, &ds); err != nil {
		return err
	}
	id := summaryDraft(ds)
	switch {
	case id != 0 && body == "":
		_, err = gitlabREST(f, "DELETE", mr+"/draft_notes/"+strconv.Itoa(id), nil)
	case id != 0:
		_, err = gitlabREST(f, "PUT", mr+"/draft_notes/"+strconv.Itoa(id), map[string]any{"note": body})
	case body != "":
		_, err = gitlabREST(f, "POST", mr+"/draft_notes", map[string]any{"note": body})
	}
	return err
}

// gitlabSubmit publishes every draft of yours, the summary and those written on the website too,
// then approves or requests changes. A later step that fails says what went out.
func gitlabSubmit(f *forge, n int, head, verdict string) error {
	w, err := gitlabCanWrite(f)
	if err != nil {
		return err
	}
	if verdict == "changes" && !w.Changes {
		return sayErr(w.Why)
	}
	mr := gitlabMR(f, n)
	out, err := gitlabREST(f, "GET", mr+"/draft_notes", nil)
	if err != nil {
		return err
	}
	var drafts []struct{}
	if err := json.Unmarshal(out, &drafts); err != nil {
		return err
	}
	if len(drafts) > 0 {
		if _, err := gitlabREST(f, "POST", mr+"/draft_notes/bulk_publish", nil); err != nil {
			return err
		}
	}
	what := ""
	switch verdict {
	case "approve":
		what = "the approval"
		// With sha GitLab approves only the head kijk showed, and answers 409 when it moved.
		var sha any
		if head != "" {
			sha = map[string]string{"sha": head}
		}
		_, err = gitlabREST(f, "POST", mr+"/approve", sha)
		if err != nil && conflictRe.MatchString(err.Error()) {
			err = sayErr(fmt.Sprintf("!%d has new commits. Look at them first.", n))
		}
	case "changes":
		what = "Request changes"
		var v struct {
			MergeRequestRequestChanges struct{ Errors []string }
		}
		err = mutate("glab", f, `mutation($path: ID!, $iid: String!) { mergeRequestRequestChanges(input: {projectPath: $path, iid: $iid}) { errors } }`,
			map[string]any{"path": f.Path, "iid": strconv.Itoa(n)}, &v)
		if err == nil && len(v.MergeRequestRequestChanges.Errors) > 0 {
			err = errors.New(v.MergeRequestRequestChanges.Errors[0])
		}
	}
	if err != nil && len(drafts) > 0 {
		return sayErr("Your comments are posted, but " + what + " failed: " + toolErr("glab", err).Error())
	}
	return err
}
