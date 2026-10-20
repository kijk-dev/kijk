package main

import (
	"cmp"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"

	"github.com/kijk-dev/kijk/internal/agentstest"
	"github.com/kijk-dev/kijk/internal/git"
)

// The subtests share one kijk and run in order, so a later one starts where the one before it ended.
func TestBrowser(t *testing.T) {
	browser := needBrowser(t)
	isolatedGit(t)
	exe := buildKijk(t, "0.0.1")
	shop := shopRepo(t)
	api, origin := apiRepo(t)
	claude := t.TempDir()
	claudeAgent(t, claude, shop, "Add the cart", "busy")
	codexHome, codex := agentstest.CodexHome(t)
	codex.Set(agentstest.Thread("t1", api, "idle", "name", "Fix the login bug"))
	home := t.TempDir()
	k := startKijkIn(t, exe, home, "CLAUDE_CONFIG_DIR="+claude, "CODEX_HOME="+codexHome, keepEngines(t, home))
	tab := startBrowser(t, browser)
	p := openPage(t, tab, k, "/")

	t.Run("agents", func(t *testing.T) {
		p := p.on(t)
		p.waitFor("two agent rows", `document.querySelectorAll('#rows > li').length === 2`)
		p.waitFor("the Busy Claude Code agent", rowLabelIs("Add the cart, Claude Code, shop · main, Busy"))
		p.waitFor("the Idle Codex agent", rowLabelIs("Fix the login bug, Codex, api · main, Idle"))
		p.waitFor("the start text", `document.querySelector('#start p')?.textContent === 'Select an agent'`)
		p.noErrors()
	})

	t.Run("tree", func(t *testing.T) {
		p := p.on(t)
		p.selectAgent("Add the cart", "shop")
		for _, name := range []string{"docs", "src", "README.md", "logo.png", "notes.txt"} {
			p.waitFor(name+" in the tree", treeItem(name))
		}
		p.waitFor("src marked as changed", treeItem("src")+`?.lastChild.classList.contains('modified')`)
		p.waitFor("notes.txt marked as new", treeItem("notes.txt")+`?.lastChild.classList.contains('untracked')`)
		p.waitFor("README.md without a mark", treeItem("README.md")+`?.lastChild.className.trim() === 'name'`)
		p.noErrors()
	})

	t.Run("code", func(t *testing.T) {
		p := p.on(t)
		p.click("src", treeItem("src"))
		p.doubleClick("main.go", treeItem("main.go"))
		p.waitFor("main.go in a kept tab", activeFile("main.go")+` && !document.querySelector('#filetabs .active').classList.contains('preview')`)
		p.waitFor("the code of main.go", viewHas("package main"))
		p.waitFor("syntax colors", `document.querySelector('#viewer .ln .tx span[class^="h"]')`)
		p.click("util.go", treeItem("util.go"))
		p.waitFor("util.go in a preview tab", activeFile("util.go")+` && document.querySelector('#filetabs .active').classList.contains('preview')`)
		p.waitFor("two tabs", `document.querySelectorAll('#filetabs .filetab').length === 2`)
		p.waitFor("the code of util.go", viewHas("func total(prices []int) int {"))
		p.press("back")
		p.waitFor("main.go after back", activeFile("main.go"))
		p.press("forward")
		p.waitFor("util.go after forward", activeFile("util.go"))
		p.noErrors()
	})

	t.Run("rendered", func(t *testing.T) {
		p := p.on(t)
		p.click("README.md", treeItem("README.md"))
		p.waitFor("the rendered README", `document.querySelector('#viewer article.md h1')?.textContent === 'Shop'`)
		p.click("Source", byText("#viewer .mode button", "Source"))
		p.waitFor("the source of the README", viewHas("# Shop"))
		p.click("Rendered", byText("#viewer .mode button", "Rendered"))
		p.waitFor("the README rendered again", `document.querySelector('#viewer article.md li')?.textContent === 'Cart'`)
		p.click("the guide link", byText("#viewer article.md a", "guide"))
		p.waitFor("the rendered AsciiDoc guide", activeFile("guide.adoc")+` && [...document.querySelectorAll('#viewer article.md h2')].some(h => h.textContent === 'Setup')`)
		p.click("logo.png", treeItem("logo.png"))
		p.waitFor("the image", `document.querySelector('#viewer .imgview img.pic')?.naturalWidth === 8`)
		p.noErrors()
	})

	t.Run("diagram", func(t *testing.T) {
		p := p.on(t)
		p.click("docs", treeItem("docs"))
		p.click("flow.mmd", treeItem("flow.mmd"))
		// The first run on a machine downloads Mermaid.
		p.waitLong(2*time.Minute, "the Mermaid diagram", `(i => i?.complete && i.naturalWidth > 0)(document.querySelector('#viewer .diagram img.diagram-pic.mermaid'))`)
		p.noErrors()
	})

	t.Run("diff", func(t *testing.T) {
		p := p.on(t)
		p.click("the Git tab", gitTab)
		p.waitFor("main.go and notes.txt in the changes", inList("Unstaged", "main.go")+" && "+inList("Unstaged", "notes.txt"))
		p.click("main.go", changeRow("main.go"))
		p.waitFor("the diff of main.go", diffRow("del", "const items = 7")+" && "+diffRow("add", "const items = 70"))
		p.noErrors()
	})

	t.Run("git", func(t *testing.T) {
		p := p.on(t)
		p.selectAgent("Fix the login bug", "api")
		p.click("the Git tab", gitTab)
		p.waitFor("handler.go not staged", inList("Unstaged", "handler.go"))
		box := changeRow("handler.go") + `?.querySelector('input')`
		p.click("the box of handler.go", box)
		p.waitFor("handler.go staged", inList("Staged", "handler.go"))
		p.click("the box of handler.go", box)
		p.waitFor("handler.go not staged again", inList("Unstaged", "handler.go")+" && !"+inList("Staged", "handler.go"))
		p.click("the box of handler.go", box)
		p.waitFor("handler.go staged again", inList("Staged", "handler.go"))
		p.click("the commit message", `document.querySelector('#message')`)
		p.typeText("Check the user name length")
		p.click("Commit", `document.querySelector('#commit-btn:not(:disabled)')`)
		p.waitFor("one commit to push", `document.querySelector('#ahead:not([hidden])')?.textContent === '↑1'`)
		p.click("Push", `document.querySelector('#push-btn:not([hidden]):not(:disabled)')`)
		p.waitFor("nothing left to push", `document.querySelector('#ahead').hidden`)
		if got, err := git.Run(origin, "log", "-1", "--format=%s", "main"); got != "Check the user name length" {
			p.fail("the origin's last commit is %q, %v", got, err)
		}
		p.click("Branch graph", `document.querySelector('#graph .section-toggle')`)
		p.waitFor("the commit in the graph", byText("#graph .item.commit .name", "Check the user name length"))
		p.noErrors()
	})

	t.Run("find", func(t *testing.T) {
		p := p.on(t)
		p.selectAgent("Add the cart", "shop")
		p.press("gotoFile")
		p.waitFor("the go to file popup", `document.querySelector('dialog.goto')?.open`)
		p.typeText("main.go")
		p.waitFor("main.go in the list", `document.querySelector('#goto-list li[title="src/main.go"]')`)
		p.key("Enter", "\r")
		p.waitFor("main.go open", activeFile("main.go")+" && "+viewHas("package main"))
		p.press("find")
		p.waitFor("the find bar", `!document.querySelector('#findbar').hidden`)
		p.typeText("func step")
		p.waitFor("12 matches", `document.querySelector('#findbar .count')?.textContent === '1 of 12'`)
		p.waitFor("a marked match", `document.querySelector('#viewer mark.hit')`)
		p.key("Escape", kb.Escape)
		p.waitFor("no find bar", `document.querySelector('#findbar').hidden`)
		p.press("gotoLine")
		p.waitFor("the go to line bar", `document.querySelector('#findbar:not([hidden])')?.dataset.mode === 'line'`)
		p.typeText("40")
		p.key("Enter", "\r")
		p.waitFor("line 40 flashed", `[...document.querySelectorAll('#viewer .ln.flash .gut span')].some(s => s.textContent === '40')`)
		p.noErrors()
	})

	t.Run("live", func(t *testing.T) {
		p := p.on(t)
		p.click("the Files tab", filesTab)
		p.click("util.go", treeItem("util.go"))
		p.waitFor("the code of util.go", activeFile("util.go")+" && "+viewHas("func total(prices []int) int {"))
		util := filepath.Join(shop, "src", "util.go")
		b, err := os.ReadFile(util)
		if err != nil {
			t.Fatal(err)
		}
		write(t, util, string(b)+"\n// Changed on disk.\n")
		p.waitFor("the change in the open file", viewHas("// Changed on disk."))
		write(t, filepath.Join(shop, "src", "extra.go"), "package main\n")
		p.waitFor("the new file in the tree", treeItem("extra.go"))
		p.noErrors()
	})

	t.Run("shots", func(t *testing.T) {
		if os.Getenv("KIJK_E2E_SHOTS") == "" {
			t.Skip("set KIJK_E2E_SHOTS=1 to save the main views in light, dark and a narrow window")
		}
		p := p.on(t)
		dir := e2eOut(t)
		views := []struct {
			name string
			open func()
		}{
			{"code", func() {
				p.click("the Files tab", filesTab)
				p.click("main.go", treeItem("main.go"))
				p.waitFor("main.go", activeFile("main.go")+" && "+viewHas("package main"))
			}},
			{"markdown", func() {
				p.click("README.md", treeItem("README.md"))
				p.waitFor("the rendered README", `document.querySelector('#viewer article.md h1')`)
			}},
			{"diff", func() {
				p.click("the Git tab", gitTab)
				p.click("main.go", changeRow("main.go"))
				p.waitFor("the diff of main.go", diffRow("add", "const items = 70"))
			}},
		}
		for _, v := range views {
			v.open()
			for _, s := range []struct {
				name, scheme string
				width        int64
			}{{"light", "light", 1400}, {"dark", "dark", 1400}, {"narrow", "light", 800}} {
				p.emulate(s.scheme, s.width)
				p.act("wait for the layout", chromedp.Sleep(300*time.Millisecond))
				p.screenshot(dir, v.name+"-"+s.name)
			}
			p.emulate("light", 1400)
		}
		p.noErrors()
		t.Logf("screenshots in %s", dir)
	})

	t.Run("folders", func(t *testing.T) {
		notes := filepath.Join(t.TempDir(), "notes")
		write(t, filepath.Join(notes, "todo.md"), "# Todo\n")
		if out := runCmd(t, k.env, exe, "-port", fmt.Sprint(k.port), "-no-open", notes); !strings.Contains(out, "Added notes") {
			p.on(t).fail("kijk <folder> said:\n%s", out)
		}
		f := openPage(t, newTab(t, tab), k, "/?window=folders")
		f.click("the notes row", row("notes"))
		f.waitFor("todo.md in the tree", treeItem("todo.md"))
		f.click("Remove", row("notes")+`?.querySelector('button.rm')`)
		f.waitFor("no notes row", "!"+row("notes"))
		f.waitFor("the undo notice", `document.querySelector('#statusbar').textContent.includes('Removed notes')`)
		f.click("Undo", byText("#statusbar button", "Undo"))
		f.waitFor("the notes row again", row("notes"))
		f.noErrors()
	})

	t.Run("remote machine", func(t *testing.T) {
		m := newFakeMachines(t)
		vm := m.add("vm", "vm", "")
		// The real binary keeps its logins where a user's kijk does; vm is a name no one else uses.
		t.Cleanup(func() {
			os.Remove(filepath.Join("/tmp", fmt.Sprintf("kijk-%d", os.Getuid()), filepath.Base(newSSHClient(vm).ctl)))
		})
		publish(t, "0.0.1", exe)
		app := namedRepo(t, "app")
		write(t, filepath.Join(app, "app.go"), "package app\n")
		claudeAgent(t, filepath.Join(m.home(vm), ".claude"), app, "Remote work", "busy")
		home := t.TempDir()
		write(t, filepath.Join(home, ".config", "kijk", "machines.json"), fmt.Sprintf(`{"machines":[{"target":%q,"enabled":true}]}`, vm))
		local := startKijkIn(t, exe, home)
		r := openPage(t, newTab(t, tab), local, "/")
		// The first connection installs kijk on the machine.
		r.waitLong(time.Minute, "the agent on the machine", rowLabelIs("Remote work, Claude Code, app · main, Busy"))
		r.click("the remote row", row("Remote work"))
		r.waitFor("app.go in the tree", treeItem("app.go"))
		r.click("app.go", treeItem("app.go"))
		r.waitFor("the remote code", activeFile("app.go")+" && "+viewHas("package app"))
		r.noErrors()
	})

	// An agent type that kijk on a remote machine cannot read joins the notice, after the machine's name.
	t.Run("remote agent error", func(t *testing.T) {
		m := newFakeMachines(t)
		vm := m.add("vm", "vm", "")
		t.Cleanup(func() {
			os.Remove(filepath.Join("/tmp", fmt.Sprintf("kijk-%d", os.Getuid()), filepath.Base(newSSHClient(vm).ctl)))
		})
		publish(t, "0.0.1", exe)
		write(t, filepath.Join(m.home(vm), ".claude", "sessions", "9.json"), "{broken")
		home, claude := t.TempDir(), t.TempDir()
		claudeAgent(t, claude, shop, "Add the cart", "idle")
		write(t, filepath.Join(home, ".config", "kijk", "machines.json"), fmt.Sprintf(`{"machines":[{"target":%q,"enabled":true}]}`, vm))
		r := openPage(t, newTab(t, tab), startKijkIn(t, exe, home, "CLAUDE_CONFIG_DIR="+claude), "/")
		// The first connection installs kijk on the machine.
		r.waitLong(time.Minute, "the notice", noticeSays("Cannot read Claude Code agents · they are hidden"))
		r.waitFor("the machine's name before the reason", agentNotice+`.dataset.tip.includes(`+jsString("\n"+vm+": cannot read session file")+`)`)
		r.waitFor("the local agent", rowLabelIs("Add the cart, Claude Code, shop · main, Idle"))
		r.noErrors()
	})

	// Every sample of testdata/filetypes shows the way its entry in /api/filetypes says.
	t.Run("file types", func(t *testing.T) {
		samples, err := os.ReadDir(filepath.Join("testdata", "filetypes"))
		if err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(t.TempDir(), "filetypes")
		texts := map[string]string{}
		for _, s := range samples {
			b, err := os.ReadFile(filepath.Join("testdata", "filetypes", s.Name()))
			if err != nil {
				t.Fatal(err)
			}
			texts[s.Name()] = string(b)
			write(t, filepath.Join(dir, s.Name()), string(b))
		}
		types := listFileTypes(t, k)
		root, err := filepath.EvalSymlinks(dir) // as kijk keeps the folder
		if err != nil {
			t.Fatal(err)
		}
		if out := runCmd(t, k.env, exe, "-port", fmt.Sprint(k.port), "-no-open", dir); !strings.Contains(out, "Added filetypes") {
			p.on(t).fail("kijk <folder> said:\n%s", out)
		}
		f := openPage(t, newTab(t, tab), k, "/?window=folders")
		f.click("the filetypes row", row("filetypes"))
		for _, s := range samples {
			name := s.Name()
			ft := typeOf(types, name)
			f.click(name, treeItem(name))
			f.waitFor(name+" open", activeFile(name))
			switch ft.Show {
			case "image":
				f.waitFor(name+" as a picture", fmt.Sprintf(`(i => i?.alt === %s && i.complete && i.naturalWidth > 0)(document.querySelector('#viewer .imgview img.pic'))`, jsString(name)))
			case "rendered":
				f.waitFor(name+" rendered", `document.querySelector('#viewer article.md:not(.diagram-file) [data-lines]')`)
			case "drawn":
				// The first run on a machine downloads the engine.
				f.waitLong(2*time.Minute, name+" drawn", fmt.Sprintf(`(i => i?.complete && i.naturalWidth > 0)(document.querySelector('#viewer .diagram img.diagram-pic.%s'))`, ft.Name))
			default:
				f.fail("%s: no way to show %q", name, ft.Show)
			}
			if ft.Show == "image" && !ft.Text {
				continue
			}
			first, _, _ := strings.Cut(texts[name], "\n")
			hl := `document.querySelector('#viewer .ln .tx span[class^="h"]')`
			if !colored(t, k, root, name) {
				hl = "!" + hl // a type chroma cannot color shows plain source
			}
			f.click("Source", byText("#viewer .mode button", "Source"))
			f.waitFor(name+" as its source", viewHas(first)+" && "+hl)
		}
		f.noErrors()
	})

	// Every agent type in the list shows its icon, in its color, and its label.
	t.Run("agent types", func(t *testing.T) {
		home := agentstest.ShortTempDir(t) // the Codex socket is in it
		types := listAgentTypes(t, k)
		for _, at := range types {
			agentstest.StartAgent(t, home, at.Name, shop, at.Label+" agent")
		}
		a := openPage(t, newTab(t, tab), startKijkIn(t, exe, home, "CLAUDE_CONFIG_DIR=", "CODEX_HOME="), "/")
		for _, at := range types {
			icon := fmt.Sprintf(`(s => (s.innerHTML = %s, [...document.querySelectorAll('#rows .agent')].find(i => i.innerHTML === s.innerHTML)))(document.createElement('span'))`, jsString(at.Icon))
			a.waitFor("the icon of "+at.Label, icon)
			a.waitFor("the label "+at.Label, icon+`.closest('li').getAttribute('aria-label').includes(`+jsString(at.Label)+`)`)
			color := cmp.Or(at.Color, "var(--text)")
			a.waitFor("the color of "+at.Label, fmt.Sprintf(`(c => { const e = document.createElement('i'); e.style.color = c; document.body.append(e); `+
				`const want = getComputedStyle(e).color; e.remove(); return getComputedStyle(%s).color === want; })(%s)`, icon, jsString(color)))
		}
		a.noErrors()
	})

	// One notice names every agent type kijk cannot read; the agents of the other types stay.
	t.Run("agent errors", func(t *testing.T) {
		claude := t.TempDir()
		broken := filepath.Join(claude, "sessions", "9.json")
		write(t, broken, "{broken")
		codexHome, codex := agentstest.CodexHome(t)
		codex.Set(agentstest.Thread("t1", api, "idle", "name", "Fix the login bug"))
		e := openPage(t, newTab(t, tab), startKijkIn(t, exe, t.TempDir(), "CLAUDE_CONFIG_DIR="+claude, "CODEX_HOME="+codexHome), "/")
		e.waitFor("the Claude Code notice", noticeSays("Cannot read Claude Code agents · they are hidden"))
		e.waitFor("the file that failed on hover", agentNotice+`.dataset.tip.includes('9.json')`)
		e.waitFor("the Codex agent", rowLabelIs("Fix the login bug, Codex, api · main, Idle"))

		codex.Reply(func(c *agentstest.Conn, _ int, _ string) { c.Send(0x1, []byte("{oops")) })
		e.waitFor("one notice naming both types", noticeSays("Cannot read Claude Code, Codex agents · they are hidden"))
		e.waitFor("no agent rows", `!document.querySelector('#rows li[role=button]')`)
		e.emulate("light", 500)
		e.waitFor("the short notice", agentNotice+`.querySelector('.short').textContent === '2 agent types hidden' && getComputedStyle(`+agentNotice+`.querySelector('.short')).display !== 'none'`)
		e.emulate("light", 1400)

		write(t, broken, sessionJSON(os.Getpid(), "s-cart", shop, "Add the cart", "idle", time.Now().UnixMilli()))
		e.waitFor("the Codex notice", noticeSays("Cannot read Codex agents · they are hidden"))
		e.waitFor("the Claude Code agent", rowLabelIs("Add the cart, Claude Code, shop · main, Idle"))
		codex.Reply(nil)
		e.waitFor("the notice gone", "!"+agentNotice)
		e.waitFor("the Codex agent again", row("Fix the login bug"))
		e.noErrors()
	})

	t.Run("no agent tools", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "missing")
		n := startKijkIn(t, exe, t.TempDir(), "CLAUDE_CONFIG_DIR="+missing, "CODEX_HOME="+missing)
		var labels []string
		for _, at := range listAgentTypes(t, n) {
			labels = append(labels, at.Label)
		}
		want := "No running agents · kijk shows " + strings.Join(labels[:len(labels)-1], ", ") + " and " + labels[len(labels)-1] + " agents"
		e := openPage(t, newTab(t, tab), n, "/")
		e.waitFor(want, `(s => !s.hidden && !s.classList.contains('error') && s.textContent === `+jsString(want)+`)(document.querySelector('#status'))`)
		e.waitFor("no notice", `document.querySelector('#statusbar .kijk') && !document.querySelector('#statusbar .agent-error')`)
		e.noErrors()
	})
}

type agentType struct {
	Name, Label, Icon, Color string
}

// listAgentTypes gets the list of agent types from kijk, as the page does.
func listAgentTypes(t *testing.T, k *kijkProcess) []agentType {
	t.Helper()
	resp, err := k.request("GET", "/api/agenttypes")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var types []agentType
	if err := json.NewDecoder(resp.Body).Decode(&types); err != nil || len(types) == 0 {
		t.Fatalf("/api/agenttypes: %d %v", resp.StatusCode, err)
	}
	return types
}

type fileType struct {
	Name, Show  string
	Exts, Names []string
	Text        bool
}

// listFileTypes gets the list of file types from kijk, as the page does.
func listFileTypes(t *testing.T, k *kijkProcess) []fileType {
	t.Helper()
	resp, err := k.request("GET", "/api/filetypes")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var types []fileType
	if err := json.NewDecoder(resp.Body).Decode(&types); err != nil || len(types) == 0 {
		t.Fatalf("/api/filetypes: %d %v", resp.StatusCode, err)
	}
	return types
}

// colored tells whether kijk colors a file's source, as it does when it has a lexer for the file.
func colored(t *testing.T, k *kijkProcess, root, name string) bool {
	t.Helper()
	resp, err := k.request("GET", "/api/lines?"+url.Values{"root": {root}, "path": {name}, "from": {"0"}, "to": {"1000"}}.Encode())
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var lines [][][2]string
	if err := json.NewDecoder(resp.Body).Decode(&lines); err != nil {
		t.Fatalf("/api/lines of %s: %d %v", name, resp.StatusCode, err)
	}
	return slices.ContainsFunc(lines, func(l [][2]string) bool {
		return slices.ContainsFunc(l, func(tk [2]string) bool { return tk[0] != "" })
	})
}

// typeOf finds the file type of a file name as kijk does: by its exact name, else by its extension.
func typeOf(types []fileType, name string) fileType {
	ext := strings.ToLower(filepath.Ext(name))
	for _, by := range []func(fileType) bool{
		func(ft fileType) bool { return slices.Contains(ft.Names, name) },
		func(ft fileType) bool { return slices.Contains(ft.Exts, ext) },
	} {
		if i := slices.IndexFunc(types, by); i >= 0 {
			return types[i]
		}
	}
	return fileType{}
}

const (
	gitTab   = `document.querySelector('#tabs button[data-tab=git]')`
	filesTab = `document.querySelector('#tabs button[data-tab=files]')`
)

const agentRows = `[...document.querySelectorAll('#rows li[role=button]')]`

// agentNotice is the status bar's notice of the agent types kijk cannot read.
const agentNotice = `document.querySelector('#statusbar .agent-error')`

func noticeSays(text string) string {
	return agentNotice + `?.querySelector('.long').textContent === ` + jsString(text)
}

func row(label string) string {
	return fmt.Sprintf(`%s.find(e => e.getAttribute('aria-label').startsWith(%s))`, agentRows, jsString(label))
}

func rowLabelIs(label string) string {
	return fmt.Sprintf(`%s.some(e => e.getAttribute('aria-label') === %s)`, agentRows, jsString(label))
}

// activeFile is true when the active tab shows the file name, not a diff of it.
func activeFile(name string) string {
	return fmt.Sprintf(`(t => t?.firstChild.textContent === %s && !t.querySelector('.muted'))(document.querySelector('#filetabs .filetab.active'))`, jsString(name))
}

// viewHas is true when a line of the open file or diff holds text.
func viewHas(text string) string {
	return fmt.Sprintf(`[...document.querySelectorAll('#viewer .ln .tx')].some(e => e.textContent.includes(%s))`, jsString(text))
}

func diffRow(kind, text string) string {
	return fmt.Sprintf(`[...document.querySelectorAll('#viewer .ln.%s .tx')].some(e => e.textContent === %s)`, kind, jsString(text))
}

func changeRow(name string) string {
	return fmt.Sprintf(`[...document.querySelectorAll('#changes li.item')].find(e => e.querySelector('.name')?.textContent === %s)`, jsString(name))
}

// inList is true when the Git tab's list, such as Staged or Unstaged, holds the file.
func inList(list, name string) string {
	return fmt.Sprintf(`[...document.querySelectorAll('#changes section')].some(s => s.querySelector('h3')?.textContent.startsWith(%s) && [...s.querySelectorAll('li.item .name')].some(n => n.textContent === %s))`,
		jsString(list+" "), jsString(name))
}
