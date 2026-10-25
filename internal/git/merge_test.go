package git

import (
	"testing"
)

func TestMergeRules(t *testing.T) {
	open := &PR{PRRow: PRRow{Base: "main"}, State: "open"}
	draft := &PR{PRRow: PRRow{Base: "main"}, State: "draft"}
	yes, no := true, false
	can := func(on bool) *struct{ CanMerge bool } { return &struct{ CanMerge bool }{on} }
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
}
