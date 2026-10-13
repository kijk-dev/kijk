package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestSettingsKeepUnknownKeys(t *testing.T) {
	dir := useConfigDir(t)
	p, _ := settingsPath()
	if p != filepath.Join(dir, "user", "kijk", "settings.json") {
		t.Fatalf("path %s", p)
	}
	raw, err := readSettings(p)
	if err != nil || len(raw) != 0 {
		t.Fatalf("missing file: %v %v", raw, err)
	}
	os.MkdirAll(filepath.Dir(p), 0o700)
	os.WriteFile(p, []byte(`{"theme":"nord","future":{"a":[1,2]}}`), 0o600)
	err = updateSettings(p, func(raw map[string]json.RawMessage) error {
		raw["theme"] = json.RawMessage(`"dawn"`)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = readSettings(p)
	if string(raw["theme"]) != `"dawn"` || !strings.Contains(string(raw["future"]), `"a"`) {
		t.Fatalf("unknown keys lost: %v", raw)
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
			t.Errorf("mode %v", st.Mode())
		}
	}
}

func TestSettingsBrokenFileIsKeptAsBak(t *testing.T) {
	useConfigDir(t)
	p, _ := settingsPath()
	os.MkdirAll(filepath.Dir(p), 0o700)
	os.WriteFile(p, []byte(`{"theme":`), 0o600)
	if _, err := readSettings(p); err == nil {
		t.Fatal("broken file: want an error")
	}
	if err := updateSettings(p, func(raw map[string]json.RawMessage) error { raw["theme"] = json.RawMessage(`"nord"`); return nil }); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p + ".bak"); string(b) != `{"theme":` {
		t.Fatalf("bak = %q", b)
	}
	if raw, err := readSettings(p); err != nil || string(raw["theme"]) != `"nord"` {
		t.Fatalf("after save: %v %v", raw, err)
	}
}

func TestUserConfigDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows keeps os.UserConfigDir")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	for xdg, want := range map[string]string{"": filepath.Join(home, ".config"), "relative": filepath.Join(home, ".config"), "/x/conf": "/x/conf"} {
		t.Setenv("XDG_CONFIG_HOME", xdg)
		if got, err := defaultUserConfigDir(); err != nil || got != want {
			t.Errorf("XDG_CONFIG_HOME=%q: %q %v, want %q", xdg, got, err, want)
		}
	}
}

// Machines live in machines.json, apart from settings.json, and keep its unknown keys.
func TestMachinesFile(t *testing.T) {
	dir := useConfigDir(t)
	p, _ := machinesPath()
	if p != filepath.Join(dir, "user", "kijk", "machines.json") {
		t.Fatalf("path %s", p)
	}
	os.MkdirAll(filepath.Dir(p), 0o700)
	os.WriteFile(p, []byte(`{"future":1}`), 0o600)
	if err := changeMachines(func(l []savedMachine) ([]savedMachine, error) { return append(l, savedMachine{"azvm", true}), nil }); err != nil {
		t.Fatal(err)
	}
	raw, _ := readSettings(p)
	if string(raw["future"]) != "1" {
		t.Fatalf("unknown key lost: %v", raw)
	}
	if list, err := savedMachines(); err != nil || !slices.Equal(list, []savedMachine{{"azvm", true}}) {
		t.Fatalf("saved: %v %v", list, err)
	}
	if s, _ := settingsPath(); exists(s) {
		t.Error("settings.json was written")
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
			t.Errorf("mode %v", st.Mode())
		}
	}
}

// A broken machines.json is refused as it is, never replaced by an empty list.
func TestBrokenMachinesFileIsRefused(t *testing.T) {
	useConfigDir(t)
	p, _ := machinesPath()
	os.MkdirAll(filepath.Dir(p), 0o700)
	for _, broken := range []string{`{"machines":`, `{"machines":[{"target":"a","enabled":"yes"}]}`, `{"machines":"nope"}`, `{"machines":[{"target":"-oProxyCommand=sh -c evil"}]}`} {
		os.WriteFile(p, []byte(broken), 0o600)
		if _, err := savedMachines(); err == nil || !strings.Contains(err.Error(), p) {
			t.Errorf("%s: read error %v", broken, err)
		}
		err := changeMachines(func(l []savedMachine) ([]savedMachine, error) { return append(l, savedMachine{"azvm", true}), nil })
		if err == nil || !strings.Contains(err.Error(), "Fix or delete it") {
			t.Errorf("%s: save error %v", broken, err)
		}
		if b, _ := os.ReadFile(p); string(b) != broken {
			t.Errorf("%s: file changed to %s", broken, b)
		}
		if exists(p + ".bak") {
			t.Errorf("%s: a .bak was written", broken)
		}
	}
}

func TestMachinesOf(t *testing.T) {
	raw := map[string]json.RawMessage{"machines": json.RawMessage(
		`[{"target":"b"},{"target":"a","enabled":false},{"target":"b","enabled":false},{"target":""},{"target":"c","enabled":true}]`)}
	want := []savedMachine{{"b", true}, {"a", false}, {"c", true}}
	if got, err := machinesOf(raw); err != nil || !slices.Equal(got, want) {
		t.Fatalf("got %v %v, want %v", got, err, want)
	}
	if got, err := machinesOf(map[string]json.RawMessage{"machines": json.RawMessage(`"nope"`)}); err == nil {
		t.Fatalf("bad key: %v", got)
	}
	setMachines(raw, nil)
	if _, ok := raw["machines"]; ok {
		t.Fatal("an empty list should remove the key")
	}
}

func TestSettingsFileReloadsOnChange(t *testing.T) {
	p := filepath.Join(t.TempDir(), "machines.json")
	s := settingsFile{path: p}
	if raw, changed := s.load(); !changed || len(raw) != 0 {
		t.Fatalf("first load: %v %v", raw, changed)
	}
	if _, changed := s.load(); changed {
		t.Fatal("no change, but changed")
	}
	os.WriteFile(p, []byte(`{"machines":[{"target":"azvm"}]}`), 0o600)
	raw, changed := s.load()
	if list, _ := machinesOf(raw); !changed || len(list) != 1 {
		t.Fatalf("after write: %v %v", raw, changed)
	}
	later := time.Now().Add(time.Minute)
	os.WriteFile(p, []byte(`{"machines":[{"target":"other"}]}`), 0o600)
	os.Chtimes(p, later, later)
	if raw, changed := s.load(); !changed || string(raw["machines"]) != `[{"target":"other"}]` {
		t.Fatalf("after edit: %v %v", raw, changed)
	}
}

func TestMachineCommands(t *testing.T) {
	if runtime.GOOS == "windows" {
		var errOut bytes.Buffer
		if code := runMachine([]string{"add", "azvm"}, nil, &bytes.Buffer{}, &errOut); code != 1 || !strings.Contains(errOut.String(), "not supported on Windows yet") {
			t.Fatalf("windows: %d %q", code, errOut.String())
		}
		if list, _ := savedMachines(); len(list) != 0 {
			t.Fatalf("windows saved %v", list)
		}
		return
	}
	dir := useConfigDir(t)
	run := func(args ...string) (int, string) {
		var out bytes.Buffer
		code := runMachine(args, strings.NewReader(""), &out, &out)
		return code, out.String()
	}
	for _, args := range [][]string{{}, {"nope"}, {"add"}, {"add", "a", "b"}, {"list", "x"}, {"remove"}} {
		if code, out := run(args...); code != 2 || !strings.Contains(out, "Usage:") {
			t.Errorf("%v: %d %q", args, code, out)
		}
	}
	for _, target := range []string{"-oProxyCommand=evil", "a b", "a/b", "a;b", "azvm\x1b[2J", "a\x7f", strings.Repeat("a", 256)} {
		if code, out := run("add", target); code != 1 || !strings.Contains(out, "is not an SSH target") {
			t.Errorf("add %q: %d %q", target, code, out)
		}
	}
	if code, out := run("add", "alice:secret@azvm"); code != 1 || !strings.Contains(out, "a password") || strings.Contains(out, "secret") {
		t.Errorf("add with a password: %d %q", code, out)
	}
	for _, target := range []string{"alice@fe80::1", "alice@azvm", strings.Repeat("a", 255)} {
		if err := checkTarget(target); err != nil {
			t.Errorf("%q: %v", target, err)
		}
	}
	if code, out := run("list"); code != 0 || !strings.Contains(out, "No machines are saved") {
		t.Fatalf("empty list: %d %q", code, out)
	}
	changeMachines(func([]savedMachine) ([]savedMachine, error) {
		return []savedMachine{{"azvm", true}, {"other", true}}, nil
	})
	if code, out := run("add", "azvm"); code != 1 || !strings.Contains(out, "already saved") {
		t.Fatalf("duplicate: %d %q", code, out)
	}
	if code, out := run("disable", "azvm"); code != 0 || !strings.Contains(out, "Disabled azvm") {
		t.Fatalf("disable: %d %q", code, out)
	}
	if code, out := run("list"); code != 0 || !strings.Contains(out, "disabled") || !strings.Contains(out, "other") {
		t.Fatalf("list: %d %q", code, out)
	}
	if code, out := run("enable", "nope"); code != 1 || !strings.Contains(out, "no machine is saved as nope") {
		t.Fatalf("enable unknown: %d %q", code, out)
	}
	if code, _ := run("remove", "azvm"); code != 0 {
		t.Fatal("remove failed")
	}
	if list, _ := savedMachines(); !slices.Equal(list, []savedMachine{{"other", true}}) {
		t.Fatalf("after remove: %v", list)
	}
	if code, out := run("login", "azvm"); code != 1 || !strings.Contains(out, "no machine is saved") {
		t.Fatalf("login unknown: %d %q", code, out)
	}
	os.WriteFile(filepath.Join(dir, "user", "kijk", "machines.json"), []byte(`{`), 0o600)
	if code, out := run("list"); code != 1 || !strings.Contains(out, "machines.json is not valid JSON") {
		t.Fatalf("list of a broken file: %d %q", code, out)
	}
}

func TestAsk(t *testing.T) {
	for in, want := range map[string]bool{"\n": true, "y\n": true, "Yes\n": true, "n\n": false, "no\n": false, "": false} {
		if got := ask(strings.NewReader(in), &bytes.Buffer{}, "? "); got != want {
			t.Errorf("ask(%q) = %v", in, got)
		}
	}
}

// A save writes through a symlink, as dotfile managers such as GNU stow make, and keeps the link.
func TestSaveThroughASymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need extra rights on Windows")
	}
	useConfigDir(t)
	p, _ := machinesPath()
	os.MkdirAll(filepath.Dir(p), 0o700)
	real := filepath.Join(t.TempDir(), "dotfiles", "machines.json")
	os.MkdirAll(filepath.Dir(real), 0o700)
	os.WriteFile(real, []byte(`{}`), 0o644)
	if err := os.Symlink(real, p); err != nil {
		t.Fatal(err)
	}
	add := func(target string) error {
		return changeMachines(func(l []savedMachine) ([]savedMachine, error) { return append(l, savedMachine{target, true}), nil })
	}
	if err := add("azvm"); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Lstat(p); err != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the link was replaced: %v %v", st.Mode(), err)
	}
	if b, _ := os.ReadFile(real); !strings.Contains(string(b), "azvm") {
		t.Fatalf("the real file did not change: %s", b)
	}
	if left, _ := os.ReadDir(filepath.Dir(p)); len(left) != 1 {
		t.Errorf("left next to the link: %v", left)
	}

	os.Remove(real)
	if err := add("other"); err == nil || !strings.Contains(err.Error(), "points nowhere") {
		t.Errorf("dangling link: %v", err)
	}
	if st, err := os.Lstat(p); err != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("a refused save replaced the link: %v", err)
	}

	// settings.json starts over when it is broken, so only the save itself can refuse a device.
	sp, _ := settingsPath()
	os.Symlink(os.DevNull, sp)
	err := updateSettings(sp, func(raw map[string]json.RawMessage) error { raw["theme"] = json.RawMessage(`"nord"`); return nil })
	if err == nil || !strings.Contains(err.Error(), "does not link to a file") {
		t.Errorf("link to %s: %v", os.DevNull, err)
	}
}
