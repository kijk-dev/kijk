package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"time"
)

// settingsMu keeps two saves in this process from racing; the rename keeps readers whole.
var settingsMu sync.Mutex

// userConfigDir holds the files the user edits, in ~/.config on macOS as on Linux. kijk's own
// files stay in configDir.
var userConfigDir = defaultUserConfigDir

func defaultUserConfigDir() (string, error) {
	if runtime.GOOS == "windows" {
		return os.UserConfigDir()
	}
	if d := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(d) {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config"), nil
}

func settingsPath() (string, error) {
	dir, err := userConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "kijk", "settings.json"), nil
}

func machinesPath() (string, error) {
	dir, err := userConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "kijk", "machines.json"), nil
}

// readSettings gives every key of the settings file. A missing file is an empty map.
func readSettings(path string) (map[string]json.RawMessage, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]json.RawMessage{}, nil
	}
	if err != nil {
		return nil, err
	}
	raw := map[string]json.RawMessage{}
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON: %w", path, err)
	}
	if raw == nil { // the file says null
		raw = map[string]json.RawMessage{}
	}
	return raw, nil
}

// updateSettings reads the file, lets change edit its keys, and writes it back atomically. Keys
// kijk does not know are kept. A file that cannot be parsed is first copied to settings.json.bak.
func updateSettings(path string, change func(raw map[string]json.RawMessage) error) error {
	settingsMu.Lock()
	defer settingsMu.Unlock()
	raw, err := readSettings(path)
	if err != nil {
		if old, rerr := os.ReadFile(path); rerr == nil {
			if werr := os.WriteFile(path+".bak", old, 0o600); werr != nil {
				return fmt.Errorf("%v, and the copy to %s.bak failed: %w", err, path, werr)
			}
		}
		raw = map[string]json.RawMessage{}
	}
	if err := change(raw); err != nil {
		return err
	}
	return writeSettings(path, raw)
}

// linkedFile gives the file a symlink at path points to, so a save keeps the link that a dotfile
// manager such as GNU stow made.
func linkedFile(path string) (string, error) {
	st, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return path, nil
	}
	if err != nil {
		return "", err
	}
	if st.Mode()&fs.ModeSymlink == 0 {
		return path, nil
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("%s is a link that points nowhere", path)
	}
	if st, err := os.Stat(real); err != nil || !st.Mode().IsRegular() {
		return "", fmt.Errorf("%s does not link to a file", path)
	}
	return real, nil
}

// writeSettings writes the keys atomically, readable only by the user.
func writeSettings(path string, raw map[string]json.RawMessage) error {
	path, err := linkedFile(path)
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".settings-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, werr := f.Write(append(b, '\n'))
	if serr := f.Sync(); werr == nil {
		werr = serr
	}
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return werr
	}
	return os.Rename(f.Name(), path)
}

// settingsFile re-reads the settings when the file's modification time changes.
type settingsFile struct {
	path string
	mod  time.Time
	size int64
	raw  map[string]json.RawMessage
	err  error
}

// load returns the current keys and whether they changed since the last call.
func (s *settingsFile) load() (map[string]json.RawMessage, bool) {
	st, err := os.Stat(s.path)
	var mod time.Time
	var size int64 = -1
	if err == nil {
		mod, size = st.ModTime(), st.Size()
	}
	if s.raw != nil && mod.Equal(s.mod) && size == s.size {
		return s.raw, false
	}
	s.mod, s.size = mod, size
	raw, err := readSettings(s.path)
	if err != nil {
		raw = map[string]json.RawMessage{}
	}
	s.raw, s.err = raw, err
	return raw, true
}

// savedMachine is one entry of the machines list in machines.json.
type savedMachine struct {
	Target  string `json:"target"`
	Enabled bool   `json:"enabled"`
}

// machinesOf reads the machines key. An entry without "enabled" is enabled.
func machinesOf(raw map[string]json.RawMessage) ([]savedMachine, error) {
	b, ok := raw["machines"]
	if !ok {
		return nil, nil
	}
	var list []struct {
		Target  string `json:"target"`
		Enabled *bool  `json:"enabled"`
	}
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, fmt.Errorf("its machines list is not valid: %w", err)
	}
	var out []savedMachine
	for _, m := range list {
		if m.Target == "" || slices.ContainsFunc(out, func(o savedMachine) bool { return o.Target == m.Target }) {
			continue
		}
		if err := checkTarget(m.Target); err != nil {
			return nil, fmt.Errorf("its machines list is not valid: %w", err)
		}
		out = append(out, savedMachine{m.Target, m.Enabled == nil || *m.Enabled})
	}
	return out, nil
}

func setMachines(raw map[string]json.RawMessage, list []savedMachine) {
	if len(list) == 0 {
		delete(raw, "machines")
		return
	}
	b, _ := json.Marshal(list)
	raw["machines"] = b
}

var errNotSaved = errors.New("not saved")

// readMachines reads machines.json. A broken file is an error that names it, never an empty list,
// so a typo cannot drop the saved machines.
func readMachines(path string) (map[string]json.RawMessage, []savedMachine, error) {
	raw, err := readSettings(path)
	if err != nil {
		return nil, nil, fmt.Errorf("%w. Fix or delete it", err)
	}
	list, err := machinesOf(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w. Fix or delete it", path, err)
	}
	return raw, list, nil
}

// changeMachines edits the saved machines list in machines.json.
func changeMachines(change func([]savedMachine) ([]savedMachine, error)) error {
	path, err := machinesPath()
	if err != nil {
		return err
	}
	settingsMu.Lock()
	defer settingsMu.Unlock()
	raw, list, err := readMachines(path)
	if err != nil {
		return err
	}
	if list, err = change(list); err != nil {
		return err
	}
	setMachines(raw, list)
	return writeSettings(path, raw)
}

func savedMachines() ([]savedMachine, error) {
	path, err := machinesPath()
	if err != nil {
		return nil, err
	}
	_, list, err := readMachines(path)
	return list, err
}
