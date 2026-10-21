package git

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// PRRow is one open pull request in the PR list.
type PRRow struct {
	Number    int    `json:"number"`
	Title     string `json:"title"`
	Author    string `json:"author"`
	Draft     bool   `json:"draft,omitempty"`
	CreatedAt string `json:"createdAt"`
	Head      string `json:"head"` // the branch it merges from
	Base      string `json:"base"` // the branch it merges into
	Add       int    `json:"add"`
	Del       int    `json:"del"`
	CI        string `json:"ci,omitempty"` // "passed", "failed" or "running"
	Requested bool   `json:"requested,omitempty"`
	URL       string `json:"url,omitempty"`
}

// PRList is the open pull requests of the repo behind origin, newest activity first.
type PRList struct {
	Forge *forge  `json:"forge,omitempty"`
	Tool  string  `json:"tool,omitempty"`
	Note  string  `json:"note,omitempty"` // why there is no list
	Me    string  `json:"me,omitempty"`   // the login of the tool's user
	Total int     `json:"total"`          // all open pull requests; PRs holds at most listSize
	PRs   []PRRow `json:"prs"`
}

const listSize = 100

// forgeTool finds origin's forge and the tool that talks to it, or says why there is none.
func forgeTool(root string) (f *forge, tool, note string) {
	f = detectForge(root)
	if f == nil {
		return nil, "", ""
	}
	tool = map[string]string{"github": "gh", "gitlab": "glab"}[f.Kind]
	if _, err := exec.LookPath(tool); err != nil {
		return f, tool, "Install " + tool + " and log in to see " + map[string]string{"gh": "pull request", "glab": "merge request"}[tool] + " status"
	}
	return f, tool, ""
}

// ListPRs asks the forge for the open pull requests. q, when set, searches them on the forge
// instead, for a repo with more open pull requests than the list holds.
func ListPRs(root, q string) PRList {
	f, tool, note := forgeTool(root)
	res := PRList{Forge: f, Tool: tool, Note: note, PRs: []PRRow{}}
	if f == nil || note != "" {
		return res
	}
	var err error
	switch {
	case tool == "gh" && q != "":
		err = graphQL("gh", f, githubSearchQuery, githubSearchVars(f, q), &res, parseGithubSearch)
	case tool == "gh":
		err = graphQL("gh", f, githubListQuery, map[string]any{"owner": owner(f), "name": name(f), "asked": githubRepoQuery(f) + " review-requested:@me"}, &res, parseGithubList)
	case q != "":
		err = graphQL("glab", f, gitlabSearchQuery, map[string]any{"path": f.Path, "q": q}, &res, parseGitlabSearch)
	default:
		err = graphQL("glab", f, gitlabListQuery, map[string]any{"path": f.Path, "pp": f.Path}, &res, parseGitlabList)
	}
	if err != nil {
		res.Note = toolNote(tool, err)
	}
	return res
}

func owner(f *forge) string { o, _, _ := strings.Cut(f.Path, "/"); return o }
func name(f *forge) string  { _, n, _ := strings.Cut(f.Path, "/"); return n }

func githubRepoQuery(f *forge) string { return "repo:" + f.Path + " is:pr is:open" }

var oneWordRe = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)

// githubSearchVars searches the text, and a single word also as an author and a head branch.
func githubSearchVars(f *forge, q string) map[string]any {
	text := githubRepoQuery(f) + " " + q
	v := map[string]any{"q": text, "a": text, "h": text}
	if oneWordRe.MatchString(q) {
		v["a"] = githubRepoQuery(f) + " author:" + q
		v["h"] = githubRepoQuery(f) + " head:" + q
	}
	return v
}

// addRows adds the rows that are not in the list yet, marked requested when asked is.
func addRows(res *PRList, rows []PRRow, asked bool) {
	for _, r := range rows {
		i := slices.IndexFunc(res.PRs, func(p PRRow) bool { return p.Number == r.Number })
		if i < 0 {
			r.Requested = r.Requested || asked
			res.PRs = append(res.PRs, r)
		} else if asked {
			res.PRs[i].Requested = true
		}
	}
}

// graphQL runs a query with gh or glab and hands the data to parse. Strings go as -f, numbers as
// -F, in name order, so a call reads the same each time.
func graphQL[T any](tool string, f *forge, query string, vars map[string]any, out *T, parse func([]byte, *T) error) error {
	args := []string{"api", "graphql", "--hostname", f.Host, "-f", "query=" + query}
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		switch v := vars[k].(type) {
		case int:
			args = append(args, "-F", k+"="+strconv.Itoa(v))
		default:
			args = append(args, "-f", fmt.Sprintf("%s=%v", k, v))
		}
	}
	b, err := runTool(tool, args...)
	if err != nil {
		return err
	}
	var env struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(b, &env); err != nil {
		return err
	}
	if len(env.Errors) > 0 && (len(env.Data) == 0 || string(env.Data) == "null") {
		return errors.New(env.Errors[0].Message)
	}
	return parse(env.Data, out)
}

const githubRow = `fragment row on PullRequest { number title isDraft url createdAt headRefName baseRefName
  additions deletions author { login } commits(last: 1) { nodes { commit { statusCheckRollup { state } } } } }`

const githubListQuery = `query($owner: String!, $name: String!, $asked: String!) {
  viewer { login }
  repository(owner: $owner, name: $name) {
    pullRequests(states: OPEN, first: 100, orderBy: {field: UPDATED_AT, direction: DESC}) { totalCount nodes { ...row } }
  }
  asked: search(query: $asked, type: ISSUE, first: 100) { nodes { ...row } }
}
` + githubRow

const githubSearchQuery = `query($q: String!, $a: String!, $h: String!) {
  viewer { login }
  t: search(query: $q, type: ISSUE, first: 30) { issueCount nodes { ...row } }
  a: search(query: $a, type: ISSUE, first: 30) { nodes { ...row } }
  h: search(query: $h, type: ISSUE, first: 30) { nodes { ...row } }
}
` + githubRow

type githubRowJSON struct {
	Number      int
	Title       string
	IsDraft     bool
	URL         string
	CreatedAt   string
	HeadRefName string
	BaseRefName string
	Additions   int
	Deletions   int
	Author      *struct{ Login string }
	Commits     struct {
		Nodes []struct {
			Commit struct {
				StatusCheckRollup *struct{ State string }
			}
		}
	}
}

func (r githubRowJSON) row() PRRow {
	p := PRRow{Number: r.Number, Title: r.Title, Draft: r.IsDraft, URL: webURL(r.URL), CreatedAt: r.CreatedAt,
		Head: r.HeadRefName, Base: r.BaseRefName, Add: r.Additions, Del: r.Deletions}
	if r.Author != nil {
		p.Author = r.Author.Login
	}
	if n := r.Commits.Nodes; len(n) > 0 && n[0].Commit.StatusCheckRollup != nil {
		p.CI = rollupCI(n[0].Commit.StatusCheckRollup.State)
	}
	return p
}

// rollupCI reads GitHub's summary state of a commit's checks.
func rollupCI(state string) string {
	switch state {
	case "SUCCESS":
		return "passed"
	case "FAILURE", "ERROR":
		return "failed"
	case "PENDING", "EXPECTED":
		return "running"
	}
	return ""
}

func parseGithubList(data []byte, res *PRList) error {
	var v struct {
		Viewer     struct{ Login string }
		Repository *struct {
			PullRequests struct {
				TotalCount int
				Nodes      []githubRowJSON
			}
		}
		Asked struct {
			Nodes []githubRowJSON
		}
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	if v.Repository == nil {
		return errors.New("repository not found")
	}
	res.Me = v.Viewer.Login
	res.Total = v.Repository.PullRequests.TotalCount
	for _, n := range v.Repository.PullRequests.Nodes {
		res.PRs = append(res.PRs, n.row())
	}
	// A pull request that waits for you may be older than the 100 the list holds.
	addRows(res, githubRows(v.Asked.Nodes), true)
	return nil
}

// githubRows are the rows of search hits that are pull requests.
func githubRows(nodes []githubRowJSON) []PRRow {
	var out []PRRow
	for _, n := range nodes {
		if n.Number > 0 { // a hit that is an issue has none of the fields
			out = append(out, n.row())
		}
	}
	return out
}

func parseGithubSearch(data []byte, res *PRList) error {
	type hits struct {
		IssueCount int
		Nodes      []githubRowJSON
	}
	var v struct {
		Viewer  struct{ Login string }
		T, A, H hits
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	res.Me = v.Viewer.Login
	res.Total = v.T.IssueCount
	for _, h := range []hits{v.T, v.A, v.H} {
		addRows(res, githubRows(h.Nodes), false)
	}
	return nil
}

const gitlabRow = `fragment row on MergeRequest { iid title draft webUrl createdAt sourceBranch targetBranch author { username }
  diffStatsSummary { additions deletions } headPipeline { status }
  reviewers { nodes { username mergeRequestInteraction { approved } } } }`

const gitlabListQuery = `query($path: ID!, $pp: String!) {
  currentUser { username reviewRequestedMergeRequests(state: opened, first: 100, projectPath: $pp) { nodes { ...row } } }
  project(fullPath: $path) { mergeRequests(state: opened, first: 100, sort: UPDATED_DESC) { count nodes { ...row } } }
}
` + gitlabRow

// The search finds the text in the title and description, and also an author or a source branch.
const gitlabSearchQuery = `query($path: ID!, $q: String!) {
  currentUser { username }
  project(fullPath: $path) {
    t: mergeRequests(state: opened, first: 30, search: $q) { count nodes { ...row } }
    a: mergeRequests(state: opened, first: 30, authorUsername: $q) { nodes { ...row } }
    h: mergeRequests(state: opened, first: 30, sourceBranches: [$q]) { nodes { ...row } }
  }
}
` + gitlabRow

type gitlabRowJSON struct {
	IID              string
	Title            string
	Draft            bool
	WebURL           string
	CreatedAt        string
	SourceBranch     string
	TargetBranch     string
	Author           *struct{ Username string }
	DiffStatsSummary *struct{ Additions, Deletions int }
	HeadPipeline     *struct{ Status string }
	Reviewers        struct {
		Nodes []struct {
			Username                string
			MergeRequestInteraction *struct{ Approved bool }
		}
	}
}

// row is the merge request as a list row; it waits for me when I am a reviewer who has not approved.
func (n gitlabRowJSON) row(me string) PRRow {
	num, _ := strconv.Atoi(n.IID)
	p := PRRow{Number: num, Title: draftPrefixRe.ReplaceAllString(n.Title, ""), Draft: n.Draft, URL: webURL(n.WebURL),
		CreatedAt: n.CreatedAt, Head: n.SourceBranch, Base: n.TargetBranch}
	if n.Author != nil {
		p.Author = n.Author.Username
	}
	if s := n.DiffStatsSummary; s != nil {
		p.Add, p.Del = s.Additions, s.Deletions
	}
	if n.HeadPipeline != nil {
		p.CI = pipelineCI(n.HeadPipeline.Status)
	}
	for _, r := range n.Reviewers.Nodes {
		if me != "" && r.Username == me && (r.MergeRequestInteraction == nil || !r.MergeRequestInteraction.Approved) {
			p.Requested = true
		}
	}
	return p
}

func gitlabRows(nodes []gitlabRowJSON, me string) []PRRow {
	out := make([]PRRow, len(nodes))
	for i, n := range nodes {
		out[i] = n.row(me)
	}
	return out
}

func parseGitlabSearch(data []byte, res *PRList) error {
	type hits struct {
		Count int
		Nodes []gitlabRowJSON
	}
	var v struct {
		CurrentUser *struct{ Username string }
		Project     *struct{ T, A, H hits }
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	if v.Project == nil {
		return errors.New("project not found")
	}
	if v.CurrentUser != nil {
		res.Me = v.CurrentUser.Username
	}
	res.Total = v.Project.T.Count
	for _, h := range []hits{v.Project.T, v.Project.A, v.Project.H} {
		addRows(res, gitlabRows(h.Nodes, res.Me), false)
	}
	return nil
}

func parseGitlabList(data []byte, res *PRList) error {
	var v struct {
		CurrentUser *struct {
			Username                     string
			ReviewRequestedMergeRequests struct{ Nodes []gitlabRowJSON }
		}
		Project *struct {
			MergeRequests struct {
				Count int
				Nodes []gitlabRowJSON
			}
		}
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	if v.Project == nil {
		return errors.New("project not found")
	}
	if v.CurrentUser != nil {
		res.Me = v.CurrentUser.Username
	}
	res.Total = v.Project.MergeRequests.Count
	res.PRs = append(res.PRs, gitlabRows(v.Project.MergeRequests.Nodes, res.Me)...)
	if v.CurrentUser != nil {
		// A merge request that waits for you may be older than the 100 the list holds.
		for _, r := range gitlabRows(v.CurrentUser.ReviewRequestedMergeRequests.Nodes, res.Me) {
			if r.Requested {
				addRows(res, []PRRow{r}, true)
			}
		}
	}
	return nil
}

// pipelineCI reads a GitLab pipeline status, from the REST API or GraphQL, which writes it in capitals.
func pipelineCI(status string) string {
	switch strings.ToLower(status) {
	case "success":
		return "passed"
	case "failed":
		return "failed"
	case "running", "pending", "created", "preparing", "waiting_for_resource", "scheduled":
		return "running"
	}
	return ""
}

// PR is one pull request as its Overview and Changes show it.
type PR struct {
	PRRow
	State     string     `json:"state"` // "open", "draft", "merged" or "closed"
	Body      string     `json:"body"`  // markdown; the viewer renders it
	HeadOid   string     `json:"headOid"`
	BaseOid   string     `json:"baseOid,omitempty"`   // GitHub: the target's tip; GitLab: the merge base
	MergeBase string     `json:"mergeBase,omitempty"` // when the forge gives it
	Commits   int        `json:"commits"`
	Reviewers []Reviewer `json:"reviewers"`
	Approvals int        `json:"approvals"`
	Comments  []Comment  `json:"comments"` // the conversation: comments not on a line
	Threads   []Thread   `json:"threads"`
	Checks    *Checks    `json:"checks,omitempty"`
	Me        string     `json:"me,omitempty"`

	rollup   *githubRollup // read into Checks once the head is in the repo
	pipeline *gitlabPipeline
}

type Reviewer struct {
	Name  string `json:"name"`
	State string `json:"state"` // "approved", "changes", "commented" or "requested"
}

type Comment struct {
	Author string `json:"author"`
	At     string `json:"at"`
	Body   string `json:"body"`
	HTML   string `json:"html,omitempty"` // set by the viewer
	URL    string `json:"url,omitempty"`
}

// Thread is a discussion on a line of the diff.
type Thread struct {
	ID       string    `json:"id"`
	Path     string    `json:"path"`
	Old      bool      `json:"old,omitempty"`  // on a removed line: Line is a line of the old file
	Line     int       `json:"line,omitempty"` // 0 when the forge no longer places it
	OrigLine int       `json:"origLine,omitempty"`
	Was      string    `json:"was,omitempty"`  // the line it was written on, for an outdated thread
	Text     string    `json:"text,omitempty"` // its line at the head, set by the viewer
	Resolved bool      `json:"resolved,omitempty"`
	Outdated bool      `json:"outdated,omitempty"`
	Comments []Comment `json:"comments"`
}

// ViewPR asks the forge for pull request n.
func ViewPR(root string, n int) (*PR, *forge, error) {
	f, tool, note := forgeTool(root)
	if f == nil {
		return nil, nil, errors.New("origin is not on GitHub or GitLab")
	}
	if note != "" {
		return nil, f, errors.New(note)
	}
	var pr PR
	var err error
	if tool == "gh" {
		err = graphQL("gh", f, githubPRQuery, map[string]any{"owner": owner(f), "name": name(f), "n": n}, &pr, parseGithubPRView)
	} else {
		err = graphQL("glab", f, gitlabPRQuery, map[string]any{"path": f.Path, "iid": strconv.Itoa(n)}, &pr, parseGitlabPR)
	}
	if err != nil {
		if note := toolNote(tool, err); note != "" {
			return nil, f, errors.New(note)
		}
		return nil, f, err
	}
	return &pr, f, nil
}

const githubPRQuery = `query($owner: String!, $name: String!, $n: Int!) {
  viewer { login }
  repository(owner: $owner, name: $name) {
    pullRequest(number: $n) {
      number title body url state isDraft createdAt headRefName baseRefName headRefOid baseRefOid
      additions deletions author { login } commits { totalCount }
      reviewRequests(first: 30) { nodes { requestedReviewer { __typename ... on User { login } ... on Team { name } ... on Bot { login } ... on Mannequin { login } } } }
      latestReviews(first: 30) { nodes { author { login } state } }
      reviews(first: 50) { nodes { author { login } state body createdAt url } }
      comments(first: 100) { nodes { author { login } body createdAt url } }
      reviewThreads(first: 100) { nodes { id isResolved isOutdated path line originalLine diffSide
        comments(first: 50) { nodes { author { login } body createdAt url state diffHunk } } } }
      head: commits(last: 1) { nodes { commit { oid statusCheckRollup { state contexts(first: 100) { nodes {
        __typename
        ... on CheckRun { databaseId name status conclusion startedAt completedAt detailsUrl
          checkSuite { app { slug name } workflowRun { databaseId runNumber url file { path } workflow { name } } } }
        ... on StatusContext { context state targetUrl description createdAt } } } } } } }
    }
  }
}`

type githubAuthor struct{ Login string }

func login(a *githubAuthor) string {
	if a == nil {
		return "ghost" // GitHub's name for a deleted account
	}
	return a.Login
}

type githubCommentJSON struct {
	Author    *githubAuthor
	Body      string
	CreatedAt string
	URL       string
	State     string
	DiffHunk  string
}

func (c githubCommentJSON) comment() Comment {
	return Comment{Author: login(c.Author), At: c.CreatedAt, Body: c.Body, URL: webURL(c.URL)}
}

func parseGithubPRView(data []byte, pr *PR) error {
	var v struct {
		Viewer     struct{ Login string }
		Repository *struct {
			PullRequest *struct {
				githubRowJSON
				Body           string
				State          string
				HeadRefOid     string
				BaseRefOid     string
				Commits        struct{ TotalCount int }
				ReviewRequests struct {
					Nodes []struct {
						RequestedReviewer *struct{ Login, Name string }
					}
				}
				LatestReviews struct {
					Nodes []struct {
						Author *githubAuthor
						State  string
					}
				}
				Reviews struct {
					Nodes []githubCommentJSON
				}
				Comments struct {
					Nodes []githubCommentJSON
				}
				ReviewThreads struct {
					Nodes []struct {
						ID           string
						IsResolved   bool
						IsOutdated   bool
						Path         string
						Line         int
						OriginalLine int
						DiffSide     string
						Comments     struct {
							Nodes []githubCommentJSON
						}
					}
				}
				Head struct {
					Nodes []struct {
						Commit struct {
							Oid               string
							StatusCheckRollup *githubRollup
						}
					}
				}
			}
		}
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	if v.Repository == nil || v.Repository.PullRequest == nil {
		return errors.New("pull request not found")
	}
	p := v.Repository.PullRequest
	*pr = PR{PRRow: p.row(), Body: p.Body, HeadOid: p.HeadRefOid, BaseOid: p.BaseRefOid, Commits: p.Commits.TotalCount,
		Me: v.Viewer.Login, Reviewers: []Reviewer{}, Comments: []Comment{}, Threads: []Thread{}}
	pr.State = prState(p.State, p.IsDraft)
	seen := map[string]int{}
	add := func(name, state string) {
		if i, ok := seen[name]; ok {
			pr.Reviewers[i].State = state
			return
		}
		seen[name] = len(pr.Reviewers)
		pr.Reviewers = append(pr.Reviewers, Reviewer{name, state})
	}
	for _, r := range p.LatestReviews.Nodes {
		if s := map[string]string{"APPROVED": "approved", "CHANGES_REQUESTED": "changes", "COMMENTED": "commented"}[r.State]; s != "" && login(r.Author) != pr.Author {
			add(login(r.Author), s)
			if s == "approved" {
				pr.Approvals++
			}
		}
	}
	// Asked again after a review: the request is what counts now.
	for _, r := range p.ReviewRequests.Nodes {
		if rr := r.RequestedReviewer; rr != nil {
			add(cmp.Or(rr.Login, rr.Name), "requested")
		}
	}
	for _, c := range p.Comments.Nodes {
		pr.Comments = append(pr.Comments, c.comment())
	}
	for _, r := range p.Reviews.Nodes {
		if strings.TrimSpace(r.Body) != "" && r.State != "PENDING" {
			pr.Comments = append(pr.Comments, r.comment())
		}
	}
	slices.SortStableFunc(pr.Comments, func(a, b Comment) int { return strings.Compare(a.At, b.At) })
	for _, t := range p.ReviewThreads.Nodes {
		th := Thread{ID: t.ID, Path: t.Path, Old: t.DiffSide == "LEFT", Line: t.Line, OrigLine: t.OriginalLine,
			Resolved: t.IsResolved, Outdated: t.IsOutdated || t.Line == 0, Comments: []Comment{}}
		for _, c := range t.Comments.Nodes {
			if c.State == "PENDING" {
				continue
			}
			if th.Was == "" && c.DiffHunk != "" {
				lines := strings.Split(strings.TrimRight(c.DiffHunk, "\n"), "\n")
				if last := lines[len(lines)-1]; last != "" && !strings.HasPrefix(last, "@@") {
					th.Was = last[1:]
				}
			}
			th.Comments = append(th.Comments, c.comment())
		}
		if len(th.Comments) > 0 {
			pr.Threads = append(pr.Threads, th)
		}
	}
	if n := p.Head.Nodes; len(n) > 0 && n[0].Commit.StatusCheckRollup != nil {
		pr.CI = rollupCI(n[0].Commit.StatusCheckRollup.State)
		pr.rollup = n[0].Commit.StatusCheckRollup
	}
	return nil
}

// prState reads GitHub's and GitLab's states of a pull request: "open", "draft", "merged" or "closed".
func prState(state string, draft bool) string {
	switch strings.ToLower(state) {
	case "merged":
		return "merged"
	case "closed", "locked":
		return "closed"
	}
	if draft {
		return "draft"
	}
	return "open"
}

const gitlabPRQuery = `query($path: ID!, $iid: String!) {
  currentUser { username }
  project(fullPath: $path) {
    mergeRequest(iid: $iid) {
      iid title description webUrl state draft createdAt sourceBranch targetBranch diffHeadSha commitCount
      author { username } diffStatsSummary { additions deletions } diffRefs { baseSha headSha startSha }
      approvedBy { nodes { username } }
      reviewers { nodes { username mergeRequestInteraction { approved reviewState } } }
      discussions(first: 100) { nodes { id resolved resolvable notes(first: 50) { nodes {
        author { username } body createdAt url system
        position { filePath newPath oldPath newLine oldLine diffRefs { headSha } } } } } }
      headPipeline { ...pipeline jobs(first: 100) { nodes { ...job downstreamPipeline { status } } } }
    }
  }
}
fragment pipeline on Pipeline { iid status path duration stages { nodes { name } } }
fragment job on CiJob { id name status duration webPath kind schedulingType stage { name } needs { nodes { name } } }`

func parseGitlabPR(data []byte, pr *PR) error {
	var v struct {
		CurrentUser *struct{ Username string }
		Project     *struct {
			MergeRequest *struct {
				IID              string
				Title            string
				Description      string
				WebURL           string
				State            string
				Draft            bool
				CreatedAt        string
				SourceBranch     string
				TargetBranch     string
				DiffHeadSha      string
				CommitCount      int
				Author           *struct{ Username string }
				DiffStatsSummary *struct{ Additions, Deletions int }
				DiffRefs         *struct{ BaseSha, HeadSha, StartSha string }
				ApprovedBy       struct {
					Nodes []struct{ Username string }
				}
				Reviewers struct {
					Nodes []struct {
						Username                string
						MergeRequestInteraction *struct {
							Approved    bool
							ReviewState string
						}
					}
				}
				Discussions struct {
					Nodes []struct {
						ID         string
						Resolved   bool
						Resolvable bool
						Notes      struct {
							Nodes []struct {
								Author    *struct{ Username string }
								Body      string
								CreatedAt string
								URL       string
								System    bool
								Position  *struct {
									FilePath, NewPath, OldPath string
									NewLine, OldLine           int
									DiffRefs                   *struct{ HeadSha string }
								}
							}
						}
					}
				}
				HeadPipeline *gitlabPipeline
			}
		}
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	if v.Project == nil || v.Project.MergeRequest == nil {
		return errors.New("merge request not found")
	}
	m := v.Project.MergeRequest
	num, _ := strconv.Atoi(m.IID)
	*pr = PR{PRRow: PRRow{Number: num, Title: draftPrefixRe.ReplaceAllString(m.Title, ""), Draft: m.Draft, URL: webURL(m.WebURL),
		CreatedAt: m.CreatedAt, Head: m.SourceBranch, Base: m.TargetBranch},
		Body: m.Description, HeadOid: m.DiffHeadSha, Commits: m.CommitCount, Reviewers: []Reviewer{}, Comments: []Comment{}, Threads: []Thread{}}
	if v.CurrentUser != nil {
		pr.Me = v.CurrentUser.Username
	}
	if m.Author != nil {
		pr.Author = m.Author.Username
	}
	if s := m.DiffStatsSummary; s != nil {
		pr.Add, pr.Del = s.Additions, s.Deletions
	}
	if d := m.DiffRefs; d != nil {
		pr.MergeBase, pr.BaseOid = d.BaseSha, d.StartSha
		if pr.HeadOid == "" {
			pr.HeadOid = d.HeadSha
		}
	}
	pr.State = prState(m.State, m.Draft)
	asked := map[string]bool{}
	for _, r := range m.Reviewers.Nodes {
		st := "requested"
		if i := r.MergeRequestInteraction; i != nil {
			switch {
			case i.Approved || i.ReviewState == "APPROVED":
				st = "approved"
			case i.ReviewState == "REQUESTED_CHANGES":
				st = "changes"
			case i.ReviewState == "REVIEWED":
				st = "commented"
			}
		}
		asked[r.Username] = true
		pr.Reviewers = append(pr.Reviewers, Reviewer{r.Username, st})
	}
	for _, a := range m.ApprovedBy.Nodes {
		pr.Approvals++
		if !asked[a.Username] { // approved without being asked
			pr.Reviewers = append(pr.Reviewers, Reviewer{a.Username, "approved"})
		}
	}
	for _, d := range m.Discussions.Nodes {
		var th *Thread
		for _, n := range d.Notes.Nodes {
			if n.System {
				continue
			}
			c := Comment{Author: "ghost", At: n.CreatedAt, Body: n.Body, URL: webURL(n.URL)}
			if n.Author != nil {
				c.Author = n.Author.Username
			}
			if th == nil && n.Position != nil {
				p := n.Position
				th = &Thread{ID: d.ID, Path: cmp.Or(p.NewPath, p.FilePath), Line: p.NewLine, Resolved: d.Resolved, Comments: []Comment{}}
				if p.NewLine == 0 {
					th.Path, th.Line, th.Old = cmp.Or(p.OldPath, p.FilePath), p.OldLine, true
				}
				th.OrigLine = th.Line
				// GitLab has no outdated flag: a note written on another head may sit on lines that changed.
				if p.DiffRefs != nil && p.DiffRefs.HeadSha != pr.HeadOid {
					th.Outdated = true
				}
			}
			if th != nil {
				th.Comments = append(th.Comments, c)
			} else {
				pr.Comments = append(pr.Comments, c)
			}
		}
		if th != nil {
			pr.Threads = append(pr.Threads, *th)
		}
	}
	if m.HeadPipeline != nil {
		pr.CI = pipelineCI(m.HeadPipeline.Status)
		pr.pipeline = m.HeadPipeline
	}
	return nil
}
