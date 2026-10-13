package main

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The fake ssh is the test binary started through a link named ssh. It runs the remote command
// on this machine with a per-target $HOME, so the bootstrap, the install and kijk serve are real.

// fakeSSHConfig is the file $KIJK_FAKE_SSH points to.
type fakeSSHConfig struct {
	Targets map[string]fakeTarget `json:"targets"`
}

type fakeTarget struct {
	Home string `json:"home"` // targets with one home are one user on one machine
	Mode string `json:"mode,omitempty"`
	// Gate pauses a sh -c call after 1 MB of stdin: it creates Gate+".paused" and goes on once
	// Gate exists.
	Gate string `json:"gate,omitempty"`
	// What ssh -G prints: by default user kijk, the target as host name, and port 22.
	User     string `json:"user,omitempty"`
	HostName string `json:"hostname,omitempty"`
	Jump     string `json:"jump,omitempty"`
}

// Modes of a fake target. The messages are what OpenSSH prints.
const (
	modeOffline      = "offline"
	modeWrongKey     = "wrong-key"
	modeNoForwarding = "no-forwarding"
)

func fakeSSH(args []string) int {
	var ctl, forward, control string
	master, resolve, noTTY, noRemoteCommand := false, false, false, false
	i := 0
	for ; i < len(args) && strings.HasPrefix(args[i], "-"); i++ {
		switch args[i] {
		case "-o", "-F", "-W", "-O":
			if i+1 == len(args) {
				return sshFail("option %s needs a value", args[i])
			}
			i++
			switch args[i-1] {
			case "-o":
				if v, ok := strings.CutPrefix(args[i], "ControlPath="); ok {
					ctl = v
				}
				noRemoteCommand = noRemoteCommand || args[i] == "RemoteCommand=none"
			case "-W":
				forward = args[i]
			case "-O":
				control = args[i]
			}
		case "-M":
			master = true
		case "-G":
			resolve = true
		case "-T":
			noTTY = true
		case "-N", "-f":
		default:
			return sshFail("fake ssh: unknown option %s", args[i])
		}
	}
	if i == len(args) {
		return sshFail("usage: ssh destination [command]")
	}
	target, command := args[i], args[i+1:]
	var conf fakeSSHConfig
	if b, err := os.ReadFile(os.Getenv("KIJK_FAKE_SSH")); err != nil || json.Unmarshal(b, &conf) != nil {
		return sshFail("fake ssh: no config in $KIJK_FAKE_SSH")
	}
	t, ok := conf.Targets[target]
	if resolve {
		fmt.Printf("user %s\nhostname %s\nport 22\n", cmp.Or(t.User, "kijk"), cmp.Or(t.HostName, target))
		if t.Jump != "" {
			fmt.Printf("proxyjump %s\n", t.Jump)
		}
		return 0
	}
	if !ok {
		return sshFail("ssh: Could not resolve hostname %s: Name or service not known", target)
	}

	switch control {
	case "":
	case "check":
		if !exists(ctl) {
			return sshFail("Control socket connect(%s): No such file or directory", ctl)
		}
		fmt.Fprintln(os.Stderr, "Master running")
		return 0
	case "exit":
		if os.Remove(ctl) != nil {
			return sshFail("Control socket connect(%s): No such file or directory", ctl)
		}
		fmt.Fprintln(os.Stderr, "Exit request sent.")
		return 0
	default:
		return sshFail("fake ssh: unknown -O %s", control)
	}

	switch t.Mode {
	case modeOffline:
		return sshFail("ssh: connect to host %s port 22: Connection refused", target)
	case modeWrongKey:
		return sshFail("kijk@%s: Permission denied (publickey).", target)
	}
	// ControlMaster=auto: the first connection becomes the shared login.
	if !exists(ctl) {
		if err := os.WriteFile(ctl, nil, 0o600); err != nil {
			return sshFail("fake ssh: %v", err)
		}
	}
	if master {
		return 0
	}

	if forward != "" {
		return fakeForward(forward, ctl, t.Mode == modeNoForwarding)
	}
	if len(command) == 0 {
		return 0
	}
	// As if the user's config had RemoteCommand and RequestTTY force: both break a command call.
	if !noRemoteCommand {
		return sshFail("Cannot execute command-line and remote command.")
	}
	if !noTTY {
		return sshFail("fake ssh: a command needs -T, or RequestTTY in the user's config gives it a terminal")
	}
	// sshd joins the words with spaces and hands them to the user's shell.
	cmd := exec.Command("sh", "-c", strings.Join(command, " "))
	cmd.Dir, cmd.Env = t.Home, remoteEnv(t.Home)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if t.Gate == "" || len(command) < 2 || command[0] != "sh" || command[1] != "-c" {
		cmd.Stdin = os.Stdin
	} else {
		w, err := cmd.StdinPipe()
		if err != nil {
			return sshFail("fake ssh: %v", err)
		}
		go func() {
			defer w.Close()
			io.CopyN(w, os.Stdin, 1<<20)
			os.WriteFile(t.Gate+".paused", nil, 0o600)
			for !exists(t.Gate) {
				time.Sleep(20 * time.Millisecond)
			}
			io.Copy(w, os.Stdin)
		}()
	}
	if err := cmd.Run(); err != nil {
		if x, ok := errors.AsType[*exec.ExitError](err); ok {
			return x.ExitCode()
		}
		return sshFail("fake ssh: %v", err)
	}
	return 0
}

// fakeForward is ssh -W. Like a real mux client, it ends when the shared login closes.
func fakeForward(addr, ctl string, refused bool) int {
	if refused {
		return sshFail("channel 0: open failed: administratively prohibited: open failed\nstdio forwarding failed")
	}
	c, err := net.Dial("tcp", addr)
	if err != nil {
		return sshFail("channel 0: open failed: connect failed: Connection refused\nstdio forwarding failed")
	}
	go func() {
		io.Copy(c, os.Stdin)
		c.(*net.TCPConn).CloseWrite()
	}()
	done := make(chan struct{})
	go func() {
		io.Copy(os.Stdout, c)
		close(done)
	}()
	for {
		select {
		case <-done:
			return 0
		case <-time.After(100 * time.Millisecond):
			if !exists(ctl) {
				return 0
			}
		}
	}
}

// remoteEnv keeps the remote command away from this user's kijk and Claude folders.
func remoteEnv(home string) []string {
	env := []string{"HOME=" + home}
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if k == "HOME" || k == "CLAUDE_CONFIG_DIR" || strings.HasPrefix(k, "XDG_") || strings.HasPrefix(k, "KIJK_") {
			continue
		}
		env = append(env, kv)
	}
	return env
}

func sshFail(format string, a ...any) int {
	fmt.Fprintf(os.Stderr, format+"\n", a...)
	return 255
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// fakeServe is a release published by fakeMachines.release, started by the bootstrap.
func fakeServe(version string) int {
	kijkVersion = version
	serveIdle = 2 * time.Minute // in case a test does not stop it
	if err := runServe(version); err != nil {
		fmt.Fprintln(os.Stderr, "kijk serve:", err)
		return 1
	}
	return 0
}

// fakeMachines puts the fake ssh first on PATH for one test.
type fakeMachines struct {
	t    *testing.T
	dir  string
	id   string
	conf fakeSSHConfig
}

func newFakeMachines(t *testing.T) *fakeMachines {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("remote machines need macOS or Linux")
	}
	f := &fakeMachines{t: t, dir: t.TempDir(), id: strconv.FormatInt(time.Now().UnixNano(), 36), conf: fakeSSHConfig{Targets: map[string]fakeTarget{}}}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(f.dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(exe, filepath.Join(bin, "ssh")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("KIJK_FAKE_SSH", filepath.Join(f.dir, "ssh.json"))
	useCacheDir(t)
	t.Cleanup(f.cleanup)
	return f
}

// add saves a target that logs in to the remote home named home, and returns the target. The
// name is made unique, so the fake login never touches a real one in sshDir.
func (f *fakeMachines) add(name, home, mode string) string {
	f.t.Helper()
	target := name + "-" + f.id
	t := fakeTarget{Home: filepath.Join(f.dir, "remote", home), Mode: mode}
	if err := os.MkdirAll(t.Home, 0o755); err != nil {
		f.t.Fatal(err)
	}
	f.conf.Targets[target] = t
	f.save()
	return target
}

func (f *fakeMachines) home(target string) string { return f.conf.Targets[target].Home }

// with changes how the fake ssh treats target.
func (f *fakeMachines) with(target string, change func(*fakeTarget)) {
	f.t.Helper()
	t := f.conf.Targets[target]
	change(&t)
	f.conf.Targets[target] = t
	f.save()
}

// pause makes the target's next install stop halfway. It returns the file that appears then, and
// the one that lets it go on.
func (f *fakeMachines) pause(target string) (paused, resume string) {
	f.t.Helper()
	t := f.conf.Targets[target]
	t.Gate = filepath.Join(f.dir, target+".gate")
	f.conf.Targets[target] = t
	f.save()
	return t.Gate + ".paused", t.Gate
}

func (f *fakeMachines) save() {
	f.t.Helper()
	b, _ := json.Marshal(f.conf)
	if err := os.WriteFile(os.Getenv("KIJK_FAKE_SSH"), b, 0o600); err != nil {
		f.t.Fatal(err)
	}
}

// release publishes the test binary as kijk version for this machine.
func (f *fakeMachines) release(version string) {
	startFakeRelease(f.t, version, releaseOf(selfBytes()))
}

func (f *fakeMachines) manager(version string, targets ...string) *machineManager {
	mm := newMachineManager(version)
	var saved []savedMachine
	for _, t := range targets {
		saved = append(saved, savedMachine{t, true})
	}
	mm.apply(saved)
	f.t.Cleanup(func() { mm.apply(nil) })
	return mm
}

// remoteConfig is the remote kijk's config folder in home, as the bootstrap finds it.
func remoteConfig(home string) string {
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support", "kijk")
	}
	return filepath.Join(home, ".config", "kijk")
}

func (f *fakeMachines) serveState(target, version string) serveState {
	var st serveState
	b, _ := os.ReadFile(filepath.Join(remoteConfig(f.home(target)), "serve-"+version+".json"))
	json.Unmarshal(b, &st)
	return st
}

// cleanup stops every remote kijk the test started and removes the fake logins.
func (f *fakeMachines) cleanup() {
	files, _ := filepath.Glob(filepath.Join(f.dir, "remote", "*"))
	for _, home := range files {
		states, _ := filepath.Glob(filepath.Join(remoteConfig(home), "serve-*.json"))
		for _, s := range states {
			var st serveState
			if b, err := os.ReadFile(s); err == nil && json.Unmarshal(b, &st) == nil && st.PID > 0 {
				if p, err := os.FindProcess(st.PID); err == nil {
					p.Kill()
				}
			}
		}
	}
	for target := range f.conf.Targets {
		os.Remove(newSSHClient(target).ctl)
	}
}

func alive(pid int) bool {
	p, err := os.FindProcess(pid)
	return err == nil && p.Signal(syscall.Signal(0)) == nil
}

func waitMachine(t *testing.T, mm *machineManager, name string, ok func(machineView) bool) machineView {
	t.Helper()
	var last machineView
	for end := time.Now().Add(60 * time.Second); time.Now().Before(end); time.Sleep(100 * time.Millisecond) {
		for _, v := range mm.views() {
			if v.Name == name {
				if last = v; ok(v) {
					return v
				}
			}
		}
	}
	t.Fatalf("timed out; %s is %+v", name, last)
	return last
}

func connected(v machineView) bool { return v.State == stateConnected }

func useCacheDir(t *testing.T) {
	dir := t.TempDir()
	old := cacheDir
	cacheDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { cacheDir = old })
}

// proxiedPing checks that the local server reaches the remote kijk through /m/<machine>/api/.
func proxiedPing(t *testing.T, mm *machineManager, target string) {
	t.Helper()
	srv := newServer("secret", 7717, nil, newHub(), &roots{}, nil, mm)
	r := httptest.NewRequest("GET", "/m/"+target+"/api/ping", nil)
	r.Host = "127.0.0.1:7717"
	r.AddCookie(&http.Cookie{Name: cookieName(7717), Value: "secret"})
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	if w.Code != http.StatusOK || w.Body.String() != "kijk" {
		t.Fatalf("proxied ping: %d %q", w.Code, w.Body.String())
	}
}

func remoteVersions(t *testing.T, home string) string {
	t.Helper()
	entries, _ := os.ReadDir(filepath.Join(home, ".local", "share", "kijk", "remote"))
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return strings.Join(names, " ")
}

func TestRemoteMachine(t *testing.T) {
	f := newFakeMachines(t)
	vm := f.add("vm", "vm", "")
	f.release("0.0.1")

	// First add: no kijk on the machine, so the local kijk installs it.
	a := f.manager("0.0.1", vm)
	v := waitMachine(t, a, vm, connected)
	if want := map[string]string{"darwin": "Darwin ", "linux": "Linux "}[runtime.GOOS]; !strings.HasPrefix(v.System, want) {
		t.Fatalf("system %q", v.System)
	}
	if got := remoteVersions(t, f.home(vm)); got != "0.0.1" {
		t.Fatalf("installed versions: %q", got)
	}
	first := f.serveState(vm, "0.0.1")
	if first.Version != "0.0.1" || first.PID == 0 {
		t.Fatalf("serve-0.0.1.json %+v", first)
	}
	if st, err := os.Stat(filepath.Join(remoteConfig(f.home(vm)), "serve-0.0.1.json")); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("serve-0.0.1.json: %v %v", st.Mode(), err)
	}
	proxiedPing(t, a, vm)

	// A second local kijk of the same version uses the same remote kijk.
	b := f.manager("0.0.1", vm)
	waitMachine(t, b, vm, connected)
	if st := f.serveState(vm, "0.0.1"); st.PID != first.PID {
		t.Fatalf("a second local kijk started a new remote kijk: %d, was %d", st.PID, first.PID)
	}

	// A dropped login reconnects to the same remote kijk.
	a.find(vm).ssh.logout()
	waitMachine(t, a, vm, func(v machineView) bool { return v.State != stateConnected })
	waitMachine(t, a, vm, connected)
	if st := f.serveState(vm, "0.0.1"); st.PID != first.PID {
		t.Fatalf("a reconnect restarted the remote kijk")
	}
	a.apply(nil)
	b.apply(nil)

	// A local kijk of another version gets its own remote kijk and leaves the first one running.
	f.release("0.0.2")
	c := f.manager("0.0.2", vm)
	waitMachine(t, c, vm, connected)
	second := f.serveState(vm, "0.0.2")
	if second.Version != "0.0.2" || second.PID == first.PID {
		t.Fatalf("after the version change: %+v", second)
	}
	if st := f.serveState(vm, "0.0.1"); st.PID != first.PID || !alive(first.PID) {
		t.Fatalf("the 0.0.2 kijk stopped the 0.0.1 remote kijk: %+v", st)
	}
	if got := remoteVersions(t, f.home(vm)); got != "0.0.1 0.0.2" {
		t.Fatalf("version folders: %q", got)
	}
	c.apply(nil)

	// Two local kijks bootstrap at once: one remote kijk starts.
	if p, err := os.FindProcess(second.PID); err == nil {
		p.Signal(syscall.SIGTERM)
	}
	for end := time.Now().Add(5 * time.Second); alive(second.PID) && time.Now().Before(end); {
		time.Sleep(50 * time.Millisecond)
	}
	d, e := make(chan *machineManager), make(chan *machineManager)
	go func() { d <- f.manager("0.0.2", vm) }()
	go func() { e <- f.manager("0.0.2", vm) }()
	dm, em := <-d, <-e
	waitMachine(t, dm, vm, connected)
	waitMachine(t, em, vm, connected)
	bin := filepath.Join(f.home(vm), ".local", "share", "kijk", "remote", "0.0.2", "kijk")
	out, _ := exec.Command("pgrep", "-f", bin+" serve").Output()
	if n := len(strings.Fields(string(out))); n != 1 {
		t.Fatalf("%d 0.0.2 remote kijks run", n)
	}

	// A key the machine does not accept asks for a login, without a prompt.
	wrong := f.add("vm-wrong-key", "vm", modeWrongKey)
	g := f.manager("0.0.2", wrong)
	v = waitMachine(t, g, wrong, func(v machineView) bool { return v.State != stateConnecting })
	if v.State != stateNeedsLogin || !strings.Contains(v.Message, "kijk machine login") {
		t.Fatalf("wrong key: %+v", v)
	}

	// Disabling drops the rows and closes the login.
	dm.apply([]savedMachine{{vm, false}})
	if v := dm.views()[0]; v.State != stateDisabled || len(v.Rows) != 0 {
		t.Fatalf("disabled: %+v", v)
	}
}

func TestRemoteMachineOffline(t *testing.T) {
	f := newFakeMachines(t)
	vm := f.add("vm", "vm", modeOffline)
	mm := f.manager("0.0.1", vm)
	v := waitMachine(t, mm, vm, func(v machineView) bool { return v.State != stateConnecting })
	if v.State != stateUnreachable || !strings.Contains(v.Message, "Connection refused") {
		t.Fatalf("offline: %+v", v)
	}
}

func TestRemoteMachineWithoutForwarding(t *testing.T) {
	f := newFakeMachines(t)
	vm := f.add("vm", "vm", modeNoForwarding)
	f.release("0.0.1")
	mm := f.manager("0.0.1", vm)
	v := waitMachine(t, mm, vm, func(v machineView) bool { return v.State != stateConnecting })
	if v.State != stateError || !strings.Contains(v.Message, "AllowTcpForwarding") || !strings.Contains(v.Message, "kijk is not running") {
		t.Fatalf("forwarding off: %+v", v)
	}
}

func TestRemoteInstallChecksumMismatch(t *testing.T) {
	f := newFakeMachines(t)
	vm := f.add("vm", "vm", "")
	asset := assetName(runtime.GOOS, runtime.GOARCH)
	startFakeRelease(t, "0.0.1", map[string][]byte{
		asset:           []byte("not kijk"),
		"checksums.txt": []byte(strings.Repeat("0", 64) + "  " + asset + "\n"),
	})
	mm := f.manager("0.0.1", vm)
	v := waitMachine(t, mm, vm, func(v machineView) bool { return v.State != stateConnecting })
	if v.State != stateInstallFailed || !strings.Contains(v.Message, "checksums.txt") {
		t.Fatalf("checksum mismatch: %+v", v)
	}
	if got := remoteVersions(t, f.home(vm)); got != "" {
		t.Fatalf("something was copied: %q", got)
	}
}

// Two installs of one version on one machine at once, such as from two local kijks, must not
// write into each other's file.
func TestRemoteInstallsAtOnce(t *testing.T) {
	f := newFakeMachines(t)
	slow, fast := f.add("slow", "vm", ""), f.add("fast", "vm", "")
	f.release("0.0.1")
	if _, err := cachedRelease("0.0.1", runtime.GOOS, runtime.GOARCH); err != nil {
		t.Fatal(err)
	}
	paused, resume := f.pause(slow)
	ctx := context.Background()

	first := make(chan error, 1)
	go func() { first <- installRemote(ctx, newSSHClient(slow), "0.0.1", runtime.GOOS, runtime.GOARCH) }()
	for !exists(paused) {
		select {
		case err := <-first:
			t.Fatalf("the first install did not pause: %v", err)
		case <-time.After(20 * time.Millisecond):
		}
	}
	if err := installRemote(ctx, newSSHClient(fast), "0.0.1", runtime.GOOS, runtime.GOARCH); err != nil {
		t.Fatalf("second install: %v", err)
	}
	os.WriteFile(resume, nil, 0o600)
	if err := <-first; err != nil {
		t.Fatalf("first install: %v", err)
	}

	d := filepath.Join(f.home(slow), ".local", "share", "kijk", "remote", "0.0.1")
	if b, _ := os.ReadFile(filepath.Join(d, "kijk")); !bytes.Equal(b, selfBytes()) {
		t.Error("the installed kijk is not the release")
	}
	if entries, _ := os.ReadDir(d); len(entries) != 1 {
		t.Errorf("left in the version folder: %v", entries)
	}
}

// kijk machine add refuses a second name for a login that is already saved, before it logs in.
func TestMachineAddRefusesADuplicateLogin(t *testing.T) {
	f := newFakeMachines(t)
	useConfigDir(t)
	login := func(name, user, jump string) string {
		target := f.add(name, user, "")
		f.with(target, func(t *fakeTarget) { t.User, t.HostName, t.Jump = user, "10.0.0.5", jump })
		return target
	}
	vm := login("vm", "abhinav", "")
	byIP := login("abhinav-at-ip", "abhinav", "")
	bob := login("bob-at-ip", "bob", "")
	jumped := login("behind-a-jump-host", "abhinav", "bastion")
	add := func(target string) (string, error) {
		var out bytes.Buffer
		err := machineAdd(target, strings.NewReader(""), &out)
		return out.String(), err
	}
	if _, err := add(vm); err != nil {
		t.Fatal(err)
	}
	out, err := add(byIP)
	if err == nil || !strings.Contains(err.Error(), "is the same login as "+vm) || !strings.Contains(err.Error(), "user abhinav, host 10.0.0.5, port 22") {
		t.Fatalf("duplicate: %v", err)
	}
	if strings.Contains(out, "Testing the login") {
		t.Errorf("logged in before refusing: %q", out)
	}
	for _, target := range []string{bob, jumped} {
		if _, err := add(target); err != nil {
			t.Errorf("%s: %v", target, err)
		}
	}
	var names []string
	list, _ := savedMachines()
	for _, m := range list {
		names = append(names, m.Target)
	}
	if want := []string{vm, bob, jumped}; !slices.Equal(names, want) {
		t.Fatalf("saved %v, want %v", names, want)
	}
}
