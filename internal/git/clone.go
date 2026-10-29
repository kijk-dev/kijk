package git

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ErrUnknownHost refuses a clone from a host that CanClone does not allow.
var ErrUnknownHost = errors.New("kijk clones only from github.com, gitlab.com or a host gh or glab is logged in to")

// CanClone says whether kijk may clone from host with the tool of kind: from github.com, gitlab.com,
// or a host the tool is logged in to. A link can name any host, and gh and glab send the user's
// token to the host they clone from. host may name a port, which only glab reaches.
func CanClone(kind, host string) bool {
	switch kind {
	case "github":
		name, _, err := net.SplitHostPort(host)
		if err != nil {
			name = host
		}
		return name == "github.com" || loggedInTo(name, ghHostFiles())
	case "gitlab":
		return host == "gitlab.com" || loggedInTo(host, glabHostFiles())
	}
	return false
}

// Clone clones host/path into dir with gh or glab, with the user's own login and a normal
// checkout; the tool talks to the terminal. host may name a port, which only glab takes. It clones
// into a part folder and renames that, so a stopped clone never leaves a half repo at dir. When dir
// holds a clone already, it does nothing.
func Clone(ctx context.Context, kind, host, path, dir string, in io.Reader, out, errOut io.Writer) error {
	if !CanClone(kind, host) {
		return ErrUnknownHost
	}
	if isClone(dir) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	sweepParts(dir)
	part := partOf(dir, os.Getpid())
	os.RemoveAll(part) // left by a crashed run with this pid; no live run has it now
	name, _, err := net.SplitHostPort(host)
	if err != nil {
		name = host
	}
	tool, repo, env := Tool(kind), name+"/"+path, os.Environ()
	if kind == "gitlab" {
		repo, env = path, append(env, "GITLAB_HOST="+host)
	}
	cmd := exec.CommandContext(ctx, tool, "repo", "clone", repo, part)
	cmd.Env, cmd.Stdin, cmd.Stdout, cmd.Stderr = env, in, out, errOut
	err = cmd.Run()
	switch {
	case ctx.Err() != nil:
		err = ctx.Err()
	case err != nil:
		err = fmt.Errorf("%s repo clone: %w", tool, err)
	default:
		if err = rename(part, dir); err != nil && isClone(dir) {
			err = nil // another run won
		}
	}
	os.RemoveAll(part)
	return err
}

func isClone(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// partOf is the part folder of the run with pid. Its "+" is in no repo name, so it is never a clone.
func partOf(dir string, pid int) string {
	return fmt.Sprintf("%s+part-%d", dir, pid)
}

// sweepParts removes the part folders of dir that a crashed run left: its process is gone, and the
// folder is over an hour old, in case another machine shares the folder.
func sweepParts(dir string) {
	parent, prefix := filepath.Dir(dir), filepath.Base(dir)+"+part-"
	entries, _ := os.ReadDir(parent)
	for _, e := range entries {
		rest, ok := strings.CutPrefix(e.Name(), prefix)
		pid, err := strconv.Atoi(rest)
		if !ok || err != nil || alive(pid) {
			continue
		}
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > time.Hour {
			os.RemoveAll(filepath.Join(parent, e.Name()))
		}
	}
}
