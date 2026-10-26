package main

import (
	"encoding/json"
	"testing"
)

// An agent row's JSON is the shape a remote kijk of another build sends and reads.
func TestAgentRowJSON(t *testing.T) {
	for _, c := range []struct {
		row  Row
		wire string
	}{
		{Row{Root: "/u/code/shop", Repo: "shop", Branch: "main", AgentSource: &AgentSource{Cwd: "web", Names: []string{"Add the cart"},
			Agents: []RowAgent{{"claude", true}}, Busy: true, LastActivity: 1761400000000}},
			`{"root":"/u/code/shop","repo":"shop","branch":"main","cwd":"web","names":["Add the cart"],"agents":[{"type":"claude","busy":true}],"busy":true,"lastActivity":1761400000000}`},
		{Row{Root: "/u/notes", Repo: "notes", AgentSource: &AgentSource{Names: []string{"a"}, Agents: []RowAgent{{"codex", false}}}},
			`{"root":"/u/notes","repo":"notes","branch":"","cwd":"","names":["a"],"agents":[{"type":"codex","busy":false}],"busy":false,"lastActivity":0}`},
	} {
		if b, _ := json.Marshal(c.row); string(b) != c.wire {
			t.Errorf("sent %s\nwant %s", b, c.wire)
		}
		var read Row
		if err := json.Unmarshal([]byte(c.wire), &read); err != nil || read.AgentSource == nil {
			t.Fatalf("read %s: %v", c.wire, err)
		}
		if b, _ := json.Marshal(read); string(b) != c.wire {
			t.Errorf("read and sent again %s\nwant %s", b, c.wire)
		}
	}
}
