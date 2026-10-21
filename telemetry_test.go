package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kijk-dev/kijk/internal/agents"
	"github.com/kijk-dev/kijk/internal/viewer"
)

type capturedEvent struct {
	APIKey     string         `json:"api_key"`
	Event      string         `json:"event"`
	DistinctID string         `json:"distinct_id"`
	Properties map[string]any `json:"properties"`
	Timestamp  string         `json:"timestamp"`
	path, ua   string
}

type fakePostHog struct {
	mu     sync.Mutex
	events []capturedEvent
	got    chan struct{}
}

func startFakePostHog(t *testing.T) *fakePostHog {
	f := &fakePostHog{got: make(chan struct{}, 16)}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var e capturedEvent
		json.NewDecoder(r.Body).Decode(&e)
		e.path, e.ua = r.URL.Path, r.UserAgent()
		f.mu.Lock()
		f.events = append(f.events, e)
		f.mu.Unlock()
		f.got <- struct{}{}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("KIJK_TELEMETRY_URL", srv.URL+"/")
	return f
}

func (f *fakePostHog) wait(t *testing.T, n int) []capturedEvent {
	t.Helper()
	for range n {
		select {
		case <-f.got:
		case <-time.After(5 * time.Second):
			t.Fatalf("waited for %d events", n)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.events)
}

func keys(m map[string]any) []string {
	var k []string
	for key := range m {
		k = append(k, key)
	}
	slices.Sort(k)
	return k
}

func TestTelemetrySwitches(t *testing.T) {
	cases := []struct {
		name, noTelemetry, dnt, version, key string
		flag, want                           bool
	}{
		{name: "on", version: "0.3.0", key: "phc_x", want: true},
		{name: "flag", version: "0.3.0", key: "phc_x", flag: true},
		{name: "KIJK_NO_TELEMETRY", noTelemetry: "1", version: "0.3.0", key: "phc_x"},
		{name: "DO_NOT_TRACK=1", dnt: "1", version: "0.3.0", key: "phc_x"},
		{name: "DO_NOT_TRACK=true", dnt: "true", version: "0.3.0", key: "phc_x"},
		{name: "DO_NOT_TRACK=0", dnt: "0", version: "0.3.0", key: "phc_x", want: true},
		{name: "dev build", version: "dev (abc1234)", key: "phc_x"},
		{name: "no key", version: "0.3.0"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("KIJK_NO_TELEMETRY", c.noTelemetry)
			t.Setenv("DO_NOT_TRACK", c.dnt)
			if got := telemetryOn(c.flag, c.version, c.key); got != c.want {
				t.Fatalf("telemetryOn = %v", got)
			}
		})
	}
}

func TestEventsCarryOnlyTheAgreedProperties(t *testing.T) {
	dir := useConfigDir(t)
	f := startFakePostHog(t)
	tel := newTelemetry("phc_test", "0.3.0")
	tel.send("kijk started", nil)
	tel.active(time.Now(), 2, true, &agentCounts{map[string]int{"claude": 3, "codex": 2}, 1, 3})
	ev := f.wait(t, 2)

	id, _ := os.ReadFile(filepath.Join(dir, "kijk", "install-id"))
	for _, e := range ev {
		if e.path != "/i/v0/e/" || e.ua != "kijk/0.3.0" || e.APIKey != "phc_test" || e.DistinctID != string(id) {
			t.Fatalf("request: %+v", e)
		}
		if _, err := time.Parse(time.RFC3339, e.Timestamp); err != nil {
			t.Fatalf("timestamp %q", e.Timestamp)
		}
		if e.Properties["version"] != "0.3.0" || e.Properties["$os"] != osName(runtime.GOOS) || e.Properties["arch"] != runtime.GOARCH {
			t.Fatalf("properties: %v", e.Properties)
		}
	}
	slices.SortFunc(ev, func(a, b capturedEvent) int { return strings.Compare(a.Event, b.Event) })
	want := []string{"$os", "agent_copies", "agents", "arch", "folders", "machines", "remote_agents", "reviews", "version"}
	for _, at := range agents.Types {
		want = append(want, at.Name+"_agents")
	}
	slices.Sort(want)
	if ev[0].Event != "kijk active" || !slices.Equal(keys(ev[0].Properties), want) {
		t.Fatalf("kijk active: %v, want the keys %v", ev[0].Properties, want)
	}
	if p := ev[0].Properties; p["agents"] != float64(5) || p["claude_agents"] != float64(3) || p["codex_agents"] != float64(2) || p["machines"] != float64(1) || p["remote_agents"] != float64(3) || p["folders"] != float64(2) {
		t.Fatalf("counts = %v", p)
	}
	if ev[1].Event != "kijk started" || !slices.Equal(keys(ev[1].Properties), []string{"$os", "arch", "version"}) {
		t.Fatalf("kijk started: %v", ev[1].Properties)
	}
}

func TestActiveOncePerLocalDay(t *testing.T) {
	tel := &telemetry{version: "0.3.0", events: make(chan telemetryEvent, 16)}
	mon := time.Date(2026, 10, 12, 9, 0, 0, 0, time.Local)
	for _, at := range []time.Time{mon, mon.Add(time.Hour), mon.Add(15 * time.Hour), mon.Add(24 * time.Hour), mon.Add(48 * time.Hour)} {
		tel.active(at, 0, true, &agentCounts{byType: map[string]int{"claude": 3}})
	}
	if n := len(tel.events); n != 3 {
		t.Fatalf("%d kijk active events over three days", n)
	}
}

// With no agents window, kijk active waits one minute after the start or the new day, and leaves
// out the agent counts.
func TestActiveWithoutAgentScan(t *testing.T) {
	start := time.Date(2026, 10, 12, 23, 59, 30, 0, time.Local)
	tel := &telemetry{version: "0.3.0", events: make(chan telemetryEvent, 16), started: start}
	for _, s := range []int{0, 59} {
		tel.active(start.Add(time.Duration(s)*time.Second), 2, false, nil)
	}
	if n := len(tel.events); n != 0 {
		t.Fatalf("%d events within the first minute", n)
	}
	tel.active(start.Add(60*time.Second), 2, false, nil) // 00:00:30, a new day, 30 s into it
	tel.active(start.Add(80*time.Second), 2, true, nil)  // looking for agents: waits for counts
	if n := len(tel.events); n != 0 {
		t.Fatalf("%d events too early on the new day", n)
	}
	tel.active(start.Add(90*time.Second), 2, true, &agentCounts{byType: map[string]int{}})
	tel.active(start.Add(25*time.Hour), 2, false, nil)
	e1, e2 := <-tel.events, <-tel.events
	if _, ok := e1.props["agents"]; !ok || e1.props["folders"] != 2 {
		t.Fatalf("day with a scan: %v", e1.props)
	}
	if _, ok := e2.props["agents"]; ok || e2.props["folders"] != 2 || len(tel.events) != 0 {
		t.Fatalf("day without a scan: %v", e2.props)
	}
	for k := range e2.props {
		if strings.HasSuffix(k, "_agents") {
			t.Fatalf("day without a scan: %v", e2.props)
		}
	}
}

// Copy for agent and submitted reviews are counted by the local kijk, and kijk active carries the
// counts since the one before.
func TestAgentCopies(t *testing.T) {
	agentCopies.Store(0)
	reviews.Store(0)
	srv := newServer("secret", 7717, nil, newHub(), &viewer.Roots{}, nil, nil, nil)
	count := func(name string) int {
		r := httptest.NewRequest("POST", "/api/count?name="+name, nil)
		r.Host = "127.0.0.1:7717"
		r.Header.Set("Origin", "http://127.0.0.1:7717")
		r.AddCookie(&http.Cookie{Name: cookieName(7717), Value: "secret"})
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		return w.Code
	}
	tel := &telemetry{version: "0.3.0", events: make(chan telemetryEvent, 16)}
	mon := time.Date(2026, 10, 12, 9, 0, 0, 0, time.Local)
	tel.active(mon, 0, false, &agentCounts{})
	for range 4 {
		if code := count("agent-copy"); code != http.StatusOK {
			t.Fatalf("count: %d", code)
		}
	}
	for range 2 {
		if code := count("review"); code != http.StatusOK {
			t.Fatalf("count a review: %d", code)
		}
	}
	if code := count("something-else"); code != http.StatusBadRequest {
		t.Fatalf("another name: %d", code)
	}
	tel.active(mon.Add(time.Hour), 0, false, &agentCounts{}) // the same day: no event
	tel.active(mon.Add(24*time.Hour), 0, false, &agentCounts{})
	tel.active(mon.Add(48*time.Hour), 0, false, &agentCounts{})
	var got []any
	for range 3 {
		e := <-tel.events
		got = append(got, e.props["agent_copies"], e.props["reviews"])
	}
	if fmt.Sprint(got) != "[0 0 4 2 0 0]" {
		t.Fatalf("agent_copies and reviews on three days: %v", got)
	}
}

func TestAgentCountCountsSessions(t *testing.T) {
	rows := []Row{
		{Names: []string{"a", "b", "c"}, Agents: []RowAgent{{Type: "claude"}, {Type: "codex"}, {Type: "claude"}}},
		{Names: []string{"d"}, Agents: []RowAgent{{Type: "codex", Busy: true}}},
	}
	if n := agentCount(rows); n != 4 {
		t.Fatalf("agentCount = %d, want 4", n)
	}
	if c := agentTypeCounts(rows); c["claude"] != 2 || c["codex"] != 2 || len(c) != 2 {
		t.Fatalf("agentTypeCounts = %v, want 2 claude and 2 codex", c)
	}
}

func TestSlowOrFailingServerDoesNotStopLaterEvents(t *testing.T) {
	var mu sync.Mutex
	var got []string
	delivered := make(chan struct{}, 3)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var e capturedEvent
		json.NewDecoder(r.Body).Decode(&e)
		switch e.Event {
		case "error":
			w.WriteHeader(http.StatusInternalServerError)
		case "slow":
			select {
			case <-r.Context().Done():
			case <-time.After(5 * time.Second):
			}
			return
		}
		mu.Lock()
		got = append(got, e.Event)
		mu.Unlock()
		delivered <- struct{}{}
	}))
	t.Cleanup(srv.Close)
	tel := &telemetry{url: srv.URL, client: &http.Client{Timeout: 200 * time.Millisecond}, events: make(chan telemetryEvent, 16)}
	go tel.run()
	for _, name := range []string{"error", "slow", "after"} {
		tel.send(name, nil)
	}
	for range 2 {
		select {
		case <-delivered:
		case <-time.After(3 * time.Second):
			t.Fatal("events after a slow or failing request were not sent")
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(got, []string{"error", "after"}) {
		t.Fatalf("delivered %v", got)
	}
}

func TestSendNeverBlocks(t *testing.T) {
	tel := &telemetry{events: make(chan telemetryEvent, 1)}
	done := make(chan struct{})
	go func() {
		for range 5 {
			tel.send("kijk started", nil)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("send blocked on a full queue")
	}
	var off *telemetry
	off.send("kijk started", nil)
	off.active(time.Now(), 0, false, nil)
}

func TestInstallID(t *testing.T) {
	dir := useConfigDir(t)
	p := filepath.Join(dir, "kijk", "install-id")

	id := installID()
	if len(id) != 32 {
		t.Fatalf("id %q", id)
	}
	if again := installID(); again != id {
		t.Fatalf("second read %q, want %q", again, id)
	}
	os.WriteFile(p, []byte("not an id"), 0o600)
	if fresh := installID(); fresh == id || len(fresh) != 32 {
		t.Fatalf("broken file gave %q", fresh)
	}
	if b, _ := os.ReadFile(p); len(b) != 32 {
		t.Fatalf("broken file not replaced: %q", b)
	}
}

func TestInstallIDInReadOnlyFolder(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("folder permissions do not stop writes on Windows")
	}
	dir := useConfigDir(t)
	os.Chmod(dir, 0o500)
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	if a, b := installID(), installID(); len(a) != 32 || a == b {
		t.Fatalf("want a fresh per-run ID each time, got %q and %q", a, b)
	}
}

func TestNoInstallIDWhenOff(t *testing.T) {
	dir := useConfigDir(t)
	t.Setenv("KIJK_NO_TELEMETRY", "")
	t.Setenv("DO_NOT_TRACK", "1")
	var tel *telemetry
	if telemetryOn(false, "0.3.0", "phc_x") {
		tel = newTelemetry("phc_x", "0.3.0")
	}
	tel.send("kijk started", nil)
	if _, err := os.Stat(filepath.Join(dir, "kijk", "install-id")); !os.IsNotExist(err) {
		t.Fatal("install-id written while telemetry is off")
	}
}
