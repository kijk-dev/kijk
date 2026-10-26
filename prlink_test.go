package main

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestParsePRLink(t *testing.T) {
	for raw, want := range map[string]string{
		"https://github.com/acme/shop/pull/477":                        "github github.com acme/shop 477",
		"https://github.com/acme/shop/pull/477/files?diff=unified#top": "github github.com acme/shop 477",
		"https://github.com/acme/.github/pull/2":                       "github github.com acme/.github 2",
		"https://gitlab.com/group/sub/api/-/merge_requests/12":         "gitlab gitlab.com group/sub/api 12",
		"https://gitlab.example.org/team/api/-/merge_requests/3/diffs": "gitlab gitlab.example.org team/api 3",
		"https://gitlab.example.org:8443/team/api/-/merge_requests/3":  "gitlab gitlab.example.org team/api 3",
		"https://github.com/acme/shop/issues/477":                      "",
		"https://github.com/acme/shop":                                 "",
		"https://github.com/acme/shop/pull/x":                          "",
		"~/code/shop":                                                  "",
		"github.com/acme/shop/pull/477":                                "",
		"https://github.com/pull/477":                                  "",
		"https://github.com/acme/shop/pull/0":                          "",
		"https://github.com/acme/shop/pull/99999999999999999999":       "",
		"https://github.com/acme/shop/pull/477?x=/-/merge_requests/1":  "github github.com acme/shop 477",
		// A link names folders and tool arguments, so it may not climb out or pass a flag.
		"https://github.com/acme/../../etc/pull/1":                  "",
		"https://github.com/acme/./shop/pull/1":                     "",
		"https://github.com/acme//shop/pull/1":                      "",
		"https://../acme/shop/pull/1":                               "",
		`https://github.com/acme\..\shop/x/pull/1`:                  "",
		"https://gitlab.com/--upload-pack=x/api/-/merge_requests/1": "",
		"https://github.com/acme/sh%2Fop/pull/1":                    "",
	} {
		got := ""
		if l, ok := parsePRLink(raw); ok {
			got = l.Kind + " " + l.Host + " " + l.Path + " " + strconv.Itoa(l.Number)
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

// A forge on its own port: the clone's origin names the port too.
func TestPRClonePort(t *testing.T) {
	isolatedGit(t)
	repo := gitRepo(t)
	run(t, repo, "remote", "add", "origin", "https://gitlab.example.org:8443/team/api.git")
	link, _ := parsePRLink("https://gitlab.example.org:8443/team/api/-/merge_requests/3")
	if got := prClone(link, []string{repo}, func() []string { return nil }); got == "" {
		t.Errorf("no clone found for %+v", link)
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
