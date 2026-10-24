package main

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestParsePRLink(t *testing.T) {
	for raw, want := range map[string]string{
		"https://github.com/acme/shop/pull/477":                        "github.com acme/shop 477",
		"https://github.com/acme/shop/pull/477/files?diff=unified#top": "github.com acme/shop 477",
		"https://gitlab.com/group/sub/api/-/merge_requests/12":         "gitlab.com group/sub/api 12",
		"https://gitlab.example.org/team/api/-/merge_requests/3/diffs": "gitlab.example.org team/api 3",
		"https://github.com/acme/shop/issues/477":                      "",
		"https://github.com/acme/shop":                                 "",
		"https://github.com/acme/shop/pull/x":                          "",
		"~/code/shop":                                                  "",
		"github.com/acme/shop/pull/477":                                "",
		"https://github.com/pull/477":                                  "",
		"https://github.com/acme/shop/pull/0":                          "",
		"https://github.com/acme/shop/pull/477?x=/-/merge_requests/1":  "github.com acme/shop 477",
	} {
		got := ""
		if l, ok := parsePRLink(raw); ok {
			got = l.Host + " " + l.Path + " " + strconv.Itoa(l.Number)
		}
		if got != want {
			t.Errorf("parsePRLink(%q) = %q, want %q", raw, got, want)
		}
	}
}

// shopClones is a main clone of github.com/acme/shop with a worktree, and a clone of another repo.
func shopClones(t *testing.T) (main, worktree, other string) {
	t.Helper()
	isolatedGit(t)
	main = gitRepo(t)
	write(t, filepath.Join(main, "a"), "a\n")
	run(t, main, "add", ".")
	run(t, main, "commit", "-qm", "a")
	run(t, main, "remote", "add", "origin", "git@github.com:Acme/Shop.git")
	worktree = filepath.Join(t.TempDir(), "agent")
	run(t, main, "worktree", "add", "-q", "-b", "agent", worktree)
	other = gitRepo(t)
	run(t, other, "remote", "add", "origin", "https://github.com/acme/billing.git")
	return main, worktree, other
}

func TestPRClone(t *testing.T) {
	main, worktree, other := shopClones(t)
	link, _ := parsePRLink("https://github.com/acme/shop/pull/477")
	same := func(a, b string) bool {
		ra, _ := filepath.EvalSymlinks(a)
		rb, _ := filepath.EvalSymlinks(b)
		return ra == rb
	}
	scanned := false
	scan := func() []string { scanned = true; return []string{worktree} }
	// A saved clone is found without a look at the agents.
	if got := prClone(link, []string{other, main}, scan); !same(got, main) || scanned {
		t.Errorf("saved main clone: %q, scanned %v", got, scanned)
	}
	// Only an agent's worktree is known: its main clone opens.
	if got := prClone(link, []string{other}, scan); !same(got, main) || !scanned {
		t.Errorf("from the worktree: %q", got)
	}
	if got := prClone(link, []string{other}, func() []string { return nil }); got != "" {
		t.Errorf("no clone: %q", got)
	}
}

func TestPRLinkNoClone(t *testing.T) {
	_, _, other := shopClones(t)
	link, _ := parsePRLink("https://github.com/acme/web/pull/12")
	err := noClone(link)
	if prClone(link, []string{other}, func() []string { return nil }) != "" || !strings.Contains(err.Error(), "No folder of github.com/acme/web is in kijk. Add a folder of this repo first: kijk <folder>") {
		t.Errorf("message %q", err)
	}
}

func TestParseArgsPRLink(t *testing.T) {
	a, err := parseArgs([]string{"-no-open", "https://github.com/acme/shop/pull/477"})
	if err != nil || a.pr.Number != 477 || !a.foldersWindow || len(a.folders) != 0 || !a.noOpen {
		t.Errorf("got %+v %v", a, err)
	}
	if _, err := parseArgs([]string{"https://github.com/acme/shop/pull/477", "~/code"}); err == nil {
		t.Error("a link and a folder should fail")
	}
}
