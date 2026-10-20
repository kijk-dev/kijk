// Package agents finds the running agents of each agent type kijk knows. Each type is one file with
// its reader, an embedded icon, and a test folder in testdata/<name>.
package agents

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// An AgentType is a program an agent can be, such as Claude Code.
type AgentType struct {
	Name  string `json:"name"`            // such as "claude": a row's agent type and its telemetry name
	Label string `json:"label"`           // such as "Claude Code"
	Icon  string `json:"icon"`            // the mark, as SVG
	Color string `json:"color,omitempty"` // the mark's color; "" for the text color
	// Sessions returns the running agents of this type.
	Sessions func(env *Env) ([]Session, error) `json:"-"`
}

// Types are the agent types kijk knows, by label: the order in which kijk names them.
var Types []AgentType

// add puts a type in Types. Each type adds itself from its own file, so a new type edits no list.
func add(t AgentType) {
	i, _ := slices.BinarySearchFunc(Types, t.Label, func(a AgentType, label string) int { return strings.Compare(a.Label, label) })
	Types = slices.Insert(Types, i, t)
}

// A Session is one running agent.
type Session struct {
	Name string
	Cwd  string // the folder it works in
	Busy bool
	Last time.Time // its last activity
}

// Env is the machine as the readers see it, and what they keep between scans. One goroutine uses it.
type Env struct {
	Home    string              // the user's home folder
	Getenv  func(string) string // the environment, such as CLAUDE_CONFIG_DIR
	Version string              // kijk's version, for a tool that asks who reads it
	states  map[string]any
	found   map[string]bool // whether each tool folder was there when a reader last looked
}

// NewEnv is the machine kijk runs on, for kijk version.
func NewEnv(version string) *Env {
	home, _ := os.UserHomeDir()
	return &Env{Home: home, Getenv: os.Getenv, Version: version}
}

// folder is a tool's folder: the one in envVar when it is set, else name in the home folder. ok is
// false when it does not exist: the tool is not installed, so its reader has no sessions and no error.
func (e *Env) folder(envVar, name string) (dir string, ok bool) {
	dir = e.Getenv(envVar)
	if dir == "" {
		dir = filepath.Join(e.Home, name)
	}
	ok = exists(dir)
	if e.found == nil {
		e.found = map[string]bool{}
	}
	e.found[dir] = ok
	return dir, ok
}

// Installed reports whether the tool of any agent type is installed, as its reader last found it.
func (e *Env) Installed() bool {
	for _, ok := range e.found {
		if ok {
			return true
		}
	}
	return false
}

// running rejects a reused pid: that process started after the agent wrote startedAt.
func (e *Env) running(pid int, startedAt int64) bool {
	created, err := processStart(pid)
	if err != nil {
		return false
	}
	return startedAt == 0 || created <= startedAt+2000
}

// state is what the reader of a type keeps between scans, made by fresh on its first scan.
func state[T any](e *Env, name string, fresh func() *T) *T {
	if s, ok := e.states[name].(*T); ok {
		return s
	}
	if e.states == nil {
		e.states = map[string]any{}
	}
	s := fresh()
	e.states[name] = s
	return s
}

// lastMatch returns the first group of the last match of re in a file. It reads the file
// backwards in growing windows, so a large JSONL file stays cheap.
func lastMatch(path string, re *regexp.Regexp) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil
	}
	size := st.Size()
	for n := int64(64 << 10); ; n *= 4 {
		n = min(n, size)
		buf := make([]byte, n)
		if _, err := f.ReadAt(buf, size-n); err != nil && err != io.EOF {
			return nil
		}
		if ms := re.FindAllSubmatch(buf, -1); len(ms) > 0 {
			return ms[len(ms)-1][1]
		}
		if n == size {
			return nil
		}
	}
}

// unquote reads the inside of a JSON string, as a regexp found it.
func unquote(b []byte) string {
	var s string
	if json.Unmarshal(append(append([]byte{'"'}, b...), '"'), &s) != nil {
		return ""
	}
	return s
}

// statusClock notes when the status of each agent changed, for a tool that gives no time for it:
// the last activity is when kijk sees the status change.
type statusClock map[string]seenStatus

type seenStatus struct {
	status string
	at     time.Time
}

// since gives when id got its status. An agent seen for the first time had it since first.
func (c statusClock) since(id, status string, first time.Time) time.Time {
	seen, ok := c[id]
	switch {
	case !ok:
		seen = seenStatus{status, first}
	case seen.status != status:
		seen = seenStatus{status, time.Now()}
	}
	c[id] = seen
	return seen.at
}

// keep forgets the agents that are gone.
func (c statusClock) keep(ids map[string]bool) {
	for id := range c {
		if !ids[id] {
			delete(c, id)
		}
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func modTime(path string) time.Time {
	st, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return st.ModTime()
}
