package main

// Row is one folder on one machine in the rail. It is there for each source that is set: agents
// work in it, or it is in the folders list, or both. A row without a source is never built.
type Row struct {
	Machine string `json:"machine,omitempty"` // "" is the local machine
	Root    string `json:"root"`
	Repo    string `json:"repo"`
	Branch  string `json:"branch"`
	// Embedded, so an agent row's JSON stays as it was and a remote kijk of another build reads it.
	*AgentSource
}

// AgentSource is what the agent scan knows about a row.
type AgentSource struct {
	Cwd          string     `json:"cwd"` // the newest agent's folder, relative to Root with slashes
	Names        []string   `json:"names"`
	Agents       []RowAgent `json:"agents"`       // one per name, in the same order
	Busy         bool       `json:"busy"`         // any agent is Busy
	LastActivity int64      `json:"lastActivity"` // unix ms
}
