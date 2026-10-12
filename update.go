package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

var errDev = errors.New("this kijk is a build from source, so it cannot update itself. Install a release with the install script (https://github.com/kijk-dev/kijk#install), or rebuild from source")

// selfPath is the file this kijk runs from. A symbolic link resolves to its target, so an update
// replaces the target and keeps the link.
func selfPath() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(p)
}

// installLatest replaces exe with the newest release when that is newer than current.
// It returns the version it installed, or "" when current is already the newest.
func installLatest(current, exe string) (string, error) {
	if !isRelease(current) {
		return "", errDev
	}
	v, err := latestVersion()
	if err != nil {
		return "", err
	}
	if !newer(v, current) {
		return "", nil
	}
	return v, installVersion(v, exe)
}

// installVersion downloads release v next to exe, checks it, and swaps it in. On any failure exe
// is left as it was.
func installVersion(v, exe string) error {
	dir := filepath.Dir(exe)
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	f, err := os.CreateTemp(dir, ".kijk-update-*"+ext)
	if errors.Is(err, fs.ErrPermission) {
		return fmt.Errorf("no write access to %s", dir)
	}
	if err != nil {
		return fmt.Errorf("cannot write to %s: %w", dir, err)
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	err = downloadRelease(v, runtime.GOOS, runtime.GOARCH, f)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		return err
	}
	if got := binaryVersion(tmp); got != v {
		return fmt.Errorf("the downloaded kijk says it is %q, not %s", got, v)
	}
	return replaceExe(tmp, exe)
}

// binaryVersion asks the kijk at path for its version, or "" when it does not answer like one.
func binaryVersion(path string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "version").Output()
	v, ok := strings.CutPrefix(strings.TrimSpace(string(out)), "kijk ")
	if err != nil || !ok {
		return ""
	}
	return v
}

// replaceExe moves tmp over exe. Windows refuses to overwrite a running program but lets it be
// renamed, so the old one steps aside as exe.old.
func replaceExe(tmp, exe string) error {
	if runtime.GOOS != "windows" {
		return os.Rename(tmp, exe)
	}
	old := exe + ".old"
	os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		return err
	}
	if err := os.Rename(tmp, exe); err != nil {
		if rerr := os.Rename(old, exe); rerr != nil {
			return fmt.Errorf("%v; kijk is left at %s", err, old)
		}
		return err
	}
	return nil
}

// removeOldExe deletes what a Windows update left behind. A kijk that still runs from it keeps
// it locked, so a later start tries again.
func removeOldExe() {
	if runtime.GOOS != "windows" {
		return
	}
	if exe, err := selfPath(); err == nil {
		os.Remove(exe + ".old")
	}
}

// runUpdate is the kijk update command.
func runUpdate(out io.Writer) error {
	if !isRelease(kijkVersion) {
		return errDev
	}
	exe, err := selfPath()
	if err != nil {
		return err
	}
	v, err := installLatest(kijkVersion, exe)
	if err != nil {
		return err
	}
	if v == "" {
		fmt.Fprintf(out, "kijk %s is up to date\n", kijkVersion)
		return nil
	}
	fmt.Fprintf(out, "kijk %s → %s\n", kijkVersion, v)
	if len(runningPorts()) > 0 {
		fmt.Fprintf(out, "kijk %s is installed. Restart kijk to use it.\n", v)
	}
	return nil
}
