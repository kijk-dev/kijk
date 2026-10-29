package main

import (
	"path/filepath"
	"slices"
	"time"

	"github.com/kijk-dev/kijk/internal/agents"
	"github.com/kijk-dev/kijk/internal/git"
	"github.com/kijk-dev/kijk/internal/viewer"
)

// RowAgent is the agent type of one name in a row, such as "claude", and whether it is Busy.
type RowAgent struct {
	Type string `json:"type"`
	Busy bool   `json:"busy"`
}

// session is one agent with its agent type and its folder's git root and branch.
type session struct {
	Agent, Name, Root, Branch, Cwd string
	Busy                           bool
	Last                           time.Time
}

type gitEntry struct {
	root, branch string
	at           time.Time
}

// scanner is used from one goroutine only.
type scanner struct {
	types []agents.AgentType
	env   *agents.Env
	git   map[string]gitEntry
}

func newScanner(types []agents.AgentType, env *agents.Env) *scanner {
	return &scanner{types: types, env: env, git: map[string]gitEntry{}}
}

// typeError is an agent type whose agents kijk cannot read, and why. Its agents are hidden; the
// agents of the other types still show.
type typeError struct {
	Type  string `json:"type"`
	Error string `json:"error"`
}

// scan returns the rows of every agent type it can read, an error for each type it cannot read, and
// whether the tool of any agent type is installed on this machine.
func (s *scanner) scan() (rows []Row, errs []typeError, installed bool) {
	var all []session
	for _, t := range s.types {
		found, err := t.Sessions(s.env)
		if err != nil {
			errs = append(errs, typeError{t.Name, err.Error()})
			continue
		}
		for _, a := range found {
			root, branch := s.gitInfo(a.Cwd)
			all = append(all, session{Agent: t.Name, Name: a.Name, Root: root, Branch: branch, Cwd: a.Cwd, Busy: a.Busy, Last: a.Last})
		}
	}
	return groupRows(all), errs, s.env.Installed()
}

func (s *scanner) gitInfo(cwd string) (string, string) {
	if e, ok := s.git[cwd]; ok && time.Since(e.at) < 5*time.Second {
		return e.root, e.branch
	}
	root, branch := gitInfo(cwd)
	s.git[cwd] = gitEntry{root, branch, time.Now()}
	return root, branch
}

func gitInfo(cwd string) (root, branch string) {
	out, err := git.Run(cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return cwd, ""
	}
	root = filepath.FromSlash(out)
	if branch, err = git.Run(cwd, "symbolic-ref", "--short", "-q", "HEAD"); err != nil {
		branch, _ = git.Run(cwd, "rev-parse", "--short", "HEAD")
	}
	return root, branch
}

func groupRows(sessions []session) []Row {
	slices.SortStableFunc(sessions, func(a, b session) int { return b.Last.Compare(a.Last) })
	var rows []Row
	index := map[string]int{}
	for _, s := range sessions {
		i, ok := index[s.Root]
		if !ok {
			i = len(rows)
			index[s.Root] = i
			rows = append(rows, Row{Root: s.Root, Repo: filepath.Base(s.Root), Branch: s.Branch,
				AgentSource: &AgentSource{Cwd: relCwd(s.Root, s.Cwd), LastActivity: s.Last.UnixMilli()}})
		}
		rows[i].Names = append(rows[i].Names, s.Name)
		rows[i].Agents = append(rows[i].Agents, RowAgent{s.Agent, s.Busy})
		rows[i].Busy = rows[i].Busy || s.Busy
	}
	return rows
}

func relCwd(root, cwd string) string {
	if real, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = real
	}
	r, err := filepath.Rel(root, cwd)
	if err != nil || r == "." || !viewer.Inside(root, cwd) {
		return ""
	}
	return filepath.ToSlash(r)
}
