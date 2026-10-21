package viewer

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kijk-dev/kijk/internal/gittest"
)

func revParse(t *testing.T, dir, ref string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--verify", "-q", ref+"^{commit}").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// prRepo is a clone whose origin looks like GitHub but is a bare repo here. Pull request 7 is in
// refs/pull/7/head only, as from a fork, and changes retry.go, which the clone has as on main.
func prRepo(t *testing.T) (clone, head, base string) {
	t.Helper()
	gittest.Isolate(t)
	bare := filepath.Join(t.TempDir(), "shop.git")
	gittest.Run(t, t.TempDir(), "init", "-q", "--bare", "-b", "main", bare)
	work := gittest.Repo(t)
	gittest.Write(t, filepath.Join(work, "retry.go"), "package shop\n\nfunc retry() {\n}\n")
	gittest.Run(t, work, "add", ".")
	gittest.Run(t, work, "commit", "-qm", "Start")
	gittest.Run(t, work, "remote", "add", "origin", bare)
	gittest.Run(t, work, "push", "-q", "origin", "main")
	gittest.Run(t, work, "checkout", "-qb", "retry")
	gittest.Write(t, filepath.Join(work, "retry.go"), "package shop\n\nfunc retry() {\n\tfor i := 0; i < 3; i++ {\n\t}\n}\n")
	gittest.Run(t, work, "commit", "-qam", "Retry")
	gittest.Run(t, work, "push", "-q", "origin", "retry:refs/pull/7/head")
	clone = filepath.Join(t.TempDir(), "shop")
	gittest.Run(t, t.TempDir(), "clone", "-q", bare, clone)
	gittest.Run(t, clone, "remote", "set-url", "origin", "https://github.com/acme/shop.git")
	gittest.Run(t, clone, "config", "url."+bare+".insteadOf", "https://github.com/acme/shop.git")
	return clone, revParse(t, work, "retry"), revParse(t, work, "main")
}

// fakeGh answers the list, the pull request and a job log, and fails anything else.
func fakeGh(t *testing.T, list, pr, log string) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{"list.json": list, "pr.json": pr, "log.txt": log} {
		os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644)
	}
	script := fmt.Sprintf(`#!/bin/sh
case "$*" in
*"pullRequests(states"*) cat %[1]q/list.json ;;
*"pullRequest(number"*) cat %[1]q/pr.json ;;
*/actions/jobs/*) cat %[1]q/log.txt ;;
*) echo "unknown call: $*" >&2; exit 1 ;;
esac
`, dir)
	os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func prAnswer(state, head, base string) string {
	return fmt.Sprintf(`{"data":{"viewer":{"login":"you"},"repository":{"pullRequest":{
"number":7,"title":"Retry three times","body":"Gives up after **one** try.","url":"https://github.com/acme/shop/pull/7","state":%q,"isDraft":false,
"createdAt":"2026-10-19T09:00:00Z","headRefName":"retry","baseRefName":"main","headRefOid":%q,"baseRefOid":%q,"additions":3,"deletions":0,
"author":{"login":"sanne"},"commits":{"totalCount":1},"reviewRequests":{"nodes":[]},"latestReviews":{"nodes":[]},"reviews":{"nodes":[]},
"comments":{"nodes":[{"author":{"login":"tim"},"body":"See <script>x</script> and `+"`code`"+`","createdAt":"2026-10-20T08:00:00Z","url":""}]},
"reviewThreads":{"nodes":[{"id":"T1","isResolved":false,"isOutdated":false,"path":"retry.go","line":4,"originalLine":4,"diffSide":"RIGHT",
 "comments":{"nodes":[{"author":{"login":"tim"},"body":"Why **3**?","createdAt":"2026-10-20T08:00:00Z","url":"","state":"SUBMITTED","diffHunk":""}]}}]},
"head":{"nodes":[{"commit":{"oid":%q,"statusCheckRollup":{"state":"SUCCESS","contexts":{"nodes":[]}}}}]}}}}}`, state, head, base, head)
}

const listAnswer = `{"data":{"viewer":{"login":"you"},"repository":{"pullRequests":{"totalCount":1,"nodes":[{"number":7,"title":"Retry three times","isDraft":false,"url":"https://github.com/acme/shop/pull/7","createdAt":"2026-10-19T09:00:00Z","headRefName":"retry","baseRefName":"main","additions":3,"deletions":0,"author":{"login":"sanne"},"commits":{"nodes":[]}}]}},"asked":{"nodes":[{"number":7}]}}}`

func prServer(t *testing.T, root string) func(route string, q url.Values) *httptest.ResponseRecorder {
	rts := &Roots{}
	rts.SetFolders([]string{root})
	srv := http.NewServeMux()
	cache := t.TempDir()
	Register(srv, rts, Options{CacheDir: func() (string, error) { return cache, nil }})
	return func(route string, q url.Values) *httptest.ResponseRecorder {
		q.Set("root", root)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, httptest.NewRequest("GET", route+"?"+q.Encode(), nil))
		return w
	}
}

func TestPRListRoute(t *testing.T) {
	clone, head, base := prRepo(t)
	fakeGh(t, listAnswer, prAnswer("OPEN", head, base), "")
	gittest.Run(t, clone, "update-ref", "refs/kijk/pr/5", "HEAD")
	get := prServer(t, clone)
	w := get("/api/prs", url.Values{})
	var res struct {
		Me    string
		Total int
		PRs   []struct {
			Number    int
			Requested bool
		}
		Forge struct{ Kind string }
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); w.Code != 200 || err != nil {
		t.Fatalf("%d %v %s", w.Code, err, w.Body.String())
	}
	if res.Me != "you" || res.Total != 1 || len(res.PRs) != 1 || !res.PRs[0].Requested || res.Forge.Kind != "github" {
		t.Errorf("list %+v", res)
	}
	// The list has every open pull request, so the ref of #5, which closed, goes.
	if revParse(t, clone, "refs/kijk/pr/5") != "" {
		t.Error("refs/kijk/pr/5 is still there")
	}
}

type prView struct {
	Number   int
	State    string
	BodyHTML string `json:"bodyHtml"`
	Split    string
	At       string
	Error    string
	Files    []struct{ Path, Status string }
	Comments []struct{ HTML string }
	Threads  []struct {
		Line int
		Text string
	}
	Checks *struct{ Groups []any }
}

func TestPRViewRoute(t *testing.T) {
	clone, head, base := prRepo(t)
	fakeGh(t, listAnswer, prAnswer("OPEN", head, base), "")
	get := prServer(t, clone)

	if w := get("/api/diff", url.Values{"path": {"retry.go"}, "mode": {"pr"}, "pr": {"7"}}); w.Code != http.StatusConflict {
		t.Fatalf("a diff before the pull request is open: %d %s", w.Code, w.Body.String())
	}
	w := get("/api/pr/view", url.Values{"n": {"7"}})
	var v prView
	if err := json.Unmarshal(w.Body.Bytes(), &v); w.Code != 200 || err != nil {
		t.Fatalf("%d %v %s", w.Code, err, w.Body.String())
	}
	if v.Error != "" || v.At != head || v.Split != base || len(v.Files) != 1 || v.Files[0].Path != "retry.go" {
		t.Fatalf("view %+v", v)
	}
	if revParse(t, clone, "refs/kijk/pr/7") != head {
		t.Error("the head was not fetched")
	}
	if !strings.Contains(v.BodyHTML, "<strong>one</strong>") || strings.Contains(v.Comments[0].HTML, "<script>") || !strings.Contains(v.Comments[0].HTML, "<code>code</code>") {
		t.Errorf("rendered %q %q", v.BodyHTML, v.Comments[0].HTML)
	}
	// Local mode places a thread by the text of its line at the head.
	if len(v.Threads) != 1 || v.Threads[0].Text != "\tfor i := 0; i < 3; i++ {" {
		t.Errorf("threads %+v", v.Threads)
	}
	if v.Checks == nil {
		t.Error("no checks")
	}

	w = get("/api/diff", url.Values{"path": {"retry.go"}, "mode": {"pr"}, "pr": {"7"}})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `{"k":"+","n":4,"t":"\tfor i := 0; i `) {
		t.Fatalf("diff %d %s", w.Code, w.Body.String())
	}
	// The whole file at the pull request's head, while the checkout has main's version.
	w = get("/api/lines", url.Values{"path": {"retry.go"}, "pr": {"7"}, "from": {"0"}, "to": {"10"}})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `["o","++"]`) {
		t.Fatalf("lines %d %s", w.Code, w.Body.String())
	}
	if w := get("/api/file", url.Values{"path": {"retry.go"}, "pr": {"7"}}); w.Code != 200 || !strings.Contains(w.Body.String(), `"lines":6`) {
		t.Fatalf("file %d %s", w.Code, w.Body.String())
	}
	for _, q := range []url.Values{
		{"path": {"retry.go"}, "pr": {"HEAD~1"}},
		{"path": {"retry.go"}, "pr": {"8"}},
		{"path": {"../x"}, "pr": {"7"}},
	} {
		if w := get("/api/lines", q); w.Code == 200 {
			t.Errorf("lines %v: %d %s", q, w.Code, w.Body.String())
		}
	}
}

func TestPRViewClosed(t *testing.T) {
	clone, head, base := prRepo(t)
	fakeGh(t, listAnswer, prAnswer("MERGED", head, base), "")
	gittest.Run(t, clone, "update-ref", "refs/kijk/pr/7", "HEAD")
	get := prServer(t, clone)
	var v prView
	w := get("/api/pr/view", url.Values{"n": {"7"}})
	if err := json.Unmarshal(w.Body.Bytes(), &v); w.Code != 200 || err != nil {
		t.Fatalf("%d %v %s", w.Code, err, w.Body.String())
	}
	if v.State != "merged" || len(v.Files) != 0 || revParse(t, clone, "refs/kijk/pr/7") != "" {
		t.Errorf("merged: %+v, ref %q", v, revParse(t, clone, "refs/kijk/pr/7"))
	}
	if w := get("/api/pr/view", url.Values{"n": {"x"}}); w.Code != http.StatusBadRequest {
		t.Errorf("n=x: %d", w.Code)
	}
}

func TestPRLogRoute(t *testing.T) {
	clone, head, base := prRepo(t)
	fakeGh(t, listAnswer, prAnswer("OPEN", head, base), "2026-10-21T10:00:00.0Z --- FAIL: TestRetry\n")
	get := prServer(t, clone)
	w := get("/api/pr/log", url.Values{"job": {"42"}})
	if w.Code != 200 || w.Body.String() != "{\"lines\":[\"--- FAIL: TestRetry\"]}\n" {
		t.Errorf("log %d %q", w.Code, w.Body.String())
	}
	if w := get("/api/pr/log", url.Values{"job": {"42/../1"}}); w.Code != http.StatusBadRequest {
		t.Errorf("bad job id: %d %s", w.Code, w.Body.String())
	}
}
