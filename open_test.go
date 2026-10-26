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
	"testing"
)

func TestDeliver(t *testing.T) {
	api := openAsk{Folders: []string{"/code/api"}}
	billing := openAsk{PR: &prLink{Host: "github.com", Path: "acme/billing", Number: 12}}
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
		{"no clone of the pull request's repo", billing, openReply{NoClone: true}, nil,
			1, "", "kijk: No folder of github.com/acme/billing is in kijk. Add a folder of this repo first: kijk <folder>\n", nil},
		{"the running kijk refuses", api, openReply{}, errors.New("the running kijk is too old for this. Quit it, then try again"),
			1, "", "kijk: the running kijk is too old for this. Quit it, then try again\n", nil},
	} {
		var out, errOut bytes.Buffer
		var sent openAsk
		var opened []int
		code := deliver(func(a openAsk) (openReply, error) { sent = a; return c.rep, c.err }, c.ask,
			func(seq int) { opened = append(opened, seq) }, terminal{&out, &errOut})
		if code != c.code || out.String() != c.out || errOut.String() != c.errOut || !slices.Equal(opened, c.opened) || jsonOf(sent) != jsonOf(c.ask) {
			t.Errorf("%s: exit %d, out %q, err %q, windows %v, sent %s", c.name, code, out.String(), errOut.String(), opened, jsonOf(sent))
		}
	}
	if code := deliver(func(openAsk) (openReply, error) { return openReply{Seq: 1}, nil }, openAsk{}, nil, terminal{io.Discard, io.Discard}); code != 0 {
		t.Errorf("-no-open: exit %d", code)
	}
}

// A kijk from before /api/open says what to do.
func TestOpenOnAnOldKijk(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	_, err := runningOpener(srv.Listener.Addr().(*net.TCPAddr).Port, "t")(openAsk{})
	if err == nil || err.Error() != "the running kijk is too old for this. Quit it, then try again" {
		t.Fatalf("error %v", err)
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
	useConfigDir(t)
	shop := &prLink{Host: "github.com", Path: "acme/shop", Number: 477}
	name := filepath.Base(main)
	agentRow := func(root string) Row {
		return Row{Root: root, Repo: filepath.Base(root), AgentSource: &AgentSource{Names: []string{"a"}, Agents: []RowAgent{{"claude", false}}}}
	}
	for _, c := range []struct {
		name   string
		saved  []savedFolder
		agents []Row
		want   openReply
		open   openRequest
		list   []string // the folders list after
	}{
		{"saved", []savedFolder{{Path: other}, {Path: main}}, nil,
			openReply{Said: "Opened #477 in " + name + ".", Seq: 1}, openRequest{Seq: 1, Root: main, PR: 477}, []string{other, main}},
		{"an agent works in the clone", nil, []Row{agentRow(main)},
			openReply{Said: "Opened #477 in " + name + ".", Seq: 1}, openRequest{Seq: 1, Root: main, PR: 477}, []string{}},
		{"an agent works in a worktree of it", nil, []Row{agentRow(worktree)},
			openReply{Said: "Added " + name + " and opened #477.", Seq: 1}, openRequest{Seq: 1, Root: main, PR: 477, Added: true}, []string{main}},
		{"no clone", []savedFolder{{Path: other}}, nil,
			openReply{NoClone: true}, openRequest{}, []string{other}},
	} {
		t.Run(c.name, func(t *testing.T) {
			useConfigDir(t)
			d, h := testDesk(t)
			d.fl.add(c.saved)
			d.rl.setAgents(&agentPart{rows: c.agents})
			rep, err := d.open(openAsk{PR: shop})
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
