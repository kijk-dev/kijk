package git

import (
	"fmt"
	"slices"
)

// Merge says whether kijk may squash and merge the pull request now, and why not.
type Merge struct {
	OK  bool   `json:"ok"`
	Why string `json:"why,omitempty"` // when not OK, in the words the box shows
	// Delete is what the merge may do to the head branch on the forge: "auto" the forge deletes it,
	// "off" or "on" kijk asks with the box unticked or ticked, "" it stays.
	Delete string `json:"delete,omitempty"`
}

// githubMergeJSON is what GitHub says about merging: the repo's part and the pull request's.
type githubMergeJSON struct {
	ViewerPermission    string
	SquashMergeAllowed  *bool
	DeleteBranchOnMerge bool
	Mergeable           string
	MergeStateStatus    string
	IsCrossRepository   bool
	HeadRef             *githubHeadRef
}

type githubHeadRef struct {
	RefUpdateRule          *struct{ AllowsDeletions bool }
	AssociatedPullRequests struct{ TotalCount int } // the open ones
}

// githubMerge: GitHub works out whether a pull request merges only when asked, so a state it does
// not know yet passes, and GitHub refuses the merge in its own words.
func githubMerge(pr *PR, m githubMergeJSON) Merge {
	switch {
	case pr.State == "draft":
		return Merge{Why: "This pull request is a draft."}
	case m.ViewerPermission != "" && !slices.Contains([]string{"ADMIN", "MAINTAIN", "WRITE"}, m.ViewerPermission):
		return Merge{Why: "You need write access to the repo to merge."}
	case m.SquashMergeAllowed != nil && !*m.SquashMergeAllowed:
		return Merge{Why: "This repo does not allow squash merges."}
	case m.Mergeable == "CONFLICTING":
		return Merge{Why: "This pull request has conflicts with " + pr.Base + "."}
	case m.MergeStateStatus == "BEHIND":
		return Merge{Why: "The branch is behind " + pr.Base + ". Update it first."}
	case m.MergeStateStatus == "BLOCKED":
		return Merge{Why: "The rules of " + pr.Base + " block the merge, such as a required review or check."}
	}
	return Merge{OK: true, Delete: githubBranch(m)}
}

// githubBranch: viewerCanDeleteHeadRef stays false until the merge, so kijk reads the branch's rules.
// When they miss one, GitHub refuses the delete in its own words.
func githubBranch(m githubMergeJSON) string {
	r := m.HeadRef
	switch {
	case m.IsCrossRepository || r == nil:
		return ""
	case r.RefUpdateRule != nil && !r.RefUpdateRule.AllowsDeletions:
		return ""
	case r.AssociatedPullRequests.TotalCount > 1: // the delete would close the others
		return ""
	case m.DeleteBranchOnMerge:
		return "auto"
	}
	return "off"
}

type gitlabMergeJSON struct {
	UserPermissions     *struct{ CanMerge, RemoveSourceBranch bool }
	SquashReadOnly      bool // the project's squash option is "always" or "never"
	SquashOnMerge       bool
	Conflicts           bool
	DetailedMergeStatus string
	SourceProjectID     int
	TargetProjectID     int
	// Either one makes GitLab delete the branch after the merge, unless the merge says otherwise.
	ForceRemoveSourceBranch, ShouldRemoveSourceBranch bool
}

// gitlabHolds says why GitLab holds a merge, by its detailed merge status. A status GitLab is still
// working out is in gitlabPending.
var gitlabHolds = map[string]string{
	"CI_MUST_PASS":                   "The pipeline must pass first.",
	"CI_STILL_RUNNING":               "The pipeline is still running.",
	"DISCUSSIONS_NOT_RESOLVED":       "Every thread must be resolved first.",
	"NOT_APPROVED":                   "It needs more approvals.",
	"REQUESTED_CHANGES":              "A reviewer requested changes.",
	"NEED_REBASE":                    "The branch needs a rebase first.",
	"BLOCKED_STATUS":                 "Another merge request must merge first.",
	"EXTERNAL_STATUS_CHECKS":         "The status checks must pass first.",
	"JIRA_ASSOCIATION":               "The title or description must name a Jira issue.",
	"LOCKED_PATHS":                   "It changes locked files.",
	"LOCKED_LFS_FILES":               "It changes locked files.",
	"MERGE_TIME":                     "The project allows merges only at set times.",
	"SECURITY_POLICIES_VIOLATIONS":   "The security policies of the project block it.",
	"SECURITY_POLICY_PIPELINE_CHECK": "The security policies of the project block it.",
	"TITLE_NOT_MATCHING":             "The title does not match the rule of the project.",
}

var gitlabPending = []string{"", "MERGEABLE", "UNCHECKED", "CHECKING", "PREPARING", "APPROVALS_SYNCING"}

func gitlabMerge(pr *PR, m gitlabMergeJSON) Merge {
	switch {
	case pr.State == "draft":
		return Merge{Why: "This merge request is a draft."}
	case m.UserPermissions != nil && !m.UserPermissions.CanMerge:
		return Merge{Why: "You are not allowed to merge into " + pr.Base + "."}
	case m.SquashReadOnly && !m.SquashOnMerge:
		// GitLab never squashes in such a project, so the merge would keep every commit.
		return Merge{Why: "This project does not allow squash merges."}
	case m.Conflicts:
		return Merge{Why: "This merge request has conflicts with " + pr.Base + "."}
	case slices.Contains(gitlabPending, m.DetailedMergeStatus):
		return Merge{OK: true, Delete: gitlabBranch(m)}
	}
	if why, ok := gitlabHolds[m.DetailedMergeStatus]; ok {
		return Merge{Why: why}
	}
	return Merge{Why: "GitLab does not allow the merge now."}
}

func gitlabBranch(m gitlabMergeJSON) string {
	switch {
	case m.SourceProjectID != m.TargetProjectID, m.UserPermissions == nil || !m.UserPermissions.RemoveSourceBranch:
		return ""
	case m.ForceRemoveSourceBranch || m.ShouldRemoveSourceBranch:
		return "on"
	}
	return "off"
}

// ErrMergeMoved is ErrHeadMoved for a merge, which would take along commits kijk did not show.
var ErrMergeMoved = movedErr("has new commits. kijk shows them now; look at them before you merge.")

type movedErr string

func (e movedErr) Error() string        { return string(e) }
func (e movedErr) Is(target error) bool { return target == ErrHeadMoved }

// Merged is what a merge did to the head branch, when kijk asked to delete it. A delete that
// fails does not fail the merge.
type Merged struct {
	Deleted bool   `json:"deleted"`
	Why     string `json:"why,omitempty"` // why the branch is still there
}

// SquashMerge squashes pull request n into one commit on its base, only while its head is head,
// the head kijk showed. del says whether to delete the branch too; nil leaves it to the forge.
func SquashMerge(root string, n int, head string, del *bool) (Merged, error) {
	if head == "" {
		return Merged{}, badInput("no head to merge")
	}
	f, tool, err := writer(root)
	if err != nil {
		return Merged{}, err
	}
	var out Merged
	if tool == "gh" {
		out, err = githubSquash(f, n, head, del != nil && *del)
	} else {
		out, err = gitlabSquash(f, n, head, del)
	}
	return out, toolErr(tool, err)
}

func mergeMoved(f *forge, n int) error {
	return fmt.Errorf("%s %w", f.Ref(n), ErrMergeMoved)
}

// githubSquash checks the head first to say so in kijk's words; expectedHeadOid still holds a push
// that lands between the two calls.
func githubSquash(f *forge, n int, head string, del bool) (Merged, error) {
	t, err := githubTargetOf(f, n)
	if err != nil {
		return Merged{}, err
	}
	if t.head != head {
		return Merged{}, mergeMoved(f, n)
	}
	var out struct {
		MergePullRequest struct {
			PullRequest struct {
				HeadRef *struct {
					ID     string
					Target struct{ Oid string }
				}
			}
		}
	}
	err = mutate("gh", f, `mutation($pr: ID!, $head: GitObjectID!) { mergePullRequest(input: {pullRequestId: $pr, mergeMethod: SQUASH, expectedHeadOid: $head}) { pullRequest { headRef { id target { oid } } } } }`,
		map[string]any{"pr": t.id, "head": head}, &out)
	if err != nil || !del {
		return Merged{}, err
	}
	ref := out.MergePullRequest.PullRequest.HeadRef
	switch {
	case ref == nil:
		return Merged{Deleted: true}, nil // someone deleted it already
	case ref.Target.Oid != head:
		return Merged{Why: "it has commits that were not merged"}, nil
	}
	if err := mutate("gh", f, `mutation($ref: ID!) { deleteRef(input: {refId: $ref}) { clientMutationId } }`, map[string]any{"ref": ref.ID}, nil); err != nil {
		return Merged{Why: toolErr("gh", err).Error()}, nil
	}
	return Merged{Deleted: true}, nil
}

// gitlabSquash: with sha GitLab merges only that head, and answers 409 when it moved. GitLab deletes
// the branch itself after the merge, so kijk says it is deleted once GitLab took the merge.
func gitlabSquash(f *forge, n int, head string, del *bool) (Merged, error) {
	body := map[string]any{"squash": true, "sha": head}
	if del != nil {
		body["should_remove_source_branch"] = *del
	}
	_, err := gitlabREST(f, "PUT", gitlabMR(f, n)+"/merge", body)
	if err != nil && conflictRe.MatchString(err.Error()) {
		return Merged{}, mergeMoved(f, n)
	}
	return Merged{Deleted: err == nil && del != nil && *del}, err
}
