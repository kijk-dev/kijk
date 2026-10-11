package main

import (
	"encoding/json"
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
	tel.scanned(12, time.Now())
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
	if ev[0].Event != "kijk active" || !slices.Equal(keys(ev[0].Properties), []string{"$os", "agents", "arch", "version"}) {
		t.Fatalf("kijk active: %v", ev[0].Properties)
	}
	if ev[0].Properties["agents"] != float64(12) {
		t.Fatalf("agents = %v", ev[0].Properties["agents"])
	}
	if ev[1].Event != "kijk started" || !slices.Equal(keys(ev[1].Properties), []string{"$os", "arch", "version"}) {
		t.Fatalf("kijk started: %v", ev[1].Properties)
	}
}

func TestActiveOncePerLocalDay(t *testing.T) {
	tel := &telemetry{version: "0.3.0", events: make(chan telemetryEvent, 16)}
	mon := time.Date(2026, 10, 12, 9, 0, 0, 0, time.Local)
	for _, at := range []time.Time{mon, mon.Add(time.Hour), mon.Add(15 * time.Hour), mon.Add(24 * time.Hour), mon.Add(48 * time.Hour)} {
		tel.scanned(3, at)
	}
	if n := len(tel.events); n != 3 {
		t.Fatalf("%d kijk active events over three days", n)
	}
}

func TestAgentCountCountsSessions(t *testing.T) {
	rows := []Row{{Names: []string{"a", "b", "c"}}, {Names: []string{"d"}}}
	if n := agentCount(rows); n != 4 {
		t.Fatalf("agentCount = %d, want 4", n)
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
	off.scanned(1, time.Now())
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
