package git

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kijk-dev/kijk/internal/gittest"
)

const mr7 = "projects/acme%2Fshop/merge_requests/7"

func glVersion(v string) gittest.Rule {
	return gittest.Rule{Has: "gitlab.com version", Out: `{"version":"` + v + `"}`}
}

var glRefs = gittest.Rule{Has: "diffRefs { baseSha", Out: `{"data":{"project":{"mergeRequest":{"diffRefs":{"baseSha":"b0","headSha":"h1","startSha":"s0"}}}}}`}

// body reads the JSON a call sent on stdin.
func body(t *testing.T, c gittest.Call) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal([]byte(c.Stdin), &v); err != nil {
		t.Fatalf("stdin %q: %v", c.Stdin, err)
	}
	return v
}

func TestGitlabComment(t *testing.T) {
	repo := forgeRepo(t, "https://gitlab.com/acme/shop.git")
	gitlabVersions.Clear()
	t.Cleanup(gitlabVersions.Clear)
	calls := gittest.FakeToolCalls(t, "glab", glVersion("17.4.1"), glRefs,
		gittest.Rule{Has: mr7 + "/draft_notes", Out: `{"id":61,"note":"x"}`})
	for _, c := range []struct {
		name string
		in   NewComment
		want string
	}{
		{"added", NewComment{Path: "retry.go", Line: 36, Body: "Add the type."},
			`{"note":"Add the type.","position":{"base_sha":"b0","head_sha":"h1","new_line":36,"new_path":"retry.go","old_path":"retry.go","position_type":"text","start_sha":"s0"}}`},
		{"unchanged", NewComment{Path: "retry.go", Line: 40, OldLine: 38, Body: "Hm."},
			`{"note":"Hm.","position":{"base_sha":"b0","head_sha":"h1","new_line":40,"new_path":"retry.go","old_line":38,"old_path":"retry.go","position_type":"text","start_sha":"s0"}}`},
		{"removed in a renamed file", NewComment{Path: "new.go", Orig: "old.go", Line: 3, Old: true, Body: "Why?"},
			`{"note":"Why?","position":{"base_sha":"b0","head_sha":"h1","new_path":"new.go","old_line":3,"old_path":"old.go","position_type":"text","start_sha":"s0"}}`},
		{"reply", NewComment{Thread: "gid://gitlab/Discussion/ab12", Body: "Yes."}, `{"in_reply_to_discussion_id":"ab12","note":"Yes."}`},
	} {
		n := len(calls())
		id, err := AddComment(repo, 7, "h1", c.in)
		if err != nil || id != "61" {
			t.Fatalf("%s: id %q, err %v", c.name, id, err)
		}
		last := calls()[len(calls())-1]
		if !has(last, "--method", "POST", mr7+"/draft_notes", "--input", "-", "-H", "Content-Type: application/json") {
			t.Errorf("%s: call %q", c.name, last.Args)
		}
		if b, _ := json.Marshal(body(t, last)); string(b) != c.want {
			t.Errorf("%s: sent\n%s\nwant\n%s", c.name, b, c.want)
		}
		// A reply needs no head check; a comment on a line does.
		checked := false
		for _, call := range calls()[n:] {
			checked = checked || strings.Contains(strings.Join(call.Args, " "), "diffRefs")
		}
		if checked == (c.in.Thread != "") {
			t.Errorf("%s: head checked %v", c.name, checked)
		}
	}
	_, err := AddComment(repo, 7, "h0", NewComment{Path: "retry.go", Line: 36, Body: "x"})
	if err == nil || err.Error() != "!7 has new commits. kijk shows them now; add your comment there." {
		t.Errorf("head moved: %v", err)
	}
}

func TestGitlabEditDeleteResolve(t *testing.T) {
	repo := forgeRepo(t, "https://gitlab.com/acme/shop.git")
	gitlabVersions.Clear()
	t.Cleanup(gitlabVersions.Clear)
	calls := gittest.FakeToolCalls(t, "glab", glVersion("18.9.0"), gittest.Rule{Has: mr7, Out: `{}`})
	if err := EditComment(repo, 7, "61", "Better."); err != nil {
		t.Fatal(err)
	}
	if err := DeleteComment(repo, 7, "61"); err != nil {
		t.Fatal(err)
	}
	if err := Resolve(repo, 7, "gid://gitlab/Discussion/ab12", true); err != nil {
		t.Fatal(err)
	}
	if err := Resolve(repo, 7, "gid://gitlab/Discussion/ab12", false); err != nil {
		t.Fatal(err)
	}
	c := calls()[1:] // after the version
	if len(c) != 4 || !has(c[0], "--method", "PUT", mr7+"/draft_notes/61") || body(t, c[0])["note"] != "Better." ||
		!has(c[1], "--method", "DELETE", mr7+"/draft_notes/61") ||
		!has(c[2], "--method", "PUT", mr7+"/discussions/ab12?resolved=true") || !has(c[3], mr7+"/discussions/ab12?resolved=false") {
		t.Fatalf("calls %q", c)
	}
	// A GitLab draft id is a number, and a discussion id hex: they go into the URL.
	if err := DeleteComment(repo, 7, "61/../../x"); err == nil {
		t.Error("a draft id that is not a number")
	}
	if err := Resolve(repo, 7, "gid://gitlab/Discussion/ab?x=1", true); err == nil {
		t.Error("a discussion id that is not hex")
	}
}

func TestGitlabSubmit(t *testing.T) {
	repo := forgeRepo(t, "https://gitlab.com/acme/shop.git")
	gitlabVersions.Clear()
	t.Cleanup(gitlabVersions.Clear)
	drafts := gittest.Rule{Has: "gitlab.com " + mr7 + "/draft_notes", Out: `[{"id":61},{"id":62}]`} // the GET has no --method
	publish := gittest.Rule{Has: mr7 + "/draft_notes/bulk_publish", Out: ``}
	summary := gittest.Rule{Has: mr7 + "/draft_notes", Out: `{"id":63}`}
	approve := gittest.Rule{Has: mr7 + "/approve", Out: `{}`}
	changes := gittest.Rule{Has: "mergeRequestRequestChanges(input", Out: `{"data":{"mergeRequestRequestChanges":{"errors":[]}}}`}

	// The summary goes as one more draft, then everything is published, then the approval.
	calls := gittest.FakeToolCalls(t, "glab", glVersion("19.6.0"), publish, approve, changes, drafts, summary)
	id, err := SubmitReview(repo, 7, "h1", "approve", "Looks good.")
	if err != nil || id != "63" {
		t.Fatalf("id %q, err %v", id, err)
	}
	c := calls()[1:]
	if len(c) != 4 || !has(c[0], "--method", "POST", mr7+"/draft_notes") || body(t, c[0])["note"] != "Looks good." ||
		!has(c[1], mr7+"/draft_notes") || has(c[1], "--method") || !has(c[2], "--method", "POST", mr7+"/draft_notes/bulk_publish") ||
		!has(c[3], "--method", "POST", mr7+"/approve") || body(t, c[3])["sha"] != "h1" {
		t.Fatalf("calls %q", c)
	}

	// The summary went out even when GitLab's answer cannot be read, so it is reported as sent.
	gittest.FakeToolCalls(t, "glab", publish, gittest.Rule{Has: "gitlab.com " + mr7 + "/draft_notes", Out: `[{"id":63}]`}, gittest.Rule{Has: mr7 + "/draft_notes", Out: `<html>`})
	if id, err := SubmitReview(repo, 7, "h1", "comment", "Fine."); err != nil || id == "" {
		t.Fatalf("id %q, err %v", id, err)
	}

	// Request changes with nothing to publish: no publish call.
	calls = gittest.FakeToolCalls(t, "glab", publish, approve, changes, gittest.Rule{Has: mr7 + "/draft_notes", Out: `[]`})
	if _, err := SubmitReview(repo, 7, "h1", "changes", ""); err != nil {
		t.Fatal(err)
	}
	if c := calls(); len(c) != 2 || !has(c[1], "-f", "iid=7", "path=acme/shop") {
		t.Fatalf("calls %q", c)
	}

	// The approval fails after the publish: kijk says what went out.
	gittest.FakeToolCalls(t, "glab", publish, gittest.Rule{Has: "/approve", Err: "403 Forbidden\n", Exit: 1}, drafts, summary)
	_, err = SubmitReview(repo, 7, "h1", "approve", "")
	if err == nil || err.Error() != "Your comments are posted, but the approval failed: glab: 403 Forbidden" {
		t.Fatalf("err %v", err)
	}
	// The approval is for the head kijk showed; GitLab refuses it when that moved.
	gittest.FakeToolCalls(t, "glab", publish, gittest.Rule{Has: "/approve", Err: "409 Conflict\n", Exit: 1}, drafts)
	_, err = SubmitReview(repo, 7, "h1", "approve", "")
	if err == nil || err.Error() != "Your comments are posted, but the approval failed: !7 has new commits. Look at them first." {
		t.Fatalf("err %v", err)
	}
	// GitLab answers a refused Request changes with errors in the data.
	gittest.FakeToolCalls(t, "glab", gittest.Rule{Has: "mergeRequestRequestChanges", Out: `{"data":{"mergeRequestRequestChanges":{"errors":["Not a reviewer"]}}}`}, gittest.Rule{Has: mr7 + "/draft_notes", Out: `[]`})
	if _, err := SubmitReview(repo, 7, "h1", "changes", ""); err == nil || err.Error() != "glab: Not a reviewer" {
		t.Fatalf("err %v", err)
	}
}

// Below 16.3 kijk writes nothing, and below 18.9 it does not request changes.
func TestGitlabOldServer(t *testing.T) {
	repo := forgeRepo(t, "https://gitlab.com/acme/shop.git")
	gitlabVersions.Clear()
	t.Cleanup(gitlabVersions.Clear)
	calls := gittest.FakeToolCalls(t, "glab", glVersion("15.11.2"), gittest.Rule{Out: `{}`})
	if _, err := AddComment(repo, 7, "h1", NewComment{Thread: "ab", Body: "x"}); err == nil || err.Error() != "Reviews from kijk need GitLab 16.3. This server runs 15.11." {
		t.Errorf("err %v", err)
	}
	if err := Resolve(repo, 7, "ab", true); err == nil || len(calls()) != 1 {
		t.Errorf("resolve on 15.11: %v, calls %q", err, calls())
	}
	gitlabVersions.Clear()
	calls = gittest.FakeToolCalls(t, "glab", glVersion("17.4.0"), gittest.Rule{Out: `[]`})
	if _, err := SubmitReview(repo, 7, "h1", "changes", ""); err == nil || err.Error() != "Request changes needs GitLab 18.9. This server runs 17.4." || len(calls()) != 1 {
		t.Errorf("request changes on 17.4: %v, calls %q", err, calls())
	}
}
