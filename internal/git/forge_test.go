package git

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/kijk-dev/kijk/internal/gittest"
)

func TestParseRemote(t *testing.T) {
	for raw, want := range map[string]string{
		"git@github.com:acme/kijk.git":                   "github.com acme/kijk",
		"https://gitlab.com/group/sub/api.git":           "gitlab.com group/sub/api",
		"ssh://git@gitlab.example.com:2222/platform/api": "gitlab.example.com platform/api",
		"https://user@code.example.org/team/api.git/":    "code.example.org team/api",
		"/Users/me/origin.git":                           "",
		"git@github.com:lonely":                          "",
	} {
		host, path, ok := parseRemote(raw)
		got := ""
		if ok {
			got = host + " " + path
		}
		if got != want {
			t.Errorf("parseRemote(%q) = %q, want %q", raw, got, want)
		}
	}
}

// An origin written with a short alias names its forge after the user's insteadOf rewrite.
func TestOriginURL(t *testing.T) {
	gittest.Isolate(t)
	repo := gittest.Repo(t)
	gittest.Run(t, repo, "remote", "add", "origin", "gh:Acme/Shop")
	gittest.Run(t, repo, "config", "url.git@github.com:.insteadOf", "gh:")
	if host, path, ok := OriginURL(repo); !ok || host != "github.com" || path != "Acme/Shop" {
		t.Errorf("OriginURL = %q %q %v", host, path, ok)
	}
	if host, _, _ := Origin(repo); host != "gh" {
		t.Errorf("Origin = %q, want the origin as written", host)
	}
	if _, _, ok := OriginURL(gittest.Repo(t)); ok {
		t.Error("a repo with no origin has one")
	}
}

func TestLoggedInTo(t *testing.T) {
	f := filepath.Join(t.TempDir(), "config.yml")
	os.WriteFile(f, []byte("hosts:\n    code.example.org:\n        token: x\n"), 0o600)
	if !loggedInTo("code.example.org", []string{f}) || loggedInTo("example.org", []string{f}) {
		t.Error("host key lookup is wrong")
	}
}

func TestParseGithubPR(t *testing.T) {
	p, err := parseGithubPR([]byte(`{"number":42,"title":"Add helper","state":"OPEN","isDraft":false,
		"url":"https://github.com/o/r/pull/42","baseRefName":"release/2.x","reviewDecision":"",
		"statusCheckRollup":[
			{"__typename":"CheckRun","name":"build","status":"COMPLETED","conclusion":"SUCCESS"},
			{"__typename":"CheckRun","name":"lint","status":"COMPLETED","conclusion":"SKIPPED"},
			{"__typename":"CheckRun","name":"test","status":"COMPLETED","conclusion":"FAILURE"},
			{"__typename":"CheckRun","name":"e2e","status":"IN_PROGRESS","conclusion":""},
			{"__typename":"StatusContext","context":"ci/legacy","state":"ERROR"}],
		"latestReviews":[{"state":"APPROVED"},{"state":"COMMENTED"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	got := fmt.Sprintf("%d %s %s %s %v %s %q %d", p.Number, p.State, p.Target, p.CI, p.Failed, p.CIURL, p.Review, p.Approvals)
	if want := `42 open release/2.x failed [test ci/legacy] https://github.com/o/r/pull/42/checks "" 1`; got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}

	p, _ = parseGithubPR([]byte(`{"state":"OPEN","isDraft":true,"url":"javascript:x","reviewDecision":"REVIEW_REQUIRED",
		"statusCheckRollup":[{"__typename":"CheckRun","status":"QUEUED"}]}`))
	if p.State != "draft" || p.CI != "running" || p.Review != "needed" || p.URL != "" || p.CIURL != "" {
		t.Errorf("draft = %+v", p)
	}
}

func TestParseGitlabMR(t *testing.T) {
	p, err := parseGitlabMR([]byte(`{"iid":7,"title":"Draft: Add helper","state":"opened","draft":true,
		"web_url":"https://gitlab.com/g/r/-/merge_requests/7","target_branch":"main",
		"head_pipeline":{"status":"running","web_url":"https://gitlab.com/g/r/-/pipelines/9"}}`))
	if err != nil {
		t.Fatal(err)
	}
	parseGitlabApprovals([]byte(`{"approved":false,"approvals_left":1,"approved_by":[{}]}`), p)
	got := fmt.Sprintf("%d %q %s %s %s %s %d", p.Number, p.Title, p.State, p.CI, p.CIURL, p.Review, p.Approvals)
	if want := `7 "Add helper" draft running https://gitlab.com/g/r/-/pipelines/9 needed 1`; got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestCreateURLAndNotes(t *testing.T) {
	gh := &forge{Kind: "github", Web: "https://github.com/o/r"}
	gl := &forge{Kind: "gitlab", Web: "https://gitlab.com/g/r"}
	if u := createURL(gh, "feat/a b"); u != "https://github.com/o/r/compare/feat/a%20b?expand=1" {
		t.Errorf("github create = %s", u)
	}
	if u := createURL(gl, "feat/x"); u != "https://gitlab.com/g/r/-/merge_requests/new?merge_request%5Bsource_branch%5D=feat%2Fx" {
		t.Errorf("gitlab create = %s", u)
	}
	if n := toolNote("gh", errNoPR); n != "" {
		t.Errorf("no PR is not a failure: %q", n)
	}
	if n := toolNote("glab", errors.New("401 Unauthorized")); n != "Log in with glab auth login to see the status" {
		t.Errorf("auth note = %q", n)
	}
}

// Each forge names a pull request in its own words: GitLab's is a merge request, !12.
func TestForgeWords(t *testing.T) {
	gh, gl := &forge{Kind: "github"}, &forge{Kind: "gitlab"}
	if got := gh.Word() + " " + gh.Ref(12) + ", " + gl.Word() + " " + gl.Ref(12); got != "pull request #12, merge request !12" {
		t.Errorf("got %q", got)
	}
}
