package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/kijk-dev/kijk/internal/agents"
)

// posthogKey can only write events, so it is safe in public source.
var posthogKey = "phc_yEdkmbhNtRr3RpXAbiLU386uNtFFXBNjkaohnHZSCL4N"

const defaultTelemetryHost = "https://eu.i.posthog.com"

func telemetryURL() string {
	host := defaultTelemetryHost
	if v := os.Getenv("KIJK_TELEMETRY_URL"); v != "" {
		host = strings.TrimRight(v, "/")
	}
	return host + "/i/v0/e/"
}

func telemetryOn(flagOff bool, version, key string) bool {
	if flagOff || os.Getenv("KIJK_NO_TELEMETRY") != "" || !isRelease(version) || key == "" {
		return false
	}
	dnt := os.Getenv("DO_NOT_TRACK")
	return dnt == "" || dnt == "0"
}

type telemetryEvent struct {
	name  string
	props map[string]any
	at    time.Time
}

// telemetry sends usage events in the background. A nil *telemetry is off and sends nothing.
type telemetry struct {
	key, version, id, url string
	client                *http.Client
	events                chan telemetryEvent
	started               time.Time
	lastActive            string // only touched by watchSessions
}

func newTelemetry(key, version string) *telemetry {
	t := &telemetry{
		key:     key,
		version: version,
		id:      installID(),
		url:     telemetryURL(),
		client:  &http.Client{Timeout: 5 * time.Second},
		events:  make(chan telemetryEvent, 16),
		started: time.Now(),
	}
	go t.run()
	return t
}

// send queues an event and drops it when the queue is full, so it never blocks the caller.
func (t *telemetry) send(name string, extra map[string]any) {
	if t == nil {
		return
	}
	props := map[string]any{"version": t.version, "$os": osName(runtime.GOOS), "arch": runtime.GOARCH}
	for k, v := range extra {
		props[k] = v
	}
	select {
	case t.events <- telemetryEvent{name, props, time.Now()}:
	default:
	}
}

// agentCopies counts the uses of Copy for agent since the last "kijk active", reviews the reviews
// submitted.
var agentCopies, reviews atomic.Int64

// countUse counts a use the page reports. Only names kijk knows are counted.
func countUse(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Query().Get("name") {
	case "agent-copy":
		agentCopies.Add(1)
	case "review":
		reviews.Add(1)
	default:
		http.Error(w, "unknown count", http.StatusBadRequest)
		return
	}
	reply(w, struct{}{})
}

// agentCounts are the agent numbers on "kijk active". byType counts local agents per agent type.
type agentCounts struct {
	byType           map[string]int
	machines, remote int
}

// active sends "kijk active" once a local day. While kijk looks for agents it waits for their
// counts; otherwise it sends one minute after kijk starts or the date changes, without them.
func (t *telemetry) active(now time.Time, folders int, looking bool, counts *agentCounts) {
	if t == nil {
		return
	}
	day := now.Format(time.DateOnly)
	if day == t.lastActive {
		return
	}
	if counts == nil {
		y, m, d := now.Date()
		if looking || now.Sub(later(t.started, time.Date(y, m, d, 0, 0, 0, 0, now.Location()))) < time.Minute {
			return
		}
	}
	t.lastActive = day
	props := map[string]any{"folders": folders, "agent_copies": agentCopies.Swap(0), "reviews": reviews.Swap(0)}
	if counts != nil {
		n := 0
		for _, c := range counts.byType {
			n += c
		}
		props["agents"] = n
		for _, at := range agents.Types {
			props[at.Name+"_agents"] = counts.byType[at.Name]
		}
		props["machines"] = counts.machines
		props["remote_agents"] = counts.remote
	}
	t.send("kijk active", props)
}

func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// agentCount counts sessions, not rows: one row holds every agent in the same repo.
func agentCount(rows []Row) int {
	n := 0
	for _, r := range rows {
		n += len(r.Names)
	}
	return n
}

func agentTypeCounts(rows []Row) map[string]int {
	byType := map[string]int{}
	for _, r := range rows {
		for _, a := range r.Agents {
			byType[a.Type]++
		}
	}
	return byType
}

func (t *telemetry) run() {
	for e := range t.events {
		t.post(e)
	}
}

func (t *telemetry) post(e telemetryEvent) {
	body, _ := json.Marshal(map[string]any{
		"api_key":     t.key,
		"event":       e.name,
		"distinct_id": t.id,
		"properties":  e.props,
		"timestamp":   e.at.UTC().Format(time.RFC3339),
	})
	req, err := http.NewRequest("POST", t.url, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "kijk/"+t.version)
	if resp, err := t.client.Do(req); err == nil {
		resp.Body.Close()
	}
}

func osName(goos string) string {
	switch goos {
	case "darwin":
		return "macOS"
	case "linux":
		return "Linux"
	case "windows":
		return "Windows"
	}
	return goos
}

func installIDPath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "kijk", "install-id"), nil
}

// installID reads the saved ID, or saves a new one. When it cannot be saved, the ID lasts this run.
func installID() string {
	p, err := installIDPath()
	if err != nil {
		return newInstallID()
	}
	if id, ok := readInstallID(p); ok {
		return id
	}
	id := newInstallID()
	if os.MkdirAll(filepath.Dir(p), 0o700) != nil {
		return id
	}
	f, err := os.CreateTemp(filepath.Dir(p), ".install-id-*")
	if err != nil {
		return id
	}
	_, werr := f.WriteString(id)
	if cerr := f.Close(); werr != nil || cerr != nil || os.Rename(f.Name(), p) != nil {
		os.Remove(f.Name())
		return id
	}
	// Read back: a kijk on another port may have saved its own ID at the same moment.
	if saved, ok := readInstallID(p); ok {
		return saved
	}
	return id
}

func readInstallID(p string) (string, bool) {
	b, err := os.ReadFile(p)
	if err != nil {
		return "", false
	}
	id := strings.TrimSpace(string(b))
	if _, err := hex.DecodeString(id); err != nil || len(id) != 32 {
		return "", false
	}
	return id, true
}

func newInstallID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}
