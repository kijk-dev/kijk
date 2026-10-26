package main

import (
	"errors"
	"net"
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

// prClone finds the main clone of the pull request's repo among the saved folders, else among the
// folders scan gives, so an agent's worktree leads to the clone it belongs to.
func prClone(l prLink, saved []string, scan func() []string) string {
	if c := cloneIn(l, saved); c != "" {
		return c
	}
	return cloneIn(l, scan())
}

func cloneIn(l prLink, folders []string) string {
	for _, f := range folders {
		host, path, ok := git.Origin(f)
		if ok && strings.EqualFold(host, l.Host) && strings.EqualFold(path, l.Path) {
			return git.MainClone(f)
		}
	}
	return ""
}

func noClone(l prLink) error {
	return errors.New("No folder of " + l.Host + "/" + l.Path + " is in kijk. Add a folder of this repo first: kijk <folder>")
}

// localAgentFolders are the folders of the agents that run on this machine now.
func localAgentFolders() []string {
	rows, _, _ := newScanner(agents.Types, agents.NewEnv(kijkVersion)).scan()
	return localRoots(rows)
}
