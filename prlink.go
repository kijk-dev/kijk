package main

import (
	"errors"
	"net"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/kijk-dev/kijk/internal/agents"
	"github.com/kijk-dev/kijk/internal/git"
)

// prLink is a link to a pull request on GitHub or a merge request on GitLab.
type prLink struct {
	Host, Path string
	Number     int
}

var prLinkRe = regexp.MustCompile(`^https?://([^/?#]+)/([^?#]+?)/(?:pull|-/merge_requests)/([0-9]+)(?:[/?#].*)?$`)

func parsePRLink(s string) (prLink, bool) {
	m := prLinkRe.FindStringSubmatch(s)
	if m == nil || !strings.Contains(m[2], "/") {
		return prLink{}, false
	}
	n, err := strconv.Atoi(m[3])
	if err != nil || n <= 0 {
		return prLink{}, false
	}
	host := m[1]
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h // as a clone's origin is matched, by its host name alone
	}
	return prLink{Host: host, Path: m[2], Number: n}, true
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

func savedFolderPaths() []string {
	p, err := foldersPath()
	if err != nil {
		return nil
	}
	list, _ := readFolders(p)
	paths := make([]string, len(list))
	for i, f := range list {
		paths[i] = filepath.Clean(f.Path)
	}
	return paths
}
