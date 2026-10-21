package viewer

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/kijk-dev/kijk/internal/gittest"
)

// reviewServer serves a clone whose origin is on GitHub; post sends a write.
func reviewServer(t *testing.T, root string) (get func(string, url.Values) *httptest.ResponseRecorder, post func(string, string) *httptest.ResponseRecorder) {
	rts := &Roots{}
	rts.SetFolders([]string{root})
	srv := http.NewServeMux()
	cache := t.TempDir()
	Register(srv, rts, Options{CacheDir: func() (string, error) { return cache, nil }})
	get = func(route string, q url.Values) *httptest.ResponseRecorder {
		q.Set("root", root)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, httptest.NewRequest("GET", route+"?"+q.Encode(), nil))
		return w
	}
	post = func(route, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, httptest.NewRequest("POST", route, strings.NewReader(body)))
		return w
	}
	return get, post
}

func ghTarget(head string) gittest.Rule {
	return gittest.Rule{Has: "headRefOid reviews(states: PENDING", Out: fmt.Sprintf(`{"data":{"repository":{"pullRequest":{"id":"PR_7","headRefOid":%q,"reviews":{"nodes":[{"id":"PRR_9","viewerDidAuthor":true}]}}}}}`, head)}
}

func TestReviewRoutes(t *testing.T) {
	clone, head, base := prRepo(t)
	js := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	rules := []gittest.Rule{
		ghTarget(head),
		{Has: "addPullRequestReviewThread(input", Out: `{"data":{"addPullRequestReviewThread":{"thread":{"id":"T9","comments":{"nodes":[{"id":"C9"}]}}}}}`},
		{Has: "addPullRequestReviewThreadReply(input", Out: `{"data":{"addPullRequestReviewThreadReply":{"comment":{"id":"C10"}}}}`},
		{Has: "updatePullRequestReviewComment(input", Out: `{"data":{}}`},
		{Has: "deletePullRequestReviewComment(input", Out: `{"data":{}}`},
		{Has: "resolveReviewThread(input", Out: `{"data":{}}`},
		{Has: "submitPullRequestReview(input", Out: `{"data":{}}`},
		{Has: "pullRequest(number", Out: prAnswer("OPEN", head, base)},
	}
	calls := gittest.FakeToolCalls(t, "gh", rules...)
	get, post := reviewServer(t, clone)
	comment := js(map[string]any{"root": clone, "n": 7, "path": "retry.go", "line": 4, "body": "Why 3?"})

	// A comment on a line needs the head kijk showed; after a restart kijk has none.
	if w := post("/api/pr/comment", comment); w.Code != http.StatusConflict {
		t.Fatalf("before the view: %d %s", w.Code, w.Body.String())
	}
	w := get("/api/pr/view", url.Values{"n": {"7"}})
	var v struct {
		Write  struct{ OK, Approve, Changes bool }
		Drafts int
	}
	if err := json.Unmarshal(w.Body.Bytes(), &v); w.Code != 200 || err != nil || !v.Write.OK || !v.Write.Approve {
		t.Fatalf("view %d %v %s", w.Code, err, w.Body.String())
	}
	if w := post("/api/pr/comment", comment); w.Code != 200 || w.Body.String() != "{\"id\":\"C9\"}\n" {
		t.Fatalf("comment %d %s", w.Code, w.Body.String())
	}
	if last := calls()[len(calls())-1]; !strings.Contains(strings.Join(last.Args, " "), "-f path=retry.go -f review=PRR_9 -f side=RIGHT") {
		t.Fatalf("call %q", last.Args)
	}
	for route, body := range map[string]map[string]any{
		"/api/pr/comment":        {"n": 7, "thread": "T1", "body": "Agreed."},
		"/api/pr/comment/edit":   {"n": 7, "id": "C9", "body": "Why three?"},
		"/api/pr/comment/delete": {"n": 7, "id": "C9"},
		"/api/pr/resolve":        {"n": 7, "thread": "T1", "resolved": true},
		"/api/pr/submit":         {"n": 7, "verdict": "approve", "body": "Looks good."},
	} {
		body["root"] = clone
		if w := post(route, js(body)); w.Code != 200 {
			t.Errorf("%s: %d %s", route, w.Code, w.Body.String())
		}
	}
	if last := calls()[len(calls())-1]; !strings.Contains(strings.Join(last.Args, " "), "-f body=Looks good. -f event=APPROVE -f review=PRR_9") {
		t.Fatalf("submit %q", last.Args)
	}

	// Someone pushed: nothing is written, and the page is told to show the pull request again.
	gittest.FakeToolCalls(t, "gh", append([]gittest.Rule{ghTarget("ffff")}, rules...)...)
	if w := post("/api/pr/comment", comment); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "#7 has new commits.") {
		t.Fatalf("head moved: %d %s", w.Code, w.Body.String())
	}

	for _, c := range []struct {
		route, body string
		code        int
	}{
		{"/api/pr/comment", js(map[string]any{"root": "/elsewhere", "n": 7, "thread": "T1", "body": "x"}), http.StatusForbidden},
		{"/api/pr/comment", js(map[string]any{"root": clone, "n": 7, "thread": "T1", "body": "  "}), http.StatusBadRequest},
		{"/api/pr/comment", js(map[string]any{"root": clone, "n": 0, "thread": "T1", "body": "x"}), http.StatusBadRequest},
		{"/api/pr/comment", js(map[string]any{"root": clone, "n": 7, "thread": "T1", "body": strings.Repeat("x", 70<<10)}), http.StatusBadRequest},
		{"/api/pr/submit", js(map[string]any{"root": clone, "n": 7, "verdict": "merge"}), http.StatusBadRequest},
		{"/api/pr/comment/delete", js(map[string]any{"root": clone, "n": 7, "id": "--hostname"}), http.StatusBadRequest},
	} {
		if w := post(c.route, c.body); w.Code != c.code {
			t.Errorf("%s %.60s: %d %s, want %d", c.route, c.body, w.Code, w.Body.String(), c.code)
		}
	}
}

// On GitLab a submit that failed after the summary became a draft still says so, so the page does
// not send the summary twice.
func TestReviewSubmitPartly(t *testing.T) {
	clone, _, _ := prRepo(t)
	gittest.Run(t, clone, "remote", "set-url", "origin", "https://gitlab.com/acme/shop.git")
	gittest.FakeToolCalls(t, "glab",
		gittest.Rule{Has: "gitlab.com version", Out: `{"version":"18.9.0"}`},
		gittest.Rule{Has: "gitlab.com projects/acme%2Fshop/merge_requests/7/draft_notes", Err: "500 Internal Server Error\n", Exit: 1},
		gittest.Rule{Has: "projects/acme%2Fshop/merge_requests/7/draft_notes", Out: `{"id":63}`})
	_, post := reviewServer(t, clone)
	w := post("/api/pr/submit", fmt.Sprintf(`{"root":%q,"n":7,"verdict":"comment","body":"Looks good."}`, clone))
	if w.Code != 200 || w.Body.String() != "{\"summary\":\"63\",\"error\":\"glab: 500 Internal Server Error\"}\n" {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}
