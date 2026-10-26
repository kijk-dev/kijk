package main

import (
	"iter"
	"net"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/kijk-dev/kijk/internal/agents"
	"github.com/kijk-dev/kijk/internal/git"
)

// prLink is a link to a pull request on GitHub or a merge request on GitLab.
type prLink struct {
	Kind   string `json:"kind"` // "github" for /pull/, "gitlab" for /-/merge_requests/: the tool that clones it
	Host   string `json:"host"`
	Port   string `json:"port,omitempty"` // a forge on its own port; origins and folders use Host alone
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
	l := prLink{Kind: "github", Host: m[1], Path: m[2], Number: n}
	if h, p, err := net.SplitHostPort(m[1]); err == nil {
		l.Host, l.Port = h, p
	}
	if m[3] != "pull" {
		l.Kind = "gitlab"
	}
	return l, l.valid()
}

// namePart is one name in a link's host or repo path. They become folder names and arguments of
// gh and glab, so none may climb out of a folder or read as a flag.
var namePart = regexp.MustCompile(`^[A-Za-z0-9_.][A-Za-z0-9_.-]*$`)

var portRe = regexp.MustCompile(`^[0-9]{1,5}$`)

func (l prLink) valid() bool {
	parts := strings.Split(l.Path, "/")
	ok := (l.Kind == "github" || l.Kind == "gitlab") && l.Number > 0 && len(parts) > 1 &&
		(l.Port == "" || portRe.MatchString(l.Port))
	for _, p := range append(parts, l.Host) {
		ok = ok && namePart.MatchString(p) && p != "." && p != ".."
	}
	return ok
}

// server is the forge's host with its port, as gh and glab reach it.
func (l prLink) server() string {
	if l.Port == "" {
		return l.Host
	}
	return net.JoinHostPort(l.Host, l.Port)
}

// prCandidates lists the places that may hold a clone of l's repo, most likely first, with no
// duplicates. It joins paths only and runs no process; it calls agents only when the places before
// the agents' folders are used up.
func prCandidates(l prLink, saved []string, agents func() []string, cwd, home, cache string) iter.Seq[string] {
	name := path.Base(l.Path)
	next := func(rs []string) []string {
		var ps []string
		for _, r := range rs {
			ps = append(ps, filepath.Join(filepath.Dir(r), name))
		}
		return ps
	}
	var first []string
	if cwd != "" {
		// kijk may run in a subfolder of the clone, or in the folder that holds it.
		for d := filepath.Clean(cwd); d != home && d != filepath.Dir(d); d = filepath.Dir(d) {
			first = append(first, d)
		}
		first = append(first, filepath.Join(cwd, name), filepath.Join(filepath.Dir(cwd), name))
	}
	first = append(first, saved...)
	if cache != "" {
		first = append(first, cloneDir(cache, l))
	}
	first = append(first, next(saved)...)
	if home != "" {
		for _, d := range []string{"code", "src", "projects", "repos", "dev", "git", "work", "workspace", "Workspaces", "Developer"} {
			first = append(first, filepath.Join(home, d, name))
		}
		for _, d := range []string{"go/src", "ghq", "src", "code"} {
			first = append(first, filepath.Join(home, filepath.FromSlash(d), l.Host, filepath.FromSlash(l.Path)))
		}
	}
	return func(yield func(string) bool) {
		seen := map[string]bool{"": true}
		each := func(ps []string) bool {
			for _, p := range ps {
				if !seen[p] {
					seen[p] = true
					if !yield(p) {
						return false
					}
				}
			}
			return true
		}
		if each(first) {
			as := agents()
			if each(as) {
				each(next(as))
			}
		}
	}
}

// maxChecked bounds what a miss costs: each place with .git costs a git process or two.
const maxChecked = 64

// matchClone returns the main clone of the first place whose origin is l's repo, so an agent's
// worktree leads to the clone it belongs to, and how many places with .git it checked.
func matchClone(l prLink, places iter.Seq[string]) (root string, checked int) {
	same := func(host, path string, ok bool) bool {
		return ok && strings.EqualFold(host, l.Host) && strings.EqualFold(path, l.Path)
	}
	for p := range places {
		if _, err := os.Stat(filepath.Join(p, ".git")); err != nil {
			continue
		}
		checked++
		// After the user's insteadOf rewrites a short alias names the forge; as written, an origin
		// rewritten to a mirror still does.
		if same(git.OriginURL(p)) || same(git.Origin(p)) {
			return git.MainClone(p), checked
		}
		if checked == maxChecked {
			break // before the next place, which may need an agent scan
		}
	}
	return "", checked
}

// cloneDir is where kijk clones l's repo when it finds no clone: in the user's cache folder.
func cloneDir(cache string, l prLink) string {
	return filepath.Join(cache, "kijk", "clones", l.Host, filepath.FromSlash(l.Path))
}

// localAgentFolders are the folders of the agents that run on this machine now.
func localAgentFolders() []string {
	rows, _, _ := newScanner(agents.Types, agents.NewEnv(kijkVersion)).scan()
	return localRoots(rows)
}
