package viewer

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kijk-dev/kijk/internal/git"
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
	gittest.FakeTool(t, "gh",
		gittest.Rule{Has: "pullRequests(states", Out: list},
		gittest.Rule{Has: "pullRequest(number", Out: pr},
		gittest.Rule{Has: "/actions/jobs/", Out: log})
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
	Files    []struct{ Path, Status, Blobs string }
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
	blob := func(rev string) string {
		out, _ := git.Run(clone, "rev-parse", rev+":retry.go")
		return out
	}
	if want := blob(base) + ".." + blob(head); v.Files[0].Blobs != want {
		t.Errorf("blobs %q, want %q", v.Files[0].Blobs, want)
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

// A file at a pull request's head includes files as they are at that head, not on disk.
func TestPRFileIncludes(t *testing.T) {
	gittest.Isolate(t)
	bare := filepath.Join(t.TempDir(), "docs.git")
	gittest.Run(t, t.TempDir(), "init", "-q", "--bare", "-b", "main", bare)
	work := gittest.Repo(t)
	gittest.Write(t, filepath.Join(work, "guide.adoc"), "= Guide\n\ninclude::part.adoc[]\n")
	gittest.Write(t, filepath.Join(work, "part.adoc"), "On main.\n")
	gittest.Run(t, work, "add", ".")
	gittest.Run(t, work, "commit", "-qm", "Start")
	gittest.Run(t, work, "remote", "add", "origin", bare)
	gittest.Run(t, work, "push", "-q", "origin", "main")
	gittest.Run(t, work, "checkout", "-qb", "docs")
	gittest.Write(t, filepath.Join(work, "part.adoc"), "In the pull request.\n")
	gittest.Run(t, work, "commit", "-qam", "Docs")
	gittest.Run(t, work, "push", "-q", "origin", "docs:refs/pull/7/head")
	clone := filepath.Join(t.TempDir(), "docs")
	gittest.Run(t, t.TempDir(), "clone", "-q", bare, clone)
	gittest.Run(t, clone, "remote", "set-url", "origin", "https://github.com/acme/docs.git")
	gittest.Run(t, clone, "config", "url."+bare+".insteadOf", "https://github.com/acme/docs.git")
	fakeGh(t, listAnswer, prAnswer("OPEN", revParse(t, work, "docs"), revParse(t, work, "main")), "")
	get := prServer(t, clone)
	if w := get("/api/pr/view", url.Values{"n": {"7"}}); w.Code != 200 {
		t.Fatalf("view %d %s", w.Code, w.Body.String())
	}
	w := get("/api/markdown", url.Values{"path": {"guide.adoc"}, "pr": {"7"}})
	if w.Code != 200 || !strings.Contains(w.Body.String(), "In the pull request.") || strings.Contains(w.Body.String(), "On main.") {
		t.Errorf("markdown %d %s", w.Code, w.Body.String())
	}
	// PlantUML reads its includes through /api/text, which takes pr too.
	if w := get("/api/text", url.Values{"path": {"part.adoc"}, "pr": {"7"}}); !strings.Contains(w.Body.String(), "In the pull request.") {
		t.Errorf("text %d %s", w.Code, w.Body.String())
	}
}

// When the head cannot be fetched, the pull request still has the checks the forge sent.
func TestPRViewChecksWithoutHead(t *testing.T) {
	clone, head, base := prRepo(t)
	fakeGh(t, listAnswer, prAnswer("OPEN", head, base), "")
	get := prServer(t, clone)
	var v prView
	w := get("/api/pr/view", url.Values{"n": {"9"}}) // origin has no refs/pull/9/head
	if err := json.Unmarshal(w.Body.Bytes(), &v); w.Code != 200 || err != nil {
		t.Fatalf("%d %v %s", w.Code, err, w.Body.String())
	}
	if !strings.HasPrefix(v.Error, "Cannot fetch #9") || v.Checks == nil {
		t.Errorf("error %q, checks %v", v.Error, v.Checks)
	}
}

// GitLab's words name a merge request with "!", GitHub's a pull request with "#".
func TestForgeWords(t *testing.T) {
	if w, s := prWords("gitlab"); w+" "+s != "merge request !" {
		t.Errorf("gitlab: %q %q", w, s)
	}
	if w, s := prWords("github"); w+" "+s != "pull request #" {
		t.Errorf("github: %q %q", w, s)
	}
}

// What changed since a reviewed head is diffed against the head kijk keeps for the pull request.
func TestPRSinceRoute(t *testing.T) {
	clone, head, base := prRepo(t)
	fakeGh(t, listAnswer, prAnswer("OPEN", head, base), "")
	get := prServer(t, clone)
	if w := get("/api/pr/since", url.Values{"n": {"7"}, "from": {base}}); w.Code != http.StatusConflict {
		t.Fatalf("before the pull request is open: %d %s", w.Code, w.Body.String())
	}
	if w := get("/api/pr/view", url.Values{"n": {"7"}}); w.Code != 200 {
		t.Fatalf("view %d %s", w.Code, w.Body.String())
	}
	type since struct {
		From, At string
		Commits  int
		Rebased  bool
		Files    []struct {
			Path     string
			Add, Del int
		}
	}
	ask := func(from string) (since, int) {
		var s since
		w := get("/api/pr/since", url.Values{"n": {"7"}, "from": {from}})
		if w.Code == 200 {
			if err := json.Unmarshal(w.Body.Bytes(), &s); err != nil {
				t.Fatal(err)
			}
		}
		return s, w.Code
	}
	if s, code := ask(base); code != 200 || s.From != base || s.At != head || s.Commits != 1 || len(s.Files) != 1 || s.Files[0].Path != "retry.go" || s.Files[0].Add != 2 {
		t.Errorf("since the base: %d %+v", code, s)
	}
	if s, code := ask(head); code != 200 || s.Commits != 0 || len(s.Files) != 0 || s.Rebased {
		t.Errorf("since the head: %d %+v", code, s)
	}
	if s, code := ask(strings.Repeat("ab", 20)); code != 200 || !s.Rebased || len(s.Files) != 0 {
		t.Errorf("a commit kijk does not have: %d %+v", code, s)
	}
	for _, bad := range []string{"", "HEAD", "main"} {
		if _, code := ask(bad); code != http.StatusBadRequest {
			t.Errorf("from %q: %d", bad, code)
		}
	}

	diff := func(from string) *httptest.ResponseRecorder {
		return get("/api/diff", url.Values{"path": {"retry.go"}, "mode": {"pr"}, "pr": {"7"}, "from": {from}})
	}
	if w := diff(head); w.Code != 200 || strings.Contains(w.Body.String(), `"k":"+"`) {
		t.Errorf("diff since the head: %d %s", w.Code, w.Body.String())
	}
	if w := diff(base); w.Code != 200 || !strings.Contains(w.Body.String(), `{"k":"+","n":4,"t":"\tfor i := 0; i `) {
		t.Errorf("diff since the base: %d %s", w.Code, w.Body.String())
	}
	if w := diff("HEAD"); w.Code != http.StatusBadRequest {
		t.Errorf("diff from HEAD: %d %s", w.Code, w.Body.String())
	}
}
