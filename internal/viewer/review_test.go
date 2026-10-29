package viewer

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/kijk-dev/kijk/internal/git"
	"github.com/kijk-dev/kijk/internal/gittest"
)

// reviewServer serves a clone whose origin is on GitHub; post sends a write.
func reviewServer(t *testing.T, root string) (get func(string, url.Values) *httptest.ResponseRecorder, post func(string, string) *httptest.ResponseRecorder) {
	rts := &Roots{}
	rts.Set([]string{root})
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
	comment := js(map[string]any{"root": clone, "n": 7, "at": head, "path": "retry.go", "line": 4, "body": "Why 3?"})

	// A comment on a line needs the head kijk showed; after a restart kijk has none, which is not the
	// same as a head that moved.
	if w := post("/api/pr/comment", comment); w.Code != http.StatusPreconditionRequired {
		t.Fatalf("before the view: %d %s", w.Code, w.Body.String())
	}
	// Nor a summary, which may start your pending review.
	if w := post("/api/pr/summary", js(map[string]any{"root": clone, "n": 7, "at": head, "body": "Fine."})); w.Code != http.StatusPreconditionRequired {
		t.Fatalf("summary before the view: %d %s", w.Code, w.Body.String())
	}
	// Nor can kijk approve before it showed the pull request: the approval is for the head on screen.
	if w := post("/api/pr/submit", js(map[string]any{"root": clone, "n": 7, "at": head, "verdict": "approve"})); w.Code != http.StatusPreconditionRequired {
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
	if last := calls()[len(calls())-1]; !last.Has("body=Why 3?", "line=4", "path=retry.go", "review=PRR_9", "side=RIGHT") {
		t.Fatalf("call %q", last)
	}
	for _, c := range []struct {
		route string
		body  map[string]any
	}{
		{"/api/pr/comment", map[string]any{"n": 7, "at": head, "thread": "T1", "body": "Agreed."}},
		{"/api/pr/comment/edit", map[string]any{"n": 7, "id": "C9", "body": "Why three?"}},
		{"/api/pr/comment/delete", map[string]any{"n": 7, "id": "C9"}},
		{"/api/pr/resolve", map[string]any{"n": 7, "thread": "T1", "resolved": true}},
		{"/api/pr/summary", map[string]any{"n": 7, "at": head, "body": "Looks good."}},
		{"/api/pr/submit", map[string]any{"n": 7, "at": head, "verdict": "approve"}},
	} {
		c.body["root"] = clone
		if w := post(c.route, js(c.body)); w.Code != 200 {
			t.Errorf("%s: %d %s", c.route, w.Code, w.Body.String())
		}
	}
	if c := calls(); !c[len(c)-3].Has("body=Looks good.", "review=PRR_9") || !c[len(c)-1].Has("event=APPROVE", "review=PRR_9") {
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

// A page shows the head it loaded. When another page loaded a newer one, a write from the first
// page would land on lines it never showed, so it is refused before any call.
func TestReviewWriteOldHead(t *testing.T) {
	clone, head, base := prRepo(t)
	js := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	thread := gittest.Rule{Has: "addPullRequestReviewThread(input", Out: `{"data":{"addPullRequestReviewThread":{"thread":{"id":"T9","comments":{"nodes":[{"id":"C9"}]}}}}}`}
	gittest.FakeToolCalls(t, "gh", gittest.Rule{Has: "pullRequest(number", Out: prAnswer("OPEN", head, base)})
	get, post := reviewServer(t, clone)
	if w := get("/api/pr/view", url.Values{"n": {"7"}}); w.Code != 200 {
		t.Fatalf("view %d %s", w.Code, w.Body.String())
	}

	newer, err := git.Run(clone, "commit-tree", head+"^{tree}", "-p", head, "-m", "More")
	if err != nil {
		t.Fatal(err)
	}
	gittest.Run(t, clone, "push", "-q", "origin", newer+":refs/pull/7/head")
	calls := gittest.FakeToolCalls(t, "gh", thread, ghTarget(newer), gittest.Rule{Has: "pullRequest(number", Out: prAnswer("OPEN", newer, base)})
	if w := get("/api/pr/view", url.Values{"n": {"7"}}); w.Code != 200 || !strings.Contains(w.Body.String(), `"at":"`+newer+`"`) {
		t.Fatalf("second view %d %s", w.Code, w.Body.String())
	}

	n := len(calls())
	for _, c := range []struct{ route, body string }{
		{"/api/pr/comment", js(map[string]any{"root": clone, "n": 7, "at": head, "path": "retry.go", "line": 4, "body": "Why 3?"})},
		{"/api/pr/comment", js(map[string]any{"root": clone, "n": 7, "at": head, "thread": "T1", "body": "Agreed."})},
		{"/api/pr/summary", js(map[string]any{"root": clone, "n": 7, "at": head, "body": "Fine."})},
		{"/api/pr/submit", js(map[string]any{"root": clone, "n": 7, "at": head, "verdict": "approve"})},
	} {
		if w := post(c.route, c.body); w.Code != http.StatusConflict || strings.TrimSpace(w.Body.String()) != "#7 has new commits. kijk shows them now; add your comment there." {
			t.Errorf("%s %.80s: %d %s", c.route, c.body, w.Code, w.Body.String())
		}
	}
	if len(calls()) != n {
		t.Fatalf("kijk called the forge for a head it did not show: %q", calls()[n:])
	}
	// A write that does not say its head is refused, not sent to the last head any page loaded.
	for _, c := range []struct{ route, body string }{
		{"/api/pr/comment", js(map[string]any{"root": clone, "n": 7, "path": "retry.go", "line": 4, "body": "Why 3?"})},
		{"/api/pr/comment", js(map[string]any{"root": clone, "n": 7, "thread": "T1", "body": "Agreed."})},
		{"/api/pr/summary", js(map[string]any{"root": clone, "n": 7, "body": "Fine."})},
		{"/api/pr/submit", js(map[string]any{"root": clone, "n": 7, "verdict": "approve"})},
	} {
		if w := post(c.route, c.body); w.Code != http.StatusBadRequest {
			t.Errorf("%s %.80s without a head: %d %s", c.route, c.body, w.Code, w.Body.String())
		}
	}
	if len(calls()) != n {
		t.Fatalf("kijk called the forge for a write with no head: %q", calls()[n:])
	}
	// The page with the newer head writes on it.
	if w := post("/api/pr/comment", js(map[string]any{"root": clone, "n": 7, "at": newer, "path": "retry.go", "line": 4, "body": "Why 3?"})); w.Code != 200 {
		t.Fatalf("comment on the newer head: %d %s", w.Code, w.Body.String())
	}
}

// When the new head cannot be fetched, kijk shows the head it has and says why, and refuses only
// the writes that go to the forge's head: a comment, not a resolve.
func TestReviewWriteHeadNotFetched(t *testing.T) {
	clone, head, base := prRepo(t)
	js := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	gittest.FakeToolCalls(t, "gh", gittest.Rule{Has: "pullRequest(number", Out: prAnswer("OPEN", head, base)})
	get, post := reviewServer(t, clone)
	if w := get("/api/pr/view", url.Values{"n": {"7"}}); w.Code != 200 {
		t.Fatalf("view %d %s", w.Code, w.Body.String())
	}

	newer, err := git.Run(clone, "commit-tree", head+"^{tree}", "-p", head, "-m", "More")
	if err != nil {
		t.Fatal(err)
	}
	gittest.Run(t, clone, "push", "-q", "origin", newer+":refs/pull/7/head")
	t.Setenv("GIT_ALLOW_PROTOCOL", "https") // origin is a folder here: its fetch fails
	calls := gittest.FakeToolCalls(t, "gh", ghTarget(newer), gittest.Rule{Has: "pullRequest(number", Out: prAnswer("OPEN", newer, base)})
	w := get("/api/pr/view", url.Values{"n": {"7"}})
	var v struct {
		At    string
		Error string
		Write struct{ OK bool }
	}
	const why = "Cannot fetch the new commits of #7 from origin: "
	if err := json.Unmarshal(w.Body.Bytes(), &v); w.Code != 200 || err != nil || v.At != head || !v.Write.OK || !strings.HasPrefix(v.Error, why) {
		t.Fatalf("view %d %v %s", w.Code, err, w.Body.String())
	}
	n := len(calls())
	if w := post("/api/pr/comment", js(map[string]any{"root": clone, "n": 7, "at": head, "path": "retry.go", "line": 4, "body": "Why 3?"})); w.Code != http.StatusConflict || !strings.HasPrefix(w.Body.String(), why) || len(calls()) != n {
		t.Fatalf("comment: %d %s, calls %q", w.Code, w.Body.String(), calls()[n:])
	}
	post("/api/pr/resolve", js(map[string]any{"root": clone, "n": 7, "thread": "T1", "resolved": true}))
	if len(calls()) == n {
		t.Fatal("the resolve did not reach the forge")
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
	w = post("/api/pr/merge", fmt.Sprintf(`{"root":%q,"n":7,"at":%q}`, clone, head))
	if w.Code != http.StatusConflict || strings.TrimSpace(w.Body.String()) != "This pull request is merged." {
		t.Errorf("merge of a merged pull request: %d %s", w.Code, w.Body.String())
	}
}

// A merge goes only to the head on screen, and not when the forge last said it would refuse it.
func TestMergeRoute(t *testing.T) {
	clone, head, base := prRepo(t)
	js := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	merge := gittest.Rule{Has: "mergePullRequest(input", Out: `{"data":{"mergePullRequest":{"pullRequest":{"id":"PR_7"}}}}`}
	calls := gittest.FakeToolCalls(t, "gh", merge, ghTarget(head), gittest.Rule{Has: "pullRequest(number", Out: prAnswer("OPEN", head, base)})
	get, post := reviewServer(t, clone)
	body := js(map[string]any{"root": clone, "n": 7, "at": head})

	if w := post("/api/pr/merge", body); w.Code != http.StatusPreconditionRequired || len(calls()) != 0 {
		t.Fatalf("before the view: %d %s", w.Code, w.Body.String())
	}
	if w := get("/api/pr/view", url.Values{"n": {"7"}}); w.Code != 200 || !strings.Contains(w.Body.String(), `"merge":{"ok":true}`) {
		t.Fatalf("view %d %s", w.Code, w.Body.String())
	}
	n := len(calls())
	if w := post("/api/pr/merge", js(map[string]any{"root": clone, "n": 7})); w.Code != http.StatusBadRequest || len(calls()) != n {
		t.Fatalf("a merge that does not say its head: %d %s", w.Code, w.Body.String())
	}
	if w := post("/api/pr/merge", js(map[string]any{"root": clone, "n": 7, "at": "ffff"})); w.Code != http.StatusConflict ||
		strings.TrimSpace(w.Body.String()) != "#7 has new commits. kijk shows them now; look at them before you merge." || len(calls()) != n {
		t.Fatalf("a merge of a head another page replaced: %d %s", w.Code, w.Body.String())
	}
	if w := post("/api/pr/merge", body); w.Code != 200 {
		t.Fatalf("merge %d %s", w.Code, w.Body.String())
	}
	if c := calls()[n:]; len(c) != 2 || !c[1].Has("pr=PR_7", "head="+head) {
		t.Fatalf("calls %q", c)
	}

	// The forge said the merge would fail: kijk does not ask it.
	conflicts := strings.Replace(prAnswer("OPEN", head, base), `"isDraft":false,`, `"isDraft":false,"mergeable":"CONFLICTING",`, 1)
	calls = gittest.FakeToolCalls(t, "gh", merge, ghTarget(head), gittest.Rule{Has: "pullRequest(number", Out: conflicts})
	get("/api/pr/view", url.Values{"n": {"7"}})
	n = len(calls())
	if w := post("/api/pr/merge", body); w.Code != http.StatusConflict || strings.TrimSpace(w.Body.String()) != "This pull request has conflicts with main." || len(calls()) != n {
		t.Fatalf("merge with conflicts: %d %s", w.Code, w.Body.String())
	}
}

// Merging and reviews are apart: a forge that takes no review from kijk may still take a merge.
func TestMergeRefused(t *testing.T) {
	p := openedPR{head: "h1", write: git.Write{Note: "Reviews from kijk need GitLab 16.3."}, merge: git.Merge{OK: true}}
	if why := refused(p, true, writeRequest{At: "h1", merge: true}); why != "" {
		t.Errorf("merge refused: %s", why)
	}
	if why := refused(p, true, writeRequest{At: "h1"}); why != "Reviews from kijk need GitLab 16.3." {
		t.Errorf("comment refused with %q", why)
	}
}

// The page may ask to delete the branch only where it showed the checkbox, and a failed delete
// still answers the merge.
func TestMergeDeleteRoute(t *testing.T) {
	clone, head, base := prRepo(t)
	js := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	branch := strings.Replace(prAnswer("OPEN", head, base), `"isDraft":false,`, `"isDraft":false,"headRef":{"associatedPullRequests":{"totalCount":1}},`, 1)
	auto := strings.Replace(branch, `"repository":{`, `"repository":{"deleteBranchOnMerge":true,`, 1)
	merge := gittest.Rule{Has: "mergePullRequest(input", Out: `{"data":{"mergePullRequest":{"pullRequest":{"headRef":{"id":"REF_1","target":{"oid":"` + head + `"}}}}}}`}
	deleted := gittest.Rule{Has: "deleteRef(input", Out: `{"data":{"deleteRef":{"clientMutationId":null}}}`}
	body := js(map[string]any{"root": clone, "n": 7, "at": head, "delete": true})

	// GitHub deletes it itself: kijk does not ask, and refuses before any merge.
	calls := gittest.FakeToolCalls(t, "gh", merge, deleted, ghTarget(head), gittest.Rule{Has: "pullRequest(number", Out: auto})
	get, post := reviewServer(t, clone)
	if w := get("/api/pr/view", url.Values{"n": {"7"}}); !strings.Contains(w.Body.String(), `"merge":{"ok":true,"delete":"auto"}`) {
		t.Fatalf("view %d %s", w.Code, w.Body.String())
	}
	n := len(calls())
	if w := post("/api/pr/merge", body); w.Code != http.StatusBadRequest || len(calls()) != n {
		t.Fatalf("delete where GitHub deletes it: %d %s", w.Code, w.Body.String())
	}

	calls = gittest.FakeToolCalls(t, "gh", merge, deleted, ghTarget(head), gittest.Rule{Has: "pullRequest(number", Out: branch})
	if w := get("/api/pr/view", url.Values{"n": {"7"}}); !strings.Contains(w.Body.String(), `"merge":{"ok":true,"delete":"off"}`) {
		t.Fatalf("view %d %s", w.Code, w.Body.String())
	}
	n = len(calls())
	if w := post("/api/pr/merge", body); w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"deleted":true}` {
		t.Fatalf("merge %d %s", w.Code, w.Body.String())
	}
	if c := calls()[n:]; len(c) != 3 || !c[2].Has("ref=REF_1") {
		t.Fatalf("calls %q", c)
	}

	refused := gittest.Rule{Has: "deleteRef(input", Out: `{"data":{"deleteRef":null},"errors":[{"message":"Cannot delete this protected branch"}]}`}
	gittest.FakeToolCalls(t, "gh", merge, refused, ghTarget(head), gittest.Rule{Has: "pullRequest(number", Out: branch})
	if w := post("/api/pr/merge", body); w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"deleted":false,"why":"gh: Cannot delete this protected branch"}` {
		t.Fatalf("merge with a failed delete: %d %s", w.Code, w.Body.String())
	}
}
