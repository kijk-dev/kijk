package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// These tests run the real ssh and bootstrap against a throwaway Linux sshd in docker. They are
// skipped under -short and where docker is missing.

type sshd struct {
	t         *testing.T
	container string
	target    string // a Host in config
	config    string // the ssh config file
	goarch    string
}

func startSSHD(t *testing.T, sshdArgs ...string) *sshd {
	t.Helper()
	if testing.Short() || runtime.GOOS == "windows" {
		t.Skip("needs docker and ssh")
	}
	if _, err := exec.LookPath("docker"); err != nil || exec.Command("docker", "info").Run() != nil {
		t.Skip("docker is not running")
	}
	runCmd(t, nil, "docker", "build", "-q", "-t", "kijk-test-sshd", "testdata/sshd")
	b := make([]byte, 4)
	rand.Read(b)
	s := &sshd{t: t, container: "kijk-test-sshd-" + hex.EncodeToString(b), target: "kijk-test-" + hex.EncodeToString(b)}
	// Alpine turns forwarding off by default. sshd takes the first value of an option, so the
	// test's own options win.
	args := append([]string{"run", "-d", "--name", s.container, "-p", "127.0.0.1:0:22", "kijk-test-sshd", "/usr/sbin/sshd", "-D", "-e"}, sshdArgs...)
	args = append(args, "-o", "AllowTcpForwarding=yes")
	runCmd(t, nil, "docker", args...)
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", s.container).Run() })

	dir := t.TempDir()
	key := filepath.Join(dir, "id")
	runCmd(t, nil, "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", key)
	runCmd(t, nil, "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", key+"-other")
	runCmd(t, nil, "docker", "cp", key+".pub", s.container+":/home/kijk/.ssh/authorized_keys")
	s.exec("root", "chown kijk:kijk /home/kijk/.ssh/authorized_keys && chmod 600 /home/kijk/.ssh/authorized_keys")
	port := strings.TrimSpace(runCmd(t, nil, "docker", "port", s.container, "22/tcp"))
	port = port[strings.LastIndex(port, ":")+1 : strings.Index(port+"\n", "\n")]
	s.config = filepath.Join(dir, "config")
	host := func(name, id string) string {
		return fmt.Sprintf("Host %s\n  HostName 127.0.0.1\n  Port %s\n  User kijk\n  IdentityFile %s\n  IdentitiesOnly yes\n  UserKnownHostsFile %s/known_hosts\n  StrictHostKeyChecking accept-new\n", name, port, id, dir)
	}
	os.WriteFile(s.config, []byte(host(s.target, key)+host(s.target+"-wrong-key", key+"-other")), 0o600)
	s.goarch, _ = goPlatformArch(s.exec("kijk", "uname -m"))
	for i := 0; ; i++ { // sshd needs a moment after the container starts
		if exec.Command("ssh", "-F", s.config, "-o", "BatchMode=yes", s.target, "true").Run() == nil {
			break
		}
		if i == 50 {
			t.Fatal("sshd did not come up")
		}
		time.Sleep(100 * time.Millisecond)
	}
	return s
}

func goPlatformArch(cpu string) (string, bool) {
	_, arch := goPlatform("Linux", strings.TrimSpace(cpu))
	return arch, arch != ""
}

func (s *sshd) exec(user, script string) string {
	s.t.Helper()
	out, _ := exec.Command("docker", "exec", "-u", user, s.container, "sh", "-c", script).CombinedOutput()
	return strings.TrimSpace(string(out))
}

func (s *sshd) serveState(version string) serveState {
	var st serveState
	json.Unmarshal([]byte(s.exec("kijk", "cat /home/kijk/.config/kijk/serve-"+version+".json")), &st)
	return st
}

// release publishes linux builds of kijk as a fake GitHub release.
func (s *sshd) release(version string) {
	s.t.Helper()
	bin := filepath.Join(s.t.TempDir(), "kijk")
	cmd := exec.Command("go", "build", "-ldflags", "-X main.version="+version, "-o", bin, ".")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+s.goarch)
	if b, err := cmd.CombinedOutput(); err != nil {
		s.t.Fatalf("build: %v\n%s", err, b)
	}
	b, _ := os.ReadFile(bin)
	startFakeRelease(s.t, version, map[string][]byte{assetName("linux", s.goarch): b})
}

func (s *sshd) manager(version string, targets ...string) *machineManager {
	mm := newMachineManager(version)
	mm.sshExtra = []string{"-F", s.config}
	var saved []savedMachine
	for _, t := range targets {
		saved = append(saved, savedMachine{t, true})
	}
	mm.apply(saved)
	s.t.Cleanup(func() { mm.apply(nil) })
	return mm
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

func TestRemoteMachine(t *testing.T) {
	s := startSSHD(t)
	useCacheDir(t)
	s.release("0.0.1")

	// First add: no kijk on the machine, so the local kijk installs it.
	a := s.manager("0.0.1", s.target)
	v := waitMachine(t, a, s.target, connected)
	if !strings.HasPrefix(v.System, "Linux ") {
		t.Fatalf("system %q", v.System)
	}
	if got := s.exec("kijk", "ls /home/kijk/.local/share/kijk/remote"); got != "0.0.1" {
		t.Fatalf("installed versions: %q", got)
	}
	first := s.serveState("0.0.1")
	if first.Version != "0.0.1" || first.PID == 0 {
		t.Fatalf("serve-0.0.1.json %+v", first)
	}
	if mode := s.exec("kijk", "stat -c %a /home/kijk/.config/kijk/serve-0.0.1.json"); mode != "600" {
		t.Fatalf("serve-0.0.1.json mode %s", mode)
	}

	// The proxy reaches the remote kijk.
	srv := newServer("secret", 7717, nil, newHub(), &roots{}, nil, a)
	r := httptest.NewRequest("GET", "/m/"+s.target+"/api/ping", nil)
	r.Host = "127.0.0.1:7717"
	r.AddCookie(&http.Cookie{Name: cookieName(7717), Value: "secret"})
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	if w.Code != http.StatusOK || w.Body.String() != "kijk" {
		t.Fatalf("proxied ping: %d %q", w.Code, w.Body.String())
	}

	// A second local kijk of the same version uses the same remote kijk.
	b := s.manager("0.0.1", s.target)
	waitMachine(t, b, s.target, connected)
	if st := s.serveState("0.0.1"); st.PID != first.PID {
		t.Fatalf("a second local kijk started a new remote kijk: %d, was %d", st.PID, first.PID)
	}

	// A dropped login reconnects to the same remote kijk.
	a.find(s.target).ssh.logout()
	waitMachine(t, a, s.target, func(v machineView) bool { return v.State != stateConnected })
	waitMachine(t, a, s.target, connected)
	if st := s.serveState("0.0.1"); st.PID != first.PID {
		t.Fatalf("a reconnect restarted the remote kijk")
	}
	a.apply(nil)
	b.apply(nil)

	// A local kijk of another version gets its own remote kijk and leaves the first one running.
	s.release("0.0.2")
	c := s.manager("0.0.2", s.target)
	waitMachine(t, c, s.target, connected)
	if st := s.serveState("0.0.2"); st.Version != "0.0.2" || st.PID == first.PID {
		t.Fatalf("after the version change: %+v", st)
	}
	if st := s.serveState("0.0.1"); st.PID != first.PID || s.exec("kijk", fmt.Sprintf("kill -0 %d && echo up", first.PID)) != "up" {
		t.Fatalf("the 0.0.2 kijk stopped the 0.0.1 remote kijk: %+v", st)
	}
	if got := s.exec("kijk", "ls /home/kijk/.local/share/kijk/remote | tr '\\n' ' '"); got != "0.0.1 0.0.2" {
		t.Fatalf("version folders: %q", got)
	}
	c.apply(nil)

	// Two local kijks bootstrap at once: one remote kijk starts.
	s.exec("kijk", `kill $(sed -n 's/.*"pid":\([0-9]*\).*/\1/p' /home/kijk/.config/kijk/serve-0.0.2.json); sleep 0.5`)
	var wg sync.WaitGroup
	var d, e *machineManager
	wg.Go(func() { d = s.manager("0.0.2", s.target) })
	wg.Go(func() { e = s.manager("0.0.2", s.target) })
	wg.Wait()
	waitMachine(t, d, s.target, connected)
	waitMachine(t, e, s.target, connected)
	if n := s.exec("kijk", "pgrep -f '[k]ijk serve 0.0.2' | wc -l"); n != "1" {
		t.Fatalf("%s 0.0.2 remote kijks run", n)
	}

	// A key the machine does not accept asks for a login, without a prompt.
	f := s.manager("0.0.2", s.target+"-wrong-key")
	v = waitMachine(t, f, s.target+"-wrong-key", func(v machineView) bool { return v.State != stateConnecting })
	if v.State != stateNeedsLogin || !strings.Contains(v.Message, "kijk machine login") {
		t.Fatalf("wrong key: %+v", v)
	}

	// Disabling drops the rows and closes the login.
	d.apply([]savedMachine{{s.target, false}})
	if v := d.views()[0]; v.State != stateDisabled || len(v.Rows) != 0 {
		t.Fatalf("disabled: %+v", v)
	}
}

func TestRemoteMachineWithoutForwarding(t *testing.T) {
	s := startSSHD(t, "-o", "AllowTcpForwarding=no")
	useCacheDir(t)
	s.release("0.0.1")
	mm := s.manager("0.0.1", s.target)
	v := waitMachine(t, mm, s.target, func(v machineView) bool { return v.State != stateConnecting })
	if v.State != stateError || !strings.Contains(v.Message, "AllowTcpForwarding") || !strings.Contains(v.Message, "kijk is not running") {
		t.Fatalf("forwarding off: %+v", v)
	}
}

func TestRemoteInstallChecksumMismatch(t *testing.T) {
	s := startSSHD(t)
	useCacheDir(t)
	startFakeRelease(t, "0.0.1", map[string][]byte{
		assetName("linux", s.goarch): []byte("not kijk"),
		"checksums.txt":              []byte(strings.Repeat("0", 64) + "  " + assetName("linux", s.goarch) + "\n"),
	})
	mm := s.manager("0.0.1", s.target)
	v := waitMachine(t, mm, s.target, func(v machineView) bool { return v.State != stateConnecting })
	if v.State != stateInstallFailed || !strings.Contains(v.Message, "checksums.txt") {
		t.Fatalf("checksum mismatch: %+v", v)
	}
	if got := s.exec("kijk", "ls /home/kijk/.local/share/kijk/remote 2>&1"); !strings.Contains(got, "No such file") {
		t.Fatalf("something was copied: %q", got)
	}
}
