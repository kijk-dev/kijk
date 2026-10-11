package main

import (
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
	go watchSessions(newScanner(claudeDir()), h, rts, u, tel)

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
	log.Fatal(http.Serve(ln, newServer(token, *port, web, h, rts, u)))
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

// sessionMessage is one message on /api/events: the agent rows and the status bar state.
func sessionMessage(rows []Row, err error, k kijkState) []byte {
	msg := struct {
		Rows  []Row     `json:"rows"`
		Error string    `json:"error,omitempty"`
		Kijk  kijkState `json:"kijk"`
	}{Rows: rows, Kijk: k}
	if err != nil {
		msg.Error = err.Error()
	}
	b, _ := json.Marshal(msg)
	return b
}

// An update state change publishes at once, with the rows from the last poll.
func watchSessions(s *scanner, h *hub, rts *roots, u *updater, tel *telemetry) {
	var rows []Row
	var err error
	next := time.Now()
	for {
		if !time.Now().Before(next) {
			rows, err = s.scan()
			rts.set(rows)
			tel.scanned(agentCount(rows), time.Now())
			next = time.Now().Add(time.Second)
		}
		h.publish(sessionMessage(rows, err, u.state()))
		select {
		case <-time.After(time.Until(next)):
		case <-u.changed:
		}
	}
}

func newToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}
