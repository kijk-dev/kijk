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
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
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
	if a.pr.Number > 0 {
		clone := prClone(a.pr, savedFolderPaths(), localAgentFolders)
		if clone == "" {
			fmt.Fprintln(os.Stderr, "kijk:", noClone(a.pr))
			os.Exit(1)
		}
		a.folders = []string{clone}
	}
	var adds []savedFolder
	if len(a.folders) > 0 {
		cwd, _ := os.Getwd()
		if adds, err = checkFolders(a.folders, cwd); err != nil {
			fmt.Fprintf(os.Stderr, "kijk: %v.\n", err)
			os.Exit(1)
		}
	}

	removeOldExe()
	if s, ok := runningInstance(a.port); ok {
		if a.foldersWindow {
			os.Exit(toRunning(a.port, s.Token, adds, a.pr.Number, a.noOpen, os.Stdout, os.Stderr))
		}
		url := uiURL(a.port, s.Token)
		fmt.Println("kijk is already running at", url)
		if !a.noOpen {
			openUI(url)
		}
		return
	}
	fp, fpErr := foldersPath()
	if fpErr != nil && a.foldersWindow {
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
	addedNote := ""
	if len(adds) > 0 {
		added, err := fl.add(adds)
		if err != nil {
			fmt.Fprintln(os.Stderr, "kijk:", err)
			remove()
			os.Exit(1)
		}
		addedNote = addedText(adds, added, false)
	}
	go fl.watch(h)
	go watchSessions(newScanner(agents.Types, agents.NewEnv(kijkVersion)), h, rl, u, tel, mm, fl)

	web, _ := fs.Sub(webFiles, "web")
	url := uiURL(a.port, token)
	if restarted {
		fmt.Printf("kijk %s restarted at %s\n", kijkVersion, url)
	} else {
		fmt.Printf("kijk %s is running at %s\n", kijkVersion, url)
	}
	if addedNote != "" {
		fmt.Println(addedNote)
	}
	if a.foldersWindow {
		url = foldersURL(a.port, token, adds, a.pr.Number)
	}
	if !a.noOpen {
		openUI(url)
	}
	log.Fatal(http.Serve(ln, newServer(token, a.port, web, h, rts, u, mm, &openDesk{h, rl, fl})))
}

// cliArgs is what kijk was asked to do, when it is not one of the commands that exit at once.
type cliArgs struct {
	port                                  int
	noOpen, version, noCheck, noTelemetry bool
	folders                               []string // to add to the folders list
	foldersWindow                         bool     // kijk <folder>… or kijk folders
	pr                                    prLink   // kijk <PR link>
	flags                                 []string // as given, for a restart
}

// parseArgs reads flags, then folders. kijk folders opens the folders window and takes no folder.
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
		a.foldersWindow = true
		args = args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return a, err
	}
	a.flags = slices.Clone(args[:len(args)-fs.NArg()])
	if a.foldersWindow && fs.NArg() > 0 {
		return a, errors.New("kijk folders takes no folder. To add one, run kijk <folder>")
	}
	if l, ok := parsePRLink(fs.Arg(0)); ok {
		if fs.NArg() > 1 {
			return a, errors.New("kijk <PR link> takes one link and no folder")
		}
		a.pr = l
		a.foldersWindow = true
		return a, nil
	}
	if fs.NArg() > 0 {
		a.folders = fs.Args()
		a.foldersWindow = true
	}
	return a, nil
}

func foldersURL(port int, token string, adds []savedFolder, pr int) string {
	u := uiURL(port, token) + "&window=folders"
	if len(adds) > 0 {
		u += "&open=" + url.QueryEscape(adds[len(adds)-1].Path)
	}
	if pr > 0 {
		u += "&pr=" + strconv.Itoa(pr)
	}
	return u
}

const foldersTitle = `"kijk · Folders"`

// addedText says what kijk <folder>… did. window says a folders window shows it.
func addedText(adds []savedFolder, added []bool, window bool) string {
	if len(adds) == 1 {
		f := adds[0]
		name := filepath.Base(f.Path)
		text := "Added " + name
		if !added[0] {
			text = name + " is already in the list"
		}
		if f.Open != "" {
			text += ", opened at " + relCwd(f.Path, f.Open) + "/"
		}
		if window {
			return text + ". It is open in the window " + foldersTitle + "."
		}
		return text + "."
	}
	n := 0
	for _, a := range added {
		if a {
			n++
		}
	}
	where := "the folders window"
	if window {
		where = "the window " + foldersTitle
	}
	text := fmt.Sprintf("Added %s to %s.", plural(n, "folder"), where)
	switch old := len(adds) - n; {
	case n == 0:
		return fmt.Sprintf("All %d folders are already in the list.", len(adds))
	case old == 1:
		text += " 1 was already in the list."
	case old > 1:
		text += fmt.Sprintf(" %d were already in the list.", old)
	}
	return text
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// toRunning hands kijk <folder>… and kijk folders to the running kijk. It opens a folders window
// only when none is open: kijk cannot bring an open window to the front.
func toRunning(port int, token string, adds []savedFolder, pr int, noOpen bool, stdout, stderr io.Writer) int {
	var res foldersReply
	var err error
	if len(adds) == 0 {
		err = callRunning(port, token, "GET", "/api/folders", nil, &res)
	} else {
		paths := make([]string, len(adds))
		for i, f := range adds {
			paths[i] = cmp.Or(f.Open, f.Path)
		}
		err = callRunning(port, token, "POST", "/api/folders/add", map[string]any{"folders": paths, "pr": pr}, &res)
	}
	if err != nil {
		fmt.Fprintln(stderr, "kijk:", err)
		return 1
	}
	if len(adds) > 0 {
		fmt.Fprintln(stdout, addedText(adds, res.Added, res.Window))
	} else if res.Window {
		fmt.Fprintln(stdout, "The folders window is already open: "+foldersTitle+".")
	}
	if res.Error != "" {
		fmt.Fprintln(stderr, "kijk:", res.Error)
	}
	if !res.Window && !noOpen {
		openUI(foldersURL(port, token, adds, pr))
	}
	return 0
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
		return errors.New("the running kijk is too old for folders. Quit it, then try again")
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
