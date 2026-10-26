package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kijk-dev/kijk/internal/gittest"
)

func TestDeliver(t *testing.T) {
	api := openAsk{Folders: []string{"/code/api"}}
	for _, c := range []struct {
		name        string
		ask         openAsk
		rep         openReply
		err         error
		code        int
		out, errOut string
		opened      []int // the requests a new window opened on
	}{
		{"no window, so one opens on the request", api, openReply{Said: "Added api.", Seq: 3}, nil,
			0, "Added api.\n", "", []int{3}},
		{"a window is open, so none opens", api, openReply{Said: "Added api. It is open in the kijk window.", Window: true, Seq: 4}, nil,
			0, "Added api. It is open in the kijk window.\n", "", nil},
		{"plain kijk", openAsk{}, openReply{}, nil,
			0, "", "", []int{0}},
		{"the list was not saved, but the folder opens", api, openReply{Said: "Added api.", Seq: 5, Error: "cannot save the folders list: disk full"}, nil,
			0, "Added api.\n", "kijk: cannot save the folders list: disk full\n", []int{5}},
		{"the running kijk refuses", api, openReply{}, errors.New("the running kijk is too old for this. Quit it, then try again"),
			1, "", "kijk: the running kijk is too old for this. Quit it, then try again\n", nil},
	} {
		var out, errOut bytes.Buffer
		var sent openAsk
		var opened []int
		code := deliver(func(a openAsk) (openReply, error) { sent = a; return c.rep, c.err }, c.ask,
			func(seq int) { opened = append(opened, seq) }, terminal{nil, &out, &errOut})
		if code != c.code || out.String() != c.out || errOut.String() != c.errOut || !slices.Equal(opened, c.opened) || jsonOf(sent) != jsonOf(c.ask) {
			t.Errorf("%s: exit %d, out %q, err %q, windows %v, sent %s", c.name, code, out.String(), errOut.String(), opened, jsonOf(sent))
		}
	}
	if code := deliver(func(openAsk) (openReply, error) { return openReply{Seq: 1}, nil }, openAsk{}, nil, terminal{nil, io.Discard, io.Discard}); code != 0 {
		t.Errorf("-no-open: exit %d", code)
	}
}

// When the kijk that shows it finds no clone, the command clones the repo with gh or glab, where
// the terminal is, and asks again with the clone.
func TestDeliverClones(t *testing.T) {
	isolatedGit(t)
	useCacheDir(t)
	cache, _ := cacheDir()
	src := gitRepo(t)
	write(t, filepath.Join(src, "README.md"), "# Billing\n")
	run(t, src, "add", ".")
	run(t, src, "commit", "-qm", "Start")
	billing := &prLink{Kind: "github", Host: "github.com", Path: "acme/billing", Number: 12}
	ledger := &prLink{Kind: "github", Host: "github.com", Path: "acme/ledger", Number: 7}
	api := &prLink{Kind: "gitlab", Host: "gitlab.example.org", Path: "team/api", Number: 3}
	dir := cloneDir(cache, *billing)
	searched := func(l *prLink) string {
		return "No clone of " + l.Host + "/" + l.Path + " in 3 folders on this machine.\n"
	}
	cloning := func(l *prLink) string { return "Cloning it with gh into " + tilde(cloneDir(cache, *l)) + " …\n" }
	for _, c := range []struct {
		name        string
		pr          *prLink
		gh          []gittest.Rule // nil: neither gh nor glab is installed
		code        int
		out, errOut string
		opened      []int
	}{
		{"cloned, then opened", billing, []gittest.Rule{{Has: "repo clone", Clone: src}},
			0, searched(billing) + cloning(billing) + "Added billing and opened #12.\n", "", []int{2}},
		{"the clone fails", ledger, []gittest.Rule{{Has: "repo clone", Err: "GraphQL: Could not resolve to a Repository\n", Exit: 1}},
			1, searched(ledger) + cloning(ledger), "GraphQL: Could not resolve to a Repository\nkijk: cannot clone github.com/acme/ledger: gh repo clone: exit status 1. Clone it, then run kijk <folder>.\n", nil},
		{"no glab", api, nil,
			1, searched(api), "kijk: glab is not installed, so kijk cannot clone gitlab.example.org/team/api. Clone it, then run this command again.\n", nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			if c.gh != nil {
				gittest.FakeTool(t, "gh", c.gh...)
			} else {
				t.Setenv("PATH", t.TempDir())
			}
			var asks []openAsk
			replies := []openReply{{NoClone: true, Said: strings.TrimSpace(searched(c.pr))}, {Said: "Added billing and opened #12.", Seq: 2}}
			open := func(a openAsk) (openReply, error) {
				asks = append(asks, a)
				return replies[len(asks)-1], nil
			}
			var out, errOut bytes.Buffer
			var opened []int
			code := deliver(open, openAsk{PR: c.pr, Cwd: "/code"}, func(seq int) { opened = append(opened, seq) }, terminal{nil, &out, &errOut})
			if code != c.code || out.String() != c.out || errOut.String() != c.errOut || !slices.Equal(opened, c.opened) {
				t.Fatalf("exit %d, windows %v\nout %q\nwant %q\nerr %q\nwant %q", code, opened, out.String(), c.out, errOut.String(), c.errOut)
			}
			if c.code != 0 {
				if len(asks) != 1 {
					t.Errorf("asked again after a failed clone: %+v", asks)
				}
				return
			}
			if again := jsonOf(asks[1]); again != jsonOf(openAsk{Folders: []string{dir}, PR: billing}) {
				t.Errorf("asked again with %s", again)
			}
			if _, err := os.Stat(filepath.Join(dir, "README.md")); err != nil {
				t.Error("no clone:", err)
			}
		})
	}
}

// A forge on its own port: glab reaches that port, and the clone's folder names the host alone.
func TestCloneOnItsOwnPort(t *testing.T) {
	useCacheDir(t)
	cache, _ := cacheDir()
	l, _ := parsePRLink("https://gitlab.example.org:8443/team/api/-/merge_requests/3")
	calls := gittest.FakeToolCalls(t, "glab", gittest.Rule{Has: "repo clone", Exit: 1})
	clone(l, terminal{nil, io.Discard, io.Discard})
	dir := filepath.Join(cache, "kijk", "clones", "gitlab.example.org", "team", "api")
	c := calls()
	if len(c) != 1 || c[0].GitlabHost != "gitlab.example.org:8443" || !strings.HasPrefix(c[0].Args[len(c[0].Args)-1], dir+".part-") {
		t.Fatalf("calls %+v", c)
	}
}

// A kijk from before /api/open says what to do, but still shows its window to a plain kijk.
func TestOpenOnAnOldKijk(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	open := runningOpener(srv.Listener.Addr().(*net.TCPAddr).Port, "t")
	var errOut bytes.Buffer
	if code := deliver(open, openAsk{Folders: []string{"/code/api"}}, func(int) { t.Error("a folder opened a window") }, terminal{nil, io.Discard, &errOut}); code != 1 || errOut.String() != "kijk: the running kijk is too old for this. Quit it, then try again\n" {
		t.Errorf("a folder: exit %d, err %q", code, errOut.String())
	}
	var opened []int
	if code := deliver(open, openAsk{}, func(seq int) { opened = append(opened, seq) }, terminal{nil, io.Discard, io.Discard}); code != 0 || !slices.Equal(opened, []int{0}) {
		t.Errorf("plain kijk: exit %d, windows %v", code, opened)
	}
}

func TestWindowURL(t *testing.T) {
	if u := windowURL(7717, "t", 0); u != "http://127.0.0.1:7717/?token=t" {
		t.Errorf("plain kijk: %s", u)
	}
	if u := windowURL(7717, "t", 3); u != "http://127.0.0.1:7717/?token=t&open=3" {
		t.Errorf("a request: %s", u)
	}
}

func testDesk(t *testing.T) (*openDesk, *hub) {
	t.Helper()
	p, _ := foldersPath()
	rl, h, _ := testRail()
	return &openDesk{h, rl, newFolderList(p, nil, rl)}, h
}

// openWindow opens a kijk window on h until the test ends.
func openWindow(t *testing.T, h *hub) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(h.serveEvents))
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
}

func realPath(p string) string {
	r, _ := filepath.EvalSymlinks(p)
	return r
}

func TestDeskFolders(t *testing.T) {
	isolatedGit(t)
	useConfigDir(t)
	repo := realPath(gitRepo(t))
	os.Mkdir(filepath.Join(repo, "web"), 0o755)
	notes := realPath(t.TempDir())
	d, h := testDesk(t)
	rep, err := d.open(openAsk{Folders: []string{filepath.Join(repo, "web")}})
	if want := (openReply{Said: "Added " + filepath.Base(repo) + ", opened at web/.", Seq: 1}); err != nil || rep != want {
		t.Fatalf("got %+v %v, want %+v", rep, err, want)
	}
	m := shown(t, h)
	if m.Open != (openRequest{Seq: 1, Root: repo, Cwd: "web", List: listFolders, Added: true}) || len(m.Rows) != 1 || m.Rows[0].Saved == nil {
		t.Fatalf("window gets %+v", m)
	}
	if !d.rl.rts.Has(repo) {
		t.Fatal("an added folder is not readable")
	}

	openWindow(t, h)
	rep, err = d.open(openAsk{Folders: []string{repo, notes}})
	want := openReply{Said: "Added 1 folder to the Folders list. 1 was already in it. The last one is open in the kijk window.", Window: true, Seq: 2}
	if err != nil || rep != want {
		t.Fatalf("got %+v %v, want %+v", rep, err, want)
	}
	if o := shown(t, h).Open; o != (openRequest{Seq: 2, Root: notes, List: listFolders, Added: true}) {
		t.Fatalf("request %+v", o)
	}
	if _, err := d.open(openAsk{Folders: []string{filepath.Join(notes, "nope")}}); err == nil {
		t.Fatal("a folder that is gone was added")
	}
}

// A saved folder that was gone and is back opens at once, though its last count found it missing.
func TestDeskOpensAFolderThatIsBack(t *testing.T) {
	isolatedGit(t)
	useConfigDir(t)
	dir := filepath.Join(realPath(t.TempDir()), "notes")
	os.Mkdir(dir, 0o755)
	d, h := testDesk(t)
	openWindow(t, h)
	go d.fl.watch(h)
	saved := func() SavedSource {
		for _, r := range shown(t, h).Rows {
			if r.Root == dir {
				return *r.Saved
			}
		}
		t.Fatal("no row")
		return SavedSource{}
	}
	await := func(what string, ok func(SavedSource) bool) {
		t.Helper()
		for end := time.Now().Add(3 * time.Second); !ok(saved()); time.Sleep(10 * time.Millisecond) {
			if time.Now().After(end) {
				t.Fatalf("%s: row %+v", what, saved())
			}
		}
	}
	d.fl.add([]savedFolder{{Path: dir}})
	await("counted", func(s SavedSource) bool { return s.Counted })
	os.Remove(dir)
	d.fl.refresh(dir)
	await("missing", func(s SavedSource) bool { return s.Missing })

	os.Mkdir(dir, 0o755)
	if _, err := d.open(openAsk{Folders: []string{dir}}); err != nil {
		t.Fatal(err)
	}
	if s := saved(); s.Missing {
		t.Fatalf("the window gets the request with the row missing: %+v", s)
	}
	await("counted again", func(s SavedSource) bool { return s.Counted && !s.Missing })
}

func TestDeskListAndPlain(t *testing.T) {
	useConfigDir(t)
	d, h := testDesk(t)
	for _, c := range []struct {
		window bool
		ask    openAsk
		want   openReply
		seq    int // of the request the window gets
	}{
		{false, openAsk{}, openReply{}, 0},
		{false, openAsk{List: listFolders}, openReply{Seq: 1}, 1},
		{true, openAsk{}, openReply{Said: "Its window is open already.", Window: true}, 1},
		{true, openAsk{List: listFolders}, openReply{Said: "The kijk window shows the Folders list.", Window: true, Seq: 2}, 2},
	} {
		if c.window && h.open() == 0 {
			openWindow(t, h)
		}
		if rep, err := d.open(c.ask); err != nil || rep != c.want {
			t.Errorf("%+v: got %+v %v, want %+v", c.ask, rep, err, c.want)
		}
		if o := shown(t, h).Open; o.Seq != c.seq || (c.seq > 0 && o.List != listFolders) {
			t.Errorf("%+v: request %+v", c.ask, o)
		}
	}
}

func TestDeskPullRequest(t *testing.T) {
	main, worktree, other := shopClones(t)
	main, worktree, other = realPath(main), realPath(worktree), realPath(other)
	home := realPath(t.TempDir())
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	useCacheDir(t)
	common := filepath.Join(home, "code", "shop")
	run(t, home, "clone", "-q", main, common)
	run(t, common, "remote", "set-url", "origin", "https://github.com/acme/shop.git")
	os.Mkdir(filepath.Join(main, "web"), 0o755)
	useConfigDir(t)
	shop := &prLink{Kind: "github", Host: "github.com", Path: "acme/shop", Number: 477}
	web := &prLink{Kind: "github", Host: "github.com", Path: "acme/web", Number: 12}
	name := filepath.Base(main)
	agentRow := func(root string) Row {
		return Row{Root: root, Repo: filepath.Base(root), AgentSource: &AgentSource{Names: []string{"a"}, Agents: []RowAgent{{"claude", false}}}}
	}
	for _, c := range []struct {
		name   string
		ask    openAsk
		saved  []savedFolder
		agents []Row
		want   openReply
		open   openRequest
		list   []string // the folders list after
	}{
		{"saved", openAsk{PR: shop}, []savedFolder{{Path: other}, {Path: main}}, nil,
			openReply{Said: "Opened #477 in " + name + ".", Seq: 1}, openRequest{Seq: 1, Root: main, PR: 477}, []string{other, main}},
		{"an agent works in the clone", openAsk{PR: shop}, nil, []Row{agentRow(main)},
			openReply{Said: "Opened #477 in " + name + ".", Seq: 1}, openRequest{Seq: 1, Root: main, PR: 477}, []string{}},
		{"an agent works in a worktree of it", openAsk{PR: shop}, nil, []Row{agentRow(worktree)},
			openReply{Said: "Found " + name + " at " + main + ". Added it and opened #477.", Seq: 1}, openRequest{Seq: 1, Root: main, PR: 477, Added: true}, []string{main}},
		{"kijk runs in a folder of the clone", openAsk{PR: shop, Cwd: filepath.Join(main, "web")}, nil, nil,
			openReply{Said: "Found " + name + " at " + main + ". Added it and opened #477.", Seq: 1}, openRequest{Seq: 1, Root: main, PR: 477, Added: true}, []string{main}},
		{"a common place", openAsk{PR: shop}, nil, nil,
			openReply{Said: "Found shop at " + filepath.FromSlash("~/code/shop") + ". Added it and opened #477.", Seq: 1}, openRequest{Seq: 1, Root: common, PR: 477, Added: true}, []string{common}},
		{"the command cloned it", openAsk{PR: shop, Folders: []string{common}}, nil, nil,
			openReply{Said: "Added shop and opened #477.", Seq: 1}, openRequest{Seq: 1, Root: common, PR: 477, Added: true}, []string{common}},
		{"no clone", openAsk{PR: web}, []savedFolder{{Path: other}}, nil,
			openReply{NoClone: true, Said: "No clone of github.com/acme/web in 1 folder on this machine."}, openRequest{}, []string{other}},
	} {
		t.Run(c.name, func(t *testing.T) {
			useConfigDir(t)
			d, h := testDesk(t)
			d.fl.add(c.saved)
			d.rl.setAgents(&agentPart{rows: c.agents})
			rep, err := d.open(c.ask)
			if err != nil || rep != c.want {
				t.Fatalf("got %+v %v, want %+v", rep, err, c.want)
			}
			if o := shown(t, h).Open; o != c.open {
				t.Errorf("request %+v, want %+v", o, c.open)
			}
			if l := d.fl.paths(); !slices.Equal(l, c.list) {
				t.Errorf("folders list %v, want %v", l, c.list)
			}
			if c.open.Root != "" && !d.rl.rts.Has(c.open.Root) {
				t.Error("the clone the window opens is not readable")
			}
		})
	}
}
