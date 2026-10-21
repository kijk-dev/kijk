package git

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// A forge is the GitHub or GitLab project behind origin. Pull request status comes from the gh or
// glab tool the user is logged in to, so kijk never holds a token.
type forge struct {
	Kind string `json:"kind"` // "github" or "gitlab"
	Host string `json:"host"`
	Path string `json:"path"` // owner/repo; on GitLab also group/subgroup/repo
	Web  string `json:"web"`
}

type pullRequest struct {
	Number    int      `json:"number"`
	Title     string   `json:"title"`
	URL       string   `json:"url"`
	State     string   `json:"state"`        // "open", "draft", "merged" or "closed"
	Target    string   `json:"target"`       // the branch it merges into
	CI        string   `json:"ci,omitempty"` // "passed", "failed" or "running"; empty when there is no CI
	CIURL     string   `json:"ciUrl,omitempty"`
	Failed    []string `json:"failed,omitempty"` // failed checks, by name
	Review    string   `json:"review,omitempty"` // "approved", "changes" or "needed"
	Approvals int      `json:"approvals"`
}

type prStatus struct {
	Forge   *forge       `json:"forge,omitempty"`   // empty when origin is not on a known host
	Tool    string       `json:"tool,omitempty"`    // "gh" or "glab"
	Note    string       `json:"note,omitempty"`    // why there is no status, such as a missing tool
	Default string       `json:"default,omitempty"` // the default branch, to tell whether a PR targets another one
	Create  string       `json:"create,omitempty"`  // the page that opens a new PR for this branch
	PR      *pullRequest `json:"pr,omitempty"`
}

var scpRemoteRe = regexp.MustCompile(`^(?:[^@/]+@)?([^:/]+):(.+)$`)

// parseRemote reads host and project path from https, ssh and scp-style ("git@host:path") URLs.
func parseRemote(raw string) (host, path string, ok bool) {
	raw = strings.TrimSpace(raw)
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		host, path = u.Hostname(), u.Path
	} else if m := scpRemoteRe.FindStringSubmatch(raw); m != nil {
		host, path = m[1], m[2]
	} else {
		return "", "", false
	}
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	return host, path, host != "" && strings.Contains(path, "/")
}

func detectForge(root string) *forge {
	raw, err := Run(root, "config", "--get", "remote.origin.url") // as written, before any insteadOf rewrite
	if err != nil {
		return nil
	}
	host, path, ok := parseRemote(raw)
	if !ok {
		return nil
	}
	f := &forge{Host: host, Path: path, Web: "https://" + host + "/" + path}
	switch {
	case strings.Contains(host, "github"):
		f.Kind = "github"
	case strings.Contains(host, "gitlab"):
		f.Kind = "gitlab"
	case loggedInTo(host, ghHostFiles()):
		f.Kind = "github"
	case loggedInTo(host, glabHostFiles()):
		f.Kind = "gitlab"
	default:
		return nil
	}
	return f
}

// loggedInTo looks for the host as a key in gh's or glab's own config, which works for any domain.
func loggedInTo(host string, files []string) bool {
	key := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(host) + `:\s*$`)
	for _, f := range files {
		if b, err := os.ReadFile(f); err == nil && key.Match(b) {
			return true
		}
	}
	return false
}

func configDirs(envDir, name string) []string {
	if d := os.Getenv(envDir); d != "" {
		return []string{d}
	}
	var dirs []string
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		dirs = append(dirs, filepath.Join(d, name))
	}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".config", name))
	}
	if d, err := os.UserConfigDir(); err == nil {
		dirs = append(dirs, filepath.Join(d, name))
	}
	return dirs
}

func ghHostFiles() []string {
	var files []string
	for _, d := range append(configDirs("GH_CONFIG_DIR", "gh"), configDirs("GH_CONFIG_DIR", "GitHub CLI")...) {
		files = append(files, filepath.Join(d, "hosts.yml"))
	}
	return files
}

func glabHostFiles() []string {
	var files []string
	for _, d := range configDirs("GLAB_CONFIG_DIR", "glab-cli") {
		files = append(files, filepath.Join(d, "config.yml"))
	}
	return files
}

// PRFor looks up the pull or merge request of the branch HEAD is on.
func PRFor(root string) prStatus {
	res := prStatus{Forge: detectForge(root)}
	if res.Forge == nil {
		return res
	}
	res.Default = strings.TrimPrefix(mainRef(root), "origin/")
	branch := prBranch(root)
	if branch == "" {
		return res
	}
	res.Create = createURL(res.Forge, branch)
	res.Tool = map[string]string{"github": "gh", "gitlab": "glab"}[res.Forge.Kind]
	if _, err := exec.LookPath(res.Tool); err != nil {
		res.Note = "Install " + res.Tool + " and log in to see " + map[string]string{"gh": "pull request", "glab": "merge request"}[res.Tool] + " status"
		return res
	}
	var err error
	if res.Tool == "gh" {
		res.PR, err = githubPR(res.Forge, branch)
	} else {
		res.PR, err = gitlabPR(res.Forge, branch)
	}
	if err != nil {
		res.Note = toolNote(res.Tool, err)
	}
	if res.Note == "" {
		knownPRs.Store(root+"\n"+branch, res.PR)
	}
	return res
}

// knownPRs keeps the last pull request PRFor found per folder and branch, so other views can show
// it without asking the forge again.
var knownPRs sync.Map

// KnownPR is the pull request of the branch HEAD is on, as PRFor last found it; nil when unknown.
func KnownPR(root string) *pullRequest {
	p, _ := knownPRs.Load(root + "\n" + prBranch(root))
	pr, _ := p.(*pullRequest)
	return pr
}

// prBranch is the branch name on the remote: the upstream's, else the local one.
func prBranch(root string) string {
	if up, err := Run(root, "rev-parse", "--abbrev-ref", "@{upstream}"); err == nil {
		if remote, name, ok := strings.Cut(up, "/"); ok && remote == "origin" {
			return name
		}
	}
	b, _ := Run(root, "symbolic-ref", "--short", "-q", "HEAD")
	return b
}

func createURL(f *forge, branch string) string {
	if f.Kind == "github" {
		parts := strings.Split(branch, "/")
		for i, p := range parts {
			parts[i] = url.PathEscape(p)
		}
		return f.Web + "/compare/" + strings.Join(parts, "/") + "?expand=1"
	}
	return f.Web + "/-/merge_requests/new?" + url.Values{"merge_request[source_branch]": {branch}}.Encode()
}

var errNoPR = errors.New("no pull request")

// toolNote turns a gh or glab failure into a short line; a missing PR is not a failure.
func toolNote(tool string, err error) string {
	if errors.Is(err, errNoPR) {
		return ""
	}
	msg := strings.ToLower(err.Error())
	for _, s := range []string{"auth login", "not logged", "authenticat", "401", "unauthorized"} {
		if strings.Contains(msg, s) {
			return "Log in with " + tool + " auth login to see the status"
		}
	}
	first, _, _ := strings.Cut(strings.TrimSpace(err.Error()), "\n")
	return tool + ": " + first
}

func runTool(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1", "NO_PROMPT=1", "NO_COLOR=1", "GH_NO_UPDATE_NOTIFIER=1", "GLAB_CHECK_UPDATE=false")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return nil, errors.New("no answer within 20 seconds")
	}
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		low := strings.ToLower(msg)
		if strings.Contains(low, "no pull requests found") || strings.Contains(low, "no open merge request") {
			return nil, errNoPR
		}
		if msg == "" {
			msg = err.Error()
		}
		return nil, errors.New(msg)
	}
	return out, nil
}

func githubPR(f *forge, branch string) (*pullRequest, error) {
	out, err := runTool("gh", "pr", "view", branch, "--repo", f.Host+"/"+f.Path,
		"--json", "number,title,state,isDraft,url,baseRefName,reviewDecision,statusCheckRollup,latestReviews")
	if err != nil {
		return nil, err
	}
	return parseGithubPR(out)
}

func parseGithubPR(out []byte) (*pullRequest, error) {
	var v struct {
		Number            int
		Title             string
		State             string
		IsDraft           bool
		URL               string
		BaseRefName       string
		ReviewDecision    string
		StatusCheckRollup []struct {
			Name, Context, Status, Conclusion, State string
		}
		LatestReviews []struct{ State string }
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return nil, err
	}
	p := &pullRequest{Number: v.Number, Title: v.Title, URL: webURL(v.URL), Target: v.BaseRefName}
	switch {
	case v.State == "MERGED":
		p.State = "merged"
	case v.State == "CLOSED":
		p.State = "closed"
	case v.IsDraft:
		p.State = "draft"
	default:
		p.State = "open"
	}
	running := false
	for _, c := range v.StatusCheckRollup {
		name := c.Name + c.Context // a check run has a name, a commit status a context
		switch {
		case c.Status != "" && c.Status != "COMPLETED", c.State == "PENDING", c.State == "EXPECTED":
			running = true
		case slices.Contains([]string{"FAILURE", "TIMED_OUT", "CANCELLED", "ACTION_REQUIRED", "STARTUP_FAILURE"}, c.Conclusion),
			c.State == "FAILURE", c.State == "ERROR":
			p.Failed = append(p.Failed, name)
		}
	}
	switch {
	case len(p.Failed) > 0:
		p.CI = "failed"
	case running:
		p.CI = "running"
	case len(v.StatusCheckRollup) > 0:
		p.CI = "passed"
	}
	if p.CI != "" && p.URL != "" {
		p.CIURL = p.URL + "/checks"
	}
	for _, r := range v.LatestReviews {
		if r.State == "APPROVED" {
			p.Approvals++
		}
	}
	p.Review = map[string]string{"APPROVED": "approved", "CHANGES_REQUESTED": "changes", "REVIEW_REQUIRED": "needed"}[v.ReviewDecision]
	return p, nil
}

func gitlabPR(f *forge, branch string) (*pullRequest, error) {
	out, err := runTool("glab", "mr", "view", branch, "--repo", f.Web, "--output", "json")
	if err != nil {
		return nil, err
	}
	p, err := parseGitlabMR(out)
	if err != nil {
		return nil, err
	}
	endpoint := "projects/" + url.PathEscape(f.Path) + "/merge_requests/" + strconv.Itoa(p.Number) + "/approvals"
	if out, err := runTool("glab", "api", "--hostname", f.Host, endpoint); err == nil {
		parseGitlabApprovals(out, p)
	}
	return p, nil
}

var draftPrefixRe = regexp.MustCompile(`(?i)^\s*(draft:|\[draft\]|\(draft\)|wip:)\s*`)

func parseGitlabMR(out []byte) (*pullRequest, error) {
	var v struct {
		IID          int    `json:"iid"`
		Title        string `json:"title"`
		State        string `json:"state"`
		Draft        bool   `json:"draft"`
		WebURL       string `json:"web_url"`
		TargetBranch string `json:"target_branch"`
		HeadPipeline *struct {
			Status string `json:"status"`
			WebURL string `json:"web_url"`
		} `json:"head_pipeline"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return nil, err
	}
	// The Draft chip says it already, so the title does not repeat it.
	p := &pullRequest{Number: v.IID, Title: draftPrefixRe.ReplaceAllString(v.Title, ""), URL: webURL(v.WebURL), Target: v.TargetBranch}
	switch {
	case v.State == "merged":
		p.State = "merged"
	case v.State == "closed" || v.State == "locked":
		p.State = "closed"
	case v.Draft:
		p.State = "draft"
	default:
		p.State = "open"
	}
	if pl := v.HeadPipeline; pl != nil {
		switch pl.Status {
		case "success":
			p.CI = "passed"
		case "failed":
			p.CI = "failed"
		case "running", "pending", "created", "preparing", "waiting_for_resource", "scheduled":
			p.CI = "running"
		}
		if p.CI != "" {
			p.CIURL = webURL(pl.WebURL)
		}
	}
	return p, nil
}

func parseGitlabApprovals(out []byte, p *pullRequest) {
	var v struct {
		Approved      bool       `json:"approved"`
		ApprovalsLeft int        `json:"approvals_left"`
		ApprovedBy    []struct{} `json:"approved_by"`
	}
	if json.Unmarshal(out, &v) != nil {
		return
	}
	p.Approvals = len(v.ApprovedBy)
	switch {
	case v.ApprovalsLeft > 0:
		p.Review = "needed"
	case v.Approved && p.Approvals > 0:
		p.Review = "approved"
	}
}

// webURL keeps only web links, so a tool's output cannot put another kind of link in the page.
func webURL(s string) string {
	if strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://") {
		return s
	}
	return ""
}
