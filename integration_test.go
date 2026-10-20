package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// These tests build real kijk binaries and publish them as a fake release, so they are slow and
// skipped under -short.

func buildKijk(t *testing.T, version string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), exeName())
	cmd := exec.Command("go", "build", "-ldflags", "-X main.version="+version, "-o", out, ".")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", version, err, b)
	}
	return out
}

func publish(t *testing.T, version, bin string) {
	t.Helper()
	b, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	startFakeRelease(t, version, releaseOf(b))
}

func runCmd(t *testing.T, env []string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), env...)
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, b)
	}
	return string(b)
}

func TestInstallScript(t *testing.T) {
	if testing.Short() {
		t.Skip("builds kijk")
	}
	publish(t, "0.0.2", buildKijk(t, "0.0.2"))
	dir := t.TempDir()

	if runtime.GOOS == "windows" {
		if os.Getenv("CI") == "" {
			t.Skip("install.ps1 changes the user PATH, so it only runs in CI")
		}
		out := runCmd(t, []string{"KIJK_INSTALL_DIR=" + dir}, "powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", "install.ps1")
		if v := binaryVersion(filepath.Join(dir, "kijk.exe")); v != "0.0.2" || !strings.Contains(out, "kijk 0.0.2 is installed") {
			t.Fatalf("version %q, output:\n%s", v, out)
		}
		return
	}

	out := runCmd(t, []string{"KIJK_INSTALL_DIR=" + dir}, "sh", "install.sh")
	if v := binaryVersion(filepath.Join(dir, "kijk")); v != "0.0.2" || !strings.Contains(out, "kijk 0.0.2 is installed in "+dir) {
		t.Fatalf("version %q, output:\n%s", v, out)
	}

	// No folder on PATH: install to ~/.local/bin, print the fish line, and write nothing else.
	home := t.TempDir()
	out = runCmd(t, []string{"HOME=" + home, "SHELL=/usr/bin/fish", "PATH=/usr/bin:/bin:/usr/sbin:/sbin"}, "sh", "install.sh")
	bin := filepath.Join(home, ".local", "bin")
	if !strings.Contains(out, "fish_add_path "+bin) {
		t.Fatalf("output:\n%s", out)
	}
	var files []string
	filepath.WalkDir(home, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			files = append(files, p)
		}
		return nil
	})
	if len(files) != 1 || files[0] != filepath.Join(bin, "kijk") {
		t.Fatalf("files written: %v", files)
	}
}

// kijkProcess is a real kijk started from a binary, with its own home folder.
type kijkProcess struct {
	cmd    *exec.Cmd
	env    []string
	port   int
	token  string
	log    string // what kijk wrote to stdout and stderr
	exited chan struct{}
}

func startKijk(t *testing.T, exe string) *kijkProcess {
	t.Helper()
	return startKijkIn(t, exe, t.TempDir())
}

// startKijkIn starts kijk with home as its home folder. env comes last, so it replaces a default.
func startKijkIn(t *testing.T, exe, home string, env ...string) *kijkProcess {
	t.Helper()
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	k := &kijkProcess{port: port, log: filepath.Join(home, "kijk.log"), exited: make(chan struct{}), env: append([]string{
		"HOME=" + home, "XDG_CONFIG_HOME=" + filepath.Join(home, ".config"), "APPDATA=" + home,
		"CLAUDE_CONFIG_DIR=" + t.TempDir(), "CODEX_HOME=" + filepath.Join(home, "codex"), "KIJK_NO_UPDATE_CHECK=1", "KIJK_NO_TELEMETRY=1",
	}, env...)}
	out, err := os.Create(k.log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { out.Close() })
	k.cmd = exec.Command(exe, "-port", fmt.Sprint(port), "-no-open")
	k.cmd.Env = append(os.Environ(), k.env...)
	k.cmd.Stdout, k.cmd.Stderr = out, out
	if err := k.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { k.cmd.Wait(); close(k.exited) }()
	t.Cleanup(func() { k.cmd.Process.Kill(); <-k.exited })

	state := fmt.Sprintf("running-%d.json", port)
	for deadline := time.Now().Add(10 * time.Second); k.token == "" && time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		filepath.WalkDir(home, func(p string, d fs.DirEntry, err error) error {
			var s instance
			if b, err := os.ReadFile(p); err == nil && d.Name() == state && json.Unmarshal(b, &s) == nil {
				k.token = s.Token
			}
			return nil
		})
	}
	if k.token == "" {
		t.Fatal("kijk did not start")
	}
	return k
}

func (k *kijkProcess) request(method, path string) (*http.Response, error) {
	req, _ := http.NewRequest(method, fmt.Sprintf("http://127.0.0.1:%d%s", k.port, path), nil)
	req.AddCookie(&http.Cookie{Name: cookieName(k.port), Value: k.token})
	return (&http.Client{Timeout: 3 * time.Second}).Do(req)
}

// waitState reads the status bar state from /api/events until ok accepts it.
func (k *kijkProcess) waitState(t *testing.T, ok func(kijkState) bool) kijkState {
	t.Helper()
	var last kijkState
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(250 * time.Millisecond) {
		resp, err := k.request("GET", "/api/events")
		if err != nil {
			continue
		}
		line, _ := bufio.NewReader(resp.Body).ReadString('\n')
		resp.Body.Close()
		var msg struct {
			Kijk kijkState `json:"kijk"`
		}
		if json.Unmarshal([]byte(strings.TrimPrefix(strings.TrimSpace(line), "data: ")), &msg) == nil {
			if last = msg.Kijk; ok(last) {
				return last
			}
		}
	}
	t.Fatalf("timed out; last state %+v", last)
	return last
}

func TestUpdateAndRestartARunningKijk(t *testing.T) {
	if testing.Short() {
		t.Skip("builds kijk")
	}
	publish(t, "0.0.2", buildKijk(t, "0.0.2"))
	exe := filepath.Join(t.TempDir(), exeName())
	b, _ := os.ReadFile(buildKijk(t, "0.0.1"))
	os.WriteFile(exe, b, 0o755)
	k := startKijk(t, exe)
	k.waitState(t, func(s kijkState) bool { return s.Version == "0.0.1" })

	out := runCmd(t, k.env, exe, "update")
	if !strings.Contains(out, "kijk 0.0.1 → 0.0.2") || !strings.Contains(out, "Restart kijk to use it.") {
		t.Fatalf("kijk update said:\n%s", out)
	}
	k.waitState(t, func(s kijkState) bool { return s.Installed == "0.0.2" })

	if !canRestart {
		return
	}
	resp, err := k.request("POST", "/api/restart")
	if err != nil || resp.StatusCode != http.StatusAccepted {
		t.Fatalf("restart: %v %v", resp, err)
	}
	// The same cookie still works, so the token survived; the same process answers, on 0.0.2.
	k.waitState(t, func(s kijkState) bool { return s.Version == "0.0.2" && s.Installed == "" })
	select {
	case <-k.exited:
		t.Fatal("the kijk process exited instead of replacing itself")
	default:
	}
}
