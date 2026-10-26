package main

import (
	"errors"
	"net"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/kijk-dev/kijk/internal/agents"
	"github.com/kijk-dev/kijk/internal/git"
)

// prLink is a link to a pull request on GitHub or a merge request on GitLab.
type prLink struct {
	Kind   string `json:"kind"` // "github" for /pull/, "gitlab" for /-/merge_requests/: the tool that clones it
	Host   string `json:"host"`
	Path   string `json:"path"`
	Number int    `json:"number"`
}

var prLinkRe = regexp.MustCompile(`^https?://([^/?#]+)/([^?#]+?)/(pull|-/merge_requests)/([0-9]+)(?:[/?#].*)?$`)

func parsePRLink(s string) (prLink, bool) {
	m := prLinkRe.FindStringSubmatch(s)
	if m == nil {
		return prLink{}, false
	}
	n, err := strconv.Atoi(m[4])
	if err != nil {
		return prLink{}, false
	}
	host := m[1]
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h // as a clone's origin is matched, by its host name alone
	}
	l := prLink{Kind: "github", Host: host, Path: m[2], Number: n}
	if m[3] != "pull" {
		l.Kind = "gitlab"
	}
	return l, l.valid()
}

// namePart is one name in a link's host or repo path. They become folder names and arguments of
// gh and glab, so none may climb out of a folder or read as a flag.
var namePart = regexp.MustCompile(`^[A-Za-z0-9_.][A-Za-z0-9_.-]*$`)

func (l prLink) valid() bool {
	parts := strings.Split(l.Path, "/")
	ok := (l.Kind == "github" || l.Kind == "gitlab") && l.Number > 0 && len(parts) > 1
	for _, p := range append(parts, l.Host) {
		ok = ok && namePart.MatchString(p) && p != "." && p != ".."
	}
	return ok
}

// prCandidates lists the places that may hold a clone of l's repo, most likely first, with no
// duplicates. It joins paths only and runs no process.
func prCandidates(l prLink, saved, agents []string, cwd, home, cache string) []string {
	name := path.Base(l.Path)
	var places []string
	seen := map[string]bool{"": true}
	add := func(ps ...string) {
		for _, p := range ps {
			if !seen[p] {
				seen[p] = true
				places = append(places, p)
			}
		}
	}
	add(saved...)
	if cwd != "" {
		// kijk may run in a subfolder of the clone, or in the folder that holds it.
		for d := filepath.Clean(cwd); d != home && d != filepath.Dir(d); d = filepath.Dir(d) {
			add(d)
		}
		add(filepath.Join(cwd, name), filepath.Join(filepath.Dir(cwd), name))
	}
	if cache != "" {
		add(cloneDir(cache, l))
	}
	add(agents...)
	for _, r := range slices.Concat(saved, agents) {
		add(filepath.Join(filepath.Dir(r), name))
	}
	if home != "" {
		for _, d := range []string{"code", "src", "projects", "repos", "dev", "git", "work", "workspace", "Workspaces", "Developer"} {
			add(filepath.Join(home, d, name))
		}
		for _, d := range []string{"go/src", "ghq", "src", "code"} {
			add(filepath.Join(home, filepath.FromSlash(d), l.Host, filepath.FromSlash(l.Path)))
		}
	}
	return places
}

// maxChecked bounds what a miss costs: each place with .git costs a git process or two.
const maxChecked = 64

// matchClone returns the main clone of the first place whose origin is l's repo, so an agent's
// worktree leads to the clone it belongs to, and how many places with .git it checked.
func matchClone(l prLink, places []string) (root string, checked int) {
	same := func(host, path string, ok bool) bool {
		return ok && strings.EqualFold(host, l.Host) && strings.EqualFold(path, l.Path)
	}
	for _, p := range places {
		if checked == maxChecked {
			break
		}
		if _, err := os.Stat(filepath.Join(p, ".git")); err != nil {
			continue
		}
		checked++
		// After the user's insteadOf rewrites a short alias names the forge; as written, an origin
		// rewritten to a mirror still does.
		if same(git.OriginURL(p)) || same(git.Origin(p)) {
			return git.MainClone(p), checked
		}
	}
	return "", checked
}

// cloneDir is where kijk clones l's repo when it finds no clone: in the user's cache folder.
func cloneDir(cache string, l prLink) string {
	return filepath.Join(cache, "kijk", "clones", l.Host, filepath.FromSlash(l.Path))
}

func noClone(l prLink) error {
	return errors.New("No folder of " + l.Host + "/" + l.Path + " is in kijk. Add a folder of this repo first: kijk <folder>")
}

// localAgentFolders are the folders of the agents that run on this machine now.
func localAgentFolders() []string {
	rows, _, _ := newScanner(agents.Types, agents.NewEnv(kijkVersion)).scan()
	return localRoots(rows)
}
