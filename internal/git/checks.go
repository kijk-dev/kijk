package git

import "encoding/json"

// Checks are the CI of a pull request's head: one group per GitHub workflow run or GitLab
// pipeline, and the checks of other apps.
type Checks struct {
	Groups []CheckGroup `json:"groups"`
	Other  []Job        `json:"other,omitempty"`
}

type githubRollup struct {
	State    string
	Contexts struct {
		Nodes []json.RawMessage
	}
}

type gitlabPipeline struct {
	IID      string
	Status   string
	Path     string
	Duration int
	Stages   struct {
		Nodes []struct{ Name string }
	}
	Jobs struct {
		Nodes []gitlabJob
	}
}

type gitlabJob struct {
	ID             string
	Name           string
	Status         string
	Duration       int
	WebPath        string
	Kind           string
	SchedulingType string
	Stage          *struct{ Name string }
	Needs          struct {
		Nodes []struct{ Name string }
	}
	DownstreamPipeline *struct{ Status string }
}

// CheckGroup is one workflow run or pipeline. Graph says the needs of its jobs are known.
type CheckGroup struct {
	Title  string `json:"title"`
	Number string `json:"number,omitempty"` // the run or pipeline number
	URL    string `json:"url,omitempty"`
	Graph  bool   `json:"graph"`
	Jobs   []Job  `json:"jobs"`
}

// Job is one check: a CI job, a commit status or another app's check run.
type Job struct {
	ID    string   `json:"id"` // for its log; empty when kijk cannot read one
	Name  string   `json:"name"`
	State string   `json:"state"` // "ok", "bad", "run", "skip" or "wait"
	Time  string   `json:"time,omitempty"`
	URL   string   `json:"url,omitempty"`
	Stage string   `json:"stage,omitempty"`
	Needs []string `json:"needs,omitempty"` // names of jobs in the same group
	Note  string   `json:"note,omitempty"`  // a status's description
}
