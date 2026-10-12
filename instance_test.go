package main

import (
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"testing/fstest"
)

func useConfigDir(t *testing.T) string {
	dir := t.TempDir()
	old := configDir
	configDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { configDir = old })
	return dir
}

// serveKijk runs a real kijk server on a free loopback port.
func serveKijk(t *testing.T, token string) int {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	srv := &http.Server{Handler: newServer(token, port, fstest.MapFS{}, newHub(), &roots{}, newUpdater("0.2.0", "", io.Discard), nil)}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return port
}

func TestSecondStartFindsTheRunningOne(t *testing.T) {
	dir := useConfigDir(t)
	port := serveKijk(t, "secret")
	if got := runningURL(port); got != "" {
		t.Fatalf("found a viewer before any state file: %q", got)
	}
	remove, err := saveInstance(port, "secret")
	if err != nil {
		t.Fatal(err)
	}
	if got := runningURL(port); got != uiURL(port, "secret") {
		t.Fatalf("running = %q", got)
	}

	p, _ := statePath(port)
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
			t.Fatalf("state file mode = %v", st.Mode().Perm())
		}
		if st, _ := os.Stat(filepath.Join(dir, "kijk")); st.Mode().Perm() != 0o700 {
			t.Fatalf("state folder mode = %v", st.Mode().Perm())
		}
	}
	remove()
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("state file still there: %v", err)
	}
}

func TestLeftoverStateFileDoesNotBlock(t *testing.T) {
	useConfigDir(t)
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	dead := ln.Addr().(*net.TCPAddr).Port
	ln.Close() // the viewer crashed: nothing listens
	saveInstance(dead, "old")
	if got := runningURL(dead); got != "" {
		t.Fatalf("a dead viewer counted as running: %q", got)
	}

	port := serveKijk(t, "new") // something answers, but with another token
	saveInstance(port, "old")
	if got := runningURL(port); got != "" {
		t.Fatalf("a viewer with another token counted: %q", got)
	}
}
