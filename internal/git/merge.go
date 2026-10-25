package git

import (
	"slices"
)

// Merge says whether kijk may squash and merge the pull request now, and why not.
type Merge struct {
	OK  bool   `json:"ok"`
	Why string `json:"why,omitempty"` // when not OK, in the words the box shows
}

// githubMergeJSON is what GitHub says about merging: the repo's part and the pull request's.
type githubMergeJSON struct {
	ViewerPermission   string
	SquashMergeAllowed *bool
	Mergeable          string
	MergeStateStatus   string
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
	return Merge{OK: true}
}

type gitlabMergeJSON struct {
	UserPermissions     *struct{ CanMerge bool }
	SquashReadOnly      bool // the project's squash option is "always" or "never"
	SquashOnMerge       bool
	Conflicts           bool
	DetailedMergeStatus string
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
		return Merge{OK: true}
	}
	if why, ok := gitlabHolds[m.DetailedMergeStatus]; ok {
		return Merge{Why: why}
	}
	return Merge{Why: "GitLab does not allow the merge now."}
}
