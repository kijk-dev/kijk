package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/http/httputil"
	"os"
	"strings"
	"sync"
	"time"
)

// machineView is one remote machine on /api/events.
type machineView struct {
	Name       string `json:"name"`
	State      string `json:"state"`
	System     string `json:"system,omitempty"`
	Message    string `json:"message,omitempty"`
	CodexError string `json:"codexError,omitempty"`
	Stale      bool   `json:"stale"`
	Rows       []Row  `json:"rows"`
}

// machineManager keeps one goroutine per enabled saved machine and follows machines.json.
type machineManager struct {
	version  string // the version to run on remote machines; "dev" for a build from source
	sshExtra []string
	changed  chan struct{}

	mu       sync.Mutex
	list     []*remoteMachine
	settings settingsFile
	log      io.Writer
}

func newMachineManager(version string) *machineManager {
	p, _ := machinesPath()
	if !isRelease(version) {
		version = "dev"
	}
	return &machineManager{version: version, changed: make(chan struct{}, 1), settings: settingsFile{path: p}, log: os.Stderr}
}

func (m *machineManager) notify() {
	select {
	case m.changed <- struct{}{}:
	default:
	}
}

// run follows machines.json, so kijk machine add, remove, disable and enable reach a running kijk.
func (m *machineManager) run(ctx context.Context) {
	for {
		m.reload()
		select {
		case <-ctx.Done():
			m.apply(nil)
			m.settings = settingsFile{path: m.settings.path} // the next run reads the file again
			return
		case <-time.After(time.Second):
		}
	}
}

// start connects to the saved machines until stop is called, which waits for run to end. The
// first read is done before it returns, so settled waits for those machines.
func (m *machineManager) start() (stop func()) {
	m.reload()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		m.run(ctx)
	}()
	return func() {
		cancel()
		<-done
	}
}

// reload applies machines.json when it changed. A broken file keeps the machines as they are,
// because dropping them would also close their logins.
func (m *machineManager) reload() {
	raw, changed := m.settings.load()
	if !changed {
		return
	}
	list, err := machinesOf(raw)
	if err != nil {
		err = fmt.Errorf("%s: %w", m.settings.path, err)
	}
	if m.settings.err != nil {
		err = m.settings.err
	}
	if err != nil {
		fmt.Fprintf(m.log, "kijk: %v. kijk keeps the machines it has until the file is fixed.\n", err)
		return
	}
	m.apply(list)
}

func (m *machineManager) apply(saved []savedMachine) {
	m.mu.Lock()
	old := map[string]*remoteMachine{}
	for _, r := range m.list {
		old[r.name] = r
	}
	var list []*remoteMachine
	for _, s := range saved {
		r := old[s.Target]
		delete(old, s.Target)
		if r == nil {
			r = &remoteMachine{name: s.Target, mm: m, ssh: newSSHClient(s.Target, m.sshExtra...), state: stateDisabled}
		}
		list = append(list, r)
		r.setEnabled(s.Enabled)
	}
	m.list = list
	m.mu.Unlock()
	for _, r := range old {
		r.setEnabled(false)
	}
	m.notify()
}

func (m *machineManager) find(name string) *remoteMachine {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.list {
		if r.name == name {
			return r
		}
	}
	return nil
}

// views is the machines part of /api/events, or nil when none is saved.
func (m *machineManager) views() []machineView {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []machineView
	for _, r := range m.list {
		out = append(out, r.view())
	}
	return out
}

// settled reports whether every enabled machine has finished its first connect attempt.
func (m *machineManager) settled() bool {
	if m == nil {
		return true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.list {
		r.mu.Lock()
		waiting := r.state == stateConnecting
		r.mu.Unlock()
		if waiting {
			return false
		}
	}
	return true
}

// remoteMachine is one saved machine and its connection to the kijk serve there.
type remoteMachine struct {
	name string
	mm   *machineManager
	ssh  *sshClient

	mu         sync.Mutex
	state      string
	message    string
	codexError string
	system     string
	rows       []Row
	proxy      http.Handler // set while connected
	stop       context.CancelFunc
	done       chan struct{}
}

func (r *remoteMachine) view() machineView {
	r.mu.Lock()
	defer r.mu.Unlock()
	rows := r.rows
	if rows == nil {
		rows = []Row{}
	}
	v := machineView{Name: r.name, State: r.state, System: r.system, Message: r.message,
		Stale: r.state != stateConnected && len(r.rows) > 0, Rows: rows}
	if r.state == stateConnected {
		v.CodexError = r.codexError
	}
	return v
}

// setEnabled starts or stops the machine's goroutine. Stopping drops its rows and closes the login.
func (r *remoteMachine) setEnabled(on bool) {
	r.mu.Lock()
	running := r.stop != nil
	if on == running {
		r.mu.Unlock()
		return
	}
	if on {
		ctx, cancel := context.WithCancel(context.Background())
		r.stop, r.done = cancel, make(chan struct{})
		r.state, r.message, r.rows = stateConnecting, "", nil
		done := r.done
		r.mu.Unlock()
		go func() {
			defer close(done)
			r.loop(ctx)
		}()
		return
	}
	stop, done := r.stop, r.done
	r.stop, r.proxy = nil, nil
	r.state, r.message, r.rows = stateDisabled, "", nil
	stop() // under the lock, so set sees the cancel and a late connect cannot undo the disable
	r.mu.Unlock()
	r.ssh.closeAll()
	go func() {
		<-done
		r.ssh.logout()
	}()
}

func (r *remoteMachine) set(ctx context.Context, f func()) {
	r.mu.Lock()
	if ctx.Err() != nil {
		r.mu.Unlock()
		return
	}
	f()
	r.mu.Unlock()
	r.mm.notify()
}

// loop connects, follows the remote agent list, and after a failure tries again with a wait
// that grows from 1 second to 2 minutes.
func (r *remoteMachine) loop(ctx context.Context) {
	wait := time.Second
	for {
		var up time.Duration
		err := r.connect(ctx)
		wasUp := err == nil
		if wasUp {
			start := time.Now()
			err = r.follow(ctx)
			up = time.Since(start)
		}
		if ctx.Err() != nil {
			return
		}
		state, msg := r.classify(err)
		// kijk reached the machine a moment ago, so it is not known to be unreachable yet.
		if wasUp && state == stateUnreachable {
			state, msg = stateReconnecting, fmt.Sprintf("The connection to kijk on %s ended.", r.name)
		}
		r.set(ctx, func() { r.state, r.message, r.proxy = state, msg, nil })
		r.ssh.closeAll()
		var sleep time.Duration
		sleep, wait = backoff(state, wait, up)
		r.sleep(ctx, sleep, state)
	}
}

// backoff gives the wait before this retry and the one after it. Only a connection that stayed
// up for a minute starts over at 1 second, so one that drops at once cannot retry every second.
// An error or a failed install waits 30 s: only a fix outside kijk helps, and a retry may
// download again.
func backoff(state string, wait, up time.Duration) (sleep, next time.Duration) {
	if up >= time.Minute {
		wait = time.Second
	}
	next = min(wait*2, 2*time.Minute)
	if state == stateError || state == stateInstallFailed {
		return 30 * time.Second, next
	}
	return wait, next
}

func (r *remoteMachine) classify(err error) (string, string) {
	if x, ok := errors.AsType[*installError](err); ok {
		return stateInstallFailed, fmt.Sprintf("Could not install kijk on %s: %v", r.name, x.err)
	}
	if x, ok := errors.AsType[*remoteError](err); ok {
		return stateError, x.Error()
	}
	return classify(r.name, err)
}

// sleep waits before the next try. A machine that needs a login tries at once when the user
// opens one with kijk machine login.
func (r *remoteMachine) sleep(ctx context.Context, d time.Duration, state string) {
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		select {
		case <-ctx.Done():
			return
		case <-time.After(min(time.Second, time.Until(end))):
		}
		if state == stateNeedsLogin {
			if _, err := os.Stat(r.ssh.ctl); err == nil {
				return
			}
		}
	}
}

// remoteError is a problem on the remote machine that a retry will not fix by itself.
type remoteError struct{ msg string }

func (e *remoteError) Error() string { return e.msg }

// connect opens the login, finds or starts kijk serve, installs it when needed, and checks
// that it answers through a pipe.
func (r *remoteMachine) connect(ctx context.Context) error {
	cctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	if err := r.ssh.login(cctx); err != nil {
		return err
	}
	b, err := bootstrap(cctx, r.ssh, r.mm.version)
	if err != nil {
		return err
	}
	r.set(ctx, func() { r.system = b.system })
	if b.need == "need-install" {
		if b.goos == "" {
			return &remoteError{fmt.Sprintf("kijk has no release for %s (%s)", r.name, b.system)}
		}
		if err := installRemote(cctx, r.ssh, r.mm.version, b.goos, b.goarch); err != nil {
			return err
		}
		if b, err = bootstrap(cctx, r.ssh, r.mm.version); err != nil {
			return err
		}
	}
	switch b.need {
	case "no-kijk":
		return &remoteError{fmt.Sprintf("kijk is not installed on %s. A kijk built from source does not install itself, so put kijk on the PATH there.", r.name)}
	case "need-install":
		return &installError{errors.New("the copied kijk is not there")}
	case "start-failed":
		msg := fmt.Sprintf("kijk serve did not start on %s", r.name)
		if b.log != "" {
			msg += ": " + firstLine(b.log)
		}
		return &remoteError{msg}
	}
	addr := fmt.Sprintf("127.0.0.1:%d", b.serve.Port)
	t := r.ssh.transport(addr)
	cookie := &http.Cookie{Name: cookieName(b.serve.Port), Value: b.serve.Token}
	if err := ping(cctx, t, addr, cookie); err != nil {
		return err
	}
	proxy := newRemoteProxy(t, addr, cookie)
	r.set(ctx, func() { r.state, r.message, r.proxy = stateConnected, "", proxy })
	return nil
}

func ping(ctx context.Context, t http.RoundTripper, addr string, cookie *http.Cookie) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://"+addr+"/api/ping", nil)
	req.AddCookie(cookie)
	resp, err := t.RoundTrip(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16))
	if resp.StatusCode != http.StatusOK || string(body) != "kijk" {
		return &remoteError{fmt.Sprintf("kijk serve did not answer as expected (HTTP %d)", resp.StatusCode)}
	}
	return nil
}

// follow keeps the remote /api/events stream open and stores its rows, until the stream breaks.
func (r *remoteMachine) follow(ctx context.Context) error {
	r.mu.Lock()
	proxy, _ := r.proxy.(*remoteProxy)
	r.mu.Unlock()
	if proxy == nil {
		return errors.New("not connected")
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://"+proxy.addr+"/api/events", nil)
	req.AddCookie(proxy.cookie)
	resp, err := proxy.transport.RoundTrip(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &remoteError{fmt.Sprintf("kijk on %s refused the agent list (HTTP %d)", r.name, resp.StatusCode)}
	}
	rd := bufio.NewReaderSize(resp.Body, 64<<10)
	for {
		line, err := rd.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return fmt.Errorf("the connection to kijk on %s ended", r.name)
			}
			return err
		}
		data, ok := strings.CutPrefix(strings.TrimRight(line, "\r\n"), "data: ")
		if !ok {
			continue
		}
		var msg struct {
			Rows       []Row  `json:"rows"`
			Error      string `json:"error"`
			CodexError string `json:"codexError"`
		}
		if json.Unmarshal([]byte(data), &msg) != nil {
			continue
		}
		r.set(ctx, func() { r.rows, r.message, r.codexError = msg.Rows, msg.Error, msg.CodexError })
	}
}

// counts gives the number of saved machines and of agents on connected ones, for telemetry.
func (m *machineManager) counts() (machines, agents int) {
	for _, v := range m.views() {
		machines++
		if v.State == stateConnected {
			agents += agentCount(v.Rows)
		}
	}
	return machines, agents
}

// remoteProxy relays the browser's /m/<machine>/api/ calls to the kijk serve on that machine.
type remoteProxy struct {
	addr      string
	cookie    *http.Cookie
	transport *http.Transport
	rp        *httputil.ReverseProxy
}

func newRemoteProxy(t *http.Transport, addr string, cookie *http.Cookie) *remoteProxy {
	p := &remoteProxy{addr: addr, cookie: cookie, transport: t}
	p.rp = &httputil.ReverseProxy{
		Transport:     t,
		FlushInterval: -1, // event streams pass through at once
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme, pr.Out.URL.Host, pr.Out.Host = "http", addr, addr
			pr.Out.URL.Path = pr.In.URL.Path[strings.Index(pr.In.URL.Path, "/api/"):]
			pr.Out.URL.RawPath = ""
			// The local token never leaves this machine; the local guard already checked the origin.
			pr.Out.Header.Del("Cookie")
			pr.Out.Header.Del("Origin")
			pr.Out.Header.Del("Referer")
			pr.Out.AddCookie(cookie)
		},
		ModifyResponse: func(resp *http.Response) error {
			// Only what the UI reads passes: a remote kijk must not set local headers, or serve a
			// type the local page could run as script.
			ct := "text/plain; charset=utf-8"
			if t, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type")); t == "application/json" || t == "text/event-stream" {
				ct = t
			}
			h := http.Header{}
			for _, k := range []string{"Cache-Control", "Content-Length"} {
				if v := resp.Header.Get(k); v != "" {
					h.Set(k, v)
				}
			}
			h.Set("Content-Type", ct)
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("Content-Security-Policy", "sandbox")
			resp.Header = h
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			http.Error(w, err.Error(), http.StatusBadGateway)
		},
	}
	return p
}

func (p *remoteProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) { p.rp.ServeHTTP(w, r) }

// serveMachine is the /m/{machine}/api/ route.
func (m *machineManager) serveMachine(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("machine")
	rest := strings.TrimPrefix(req.URL.Path, "/m/"+name)
	if rest == "/api/update" || rest == "/api/restart" || !strings.HasPrefix(rest, "/api/") {
		http.NotFound(w, req)
		return
	}
	r := m.find(name)
	if r == nil {
		http.Error(w, "No saved machine is named "+name, http.StatusNotFound)
		return
	}
	r.mu.Lock()
	proxy, state, msg := r.proxy, r.state, r.message
	r.mu.Unlock()
	if proxy == nil {
		if msg == "" {
			msg = fmt.Sprintf("%s is %s", name, strings.ReplaceAll(state, "-", " "))
		}
		http.Error(w, msg, http.StatusServiceUnavailable)
		return
	}
	proxy.ServeHTTP(w, req)
}
