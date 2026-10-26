package main

import (
	"os"
	"path/filepath"
	"slices"
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

func TestPRCandidates(t *testing.T) {
	p := filepath.FromSlash
	billing := prLink{Kind: "github", Host: "github.com", Path: "acme/billing", Number: 12}
	saved := []string{p("/h/code/shop"), p("/h/code/billing")}
	agents := []string{p("/h/work/shop-agent"), p("/h/code/shop")}
	want := []string{
		// Saved folders.
		"/h/code/shop", "/h/code/billing",
		// Where kijk runs, its parents up to home, and the repo's name in it and next to it.
		"/h/code/shop/web", "/h/code", "/h/code/shop/web/billing", "/h/code/shop/billing",
		// kijk's own clone.
		"/h/.cache/kijk/clones/github.com/acme/billing",
		// Agent folders, then folders next to the saved and agent folders.
		"/h/work/shop-agent", "/h/work/billing",
		// Common places.
		"/h/src/billing", "/h/projects/billing", "/h/repos/billing", "/h/dev/billing", "/h/git/billing",
		"/h/workspace/billing", "/h/Workspaces/billing", "/h/Developer/billing",
		"/h/go/src/github.com/acme/billing", "/h/ghq/github.com/acme/billing", "/h/src/github.com/acme/billing", "/h/code/github.com/acme/billing",
	}
	check := func(got, want []string) {
		t.Helper()
		for i := range want {
			want[i] = p(want[i])
		}
		if !slices.Equal(got, want) {
			t.Errorf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
	}
	check(prCandidates(billing, saved, agents, p("/h/code/shop/web"), p("/h"), p("/h/.cache")), want)
	// Outside the home folder, the parents go up to the root, which is never a clone.
	check(prCandidates(billing, nil, nil, p("/srv/x"), "", ""), []string{"/srv/x", "/srv", "/srv/x/billing", "/srv/billing"})
	api := prLink{Kind: "gitlab", Host: "gitlab.com", Path: "group/sub/api", Number: 3}
	got := prCandidates(api, nil, nil, "", p("/h"), p("/c"))
	if got[0] != p("/c/kijk/clones/gitlab.com/group/sub/api") || got[1] != p("/h/code/api") || got[len(got)-1] != p("/h/code/gitlab.com/group/sub/api") {
		t.Errorf("a GitLab subgroup: %q", got)
	}
}

func TestMatchClone(t *testing.T) {
	main, worktree, other := shopClones(t)
	shop := prLink{Kind: "github", Host: "github.com", Path: "acme/shop", Number: 477}
	plain := t.TempDir()
	// The user writes origins as gh:<path>, and git rewrites them.
	alias := gitRepo(t)
	run(t, alias, "remote", "add", "origin", "gh:acme/shop")
	run(t, alias, "config", "--global", "url.https://github.com/.insteadOf", "gh:")
	// origin names the forge, but git fetches from a mirror, as in the browser tests.
	mirror := gitRepo(t)
	run(t, mirror, "remote", "add", "origin", "https://github.com/acme/shop.git")
	run(t, mirror, "config", "url."+t.TempDir()+".insteadOf", "https://github.com/acme/shop.git")
	for _, c := range []struct {
		name    string
		places  []string
		root    string
		checked int
	}{
		{"another repo, then the clone", []string{plain, filepath.Join(plain, "gone"), other, main}, main, 2},
		{"a worktree leads to its main clone", []string{worktree}, main, 1},
		{"an origin through an insteadOf alias", []string{alias}, alias, 1},
		{"an origin rewritten to a mirror", []string{mirror}, mirror, 1},
		{"no clone", []string{plain, other}, "", 1},
	} {
		root, checked := matchClone(shop, c.places)
		if realPath(root) != realPath(c.root) || checked != c.checked {
			t.Errorf("%s: %q after %d places, want %q after %d", c.name, root, checked, c.root, c.checked)
		}
	}

	// A miss checks at most 64 places with .git.
	var many []string
	for i := range maxChecked {
		d := filepath.Join(plain, strconv.Itoa(i))
		os.MkdirAll(filepath.Join(d, ".git"), 0o755)
		many = append(many, d)
	}
	if root, checked := matchClone(shop, append(slices.Clone(many), main)); root != "" || checked != 64 {
		t.Errorf("past the cap: %q after %d places", root, checked)
	}
	if root, checked := matchClone(shop, append(many[:63], main)); realPath(root) != realPath(main) || checked != 64 {
		t.Errorf("the 64th place: %q after %d places", root, checked)
	}
}

// A forge on its own port: the clone's origin names the port too.
func TestMatchClonePort(t *testing.T) {
	isolatedGit(t)
	repo := gitRepo(t)
	run(t, repo, "remote", "add", "origin", "https://gitlab.example.org:8443/team/api.git")
	link, _ := parsePRLink("https://gitlab.example.org:8443/team/api/-/merge_requests/3")
	if root, _ := matchClone(link, []string{repo}); root == "" {
		t.Errorf("no clone found for %+v", link)
	}
}
