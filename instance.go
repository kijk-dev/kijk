package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

var configDir = os.UserConfigDir

type instance struct {
	Port  int    `json:"port"`
	Token string `json:"token"`
}

func statePath(port int) (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "kijk", fmt.Sprintf("running-%d.json", port)), nil
}

func uiURL(port int, token string) string {
	return fmt.Sprintf("http://127.0.0.1:%d/?token=%s", port, token)
}

// runningURL gives the UI address of a kijk that already serves port for this user, else "".
func runningURL(port int) string {
	s, ok := runningInstance(port)
	if !ok {
		return ""
	}
	return uiURL(port, s.Token)
}

// runningInstance reads the state file of port and checks that a kijk answers there.
// A state file left by a crash points at a dead or foreign server, so it does not count.
func runningInstance(port int) (instance, bool) {
	var s instance
	p, err := statePath(port)
	if err != nil {
		return s, false
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return s, false
	}
	if json.Unmarshal(b, &s) != nil || s.Port != port || s.Token == "" {
		return s, false
	}
	req, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/api/ping", port), nil)
	req.AddCookie(&http.Cookie{Name: cookieName(port), Value: s.Token})
	resp, err := (&http.Client{Timeout: time.Second}).Do(req)
	if err != nil {
		return s, false
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16))
	return s, resp.StatusCode == http.StatusOK && string(body) == "kijk"
}

// saveInstance writes the state file, readable only by the user, and returns a func that removes it.
func saveInstance(port int, token string) (func(), error) {
	p, err := statePath(port)
	if err != nil {
		return func() {}, err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return func() {}, err
	}
	b, _ := json.Marshal(instance{port, token})
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return func() {}, err
	}
	if err := os.Rename(tmp, p); err != nil {
		return func() {}, err
	}
	return func() { os.Remove(p) }, nil
}

// runningPorts lists the ports of every kijk this user has running.
func runningPorts() []int {
	p, err := statePath(0)
	if err != nil {
		return nil
	}
	files, _ := filepath.Glob(filepath.Join(filepath.Dir(p), "running-*.json"))
	var ports []int
	for _, f := range files {
		var port int
		if _, err := fmt.Sscanf(filepath.Base(f), "running-%d.json", &port); err == nil && runningURL(port) != "" {
			ports = append(ports, port)
		}
	}
	return ports
}
