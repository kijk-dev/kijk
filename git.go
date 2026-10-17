package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

var errNoGit = errors.New("git was not found")

func gitCmd(ctx context.Context, dir string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir, "-c", "core.quotePath=false"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
	return cmd
}

func git(dir string, args ...string) (string, error) {
	out, err := gitRaw(dir, args...)
	return strings.TrimSpace(string(out)), err
}

// gitRaw returns stdout; on failure the error carries git's stderr.
func gitRaw(dir string, args ...string) ([]byte, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return nil, errNoGit
	}
	cmd := gitCmd(context.Background(), dir, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return out, errors.New(msg)
		}
		return out, err
	}
	return out, nil
}

// gitOutput runs a git command that changes the repository and returns its combined output,
// because hooks write their errors to stdout as well as stderr.
func gitOutput(dir, stdin string, args ...string) (string, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return "", errNoGit
	}
	cmd := gitCmd(context.Background(), dir, args...)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func checkPaths(paths []string) error {
	if len(paths) == 0 {
		return errors.New("no files given")
	}
	for _, p := range paths {
		if !filepath.IsLocal(filepath.FromSlash(p)) {
			return errOutside
		}
	}
	return nil
}

func stage(root string, paths []string) (string, error) {
	if err := checkPaths(paths); err != nil {
		return "", err
	}
	if out, _ := git(root, append([]string{"ls-files", "-u", "--"}, paths...)...); out != "" {
		return "", errors.New("files with a merge conflict cannot be staged here")
	}
	return gitOutput(root, "", append([]string{"add", "--"}, paths...)...)
}

func unstage(root string, paths []string) (string, error) {
	if err := checkPaths(paths); err != nil {
		return "", err
	}
	if _, err := git(root, "rev-parse", "--verify", "-q", "HEAD"); err != nil { // no commit yet
		return gitOutput(root, "", append([]string{"rm", "--cached", "-q", "--"}, paths...)...)
	}
	return gitOutput(root, "", append([]string{"restore", "--staged", "--"}, paths...)...)
}

func commit(root, message string) (string, error) {
	if strings.TrimSpace(message) == "" {
		return "", errors.New("the commit message is empty")
	}
	return gitOutput(root, message, "commit", "-F", "-")
}

type change struct {
	Path   string `json:"path"`
	Orig   string `json:"orig,omitempty"`
	Status string `json:"status"` // git's letter: M A D R C T U ?
	Add    int    `json:"add,omitempty"`
	Del    int    `json:"del,omitempty"`
}

type gitStatus struct {
	Git       bool   `json:"git"`
	Error     string `json:"error,omitempty"`
	Branch    string `json:"branch"`             // empty when HEAD is detached
	Upstream  string `json:"upstream,omitempty"` // e.g. origin/main
	Base      string `json:"base,omitempty"`     // what the ahead count is counted against
	Ahead     int    `json:"ahead"`
	Behind    int    `json:"behind"`              // commits on the upstream not in HEAD
	Remote    bool   `json:"remote"`              // an "origin" remote exists, so the branch can be published
	FetchedAt int64  `json:"fetchedAt,omitempty"` // unix ms of the last fetch

	UpHash string `json:"upHash,omitempty"` // where the upstream is, so the UI notices a push

	Main          string   `json:"main,omitempty"` // compared with: the PR target, else the main line; empty when that is the upstream or this branch
	MainBehind    int      `json:"mainBehind"`
	MainConflicts []string `json:"mainConflicts,omitempty"` // files a rebase onto Main would likely conflict in
	UpConflicts   []string `json:"upConflicts,omitempty"`   // same, for a pull when HEAD and upstream diverged

	Staged    []change `json:"staged"`
	Changes   []change `json:"changes"`
	Untracked []change `json:"untracked"`
	Conflicts []change `json:"conflicts"`
	Ignored   []string `json:"ignored"` // folders end with "/"

	Total *branchTotal `json:"total,omitempty"`
}

func status(root string) gitStatus { return statusFor(root, "") }

// statusFor compares with target, the branch a pull request merges into, when it is given.
func statusFor(root, target string) gitStatus {
	st := gitStatus{Staged: []change{}, Changes: []change{}, Untracked: []change{}, Conflicts: []change{}, Ignored: []string{}}
	finishTotal := startTotal(root, target)
	out, err := gitRaw(root, "status", "--porcelain=v2", "-z", "--branch", "-uall")
	if err != nil {
		finishTotal("", nil)
		if !strings.Contains(err.Error(), "not a git repository") {
			st.Error = err.Error()
		}
		return st
	}
	st.Git = true
	parseStatus(out, &st)
	var wg sync.WaitGroup
	var unstagedN, stagedN map[string]lineCount
	wg.Add(2)
	go func() { defer wg.Done(); unstagedN = numstatOf(root, st.Changes) }()
	go func() { defer wg.Done(); stagedN = numstatOf(root, st.Staged, "--cached", "-M") }()
	for i, c := range st.Untracked {
		if !strings.HasSuffix(c.Path, "/") {
			st.Untracked[i].Add = countLines(filepath.Join(root, filepath.FromSlash(c.Path)))
		}
	}
	st.Remote = hasOrigin(root)
	st.Base = st.Upstream
	if st.Base == "" && st.Branch != "" {
		st.Base = defaultBase(root)
		if st.Base != "" {
			n, _ := git(root, "rev-list", "--count", st.Base+"..HEAD")
			st.Ahead, _ = strconv.Atoi(n)
		}
	}
	m := mainRef(root)
	if t := targetRef(root, target); t != "" {
		m = t
	}
	if m != "" && m != st.Upstream && m != st.Branch {
		st.Main = m
		var ahead int
		ahead, st.MainBehind = aheadBehind(root, m)
		if ahead > 0 && st.MainBehind > 0 {
			st.MainConflicts = conflictsWith(root, m)
		}
	}
	if st.Upstream != "" && st.Ahead > 0 && st.Behind > 0 {
		st.UpConflicts = conflictsWith(root, st.Upstream)
	}
	if st.Upstream != "" {
		st.UpHash, _ = git(root, "rev-parse", "--verify", "-q", "@{upstream}")
	}
	if p, err := git(root, "rev-parse", "--git-path", "FETCH_HEAD"); err == nil {
		if !filepath.IsAbs(p) {
			p = filepath.Join(root, p)
		}
		if fi, err := os.Stat(p); err == nil {
			st.FetchedAt = fi.ModTime().UnixMilli()
		}
	}
	if ign, err := gitRaw(root, "ls-files", "-z", "--others", "--ignored", "--exclude-standard", "--directory"); err == nil {
		for _, p := range strings.Split(string(ign), "\x00") {
			if p != "" {
				st.Ignored = append(st.Ignored, p)
			}
		}
	}
	wg.Wait()
	setCounts(st.Changes, unstagedN)
	setCounts(st.Staged, stagedN)
	st.Total = finishTotal(st.Branch, st.Untracked)
	return st
}

func parseStatus(out []byte, st *gitStatus) {
	fields := strings.Split(string(out), "\x00")
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if len(f) < 2 {
			continue
		}
		switch f[0] {
		case '#':
			switch {
			case strings.HasPrefix(f, "# branch.head "):
				if b := strings.TrimPrefix(f, "# branch.head "); b != "(detached)" {
					st.Branch = b
				}
			case strings.HasPrefix(f, "# branch.upstream "):
				st.Upstream = strings.TrimPrefix(f, "# branch.upstream ")
			case strings.HasPrefix(f, "# branch.ab "):
				fmt.Sscanf(f, "# branch.ab +%d -%d", &st.Ahead, &st.Behind)
			}
		case '1', '2':
			n := 9 // "1 XY sub mH mI mW hH hI path"
			if f[0] == '2' {
				n = 10 // adds the rename score; the original path follows as its own field
			}
			parts := strings.SplitN(f, " ", n)
			if len(parts) < n {
				continue
			}
			xy, path := parts[1], parts[n-1]
			orig := ""
			if f[0] == '2' && i+1 < len(fields) {
				i++
				orig = fields[i]
			}
			if xy[0] != '.' {
				st.Staged = append(st.Staged, change{Path: path, Orig: orig, Status: xy[:1]})
			}
			if xy[1] != '.' {
				st.Changes = append(st.Changes, change{Path: path, Status: xy[1:]})
			}
		case 'u':
			if parts := strings.SplitN(f, " ", 11); len(parts) == 11 {
				st.Conflicts = append(st.Conflicts, change{Path: parts[10], Status: "U"})
			}
		case '?':
			st.Untracked = append(st.Untracked, change{Path: f[2:], Status: "?"})
		}
	}
}

func hasOrigin(root string) bool {
	_, err := git(root, "remote", "get-url", "origin")
	return err == nil
}

// defaultBase is the branch the ahead count is counted against when there is no upstream.
func defaultBase(root string) string {
	for _, ref := range []string{"origin/HEAD", "origin/main", "origin/master"} {
		if _, err := git(root, "rev-parse", "--verify", "-q", ref); err == nil {
			return ref
		}
	}
	return ""
}

// mainRef is the main line to compare with: origin's default branch, else a local main or master.
func mainRef(root string) string {
	switch b := defaultBase(root); b {
	case "":
	case "origin/HEAD":
		if name, err := git(root, "rev-parse", "--abbrev-ref", b); err == nil {
			return name
		}
		return b
	default:
		return b
	}
	for _, ref := range []string{"main", "master"} {
		if _, err := git(root, "rev-parse", "--verify", "-q", "refs/heads/"+ref); err == nil {
			return ref
		}
	}
	return ""
}

var branchNameRe = regexp.MustCompile(`^[A-Za-z0-9._][A-Za-z0-9._/-]*$`)

// targetRef turns a pull request's target branch into a ref, preferring origin's copy.
func targetRef(root, name string) string {
	if !branchNameRe.MatchString(name) || strings.Contains(name, "..") {
		return ""
	}
	if _, err := git(root, "rev-parse", "--verify", "-q", "refs/remotes/origin/"+name); err == nil {
		return "origin/" + name
	}
	if _, err := git(root, "rev-parse", "--verify", "-q", "refs/heads/"+name); err == nil {
		return name
	}
	return ""
}

func aheadBehind(root, ref string) (int, int) {
	var ahead, behind int
	out, _ := git(root, "rev-list", "--left-right", "--count", "HEAD..."+ref, "--")
	fmt.Sscanf(out, "%d %d", &ahead, &behind)
	return ahead, behind
}

// The cache also stops a loop: merge-tree writes objects under .git, which fires a folder
// event, which asks for the status again.
var conflictCache = struct {
	sync.Mutex
	m map[string][]string
}{m: map[string][]string{}}

// conflictsWith lists the files a merge of ref into HEAD would conflict in, without touching
// the work tree. A rebase replays one commit at a time, so it can differ, but seldom does.
func conflictsWith(root, ref string) []string {
	key, err := git(root, "rev-parse", "HEAD", ref)
	if err != nil {
		return nil
	}
	conflictCache.Lock()
	files, ok := conflictCache.m[key]
	conflictCache.Unlock()
	if ok {
		return files
	}
	out, err := gitCmd(context.Background(), root, "merge-tree", "--write-tree", "--name-only", "--no-messages", "-z", "HEAD", ref).Output()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 { // the tree id comes first, then the files
		for _, f := range strings.Split(string(out), "\x00")[1:] {
			if f != "" {
				files = append(files, f)
			}
		}
	} else if err != nil {
		return nil // git before 2.38 has no --write-tree
	}
	conflictCache.Lock()
	if len(conflictCache.m) > 100 {
		clear(conflictCache.m)
	}
	conflictCache.m[key] = files
	conflictCache.Unlock()
	return files
}

type commitInfo struct {
	Hash    string   `json:"hash"`
	Short   string   `json:"short"`
	Subject string   `json:"subject"`
	Author  string   `json:"author"`
	Time    int64    `json:"time"` // unix seconds
	Refs    []string `json:"refs,omitempty"`
}

const logFields = "%H%x1f%h%x1f%s%x1f%an%x1f%at%x1f%D"

func parseCommit(rec string) (commitInfo, bool) {
	f := strings.Split(rec, "\x1f")
	if len(f) != 6 {
		return commitInfo{}, false
	}
	t, _ := strconv.ParseInt(f[4], 10, 64)
	c := commitInfo{Hash: f[0], Short: f[1], Subject: f[2], Author: f[3], Time: t}
	for _, r := range strings.Split(f[5], ", ") {
		if r != "" && !strings.HasSuffix(r, "/HEAD") {
			c.Refs = append(c.Refs, r)
		}
	}
	return c, true
}

type graphRow struct {
	Graph  string      `json:"g"`           // git's text graph for this row, e.g. "| * "
	Commit *commitInfo `json:"c,omitempty"` // empty on rows that only join lanes
	Lane   int         `json:"l,omitempty"` // the branch a commit is on, for its colour: 0 this one, 1 the PR target, 2 main
}

type graphResult struct {
	Rows []graphRow `json:"rows"`
	Fork string     `json:"fork,omitempty"` // where HEAD, its upstream and main split
}

// graph shows HEAD, its upstream, main and the PR target from where they split, plus some older
// commits. git lays out the lanes; the UI draws its text graph as lines.
func graph(root, target string) (graphResult, error) {
	res := graphResult{Rows: []graphRow{}}
	if _, err := git(root, "rev-parse", "--verify", "-q", "HEAD"); err != nil { // no commit yet
		return res, nil
	}
	current := []string{"HEAD"}
	if up, err := git(root, "rev-parse", "--abbrev-ref", "@{upstream}"); err == nil {
		current = append(current, up)
	}
	main := mainRef(root)
	tgt := targetRef(root, target)
	if tgt == main {
		tgt = ""
	}
	refs := current
	for _, r := range []string{tgt, main} {
		if r != "" {
			refs = append(refs, r)
		}
	}
	n := 50
	if len(refs) > 1 {
		if fork, err := git(root, append([]string{"merge-base", "--octopus"}, refs...)...); err == nil {
			res.Fork = fork
			since, _ := git(root, append(append([]string{"rev-list", "--count"}, refs...), "^"+fork, "--")...)
			k, _ := strconv.Atoi(since)
			n = min(300, max(n, k+10))
		}
	}
	// Each commit since the split takes the colour of the first branch that has it; older ones are this branch's.
	lane := map[string]int{}
	if res.Fork != "" {
		for _, l := range []struct {
			n    int
			refs []string
		}{{2, []string{main}}, {1, []string{tgt}}, {0, current}} {
			if l.refs[0] == "" {
				continue
			}
			out, _ := git(root, append(append([]string{"rev-list"}, l.refs...), "^"+res.Fork, "--")...)
			for _, h := range strings.Fields(out) {
				lane[h] = l.n
			}
		}
	}
	raw, err := gitRaw(root, append(append([]string{"log", "--graph", "--no-show-signature", "--max-count=" + strconv.Itoa(n), "--format=%x00" + logFields}, refs...), "--")...)
	if err != nil {
		return res, err
	}
	for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		g, rec, found := strings.Cut(line, "\x00")
		row := graphRow{Graph: strings.TrimRight(g, " ")}
		if c, ok := parseCommit(rec); found && ok {
			row.Commit = &c
			row.Lane = lane[c.Hash]
		}
		res.Rows = append(res.Rows, row)
	}
	return res, nil
}

var (
	hashRe     = regexp.MustCompile(`^[0-9a-f]{4,64}$`)
	errBadHash = errors.New("bad commit hash")
)

// commitFiles lists the files a commit changed, compared with its first parent.
func commitFiles(root, hash string) ([]change, error) {
	if !hashRe.MatchString(hash) {
		return nil, errBadHash
	}
	raw, err := gitRaw(root, "show", "-z", "--format=", "--name-status", "-M", "--first-parent", hash)
	if err != nil {
		return nil, err
	}
	out := []change{}
	f := strings.Split(strings.TrimPrefix(string(raw), "\n"), "\x00")
	for i := 0; i+1 < len(f); i++ {
		st := f[i]
		if st == "" {
			continue
		}
		c := change{Status: st[:1], Path: f[i+1]}
		i++
		if (st[0] == 'R' || st[0] == 'C') && i+1 < len(f) {
			c.Orig, c.Path = c.Path, f[i+1]
			i++
		}
		out = append(out, c)
	}
	if n, err := gitRaw(root, "show", "-z", "--format=", "--numstat", "-M", "--first-parent", hash); err == nil {
		setCounts(out, parseNumstat(n))
	}
	return out, nil
}

// push pushes the current branch, or publishes it to origin when it has no upstream.
// It never waits for a password: prompts are off and ssh runs in batch mode.
func push(root string) (string, error) {
	st := status(root)
	if st.Branch == "" {
		return "", errors.New("HEAD is not on a branch")
	}
	if st.Upstream == "" {
		return network(root, "push", "-u", "origin", "HEAD")
	}
	return network(root, "push")
}

// fetch updates the remote branches, so the behind counts and conflict warnings are current.
func fetch(root string) (string, error) {
	return network(root, "fetch")
}

// network runs a git command that talks to a remote.
func network(root string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := gitCmd(ctx, root, args...)
	if os.Getenv("GIT_SSH_COMMAND") == "" {
		if custom, _ := git(root, "config", "--get", "core.sshCommand"); custom == "" {
			cmd.Env = append(cmd.Env, "GIT_SSH_COMMAND=ssh -o BatchMode=yes")
		}
	}
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return string(out), fmt.Errorf("%s took longer than 2 minutes and was stopped", args[0])
	}
	return strings.TrimSpace(string(out)), err
}

type diffLine struct {
	Kind   string `json:"k"` // " ", "+" or "-"
	Old    int    `json:"o,omitempty"`
	New    int    `json:"n,omitempty"`
	Text   string `json:"t"`
	Tokens []tok  `json:"x,omitempty"`
}

type hunk struct {
	OldStart int        `json:"oldStart"`
	NewStart int        `json:"newStart"`
	Lines    []diffLine `json:"lines"`
}

type diffResult struct {
	Binary bool        `json:"binary,omitempty"`
	Folder bool        `json:"folder,omitempty"` // an untracked folder git does not look into, such as a nested repository
	Image  *imageSides `json:"image,omitempty"`
	Hunks  []hunk      `json:"hunks"`
}

// diff returns the change of one file. mode is "unstaged", "staged", "untracked" or "commit" (with hash).
func diff(root, path, orig, mode, hash string, context int) (diffResult, error) {
	if !filepath.IsLocal(filepath.FromSlash(path)) || (orig != "" && !filepath.IsLocal(filepath.FromSlash(orig))) {
		return diffResult{}, errOutside
	}
	if mode == "untracked" {
		return untrackedDiff(root, path)
	}
	args := []string{"diff", "--no-color", "--no-ext-diff", "-M", "-U" + strconv.Itoa(context)}
	switch mode {
	case "staged":
		args = append(args, "--cached")
	case "commit":
		if !hashRe.MatchString(hash) {
			return diffResult{}, errBadHash
		}
		args = []string{"show", "--format=", "--first-parent", "--no-color", "--no-ext-diff", "-M", "-U" + strconv.Itoa(context), hash}
	}
	args = append(args, "--")
	if orig != "" {
		args = append(args, orig)
	}
	out, err := gitRaw(root, append(args, path)...)
	if err != nil {
		return diffResult{}, err
	}
	return parseDiff(string(out)), nil
}

var hunkRe = regexp.MustCompile(`^@@ -(\d+)(?:,\d+)? \+(\d+)`)

type lineChange struct {
	Kind  string `json:"kind"`  // "add", "mod" or "del"
	Start int    `json:"start"` // first changed line of the file on disk; for "del", the line above the removed lines
	Count int    `json:"count"` // changed lines of the file on disk; 0 for "del"
}

var hunkCountRe = regexp.MustCompile(`(?m)^@@ -\d+(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// lineChanges compares the file on disk with HEAD, for the change marks beside the code.
func lineChanges(root, path string) ([]lineChange, error) {
	if !filepath.IsLocal(filepath.FromSlash(path)) {
		return nil, errOutside
	}
	out, err := gitRaw(root, "diff", "--no-color", "--no-ext-diff", "-U0", "HEAD", "--", path)
	if err != nil {
		return nil, err
	}
	changes := []lineChange{}
	count := func(s string) int { // git leaves out a count of 1
		if s == "" {
			return 1
		}
		n, _ := strconv.Atoi(s)
		return n
	}
	for _, m := range hunkCountRe.FindAllStringSubmatch(string(out), -1) {
		removed, start, added := count(m[1]), count(m[2]), count(m[3])
		c := lineChange{Kind: "mod", Start: start, Count: added}
		if removed == 0 {
			c.Kind = "add"
		} else if added == 0 {
			c.Kind = "del"
		}
		changes = append(changes, c)
	}
	return changes, nil
}

func parseDiff(out string) diffResult {
	res := diffResult{Hunks: []hunk{}}
	var h *hunk
	var o, n int
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "@@ "):
			m := hunkRe.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			o, _ = strconv.Atoi(m[1])
			n, _ = strconv.Atoi(m[2])
			res.Hunks = append(res.Hunks, hunk{OldStart: o, NewStart: n, Lines: []diffLine{}})
			h = &res.Hunks[len(res.Hunks)-1]
		case h == nil:
			if strings.HasPrefix(line, "Binary files ") {
				res.Binary = true
			}
		case strings.HasPrefix(line, "+"):
			h.Lines = append(h.Lines, diffLine{Kind: "+", New: n, Text: line[1:]})
			n++
		case strings.HasPrefix(line, "-"):
			h.Lines = append(h.Lines, diffLine{Kind: "-", Old: o, Text: line[1:]})
			o++
		case strings.HasPrefix(line, " "):
			h.Lines = append(h.Lines, diffLine{Kind: " ", Old: o, New: n, Text: line[1:]})
			o++
			n++
		}
	}
	return res
}

func untrackedDiff(root, path string) (diffResult, error) {
	p, err := resolve(root, path)
	if err != nil {
		return diffResult{}, err
	}
	st, err := os.Stat(p)
	if err != nil {
		return diffResult{}, err
	}
	if st.IsDir() {
		return diffResult{Folder: true, Hunks: []hunk{}}, nil
	}
	idx, err := buildIndex(p, st)
	if err != nil {
		return diffResult{}, err
	}
	if idx.Binary || idx.TooLarge {
		return diffResult{Binary: true, Hunks: []hunk{}}, nil
	}
	lines, err := readLines(p, idx, 0, idx.Lines)
	if err != nil {
		return diffResult{}, err
	}
	h := hunk{OldStart: 0, NewStart: 1, Lines: make([]diffLine, len(lines))}
	for i, l := range lines {
		h.Lines[i] = diffLine{Kind: "+", New: i + 1, Text: l}
	}
	return diffResult{Hunks: []hunk{h}}, nil
}
