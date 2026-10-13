package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestServeName(t *testing.T) {
	for _, c := range []struct {
		arg, want string
		ok        bool
	}{
		{"", "dev", true}, // a test binary is not a release
		{"dev", "dev", true},
		{"0.9.0", "0.9.0", true},
		{"dev (abc1234)", "", false},
		{"../0.9.0", "", false},
		{"latest", "", false},
	} {
		got, err := serveName(c.arg)
		if got != c.want || (err == nil) != c.ok {
			t.Errorf("serveName(%q) = %q, %v", c.arg, got, err)
		}
	}
}

// fakeKijk stands in for kijk serve: it writes its state file and keeps running.
const fakeKijk = `#!/bin/sh
[ "$1" = serve ] || exit 1
case $(uname -s) in
Darwin) cfg="$HOME/Library/Application Support/kijk" ;;
*) cfg="$HOME/.config/kijk" ;;
esac
printf '{"version":"%s","port":4000,"token":"t","pid":%s}' "$2" $$ > "$cfg/serve-$2.json"
while :; do sleep 1; done
`

// installFake puts a fake kijk of version v where the bootstrap looks for it, and returns its folder.
func installFake(t *testing.T, home, v string) string {
	t.Helper()
	d := filepath.Join(home, ".local", "share", "kijk", "remote", v)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "kijk"), []byte(fakeKijk), 0o755); err != nil {
		t.Fatal(err)
	}
	return d
}

// bootLocally runs the bootstrap script for version v as a remote machine would, with home as $HOME.
func bootLocally(t *testing.T, home, v string) bootResult {
	t.Helper()
	cmd := exec.Command("sh", "-s")
	cmd.Stdin = strings.NewReader(fmt.Sprintf(bootstrapScript, v))
	cmd.Env = append(os.Environ(), "HOME="+home, "XDG_CONFIG_HOME=")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("bootstrap %s: %v", v, err)
	}
	b, err := parseBoot(string(out))
	if err != nil || b.need != "" {
		t.Fatalf("bootstrap %s: %+v %v\n%s", v, b, err, out)
	}
	t.Cleanup(func() {
		if p, err := os.FindProcess(b.serve.PID); err == nil {
			p.Kill()
		}
	})
	return b
}

func skipWithoutSh(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the bootstrap script runs on Linux and macOS")
	}
}

func TestBootstrapKeepsOtherVersions(t *testing.T) {
	skipWithoutSh(t)
	home := t.TempDir()
	old := installFake(t, home, "0.8.0")
	installFake(t, home, "0.9.0")

	a := bootLocally(t, home, "0.8.0")
	b := bootLocally(t, home, "0.9.0")
	if a.serve.PID == b.serve.PID {
		t.Fatalf("both versions got remote kijk %d", a.serve.PID)
	}
	if again := bootLocally(t, home, "0.8.0"); again.serve.PID != a.serve.PID {
		t.Fatalf("0.9.0 stopped the 0.8.0 remote kijk: %d, was %d", again.serve.PID, a.serve.PID)
	}
	if _, err := os.Stat(old); err != nil {
		t.Fatalf("the folder of a running version was deleted: %v", err)
	}
}

func TestBootstrapDeletesOnlyUnusedOldVersions(t *testing.T) {
	skipWithoutSh(t)
	home := t.TempDir()
	unused := installFake(t, home, "0.7.0")
	hour := time.Now().Add(-time.Hour)
	for _, p := range []string{filepath.Join(unused, "kijk"), unused} {
		if err := os.Chtimes(p, hour, hour); err != nil {
			t.Fatal(err)
		}
	}
	justCopied := installFake(t, home, "0.6.0")
	installFake(t, home, "0.9.0")

	bootLocally(t, home, "0.9.0")
	if _, err := os.Stat(unused); !os.IsNotExist(err) {
		t.Fatalf("an unused version older than 10 minutes was kept: %v", err)
	}
	if _, err := os.Stat(justCopied); err != nil {
		t.Fatalf("a version copied a moment ago was deleted: %v", err)
	}
}

// sshd ends a copy cut short by closing stdin, so cat exits 0 with part of the file.
func TestInstallRefusesACutOffCopy(t *testing.T) {
	skipWithoutSh(t)
	bin := []byte("#!/bin/sh\necho kijk\n")
	for _, tc := range []struct {
		name string
		sent []byte
		ok   bool
	}{{"whole", bin, true}, {"cut off", bin[:7], false}} {
		home := t.TempDir()
		cmd := exec.Command("sh", "-c", installScript("0.9.0", int64(len(bin))))
		cmd.Env = append(os.Environ(), "HOME="+home)
		cmd.Stdin = strings.NewReader(string(tc.sent))
		err := cmd.Run()
		got, _ := os.ReadFile(filepath.Join(home, ".local", "share", "kijk", "remote", "0.9.0", "kijk"))
		if tc.ok && (err != nil || string(got) != string(bin)) {
			t.Errorf("%s: %v, installed %q", tc.name, err, got)
		}
		if !tc.ok && (err == nil || got != nil) {
			t.Errorf("%s: %v, installed %q", tc.name, err, got)
		}
	}
}
