package main

import (
	"bytes"
	"cmp"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/kijk-dev/kijk/internal/agents"
	"github.com/kijk-dev/kijk/internal/viewer"
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
	a, err := parseArgs(os.Args[1:])
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "kijk:", err)
		os.Exit(2)
	}
	if a.version {
		fmt.Println("kijk", kijkVersion)
		return
	}
	var adds []savedFolder
	cwd, _ := os.Getwd()
	if len(a.folders) > 0 {
		if adds, err = checkFolders(a.folders, cwd); err != nil {
			fmt.Fprintf(os.Stderr, "kijk: %v.\n", err)
			os.Exit(1)
		}
	}
	ask := askOf(a, adds, cwd)
	plain := len(ask.Folders) == 0 && ask.PR == nil && ask.List == ""
	term := terminal{os.Stdin, os.Stdout, os.Stderr}

	removeOldExe()
	if s, ok := runningInstance(a.port); ok {
		if plain {
			fmt.Println("kijk is already running at", uiURL(a.port, s.Token))
		}
		os.Exit(deliver(runningOpener(a.port, s.Token), ask, windows(a, s.Token), term))
	}
	fp, fpErr := foldersPath()
	if fpErr != nil && !plain {
		fmt.Fprintln(os.Stderr, "kijk: cannot find the folders list:", fpErr)
		os.Exit(1)
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", a.port))
	if err != nil {
		log.Fatalf("cannot listen on port %d (another program may use it; try -port): %v", a.port, err)
	}
	if token == "" {
		token = newToken()
	}
	remove, err := saveInstance(a.port, token)
	if err != nil {
		log.Printf("cannot write the state file, so a second start will not find this one: %v", err)
	}

	exe, _ := selfPath()
	u := newUpdater(kijkVersion, exe, os.Stdout)
	u.restart = func() error {
		remove()
		args := a.flags
		if !slices.Contains(args, "-no-open") {
			args = append(args, "-no-open")
		}
		err := restartInPlace(exe, args, token)
		remove, _ = saveInstance(a.port, token) // reached only when the restart failed
		return err
	}
	if isRelease(kijkVersion) {
		go u.run(updateCheckOn(a.noCheck))
	}
	var tel *telemetry
	if telemetryOn(a.noTelemetry, kijkVersion, posthogKey) {
		tel = newTelemetry(posthogKey, kijkVersion)
		if !restarted {
			tel.send("kijk started", nil)
		}
	}
	h := newHub()
	rts := &viewer.Roots{}
	rl := newRail(h, rts)
	var mm *machineManager
	if runtime.GOOS != "windows" {
		mm = newMachineManager(kijkVersion)
	}
	fl := newFolderList(fp, fpErr, rl)
	if fl.readErr != nil {
		fmt.Fprintln(os.Stderr, "kijk:", fl.readErr)
	}
	go fl.watch(h)
	go watchSessions(newScanner(agents.Types, agents.NewEnv(kijkVersion)), h, rl, u, tel, mm, fl)

	web, _ := fs.Sub(webFiles, "web")
	d := &openDesk{h, rl, fl}
	srv := newServer(token, a.port, web, h, rts, u, mm, d)
	served := make(chan error, 1)
	go func() { served <- http.Serve(ln, srv) }()
	// A clone in deliver takes Ctrl+C for itself, to clean up before kijk exits.
	if code := deliver(d.open, ask, windows(a, token), term); code != 0 {
		remove()
		os.Exit(code)
	}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-stop
		remove()
		os.Exit(0)
	}()
	if restarted {
		fmt.Printf("kijk %s restarted at %s\n", kijkVersion, uiURL(a.port, token))
	} else {
		fmt.Printf("kijk %s is running at %s\n", kijkVersion, uiURL(a.port, token))
	}
	log.Fatal(<-served)
}

// cliArgs is what kijk was asked to do, when it is not one of the commands that exit at once.
type cliArgs struct {
	port                                  int
	noOpen, version, noCheck, noTelemetry bool
	folders                               []string // to add to the folders list
	list                                  string   // "folders" for kijk folders
	pr                                    prLink   // kijk <PR link>
	flags                                 []string // as given, for a restart
}

// askOf is what the command asks the kijk that shows it. adds are its folders, checked; cwd is
// where it runs, where a clone of a pull request's repo may be.
func askOf(a cliArgs, adds []savedFolder, cwd string) openAsk {
	ask := openAsk{List: a.list}
	if a.pr.Number > 0 {
		ask.PR, ask.Cwd = &a.pr, cwd
	}
	for _, f := range adds {
		ask.Folders = append(ask.Folders, cmp.Or(f.Open, f.Path))
	}
	return ask
}

// windows opens a new kijk window on request seq, or is nil with -no-open.
func windows(a cliArgs, token string) func(seq int) {
	if a.noOpen {
		return nil
	}
	return func(seq int) { openUI(windowURL(a.port, token, seq)) }
}

// parseArgs reads flags, then folders. kijk folders shows the Folders list and takes no folder.
func parseArgs(args []string) (cliArgs, error) {
	var a cliArgs
	fs := flag.NewFlagSet("kijk", flag.ContinueOnError)
	fs.IntVar(&a.port, "port", 7717, "port to listen on")
	fs.BoolVar(&a.noOpen, "no-open", false, "do not open the browser")
	fs.BoolVar(&a.version, "version", false, "print the version and exit")
	fs.BoolVar(&a.noCheck, "no-update-check", false, "do not check GitHub for a new kijk version")
	fs.BoolVar(&a.noTelemetry, "no-telemetry", false, "do not send anonymous usage events")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: kijk [flags] [folder…]\n       kijk [flags] <PR link>\n       kijk folders [flags]")
		fs.PrintDefaults()
	}
	if len(args) > 0 && args[0] == "folders" {
		a.list = listFolders
		args = args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return a, err
	}
	a.flags = slices.Clone(args[:len(args)-fs.NArg()])
	if a.list != "" && fs.NArg() > 0 {
		return a, errors.New("kijk folders takes no folder. To add one, run kijk <folder>")
	}
	if l, ok := parsePRLink(fs.Arg(0)); ok {
		if fs.NArg() > 1 {
			return a, errors.New("kijk <PR link> takes one link and no folder")
		}
		a.pr = l
		return a, nil
	}
	a.folders = fs.Args()
	return a, nil
}

// callRunning sends a request to the kijk on port, with its token, and reads the JSON reply.
func callRunning(port int, token, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, fmt.Sprintf("http://127.0.0.1:%d%s", port, path), rd)
	req.AddCookie(&http.Cookie{Name: cookieName(port), Value: token})
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed:
		return errors.New("the running kijk is too old for this. Quit it, then try again")
	case resp.StatusCode != http.StatusOK:
		return errors.New(strings.TrimSpace(string(b)))
	}
	return json.Unmarshal(b, out)
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

// watchSessions looks for agents while a kijk window is open, and for a minute after the last one
// closes. An update or machine state change reaches the rail at once, with the last scan's rows.
func watchSessions(s *scanner, h *hub, rl *rail, u *updater, tel *telemetry, mm *machineManager, fl *folderList) {
	var rows []Row
	var errs []typeError
	installed := true
	var updated, machinesChanged <-chan struct{}
	if u != nil {
		updated = u.changed
	}
	if mm != nil {
		machinesChanged = mm.changed
	}
	looking := false
	stopMachines := func() {}
	var since, next time.Time
	for {
		opened := h.opened()
		now := time.Now()
		if want := h.wanted(now, time.Minute); want != looking {
			looking = want
			if looking {
				if mm != nil {
					stopMachines = mm.start()
				}
				since, next = now, now
			} else {
				stopMachines()
				rows, errs, installed = nil, nil, true
				rl.setAgents(nil)
			}
		}
		var counts *agentCounts
		if looking && !now.Before(next) {
			rows, errs, installed = s.scan()
			// Wait for remote machines to connect, so the first event counts their agents too.
			if mm.settled() || time.Since(since) > 30*time.Second {
				machines, remote := mm.counts()
				counts = &agentCounts{agentTypeCounts(rows), machines, remote}
			}
			next = time.Now().Add(time.Second)
		}
		tel.active(now, fl.count(), looking, counts)
		if u != nil {
			rl.setKijk(u.state())
		}
		if looking {
			rl.setAgents(&agentPart{rows, errs, !installed, mm.views()})
		}
		wait := time.Second
		if looking {
			wait = time.Until(next)
		}
		select {
		case <-time.After(wait):
		case <-updated:
		case <-machinesChanged:
		case <-opened:
		}
	}
}

func newToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}
