package git

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

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

// ReadChecks fills pr.Checks from what the forge said about its head commit. A GitHub workflow
// file is read at the head, so the head must be in the repo.
func (pr *PR) ReadChecks(root string, f *forge) {
	switch {
	case pr.rollup != nil:
		pr.Checks = githubChecks(root, pr.HeadOid, pr.rollup)
	case pr.pipeline != nil:
		pr.Checks = gitlabChecks(pr.pipeline, f)
	}
}

type githubContext struct {
	Typename    string `json:"__typename"`
	DatabaseID  int64
	Name        string
	Status      string
	Conclusion  string
	StartedAt   string
	CompletedAt string
	DetailsURL  string
	CheckSuite  *struct {
		WorkflowRun *struct {
			DatabaseID int64
			RunNumber  int
			URL        string
			File       *struct{ Path string }
			Workflow   *struct{ Name string }
		}
	}
	Context     string
	State       string
	TargetURL   string
	Description string
}

func githubChecks(root, head string, r *githubRollup) *Checks {
	c := &Checks{Groups: []CheckGroup{}}
	byRun := map[int64]int{}
	files := map[int64]string{}
	for _, raw := range r.Contexts.Nodes {
		var x githubContext
		if json.Unmarshal(raw, &x) != nil {
			continue
		}
		if x.Typename == "StatusContext" {
			c.Other = append(c.Other, Job{Name: x.Context, State: statusState(x.State), URL: webURL(x.TargetURL), Note: x.Description})
			continue
		}
		j := Job{Name: x.Name, State: runState(x.Status, x.Conclusion), Time: took(x.StartedAt, x.CompletedAt), URL: webURL(x.DetailsURL)}
		run := x.CheckSuite
		if run == nil || run.WorkflowRun == nil {
			c.Other = append(c.Other, j)
			continue
		}
		w := run.WorkflowRun
		j.ID = strconv.FormatInt(x.DatabaseID, 10) // an Actions job's id, for its log
		i, ok := byRun[w.DatabaseID]
		if !ok {
			i = len(c.Groups)
			byRun[w.DatabaseID] = i
			title := "Workflow"
			if w.Workflow != nil {
				title = w.Workflow.Name
			}
			c.Groups = append(c.Groups, CheckGroup{Title: title, Number: strconv.Itoa(w.RunNumber), URL: webURL(w.URL), Jobs: []Job{}})
			if w.File != nil {
				files[w.DatabaseID] = w.File.Path
			}
		}
		c.Groups[i].Jobs = append(c.Groups[i].Jobs, j)
	}
	for run, i := range byRun {
		needsFromFile(root, head, files[run], &c.Groups[i])
	}
	return c
}

// needsFromFile draws the group as a graph when its workflow file names every job's needs.
func needsFromFile(root, head, path string, g *CheckGroup) {
	if path == "" || !filepath.IsLocal(filepath.FromSlash(path)) || !ValidHash(head) {
		return
	}
	src, err := Run(root, "show", head+":"+path)
	if err != nil {
		return
	}
	jobs, err := parseWorkflow(src)
	if err != nil {
		return
	}
	ids := make([]string, len(g.Jobs))
	for i, j := range g.Jobs {
		var ok bool
		if ids[i], ok = matchJob(j.Name, jobs); !ok {
			return
		}
	}
	for i := range g.Jobs {
		w := jobs[slices.IndexFunc(jobs, func(w workflowJob) bool { return w.ID == ids[i] })]
		for k, id := range ids {
			if slices.Contains(w.Needs, id) {
				g.Jobs[i].Needs = append(g.Jobs[i].Needs, g.Jobs[k].Name)
			}
		}
	}
	g.Graph = true
}

func runState(status, conclusion string) string {
	switch status {
	case "IN_PROGRESS":
		return "run"
	case "COMPLETED":
	case "":
		return "wait"
	default: // queued, waiting, pending, requested
		return "wait"
	}
	switch conclusion {
	case "SUCCESS", "NEUTRAL":
		return "ok"
	case "FAILURE", "TIMED_OUT", "STARTUP_FAILURE", "ACTION_REQUIRED":
		return "bad"
	}
	return "skip" // skipped, cancelled, stale
}

func statusState(state string) string {
	switch state {
	case "SUCCESS":
		return "ok"
	case "FAILURE", "ERROR":
		return "bad"
	}
	return "run"
}

// took is the time between two RFC 3339 times, as "2m 05s".
func took(start, end string) string {
	a, err1 := time.Parse(time.RFC3339, start)
	b, err2 := time.Parse(time.RFC3339, end)
	if err1 != nil || err2 != nil || b.Before(a) {
		return ""
	}
	return seconds(int(b.Sub(a).Seconds()))
}

func seconds(s int) string {
	if s <= 0 {
		return ""
	}
	if s < 60 {
		return fmt.Sprintf("%ds", s)
	}
	if s < 3600 {
		return fmt.Sprintf("%dm %02ds", s/60, s%60)
	}
	return fmt.Sprintf("%dh %02dm", s/3600, s/60%60)
}

func gitlabChecks(p *gitlabPipeline, f *forge) *Checks {
	web := func(path string) string {
		if !strings.HasPrefix(path, "/") || f == nil {
			return ""
		}
		return "https://" + f.Host + path
	}
	g := CheckGroup{Title: "Pipeline", Number: p.IID, URL: web(p.Path), Graph: true, Jobs: []Job{}}
	stageOf := map[string]int{}
	for i, s := range p.Stages.Nodes {
		stageOf[s.Name] = i
	}
	names := map[string]bool{}
	for _, j := range p.Jobs.Nodes {
		names[j.Name] = true
	}
	stage := func(j gitlabJob) string {
		if j.Stage == nil {
			return ""
		}
		return j.Stage.Name
	}
	// GitLab lists jobs newest stage first; the graph reads from the first stage.
	jobs := slices.Clone(p.Jobs.Nodes)
	slices.SortStableFunc(jobs, func(a, b gitlabJob) int { return stageOf[stage(a)] - stageOf[stage(b)] })
	for _, j := range jobs {
		job := Job{Name: j.Name, State: jobState(j.Status), Time: seconds(j.Duration), URL: web(j.WebPath), Stage: stage(j)}
		if j.Kind == "BRIDGE" {
			if j.DownstreamPipeline != nil {
				job.State = jobState(j.DownstreamPipeline.Status)
			}
		} else if jobPathRe.MatchString(j.WebPath) {
			job.ID = j.WebPath
		}
		if strings.EqualFold(j.SchedulingType, "stage") {
			// A job without needs waits for the whole stage before it.
			if at, ok := stageOf[stage(j)]; ok && at > 0 {
				for _, o := range jobs {
					if stageOf[stage(o)] == at-1 && stage(o) != "" {
						job.Needs = append(job.Needs, o.Name)
					}
				}
			}
		} else {
			for _, n := range j.Needs.Nodes {
				if names[n.Name] {
					job.Needs = append(job.Needs, n.Name)
				}
			}
		}
		g.Jobs = append(g.Jobs, job)
	}
	return &Checks{Groups: []CheckGroup{g}}
}

func jobState(status string) string {
	switch strings.ToUpper(status) {
	case "SUCCESS":
		return "ok"
	case "FAILED":
		return "bad"
	case "RUNNING":
		return "run"
	case "SKIPPED", "CANCELED", "CANCELING":
		return "skip"
	}
	return "wait" // created, pending, manual, scheduled, waiting for a resource
}

var (
	jobPathRe   = regexp.MustCompile(`^/([A-Za-z0-9_.-]+(?:/[A-Za-z0-9_.-]+)+)/-/jobs/([0-9]+)$`)
	jobIDRe     = regexp.MustCompile(`^[0-9]{1,20}$`)
	timestampRe = regexp.MustCompile(`^\d{4}-\d\d-\d\dT[\d:.]+Z `)
	ansiRe      = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]|section_(start|end):\d+:[^\r\n]*\r?`)
)

const logLines = 30

// JobLog gives the last lines of a CI job's log. id is a GitHub Actions job id, or on GitLab the
// job's path, which names the project it ran in, also for a fork.
func JobLog(root, id string) ([]string, error) {
	f, tool, note := forgeTool(root)
	if f == nil {
		return nil, errors.New("origin is not on GitHub or GitLab")
	}
	if note != "" {
		return nil, errors.New(note)
	}
	var endpoint string
	if tool == "gh" {
		if !jobIDRe.MatchString(id) {
			return nil, ErrBadHash
		}
		endpoint = "repos/" + f.Path + "/actions/jobs/" + id + "/logs"
	} else {
		m := jobPathRe.FindStringSubmatch(id)
		if m == nil {
			return nil, ErrBadHash
		}
		endpoint = "projects/" + url.PathEscape(m[1]) + "/jobs/" + m[2] + "/trace"
	}
	out, err := runTool(tool, "api", "--hostname", f.Host, endpoint)
	if err != nil {
		return nil, errors.New(toolNote(tool, err))
	}
	var lines []string
	for _, l := range strings.Split(string(out), "\n") {
		l = timestampRe.ReplaceAllString(ansiRe.ReplaceAllString(l, ""), "")
		l = strings.TrimRight(strings.ReplaceAll(l, "\r", ""), " \t")
		if l != "" && l != "##[endgroup]" {
			lines = append(lines, l)
		}
	}
	return lines[max(0, len(lines)-logLines):], nil
}
