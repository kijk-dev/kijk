package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// bootstrapScript finds or starts the remote kijk of one version, in one round trip. It is POSIX
// sh on stdin, so the remote login shell does not matter. parseBoot reads what it prints.
const bootstrapScript = `set -u
v='%s'
os=$(uname -s); cpu=$(uname -m)
echo "system $os $cpu"
case $os in
Darwin) cfg="$HOME/Library/Application Support/kijk" ;;
*) cfg="${XDG_CONFIG_HOME:-$HOME/.config}/kijk" ;;
esac
state="$cfg/serve-$v.json"
dir="$HOME/.local/share/kijk/remote"
field() { sed -n 's/.*"'"$1"'":"\{0,1\}\([^",}]*\).*/\1/p' "${2:-$state}" 2>/dev/null; }
running() {
  [ -n "$1" ] && kill -0 "$1" 2>/dev/null || return 1
  { tr '\0' ' ' < "/proc/$1/cmdline" || ps -p "$1" -o args=; } 2>/dev/null | grep -q 'kijk.* serve'
}
if running "$(field pid)"; then printf 'serve %%s\n' "$(cat "$state")"; exit 0; fi
if [ "$v" = dev ]; then
  bin=$(command -v kijk 2>/dev/null) || bin="$HOME/.local/bin/kijk"
  [ -x "$bin" ] || { echo no-kijk; exit 0; }
else
  bin="$dir/$v/kijk"
  [ -x "$bin" ] || { echo need-install; exit 0; }
fi
mkdir -p "$cfg"
nohup "$bin" serve "$v" </dev/null >"$cfg/serve-$v.log" 2>&1 &
i=0
while [ $i -lt 100 ]; do
  if running "$(field pid)"; then
    if [ "$v" != dev ]; then
      # Keep a version a remote kijk runs from, or that another local kijk copied a moment ago.
      for d in "$dir"/*/; do
        w=$(basename "$d")
        [ -d "$d" ] && [ "$w" != "$v" ] || continue
        running "$(field pid "$cfg/serve-$w.json")" && continue
        recent=$(find "$d" -mmin -10 2>/dev/null) || continue
        [ -z "$recent" ] && rm -rf "$d"
      done
    fi
    printf 'serve %%s\n' "$(cat "$state")"
    exit 0
  fi
  sleep 0.1; i=$((i+1))
done
echo start-failed
tail -n 5 "$cfg/serve-$v.log" 2>/dev/null
`

// bootResult is what the bootstrap script reported.
type bootResult struct {
	system string // as uname -sm says, such as "Linux x86_64"
	goos   string
	goarch string
	serve  serveState
	need   string // "", "need-install", "no-kijk" or "start-failed"
	log    string // the end of the serve log after start-failed
}

func parseBoot(out string) (bootResult, error) {
	var b bootResult
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	for i, line := range lines {
		word, rest, _ := strings.Cut(line, " ")
		switch word {
		case "system":
			os, cpu, _ := strings.Cut(rest, " ")
			b.system = rest
			b.goos, b.goarch = goPlatform(os, cpu)
		case "serve":
			if err := json.Unmarshal([]byte(rest), &b.serve); err != nil || b.serve.Port == 0 || b.serve.Token == "" {
				return b, &remoteError{fmt.Sprintf("kijk serve wrote a state file kijk cannot read: %q", rest)}
			}
			return b, nil
		case "need-install", "no-kijk":
			b.need = word
			return b, nil
		case "start-failed":
			b.need = word
			b.log = strings.TrimSpace(strings.Join(lines[i+1:], "\n"))
			return b, nil
		}
	}
	return b, &remoteError{fmt.Sprintf("unexpected answer from the remote machine: %q", lastLines(out, 3))}
}

// goPlatform maps uname -s and -m to Go's names, or "" for a system kijk has no release for.
func goPlatform(os, cpu string) (string, string) {
	var goos, goarch string
	switch os {
	case "Linux":
		goos = "linux"
	case "Darwin":
		goos = "darwin"
	}
	switch cpu {
	case "x86_64", "amd64":
		goarch = "amd64"
	case "aarch64", "arm64":
		goarch = "arm64"
	}
	if goos == "" || goarch == "" {
		return "", ""
	}
	return goos, goarch
}

// bootstrap runs the script on the remote machine. version is "dev" for a build from source.
func bootstrap(ctx context.Context, s *sshClient, version string) (bootResult, error) {
	out, err := s.run(ctx, strings.NewReader(fmt.Sprintf(bootstrapScript, version)), "sh", "-s")
	if err != nil {
		return bootResult{}, err
	}
	return parseBoot(out)
}

// installError is a failed install, shown as the install-failed state.
type installError struct{ err error }

func (e *installError) Error() string { return e.err.Error() }
func (e *installError) Unwrap() error { return e.err }

// installRemote copies release v for goos/goarch into the private folder for v on the remote
// machine. The download is cached here, so a second machine of the same kind needs no new one.
func installRemote(ctx context.Context, s *sshClient, v, goos, goarch string) error {
	bin, err := cachedRelease(v, goos, goarch)
	if err != nil {
		return &installError{err}
	}
	f, err := os.Open(bin)
	if err != nil {
		return &installError{err}
	}
	defer f.Close()
	// cat, not scp or sftp: the remote only needs a shell. The temp name has the shell's pid, so
	// two installs at once, such as from two local kijks, do not write into one file.
	script := fmt.Sprintf(`umask 077; d="$HOME/.local/share/kijk/remote/%s"; t="$d/kijk.tmp.$$"; trap "rm -f \"$t\"" EXIT; mkdir -p "$d" && cat > "$t" && chmod 755 "$t" && mv "$t" "$d/kijk"`, v)
	if _, err := s.run(ctx, f, "sh", "-c", "'"+script+"'"); err != nil {
		if x, ok := errors.AsType[*sshError](err); ok && x.exit == 255 {
			return err // ssh itself failed, so the machine is unreachable, not the install broken
		}
		return &installError{fmt.Errorf("could not copy kijk to the remote machine: %w", err)}
	}
	return nil
}

var cacheDir = os.UserCacheDir

// cachedRelease gives the path of release v for goos/goarch, downloaded and checked once.
func cachedRelease(v, goos, goarch string) (string, error) {
	dir, err := cacheDir()
	if err != nil {
		return "", err
	}
	dir = filepath.Join(dir, "kijk", "remote", v)
	p := filepath.Join(dir, assetName(goos, goarch))
	if st, err := os.Stat(p); err == nil && st.Size() > 0 {
		return p, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, ".download-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	err = downloadRelease(v, goos, goarch, f)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", err
	}
	return p, os.Rename(f.Name(), p)
}
