package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// TestMain lets a copy of the test binary stand in for a kijk release: with KIJK_TEST_VERSION
// set, "<binary> version" answers like kijk would.
func TestMain(m *testing.M) {
	if v := os.Getenv("KIJK_TEST_VERSION"); v != "" && len(os.Args) > 1 && os.Args[1] == "version" {
		os.Stdout.WriteString("kijk " + v + "\n")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

var selfBytes = sync.OnceValue(func() []byte {
	p, _ := os.Executable()
	b, _ := os.ReadFile(p)
	return b
})

func exeName() string {
	if runtime.GOOS == "windows" {
		return "kijk.exe"
	}
	return "kijk"
}

// oldKijk writes a runnable stand-in for the installed kijk into a new folder.
func oldKijk(t *testing.T) string {
	exe := filepath.Join(t.TempDir(), exeName())
	if err := os.WriteFile(exe, selfBytes(), 0o755); err != nil {
		t.Fatal(err)
	}
	return exe
}

func releaseOf(b []byte) map[string][]byte {
	return map[string][]byte{assetName(runtime.GOOS, runtime.GOARCH): b}
}

// stillWorks checks that a failed update left the old kijk untouched and runnable, with no
// temp file next to it.
func stillWorks(t *testing.T, exe string) {
	t.Helper()
	if b, _ := os.ReadFile(exe); !bytes.Equal(b, selfBytes()) {
		t.Error("the old kijk was changed")
	}
	t.Setenv("KIJK_TEST_VERSION", "0.2.0")
	if v := binaryVersion(exe); v != "0.2.0" {
		t.Errorf("the old kijk does not run: version %q", v)
	}
	if entries, _ := os.ReadDir(filepath.Dir(exe)); len(entries) != 1 {
		t.Errorf("left files behind: %v", entries)
	}
}

func TestInstallNewer(t *testing.T) {
	startFakeRelease(t, "0.3.0", releaseOf(selfBytes()))
	t.Setenv("KIJK_TEST_VERSION", "0.3.0")
	exe := filepath.Join(t.TempDir(), exeName())
	os.WriteFile(exe, []byte("old kijk"), 0o755)

	v, err := installLatest("0.2.0", exe)
	if err != nil || v != "0.3.0" {
		t.Fatalf("install = %q, %v", v, err)
	}
	if b, _ := os.ReadFile(exe); !bytes.Equal(b, selfBytes()) {
		t.Fatal("the binary was not replaced")
	}
	if v := binaryVersion(exe); v != "0.3.0" {
		t.Fatalf("installed kijk says %q", v)
	}
}

func TestInstallUpToDate(t *testing.T) {
	f := startFakeRelease(t, "0.3.0", releaseOf(selfBytes()))
	exe := oldKijk(t)
	if v, err := installLatest("0.3.0", exe); err != nil || v != "" {
		t.Fatalf("install = %q, %v", v, err)
	}
	if f.lookups.Load() != 1 {
		t.Fatalf("lookups = %d", f.lookups.Load())
	}
	stillWorks(t, exe)
}

func TestInstallRefusesDevBuild(t *testing.T) {
	f := startFakeRelease(t, "0.3.0", releaseOf(selfBytes()))
	exe := oldKijk(t)
	if _, err := installLatest("dev (7715b0f)", exe); err != errDev {
		t.Fatalf("err = %v", err)
	}
	if f.lookups.Load() != 0 {
		t.Fatal("a dev build asked GitHub")
	}
	stillWorks(t, exe)
}

func TestInstallFailuresKeepTheOldKijk(t *testing.T) {
	name := assetName(runtime.GOOS, runtime.GOARCH)
	cases := map[string]struct {
		files   map[string][]byte
		version string
		want    string
	}{
		"checksum mismatch": {map[string][]byte{name: selfBytes(), "checksums.txt": []byte(strings.Repeat("0", 64) + "  " + name + "\n")}, "0.3.0", "does not match"},
		"wrong version":     {releaseOf(selfBytes()), "0.2.9", "says it is"},
		"missing binary":    {map[string][]byte{"checksums.txt": {}}, "0.3.0", "no line"},
	}
	for label, c := range cases {
		t.Run(label, func(t *testing.T) {
			startFakeRelease(t, "0.3.0", c.files)
			t.Setenv("KIJK_TEST_VERSION", c.version)
			exe := oldKijk(t)
			if _, err := installLatest("0.2.0", exe); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
			stillWorks(t, exe)
		})
	}
}

func TestInstallReadOnlyFolder(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("folder permissions do not stop this user")
	}
	startFakeRelease(t, "0.3.0", releaseOf(selfBytes()))
	exe := oldKijk(t)
	dir := filepath.Dir(exe)
	os.Chmod(dir, 0o555)
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	_, err := installLatest("0.2.0", exe)
	if err == nil || !strings.Contains(err.Error(), "no write access to "+dir) {
		t.Fatalf("err = %v", err)
	}
	stillWorks(t, exe)
}

func TestInstallKeepsTheSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic links need extra rights on Windows")
	}
	startFakeRelease(t, "0.3.0", releaseOf(selfBytes()))
	t.Setenv("KIJK_TEST_VERSION", "0.3.0")
	target := filepath.Join(t.TempDir(), "kijk")
	os.WriteFile(target, []byte("old kijk"), 0o755)
	link := filepath.Join(t.TempDir(), "kijk")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	resolved, _ := filepath.EvalSymlinks(link)
	if _, err := installLatest("0.2.0", resolved); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Lstat(link); st.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the link was replaced by a file")
	}
	if b, _ := os.ReadFile(link); !bytes.Equal(b, selfBytes()) {
		t.Fatal("the link's target was not updated")
	}
}

func TestUpdateCommandOnDevBuild(t *testing.T) {
	old := kijkVersion
	kijkVersion = "dev (7715b0f)"
	t.Cleanup(func() { kijkVersion = old })
	if err := runUpdate(&bytes.Buffer{}); err != errDev || !strings.Contains(err.Error(), "build from source") {
		t.Fatalf("err = %v", err)
	}
}
