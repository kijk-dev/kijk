package main

import (
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestParseArgs(t *testing.T) {
	for _, c := range []struct {
		args          []string
		port          int
		folders       []string
		foldersWindow bool
		flags         []string
		err           string
	}{
		{args: nil, port: 7717},
		{args: []string{"./update"}, port: 7717, folders: []string{"./update"}, foldersWindow: true},
		{args: []string{"-port", "7800", "~/a"}, port: 7800, folders: []string{"~/a"}, foldersWindow: true, flags: []string{"-port", "7800"}},
		{args: []string{"folders"}, port: 7717, foldersWindow: true},
		{args: []string{"folders", "-no-open"}, port: 7717, foldersWindow: true, flags: []string{"-no-open"}},
		{args: []string{"folders", "a"}, err: "takes no folder"},
		{args: []string{"-nope"}, err: "not defined"},
	} {
		a, err := parseArgs(c.args)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%q: want error %q, got %v", c.args, c.err, err)
			}
			continue
		}
		if err != nil || a.port != c.port || !slices.Equal(a.folders, c.folders) || a.foldersWindow != c.foldersWindow || !slices.Equal(a.flags, c.flags) {
			t.Errorf("%q: got %+v %v", c.args, a, err)
		}
	}
}

func TestAddedText(t *testing.T) {
	kijk := []savedFolder{{Path: "/code/kijk", Open: "/code/kijk/web"}}
	api := []savedFolder{{Path: "/code/api"}}
	two := append(api, kijk...)
	for _, c := range []struct {
		adds   []savedFolder
		added  []bool
		window bool
		want   string
	}{
		{kijk, []bool{true}, false, "Added kijk, opened at web/."},
		{api, []bool{false}, true, `api is already in the list. It is open in the window "kijk · Folders".`},
		{two, []bool{true, true}, false, "Added 2 folders to the folders window."},
		{two, []bool{true, false}, true, `Added 1 folder to the window "kijk · Folders". 1 was already in the list.`},
		{two, []bool{false, false}, true, "All 2 folders are already in the list."},
	} {
		if got := addedText(c.adds, c.added, c.window); got != c.want {
			t.Errorf("got %q, want %q", got, c.want)
		}
	}
}

// The folder view must work on a page without the rail, so viewer files never import app files.
func TestViewerImportsNoAppFile(t *testing.T) {
	app := []string{"app.js", "status.js"}
	imports := regexp.MustCompile(`\b(?:from|import)\s*\(?\s*['"]\./([^'"]+)['"]`)
	files, _ := fs.Glob(webFiles, "web/*.js")
	for _, f := range files {
		if slices.Contains(app, path.Base(f)) {
			continue
		}
		b, err := fs.ReadFile(webFiles, f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range imports.FindAllStringSubmatch(string(b), -1) {
			if slices.Contains(app, m[1]) {
				t.Errorf("%s imports %s, an app file", f, m[1])
			}
		}
	}
	if len(files) < 10 {
		t.Fatalf("found only %d JavaScript files", len(files))
	}
}

// TestPRsTabAsksThroughTheStore checks that only prstore.js asks the forge about pull requests or
// writes to it, so every answer goes through its one rule of which answer is newer.
func TestPRsTabAsksThroughTheStore(t *testing.T) {
	asks := regexp.MustCompile(`(?:^|[^.\w])get\(\s*['"](?:prs?|pr/view|pr/since)['"]`)
	posts := regexp.MustCompile(`\bpost\(`)
	tab := []string{"pr.js", "prs.js", "review.js", "drafts.js", "boxes.js", "changes.js", "overview.js", "threads.js"}
	files, _ := fs.Glob(webFiles, "web/*.js")
	for _, f := range files {
		name := path.Base(f)
		if name == "prstore.js" {
			continue
		}
		b, err := fs.ReadFile(webFiles, f)
		if err != nil {
			t.Fatal(err)
		}
		if asks.Match(b) {
			t.Errorf("%s asks the forge about pull requests; ask through prstore.js", f)
		}
		if slices.Contains(tab, name) && posts.Match(b) {
			t.Errorf("%s writes to the forge; write through prstore.js", f)
		}
	}
}

// TestPRsTabDrawsInPlace checks that the PRs tab draws through patch, which keeps the elements the
// user holds, instead of building them again and putting back the focus or the scroll place by hand.
func TestPRsTabDrawsInPlace(t *testing.T) {
	rebuilds := regexp.MustCompile(`\bkeepFocus\b|\.replaceChildren\(|\.scrollTop\s*=[^=]`)
	for _, name := range []string{"pr.js", "prs.js", "overview.js"} {
		b, err := fs.ReadFile(webFiles, "web/"+name)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range rebuilds.FindAll(b, -1) {
			t.Errorf("%s: %s; draw through patch", name, m)
		}
	}
}
