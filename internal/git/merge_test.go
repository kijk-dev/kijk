package git

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/kijk-dev/kijk/internal/gittest"
)

func TestMergeRules(t *testing.T) {
	open := &PR{PRRow: PRRow{Base: "main"}, State: "open"}
	draft := &PR{PRRow: PRRow{Base: "main"}, State: "draft"}
	yes, no := true, false
	can := func(on bool) *struct{ CanMerge, RemoveSourceBranch bool } {
		return &struct{ CanMerge, RemoveSourceBranch bool }{CanMerge: on}
	}
	for _, c := range []struct {
		name string
		got  Merge
		want string // why; empty for OK
	}{
		{"GitHub clean", githubMerge(open, githubMergeJSON{ViewerPermission: "WRITE", SquashMergeAllowed: &yes, Mergeable: "MERGEABLE", MergeStateStatus: "CLEAN"}), ""},
		{"GitHub failing checks that are not required", githubMerge(open, githubMergeJSON{Mergeable: "MERGEABLE", MergeStateStatus: "UNSTABLE"}), ""},
		{"GitHub not worked out yet", githubMerge(open, githubMergeJSON{Mergeable: "UNKNOWN", MergeStateStatus: "UNKNOWN"}), ""},
		{"GitHub draft before all else", githubMerge(draft, githubMergeJSON{ViewerPermission: "READ", Mergeable: "CONFLICTING"}), "This pull request is a draft."},
		{"GitHub read only", githubMerge(open, githubMergeJSON{ViewerPermission: "TRIAGE", SquashMergeAllowed: &no}), "You need write access to the repo to merge."},
		{"GitHub app token", githubMerge(open, githubMergeJSON{ViewerPermission: ""}), ""},
		{"GitHub no squash", githubMerge(open, githubMergeJSON{ViewerPermission: "ADMIN", SquashMergeAllowed: &no, Mergeable: "CONFLICTING"}), "This repo does not allow squash merges."},
		{"GitHub conflicts", githubMerge(open, githubMergeJSON{Mergeable: "CONFLICTING", MergeStateStatus: "DIRTY"}), "This pull request has conflicts with main."},
		{"GitHub behind", githubMerge(open, githubMergeJSON{Mergeable: "MERGEABLE", MergeStateStatus: "BEHIND"}), "The branch is behind main. Update it first."},
		{"GitHub blocked", githubMerge(open, githubMergeJSON{Mergeable: "MERGEABLE", MergeStateStatus: "BLOCKED"}), "The rules of main block the merge, such as a required review or check."},

		{"GitLab mergeable", gitlabMerge(open, gitlabMergeJSON{UserPermissions: can(true), SquashOnMerge: true, DetailedMergeStatus: "MERGEABLE"}), ""},
		{"GitLab still checking", gitlabMerge(open, gitlabMergeJSON{DetailedMergeStatus: "CHECKING"}), ""},
		{"GitLab squash always", gitlabMerge(open, gitlabMergeJSON{SquashReadOnly: true, SquashOnMerge: true}), ""},
		{"GitLab draft before all else", gitlabMerge(draft, gitlabMergeJSON{UserPermissions: can(false), Conflicts: true}), "This merge request is a draft."},
		{"GitLab not allowed", gitlabMerge(open, gitlabMergeJSON{UserPermissions: can(false), Conflicts: true}), "You are not allowed to merge into main."},
		{"GitLab squash never", gitlabMerge(open, gitlabMergeJSON{SquashReadOnly: true, Conflicts: true}), "This project does not allow squash merges."},
		{"GitLab conflicts", gitlabMerge(open, gitlabMergeJSON{Conflicts: true, DetailedMergeStatus: "CONFLICT"}), "This merge request has conflicts with main."},
		{"GitLab not approved", gitlabMerge(open, gitlabMergeJSON{DetailedMergeStatus: "NOT_APPROVED"}), "It needs more approvals."},
		{"GitLab pipeline", gitlabMerge(open, gitlabMergeJSON{DetailedMergeStatus: "CI_MUST_PASS"}), "The pipeline must pass first."},
		{"GitLab status kijk does not know", gitlabMerge(open, gitlabMergeJSON{DetailedMergeStatus: "SOMETHING_NEW"}), "GitLab does not allow the merge now."},
	} {
		if want := (Merge{OK: c.want == "", Why: c.want}); c.got != want {
			t.Errorf("%s: %+v, want %+v", c.name, c.got, want)
		}
	}
}

// Delete is what a merge may do to the head branch: the checkbox, the forge's own setting, or nothing.
func TestBranchDelete(t *testing.T) {
	open := &PR{PRRow: PRRow{Base: "main"}, State: "open"}
	ref := func(rule *bool, prs int) *githubHeadRef {
		r := &githubHeadRef{AssociatedPullRequests: struct{ TotalCount int }{prs}}
		if rule != nil {
			r.RefUpdateRule = &struct{ AllowsDeletions bool }{*rule}
		}
		return r
	}
	yes, no := true, false
	branch, protected, rule, twice := ref(nil, 1), ref(&no, 1), ref(&yes, 1), ref(nil, 2)
	may := &struct{ CanMerge, RemoveSourceBranch bool }{true, true}
	mayNot := &struct{ CanMerge, RemoveSourceBranch bool }{true, false}
	for _, c := range []struct {
		name string
		got  Merge
		want string
	}{
		{"GitHub", githubMerge(open, githubMergeJSON{HeadRef: branch}), "off"},
		{"GitHub with a rule that allows deletes", githubMerge(open, githubMergeJSON{HeadRef: rule}), "off"},
		{"GitHub deletes it itself", githubMerge(open, githubMergeJSON{DeleteBranchOnMerge: true, HeadRef: branch}), "auto"},
		{"GitHub would delete it, but for a rule", githubMerge(open, githubMergeJSON{DeleteBranchOnMerge: true, HeadRef: protected}), ""},
		{"GitHub fork", githubMerge(open, githubMergeJSON{IsCrossRepository: true, DeleteBranchOnMerge: true, HeadRef: branch}), ""},
		{"GitHub branch gone", githubMerge(open, githubMergeJSON{}), ""},
		{"GitHub protected branch", githubMerge(open, githubMergeJSON{HeadRef: protected}), ""},
		{"GitHub branch of another open pull request", githubMerge(open, githubMergeJSON{HeadRef: twice}), ""},
		{"GitHub cannot merge", githubMerge(open, githubMergeJSON{Mergeable: "CONFLICTING", HeadRef: branch}), ""},

		{"GitLab", gitlabMerge(open, gitlabMergeJSON{UserPermissions: may, SourceProjectID: 7, TargetProjectID: 7}), "off"},
		{"GitLab deletes it by default", gitlabMerge(open, gitlabMergeJSON{UserPermissions: may, SourceProjectID: 7, TargetProjectID: 7, ForceRemoveSourceBranch: true}), "on"},
		{"GitLab asked to delete it", gitlabMerge(open, gitlabMergeJSON{UserPermissions: may, SourceProjectID: 7, TargetProjectID: 7, ShouldRemoveSourceBranch: true}), "on"},
		{"GitLab fork", gitlabMerge(open, gitlabMergeJSON{UserPermissions: may, SourceProjectID: 8, TargetProjectID: 7, ForceRemoveSourceBranch: true}), ""},
		{"GitLab may not delete", gitlabMerge(open, gitlabMergeJSON{UserPermissions: mayNot, SourceProjectID: 7, TargetProjectID: 7, ForceRemoveSourceBranch: true}), ""},
		{"GitLab cannot merge", gitlabMerge(open, gitlabMergeJSON{UserPermissions: may, SourceProjectID: 7, TargetProjectID: 7, Conflicts: true}), ""},
	} {
		if c.got.Delete != c.want {
			t.Errorf("%s: %q, want %q", c.name, c.got.Delete, c.want)
		}
	}
}

// Each forge's merge rule reads the fields its query asks.
func TestParseMerge(t *testing.T) {
	var gh, gl PR
	if err := parseGithubPRView(recorded(t, "github-pr.json"), &gh); err != nil {
		t.Fatal(err)
	}
	if want := (Merge{Why: "The rules of main block the merge, such as a required review or check."}); gh.Merge != want {
		t.Errorf("GitHub: %+v", gh.Merge)
	}
	if err := parseGitlabPR(recorded(t, "gitlab-pr.json"), &gl); err != nil {
		t.Fatal(err)
	}
	if want := (Merge{Why: "This merge request is a draft."}); gl.Merge != want {
		t.Errorf("GitLab: %+v", gl.Merge)
	}

	// Once they may merge, each says what happens to the branch.
	clean := bytes.Replace(recorded(t, "github-pr.json"), []byte(`"BLOCKED"`), []byte(`"CLEAN"`), 1)
	if err := parseGithubPRView(clean, &gh); err != nil {
		t.Fatal(err)
	}
	if want := (Merge{OK: true, Delete: "off"}); gh.Merge != want {
		t.Errorf("GitHub: %+v", gh.Merge)
	}
	ready := bytes.Replace(recorded(t, "gitlab-pr.json"), []byte(`"draft":true`), []byte(`"draft":false`), 1)
	if err := parseGitlabPR(bytes.Replace(ready, []byte(`"DRAFT_STATUS"`), []byte(`"MERGEABLE"`), 1), &gl); err != nil {
		t.Fatal(err)
	}
	if want := (Merge{OK: true, Delete: "on"}); gl.Merge != want {
		t.Errorf("GitLab: %+v", gl.Merge)
	}
}

func TestGithubSquashMerge(t *testing.T) {
	repo := forgeRepo(t, "git@github.com:acme/shop.git")
	merge := gittest.Rule{Has: "mergePullRequest(input", Out: `{"data":{"mergePullRequest":{"pullRequest":{"headRef":{"id":"REF_1","target":{"oid":"aaa"}}}}}}`}
	calls := gittest.FakeToolCalls(t, "gh", merge, ghTarget("aaa", ""))
	if out, err := SquashMerge(repo, 7, "aaa", nil); err != nil || out != (Merged{}) {
		t.Fatal(out, err)
	}
	c := calls()
	if len(c) != 2 || !c[1].Has("pr=PR_7", "head=aaa") || !strings.Contains(c[1].Stdin, "mergeMethod: SQUASH, expectedHeadOid: $head") {
		t.Fatalf("calls %q", c)
	}

	// Someone pushed since kijk showed the pull request: nothing is merged.
	calls = gittest.FakeToolCalls(t, "gh", merge, ghTarget("bbb", ""))
	_, err := SquashMerge(repo, 7, "aaa", nil)
	if !errors.Is(err, ErrHeadMoved) || err.Error() != "#7 has new commits. kijk shows them now; look at them before you merge." || len(calls()) != 1 {
		t.Fatalf("err %v, calls %q", err, calls())
	}

	// GitHub's own reason, as for a required check kijk did not know about.
	gittest.FakeToolCalls(t, "gh", gittest.Rule{Has: "mergePullRequest(input", Out: `{"data":{"mergePullRequest":null},"errors":[{"message":"Required status check \"build\" is expected."}]}`}, ghTarget("aaa", ""))
	if _, err := SquashMerge(repo, 7, "aaa", nil); err == nil || err.Error() != `gh: Required status check "build" is expected.` {
		t.Fatalf("err %v", err)
	}
}

// GitHub deletes the branch in a second call, after the merge, and only at the merged head.
func TestGithubMergeDelete(t *testing.T) {
	repo := forgeRepo(t, "git@github.com:acme/shop.git")
	yes, no := true, false
	merged := func(ref string) gittest.Rule {
		return gittest.Rule{Has: "mergePullRequest(input", Out: `{"data":{"mergePullRequest":{"pullRequest":{"headRef":` + ref + `}}}}`}
	}
	at := func(oid string) gittest.Rule { return merged(`{"id":"REF_1","target":{"oid":"` + oid + `"}}`) }
	deleted := gittest.Rule{Has: "deleteRef(input", Out: `{"data":{"deleteRef":{"clientMutationId":null}}}`}
	deletes := func(c []gittest.Call) (n int) {
		for _, x := range c {
			if strings.Contains(x.Stdin, "deleteRef(input") {
				n++
			}
		}
		return n
	}

	calls := gittest.FakeToolCalls(t, "gh", at("aaa"), deleted, ghTarget("aaa", ""))
	out, err := SquashMerge(repo, 7, "aaa", &yes)
	if c := calls(); err != nil || out != (Merged{Deleted: true}) || len(c) != 3 || !c[2].Has("ref=REF_1") || deletes(c) != 1 {
		t.Fatalf("%+v %v, calls %q", out, err, c)
	}

	// Unticked, the branch stays.
	calls = gittest.FakeToolCalls(t, "gh", at("aaa"), deleted, ghTarget("aaa", ""))
	if out, err := SquashMerge(repo, 7, "aaa", &no); err != nil || out != (Merged{}) || deletes(calls()) != 0 {
		t.Fatalf("%+v %v, calls %q", out, err, calls())
	}

	// A push after the merge: the branch keeps those commits.
	calls = gittest.FakeToolCalls(t, "gh", at("bbb"), deleted, ghTarget("aaa", ""))
	if out, err := SquashMerge(repo, 7, "aaa", &yes); err != nil || out != (Merged{Why: "it has commits that were not merged"}) || deletes(calls()) != 0 {
		t.Fatalf("%+v %v, calls %q", out, err, calls())
	}

	// Someone deleted it already.
	calls = gittest.FakeToolCalls(t, "gh", merged("null"), deleted, ghTarget("aaa", ""))
	if out, err := SquashMerge(repo, 7, "aaa", &yes); err != nil || out != (Merged{Deleted: true}) || deletes(calls()) != 0 {
		t.Fatalf("%+v %v, calls %q", out, err, calls())
	}

	// The merge stands when GitHub refuses the delete.
	gittest.FakeToolCalls(t, "gh", at("aaa"), gittest.Rule{Has: "deleteRef(input", Out: `{"data":{"deleteRef":null},"errors":[{"message":"Cannot delete this protected branch"}]}`}, ghTarget("aaa", ""))
	if out, err := SquashMerge(repo, 7, "aaa", &yes); err != nil || out != (Merged{Why: "gh: Cannot delete this protected branch"}) {
		t.Fatalf("%+v %v", out, err)
	}
}

func TestGitlabSquashMerge(t *testing.T) {
	repo := forgeRepo(t, "https://gitlab.com/acme/shop.git")
	calls := gittest.FakeToolCalls(t, "glab", gittest.Rule{Has: mr7 + "/merge", Out: `{"iid":7,"state":"merged"}`})
	if _, err := SquashMerge(repo, 7, "h1", nil); err != nil {
		t.Fatal(err)
	}
	c := calls()
	if b, _ := json.Marshal(body(t, c[0])); len(c) != 1 || !c[0].Has("--method", "PUT", mr7+"/merge") || string(b) != `{"sha":"h1","squash":true}` {
		t.Fatalf("calls %q", c)
	}

	// The choice goes in the same PUT, also when it is no, against the project's default.
	yes, no := true, false
	for _, c := range []struct {
		del  *bool
		body string
		out  Merged
	}{
		{&yes, `{"sha":"h1","should_remove_source_branch":true,"squash":true}`, Merged{Deleted: true}},
		{&no, `{"sha":"h1","should_remove_source_branch":false,"squash":true}`, Merged{}},
	} {
		calls := gittest.FakeToolCalls(t, "glab", gittest.Rule{Has: mr7 + "/merge", Out: `{"iid":7,"state":"merged"}`})
		out, err := SquashMerge(repo, 7, "h1", c.del)
		if b, _ := json.Marshal(body(t, calls()[0])); err != nil || out != c.out || string(b) != c.body {
			t.Errorf("%+v %v, body %s", out, err, b)
		}
	}

	// GitLab merges only the head kijk showed, and answers 409 when it moved.
	gittest.FakeToolCalls(t, "glab", gittest.Rule{Has: mr7 + "/merge", Err: "409 Conflict\n", Exit: 1})
	if out, err := SquashMerge(repo, 7, "h1", &yes); !errors.Is(err, ErrHeadMoved) || err.Error() != "!7 has new commits. kijk shows them now; look at them before you merge." || out.Deleted {
		t.Fatalf("%+v %v", out, err)
	}
	gittest.FakeToolCalls(t, "glab", gittest.Rule{Has: mr7 + "/merge", Err: "405 Method Not Allowed\n", Exit: 1})
	if _, err := SquashMerge(repo, 7, "h1", nil); err == nil || err.Error() != "glab: 405 Method Not Allowed" {
		t.Fatalf("err %v", err)
	}
}
