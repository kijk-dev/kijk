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
	return gittest.Rule{Has: "headRefOid reviews(states: PENDING", Out: fmt.Sprintf(`{"data":{"repository":{"pullRequest":{"id":"PR_7","headRefOid":%[1]q,"reviews":{"nodes":[{"id":"PRR_9","viewerDidAuthor":true,"commit":{"oid":%[1]q}}]}}}}}`, head)}
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
		{Has: "updatePullRequestReview(input", Out: `{"data":{}}`},
		{Has: "pullRequest(number", Out: prAnswer("OPEN", head, base)},
	}
	calls := gittest.FakeToolCalls(t, "gh", rules...)
	get, post := reviewServer(t, clone)
	comment := js(map[string]any{"root": clone, "n": 7, "path": "retry.go", "line": 4, "body": "Why 3?"})

	// A comment on a line needs the head kijk showed; after a restart kijk has none, which is not the
	// same as a head that moved.
	if w := post("/api/pr/comment", comment); w.Code != http.StatusPreconditionRequired {
		t.Fatalf("before the view: %d %s", w.Code, w.Body.String())
	}
	// Nor a summary, which may start your pending review.
	if w := post("/api/pr/summary", js(map[string]any{"root": clone, "n": 7, "body": "Fine."})); w.Code != http.StatusPreconditionRequired {
		t.Fatalf("summary before the view: %d %s", w.Code, w.Body.String())
	}
	// Nor can kijk approve before it showed the pull request: the approval is for the head on screen.
	if w := post("/api/pr/submit", js(map[string]any{"root": clone, "n": 7, "verdict": "approve"})); w.Code != http.StatusPreconditionRequired {
		t.Fatalf("approve before the view: %d %s", w.Code, w.Body.String())
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
	for _, c := range []struct {
		route string
		body  map[string]any
	}{
		{"/api/pr/comment", map[string]any{"n": 7, "thread": "T1", "body": "Agreed."}},
		{"/api/pr/comment/edit", map[string]any{"n": 7, "id": "C9", "body": "Why three?"}},
		{"/api/pr/comment/delete", map[string]any{"n": 7, "id": "C9"}},
		{"/api/pr/resolve", map[string]any{"n": 7, "thread": "T1", "resolved": true}},
		{"/api/pr/summary", map[string]any{"n": 7, "body": "Looks good."}},
		{"/api/pr/submit", map[string]any{"n": 7, "verdict": "approve"}},
	} {
		c.body["root"] = clone
		if w := post(c.route, js(c.body)); w.Code != 200 {
			t.Errorf("%s: %d %s", c.route, w.Code, w.Body.String())
		}
	}
	if c := calls(); !strings.Contains(strings.Join(c[len(c)-3].Args, " "), "-f body=Looks good. -f review=PRR_9") || !strings.Contains(strings.Join(c[len(c)-1].Args, " "), "-f event=APPROVE -f review=PRR_9") {
		t.Fatalf("summary and submit %q", c[len(c)-3:])
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

// The server refuses what the forge would refuse, from what it knows about the pull request.
func TestReviewWriteRules(t *testing.T) {
	clone, head, base := prRepo(t)
	own := strings.Replace(prAnswer("OPEN", head, base), `"author":{"login":"sanne"}`, `"author":{"login":"you"}`, 1)
	calls := gittest.FakeToolCalls(t, "gh", gittest.Rule{Has: "pullRequest(number", Out: own})
	get, post := reviewServer(t, clone)
	if w := get("/api/pr/view", url.Values{"n": {"7"}}); w.Code != 200 {
		t.Fatalf("view %d %s", w.Code, w.Body.String())
	}
	n := len(calls())
	w := post("/api/pr/submit", fmt.Sprintf(`{"root":%q,"n":7,"verdict":"approve"}`, clone))
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "You cannot approve or request changes on your own pull request") || len(calls()) != n {
		t.Errorf("approve your own: %d %s", w.Code, w.Body.String())
	}

	gittest.FakeToolCalls(t, "gh", gittest.Rule{Has: "pullRequest(number", Out: prAnswer("MERGED", head, base)})
	get("/api/pr/view", url.Values{"n": {"7"}})
	w = post("/api/pr/comment", fmt.Sprintf(`{"root":%q,"n":7,"thread":"T1","body":"Late."}`, clone))
	if w.Code != http.StatusConflict || strings.TrimSpace(w.Body.String()) != "This pull request is merged." {
		t.Errorf("reply on a merged pull request: %d %s", w.Code, w.Body.String())
	}
}
