package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// serveState is serve.json: how a local kijk reaches the headless kijk on this machine.
type serveState struct {
	Version string `json:"version"`
	Port    int    `json:"port"`
	Token   string `json:"token"`
	PID     int    `json:"pid"`
}

// serveIdle is how long kijk serve waits without any connection before it exits. A connected
// local kijk always holds /api/events open, so no connection means no local kijk.
var serveIdle = 10 * time.Minute

var errLocked = errors.New("locked")

// runServe is kijk serve: the server without a window, for a local kijk to reach over SSH.
func runServe() error {
	dir, err := configDir()
	if err != nil {
		return err
	}
	dir = filepath.Join(dir, "kijk")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// One kijk serve per user: when two local kijks start one at once, the second exits here and
	// their bootstrap waits for the first one's serve.json.
	unlock, err := lockFile(filepath.Join(dir, "serve.lock"))
	if errors.Is(err, errLocked) {
		fmt.Fprintln(os.Stderr, "kijk serve already runs for this user")
		return nil
	}
	if err != nil {
		return err
	}
	defer unlock()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	token := newToken()
	h := newHub()
	rts := &roots{}
	go watchSessions(newScanner(claudeDir()), h, rts, nil, nil, nil)
	srv := &http.Server{Handler: newServer(token, port, nil, h, rts, nil, nil)}

	state := filepath.Join(dir, "serve.json")
	b, _ := json.Marshal(serveState{kijkVersion, port, token, os.Getpid()})
	if err := writePrivate(state, b); err != nil {
		return err
	}
	remove := func() {
		// A newer kijk serve may have taken over the file after this one was told to stop.
		var s serveState
		if b, err := os.ReadFile(state); err == nil && json.Unmarshal(b, &s) == nil && s.PID == os.Getpid() {
			os.Remove(state)
		}
	}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM) // not SIGHUP: Notify would undo nohup
	go func() {
		<-stop
		remove()
		os.Exit(0)
	}()
	fmt.Printf("kijk %s serves on 127.0.0.1:%d\n", kijkVersion, port)
	err = serveUntilIdle(srv, ln, serveIdle)
	remove()
	return err
}

// serveUntilIdle serves until no connection has been open for idle.
func serveUntilIdle(srv *http.Server, ln net.Listener, idle time.Duration) error {
	var mu sync.Mutex
	open, since := 0, time.Now()
	srv.ConnState = func(_ net.Conn, s http.ConnState) {
		mu.Lock()
		defer mu.Unlock()
		switch s {
		case http.StateNew:
			open++
		case http.StateClosed, http.StateHijacked:
			if open--; open == 0 {
				since = time.Now()
			}
		}
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	tick := time.NewTicker(min(idle/4, 15*time.Second))
	defer tick.Stop()
	for {
		select {
		case err := <-errc:
			return err
		case <-tick.C:
			mu.Lock()
			done := open == 0 && time.Since(since) >= idle
			mu.Unlock()
			if done {
				srv.Close()
				return nil
			}
		}
	}
}

// writePrivate writes a file only the user can read, replacing it in one step.
func writePrivate(p string, b []byte) error {
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}
