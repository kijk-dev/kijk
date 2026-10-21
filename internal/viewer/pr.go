package viewer

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/kijk-dev/kijk/internal/git"
	"github.com/kijk-dev/kijk/internal/render"
)

// prRanges keeps the forge diff of each pull request a page opened, so its files are diffed by
// number: the page never sends a revision.
type prRanges struct {
	sync.Mutex
	m      map[string][2]string // root and number: split point and head
	writes map[string]git.Write // root and number: what kijk may write, as the forge last said
}

func (p *prRanges) setWrite(root string, n int, w git.Write) {
	p.Lock()
	defer p.Unlock()
	if len(p.writes) > 200 {
		clear(p.writes)
	}
	p.writes[root+"\n"+strconv.Itoa(n)] = w
}

func (p *prRanges) rules(root string, n int) (git.Write, bool) {
	p.Lock()
	defer p.Unlock()
	w, ok := p.writes[root+"\n"+strconv.Itoa(n)]
	return w, ok
}

func (p *prRanges) set(root string, n int, split, head string) {
	p.Lock()
	defer p.Unlock()
	if len(p.m) > 200 {
		clear(p.m)
	}
	p.m[root+"\n"+strconv.Itoa(n)] = [2]string{split, head}
}

func (p *prRanges) get(root string, n int) ([2]string, bool) {
	p.Lock()
	defer p.Unlock()
	r, ok := p.m[root+"\n"+strconv.Itoa(n)]
	return r, ok
}

// prFiles is where files at a pull request's head are kept, to read them as files on disk.
func (a *api) prFiles() string {
	if a.cacheDir != nil {
		if d, err := a.cacheDir(); err == nil {
			return filepath.Join(d, "kijk", "pr-files")
		}
	}
	return filepath.Join(os.TempDir(), "kijk-pr-files")
}

func prNumber(w http.ResponseWriter, s string) (int, bool) {
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		http.Error(w, "bad pull request number", http.StatusBadRequest)
		return 0, false
	}
	return n, true
}

// prs answers the open pull requests. When the list holds all of them, the refs of pull requests
// that closed go.
func (a *api) prs(w http.ResponseWriter, r *http.Request) {
	root, _, ok := a.target(w, r)
	if !ok {
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	res := git.ListPRs(root, q)
	if q == "" && res.Forge != nil && res.Note == "" && res.Total <= len(res.PRs) {
		open := make([]int, len(res.PRs))
		for i, p := range res.PRs {
			open[i] = p.Number
		}
		git.SweepPRRefs(root, open)
	}
	reply(w, res)
}

type prReply struct {
	*git.PR
	Forge    any    `json:"forge"`
	BodyHTML string `json:"bodyHtml"`
	Split    string `json:"split,omitempty"` // where the forge diff starts
	At       string `json:"at,omitempty"`    // the head commit kijk has
	Fetched  bool   `json:"fetched,omitempty"`
	Error    string `json:"error,omitempty"` // why there are no files
	Files    any    `json:"files"`
}

// prView answers one pull request. It fetches the head when it moved, so the files can be diffed
// with plain git, and deletes the ref of a pull request that closed.
func (a *api) prView(w http.ResponseWriter, r *http.Request) {
	root, _, ok := a.target(w, r)
	if !ok {
		return
	}
	n, ok := prNumber(w, r.URL.Query().Get("n"))
	if !ok {
		return
	}
	pr, f, err := git.ViewPR(root, n)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	a.ranges.setWrite(root, n, pr.Write)
	out := prReply{PR: pr, Forge: f, Files: []any{}}
	out.BodyHTML = markdownHTML(pr.Body)
	for i := range pr.Comments {
		pr.Comments[i].HTML = markdownHTML(pr.Comments[i].Body)
	}
	for i := range pr.Threads {
		for j := range pr.Threads[i].Comments {
			c := &pr.Threads[i].Comments[j]
			c.HTML = markdownHTML(c.Body)
		}
	}
	if pr.State == "merged" || pr.State == "closed" {
		git.DeletePRRef(root, n)
		word, _ := prWords(f.Kind)
		out.Error = "This " + word + " is " + pr.State + "."
		reply(w, out)
		return
	}
	out.Fetched, err = git.FetchPR(root, f.Kind, n, pr.HeadOid, pr.BaseOid, pr.Base)
	head := git.PRHead(root, n)
	if head != "" {
		pr.HeadOid = head
	}
	// The checks come from the forge; without the head only their graph needs the workflow file.
	pr.ReadChecks(root, f)
	if head == "" {
		_, sign := prWords(f.Kind)
		out.Error = "Cannot fetch " + sign + strconv.Itoa(n) + " from origin"
		if err != nil {
			out.Error += ": " + err.Error()
		}
		reply(w, out)
		return
	}
	split, err := git.PRSplit(root, pr, head)
	if err != nil {
		out.Error = err.Error()
		reply(w, out)
		return
	}
	files, err := git.PRFiles(root, split, head)
	if err != nil {
		out.Error = err.Error()
		reply(w, out)
		return
	}
	a.ranges.set(root, n, split, head)
	out.Split, out.At, out.Files = split, head, files
	threadTexts(root, head, pr.Threads)
	reply(w, out)
}

type sinceReply struct {
	git.Since
	From string `json:"from"`
	At   string `json:"at"`
}

// prSince answers what changed in a pull request since from, the head of your last review, up to
// the head kijk keeps for it.
func (a *api) prSince(w http.ResponseWriter, r *http.Request) {
	root, _, ok := a.target(w, r)
	if !ok {
		return
	}
	n, ok := prNumber(w, r.URL.Query().Get("n"))
	if !ok {
		return
	}
	rg, ok := a.ranges.get(root, n)
	if !ok {
		http.Error(w, "Open the pull request again", http.StatusConflict)
		return
	}
	from := r.URL.Query().Get("from")
	s, err := git.PRSince(root, rg[0], from, rg[1])
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, sinceReply{Since: s, From: from, At: rg[1]})
}

// threadTexts adds each thread's line as it is at the head, so a thread can find its line on disk.
func threadTexts(root, head string, ts []git.Thread) {
	files := map[string][]string{}
	for i := range ts {
		t := &ts[i]
		if t.Old || t.Line <= 0 {
			continue
		}
		lines, ok := files[t.Path]
		if !ok {
			if src, err := git.FileAt(root, head, t.Path); err == nil {
				lines = strings.Split(src, "\n")
			}
			files[t.Path] = lines
		}
		if t.Line <= len(lines) {
			t.Text = strings.TrimSuffix(lines[t.Line-1], "\r")
		}
	}
}

func markdownHTML(src string) string {
	if strings.TrimSpace(src) == "" {
		return ""
	}
	html, _, err := render.Markdown([]byte(src), "", nil)
	if err != nil {
		return ""
	}
	return html
}

// prLog answers the last lines of a failed CI job's log.
func (a *api) prLog(w http.ResponseWriter, r *http.Request) {
	root, _, ok := a.target(w, r)
	if !ok {
		return
	}
	lines, err := git.JobLog(root, r.URL.Query().Get("job"))
	if err == git.ErrBadHash {
		http.Error(w, "bad job", http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	reply(w, map[string][]string{"lines": lines})
}

// prWords are what a forge calls a pull request, and the sign before its number.
func prWords(kind string) (word, sign string) {
	if kind == "gitlab" {
		return "merge request", "!"
	}
	return "pull request", "#"
}
