package git

import (
	"context"
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

// Clone clones host/path into dir with gh or glab, with the user's own login and a normal
// checkout; the tool talks to the terminal. host may name a port, which only glab takes. It clones into dir.part-<pid> and renames that, so a
// stopped clone never leaves a half repo at dir. When dir holds a clone already, it does nothing.
func Clone(ctx context.Context, kind, host, path, dir string, in io.Reader, out, errOut io.Writer) error {
	if isClone(dir) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	sweepParts(dir)
	part := fmt.Sprintf("%s.part-%d", dir, os.Getpid())
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

// sweepParts removes the part folders of dir that a crashed run left. Age tells them apart: a run
// that still clones made its part folder within the hour.
func sweepParts(dir string) {
	parent, prefix := filepath.Dir(dir), filepath.Base(dir)+".part-"
	entries, _ := os.ReadDir(parent)
	for _, e := range entries {
		pid, ok := strings.CutPrefix(e.Name(), prefix)
		if _, err := strconv.Atoi(pid); !ok || err != nil {
			continue
		}
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > time.Hour {
			os.RemoveAll(filepath.Join(parent, e.Name()))
		}
	}
}
