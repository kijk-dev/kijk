package main

import (
	"cmp"
	"encoding/json"
	"path/filepath"
	"sync"

	"github.com/kijk-dev/kijk/internal/viewer"
)

// Row is one folder on one machine in the rail. It is there for each source that is set: agents
// work in it, or it is in the folders list, or both. A row without a source is never built.
type Row struct {
	Machine string `json:"machine,omitempty"` // "" is the local machine
	Root    string `json:"root"`
	Repo    string `json:"repo"`
	Branch  string `json:"branch"` // the agent scan's when agents work here
	// Embedded, so an agent row's JSON stays as it was and a remote kijk of another build reads it.
	*AgentSource
	Saved *SavedSource `json:"saved,omitempty"`
}

// AgentSource is what the agent scan knows about a row.
type AgentSource struct {
	Cwd          string     `json:"cwd"` // the newest agent's folder, relative to Root with slashes
	Names        []string   `json:"names"`
	Agents       []RowAgent `json:"agents"`       // one per name, in the same order
	Busy         bool       `json:"busy"`         // any agent is Busy
	LastActivity int64      `json:"lastActivity"` // unix ms
}

// SavedSource is what the folders list knows about a row.
type SavedSource struct {
	At      int    `json:"at"`             // its place in the folders list
	Parent  string `json:"parent"`         // the folder that holds it, with ~ for the home folder
	Open    string `json:"open,omitempty"` // where it opens, relative to Root with slashes
	Add     int    `json:"add"`
	Del     int    `json:"del"`
	Counted bool   `json:"counted"` // false until its first git status
	Missing bool   `json:"missing,omitempty"`
}

// folderFacts are what the last git status of a saved folder found.
type folderFacts struct {
	branch   string
	add, del int
	missing  bool
}

// agentPart is the agent scan's part of the rail.
type agentPart struct {
	rows          []Row // local, with only the agents source set
	errs          []typeError
	noneInstalled bool
	machines      []machineView // each with its rows, as its remote kijk sent them
}

// listFolders names the Folders list in an open request. The page shows one list at a time; both
// are filters over the same rows.
const listFolders = "folders"

// openRequest asks the window to open a row, at a pull request, or to show a list. Seq grows, so a
// window acts on each request once.
type openRequest struct {
	Seq   int    `json:"seq"`
	Root  string `json:"root,omitempty"`
	Cwd   string `json:"cwd,omitempty"` // where to open, relative to Root with slashes
	PR    int    `json:"pr,omitempty"`
	List  string `json:"list,omitempty"`
	Added bool   `json:"added,omitempty"`
}

// railMessage is the one message on /api/events, for the window and for a local kijk that follows
// this one as a remote kijk.
type railMessage struct {
	Rows          []Row         `json:"rows"`
	Scanned       bool          `json:"scanned"`            // the agent scan of this look has run
	Machines      []machineView `json:"machines,omitempty"` // state only; Row.Machine names the machine
	Errors        []typeError   `json:"errors,omitempty"`
	NoneInstalled bool          `json:"noneInstalled,omitempty"`
	ListError     string        `json:"listError,omitempty"`
	Open          openRequest   `json:"open"`
	Kijk          kijkState     `json:"kijk"`
}

// rail is every row the window lists, merged from the parts that find them. It is the one place
// that says what the server may read: after each change it sets Roots from its local rows.
type rail struct {
	h   *hub
	rts *viewer.Roots

	mu      sync.Mutex
	agents  *agentPart // nil while kijk does not look for agents
	saved   []Row      // the saved folders, with only the saved source set
	facts   map[string]folderFacts
	listErr error
	open    openRequest
	kijk    kijkState
	rows    []Row // the last merge
}

func newRail(h *hub, rts *viewer.Roots) *rail {
	return &rail{h: h, rts: rts, kijk: kijkState{Version: kijkVersion}}
}

// setAgents takes the scan's part, or nil when kijk stops looking for agents.
func (r *rail) setAgents(p *agentPart) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.agents = p
	r.changedLocked()
}

// setSaved takes the folders list on every change, so a folder is readable before the command
// that added it returns.
func (r *rail) setSaved(list []savedFolder, err error) {
	saved := make([]Row, len(list))
	for i, f := range list {
		s := &SavedSource{At: i, Parent: tilde(filepath.Dir(f.Path))}
		if f.Open != "" {
			s.Open = relCwd(f.Path, f.Open)
		}
		saved[i] = Row{Root: f.Path, Repo: filepath.Base(f.Path), Saved: s}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.saved, r.listErr = saved, err
	r.changedLocked()
}

// setFacts takes the facts of the saved folders, by path.
func (r *rail) setFacts(facts map[string]folderFacts) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.facts = facts
	r.changedLocked()
}

func (r *rail) setKijk(k kijkState) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if k == r.kijk {
		return
	}
	r.kijk = k
	r.changedLocked()
}

// request asks the window to open a row or show a list, and returns its seq for a new window.
func (r *rail) request(q openRequest) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	q.Seq = r.open.Seq + 1
	r.open = q
	r.changedLocked()
	return q.Seq
}

// has says whether a local row lists root.
func (r *rail) has(root string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, row := range r.rows {
		if row.Machine == "" && row.Root == root {
			return true
		}
	}
	return false
}

// agentRoots are the local agent rows' roots. ok is false while kijk does not look for agents.
func (r *rail) agentRoots() (roots []string, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.agents == nil {
		return nil, false
	}
	return localRoots(r.agents.rows), true
}

// changedLocked merges, sets Roots and publishes. It never waits for the agent scan, so the
// Folders list shows at once; scanned says whether the Agents list is known yet. r.mu is held.
func (r *rail) changedLocked() {
	r.rows = mergeRows(r.agents, r.saved, r.facts)
	r.rts.Set(localRoots(r.rows))
	msg := railMessage{Rows: r.rows, Open: r.open, Kijk: r.kijk}
	if a := r.agents; a != nil {
		msg.Scanned, msg.Machines, msg.Errors, msg.NoneInstalled = true, a.machines, a.errs, a.noneInstalled
	}
	if r.listErr != nil {
		msg.ListError = r.listErr.Error()
	}
	b, _ := json.Marshal(msg)
	r.h.publish(b)
}

// mergeRows joins the parts into one row per folder on each machine: local agent rows in scan
// order, then each machine's rows, then saved folders without agents in list order.
func mergeRows(agents *agentPart, saved []Row, facts map[string]folderFacts) []Row {
	rows := []Row{}
	local := map[string]int{}
	if agents != nil {
		for _, row := range agents.rows {
			local[row.Root] = len(rows)
			rows = append(rows, row)
		}
		for _, m := range agents.machines {
			for _, row := range m.Rows {
				row.Machine = m.Name
				rows = append(rows, row)
			}
		}
	}
	for _, s := range saved {
		src := *s.Saved
		ff, counted := facts[s.Root]
		src.Add, src.Del, src.Counted, src.Missing = ff.add, ff.del, counted, ff.missing
		if i, ok := local[s.Root]; ok {
			rows[i].Saved = &src
			rows[i].Branch = cmp.Or(rows[i].Branch, ff.branch)
			continue
		}
		s.Branch, s.Saved = ff.branch, &src
		rows = append(rows, s)
	}
	return rows
}

// localRoots are what this kijk may read: every local row, saved or with agents.
func localRoots(rows []Row) []string {
	var roots []string
	for _, r := range rows {
		if r.Machine == "" {
			roots = append(roots, r.Root)
		}
	}
	return roots
}
