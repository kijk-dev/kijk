package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// A browser that can open an app window (--app): no address bar, no tabs, and the page gets
// more of the keys.
type browser struct {
	macApp  string   // the bundle and program name on macOS
	macID   string   // the bundle id, lower case
	linux   []string // commands; any of them can also name its .desktop file
	win     string   // the program under Program Files or LocalAppData
	winProg string   // the start of its ProgId for links
}

var (
	chrome   = browser{"Google Chrome", "com.google.chrome", []string{"google-chrome", "google-chrome-stable"}, `Google\Chrome\Application\chrome.exe`, "ChromeHTML"}
	edge     = browser{"Microsoft Edge", "com.microsoft.edgemac", []string{"microsoft-edge", "microsoft-edge-stable"}, `Microsoft\Edge\Application\msedge.exe`, "MSEdgeHTM"}
	browsers = []browser{
		chrome, edge,
		{"Brave Browser", "com.brave.browser", []string{"brave-browser", "brave"}, `BraveSoftware\Brave-Browser\Application\brave.exe`, "BraveHTML"},
		{"Chromium", "org.chromium.chromium", []string{"chromium", "chromium-browser"}, `Chromium\Application\chrome.exe`, "ChromiumHTM"},
		{"Vivaldi", "com.vivaldi.vivaldi", []string{"vivaldi", "vivaldi-stable"}, `Vivaldi\Application\vivaldi.exe`, "VivaldiHTM"},
	}
)

// system is what the browser lookup reads from the machine, so tests can fake it.
type system struct {
	goos     string
	home     string
	env      func(string) string
	exists   func(string) bool
	lookPath func(string) (string, error)
	def      string // the default browser: a macOS bundle id, a Linux .desktop name or a Windows ProgId
}

func thisSystem() system {
	home, _ := os.UserHomeDir()
	s := system{
		goos:     runtime.GOOS,
		home:     home,
		env:      os.Getenv,
		exists:   func(p string) bool { _, err := os.Stat(p); return err == nil },
		lookPath: exec.LookPath,
	}
	s.def = defaultBrowser(s)
	return s
}

// appBrowser returns what to start for an app window: a macOS app bundle or a program path.
// The default browser comes first, then Chrome, then Edge; "" when none is installed.
func appBrowser(s system) string {
	for _, b := range browsers {
		if s.isDefault(b) {
			if p := s.locate(b); p != "" {
				return p
			}
		}
	}
	for _, b := range []browser{chrome, edge} {
		if p := s.locate(b); p != "" {
			return p
		}
	}
	return ""
}

func (s system) isDefault(b browser) bool {
	d := strings.ToLower(s.def)
	switch s.goos {
	case "darwin":
		return d == b.macID
	case "windows":
		return d != "" && strings.HasPrefix(d, strings.ToLower(b.winProg))
	default:
		return slices.ContainsFunc(b.linux, func(c string) bool { return d == c+".desktop" })
	}
}

// locate checks for the program itself: an app folder can stay behind after its program is gone.
func (s system) locate(b browser) string {
	switch s.goos {
	case "darwin":
		for _, dir := range []string{"/Applications", filepath.Join(s.home, "Applications")} {
			app := filepath.Join(dir, b.macApp+".app")
			if s.exists(filepath.Join(app, "Contents", "MacOS", b.macApp)) {
				return app
			}
		}
	case "windows":
		for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "LocalAppData"} {
			if base := s.env(env); base != "" && s.exists(filepath.Join(base, b.win)) {
				return filepath.Join(base, b.win)
			}
		}
	default:
		for _, c := range b.linux {
			if p, err := s.lookPath(c); err == nil {
				return p
			}
		}
	}
	return ""
}

func defaultBrowser(s system) string {
	switch s.goos {
	case "darwin":
		p := filepath.Join(s.home, "Library", "Preferences", "com.apple.LaunchServices", "com.apple.launchservices.secure.plist")
		out, err := exec.Command("plutil", "-convert", "json", "-o", "-", p).Output()
		if err != nil {
			return ""
		}
		var v struct {
			LSHandlers []struct {
				Scheme string `json:"LSHandlerURLScheme"`
				Role   string `json:"LSHandlerRoleAll"`
			}
		}
		json.Unmarshal(out, &v)
		for _, h := range v.LSHandlers {
			if h.Scheme == "http" {
				return h.Role
			}
		}
	case "windows":
		out, err := exec.Command("reg", "query", `HKCU\Software\Microsoft\Windows\Shell\Associations\UrlAssociations\http\UserChoice`, "/v", "ProgId").Output()
		if err != nil {
			return ""
		}
		if f := strings.Fields(string(out)); len(f) > 0 {
			return f[len(f)-1]
		}
	default:
		out, _ := exec.Command("xdg-settings", "get", "default-web-browser").Output()
		return strings.TrimSpace(string(out))
	}
	return ""
}

// openUI opens url in an app window (see appBrowser), else in the default browser.
func openUI(url string) {
	var cmd *exec.Cmd
	switch b := appBrowser(thisSystem()); {
	case b != "" && runtime.GOOS == "darwin":
		cmd = exec.Command("open", "-na", b, "--args", "--app="+url) // without -n, a running browser drops the arguments
	case b != "":
		cmd = exec.Command(b, "--app="+url)
	case runtime.GOOS == "darwin":
		cmd = exec.Command("open", url)
	case runtime.GOOS == "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	detach(cmd)
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "cannot open a browser: %v\n", err)
		return
	}
	go cmd.Wait()
}
