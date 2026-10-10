package main

import (
	"errors"
	"path/filepath"
	"testing"
)

// fakeSystem says the given paths exist; on Linux, the given commands are on the PATH.
func fakeSystem(goos, def string, have ...string) system {
	set := map[string]bool{}
	for _, p := range have {
		set[p] = true
	}
	env := map[string]string{"ProgramFiles": `C:\PF`, "LocalAppData": `C:\Local`}
	return system{
		goos:   goos,
		home:   "/Users/me",
		def:    def,
		env:    func(k string) string { return env[k] },
		exists: func(p string) bool { return set[p] },
		lookPath: func(c string) (string, error) {
			if set[c] {
				return "/usr/bin/" + c, nil
			}
			return "", errors.New("not found")
		},
	}
}

func TestAppBrowserMac(t *testing.T) {
	brave := "/Applications/Brave Browser.app/Contents/MacOS/Brave Browser"
	chromeExe := "/Users/me/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
	for _, c := range []struct {
		name, def string
		have      []string
		want      string
	}{
		{"default is Brave", "com.brave.browser", []string{brave, chromeExe}, "/Applications/Brave Browser.app"},
		{"default is Safari", "com.apple.safari", []string{brave, chromeExe}, "/Users/me/Applications/Google Chrome.app"},
		{"default Brave is gone", "com.brave.browser", []string{chromeExe}, "/Users/me/Applications/Google Chrome.app"},
		{"an Edge folder without its program", "com.microsoft.edgemac", []string{"/Applications/Microsoft Edge.app"}, ""},
		{"nothing installed", "", nil, ""},
	} {
		if got := appBrowser(fakeSystem("darwin", c.def, c.have...)); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestAppBrowserLinux(t *testing.T) {
	if got := appBrowser(fakeSystem("linux", "brave-browser.desktop", "brave-browser", "google-chrome")); got != "/usr/bin/brave-browser" {
		t.Errorf("default Brave: %q", got)
	}
	if got := appBrowser(fakeSystem("linux", "firefox.desktop", "microsoft-edge-stable")); got != "/usr/bin/microsoft-edge-stable" {
		t.Errorf("default Firefox, Edge installed: %q", got)
	}
	if got := appBrowser(fakeSystem("linux", "vivaldi-stable.desktop", "vivaldi-stable", "google-chrome")); got != "/usr/bin/vivaldi-stable" {
		t.Errorf("default Vivaldi: %q", got)
	}
}

func TestAppBrowserWindows(t *testing.T) {
	brave := filepath.Join(`C:\Local`, `BraveSoftware\Brave-Browser\Application\brave.exe`)
	chromeExe := filepath.Join(`C:\PF`, `Google\Chrome\Application\chrome.exe`)
	if got := appBrowser(fakeSystem("windows", "BraveHTML", brave, chromeExe)); got != brave {
		t.Errorf("default Brave: %q", got)
	}
	if got := appBrowser(fakeSystem("windows", "MSEdgeHTM.ABC123", brave, chromeExe)); got != chromeExe {
		t.Errorf("default Edge not installed, Chrome is: %q", got)
	}
}
