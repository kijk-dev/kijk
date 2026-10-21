package main

import (
	"errors"
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
	return prLink{Host: m[1], Path: m[2], Number: n}, true
}

// prClone finds the folder to open a pull request in: a clone of its repo among the saved
// folders, else among the folders scan gives, the local agents'. It gives the main clone of what
// it finds, so an agent's worktree leads to the clone it belongs to. kijk never clones.
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
	return rowRoots(rows)
}

func savedFolderPaths() []string {
	p, err := foldersPath()
	if err != nil {
		return nil
	}
	list, _ := readFolders(p)
	paths := folderPaths(list)
	for i := range paths {
		paths[i] = filepath.Clean(paths[i])
	}
	return paths
}
