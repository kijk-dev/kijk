package git

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kijk-dev/kijk/internal/gittest"
)

func groupsText(c *Checks) string {
	var s []string
	for _, g := range c.Groups {
		var js []string
		for _, j := range g.Jobs {
			js = append(js, fmt.Sprintf("%s=%s<-%s", j.Name, j.State, strings.Join(j.Needs, "+")))
		}
		s = append(s, fmt.Sprintf("%s #%s graph=%v: %s", g.Title, g.Number, g.Graph, strings.Join(js, " ")))
	}
	var o []string
	for _, j := range c.Other {
		o = append(o, j.Name+"="+j.State)
	}
	return strings.Join(s, "\n") + "\nother: " + strings.Join(o, " ")
}

const ciYAML = `name: CI
on: [push]
jobs:
  build:
    runs-on: ubuntu-latest
  test:
    needs: build
    strategy:
      matrix:
        os: [linux, windows]
  integration:
    needs: test
  deploy:
    name: deploy preview
    needs: [integration]
`

func checkRun(id int64, name, status, conclusion string, run int64, file, workflow string) string {
	suite := `{"app":{"slug":"vercel","name":"Vercel"},"workflowRun":null}`
	if run != 0 {
		suite = fmt.Sprintf(`{"app":{"slug":"github-actions","name":"GitHub Actions"},"workflowRun":{"databaseId":%d,"runNumber":%d,"url":"https://github.com/acme/shop/actions/runs/%d","file":{"path":%q},"workflow":{"name":%q}}}`, run, run%1000, run, file, workflow)
	}
	return fmt.Sprintf(`{"__typename":"CheckRun","databaseId":%d,"name":%q,"status":%q,"conclusion":%q,"startedAt":"2026-10-21T10:00:00Z","completedAt":"2026-10-21T10:02:05Z","detailsUrl":"https://github.com/acme/shop/actions/runs/1/job/%d","checkSuite":%s}`,
		id, name, status, conclusion, id, suite)
}

func TestGithubChecks(t *testing.T) {
	gittest.Isolate(t)
	repo := gittest.Repo(t)
	gittest.Write(t, filepath.Join(repo, ".github", "workflows", "ci.yml"), ciYAML)
	gittest.Run(t, repo, "add", ".")
	gittest.Run(t, repo, "commit", "-qm", "ci")
	head := rev(t, repo, "HEAD")
	contexts := []string{
		checkRun(1, "build", "COMPLETED", "SUCCESS", 1284, ".github/workflows/ci.yml", "CI"),
		checkRun(2, "test (linux)", "COMPLETED", "SUCCESS", 1284, ".github/workflows/ci.yml", "CI"),
		checkRun(3, "test (windows)", "COMPLETED", "FAILURE", 1284, ".github/workflows/ci.yml", "CI"),
		checkRun(4, "integration", "COMPLETED", "SKIPPED", 1284, ".github/workflows/ci.yml", "CI"),
		checkRun(5, "deploy preview", "WAITING", "", 1284, ".github/workflows/ci.yml", "CI"),
		checkRun(6, "analyze (go)", "IN_PROGRESS", "", 1283, ".github/workflows/codeql.yml", "CodeQL"),
		checkRun(7, "Vercel – Preview", "COMPLETED", "SUCCESS", 0, "", ""),
		`{"__typename":"StatusContext","context":"ci/legacy","state":"ERROR","targetUrl":"https://ci.example.com/1","description":"Build errored","createdAt":"2026-10-21T10:00:00Z"}`,
	}
	var r githubRollup
	if err := json.Unmarshal([]byte(`{"state":"FAILURE","contexts":{"nodes":[`+strings.Join(contexts, ",")+`]}}`), &r); err != nil {
		t.Fatal(err)
	}
	c := githubChecks(repo, head, &r)
	// The CodeQL workflow file is not at the head commit, so it is a list.
	want := `CI #284 graph=true: build=ok<- test (linux)=ok<-build test (windows)=bad<-build integration=skip<-test (linux)+test (windows) deploy preview=wait<-integration
CodeQL #283 graph=false: analyze (go)=run<-
other: Vercel – Preview=ok ci/legacy=bad`
	if got := groupsText(c); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	if j := c.Groups[0].Jobs[2]; j.ID != "3" || j.Time != "2m 05s" || j.URL == "" {
		t.Errorf("test (windows) = %+v", j)
	}
	if j := c.Other[0]; j.ID != "" {
		t.Errorf("another app's check has no log: %+v", j)
	}

	// A job the file does not name makes the whole workflow a list, never a wrong graph.
	r.Contexts.Nodes = append(r.Contexts.Nodes, json.RawMessage(checkRun(9, "surprise", "COMPLETED", "SUCCESS", 1284, ".github/workflows/ci.yml", "CI")))
	if g := githubChecks(repo, head, &r).Groups[0]; g.Graph || len(g.Jobs[1].Needs) > 0 {
		t.Errorf("with an unknown job: %+v", g)
	}
}

// The PR line and the Overview read a check the same way: a cancelled job is not a pass.
func TestCheckStates(t *testing.T) {
	for _, c := range []struct{ status, conclusion, want string }{
		{"COMPLETED", "SUCCESS", "ok"}, {"COMPLETED", "NEUTRAL", "ok"}, {"COMPLETED", "FAILURE", "bad"},
		{"COMPLETED", "TIMED_OUT", "bad"}, {"COMPLETED", "CANCELLED", "cancel"}, {"COMPLETED", "SKIPPED", "skip"},
		{"COMPLETED", "STALE", "skip"}, {"IN_PROGRESS", "", "run"}, {"QUEUED", "", "wait"}, {"", "", "wait"},
	} {
		if got := runState(c.status, c.conclusion); got != c.want {
			t.Errorf("runState(%s, %s) = %s, want %s", c.status, c.conclusion, got, c.want)
		}
	}
	// A job still cancelling still runs.
	for status, want := range map[string]string{"CANCELED": "cancel", "canceling": "run", "SKIPPED": "skip", "MANUAL": "wait"} {
		if got := jobState(status); got != want {
			t.Errorf("jobState(%s) = %s, want %s", status, got, want)
		}
	}
	p, _ := parseGithubPR([]byte(`{"number":1,"state":"OPEN","statusCheckRollup":[
		{"__typename":"CheckRun","name":"build","status":"COMPLETED","conclusion":"SUCCESS"},
		{"__typename":"CheckRun","name":"test","status":"COMPLETED","conclusion":"CANCELLED"}]}`))
	if p.CI != "failed" || len(p.Failed) != 1 || p.Failed[0] != "test" {
		t.Errorf("the PR line with a cancelled job: %s %v", p.CI, p.Failed)
	}
	// A GitLab pipeline reads the same: cancelled is not a pass, cancelling still runs.
	for status, want := range map[string]string{"canceled": "failed", "CANCELED": "failed", "canceling": "running", "skipped": ""} {
		if got := pipelineCI(status); got != want {
			t.Errorf("pipelineCI(%s) = %q, want %q", status, got, want)
		}
	}
}

func TestGitlabChecks(t *testing.T) {
	var p gitlabPipeline
	err := json.Unmarshal([]byte(`{"iid":"9123","status":"FAILED","path":"/acme/shop/-/pipelines/77","duration":370,
	"stages":{"nodes":[{"name":"build"},{"name":"test"},{"name":"deploy"}]},
	"jobs":{"nodes":[
	 {"id":"gid://gitlab/Ci::Build/1","name":"build","status":"SUCCESS","duration":72,"webPath":"/acme/shop/-/jobs/1","kind":"BUILD","schedulingType":"stage","stage":{"name":"build"},"needs":{"nodes":[]}},
	 {"id":"gid://gitlab/Ci::Build/2","name":"lint","status":"RUNNING","duration":null,"webPath":"/acme/shop/-/jobs/2","kind":"BUILD","schedulingType":"stage","stage":{"name":"build"},"needs":{"nodes":[]}},
	 {"id":"gid://gitlab/Ci::Build/3","name":"test:linux","status":"SUCCESS","duration":220,"webPath":"/acme/shop/-/jobs/3","kind":"BUILD","schedulingType":"dag","stage":{"name":"test"},"needs":{"nodes":[{"name":"build"},{"name":"gone"}]}},
	 {"id":"gid://gitlab/Ci::Build/4","name":"test:windows","status":"FAILED","duration":125,"webPath":"/acme/shop/-/jobs/4","kind":"BUILD","schedulingType":"stage","stage":{"name":"test"},"needs":{"nodes":[]}},
	 {"id":"gid://gitlab/Ci::Bridge/5","name":"docs","status":"SUCCESS","duration":3,"webPath":"/acme/shop/-/jobs/5","kind":"BRIDGE","schedulingType":"stage","stage":{"name":"deploy"},"needs":{"nodes":[]},"downstreamPipeline":{"status":"FAILED"}},
	 {"id":"gid://gitlab/Ci::Build/6","name":"deploy:preview","status":"MANUAL","duration":null,"webPath":"/acme/shop/-/jobs/6","kind":"BUILD","schedulingType":"dag","stage":{"name":"deploy"},"needs":{"nodes":[{"name":"test:linux"}]}}
	]}}`), &p)
	if err != nil {
		t.Fatal(err)
	}
	c := gitlabChecks(&p, &forge{Host: "gitlab.com"})
	want := `Pipeline #9123 graph=true: build=ok<- lint=run<- test:linux=ok<-build test:windows=bad<-build+lint docs=bad<-test:linux+test:windows deploy:preview=wait<-test:linux
other: `
	if got := groupsText(c); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	g := c.Groups[0]
	if g.URL != "https://gitlab.com/acme/shop/-/pipelines/77" || g.Jobs[3].ID != "/acme/shop/-/jobs/4" || g.Jobs[3].Time != "2m 05s" || g.Jobs[3].Stage != "test" {
		t.Errorf("group %+v", g)
	}
	if g.Jobs[4].ID != "" {
		t.Errorf("a bridge job has no log of its own: %+v", g.Jobs[4])
	}
}

func TestJobLog(t *testing.T) {
	gittest.Isolate(t)
	repo := gittest.Repo(t)
	gittest.Run(t, repo, "remote", "add", "origin", "git@github.com:acme/shop.git")
	var log strings.Builder
	for i := 1; i <= 40; i++ {
		fmt.Fprintf(&log, "2026-10-21T10:00:%02d.1234567Z line %d\n", i%60, i)
	}
	log.WriteString("2026-10-21T10:01:00.0000000Z \x1b[31m--- FAIL: TestBackoff\x1b[0m\n")
	args := fakeTool(t, "gh", log.String())
	lines, err := JobLog(repo, "3")
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 30 || lines[0] != "line 12" || lines[29] != "--- FAIL: TestBackoff" {
		t.Errorf("lines %q", lines)
	}
	// A log holds color codes, which gh prints only when asked to; kijk cuts them off itself.
	if a := strings.Join(args(), " "); a != "api --hostname github.com --allow-escape-sequences repos/acme/shop/actions/jobs/3/logs" {
		t.Errorf("gh was called with %s", a)
	}
	for _, bad := range []string{"", "3; rm", "../1", "/acme/shop/-/jobs/1"} {
		if _, err := JobLog(repo, bad); err == nil {
			t.Errorf("JobLog(%q) should fail", bad)
		}
	}
}

// A gh from before --allow-escape-sequences gets the call again without it.
func TestJobLogOldGh(t *testing.T) {
	gittest.Isolate(t)
	repo := gittest.Repo(t)
	gittest.Run(t, repo, "remote", "add", "origin", "git@github.com:acme/shop.git")
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "gh"), []byte("#!/bin/sh\ncase \"$*\" in *--allow-escape-sequences*) echo 'unknown flag: --allow-escape-sequences' >&2; exit 1 ;; esac\necho 'line one'\n"), 0o755)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if lines, err := JobLog(repo, "3"); err != nil || strings.Join(lines, "|") != "line one" {
		t.Errorf("lines %q %v", lines, err)
	}
}

func TestJobLogGitlab(t *testing.T) {
	gittest.Isolate(t)
	repo := gittest.Repo(t)
	gittest.Run(t, repo, "remote", "add", "origin", "https://gitlab.com/acme/shop.git")
	args := fakeTool(t, "glab", "section_start:1700000000:step_script\r\x1b[0K\x1b[32;1m$ go test\x1b[0;m\nok\nFAIL shop 0.4s\r\nsection_end:1700000001:step_script\r\x1b[0K\n")
	lines, err := JobLog(repo, "/acme/fork/-/jobs/42")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(lines, "|") != "$ go test|ok|FAIL shop 0.4s" {
		t.Errorf("lines %q", lines)
	}

	// A newer runner starts each line with a time and the stream it came from.
	fakeTool(t, "glab", "2026-10-21T16:30:54.432218Z 00O+section_start:1792600254:cleanup\r\x1b[0K\n2026-10-21T16:30:54.432222Z 00O+\x1b[36;1mCleaning up\x1b[0;m\n2026-10-21T16:30:54.919476Z 00E \x1b[31;1mERROR: Job failed\x1b[0;m\n")
	if lines, _ := JobLog(repo, "/acme/fork/-/jobs/42"); strings.Join(lines, "|") != "Cleaning up|ERROR: Job failed" {
		t.Errorf("lines with times %q", lines)
	}
	if a := strings.Join(args(), " "); a != "api --hostname gitlab.com projects/acme%2Ffork/jobs/42/trace" {
		t.Errorf("glab was called with %s", a)
	}
}
