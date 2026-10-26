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
	shop := prLink{Host: "github.com", Path: "acme/shop", Number: 477}
	for _, c := range []struct {
		args    []string
		port    int
		folders []string
		list    string
		pr      prLink
		flags   []string
		err     string
	}{
		{args: nil, port: 7717},
		{args: []string{"./update"}, port: 7717, folders: []string{"./update"}},
		{args: []string{"-port", "7800", "~/a", "b"}, port: 7800, folders: []string{"~/a", "b"}, flags: []string{"-port", "7800"}},
		{args: []string{"folders"}, port: 7717, list: listFolders},
		{args: []string{"folders", "-no-open"}, port: 7717, list: listFolders, flags: []string{"-no-open"}},
		{args: []string{"-no-open", "https://github.com/acme/shop/pull/477"}, port: 7717, pr: shop, flags: []string{"-no-open"}},
		{args: []string{"folders", "a"}, err: "takes no folder"},
		{args: []string{"https://github.com/acme/shop/pull/477", "~/code"}, err: "takes one link and no folder"},
		{args: []string{"-nope"}, err: "not defined"},
	} {
		a, err := parseArgs(c.args)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%q: want error %q, got %v", c.args, c.err, err)
			}
			continue
		}
		if err != nil || a.port != c.port || !slices.Equal(a.folders, c.folders) || a.list != c.list || a.pr != c.pr || !slices.Equal(a.flags, c.flags) {
			t.Errorf("%q: got %+v %v", c.args, a, err)
		}
	}
}

// What each command sends to the kijk that shows it.
func TestAskOf(t *testing.T) {
	adds := []savedFolder{{Path: "/code/kijk", Open: "/code/kijk/web"}, {Path: "/code/api"}}
	for _, c := range []struct {
		a    cliArgs
		adds []savedFolder
		want string
	}{
		{cliArgs{}, nil, `{}`},
		{cliArgs{folders: []string{"web", "~/code/api"}}, adds, `{"folders":["/code/kijk/web","/code/api"]}`},
		{cliArgs{list: listFolders}, nil, `{"list":"folders"}`},
		{cliArgs{pr: prLink{Host: "github.com", Path: "acme/shop", Number: 477}}, nil, `{"pr":{"host":"github.com","path":"acme/shop","number":477}}`},
	} {
		if got := jsonOf(askOf(c.a, c.adds)); got != c.want {
			t.Errorf("%+v: got %s, want %s", c.a, got, c.want)
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
		{api, []bool{true}, true, "Added api. It is open in the kijk window."},
		{api, []bool{false}, true, "api is already in the Folders list. It is open in the kijk window."},
		{two, []bool{true, true}, false, "Added 2 folders to the Folders list."},
		{two, []bool{true, false}, true, "Added 1 folder to the Folders list. 1 was already in it. The last one is open in the kijk window."},
		{two, []bool{false, false}, false, "All 2 folders are already in the Folders list."},
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

// prsTabFiles are the web files of the PRs tab, besides prstore.js.
var prsTabFiles = []string{"pr.js", "prs.js", "review.js", "drafts.js", "boxes.js", "changes.js", "overview.js", "threads.js"}

// TestPRsTabAsksThroughTheStore checks that only prstore.js asks the forge about pull requests or
// writes to it, so every answer goes through its one rule of which answer is newer.
func TestPRsTabAsksThroughTheStore(t *testing.T) {
	asks := regexp.MustCompile(`(?:^|[^.\w])get\(\s*['"](?:prs?|pr/view|pr/since)['"]`)
	posts := regexp.MustCompile(`\bpost\(`)
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
		if slices.Contains(prsTabFiles, name) && posts.Match(b) {
			t.Errorf("%s writes to the forge; write through prstore.js", f)
		}
	}
}

// TestPRsTabDrawsInPlace checks that the PRs tab draws through patch, which keeps the elements the
// user holds, instead of building them again and putting back the focus or the scroll place by hand.
func TestPRsTabDrawsInPlace(t *testing.T) {
	rebuilds := regexp.MustCompile(`\bkeepFocus\b|\.replaceChildren\(|\.scrollTop\s*=[^=]`)
	// The lines that move the view on purpose.
	allowed := []struct{ file, line, why string }{
		{"boxes.js", "scroll.scrollTop = v.keep.scrollTop", "a tab shown again starts where it was"},
		{"boxes.js", "v.scroll.scrollTop = a.b.el.offsetTop + a.offset", "the box at the top stays while boxes above it fold, open or load"},
		{"boxes.js", "v.scroll.scrollTop = b.el.offsetTop - 12", "a jump to a file, or to the header of a box folded across the top"},
		{"boxes.js", "v.scroll.scrollTop = y", "the arrow keys keep the current line in view"},
		{"boxes.js", "v.scroll.scrollTop = Math.max(0, y - AHEAD)", "a jump to the next change, thread or note"},
		{"changes.js", "v.scroll.scrollTop = 0", "the other diff of the Since switch starts at the top"},
	}
	used := make([]bool, len(allowed))
	for _, name := range prsTabFiles {
		b, err := fs.ReadFile(webFiles, "web/"+name)
		if err != nil {
			t.Fatal(err)
		}
	lines:
		for i, line := range strings.Split(string(b), "\n") {
			if !rebuilds.MatchString(line) {
				continue
			}
			for j, a := range allowed {
				if a.file == name && strings.Contains(line, a.line) {
					used[j] = true
					continue lines
				}
			}
			t.Errorf("%s:%d: draw through patch: %s", name, i+1, strings.TrimSpace(line))
		}
	}
	for j, a := range allowed {
		if !used[j] {
			t.Errorf("%s no longer has %q; drop it from the allowed lines", a.file, a.line)
		}
	}
}
