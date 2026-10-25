package viewer

import (
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/kijk-dev/kijk/internal/git"
	"github.com/kijk-dev/kijk/internal/render"
)

// openedPR is what kijk keeps of a pull request a page opened: its forge diff, so its files are
// diffed by number (the page never sends a revision), and what the forge last said kijk may write
// and merge.
type openedPR struct {
	split, head string // empty while kijk has no diff of it
	write       git.Write
	merge       git.Merge
	ref         string // as the forge says its number, such as "#7"
	stale       string // why kijk cannot show the forge's head, which a comment would go to
}

type openedPRs struct {
	sync.Mutex
	m map[string]openedPR
}

func prKey(root string, n int) string { return root + "\n" + strconv.Itoa(n) }

func (o *openedPRs) get(root string, n int) (openedPR, bool) {
	o.Lock()
	defer o.Unlock()
	p, ok := o.m[prKey(root, n)]
	return p, ok
}

func (o *openedPRs) set(root string, n int, change func(*openedPR)) {
	o.Lock()
	defer o.Unlock()
	if len(o.m) > 200 {
		clear(o.m)
	}
	p := o.m[prKey(root, n)]
	change(&p)
	o.m[prKey(root, n)] = p
}

// prFile is a file as it is at pull request n's head on screen, kept in the cache to read as a
// file on disk.
func (a *api) prFile(root string, n int, rel string) (string, error) {
	p, _ := a.opened.get(root, n)
	head := p.head
	if head == "" {
		head = git.PRHead(root, n) // as after a restart, when a tab asks before the pull request
	}
	if head == "" {
		return "", fs.ErrNotExist
	}
	dir := filepath.Join(os.TempDir(), "kijk-pr-files")
	if a.cacheDir != nil {
		if d, err := a.cacheDir(); err == nil {
			dir = filepath.Join(d, "kijk", "pr-files")
		}
	}
	return git.PRFile(root, head, rel, dir)
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
	ref := f.Ref(n)
	a.opened.set(root, n, func(p *openedPR) { p.write, p.merge, p.ref = pr.Write, pr.Merge, ref })
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
		out.Error = pr.Write.Note
		reply(w, out)
		return
	}
	_, err = git.FetchPR(root, f.Kind, n, pr.HeadOid, pr.BaseOid, pr.Base)
	head := git.PRHead(root, n)
	if head == "" {
		out.Error = "Cannot fetch " + ref + " from origin"
		if err != nil {
			out.Error += ": " + err.Error()
		}
	}
	if err != nil && head != "" && head != pr.HeadOid {
		out.Error = "Cannot fetch the new commits of " + ref + " from origin: " + err.Error()
	}
	a.opened.set(root, n, func(p *openedPR) { p.stale = out.Error })
	if head != "" {
		pr.HeadOid = head
	}
	// The checks come from the forge; without the head only their graph needs the workflow file.
	pr.ReadChecks(root, f)
	if head == "" {
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
	a.opened.set(root, n, func(p *openedPR) { p.split, p.head = split, head })
	out.Split, out.At, out.Files = split, head, files
	threadTexts(root, head, pr.Threads)
	reply(w, out)
}

type sinceReply struct {
	git.Since
	At string `json:"at"` // the head it was diffed with
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
	p, _ := a.opened.get(root, n)
	if p.head == "" {
		http.Error(w, "Open the pull request again", http.StatusConflict)
		return
	}
	from := r.URL.Query().Get("from")
	s, err := git.PRSince(root, p.split, from, p.head)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, sinceReply{Since: s, At: p.head})
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
