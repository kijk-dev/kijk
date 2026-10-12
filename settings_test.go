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
	if p != filepath.Join(dir, "kijk", "settings.json") {
		t.Fatalf("path %s", p)
	}
	raw, err := readSettings(p)
	if err != nil || len(raw) != 0 {
		t.Fatalf("missing file: %v %v", raw, err)
	}
	os.MkdirAll(filepath.Dir(p), 0o700)
	os.WriteFile(p, []byte(`{"theme":"nord","future":{"a":[1,2]}}`), 0o600)
	err = updateSettings(p, func(raw map[string]json.RawMessage) error {
		setMachines(raw, []savedMachine{{"azvm", true}})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = readSettings(p)
	if string(raw["theme"]) != `"nord"` || !strings.Contains(string(raw["future"]), `"a"`) {
		t.Fatalf("unknown keys lost: %v", raw)
	}
	if got := machinesOf(raw); !slices.Equal(got, []savedMachine{{"azvm", true}}) {
		t.Fatalf("machines = %v", got)
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
	if err := changeMachines(func(l []savedMachine) ([]savedMachine, error) { return append(l, savedMachine{"azvm", true}), nil }); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p + ".bak"); string(b) != `{"theme":` {
		t.Fatalf("bak = %q", b)
	}
	if list, err := savedMachines(); err != nil || len(list) != 1 {
		t.Fatalf("after save: %v %v", list, err)
	}
}

func TestMachinesOf(t *testing.T) {
	raw := map[string]json.RawMessage{"machines": json.RawMessage(
		`[{"target":"b"},{"target":"a","enabled":false},{"target":"b","enabled":false},{"target":""},{"target":"c","enabled":true}]`)}
	want := []savedMachine{{"b", true}, {"a", false}, {"c", true}}
	if got := machinesOf(raw); !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if got := machinesOf(map[string]json.RawMessage{"machines": json.RawMessage(`"nope"`)}); got != nil {
		t.Fatalf("bad key: %v", got)
	}
	setMachines(raw, nil)
	if _, ok := raw["machines"]; ok {
		t.Fatal("an empty list should remove the key")
	}
}

func TestSettingsFileReloadsOnChange(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	s := settingsFile{path: p}
	if raw, changed := s.load(); !changed || len(raw) != 0 {
		t.Fatalf("first load: %v %v", raw, changed)
	}
	if _, changed := s.load(); changed {
		t.Fatal("no change, but changed")
	}
	os.WriteFile(p, []byte(`{"machines":[{"target":"azvm"}]}`), 0o600)
	raw, changed := s.load()
	if !changed || len(machinesOf(raw)) != 1 {
		t.Fatalf("after write: %v %v", raw, changed)
	}
	later := time.Now().Add(time.Minute)
	os.WriteFile(p, []byte(`{"machines":[{"target":"other"}]}`), 0o600)
	os.Chtimes(p, later, later)
	if raw, changed := s.load(); !changed || machinesOf(raw)[0].Target != "other" {
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
	useConfigDir(t)
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
	for _, target := range []string{"-oProxyCommand=evil", "a b", "a/b", "a;b"} {
		if code, out := run("add", target); code != 1 || !strings.Contains(out, "is not an SSH target") {
			t.Errorf("add %q: %d %q", target, code, out)
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
}

func TestAsk(t *testing.T) {
	for in, want := range map[string]bool{"\n": true, "y\n": true, "Yes\n": true, "n\n": false, "no\n": false, "": false} {
		if got := ask(strings.NewReader(in), &bytes.Buffer{}, "? "); got != want {
			t.Errorf("ask(%q) = %v", in, got)
		}
	}
}
