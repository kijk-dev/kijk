package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/kijk-dev/kijk/internal/viewer"
)

func testRail() (*rail, *hub, *viewer.Roots) {
	h, rts := newHub(), &viewer.Roots{}
	return newRail(h, rts), h, rts
}

// shown is the message a window that opens now gets.
func shown(t *testing.T, h *hub) railMessage {
	t.Helper()
	h.mu.Lock()
	b := h.last
	h.mu.Unlock()
	var m railMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("message %s: %v", b, err)
	}
	return m
}

func jsonOf(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

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

// railParts are the agent rows, one remote machine, saved folders and their facts that the rail
// tests merge.
func railParts() (agents *agentPart, saved []savedFolder, facts map[string]folderFacts) {
	shop, api, notes := abs("/code/shop"), abs("/code/api"), abs("/code/notes")
	agents = &agentPart{
		rows: []Row{
			{Root: shop, Repo: "shop", Branch: "cart", AgentSource: &AgentSource{Cwd: "web", Names: []string{"Add the cart"}, Agents: []RowAgent{{"claude", true}}, Busy: true, LastActivity: 3}},
			{Root: api, Repo: "api", AgentSource: &AgentSource{Names: []string{"Fix login"}, Agents: []RowAgent{{"codex", false}}, LastActivity: 2}},
		},
		machines: []machineView{{Name: "azvm", State: stateConnected, Rows: []Row{
			{Root: notes, Repo: "notes", Branch: "main", AgentSource: &AgentSource{Names: []string{"Deploy"}, Agents: []RowAgent{{"claude", false}}, LastActivity: 1}},
		}}},
	}
	saved = []savedFolder{{Path: notes}, {Path: shop, Open: filepath.Join(shop, "docs")}, {Path: api}}
	facts = map[string]folderFacts{notes: {branch: "main", add: 3, del: 1}, shop: {branch: "old", add: 5}, api: {branch: "dev"}}
	return agents, saved, facts
}

func TestMergeRows(t *testing.T) {
	agents, saved, facts := railParts()
	shop, api, notes, code := abs("/code/shop"), abs("/code/api"), abs("/code/notes"), abs("/code")
	both := agents.rows[0]
	both.Saved = &SavedSource{At: 1, Parent: code, Open: "docs", Add: 5, Counted: true}
	noBranch := agents.rows[1]
	noBranch.Branch, noBranch.Saved = "dev", &SavedSource{At: 2, Parent: code, Counted: true}
	remote := agents.machines[0].Rows[0]
	remote.Machine = "azvm"
	for _, c := range []struct {
		name   string
		agents *agentPart
		facts  map[string]folderFacts
		want   []Row
	}{
		{"looking", agents, facts, []Row{
			both,     // one row, opened at the agent's cwd or at the saved folder; the agent's branch wins
			noBranch, // the agent scan has no branch here, so the folder's own is used
			remote,   // a remote folder at the same path as a local saved folder is another row
			{Root: notes, Repo: "notes", Branch: "main", Saved: &SavedSource{At: 0, Parent: code, Add: 3, Del: 1, Counted: true}},
		}},
		{"not looking, not counted", nil, nil, []Row{
			{Root: notes, Repo: "notes", Saved: &SavedSource{At: 0, Parent: code}},
			{Root: shop, Repo: "shop", Saved: &SavedSource{At: 1, Parent: code, Open: "docs"}},
			{Root: api, Repo: "api", Saved: &SavedSource{At: 2, Parent: code}},
		}},
	} {
		rl, h, _ := testRail()
		rl.setSaved(saved, nil)
		rl.setFacts(c.facts)
		if c.agents != nil {
			rl.setAgents(c.agents)
		}
		if got, want := jsonOf(shown(t, h).Rows), jsonOf(c.want); got != want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, want)
		}
	}
}

// What the server may read follows the local rows: saved, or with agents.
func TestRailRoots(t *testing.T) {
	agents, _, _ := railParts()
	shop, notes, other := abs("/code/shop"), abs("/code/notes"), abs("/code/other")
	agents.machines[0].Rows[0].Root = other
	rl, _, rts := testRail()
	rl.setSaved([]savedFolder{{Path: notes}}, nil)
	rl.setAgents(agents)
	if !rts.Has(shop) || !rts.Has(notes) || rts.Has(other) {
		t.Fatal("Roots are the local rows, and only those")
	}
	rl.setAgents(nil)
	if rts.Has(shop) || !rts.Has(notes) {
		t.Fatal("an agent folder stays readable after kijk stops looking, or a saved one went")
	}
	rl.setSaved(nil, nil)
	if rts.Has(notes) {
		t.Fatal("a removed folder is still readable")
	}
}

// The Folders list never waits for the agent scan; scanned says when the Agents list is known.
func TestRailScanned(t *testing.T) {
	notes := abs("/code/notes")
	rl, h, _ := testRail()
	rl.setSaved([]savedFolder{{Path: notes}}, nil)
	if m := shown(t, h); m.Scanned || len(m.Rows) != 1 || m.Rows[0].Root != notes {
		t.Fatalf("before the scan: %+v", m)
	}
	rl.setAgents(&agentPart{})
	if m := shown(t, h); !m.Scanned || len(m.Rows) != 1 {
		t.Fatalf("after a scan that found nothing: %+v", m)
	}
	rl.setAgents(nil)
	if m := shown(t, h); m.Scanned {
		t.Fatalf("after kijk stopped looking: %+v", m)
	}
}

// Each request gets a higher seq, and rides in the message with the rows it names.
func TestRailRequest(t *testing.T) {
	shop := abs("/code/shop")
	rl, h, _ := testRail()
	rl.setSaved([]savedFolder{{Path: shop}}, nil)
	if seq := rl.request(openRequest{Root: shop, Cwd: "web", List: listFolders, Added: true}); seq != 1 {
		t.Fatalf("first seq %d", seq)
	}
	if m := shown(t, h); m.Open != (openRequest{Seq: 1, Root: shop, Cwd: "web", List: listFolders, Added: true}) || len(m.Rows) != 1 {
		t.Fatalf("message %+v", m)
	}
	if seq := rl.request(openRequest{List: listFolders}); seq != 2 || shown(t, h).Open != (openRequest{Seq: 2, List: listFolders}) {
		t.Fatalf("second seq %d, open %+v", seq, shown(t, h).Open)
	}
}

// The message the window and a following kijk read: rows of both lists, flat, with the machines'
// state apart.
func TestRailMessage(t *testing.T) {
	if filepath.Separator != '/' {
		t.Skip("the parent folder is written with the system's separator")
	}
	rl, h, _ := testRail()
	shop, notes := "/u/code/shop", "/u/code/notes"
	rl.setSaved([]savedFolder{{Path: shop}, {Path: notes}}, fmt.Errorf("cannot save the folders list"))
	rl.setFacts(map[string]folderFacts{shop: {branch: "main", add: 40, del: 12}})
	rl.setKijk(kijkState{Version: "0.9.0"})
	rl.request(openRequest{Root: shop, PR: 477})
	rl.setAgents(&agentPart{
		rows: []Row{
			{Root: shop, Repo: "shop", Branch: "cart", AgentSource: &AgentSource{Cwd: "web", Names: []string{"Add the cart"}, Agents: []RowAgent{{"claude", true}}, Busy: true, LastActivity: 1761400000000}},
			{Root: "/u/code/api", Repo: "api", Branch: "main", AgentSource: &AgentSource{Names: []string{"Fix login"}, Agents: []RowAgent{{"codex", false}}, LastActivity: 1761300000000}},
		},
		errs: []typeError{{"codex", "cannot read"}},
		machines: []machineView{{Name: "azvm", State: stateConnected, System: "Linux x86_64", Rows: []Row{
			{Root: "/home/u/infra", Repo: "infra", Branch: "main", AgentSource: &AgentSource{Names: []string{"Deploy"}, Agents: []RowAgent{{"claude", false}}, LastActivity: 1761200000000}},
		}}},
	})
	h.mu.Lock()
	got := string(h.last)
	h.mu.Unlock()
	want := `{"rows":[` +
		`{"root":"/u/code/shop","repo":"shop","branch":"cart","cwd":"web","names":["Add the cart"],"agents":[{"type":"claude","busy":true}],"busy":true,"lastActivity":1761400000000,"saved":{"at":0,"parent":"/u/code","add":40,"del":12,"counted":true}},` +
		`{"root":"/u/code/api","repo":"api","branch":"main","cwd":"","names":["Fix login"],"agents":[{"type":"codex","busy":false}],"busy":false,"lastActivity":1761300000000},` +
		`{"machine":"azvm","root":"/home/u/infra","repo":"infra","branch":"main","cwd":"","names":["Deploy"],"agents":[{"type":"claude","busy":false}],"busy":false,"lastActivity":1761200000000},` +
		`{"root":"/u/code/notes","repo":"notes","branch":"","saved":{"at":1,"parent":"/u/code","add":0,"del":0,"counted":false}}],` +
		`"scanned":true,"machines":[{"name":"azvm","state":"connected","system":"Linux x86_64","stale":false}],` +
		`"errors":[{"type":"codex","error":"cannot read"}],"listError":"cannot save the folders list",` +
		`"open":{"seq":1,"root":"/u/code/shop","pr":477},"kijk":{"version":"0.9.0","canRestart":false}}`
	if got != want {
		t.Errorf("message\n got %s\nwant %s", got, want)
	}
}

// BenchmarkMergeRows merges a rail of 30 rows, as every change does.
func BenchmarkMergeRows(b *testing.B) {
	agents := &agentPart{}
	var saved []savedFolder
	for i := range 20 {
		root := fmt.Sprintf("/u/code/repo%d", i)
		agents.rows = append(agents.rows, Row{Root: root, Repo: filepath.Base(root), Branch: "main",
			AgentSource: &AgentSource{Names: []string{"a task"}, Agents: []RowAgent{{"claude", i%3 == 0}}}})
		if i%4 == 0 {
			saved = append(saved, savedFolder{Path: root})
		}
	}
	for i := range 5 {
		saved = append(saved, savedFolder{Path: fmt.Sprintf("/u/notes/n%d", i)})
	}
	agents.machines = []machineView{{Name: "azvm", State: stateConnected, Rows: agents.rows[:5]}}
	rl, _, _ := testRail()
	rl.setSaved(saved, nil)
	facts := map[string]folderFacts{}
	for _, f := range saved {
		facts[f.Path] = folderFacts{branch: "main", add: 3}
	}
	rl.setFacts(facts)
	b.ReportAllocs()
	for b.Loop() {
		rl.setAgents(agents)
	}
}
