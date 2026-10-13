package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// This smoke test runs the real ssh against a throwaway Linux sshd in docker, for what the fake
// ssh cannot show. It is skipped under -short and where docker is missing.

type sshd struct {
	t         *testing.T
	container string
	target    string // a Host in config
	config    string // the ssh config file
	goarch    string
}

func startSSHD(t *testing.T) *sshd {
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
	runCmd(t, nil, "docker", "run", "-d", "--name", s.container, "-p", "127.0.0.1:0:22", "kijk-test-sshd", "/usr/sbin/sshd", "-D", "-e", "-o", "AllowTcpForwarding=yes")
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
	// A user config that runs a command and asks for a terminal, which kijk's own calls must ignore.
	strict := host(s.target+"-strict", key) + "  RemoteCommand false\n  RequestTTY force\n"
	os.WriteFile(s.config, []byte(host(s.target, key)+host(s.target+"-wrong-key", key+"-other")+strict), 0o600)
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

func TestRemoteMachineOverRealSSH(t *testing.T) {
	s := startSSHD(t)
	useCacheDir(t)
	s.release("0.0.1")

	// The first connection installs kijk, so the binary goes through the strict config too.
	strict := s.manager("0.0.1", s.target+"-strict")
	v := waitMachine(t, strict, s.target+"-strict", func(v machineView) bool { return v.State != stateConnecting })
	if v.State != stateConnected || !strings.HasPrefix(v.System, "Linux ") {
		t.Fatalf("strict config: %+v", v)
	}
	proxiedPing(t, strict, s.target+"-strict")

	mm := s.manager("0.0.1", s.target)
	waitMachine(t, mm, s.target, connected)
	proxiedPing(t, mm, s.target)

	wrong := s.manager("0.0.1", s.target+"-wrong-key")
	v = waitMachine(t, wrong, s.target+"-wrong-key", func(v machineView) bool { return v.State != stateConnecting })
	if v.State != stateNeedsLogin || !strings.Contains(v.Message, "kijk machine login") {
		t.Fatalf("wrong key: %+v", v)
	}
}
