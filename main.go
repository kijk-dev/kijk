package main

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"slices"
	"syscall"
	"time"
)

//go:embed web
var webFiles embed.FS

func main() {
	token, restarted := takeRestartEnv()

	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version":
			fmt.Println("kijk", kijkVersion)
			return
		case "update":
			if err := runUpdate(os.Stdout); err != nil {
				fmt.Fprintln(os.Stderr, "kijk update:", err)
				os.Exit(1)
			}
			return
		case "machine":
			os.Exit(runMachine(os.Args[2:], os.Stdin, os.Stdout, os.Stderr))
		case "serve":
			if runtime.GOOS == "windows" {
				fmt.Fprintln(os.Stderr, "kijk serve:", errWindows)
				os.Exit(1)
			}
			if len(os.Args) > 3 {
				fmt.Fprintln(os.Stderr, "usage: kijk serve [version]")
				os.Exit(2)
			}
			arg := ""
			if len(os.Args) == 3 {
				arg = os.Args[2]
			}
			name, err := serveName(arg)
			if err == nil {
				err = runServe(name)
			}
			if err != nil {
				fmt.Fprintln(os.Stderr, "kijk serve:", err)
				os.Exit(1)
			}
			return
		}
	}
	port := flag.Int("port", 7717, "port to listen on")
	noOpen := flag.Bool("no-open", false, "do not open the browser")
	showVersion := flag.Bool("version", false, "print the version and exit")
	noCheck := flag.Bool("no-update-check", false, "do not check GitHub for a new kijk version")
	noTelemetry := flag.Bool("no-telemetry", false, "do not send anonymous usage events")
	flag.Parse()
	if *showVersion {
		fmt.Println("kijk", kijkVersion)
		return
	}

	removeOldExe()
	if url := runningURL(*port); url != "" {
		fmt.Println("kijk is already running at", url)
		if !*noOpen {
			openUI(url)
		}
		return
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *port))
	if err != nil {
		log.Fatalf("cannot listen on port %d (another program may use it; try -port): %v", *port, err)
	}
	if token == "" {
		token = newToken()
	}
	remove, err := saveInstance(*port, token)
	if err != nil {
		log.Printf("cannot write the state file, so a second start will not find this one: %v", err)
	}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-stop
		remove()
		os.Exit(0)
	}()

	exe, _ := selfPath()
	u := newUpdater(kijkVersion, exe, os.Stdout)
	u.restart = func() error {
		remove()
		args := os.Args[1:]
		if !slices.Contains(args, "-no-open") {
			args = append(args, "-no-open")
		}
		err := restartInPlace(exe, args, token)
		remove, _ = saveInstance(*port, token) // reached only when the restart failed
		return err
	}
	if isRelease(kijkVersion) {
		go u.run(updateCheckOn(*noCheck))
	}
	var tel *telemetry
	if telemetryOn(*noTelemetry, kijkVersion, posthogKey) {
		tel = newTelemetry(posthogKey, kijkVersion)
		if !restarted {
			tel.send("kijk started", nil)
		}
	}
	h := newHub()
	rts := &roots{}
	var mm *machineManager
	if runtime.GOOS != "windows" {
		mm = newMachineManager(kijkVersion)
		go mm.run(context.Background())
	}
	go watchSessions(newScanner(claudeDir(), codexHome()), h, rts, u, tel, mm)

	web, _ := fs.Sub(webFiles, "web")
	url := uiURL(*port, token)
	if restarted {
		fmt.Printf("kijk %s restarted at %s\n", kijkVersion, url)
	} else {
		fmt.Printf("kijk %s is running at %s\n", kijkVersion, url)
	}
	if !*noOpen {
		openUI(url)
	}
	log.Fatal(http.Serve(ln, newServer(token, *port, web, h, rts, u, mm)))
}

// takeRestartEnv reads what a restart from the window hands over, and removes it before any git,
// gh or glab could inherit the token.
func takeRestartEnv() (token string, restarted bool) {
	token = os.Getenv("KIJK_TOKEN")
	restarted = os.Getenv("KIJK_RESTARTED") != ""
	os.Unsetenv("KIJK_TOKEN")
	os.Unsetenv("KIJK_RESTARTED")
	return token, restarted
}

// sessionMessage is one message on /api/events. err replaces the rows in the Agents list; codexErr
// goes to the status bar. machines is left out when none is saved, so the message stays as before.
func sessionMessage(rows []Row, err, codexErr error, k kijkState, machines []machineView) []byte {
	msg := struct {
		Rows       []Row         `json:"rows"`
		Error      string        `json:"error,omitempty"`
		CodexError string        `json:"codexError,omitempty"`
		Kijk       kijkState     `json:"kijk"`
		Machines   []machineView `json:"machines,omitempty"`
	}{Rows: rows, Kijk: k, Machines: machines}
	if err != nil {
		msg.Error = err.Error()
	}
	if codexErr != nil {
		msg.CodexError = codexErr.Error()
	}
	b, _ := json.Marshal(msg)
	return b
}

// An update or machine state change publishes at once, with the rows from the last poll.
// kijk serve has no updater and no machines.
func watchSessions(s *scanner, h *hub, rts *roots, u *updater, tel *telemetry, mm *machineManager) {
	var rows []Row
	var err, codexErr error
	var updated, machinesChanged <-chan struct{}
	k := kijkState{Version: kijkVersion}
	if u != nil {
		updated = u.changed
	}
	if mm != nil {
		machinesChanged = mm.changed
	}
	start := time.Now()
	next := start
	for {
		if !time.Now().Before(next) {
			rows, err, codexErr = s.scan()
			rts.set(rows)
			// Wait for remote machines to connect, so the first event counts their agents too.
			if mm.settled() || time.Since(start) > 30*time.Second {
				machines, remote := mm.counts()
				tel.scanned(agentTypeCounts(rows), machines, remote, time.Now())
			}
			next = time.Now().Add(time.Second)
		}
		if u != nil {
			k = u.state()
		}
		h.publish(sessionMessage(rows, err, codexErr, k, mm.views()))
		select {
		case <-time.After(time.Until(next)):
		case <-updated:
		case <-machinesChanged:
		}
	}
}

func newToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}
