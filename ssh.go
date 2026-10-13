package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// sshClient runs the user's ssh for one remote machine. Every command shares one ControlMaster
// login, so only the first one pays for the SSH handshake.
type sshClient struct {
	target string
	ctl    string   // the ControlPath socket
	extra  []string // options before the target, such as -F for tests

	mu    sync.Mutex
	conns map[*pipeConn]struct{}
}

// sshDirName names the folder in /tmp that holds the login sockets. Tests use their own, so
// they never close a running kijk's logins.
var sshDirName = fmt.Sprintf("kijk-%d", os.Getuid())

// sshDir holds the login sockets. macOS limits a socket path to 104 bytes, so $TMPDIR is too long.
func sshDir() string {
	return filepath.Join("/tmp", sshDirName)
}

// The socket is named after the target, not ssh's %C, so kijk can see a login open without ssh.
func newSSHClient(target string, extra ...string) *sshClient {
	h := sha256.Sum256([]byte(target))
	return &sshClient{target: target, ctl: filepath.Join(sshDir(), hex.EncodeToString(h[:8])), extra: extra, conns: map[*pipeConn]struct{}{}}
}

func (s *sshClient) opts(batch bool) []string {
	a := append([]string{}, s.extra...)
	if batch {
		a = append(a, "-o", "BatchMode=yes")
	}
	// A RemoteCommand or RequestTTY in the user's config would refuse or garble kijk's commands.
	a = append(a, "-T", "-o", "RemoteCommand=none")
	// ServerAlive also ends a silent -W stream about 40 s after the link dies; follow relies on it.
	return append(a, "-o", "ControlMaster=auto", "-o", "ControlPersist=10m", "-o", "ControlPath="+s.ctl,
		"-o", "ConnectTimeout=10", "-o", "ServerAliveInterval=10", "-o", "ServerAliveCountMax=3")
}

// command builds a background ssh command, which fails instead of asking for anything.
func (s *sshClient) command(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "ssh", append(s.opts(true), args...)...)
	cmd.WaitDelay = 2 * time.Second // a master forked by ControlMaster=auto may hold stderr open
	return cmd
}

func ensureSSHDir() error { return privateDir("/tmp", sshDir(), os.Getuid(), 0) }

// privateDir makes dir in base, or accepts it only when no other user can have put a socket in
// it: ssh's ControlMaster=auto would use that socket. me and root are uids, for tests.
func privateDir(base, dir string, me, root int) error {
	st, err := os.Stat(base) // /tmp is a link to /private/tmp on macOS
	if err != nil {
		return err
	}
	if st.Mode()&fs.ModeSticky == 0 {
		return &remoteError{fmt.Sprintf("kijk will not put its SSH logins in %s: it has no sticky bit, so other users could replace them", base)}
	}
	if fileOwner(st) != root {
		return &remoteError{fmt.Sprintf("kijk will not put its SSH logins in %s: it is not owned by root", base)}
	}
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	st, err = os.Lstat(dir)
	if err != nil {
		return err
	}
	why := ""
	switch {
	case st.Mode()&fs.ModeSymlink != 0:
		why = "it is a symlink"
	case !st.IsDir():
		why = "it is not a folder"
	case fileOwner(st) != me:
		why = "it belongs to another user"
	case st.Mode().Perm() != 0o700:
		why = fmt.Sprintf("other users can open it (mode %o)", st.Mode().Perm())
	default:
		return nil
	}
	return &remoteError{fmt.Sprintf("kijk will not use %s for its SSH logins: %s. Remove it (this may need sudo) and kijk tries again.", dir, why)}
}

// sshError is a failed ssh command, with the last lines ssh wrote to stderr.
type sshError struct {
	exit   int
	stderr string
}

func (e *sshError) Error() string {
	if e.stderr != "" {
		return e.stderr
	}
	return fmt.Sprintf("ssh exited with status %d", e.exit)
}

func runErr(err error, stderr []byte) error {
	if err == nil {
		return nil
	}
	code := -1
	if x, ok := errors.AsType[*exec.ExitError](err); ok {
		code = x.ExitCode()
	}
	if code == -1 && len(stderr) == 0 {
		return err
	}
	return &sshError{code, lastLines(string(stderr), 3)}
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n")), "\n")
	return strings.TrimSpace(strings.Join(lines[max(0, len(lines)-n):], "\n"))
}

// loggedIn reports whether the shared login is open, without connecting.
func (s *sshClient) loggedIn(ctx context.Context) bool {
	if _, err := os.Stat(s.ctl); err != nil {
		return false
	}
	return s.command(ctx, "-O", "check", s.target).Run() == nil
}

// login opens the shared login in the background, unless one is open.
func (s *sshClient) login(ctx context.Context) error {
	if err := ensureSSHDir(); err != nil {
		return err
	}
	if s.loggedIn(ctx) {
		return nil
	}
	var stderr bytes.Buffer
	cmd := s.command(ctx, "-M", "-N", "-f", s.target)
	cmd.Stderr = &stderr
	return runErr(cmd.Run(), stderr.Bytes())
}

// loginInTerminal opens the shared login with ssh's prompts on the terminal: passphrase,
// password and host key questions go from the user to ssh, never through kijk.
func (s *sshClient) loginInTerminal() error {
	if err := ensureSSHDir(); err != nil {
		return err
	}
	if s.loggedIn(context.Background()) {
		return nil
	}
	cmd := exec.Command("ssh", append(s.opts(false), "-M", "-N", "-f", s.target)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	if !s.loggedIn(context.Background()) {
		return errors.New("ssh ended without an open login")
	}
	return nil
}

// logout closes the shared login, which also ends every pipe that uses it.
func (s *sshClient) logout() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := os.Stat(s.ctl); err == nil {
		s.command(ctx, "-O", "exit", s.target).Run()
	}
}

// run runs a command on the remote machine over the shared login, with stdin as its input.
func (s *sshClient) run(ctx context.Context, stdin io.Reader, remoteCmd ...string) (string, error) {
	var out, stderr bytes.Buffer
	cmd := s.command(ctx, append([]string{s.target}, remoteCmd...)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, &out, &stderr
	err := runErr(cmd.Run(), stderr.Bytes())
	// ssh's own failures exit with 255; any other code is the remote command's.
	if x, ok := errors.AsType[*sshError](err); ok && x.exit != 255 {
		msg := x.stderr
		if msg == "" {
			msg = lastLines(out.String(), 3)
		}
		err = &remoteError{fmt.Sprintf("The shell on %s failed (exit %d): %s", s.target, x.exit, msg)}
	}
	return out.String(), err
}

// sshLogin is who ssh logs in as and where, as ssh -G resolves a target.
type sshLogin struct{ user, host, port, jump string }

func parseSSHConfig(out string) sshLogin {
	var l sshLogin
	for _, line := range strings.Split(out, "\n") {
		k, v, _ := strings.Cut(strings.TrimSpace(line), " ")
		switch k = strings.ToLower(k); k {
		case "user":
			l.user = v
		case "hostname":
			l.host = v
		case "port":
			l.port = v
		case "proxyjump", "proxycommand":
			if v != "none" {
				l.jump = strings.TrimSpace(l.jump + " " + k + " " + v)
			}
		}
	}
	return l
}

// resolve asks ssh how it would log in to the target, without connecting.
func (s *sshClient) resolve(ctx context.Context) (sshLogin, error) {
	var stderr bytes.Buffer
	cmd := s.command(ctx, "-G", s.target)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err := runErr(err, stderr.Bytes()); err != nil {
		return sshLogin{}, err
	}
	l := parseSSHConfig(string(out))
	if l.host == "" {
		return sshLogin{}, errors.New("ssh -G printed no host name")
	}
	return l, nil
}

// transport is an HTTP transport whose every connection is one ssh -W pipe to addr on the remote.
func (s *sshClient) transport(addr string) *http.Transport {
	return &http.Transport{
		DialContext:         func(ctx context.Context, _, _ string) (net.Conn, error) { return s.dial(addr) },
		MaxIdleConnsPerHost: 64, // Go's default of 2 starts a new ssh for most requests of a burst
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  true,
	}
}

// dial starts ssh -W addr and returns its stdin and stdout as a net.Conn.
func (s *sshClient) dial(addr string) (net.Conn, error) {
	inR, inW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		inR.Close()
		inW.Close()
		return nil, err
	}
	// Own pipes, not cmd.StdoutPipe: cmd.Wait would close stdout before the last bytes are read.
	cmd := exec.Command("ssh", append(s.opts(true), "-W", addr, s.target)...)
	cmd.Stdin, cmd.Stdout = inR, outW
	c := &pipeConn{in: inW, out: outR, done: make(chan struct{}), owner: s}
	cmd.Stderr = &c.stderr
	if err := cmd.Start(); err != nil {
		inR.Close()
		inW.Close()
		outR.Close()
		outW.Close()
		return nil, err
	}
	inR.Close()
	outW.Close()
	c.proc = cmd.Process
	s.mu.Lock()
	s.conns[c] = struct{}{}
	s.mu.Unlock()
	go func() {
		cmd.Wait()
		s.mu.Lock()
		delete(s.conns, c)
		s.mu.Unlock()
		close(c.done)
	}()
	return c, nil
}

// closeAll ends every open pipe of this machine, such as an event stream.
func (s *sshClient) closeAll() {
	s.mu.Lock()
	conns := make([]*pipeConn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.mu.Unlock()
	for _, c := range conns {
		c.Close()
	}
}

// stderrTail keeps the first 4 KB of what ssh writes to stderr.
type stderrTail struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *stderrTail) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.b.Len() < 4096 {
		l.b.Write(p)
	}
	return len(p), nil
}

func (l *stderrTail) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return lastLines(l.b.String(), 3)
}

type pipeConn struct {
	in, out *os.File
	proc    *os.Process
	stderr  stderrTail
	done    chan struct{}
	once    sync.Once
	owner   *sshClient
}

// Read turns the end of the pipe into ssh's own message, so a failure is not a bare EOF.
func (c *pipeConn) Read(p []byte) (int, error) {
	n, err := c.out.Read(p)
	if err != nil && n == 0 {
		select {
		case <-c.done:
		case <-time.After(2 * time.Second):
		}
		if msg := c.stderr.String(); msg != "" {
			return 0, &sshError{255, msg}
		}
	}
	return n, err
}

func (c *pipeConn) Write(p []byte) (int, error) { return c.in.Write(p) }

func (c *pipeConn) Close() error {
	c.once.Do(func() {
		c.in.Close()
		c.out.Close()
		go func() {
			select {
			case <-c.done:
			case <-time.After(2 * time.Second):
				c.proc.Kill()
			}
		}()
	})
	return nil
}

func (c *pipeConn) LocalAddr() net.Addr  { return pipeAddr{} }
func (c *pipeConn) RemoteAddr() net.Addr { return pipeAddr{} }
func (c *pipeConn) SetDeadline(t time.Time) error {
	c.in.SetWriteDeadline(t)
	return c.out.SetReadDeadline(t)
}
func (c *pipeConn) SetReadDeadline(t time.Time) error  { return c.out.SetReadDeadline(t) }
func (c *pipeConn) SetWriteDeadline(t time.Time) error { return c.in.SetWriteDeadline(t) }

type pipeAddr struct{}

func (pipeAddr) Network() string { return "ssh-pipe" }
func (pipeAddr) String() string  { return "ssh-pipe" }

// Machine states, as the UI and kijk machine list show them.
const (
	stateDisabled      = "disabled"
	stateConnecting    = "connecting"
	stateConnected     = "connected"
	stateNeedsLogin    = "needs-login"
	stateUnreachable   = "unreachable"
	stateError         = "error"
	stateInstallFailed = "install-failed"
)

// classify turns an ssh failure into a machine state and a message for the user.
func classify(target string, err error) (state, message string) {
	msg := err.Error()
	if x, ok := errors.AsType[*sshError](err); ok {
		msg = x.stderr
	}
	switch {
	case isHostKeyChanged(err):
		return stateError, hostKeyChanged(target, msg)
	case strings.Contains(msg, "Permission denied"), strings.Contains(msg, "Host key verification failed"),
		strings.Contains(msg, "Too many authentication failures"):
		return stateNeedsLogin, fmt.Sprintf("The login to %s needs you. Run kijk machine login %s", target, target)
	case strings.Contains(msg, "forwarding request failed"), strings.Contains(msg, "administratively prohibited"),
		strings.Contains(msg, "open failed"), strings.Contains(msg, "Session open refused"):
		return stateError, fmt.Sprintf("%s refused the connection to kijk. Either kijk is not running there, or its SSH server does not allow forwarding (AllowTcpForwarding).", target)
	}
	if msg == "" {
		msg = "no answer"
	}
	return stateUnreachable, fmt.Sprintf("%s is unreachable: %s", target, firstLine(msg))
}

// isHostKeyChanged tells a changed host key, which a new login cannot fix, from an unknown one,
// which kijk machine login lets ssh ask about.
func isHostKeyChanged(err error) bool {
	x, ok := errors.AsType[*sshError](err)
	return ok && strings.Contains(x.stderr, "has changed and you have requested strict checking")
}

// hostKeyChanged takes the host from ssh's own line, as known_hosts stores it, such as
// [127.0.0.1]:2222. macOS ssh does not print the ssh-keygen command itself.
func hostKeyChanged(target, msg string) string {
	s := fmt.Sprintf("The host key of %s has changed. The machine may have been reinstalled, or someone may be intercepting the connection. Check with: ssh %s", target, target)
	for line := range strings.Lines(msg) {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "Host key for ")
		if host, _, ok2 := strings.Cut(rest, " has changed"); ok && ok2 && host != "" && !strings.ContainsAny(host, "' ") {
			return s + fmt.Sprintf(". If the change is expected, remove the old key with: ssh-keygen -R '%s', then run kijk machine login %s", host, target)
		}
	}
	return s
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(s)
}
