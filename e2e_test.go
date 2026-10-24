package main

import (
	"cmp"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"

	"github.com/kijk-dev/kijk/internal/agents"
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
		p.waitFor("no PRs tab on main", prsTab+`.hidden`)
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

	t.Run("branch review", func(t *testing.T) {
		repo, origin := reviewRepo(t)
		forge := newFakeForge(t)
		reviewForge(t, forge, repo, origin, false)
		claude := t.TempDir()
		claudeAgent(t, claude, repo, "Fix the login", "idle")
		k := startKijkIn(t, exe, t.TempDir(), "CLAUDE_CONFIG_DIR="+claude, forge.env())
		b := openPage(t, newTab(t, tab), k, "/")
		b.selectAgent("Fix the login", "review")
		const session = "internal/auth/session.go"

		// The PRs tab and its panel.
		b.waitFor("the PRs tab", `(b => b && !b.hidden && b.dataset.tip.startsWith('PRs\n'))(`+prsTab+`)`)
		b.press("prsTab")
		b.waitFor("the PRs panel", `!document.querySelector('#prs').hidden && `+prsTab+`.classList.contains('active')`)
		b.click("This branch", prRow(482))
		b.waitFor("the pull request", `document.querySelector('#prs .pr-num')?.textContent === '#482' && document.querySelector('#prs .pr-title')?.textContent === 'Fix login redirect loop'`)
		b.waitFor("the branch and its base", `[...document.querySelectorAll('#prs .prmeta .ref')].map(r => r.textContent).join(' ') === 'fix-login main'`)
		b.waitFor("the bar", `document.querySelector('#prs .unpushed .t')?.textContent === "1 commit and 2 files not pushed. Reviewers don't see them yet." && `+
			byText("#prs .unpushed button", "Push"))
		b.waitFor("six files", `document.querySelectorAll('#prs-branch .prfiles li:not(.ov)').length === 6`)
		b.waitFor("the new files marked", prsRow("redirect.go")+`.querySelector('.np') && `+prsRow("redirect_test.go")+`.querySelector('.np') && !`+prsRow("session.go")+`.querySelector('.np')`)
		b.act("reload", chromedp.Reload())
		b.waitFor("the PRs tab kept", prsTab+`.classList.contains('active') && !document.querySelector('#prs').hidden && document.querySelectorAll('#prs-branch .prfiles li:not(.ov)').length === 6`)

		// Branch changes: every file in one scroll.
		b.waitFor("the Branch changes tab", `document.querySelector('#filetabs .filetab.active')?.textContent.startsWith('Branch changes')`)
		b.waitFor("six boxes", `document.querySelectorAll('#viewer .fbox').length === 6`)
		b.waitFor("go.sum folded", fileBox("go.sum")+`.classList.contains('shut') && `+fileBox("go.sum")+`.querySelector('.fnote').textContent.startsWith('Generated file, 2 lines')`)
		b.waitFor("the edit not committed", boxHas("README.md", "add", "Run it with go run."))
		b.waitFor("the untracked file", boxHas("internal/auth/redirect_test.go", "add np", "func TestSafeNext(t *testing.T) {"))
		b.waitFor("the lines not pushed", boxHas(session, "add np", "\tsess, ok := s.byID[id]")+" && "+fileBox(session)+`.querySelector('.np-tag')`)
		b.waitFor("the pushed line without dashes", boxHas(session, "add", "\tsess, ok := s.lookup(c.Value)")+` && !`+boxRow(session, "\tsess, ok := s.lookup(c.Value)")+`.classList.contains('np')`)
		b.waitFor("the dashed circle on new files", fileBox("internal/auth/redirect.go")+`.querySelector('.fhead .np') && !`+fileBox(session)+`.querySelector('.fhead .np')`)
		fold := fileBox("README.md") + `.querySelector('.fold')`
		b.click("the README.md chevron", fold)
		b.waitFor("README.md folded", fileBox("README.md")+`.classList.contains('shut')`)
		b.click("the README.md chevron", fold)
		b.waitFor("README.md open", `!`+fileBox("README.md")+`.classList.contains('shut')`)

		// The sidebar list jumps to a box, and follows the scroll.
		b.click("session.go in the list", prsRow("session.go"))
		b.waitFor("session.go at the top", `(s => s.scrollTop > 0 && Math.abs(`+fileBox(session)+`.offsetTop - 12 - s.scrollTop) < 2)(document.querySelector('.allfiles'))`)
		b.evalJS(`document.querySelector('.allfiles').scrollTop = 0`)
		b.waitFor("README.md selected", prsRow("README.md")+`.classList.contains('selected')`)
		b.press("nextChange")
		b.waitFor("a change below the top", changeAt)
		top := evalJS[float64](b, `document.querySelector('.allfiles').scrollTop`)
		b.press("nextChange")
		b.waitFor("the next change", changeAt+` && document.querySelector('.allfiles').scrollTop > `+fmt.Sprint(top))
		b.press("prevChange")
		b.waitFor("the change before", changeAt+` && document.querySelector('.allfiles').scrollTop === `+fmt.Sprint(top))
		b.press("find")
		b.act("wait for a find bar", chromedp.Sleep(200*time.Millisecond))
		b.waitFor("no find bar in Branch changes", `document.querySelector('#findbar').hidden`)
		b.click("Show more lines", fileBox(session)+`.querySelector('.ln.sep .more')`)
		b.waitFor("more lines of session.go", boxRow(session, "func (s *Store) Resume(w http.ResponseWriter, r *http.Request) (*Session, error) {"))

		// The agent edits a file above the one on screen: the view stays where it is.
		b.click("session.go in the list", prsRow("session.go"))
		b.waitFor("session.go at the top", `(s => s.scrollTop > 0 && Math.abs(`+fileBox(session)+`.offsetTop - 12 - s.scrollTop) < 2)(document.querySelector('.allfiles'))`)
		write(t, filepath.Join(repo, "README.md"), "# Review\n\nA shop backend.\n\nRun it with go run.\n\nOr build it.\nThen run the binary.\n")
		b.waitFor("the new lines", boxHas("README.md", "add", "Then run the binary."))
		b.waitFor("session.go still at the top", `Math.abs(`+fileBox(session)+`.offsetTop - 12 - document.querySelector('.allfiles').scrollTop) < 2`)

		// Agent notes: with + and with the key.
		b.waitFor("no + on a removed line", fileBox(session)+`.querySelector('.ln.del') && !`+fileBox(session)+`.querySelector('.ln.del .addc')`)
		noted := boxRow(session, "\tsess, ok := s.byID[id]")
		b.hover("the line to note", noted)
		b.waitFor("the + button", `getComputedStyle(`+noted+`.querySelector('.addc')).display !== 'none'`)
		b.click("+", noted+`.querySelector('.addc')`)
		b.waitFor("the note editor", `document.activeElement?.matches('.thr.editing textarea')`)
		b.typeText("Return a copy, not the pointer.")
		b.press("submitNote")
		b.waitFor("the note under its line", noted+`.nextElementSibling?.matches('.thr.note') && `+noted+`.nextElementSibling.querySelector('p').textContent === 'Return a copy, not the pointer.'`)
		b.waitFor("one note on session.go", fileBox(session)+`.querySelector('.fhead .tc')?.textContent === '1' && `+prsRow("session.go")+`.querySelector('.tc')?.textContent === '1'`)
		redirect := boxRow("internal/auth/redirect.go", "\treturn next")
		b.click("a line of redirect.go", redirect+`.querySelector('.tx')`)
		b.press("addNote")
		b.waitFor("the note editor", `document.activeElement?.matches('.thr.editing textarea')`)
		b.press("submitNote")
		b.waitFor("an empty note not added", `document.querySelector('.thr.editing') && !`+fileBox("internal/auth/redirect.go")+`.querySelector('.thr.note:not(.editing)')`)
		b.typeText("Keep next only when it is a path on this site.")
		b.press("submitNote")
		b.waitFor("the second note", redirect+`.nextElementSibling?.matches('.thr.note:not(.editing)')`)
		b.click("Edit", redirect+`.nextElementSibling.querySelector('[aria-label=Edit]')`)
		b.waitFor("the editor with the note", `document.activeElement?.value === 'Keep next only when it is a path on this site.'`)
		b.typeText(" A //evil.com path passes now.")
		b.press("submitNote")
		b.waitFor("the edited note", redirect+`.nextElementSibling?.querySelector('p')?.textContent === 'Keep next only when it is a path on this site. A //evil.com path passes now.'`)
		b.waitFor("two notes in the box", `document.querySelector('#prs .reviewbox .head .muted')?.textContent === '2'`)

		// Notes stay over a reload.
		b.act("reload", chromedp.Reload())
		b.waitFor("the notes after a reload", noted+`?.nextElementSibling?.matches('.thr.note') && `+redirect+`?.nextElementSibling?.matches('.thr.note')`)
		b.waitFor("the PRs tab kept", prsTab+`.classList.contains('active') && !document.querySelector('#prs').hidden`)
		b.evalJS(`document.querySelector('.allfiles').scrollTop = 0`)
		b.evalJS(`document.querySelector('.allfiles').focus()`)
		b.press("nextNote")
		b.waitFor("the first note below the top", `(s => [...s.querySelectorAll('.thr.note')].some(n => Math.abs(n.getBoundingClientRect().top - s.getBoundingClientRect().top - 124) < 2))(document.querySelector('.allfiles'))`)

		// Another branch: main has no branch changes and shows no notes; back on fix-login they return.
		run(t, repo, "stash", "-q", "-u")
		run(t, repo, "checkout", "-q", "main")
		b.waitFor("the list without this branch on main", `!`+prsTab+`.hidden && document.querySelector('#prs-branch').hidden && ![...document.querySelectorAll('#prs-list .section-toggle')].some(s => s.textContent === 'This branch')`)
		b.waitFor("no branch changes on main", `!document.querySelector('.fbox') && !document.querySelector('.thr.note') && document.querySelector('#viewer .empty')?.textContent === 'This branch has no branch changes'`)
		run(t, repo, "checkout", "-q", "fix-login")
		run(t, repo, "stash", "pop", "-q")
		b.waitFor("the PRs tab again", `!`+prsTab+`.hidden && !document.querySelector('#prs').hidden`)
		b.waitFor("the notes again", noted+`?.nextElementSibling?.matches('.thr.note') && `+redirect+`?.nextElementSibling?.matches('.thr.note')`)

		// The same while a file tab shows: Branch changes is not left on the old branch.
		b.click("Open the whole file", fileBox(session)+`.querySelector('[aria-label="Open the whole file"]')`)
		b.waitFor("the file tab", activeFile("session.go"))
		b.click("the Files tab", filesTab)
		run(t, repo, "stash", "-q", "-u")
		run(t, repo, "checkout", "-q", "main")
		b.waitFor("the list without this branch on main", `!`+prsTab+`.hidden && document.querySelector('#prs-branch').hidden && ![...document.querySelectorAll('#prs-list .section-toggle')].some(s => s.textContent === 'This branch')`)
		b.click("the Branch changes tab", byText("#filetabs .filetab span", "Branch changes"))
		b.waitFor("no branch changes on main", `!document.querySelector('.fbox') && !document.querySelector('.thr.note') && document.querySelector('#viewer .empty')?.textContent === 'This branch has no branch changes'`)
		run(t, repo, "checkout", "-q", "fix-login")
		run(t, repo, "stash", "pop", "-q")
		b.waitFor("the PRs tab again", `!`+prsTab+`.hidden`)
		b.press("prsTab")
		b.waitFor("the notes again", noted+`?.nextElementSibling?.matches('.thr.note') && `+redirect+`?.nextElementSibling?.matches('.thr.note')`)

		// A branch with the same split point, upstream and changes shows none of these notes.
		run(t, repo, "stash", "-q", "-u")
		run(t, repo, "checkout", "-q", "-b", "other")
		run(t, repo, "branch", "-q", "--set-upstream-to=origin/fix-login")
		run(t, repo, "stash", "pop", "-q")
		b.waitFor("the other branch", `[...document.querySelectorAll('#prs .prmeta .ref')].map(r => r.textContent).join(' ') === 'other main'`)
		b.waitFor("no notes on the other branch", `document.querySelector('#prs .reviewbox .head .muted')?.textContent === 'none yet' && document.querySelector('.fbox') && !document.querySelector('.thr.note')`)
		run(t, repo, "stash", "-q", "-u")
		run(t, repo, "checkout", "-q", "fix-login")
		run(t, repo, "stash", "pop", "-q")
		b.waitFor("the notes of fix-login", noted+`?.nextElementSibling?.matches('.thr.note') && `+redirect+`?.nextElementSibling?.matches('.thr.note')`)

		// A failed read of a file does not make its notes outdated, also when their boxes are not drawn.
		b.click("close Branch changes", `document.querySelector('#filetabs .filetab.active .close')`)
		b.waitFor("Branch changes closed", `!document.querySelector('.fbox')`)
		b.evalJS(`window.e2eFetch = window.fetch; window.fetch = (u, o) => String(u).includes('/api/lines') ? Promise.resolve(new Response('git failed', {status: 500})) : e2eFetch(u, o)`)
		b.click("Copy for agent", byText("#prs .reviewbox button", "Copy for agent…"))
		b.waitFor("the panel", `document.querySelectorAll('.copypanel li').length === 2`)
		b.waitFor("no note outdated", `![...document.querySelectorAll('.copypanel .where')].some(w => w.textContent.includes('outdated'))`)
		b.key("Escape", kb.Escape)
		b.evalJS(`window.fetch = e2eFetch`)
		b.press("prsTab")
		b.waitFor("Branch changes again", redirect+`?.nextElementSibling?.matches('.thr.note') && !document.querySelector('.thr.note .was')`)

		// A failed refresh keeps the view and the note being typed.
		b.click("a line of redirect.go", redirect+`.querySelector('.tx')`)
		b.press("addNote")
		b.typeText("Half a note")
		b.evalJS(`window.e2eFails = 0; window.fetch = (u, o) => String(u).includes('/api/branch') ? (e2eFails++, Promise.resolve(new Response('git failed', {status: 500}))) : e2eFetch(u, o)`)
		readme, err := os.ReadFile(filepath.Join(repo, "README.md"))
		if err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(repo, "README.md"), string(readme)+"\n")
		b.waitFor("a failed refresh", `e2eFails > 0`)
		b.act("wait for the page", chromedp.Sleep(300*time.Millisecond))
		b.waitFor("the view and the note kept", `document.querySelector('.fbox') && document.querySelector('.thr.editing textarea')?.value === 'Half a note' && document.querySelectorAll('.thr.note:not(.editing)').length === 2`)
		b.evalJS(`window.fetch = e2eFetch`)
		write(t, filepath.Join(repo, "README.md"), string(readme))
		b.key("Escape", kb.Escape)
		b.waitFor("the editor closed", `!document.querySelector('.thr.editing')`)

		// Lines added above a note: it stays under its line, also when its box is not on screen.
		redirectGo := filepath.Join(repo, "internal", "auth", "redirect.go")
		src, err := os.ReadFile(redirectGo)
		if err != nil {
			t.Fatal(err)
		}
		write(t, redirectGo, strings.Replace(string(src), "package auth\n", "package auth\n\n// One.\n// Two.\n// Three.\n", 1))
		b.waitFor("the note four lines lower", redirect+`?.querySelector('.gut span:last-child').textContent === '12' && `+redirect+`.nextElementSibling?.matches('.thr.note')`)
		b.click("close Branch changes", `document.querySelector('#filetabs .filetab.active .close')`)
		b.waitFor("Branch changes closed", `!document.querySelector('.fbox')`)
		write(t, redirectGo, strings.Replace(string(src), "package auth\n", "package auth\n\n// One.\n// Two.\n// Three.\n// Four.\n// Five.\n", 1))
		b.click("Copy for agent", byText("#prs .reviewbox button", "Copy for agent…"))
		b.waitFor("the note at its new line", `document.querySelector('.copypanel pre')?.textContent.includes('internal/auth/redirect.go:14\n')`)
		b.key("Escape", kb.Escape)
		b.press("prsTab")
		b.waitFor("Branch changes again", redirect+`?.nextElementSibling?.matches('.thr.note')`)

		// The agent changes the noted line: the note is outdated and keeps the old line.
		write(t, filepath.Join(repo, session), sessionGo("sess, ok := s.lookup(c.Value)", strings.Replace(lookupGo, "sess, ok := s.byID[id]", "sess, ok := s.copyOf(id)", 1)))
		b.waitFor("the note outdated", `[...document.querySelectorAll('.thr.note')].some(n => [...n.querySelectorAll('.chip')].some(c => c.textContent === 'Outdated') && n.querySelector('.was')?.textContent === '\tsess, ok := s.byID[id]')`)

		// Copy for agent.
		b.evalJS(`window.e2eFetches = []; { const f = window.fetch; window.fetch = (u, o) => f(u, o).then(r => (e2eFetches.push(u + ' ' + r.status), r)); }`)
		b.evalJS(`navigator.clipboard.writeText = () => Promise.reject(new Error('denied'))`)
		b.click("Copy for agent", byText("#prs .reviewbox button", "Copy for agent…"))
		b.waitFor("the panel with both notes checked", `[...document.querySelectorAll('.copypanel li input')].filter(c => c.checked).length === 2`)
		prompt := "Review notes for branch fix-login (PR #482).\nFix each one, then say what you changed.\n\n" +
			"internal/auth/redirect.go:14\n  Keep next only when it is a path on this site. A //evil.com path passes now.\n\n" +
			fmt.Sprintf("internal/auth/session.go:%d\n  Return a copy, not the pointer.", lineOf(sessionGo("", lookupGo), "\tsess, ok := s.byID[id]"))
		b.waitFor("the prompt", `document.querySelector('.copypanel pre').textContent === `+jsString(prompt))
		b.click("Copy 2", byText(".copypanel button", "Copy 2"))
		b.waitFor("the prompt selected when the clipboard is refused", `document.querySelector('.copypanel') && getSelection().toString().trim() === document.querySelector('.copypanel pre').textContent.trim()`)
		b.key("Escape", kb.Escape)
		b.waitFor("the panel closed", `!document.querySelector('.copypanel')`)
		b.evalJS(`navigator.clipboard.writeText = t => (window.e2eClip = t, Promise.resolve())`)
		b.click("Copy for agent", byText("#prs .reviewbox button", "Copy for agent…"))
		b.waitFor("the first box focused", `document.activeElement?.matches('.copypanel li input')`)
		b.key("Space", " ")
		b.waitFor("one note picked", `document.querySelector('.copypanel .acts .muted').textContent === '1 of 2 picked'`)
		b.key("Space", " ")
		b.waitFor("two notes picked", `document.querySelector('.copypanel .acts .muted').textContent === '2 of 2 picked'`)
		b.key("Enter", "\r")
		b.waitFor("the prompt on the clipboard", `window.e2eClip === `+jsString(prompt))
		b.waitFor("the notes copied", `document.querySelector('#prs .reviewbox .head .muted')?.textContent === '2 · 2 copied' && document.querySelectorAll('.thr.note.copied').length === 2`)
		b.waitFor("the status bar", `document.querySelector('#statusbar').textContent.includes('Copied 2 notes')`)
		b.waitFor("the copy counted by the local kijk", `e2eFetches.includes('/api/count?name=agent-copy 200')`)
		b.waitFor("the copy read aloud", `document.querySelector('#announce').textContent === 'Copied 2 notes'`)
		if os.Getenv("KIJK_E2E_SHOTS") != "" {
			b.evalJS(`[...document.querySelectorAll('.thr.note')].at(-1).scrollIntoView({block: 'center'})`)
			b.act("wait for the layout", chromedp.Sleep(300*time.Millisecond))
			b.screenshot(e2eOut(t), "branch-copied")
		}

		// A second round: only the new note is picked, and Clear copied keeps it.
		binary := boxRow("README.md", "Then run the binary.")
		b.click("a line of README.md", binary+`.querySelector('.tx')`)
		b.press("addNote")
		b.typeText("Say how to build it.")
		b.press("submitNote")
		b.waitFor("the third note", binary+`.nextElementSibling?.matches('.thr.note:not(.editing)')`)
		b.click("Copy for agent", byText("#prs .reviewbox button", "Copy for agent…"))
		b.waitFor("only the new note picked", `document.querySelector('.copypanel .acts .muted')?.textContent === '1 of 3 picked' && document.querySelector('.copypanel li input:checked + .what .where').textContent === 'README.md:8'`)
		b.key("Escape", kb.Escape)
		b.click("Clear copied", byText("#prs .reviewbox button", "Clear copied"))
		b.waitFor("the new note kept", `document.querySelector('#prs .reviewbox .head .muted')?.textContent === '1' && document.querySelectorAll('.thr.note').length === 1 && `+binary+`.nextElementSibling?.matches('.thr.note')`)
		b.click("Delete", binary+`.nextElementSibling.querySelector('[aria-label=Delete]')`)
		b.waitFor("no notes", `document.querySelector('#prs .reviewbox .head .muted')?.textContent === 'none yet' && !document.querySelector('.thr.note')`)

		// A note on a line the diff no longer shows keeps that line in view.
		check := boxRow(session, "func check16() bool {")
		b.hover("a line next to the change", check)
		b.click("+", check+`.querySelector('.addc')`)
		b.typeText("Name this for what it checks.")
		b.press("submitNote")
		b.waitFor("the note", check+`.nextElementSibling?.matches('.thr.note')`)
		write(t, filepath.Join(repo, session), sessionGo("sess, ok := s.lookup(c.Value)", ""))
		b.waitFor("its line as a hunk of its own", `!`+boxHas(session, "add", "// lookup reads a session without touching its expiry.")+` && `+check+`?.previousElementSibling?.matches('.sep') && `+check+`.nextElementSibling?.matches('.thr.note')`)
		b.click("Delete", check+`.nextElementSibling.querySelector('[aria-label=Delete]')`)
		b.waitFor("the note deleted", `!document.querySelector('.thr.note') && !`+check)

		// All pushed: no bar and no dashed marks.
		run(t, repo, "add", "-A")
		run(t, repo, "commit", "-qm", "All of it")
		run(t, repo, "push", "-q")
		b.evalJS(`document.querySelector('.allfiles').scrollTop = 0`)
		b.waitFor("no bar", `document.querySelector('#prs .prfiles') && !document.querySelector('#prs .unpushed')`)
		b.waitFor("no dashed marks", `!document.querySelector('#prs .np, .fhead .np') && !`+fileBox("README.md")+`.querySelector('.ln.np, .np-tag')`)

		if os.Getenv("KIJK_E2E_SHOTS") != "" {
			write(t, filepath.Join(repo, session), sessionGo("sess, ok := s.lookup(c.Value)", strings.Replace(lookupGo, "sess, ok := s.byID[id]", "sess, ok := s.copyOf(id)", 1)))
			copyOf := boxRow(session, "\tsess, ok := s.copyOf(id)")
			b.waitFor("the line", copyOf)
			b.hover("a line", copyOf)
			b.click("+", copyOf+`.querySelector('.addc')`)
			b.typeText("Say why a copy is needed.")
			b.press("submitNote")
			b.click("session.go in the list", prsRow("session.go"))
			dir := e2eOut(t)
			for _, s := range []struct {
				name, scheme string
				width        int64
			}{{"light", "light", 1400}, {"dark", "dark", 1400}, {"narrow", "light", 800}} {
				b.emulate(s.scheme, s.width)
				b.act("wait for the layout", chromedp.Sleep(300*time.Millisecond))
				b.screenshot(dir, "branch-"+s.name)
			}
			b.emulate("light", 1400)
			b.click("Copy for agent", byText("#prs .reviewbox button", "Copy for agent…"))
			b.screenshot(dir, "branch-copy")
			b.key("Escape", kb.Escape)
		}

		// On GitLab the tab says MRs, and the prompt names the merge request.
		run(t, repo, "config", "remote.origin.url", "https://gitlab.com/acme/review.git")
		b.act("reload", chromedp.Reload())
		b.waitFor("the MRs tab", prsTab+`.dataset.tip.startsWith('MRs\n') && document.querySelector('#prs .pr-num')?.textContent === '!12'`)
		b.waitFor("Branch changes", binary)
		b.click("a line of README.md", binary+`.querySelector('.tx')`)
		b.press("addNote")
		b.typeText("Say how to build it.")
		b.press("submitNote")
		b.click("Copy for agent", byText("#prs .reviewbox button", "Copy for agent…"))
		b.waitFor("the merge request in the prompt", `document.querySelector('.copypanel pre')?.textContent.startsWith('Review notes for branch fix-login (MR !12).\n')`)
		b.key("Escape", kb.Escape)
		b.noErrors()
	})

	t.Run("pull requests", func(t *testing.T) {
		repo, origin := reviewRepo(t)
		forge := newFakeForge(t)
		reviewForge(t, forge, repo, origin, true)
		claude := t.TempDir()
		claudeAgent(t, claude, repo, "Fix the login", "idle")
		k := startKijkIn(t, exe, t.TempDir(), "CLAUDE_CONFIG_DIR="+claude, forge.env())
		b := openPage(t, newTab(t, tab), k, "/")
		b.selectAgent("Fix the login", "review")

		// The list, and the count of pull requests that wait for you, also while the tab is hidden.
		b.waitFor("the count on the tab", prsTab+`.querySelector('.tabcount')?.textContent === '2' && !`+prsTab+`.classList.contains('active')`)
		b.press("prsTab")
		b.waitFor("the list", `!document.querySelector('#prs-list').hidden && [...document.querySelectorAll('#prs-list .section-toggle')].map(s => s.textContent).join('|') === 'This branch|Review requested 2|Yours 1|All open 5'`)
		b.waitFor("this branch first", prRow(482)+`?.closest('ul') === document.querySelector('#prs-list ul') && `+prRow(482)+`.querySelector('.prt').textContent === 'Fix login redirect loop'`)
		b.waitFor("review requested", `[...document.querySelectorAll('#prs-list ul')][1]?.textContent.includes('#477') && [...document.querySelectorAll('#prs-list ul')][1].textContent.includes('#471')`)
		b.waitFor("row details", prRow(477)+`.querySelector('.ci.fail') && `+prRow(477)+`.querySelector('.who').textContent.startsWith('sanne · ') && `+prRow(477)+`.querySelector('.lc-add').textContent === '+37'`)
		b.waitFor("all open folded", `document.querySelectorAll('#prs-list li.prrow').length === 4`)
		b.click("All open", byText("#prs-list .section-toggle", "All open 5"))
		b.waitFor("all open shown", `document.querySelectorAll('#prs-list li.prrow').length === 9 && `+prRow(463)+`.querySelector('.prstate.draft')`)
		shots(b, "prs-list")
		b.click("the search box", `document.querySelector('#prs-list .prsearch input')`)
		b.typeText("webhook")
		b.waitFor("one match", `document.querySelectorAll('#prs-list li.prrow').length === 1 && `+prRow(477))
		b.key("Escape", kb.Escape)
		b.waitFor("the search cleared", `document.querySelector('#prs-list .prsearch input').value === '' && document.querySelectorAll('#prs-list li.prrow').length === 9`)

		// A teammate's pull request.
		b.click("#477", prRow(477))
		b.waitFor("its header", `!document.querySelector('#prs-pr').hidden && document.querySelector('#prs-pr .pr-num')?.textContent === '#477' && document.querySelector('#prs-pr .pr-title')?.textContent === 'Retry webhook delivery with backoff'`)
		b.waitFor("author and branches", `document.querySelector('#prs-pr .prmeta')?.textContent === 'Ssanneretry-webhooksmain'`)
		b.waitFor("its chips", `[...document.querySelectorAll('#prs-pr .chips .chip')].map(c => c.textContent).join('|') === '✗ Checks failed|✓ 1 approval'`)
		b.waitFor("the website link named", `document.querySelector('#prs-pr .pr-link')?.getAttribute('aria-label') === 'Open on GitHub'`)
		b.waitFor("no switch on a teammate's pull request", `!document.querySelector('#prs-pr .prmode')`)
		b.waitFor("Overview and the files", `[...document.querySelectorAll('#prs-pr .prfiles li')].map(l => l.querySelector('.name').textContent).join('|') === 'Overview|ci.yml|README.md|retry.go'`)
		b.waitFor("the Changes tab", `(t => t?.firstChild.textContent === 'Changes' && t.querySelector('.muted')?.textContent === '#477')(document.querySelector('#filetabs .filetab.active'))`)
		b.waitFor("three boxes", `document.querySelectorAll('#viewer .fbox').length === 3`)
		b.waitFor("the forge diff", boxHas("internal/webhook/retry.go", "add", "\t\tif !retryable(err) {")+` && `+boxHas("README.md", "add", "Webhooks retry five times."))
		if got := gitOut(t, repo, "rev-parse", "refs/kijk/pr/477"); got != gitOut(t, origin, "rev-parse", "refs/pull/477/head") {
			t.Fatalf("refs/kijk/pr/477 is %q", got)
		}
		if out := gitOut(t, repo, "branch", "--list"); out != "* fix-login\n  main" {
			t.Fatalf("branches changed: %q", out)
		}
		// Threads: open under its line, resolved folded to one line, outdated in the bar.
		const retry = "internal/webhook/retry.go"
		open := boxRow(retry, "\t\tif !retryable(err) {") + `.nextElementSibling`
		b.waitFor("the open thread", open+`?.matches('.thr') && `+open+`.querySelector('.thead').textContent === 'Open thread · line 15· 3 commentsResolve' && `+open+`.querySelectorAll('.c').length === 3`)
		b.waitFor("its comments as markdown", open+`.querySelector('.c .cbody p')?.textContent === 'Should a 429 count as retryable?'`)
		resolved := boxRow(retry, "\t\tselect {") + `.nextElementSibling`
		b.waitFor("the resolved thread folded", resolved+`?.matches('.thr.folded') && `+resolved+`.textContent === 'Resolved · priya: Check ctx before the first sleep? · 2 replies'`)
		b.click("the resolved thread", resolved)
		b.waitFor("the resolved thread open", resolved+`?.matches('.thr.resolved') && `+resolved+`.querySelectorAll('.c').length === 3`)
		b.click("Fold", resolved+`.querySelector('.thead button.all')`)
		b.waitFor("folded again", resolved+`?.matches('.thr.folded')`)
		b.waitFor("the outdated bar", fileBox(retry)+`.querySelector('.obar')?.textContent.startsWith('2 outdated threads · their lines changed after they were written')`)
		b.click("retry.go in the list", prsRow("retry.go"))
		b.click("Show", fileBox(retry)+`.querySelector('.obar button')`)
		b.waitFor("the outdated thread with its old line", fileBox(retry)+`.querySelector('.obar + .thr .thead')?.textContent.startsWith('Outdated thread · line 20') && `+fileBox(retry)+`.querySelector('.obar + .thr .was')?.textContent === '\ttime.Sleep(time.Second)'`)
		b.waitFor("a resolved outdated thread folded in the bar", `(t => t?.matches('.thr.folded') && t.textContent === 'Resolved · priya: Log less here. · 1 reply')(`+fileBox(retry)+`.querySelector('.obar').nextElementSibling.nextElementSibling)`)
		b.waitFor("thread counts", fileBox(retry)+`.querySelector('.fhead .tc')?.textContent === '4' && `+prsRow("retry.go")+`.querySelector('.tc')?.textContent === '4'`)
		b.evalJS(`document.querySelector('.allfiles').scrollTop = 0`)
		b.evalJS(`document.querySelector('.allfiles').focus()`)
		b.press("nextNote")
		b.waitFor("the first thread below the top", `(s => [...s.querySelectorAll('.thr')].some(n => Math.abs(n.getBoundingClientRect().top - s.getBoundingClientRect().top - 124) < 2))(document.querySelector('.allfiles'))`)
		first := evalJS[float64](b, `document.querySelector('.allfiles').scrollTop`)
		b.press("nextNote")
		b.waitFor("the next thread", `document.querySelector('.allfiles').scrollTop > `+fmt.Sprint(first))
		b.press("prevNote")
		b.waitFor("the thread before", `document.querySelector('.allfiles').scrollTop === `+fmt.Sprint(first))
		shots(b, "prs-changes")
		b.click("README.md in the list", prsRow("README.md"))
		b.waitFor("README.md at the top", `(s => s.scrollTop > 0 && Math.abs(`+fileBox("README.md")+`.offsetTop - 12 - s.scrollTop) < 2)(document.querySelector('.allfiles'))`)
		b.click("Open the whole file", fileBox("internal/webhook/retry.go")+`.querySelector('[aria-label="Open the whole file"]')`)
		b.waitFor("retry.go at #477", `(t => t?.firstChild.textContent === 'retry.go' && t.querySelector('.muted')?.textContent === '#477')(document.querySelector('#filetabs .filetab.active')) && `+viewHas("func (s *Sender) deliver(ctx context.Context, ev Event) error {"))

		// The Overview: who wants what, reviewers, the description, checks as a graph, the conversation.
		// The log cannot be read at first, as while its run still goes; a refresh reads it.
		logFile := filepath.Join(forge.dir, "gh-log-303.txt")
		if err := os.Rename(logFile, logFile+".later"); err != nil {
			t.Fatal(err)
		}
		b.click("Overview", prsRow("Overview"))
		b.waitFor("the log not read yet", `document.querySelector('.md.ov .joblog pre')?.textContent.startsWith('gh: no answer for')`)
		if err := os.Rename(logFile+".later", logFile); err != nil {
			t.Fatal(err)
		}
		b.press("refreshPrs")
		b.waitFor("the Overview tab", `(t => t?.firstChild.textContent === 'Overview' && t.querySelector('.muted')?.textContent === '#477')(document.querySelector('#filetabs .filetab.active'))`)
		b.waitFor("title and meta", `document.querySelector('.md.ov h1')?.textContent === 'Retry webhook delivery with backoff #477' && document.querySelector('.md.ov .meta').textContent.includes('sanne wants to merge') && document.querySelector('.md.ov .meta').textContent.includes('· 2 commits')`)
		b.waitFor("the reviewers", `[...document.querySelectorAll('.md.ov .person')].map(p => p.textContent + ':' + p.dataset.tip.split('\n')[1]).join('|') === 'Ppriya:Approved|Ttim:Commented|Yyou:Review requested'`)
		b.waitFor("the description", `document.querySelector('.md.ov .desc strong')?.textContent === 'one' && document.querySelector('.md.ov .desc li')?.textContent === 'Retry up to five times.'`)
		b.waitFor("the checks graph", `(d => d && [...d.querySelectorAll('.dagcol')].map(c => [...c.querySelectorAll('.job')].map(j => j.textContent).join('+')).join(' | ') === 'build | test (linux)+test (windows)')(document.querySelector('.md.ov .dag'))`)
		b.waitFor("the failed job and the lines", `document.querySelector('.md.ov .job.bad')?.textContent === 'test (windows)' && document.querySelectorAll('.md.ov .dag .edges path').length === 2`)
		b.waitFor("the run's link named", `document.querySelector('.md.ov .dagtitle a')?.getAttribute('aria-label') === 'Open the run on GitHub'`)
		b.waitFor("the checks summary", `[...document.querySelectorAll('.md.ov h2')].find(h => h.firstChild.textContent === 'Checks')?.querySelector('.muted').textContent === '1 failed · 1 cancelled'`)
		b.waitFor("other checks", `[...document.querySelectorAll('.md.ov .ovlist.other li')].map(l => l.textContent).join('|').startsWith('Vercel – Preview')`)
		b.waitFor("the end of the failed log", `document.querySelector('.md.ov .joblog .jlhead b')?.textContent === 'test (windows)' && document.querySelector('.md.ov .joblog pre')?.textContent.endsWith('--- FAIL: TestBackoffJitter (0.00s)') && !document.querySelector('.md.ov .joblog button')`)
		b.waitFor("the conversation", `document.querySelector('.md.ov .thr.conv .cbody')?.textContent.trim() === 'Do we want the attempt number in the delivery log?'`)
		shots(b, "prs-overview")
		b.evalJS(byText(".md.ov .h2mode button", "List") + `.focus()`)
		b.key("Enter", "\r")
		b.waitFor("the checks as a list", `!document.querySelector('.md.ov .dag') && document.querySelectorAll('.md.ov .ovlist.jobs li').length === 3`)
		b.waitFor("the focus still on List", `document.activeElement?.matches('.md.ov .h2mode button') && document.activeElement.textContent === 'List'`)
		b.click("Graph", byText(".md.ov .h2mode button", "Graph"))
		b.waitFor("the graph again", `document.querySelector('.md.ov .dag')`)

		// The list again, with the button and with Alt+←.
		b.click("back", `document.querySelector('#prs-pr .back')`)
		b.waitFor("the list again", `!document.querySelector('#prs-list').hidden && document.querySelector('#prs-pr').hidden`)
		b.click("#477", prRow(477))
		b.waitFor("#477 again", `document.querySelector('#prs-pr .pr-num')?.textContent === '#477'`)
		b.evalJS(`document.querySelector('#prs-pr .prfiles li[tabindex="0"]').focus()`)
		b.press("prsBack")
		b.waitFor("the list again", `!document.querySelector('#prs-list').hidden`)

		// Your own branch: Local, and As on GitHub.
		b.click("this branch", prRow(482))
		b.waitFor("its switch", `[...document.querySelectorAll('#prs-branch .prmode button')].map(x => x.textContent + ':' + x.getAttribute('aria-pressed')).join('|') === 'Local:true|As on GitHub:false'`)
		b.waitFor("Branch changes", `document.querySelector('#filetabs .filetab.active')?.textContent.startsWith('Branch changes') && document.querySelectorAll('#viewer .fbox').length === 6`)
		b.click("As on GitHub", byText("#prs-branch .prmode button", "As on GitHub"))
		b.waitFor("the forge diff of #482", `!document.querySelector('#prs-pr').hidden && document.querySelector('#prs-pr .pr-num')?.textContent === '#482'`)
		b.waitFor("pushed files only", `document.querySelectorAll('#prs-pr .prfiles li').length === 4 && (t => t?.firstChild.textContent === 'Changes' && t.querySelector('.muted')?.textContent === '#482')(document.querySelector('#filetabs .filetab.active'))`)
		b.act("reload", chromedp.Reload())
		b.waitFor("the choice kept", `!document.querySelector('#prs-pr').hidden && document.querySelector('#prs-pr .prmode button[aria-pressed=true]')?.textContent === 'As on GitHub'`)
		b.click("Local", byText("#prs-pr .prmode button", "Local"))
		b.waitFor("Local again", `!document.querySelector('#prs-branch').hidden && document.querySelector('#filetabs .filetab.active')?.textContent.startsWith('Branch changes')`)

		// In Local mode sanne's thread is on its line on disk, and its reply strip makes an agent note.
		const session = "internal/auth/session.go"
		asked := boxRow(session, "\tsess, ok := s.lookup(c.Value)")
		b.waitFor("the thread on its line", asked+`?.nextElementSibling?.matches('.thr:not(.note)') && `+asked+`.nextElementSibling.querySelector('.cbody').textContent.trim() === 'Does lookup also check the expiry?'`)
		b.waitFor("the reply strip", asked+`.nextElementSibling.querySelector('.foot input')?.placeholder === 'Note for the agent…' && `+asked+`.nextElementSibling.querySelector('.foot .hint').textContent === 'Reply to sanne in “As on GitHub”'`)
		b.waitFor("counts with the thread", prsRow("session.go")+`.querySelector('.tc')?.textContent === '1'`)
		b.click("the reply strip", asked+`.nextElementSibling.querySelector('.foot input')`)
		b.typeText("Check the expiry in lookup too.")
		b.key("Enter", "\r")
		b.waitFor("an agent note under the thread", `(t => t?.nextElementSibling?.matches('.thr.note') && t.nextElementSibling.querySelector('p').textContent === 'Check the expiry in lookup too.')(`+asked+`.nextElementSibling)`)
		b.waitFor("nothing sent", `!document.querySelector('.thr:not(.note) .foot input')?.value`)
		if calls := strings.Join(forge.calls(), "\n"); strings.Contains(calls, "mutation") {
			t.Fatalf("kijk wrote to the forge:\n%s", calls)
		}
		shots(b, "prs-local")

		// After a reload with the PRs tab open, kijk asks about the pull request again by itself, also
		// when the tab shows before the PR line knows the forge, and with no window focus to wait for.
		stop := b.onEveryPage(`addEventListener('focus', e => e.stopImmediatePropagation(), true);
			for (const k of Object.keys(localStorage)) if (k.startsWith('kijk.pr:')) localStorage.removeItem(k);
			{ const f = window.fetch; window.fetch = (u, o) => String(u).includes('/api/pr?') ? new Promise(r => setTimeout(r, 800)).then(() => f(u, o)) : f(u, o); }`)
		b.act("reload", chromedp.Reload())
		b.waitFor("the thread after a reload", asked+`?.nextElementSibling?.matches('.thr:not(.note)')`)
		stop()

		// Copy for agent lists the open thread too, and marks it copied.
		b.evalJS(`navigator.clipboard.writeText = t => (window.e2eClip = t, Promise.resolve())`)
		b.click("Copy for agent", byText("#prs-branch .reviewbox button", "Copy for agent…"))
		b.waitFor("notes and threads", `[...document.querySelectorAll('.copypanel .label')].map(l => l.textContent).join('|') === 'Agent notes|Threads on GitHub' && [...document.querySelectorAll('.copypanel li input')].filter(c => c.checked).length === 2`)
		at := fmt.Sprint(lineOf(sessionGo("sess, ok := s.lookup(c.Value)", ""), "\tsess, ok := s.lookup(c.Value)"))
		prompt := "Review notes for branch fix-login (PR #482).\nFix each one, then say what you changed.\n\n" +
			session + ":" + at + "\n  Check the expiry in lookup too.\n\n" +
			session + ":" + at + " (sanne on GitHub)\n  Does lookup also check the expiry?"
		b.waitFor("the prompt with the thread", `document.querySelector('.copypanel pre')?.textContent === `+jsString(prompt))
		b.click("Copy 2", byText(".copypanel button", "Copy 2"))
		b.waitFor("on the clipboard", `window.e2eClip === `+jsString(prompt))
		b.waitFor("the status bar", `document.querySelector('#statusbar').textContent.includes('Copied 1 note and 1 thread')`)
		b.click("Copy for agent", byText("#prs-branch .reviewbox button", "Copy for agent…"))
		b.waitFor("the thread copied", `(l => l?.classList.contains('used') && !l.querySelector('input').checked && l.querySelector('.where').textContent.includes('copied'))([...document.querySelectorAll('.copypanel li')].at(-1))`)
		b.key("Escape", kb.Escape)

		// The agent adds lines above the thread while its box is not drawn: the prompt has its line now.
		b.click("close Branch changes", `document.querySelector('#filetabs .filetab.active .close')`)
		b.waitFor("Branch changes closed", `![...document.querySelectorAll('#filetabs .filetab')].some(t => t.textContent.startsWith('Branch changes'))`)
		sessionPath := filepath.Join(repo, session)
		src, err := os.ReadFile(sessionPath)
		if err != nil {
			t.Fatal(err)
		}
		write(t, sessionPath, strings.Replace(string(src), "package auth\n", "package auth\n\n// One.\n// Two.\n// Three.\n// Four.\n", 1))
		b.click("Copy for agent", byText("#prs-branch .reviewbox button", "Copy for agent…"))
		b.waitFor("the thread at its line on disk", `[...document.querySelectorAll('.copypanel li')].at(-1)?.querySelector('.where').textContent === `+jsString(session+":"+fmt.Sprint(lineOf(string(src), "\tsess, ok := s.lookup(c.Value)")+5)+" · sanne · copied"))
		b.key("Escape", kb.Escape)
		write(t, sessionPath, string(src))
		b.press("prsTab")

		// A failed job of your own branch goes to the agent.
		b.click("Overview", prsRow("Overview"))
		b.waitFor("the failed job of #482", `document.querySelector('.md.ov .joblog .jlhead b')?.textContent === 'test (linux)'`)
		b.click("Copy for agent", `document.querySelector('.md.ov .joblog button')`)
		b.waitFor("the failure on the clipboard", `window.e2eClip === `+jsString("CI job test (linux) failed on branch fix-login (PR #482).\nFix it, then say what you changed.\n\n=== RUN   TestResume\n    session_test.go:12: got nil session\n--- FAIL: TestResume (0.00s)"))
		b.waitFor("the status bar", `document.querySelector('#statusbar').textContent.includes('Copied the failure of test (linux)')`)

		// Refresh asks the forge again.
		before := forge.settle() // a call that runs takes in the refresh
		b.press("refreshPrs")
		forge.waitFor(t, before, "pullRequest(number")

		// The PR line's title opens the PRs tab on its pull request; ↗ still goes to the website.
		b.click("the Git tab", gitTab)
		b.waitFor("the PR line", `document.querySelector('#prline .pr-title')?.textContent === 'Fix login redirect loop' && document.querySelector('#prline .pr-link')?.href === 'https://github.com/acme/review/pull/482'`)
		b.waitFor("the website link's tooltip", `(a => a.dataset.tip === 'Open on GitHub' && !a.title)([...document.querySelectorAll('#prline .pr-link')].at(-1))`)
		b.click("the PR line's title", `document.querySelector('#prline .pr-title')`)
		b.waitFor("the PRs tab on #482", prsTab+`.classList.contains('active') && !document.querySelector('#prs-branch').hidden && document.querySelector('#prs-branch .pr-num')?.textContent === '#482'`)
		b.noErrors()

		// kijk <PR link> opens the pull request in the folders window, in the clone kijk knows.
		f := openPage(t, newTab(t, tab), k, "/?window=folders")
		f.waitFor("the folders window", `document.title === 'kijk · Folders'`)
		if out := runCmd(t, k.env, exe, "-port", fmt.Sprint(k.port), "-no-open", "https://github.com/acme/review/pull/477"); !strings.Contains(out, "Added review") {
			t.Fatalf("kijk <PR link> said:\n%s", out)
		}
		f.waitFor("#477 in the folders window", `!document.querySelector('#prs-pr').hidden && document.querySelector('#prs-pr .pr-num')?.textContent === '#477' && (t => t?.firstChild.textContent === 'Changes' && t.querySelector('.muted')?.textContent === '#477')(document.querySelector('#filetabs .filetab.active'))`)
		// Not logged in to gh: the tab opens on This branch and says why there is no list.
		forge.set(t, "gh-error.txt", "To get started with GitHub CLI, please run:  gh auth login")
		b.act("reload", chromedp.Reload())
		b.press("prsTab")
		b.waitFor("This branch and the note", `!document.querySelector('#prs-branch').hidden && document.querySelector('#prs-branch .pr-note')?.textContent === 'Log in with gh auth login to see the status' && !document.querySelector('#prs-branch .back')`)
		// On the main line there is no This branch, so the list's place says it.
		run(t, repo, "stash", "-q", "-u")
		run(t, repo, "checkout", "-q", "main")
		b.waitFor("the note on main", `!document.querySelector('#prs-list').hidden && document.querySelector('#prs-list .note')?.textContent === 'Log in with gh auth login to see the status'`)
		run(t, repo, "checkout", "-q", "fix-login")
		run(t, repo, "stash", "pop", "-q")
		os.Remove(filepath.Join(forge.dir, "gh-error.txt"))

		cmd := exec.Command(exe, "-port", fmt.Sprint(k.port), "-no-open", "https://github.com/acme/billing/pull/12")
		cmd.Env = append(os.Environ(), k.env...)
		out, err := cmd.CombinedOutput()
		if cmd.ProcessState.ExitCode() != 1 || !strings.Contains(string(out), "No folder of github.com/acme/billing is in kijk. Add a folder of this repo first: kijk <folder>") {
			t.Fatalf("a link to a repo kijk has no clone of: %v\n%s", err, out)
		}
		f.noErrors()
	})

	t.Run("reviews", func(t *testing.T) {
		repo, origin := reviewRepo(t)
		forge := newFakeForge(t)
		reviewForge(t, forge, repo, origin, false)
		theirs := gitOut(t, origin, "rev-parse", "refs/pull/477/head") // moves when someone pushes
		main := gitOut(t, repo, "rev-parse", "origin/main")
		const retry = "internal/webhook/retry.go"
		errAt := lineOf(retryGo, "\tvar err error")
		// What GitHub holds: a draft written on the website, and after each write what it did.
		web := ghThread("T7", "README.md", 5, 0, false, false, "", ghDraft("C40", "Say how long it waits too."))
		answer := func(edit func([]obj) []obj) obj {
			return pr477(theirs, main, func(ts []obj) []obj { return edit(append(ts, web)) })
		}
		same := func(ts []obj) []obj { return ts }
		mine := func(body string) obj { return ghThread("T8", retry, errAt, 0, false, false, "", ghDraft("C50", body)) }
		// target is the answer a write asks first: the head, and your pending review with its summary.
		targetWith := func(head, review, summary string) obj {
			nodes := []obj{}
			if review != "" {
				nodes = []obj{{"id": review, "viewerDidAuthor": true, "commit": obj{"oid": head}, "body": summary}}
			}
			return ghData(obj{"repository": obj{"pullRequest": obj{"id": "PR_477", "headRefOid": head, "reviews": obj{"nodes": nodes}}}})
		}
		target := func(head, review string) obj { return targetWith(head, review, "") }
		// withSummary gives an answer about #477 your pending review's body.
		withSummary := func(o obj, body string) obj {
			o["data"].(obj)["repository"].(obj)["pullRequest"].(obj)["pending"] = obj{"nodes": []obj{{"body": body, "viewerDidAuthor": true}}}
			return o
		}
		forge.set(t, "gh-pr-477.json", answer(same))
		forge.set(t, "gh-target.json", target(theirs, ""))
		forge.set(t, "gh-review.json", ghData(obj{"addPullRequestReview": obj{"pullRequestReview": obj{"id": "PRR_1"}}}))
		forge.set(t, "gh-thread.json", ghData(obj{"addPullRequestReviewThread": obj{"thread": obj{"id": "T8", "comments": obj{"nodes": []obj{{"id": "C50"}}}}}}))
		forge.set(t, "gh-reply.json", ghData(obj{"addPullRequestReviewThreadReply": obj{"comment": obj{"id": "C51"}}}))
		for _, m := range []string{"edit", "delete", "resolve", "unresolve", "submit", "update"} {
			forge.set(t, "gh-"+m+".json", ghData(obj{}))
		}
		claude := t.TempDir()
		claudeAgent(t, claude, repo, "Fix the login", "idle")
		k := startKijkIn(t, exe, t.TempDir(), "CLAUDE_CONFIG_DIR="+claude, forge.env())
		b := openPage(t, newTab(t, tab), k, "/")
		b.selectAgent("Fix the login", "review")
		b.waitFor("the PRs tab", prsTab+`.querySelector('.tabcount')?.textContent === '2'`)
		b.press("prsTab")
		b.click("#477", prRow(477))
		open := boxRow(retry, "\t\tif !retryable(err) {") + `?.nextElementSibling`
		b.waitFor("Resolve and a reply strip on the open thread", open+`?.querySelector('.thead button')?.textContent === 'Resolve' && `+open+`.querySelector('.foot input')?.placeholder === 'Reply…'`)

		// A draft written on the website shows as yours, marked.
		readme := boxRow("README.md", "Webhooks retry five times.") + `?.nextElementSibling`
		b.waitFor("the draft from GitHub", readme+`?.matches('.thr.draftonly') && `+readme+`.querySelector('.thead').textContent === 'New comment · line 5' && `+
			readme+`.querySelector('.c.mine .chip.draft') && `+readme+`.querySelector('.c.mine .who').textContent.includes('· written on GitHub')`)
		b.waitFor("drafts counted with threads", prsRow("README.md")+`.querySelector('.tc')?.dataset.tip === '1 draft'`)

		// + on a line opens the editor; the comment goes into a new pending review at the head kijk showed.
		row := boxRow(retry, "\tvar err error")
		b.hover("a line", row)
		b.click("+", row+`.querySelector('.addc')`)
		editor := row + `?.nextElementSibling`
		b.waitFor("the editor", editor+`?.matches('.thr.draftonly.editing') && `+editor+`.querySelector('.thead').textContent === 'New comment · line 10' && document.activeElement === `+editor+`.querySelector('textarea')`)
		forge.after(t, "review", "gh-target.json", target(theirs, "PRR_1"))
		forge.after(t, "thread", "gh-pr-477.json", answer(func(ts []obj) []obj { return append(ts, mine("Name the error.")) }))
		n := forge.settle()
		b.typeText("Name the error.")
		b.press("submitNote")
		b.waitFor("the draft under its line", editor+`?.matches('.thr.draftonly:not(.editing)') && `+editor+`.querySelector('.cbody')?.textContent.trim() === 'Name the error.' && !`+editor+`.querySelector('.who').textContent.includes('written on')`)
		calls := strings.Join(forge.calls()[n:], "\n")
		if !strings.Contains(calls, `{"commit":"`+theirs+`","pr":"PR_477"}`) || !strings.Contains(calls, `{"body":"Name the error.","line":10,"path":"`+retry+`","review":"PRR_1","side":"RIGHT"}`) {
			t.Fatalf("the comment's calls:\n%s", calls)
		}

		// A reply goes into the pending review too.
		forge.after(t, "reply", "gh-pr-477.json", answer(func(ts []obj) []obj {
			c := ts[0]["comments"].(obj)
			c["nodes"] = append(c["nodes"].([]obj), ghDraft("C51", "Cap Retry-After at MaxDelay too."))
			return append(ts, mine("Name the error."))
		}))
		b.click("the reply strip", open+`.querySelector('.foot input')`)
		b.typeText("Cap Retry-After at MaxDelay too.")
		b.key("Enter", "\r")
		forge.waitFor(t, n, `"review":"PRR_1","thread":"T1"}`)
		b.waitFor("the reply as a draft", `(c => c?.matches('.c.mine') && c.querySelector('.chip.draft') && c.querySelector('.cbody').textContent.trim() === 'Cap Retry-After at MaxDelay too.')([...`+open+`.querySelectorAll('.c')].at(-1))`)
		b.waitFor("the strip empty", open+`.querySelector('.foot input').value === ''`)

		// Edit a draft, and delete the one from the website.
		forge.after(t, "edit", "gh-pr-477.json", answer(func(ts []obj) []obj { return append(ts, mine("Name the error: ev.ID.")) }))
		b.evalJS(editor + `.scrollIntoView({block: 'center'})`) // not under the sticky file header
		b.click("Edit", editor+`.querySelector('[aria-label=Edit]')`)
		b.waitFor("the draft in a text box", `document.activeElement?.matches('.thr textarea') && document.activeElement.value === 'Name the error.'`)
		b.evalJS(`document.activeElement.select()`)
		b.typeText("Name the error: ev.ID.")
		b.press("submitNote")
		forge.waitFor(t, n, `{"body":"Name the error: ev.ID.","id":"C50"}`)
		b.waitFor("the new text", editor+`?.querySelector('.cbody')?.textContent.trim() === 'Name the error: ev.ID.' && !`+editor+`.querySelector('textarea')`)
		forge.after(t, "delete", "gh-pr-477.json", pr477(theirs, main, func(ts []obj) []obj { return append(ts, mine("Name the error: ev.ID.")) }))
		b.evalJS(readme + `.scrollIntoView({block: 'center'})`)
		b.click("Delete", readme+`.querySelector('[aria-label=Delete]')`)
		forge.waitFor(t, n, `{"id":"C40"}`)
		b.waitFor("the website's draft gone", `!`+fileBox("README.md")+`.querySelector('.thr') && document.querySelector('#prs-pr .reviewbox .head').textContent === 'Your review1 draft'`)

		// Resolve and unresolve happen at once.
		resolved := func(on bool) obj {
			return pr477(theirs, main, func(ts []obj) []obj {
				ts[0]["isResolved"], ts[0]["viewerCanResolve"], ts[0]["viewerCanUnresolve"] = on, !on, on
				return append(ts, mine("Name the error: ev.ID."))
			})
		}
		forge.after(t, "resolve", "gh-pr-477.json", resolved(true))
		b.evalJS(open + `.scrollIntoView({block: 'center'})`)
		b.click("Resolve", open+`.querySelector('.thead button')`)
		forge.waitFor(t, n, `resolveReviewThread(input: {threadId: $thread}) { thread { id } } }","variables":{"thread":"T1"}`)
		b.waitFor("the thread folded", open+`?.matches('.thr.folded') && `+open+`.textContent === 'Resolved · tim: Should a 429 count as retryable? · 2 replies'`)
		b.click("the resolved thread", open)
		forge.after(t, "unresolve", "gh-pr-477.json", resolved(false))
		b.click("Unresolve", byText("#viewer .thr.resolved .thead button", "Unresolve"))
		forge.waitFor(t, n, "unresolveReviewThread(")
		b.waitFor("the thread open again", open+`?.matches('.thr:not(.resolved):not(.folded)') && `+open+`.querySelector('.thead button')?.textContent === 'Resolve'`)

		// A resolve the forge refuses says why in the thread.
		forge.set(t, "gh-resolve.json", `{"data":{"resolveReviewThread":null},"errors":[{"message":"Resource not accessible by integration"}]}`)
		b.click("Resolve", open+`.querySelector('.thead button')`)
		b.waitFor("the reason", open+`?.querySelector('.thead + .error')?.textContent === 'gh: Resource not accessible by integration' && !`+open+`.matches('.folded')`)
		forge.set(t, "gh-resolve.json", ghData(obj{}))

		// Escape closes a new comment, and + on the same line gives its text back.
		half := boxRow(retry, "\t\t\treturn ctx.Err()")
		b.evalJS(half + `.scrollIntoView({block: 'center'})`)
		b.hover("a line", half)
		b.click("+", half+`.querySelector('.addc')`)
		b.waitFor("the editor", `document.activeElement?.matches('.thr.editing textarea')`)
		b.typeText("Half a thought")
		b.key("Escape", kb.Escape)
		b.waitFor("the editor closed", `!document.querySelector('.thr.editing')`)
		b.hover("the line again", half)
		b.click("+", half+`.querySelector('.addc')`)
		b.waitFor("the text back", `document.activeElement?.matches('.thr.editing textarea') && document.activeElement.value === 'Half a thought'`)
		b.key("Escape", kb.Escape)
		shots(b, "prs-drafts")

		// The review box counts every draft, also the one from the website.
		full := func() obj {
			return answer(func(ts []obj) []obj {
				c := ts[0]["comments"].(obj)
				c["nodes"] = append(c["nodes"].([]obj), ghDraft("C51", "Cap Retry-After at MaxDelay too."))
				return append(ts, mine("Name the error: ev.ID."))
			})
		}
		forge.set(t, "gh-pr-477.json", full())
		b.press("refreshPrs")
		box := `document.querySelector('#prs-pr .reviewbox')`
		b.waitFor("the review box", box+`?.querySelector('.head').textContent === 'Your review3 drafts · 1 from GitHub' && `+
			`[...`+box+`.querySelectorAll('.mode button')].map(x => x.textContent + ':' + x.getAttribute('aria-pressed') + ':' + x.disabled).join('|') === 'Comment:true:false|Approve:false:false|Request changes:false:false' && `+
			box+`.querySelector('textarea').placeholder === 'Summary (optional)' && `+box+`.querySelector('button.primary').textContent === 'Submit review…'`)

		// The list shows your review in progress.
		b.click("back", `document.querySelector('#prs-pr .back')`)
		b.waitFor("your drafts on the row", prRow(477)+`?.querySelector('.yours')?.textContent === '3 drafts' && !`+prRow(471)+`.querySelector('.yours')`)
		b.click("#477", prRow(477))

		// A comment on the pull request becomes the summary of the review: the body of the pending
		// review on GitHub, so it lasts like every other draft.
		const said = "Looks good, one question on Retry-After."
		forge.after(t, "update", "gh-pr-477.json", withSummary(full(), said))
		forge.after(t, "update", "gh-target.json", targetWith(theirs, "PRR_1", said))
		n = len(forge.calls())
		b.click("Overview", prsRow("Overview"))
		foot := `document.querySelector('.md.ov .thr.conv .foot')`
		b.waitFor("the comment box", foot+`?.querySelector('input').placeholder === 'Comment on the pull request…'`)
		b.click("the comment box", foot+`.querySelector('input')`)
		b.typeText("Looks good, one question on Retry-After.")
		b.waitFor("the comment typed", foot+`.querySelector('input').value === 'Looks good, one question on Retry-After.'`)
		// A read that started before the write answers only after it: what shows is still the write.
		b.evalJS(`{ const f = window.fetch; let wrote; const done = new Promise(r => wrote = r);
			window.fetch = (u, o) => { const p = f(u, o); return String(u).includes('/api/pr/view') ? p.then(r => done.then(() => r)) : String(u).includes('/api/pr/summary') ? p.finally(wrote) : p; }; }`)
		b.press("refreshPrs")
		b.click("Add to review", byText(".md.ov .thr.conv .foot button", "Add to review"))
		forge.waitFor(t, n, `updatePullRequestReview(input: {pullRequestReviewId: $review, body: $body}) { pullRequestReview { id } } }","variables":{"body":"`+said+`","review":"PRR_1"}`)
		b.waitFor("the summary in the box", box+`.querySelector('textarea').value === 'Looks good, one question on Retry-After.' && `+foot+`.querySelector('input').value === ''`)
		b.waitFor("the summary as a draft", `(c => c?.querySelector('.chip.draft') && c.querySelector('.who').textContent.includes('· sent with your review') && c.querySelector('.cbody').textContent === `+jsString(said)+`)(document.querySelector('.md.ov .thr.conv .c.mine.summary'))`)
		shots(b, "prs-overview-review")

		// The box saves its summary on Ctrl+Enter, not on every key.
		const summary = said + " Thanks."
		b.evalJS(`(a => { a.focus(); a.setSelectionRange(a.value.length, a.value.length) })(` + box + `.querySelector('textarea'))`)
		n = len(forge.calls())
		b.typeText(" Thanks.")
		saveKey := map[bool]string{false: "Ctrl", true: "⌘"}[b.mac]
		b.waitFor("not saved yet", box+`.querySelector('.saved').textContent === 'Not saved · `+saveKey+` Enter saves it'`)
		shots(b, "prs-summary-unsaved")
		if strings.Contains(strings.Join(forge.calls()[n:], "\n"), "updatePullRequestReview(") {
			t.Fatal("kijk saved the summary on a key press")
		}
		forge.after(t, "update", "gh-pr-477.json", withSummary(full(), summary))
		forge.after(t, "update", "gh-target.json", targetWith(theirs, "PRR_1", summary))
		b.press("submitNote")
		forge.waitFor(t, n, `{"body":"`+summary+`","review":"PRR_1"}`)
		b.waitFor("saved", box+`.querySelector('.saved').hidden && `+box+`.querySelector('textarea').value === `+jsString(summary))

		// The check lists what goes out; Escape goes back with nothing lost.
		b.click("Approve", byText("#prs-pr .reviewbox .mode button", "Approve"))
		b.click("Submit review…", box+`.querySelector('button.primary')`)
		dialog := `document.querySelector('.confirm[role=dialog]')`
		b.waitFor("the check", dialog+`?.querySelector('h2').textContent === 'Post your review on #477?' && `+dialog+`.querySelector('.sub').textContent === 'Retry webhook delivery with backoff · sanne' && `+
			`[...`+dialog+`.querySelectorAll('li')].map(l => l.textContent).join('|') === 'Verdict✓ Approve|Comments2 on lines · 1 written on GitHub|Replies1, to tim on retry.go:15|Summary`+summary+`' && `+
			dialog+`.querySelector('.acts .muted').textContent === 'Everyone on the pull request sees it at once.' && document.activeElement === `+dialog+`.querySelector('button.primary') && document.querySelector('.backdrop')`)
		shots(b, "prs-check")
		b.key("Escape", kb.Escape)
		b.waitFor("the check closed, nothing lost", `!document.querySelector('.confirm') && !document.querySelector('.backdrop') && `+box+`.querySelector('textarea').value === `+jsString(summary)+` && `+box+`.querySelector('.mode button[aria-pressed=true]').textContent === 'Approve'`)
		if strings.Contains(strings.Join(forge.calls(), "\n"), "submitPullRequestReview(") {
			t.Fatal("Escape posted the review")
		}

		// Each row counts its own drafts from the website: here also the reply.
		b.evalJS(`for (const k of Object.keys(localStorage)) if (k.startsWith('kijk.drafts:') && k.endsWith(':477')) localStorage.setItem(k, JSON.stringify(JSON.parse(localStorage.getItem(k)).filter(id => id !== 'C51')))`)
		b.click("Submit review…", box+`.querySelector('button.primary')`)
		b.waitFor("the website's reply counted", `[...`+dialog+`?.querySelectorAll('li') || []].map(l => l.textContent).slice(1, 3).join('|') === 'Comments2 on lines · 1 written on GitHub|Replies1, to tim on retry.go:15 · 1 written on GitHub'`)
		b.key("Escape", kb.Escape)
		b.waitFor("the check closed", `!document.querySelector('.confirm')`)

		// Enter posts it all as one approving review.
		b.evalJS(`window.e2eCounts = []; { const f = window.fetch; window.fetch = (u, o) => (String(u).includes('/api/count') && e2eCounts.push(String(u)), f(u, o)); }`)
		b.click("Submit review…", box+`.querySelector('button.primary')`)
		b.waitFor("the check", dialog)
		forge.after(t, "submit", "gh-pr-477.json", pr477(theirs, main, same))
		forge.after(t, "submit", "gh-target.json", target(theirs, ""))
		n = len(forge.calls())
		b.key("Enter", "\r")
		forge.waitFor(t, n, `{"body":"`+summary+`","event":"APPROVE","review":"PRR_1"}`)
		b.waitFor("posted", `!document.querySelector('.confirm') && document.querySelector('#statusbar').textContent.includes('Posted your review on #477') && `+
			box+`.querySelector('.head').textContent === 'Your reviewno drafts yet' && `+box+`.querySelector('textarea').value === '' && `+box+`.querySelector('.mode button[aria-pressed=true]').textContent === 'Comment' && `+
			box+`.querySelector('button.primary').disabled && window.e2eCounts.some(u => u.endsWith('name=review'))`)

		// Someone pushed since kijk showed the diff: kijk writes nothing, keeps the text, shows the new
		// head, and moves the editor to its line there. The review went out, so the comment starts a new
		// one on the new head.
		push := filepath.Join(t.TempDir(), "push")
		run(t, t.TempDir(), "clone", "-q", origin, push)
		run(t, push, "fetch", "-q", "origin", "refs/pull/477/head")
		run(t, push, "checkout", "-q", "FETCH_HEAD")
		write(t, filepath.Join(push, retry), strings.Replace(retryGo, "\t}\n\treturn err\n}", "\t}\n\t// Every attempt failed.\n\n\treturn err\n}", 1))
		run(t, push, "commit", "-qam", "Say why")
		run(t, push, "push", "-q", "origin", "HEAD:refs/pull/477/head")
		theirs = gitOut(t, push, "rev-parse", "HEAD")
		forge.set(t, "gh-target.json", target(theirs, ""))
		forge.set(t, "gh-pr-477.json", pr477(theirs, main, same))
		forge.set(t, "gh-review.json", ghData(obj{"addPullRequestReview": obj{"pullRequestReview": obj{"id": "PRR_2"}}}))
		other := boxRow(retry, "\treturn err")
		b.evalJS(other + `.scrollIntoView({block: 'center'})`)
		b.hover("another line", other)
		b.click("+", other+`.querySelector('.addc')`)
		b.waitFor("the editor", `document.activeElement?.matches('.thr.editing textarea')`)
		b.typeText("Wrap it.")
		before := len(forge.calls())
		b.press("submitNote")
		b.waitFor("the reason, on the line's new place", `(e => e && e.querySelector('.error')?.textContent === '#477 has new commits. kijk shows them now; add your comment there.' && e.querySelector('textarea').value === 'Wrap it.' && `+
			`e.querySelector('.thead').textContent === 'New comment · line 26' && e.previousElementSibling?.querySelector('.tx')?.textContent === '\treturn err')(document.querySelector('.thr.editing'))`)
		if strings.Contains(strings.Join(forge.calls()[before:], "\n"), "addPullRequestReviewThread(") {
			t.Fatal("kijk wrote a comment on a head it did not show")
		}
		b.waitFor("the new head", boxHas(retry, "add", "\t// Every attempt failed."))
		b.evalJS(`document.querySelector('.thr.editing textarea').focus()`)
		b.press("submitNote")
		forge.waitFor(t, before, `{"body":"Wrap it.","line":26,"path":"`+retry+`","review":"PRR_2","side":"RIGHT"}`)
		if calls := strings.Join(forge.calls()[before:], "\n"); !strings.Contains(calls, `{"commit":"`+theirs+`","pr":"PR_477"}`) {
			t.Fatalf("the new review is not on the new head:\n%s", calls)
		}
		b.waitFor("the editor closed", `!document.querySelector('.thr.editing')`)

		// kijk cannot fetch a newer head: it shows the head it has and says why.
		run(t, origin, "update-ref", "-d", "refs/pull/477/head")
		forge.set(t, "gh-pr-477.json", pr477(strings.Repeat("e", 40), main, same))
		b.press("refreshPrs")
		b.waitFor("why the head is old", `document.querySelector('#prs-pr .note.error')?.textContent.startsWith('Cannot fetch the new commits of #477 from origin: ') && `+boxHas(retry, "add", "\t// Every attempt failed."))

		// On your own pull request GitHub refuses Approve and Request changes.
		b.click("back", `document.querySelector('#prs-pr .back')`)
		b.click("this branch", prRow(482))
		b.click("As on GitHub", byText("#prs-branch .prmode button", "As on GitHub"))
		b.waitFor("only Comment on your own", `document.querySelector('#prs-pr .pr-num')?.textContent === '#482' && `+
			`[...`+box+`.querySelectorAll('.mode button')].map(x => x.textContent + ':' + x.disabled).join('|') === 'Comment:false|Approve:true|Request changes:true' && `+
			box+`.querySelector('.why')?.textContent === 'You cannot approve or request changes on your own pull request'`)

		// GitHub takes comments only on lines of its own diff: none on lines that Show more lines adds.
		const session = "internal/auth/session.go"
		b.waitFor("session.go", fileBox(session)+`?.querySelector('.ln.sep .more')`)
		b.evalJS(fileBox(session) + `.querySelector('.ln.sep .more').scrollIntoView({block: 'center'})`)
		b.click("Show more lines", fileBox(session)+`.querySelector('.ln.sep .more')`)
		far, near := boxRow(session, "// Store keeps the sessions."), boxRow(session, "\tif err != nil {")
		b.waitFor("a + only near the change", far+` && !`+far+`.querySelector('.addc') && !`+boxRow(session, "\tc, err := r.Cookie(cookieName)")+`.querySelector('.addc') && `+near+`?.querySelector('.addc')`)
		b.noErrors()
	})

	t.Run("reviews on GitLab", func(t *testing.T) {
		repo, origin := reviewRepo(t)
		const url = "https://gitlab.com/acme/review.git"
		run(t, repo, "remote", "set-url", "origin", url)
		run(t, repo, "config", "url."+origin+".insteadOf", url)
		theirs := teammatePR(t, repo, origin)
		run(t, origin, "update-ref", "refs/merge-requests/12/head", theirs)
		main := gitOut(t, repo, "rev-parse", "origin/main")
		forge := newFakeForge(t)
		row := func(iid int, title, author, head string, asked bool) obj {
			return obj{"iid": fmt.Sprint(iid), "title": title, "draft": false, "webUrl": fmt.Sprintf("https://gitlab.com/acme/review/-/merge_requests/%d", iid),
				"createdAt": "2026-10-19T09:00:00Z", "sourceBranch": head, "targetBranch": "main", "author": obj{"username": author},
				"diffStatsSummary": obj{"additions": 37, "deletions": 0}, "headPipeline": nil,
				"reviewers": obj{"nodes": []obj{{"username": "you", "mergeRequestInteraction": obj{"approved": !asked}}}}}
		}
		r12 := row(12, "Retry webhook delivery with backoff", "sanne", "retry-webhooks", true)
		forge.set(t, "glab-list.json", ghData(obj{"currentUser": obj{"username": "you", "reviewRequestedMergeRequests": obj{"nodes": []obj{r12}}},
			"project": obj{"mergeRequests": obj{"count": 2, "nodes": []obj{r12, row(3, "Fix login redirect loop", "you", "fix-login", false)}}}}))
		forge.set(t, "glab-mr-view.json", `{"iid":3,"title":"Fix login redirect loop","state":"opened","draft":false,"web_url":"https://gitlab.com/acme/review/-/merge_requests/3","target_branch":"main"}`)
		mr := obj{}
		for k, v := range r12 {
			mr[k] = v
		}
		mr["description"], mr["state"], mr["diffHeadSha"], mr["commitCount"] = "Retry five times.", "opened", theirs, 1
		mr["diffRefs"] = obj{"baseSha": main, "headSha": theirs, "startSha": main}
		mr["approvedBy"], mr["discussions"] = obj{"nodes": []obj{}}, obj{"nodes": []obj{}}
		forge.set(t, "glab-pr-12.json", ghData(obj{"currentUser": obj{"username": "you"}, "project": obj{"mergeRequest": mr}}))
		forge.set(t, "glab-drafts.json", `[{"id":61,"note":"Thanks for this.","discussion_id":null,"position":null}]`)
		forge.set(t, "glab-version-gitlab.com.json", `{"version":"17.4.1-ee"}`)
		claude := t.TempDir()
		claudeAgent(t, claude, repo, "Fix the login", "idle")
		open := func() page {
			k := startKijkIn(t, exe, t.TempDir(), "CLAUDE_CONFIG_DIR="+claude, forge.env())
			b := openPage(t, newTab(t, tab), k, "/")
			b.selectAgent("Fix the login", "review")
			b.waitFor("the MRs tab", prsTab+`.querySelector('.tabcount')?.textContent === '1'`)
			b.press("prsTab")
			b.click("!12", prRow(12))
			b.waitFor("!12", `document.querySelector('#prs-pr .pr-num')?.textContent === '!12' && document.querySelectorAll('#viewer .fbox').length >= 3`)
			return b
		}

		// GitLab 17.4 takes drafts and approvals, but not Request changes.
		b := open()
		box := `document.querySelector('#prs-pr .reviewbox')`
		b.waitFor("Request changes off", `[...`+box+`?.querySelectorAll('.mode button') || []].map(x => x.textContent + ':' + x.disabled).join('|') === 'Comment:false|Approve:false|Request changes:true' && `+
			box+`.querySelector('.why').textContent === 'Request changes needs GitLab 18.9. This server runs 17.4.' && `+
			byText("#prs-pr .reviewbox .mode button", "Request changes")+`.dataset.tip === 'Request changes needs GitLab 18.9. This server runs 17.4.' && `+
			fileBox("internal/webhook/retry.go")+`.querySelector('.addc')`)
		// A draft is not a comment yet: the Overview row does not count it.
		b.waitFor("a summary from the website", box+`.querySelector('.head').textContent === 'Your reviewno drafts yet' && `+box+`.querySelector('textarea').value === 'Thanks for this.' && `+
			`!`+box+`.querySelector('button.primary').disabled && `+prsRow("Overview")+`.querySelector('.dir').textContent === ''`)

		// Text not saved in the box stays, after the summary that changed on the forge meanwhile.
		b.evalJS(`(a => { a.focus(); a.setSelectionRange(a.value.length, a.value.length) })(` + box + `.querySelector('textarea'))`)
		b.typeText(" More.")
		b.waitFor("not saved", `!`+box+`.querySelector('.saved').hidden`)
		forge.after(t, "put", "glab-drafts.json", `[{"id":61,"note":"Thanks for this.\n\nFix the retry.","discussion_id":null,"position":null}]`)
		n := forge.settle()
		b.click("Overview", prsRow("Overview"))
		foot := `document.querySelector('.md.ov .thr.conv .foot')`
		b.waitFor("the comment box", foot+`?.querySelector('input')`)
		b.click("the comment box", foot+`.querySelector('input')`)
		b.typeText("Fix the retry.")
		b.waitFor("the comment typed", foot+`.querySelector('input').value === 'Fix the retry.'`)
		b.click("Add to review", byText(".md.ov .thr.conv .foot button", "Add to review"))
		forge.waitFor(t, n, "--method PUT projects/acme%2Freview/merge_requests/12/draft_notes/61")
		b.waitFor("both kept", box+`.querySelector('textarea').value === 'Thanks for this.\n\nFix the retry. More.' && !`+box+`.querySelector('.saved').hidden`)
		b.noErrors()

		// Since your review, a comment on a line far from every change of the merge request goes
		// with its line in the target: 10 lines above it are new.
		const session = "internal/auth/session.go"
		work := filepath.Join(t.TempDir(), "sanne")
		run(t, t.TempDir(), "clone", "-q", origin, work)
		run(t, work, "fetch", "-q", "origin", "refs/merge-requests/12/head")
		run(t, work, "checkout", "-qb", "sanne", "FETCH_HEAD")
		onMain := sessionGo("sess, ok := s.byID[c.Value]", "")
		pushMR := func(text string) string {
			write(t, filepath.Join(work, filepath.FromSlash(session)), text)
			run(t, work, "commit", "-qam", "Note the session store")
			run(t, work, "push", "-q", "origin", "HEAD:refs/merge-requests/12/head")
			head := gitOut(t, work, "rev-parse", "HEAD")
			mr["diffHeadSha"], mr["diffRefs"] = head, obj{"baseSha": main, "headSha": head, "startSha": main}
			forge.set(t, "glab-pr-12.json", ghData(obj{"currentUser": obj{"username": "you"}, "project": obj{"mergeRequest": mr}}))
			return head
		}
		top := strings.Repeat("// The store.\n", 10)
		reviewed := pushMR(top + strings.Replace(onMain, "return 16 > 0", "return 16 > 1", 1))
		b.press("refreshPrs")
		b.click("Overview", prsRow("Overview"))
		b.waitFor("four files", `document.querySelectorAll('#prs-pr .prfiles input.viewedbox').length === 4`)
		for _, name := range []string{"ci.yml", "README.md", "session.go", "retry.go"} {
			b.click("the box of "+name+"'s row", prsRow(name)+`?.querySelector('input.viewedbox')`)
		}
		b.waitFor("every file viewed: the last review", `Object.entries(localStorage).some(([k, v]) => k.startsWith('kijk.viewed:') && k.endsWith(':12') && JSON.parse(v).review?.head === `+jsString(reviewed)+`)`)
		pushMR(top + onMain) // sanne puts check16 back as on main
		b.press("refreshPrs")
		b.click("Since your review", byText("#prs-pr .prmode.since .mode button", "Since your review"))
		near := boxRow(session, "func check16() bool {")
		b.waitFor("only the line put back", `document.querySelectorAll('#viewer .fbox').length === 1 && `+near)
		b.hover("a line", near)
		b.click("+", near+`.querySelector('.addc')`)
		b.waitFor("the editor", `document.activeElement?.matches('#viewer textarea')`)
		b.typeText("Why 16?")
		n = len(forge.calls())
		b.press("submitNote")
		at := lineOf(onMain, "func check16() bool {")
		forge.waitFor(t, n, fmt.Sprintf(`"new_line":%d,"new_path":%q,"old_line":%d,`, at+10, session, at)) // its line in the target
		b.noErrors()

		// Below 16.3 the merge request is read only, and the box's place says why.
		forge.set(t, "glab-version-gitlab.com.json", `{"version":"15.11.13"}`)
		before := forge.settle()
		b = open()
		b.waitFor("read only", box+`?.querySelector('.hint')?.textContent === 'Reviews from kijk need GitLab 16.3. This server runs 15.11.' && !`+box+`.querySelector('button') && `+
			fileBox("internal/webhook/retry.go")+`.querySelector('.ln.add') && !document.querySelector('#viewer .addc')`)
		if strings.Contains(strings.Join(forge.calls()[before:], "\n"), "draft_notes") {
			t.Fatal("kijk read drafts on GitLab 15.11")
		}
		b.noErrors()
	})

	t.Run("viewed marks", func(t *testing.T) {
		repo, origin := reviewRepo(t)
		forge := newFakeForge(t)
		reviewForge(t, forge, repo, origin, false)
		tmp, err := filepath.EvalSymlinks(t.TempDir()) // as kijk keeps the folder
		if err != nil {
			t.Fatal(err)
		}
		other := filepath.Join(tmp, "review-other")
		run(t, repo, "worktree", "add", "-q", "-b", "other", other, "origin/main")
		claude := t.TempDir()
		claudeAgent(t, claude, repo, "Fix the login", "idle")
		write(t, filepath.Join(claude, "sessions", "2.json"), agentstest.SessionJSON(os.Getpid(), "s-other", other, "Other work", "idle", time.Now().UnixMilli()))
		k := startKijkIn(t, exe, t.TempDir(), "CLAUDE_CONFIG_DIR="+claude, forge.env())
		b := openPage(t, newTab(t, tab), k, "/")
		b.selectAgent("Fix the login", "review")
		b.waitFor("the count on the tab", prsTab+`.querySelector('.tabcount')?.textContent === '2'`)
		b.press("prsTab")
		b.waitFor("the list", prRow(477))
		b.click("#477", prRow(477))
		b.waitFor("three boxes", `document.querySelectorAll('#viewer .fbox').length === 3`)

		const ci, readme, retry = ".github/workflows/ci.yml", "README.md", "internal/webhook/retry.go"
		check := func(path string) string { return fileBox(path) + `?.querySelector('.fhead .check input')` }
		viewed := func(path string) string {
			return `(` + fileBox(path) + `?.matches('.viewed') && ` + check(path) + `.checked)`
		}
		notViewed := func(path string) string {
			return `(` + check(path) + ` && !` + fileBox(path) + `.matches('.viewed') && !` + check(path) + `.checked)`
		}
		shut := func(path string) string { return fileBox(path) + `?.matches('.shut')` }
		// still is true once an element is where it was at the poll before, so a click lands on it.
		still := func(el string) string {
			return `(r => { const y = Math.round(r.top); const same = window.e2eY === y; window.e2eY = y; return same })(` + el + `.getBoundingClientRect())`
		}
		b.waitFor("a Viewed box on each file", `[...document.querySelectorAll('#viewer .fbox')].every(f => f.querySelector('.fhead .check')?.textContent.trim() === 'Viewed' && f.querySelector('.fhead .check').dataset.tip === 'Viewed\nV')`)
		b.waitFor("nothing viewed yet", notViewed(ci)+` && `+notViewed(readme)+` && `+notViewed(retry))

		rowCheck := func(name string) string { return prsRow(name) + `?.querySelector('input.viewedbox')` }
		rowViewed := func(name string) string {
			return `(` + prsRow(name) + `?.matches('.viewed') && ` + rowCheck(name) + `.checked)`
		}
		count := func(text string) string {
			return `document.querySelector('#prs-pr .prfiles h3 .viewedcount')?.textContent === ` + jsString(text)
		}
		b.waitFor("a Viewed box on each row", `[...document.querySelectorAll('#prs-pr .prfiles li[data-path]:not(.ov)')].every(r => (c => c && !c.checked && c.tabIndex === -1 && c.getAttribute('aria-label') === 'Viewed' && c.dataset.tip === 'Viewed\nV')(r.querySelector('input.viewedbox')))`)
		b.waitFor("none of three viewed", count("0 of 3 viewed"))

		// A row's box keeps the place in Changes, also when its file is above the view.
		b.evalJS(`(s => s.scrollTop = ` + fileBox(retry) + `.offsetTop + 20)(document.querySelector('.allfiles'))`)
		b.waitFor("the view inside retry.go", `document.querySelector('.allfiles').scrollTop > `+fileBox(retry)+`.offsetTop`)
		place := `Math.round(` + fileBox(retry) + `.getBoundingClientRect().top)`
		at := fmt.Sprint(evalJS[float64](b, place))
		b.click("the box of ci.yml's row", rowCheck("ci.yml"))
		b.waitFor("ci.yml viewed, the view in place", viewed(ci)+` && `+shut(ci)+` && Math.abs(`+place+` - `+at+`) < 2`)
		b.click("the box of ci.yml's row", rowCheck("ci.yml"))
		b.waitFor("ci.yml open again, the view in place", notViewed(ci)+` && !`+shut(ci)+` && Math.abs(`+place+` - `+at+`) < 2`)
		b.evalJS(`document.querySelector('.allfiles').scrollTop = 0`)

		// Checked in its header, a file folds; its chevron still opens it.
		b.waitFor("the Viewed box of README.md in place", still(check(readme)))
		b.click("Viewed of README.md", check(readme))
		b.waitFor("README.md viewed and folded", viewed(readme)+` && `+shut(readme))
		b.waitFor("the focus kept in Changes, for V", `document.activeElement === document.querySelector('.allfiles')`)
		b.waitFor("its row viewed, and the count", rowViewed("README.md")+` && `+count("1 of 3 viewed"))
		b.click("the chevron of README.md", fileBox(readme)+`.querySelector('.fhead .fold')`)
		b.waitFor("README.md open and still viewed", viewed(readme)+` && !`+shut(readme)+` && `+boxHas(readme, "add", "Webhooks retry five times."))

		// V marks the file at the top and goes to the next one not viewed.
		b.evalJS(`document.querySelector('.allfiles').scrollTop = 0`)
		b.evalJS(`document.querySelector('.allfiles').focus()`)
		b.press("markViewed")
		atTop := func(path string) string {
			return `(s => { const top = s.getBoundingClientRect().top; const b = ` + fileBox(path) + `; return Math.abs(b.getBoundingClientRect().top - top - 12) < 2 || (s.scrollTop + s.clientHeight >= s.scrollHeight - 1 && b.getBoundingClientRect().top >= top) })(document.querySelector('.allfiles'))`
		}
		b.waitFor("ci.yml viewed, retry.go next", viewed(ci)+` && `+shut(ci)+` && `+atTop(retry))
		b.press("markViewed")
		b.waitFor("retry.go viewed", viewed(retry)+` && `+shut(retry))
		// On a viewed file, V clears the mark.
		b.click("a line of README.md", boxRow(readme, "Webhooks retry five times."))
		b.press("markViewed")
		b.waitFor("README.md not viewed", notViewed(readme)+` && !`+shut(readme))

		// V types in a comment.
		b.click("a line of README.md", boxRow(readme, "Webhooks retry five times."))
		b.press("addNote")
		b.waitFor("the comment box", `document.activeElement?.matches('#viewer textarea')`)
		b.typeText("very")
		b.waitFor("the text typed, nothing marked", `document.activeElement.value === 'very' && `+notViewed(readme))
		b.key("Escape", kb.Escape)
		b.waitFor("the comment box closed", `!document.querySelector('#viewer textarea')`)

		// Kept after a reload.
		b.act("reload", chromedp.Reload())
		b.waitFor("the marks after a reload", viewed(ci)+` && `+viewed(retry)+` && `+notViewed(readme)+` && `+shut(ci)+` && `+shut(retry))

		// A mark made while Changes is not in front wins over a fold made by hand.
		b.click("the chevron of README.md", fileBox(readme)+`.querySelector('.fhead .fold')`)
		b.waitFor("README.md folded by hand", shut(readme))
		b.click("Overview", prsRow("Overview"))
		b.waitFor("the Overview in front", `document.querySelector('.md.ov')`)
		b.click("the box of README.md's row", rowCheck("README.md"))
		b.waitFor("README.md viewed", rowViewed("README.md"))

		// Sanne pushes: the marks of the files she changed clear and say so; ci.yml stays viewed.
		work := filepath.Join(t.TempDir(), "sanne")
		run(t, t.TempDir(), "clone", "-q", origin, work)
		run(t, work, "fetch", "-q", "origin", "refs/pull/477/head")
		run(t, work, "checkout", "-q", "FETCH_HEAD")
		withDraft := func(ts []obj) []obj {
			return append(ts, ghThread("T7", readme, 5, 0, false, false, "", ghDraft("C40", "Say how long it waits too.")))
		}
		push := func(files map[string]string) {
			for path, text := range files {
				write(t, filepath.Join(work, filepath.FromSlash(path)), text)
			}
			run(t, work, "add", "-A")
			run(t, work, "commit", "-qm", "Change more")
			run(t, work, "push", "-q", "origin", "HEAD:refs/pull/477/head")
			forge.set(t, "gh-pr-477.json", pr477(gitOut(t, work, "rev-parse", "HEAD"), gitOut(t, repo, "rev-parse", "origin/main"), withDraft))
		}
		readmeText, _ := os.ReadFile(filepath.Join(work, "README.md"))
		push(map[string]string{retry: retryGo + "\n// backoff grows with each attempt.\n", readme: string(readmeText) + "\nSee retry.go.\n"})
		b.press("refreshPrs")
		b.waitFor("the new head", `document.querySelector('#prs-pr .prfiles li[data-path="README.md"] .again')`)
		b.click("retry.go in the list", prsRow("retry.go"))
		b.waitFor("README.md changed and open", notViewed(readme)+` && !`+shut(readme)+` && `+fileBox(readme)+`.querySelector('.fhead .again')`)
		b.waitFor("retry.go changed since viewed", notViewed(retry)+` && !`+shut(retry)+` && `+fileBox(retry)+`.querySelector('.fhead .again')?.textContent === 'changed since you viewed it'`)
		b.waitFor("ci.yml still viewed", viewed(ci)+` && `+shut(ci)+` && !`+fileBox(ci)+`.querySelector('.again')`)
		b.waitFor("the row of retry.go changed", `!`+rowViewed("retry.go")+` && `+prsRow("retry.go")+`.querySelector('.again')?.textContent === 'changed' && `+prsRow("retry.go")+`.querySelector('.again').dataset.tip === 'Changed since you viewed it' && `+count("1 of 3 viewed"))
		shots(b, "viewed-changes")
		b.waitFor("the Viewed box of retry.go in place", still(check(retry)))
		b.click("Viewed of retry.go", check(retry))
		b.waitFor("retry.go viewed again", viewed(retry)+` && `+shut(retry)+` && !`+fileBox(retry)+`.querySelector('.again')`)
		b.waitFor("its row too", rowViewed("retry.go")+` && !`+prsRow("retry.go")+`.querySelector('.again') && `+count("2 of 3 viewed"))

		// V in the sidebar: on a viewed row it clears the mark; on another it marks and goes to the next.
		b.evalJS(prsRow("ci.yml") + `.focus()`)
		b.press("markViewed")
		b.waitFor("ci.yml not viewed", notViewed(ci)+` && !`+rowViewed("ci.yml")+` && `+count("1 of 3 viewed")+` && document.activeElement === `+prsRow("ci.yml"))
		b.press("markViewed")
		b.waitFor("ci.yml viewed, README.md next", viewed(ci)+` && `+rowViewed("ci.yml")+` && document.activeElement === `+prsRow("README.md")+` && `+prsRow("README.md")+`.matches('.selected')`)
		// A click on a row's box marks the file and does not open it.
		before := evalJS[float64](b, `document.querySelector('.allfiles').scrollTop`)
		b.click("the box of README.md's row", rowCheck("README.md"))
		b.waitFor("all three viewed", viewed(readme)+` && `+shut(readme)+` && `+rowViewed("README.md")+` && `+count("3 of 3 viewed"))
		if after := evalJS[float64](b, `document.querySelector('.allfiles').scrollTop`); after > before {
			t.Fatalf("Changes scrolled from %v to %v", before, after)
		}
		shots(b, "viewed-sidebar")

		// A mark made again, after a reload with Changes not in front, still folds a file opened by hand.
		b.click("the chevron of README.md", fileBox(readme)+`.querySelector('.fhead .fold')`)
		b.waitFor("README.md open by hand", viewed(readme)+` && !`+shut(readme))
		b.click("Overview", prsRow("Overview"))
		b.waitFor("the Overview in front", `document.querySelector('.md.ov')`)
		b.act("reload", chromedp.Reload())
		b.waitFor("the sidebar after the reload", rowViewed("README.md"))
		b.click("the box of README.md's row", rowCheck("README.md"))
		b.waitFor("README.md cleared", `!`+rowViewed("README.md"))
		b.click("the box of README.md's row", rowCheck("README.md"))
		b.waitFor("README.md viewed again", rowViewed("README.md"))
		b.click("retry.go in the list", prsRow("retry.go"))
		b.waitFor("README.md folded", viewed(readme)+` && `+shut(readme))

		// The list says how far you got.
		b.click("back", `document.querySelector('#prs-pr .back')`)
		b.waitFor("the count in the list, after the drafts", prRow(477)+`?.querySelector('.yours')?.textContent === '1 draft · 3/3 viewed'`)
		b.act("reload", chromedp.Reload())
		b.waitFor("the count after a reload", prRow(477)+`?.querySelector('.yours')?.textContent === '1 draft · 3/3 viewed'`)

		// A generated file that changed since you viewed it opens too.
		b.click("#477", prRow(477))
		b.waitFor("#477 open", `document.querySelector('#prs-pr .pr-num')?.textContent === '#477'`)
		push(map[string]string{"go.sum": "example.com/a v1.0.0 h1:aaa=\nexample.com/d v0.1.0 h1:ddd=\n"})
		b.press("refreshPrs")
		b.waitFor("go.sum folded as generated", shut("go.sum")+` && `+notViewed("go.sum"))
		b.click("the box of go.sum's row", rowCheck("go.sum"))
		b.waitFor("go.sum viewed", viewed("go.sum"))
		push(map[string]string{"go.sum": "example.com/a v1.0.0 h1:aaa=\nexample.com/e v0.2.0 h1:eee=\n"})
		b.press("refreshPrs")
		b.waitFor("go.sum open and changed", notViewed("go.sum")+` && !`+shut("go.sum")+` && `+fileBox("go.sum")+`.querySelector('.fhead .again')`)

		// A push that opens a file above the view keeps the place.
		b.click("the chevron of retry.go", fileBox(retry)+`.querySelector('.fhead .fold')`)
		b.waitFor("retry.go open", `!`+shut(retry)+` && `+boxHas(retry, "add", "\t\tif !retryable(err) {"))
		b.evalJS(`(s => s.scrollTop = ` + fileBox(retry) + `.offsetTop + 20)(document.querySelector('.allfiles'))`)
		b.waitFor("the view inside retry.go", `document.querySelector('.allfiles').scrollTop > `+fileBox(retry)+`.offsetTop`)
		at = fmt.Sprint(evalJS[float64](b, place))
		push(map[string]string{".github/workflows/ci.yml": ciYml + "  lint:\n    runs-on: ubuntu-latest\n"})
		b.press("refreshPrs")
		b.waitFor("ci.yml open above, the view in place", notViewed(ci)+` && !`+shut(ci)+` && `+fileBox(ci)+`.querySelector('.ln') && Math.abs(`+place+` - `+at+`) < 2`)
		b.waitFor("the Viewed box of ci.yml in place", still(check(ci)))
		b.click("Viewed of ci.yml", check(ci))
		b.waitFor("ci.yml viewed again", viewed(ci))
		b.click("back", `document.querySelector('#prs-pr .back')`)

		// Local mode has no marks; As on GitHub has.
		b.click("this branch", prRow(482))
		b.waitFor("Branch changes", `document.querySelector('#filetabs .filetab.active')?.textContent.startsWith('Branch changes') && document.querySelectorAll('#viewer .fbox').length === 6`)
		b.waitFor("no Viewed box in Local mode", `!document.querySelector('#viewer .fhead .check')`)
		b.click("As on GitHub", byText("#prs-branch .prmode button", "As on GitHub"))
		b.waitFor("Viewed boxes As on GitHub", `(t => t?.firstChild.textContent === 'Changes' && t.querySelector('.muted')?.textContent === '#482')(document.querySelector('#filetabs .filetab.active')) && document.querySelectorAll('#viewer .fbox').length > 0 && [...document.querySelectorAll('#viewer .fbox')].every(f => f.querySelector('.fhead .check input') && !f.matches('.viewed'))`)
		b.click("Local", byText("#prs-pr .prmode button", "Local"))

		// Another worktree of the repo, in a second window, shares the marks, and a mark made there
		// shows here at once.
		b.click("back", `document.querySelector('#prs-branch .back')`)
		b.waitFor("the list", prRow(477))
		b.click("#477", prRow(477))
		b.waitFor("#477 here", viewed(ci))
		w := openPage(t, newTab(t, tab), k, "/")
		w.click("the other worktree", row("Other work"))
		w.waitFor("the other folder", `document.querySelector('#folder-name')?.title === `+jsString(other))
		w.waitFor("the count on the tab", prsTab+`.querySelector('.tabcount')?.textContent === '2'`)
		w.press("prsTab")
		w.waitFor("the count kept with the marks", prRow(477)+`?.querySelector('.yours')?.textContent === '3/4 viewed'`)
		w.click("#477", prRow(477))
		w.waitFor("the same marks", viewed(ci)+` && `+viewed(retry)+` && `+viewed(readme)+` && `+notViewed("go.sum"))
		w.waitFor("the Viewed box of ci.yml in place", still(check(ci)))
		w.click("Viewed of ci.yml", check(ci))
		w.waitFor("ci.yml cleared there", notViewed(ci))
		b.waitFor("ci.yml cleared here", notViewed(ci)+` && !`+rowViewed("ci.yml"))
		w.noErrors()
		b.press("prsTab")

		// Once #477 is closed, the list holds every open pull request without it, and its marks go.
		b.click("back", `document.querySelector('#prs-pr .back')`)
		marks477 := `Object.keys(localStorage).some(k => k.startsWith('kijk.viewed:') && k.endsWith(':477'))`
		b.waitFor("the marks of #477 kept", marks477)
		forge.set(t, "gh-list.json", ghData(obj{"viewer": ghUser("you"),
			"repository": obj{"pullRequests": obj{"totalCount": 1, "nodes": []obj{ghRow(482, "Fix login redirect loop", "you", "fix-login", false, 60, 1, "SUCCESS")}}},
			"asked":      obj{"nodes": []obj{}}}))
		b.press("refreshPrs")
		b.waitFor("#477 gone from the list", `document.querySelector('#prs-list .section-toggle:last-of-type')?.textContent === 'All open 1' && !`+prRow(477))
		b.waitFor("its marks dropped", `!`+marks477)
		b.noErrors()
	})

	t.Run("since last review", func(t *testing.T) {
		repo, origin := reviewRepo(t)
		forge := newFakeForge(t)
		reviewForge(t, forge, repo, origin, false)
		theirs := gitOut(t, origin, "rev-parse", "refs/pull/477/head")
		target := func(head string) {
			forge.set(t, "gh-target.json", ghData(obj{"repository": obj{"pullRequest": obj{"id": "PR_477", "headRefOid": head, "reviews": obj{"nodes": []obj{}}}}}))
		}
		target(theirs)
		forge.set(t, "gh-review.json", ghData(obj{"addPullRequestReview": obj{"pullRequestReview": obj{"id": "PRR_1"}}}))
		forge.set(t, "gh-thread.json", ghData(obj{"addPullRequestReviewThread": obj{"thread": obj{"id": "T8", "comments": obj{"nodes": []obj{{"id": "C50"}}}}}}))
		claude := t.TempDir()
		claudeAgent(t, claude, repo, "Fix the login", "idle")
		k := startKijkIn(t, exe, t.TempDir(), "CLAUDE_CONFIG_DIR="+claude, forge.env())
		b := openPage(t, newTab(t, tab), k, "/")
		b.selectAgent("Fix the login", "review")
		b.waitFor("the count on the tab", prsTab+`.querySelector('.tabcount')?.textContent === '2'`)
		b.press("prsTab")
		b.click("#477", prRow(477))
		b.waitFor("three boxes", `document.querySelectorAll('#viewer .fbox').length === 3`)

		const ci, readme, retry = ".github/workflows/ci.yml", "README.md", "internal/webhook/retry.go"
		sw := `document.querySelector('#prs-pr .prmode.since')`
		bar := `document.querySelector('#viewer .allfiles > .sincebar')`
		boxes := `[...document.querySelectorAll('#viewer .fbox')].map(f => f.dataset.path).join(' ')`
		rows := `[...document.querySelectorAll('#prs-pr .prfiles li[data-path]:not(.ov)')].map(r => r.dataset.path).join(' ')`
		rowCheck := func(name string) string { return prsRow(name) + `?.querySelector('input.viewedbox')` }
		reviewed := func(head string) string {
			return `Object.entries(localStorage).some(([k, v]) => k.startsWith('kijk.viewed:') && k.endsWith(':477') && JSON.parse(v).review?.head === ` + jsString(head) + `)`
		}
		b.waitFor("no switch before a review", `document.querySelector('#prs-pr .prfiles h3') && !`+sw)

		// Two of three files viewed: not every file, so no reviewed head yet.
		b.click("the box of ci.yml's row", rowCheck("ci.yml"))
		b.click("the box of README.md's row", rowCheck("README.md"))
		b.waitFor("two viewed", `document.querySelector('#prs-pr .prfiles h3 .viewedcount')?.textContent === '2 of 3 viewed' && !`+reviewed(theirs))

		// Posting a review keeps the head; it is the head now, so still no switch.
		box := `document.querySelector('#prs-pr .reviewbox')`
		b.click("Approve", byText("#prs-pr .reviewbox .mode button", "Approve"))
		b.click("Submit review…", box+`.querySelector('button.primary')`)
		b.waitFor("the check", `document.querySelector('.confirm[role=dialog]')`)
		b.key("Enter", "\r")
		b.waitFor("posted, the head kept", `!document.querySelector('.confirm') && document.querySelector('#statusbar').textContent.includes('Posted your review on #477') && `+reviewed(theirs))
		b.waitFor("no switch on the reviewed head", `!`+sw)

		// Sanne pushes a fix to retry.go.
		work := filepath.Join(t.TempDir(), "sanne")
		run(t, t.TempDir(), "clone", "-q", origin, work)
		run(t, work, "fetch", "-q", "origin", "refs/pull/477/head")
		run(t, work, "checkout", "-qb", "sanne", "FETCH_HEAD")
		main := gitOut(t, repo, "rev-parse", "origin/main")
		push := func(force bool) string {
			args := []string{"push", "-q", "origin", "HEAD:refs/pull/477/head"}
			if force {
				args = append(args, "--force")
			}
			run(t, work, args...)
			head := gitOut(t, work, "rev-parse", "HEAD")
			forge.set(t, "gh-pr-477.json", pr477(head, main, func(ts []obj) []obj { return ts }))
			target(head)
			return head
		}
		commit := func(path, text, msg string) {
			write(t, filepath.Join(work, filepath.FromSlash(path)), text)
			run(t, work, "add", "-A")
			run(t, work, "commit", "-qm", msg)
		}
		commit(retry, retryGo+"\n// backoff grows with each attempt.\n", "Say how backoff grows")
		push(false)
		// The first question about what changed fails; the next answer about #477 asks again.
		b.evalJS(`window.e2eFail = 1; { const f = window.fetch; window.fetch = (u, o) => String(u).includes('/api/pr/since') && window.e2eFail-- > 0 ? Promise.resolve(new Response('git failed', {status: 500})) : f(u, o); }`)
		b.press("refreshPrs")
		since := byText("#prs-pr .prmode.since .mode button", "Since your review")
		all := byText("#prs-pr .prmode.since .mode button", "All changes")
		b.waitFor("the switch, with no count", since+` && window.e2eFail === 0 && !`+since+`.dataset.tip.includes('new commit')`)
		b.press("refreshPrs")
		b.waitFor("the switch after a push", sw+` && `+sw+`.querySelector('.mode').getAttribute('aria-label') === 'Which changes' && `+all+`?.getAttribute('aria-pressed') === 'true' && `+
			since+`?.getAttribute('aria-pressed') === 'false' && `+since+`.dataset.tip.startsWith('Only what changed since your review\nReviewed ') && `+since+`.dataset.tip.endsWith(' ago · 1 new commit')`)

		// Since your review: only the new line of retry.go, the bar, and the file list.
		b.click("Overview", prsRow("Overview"))
		b.waitFor("the Overview in front", `document.querySelector('.md.ov')`)
		b.click("Since your review", since)
		b.waitFor("Changes with only retry.go", `document.querySelector('#filetabs .filetab.active')?.firstChild.textContent === 'Changes' && `+boxes+` === `+jsString(retry))
		b.waitFor("only the new line", boxHas(retry, "add", "// backoff grows with each attempt.")+` && !`+boxRow(retry, "\t\tif !retryable(err) {"))
		b.waitFor("the bar", bar+`?.firstChild.textContent === 'Since your review · 1 commit · 1 file' && `+bar+`.querySelector('button')?.textContent === 'Show all changes'`)
		b.waitFor("the file list since", rows+` === `+jsString(retry)+` && document.querySelector('#prs-pr .prfiles h3').firstChild.textContent === 'Files 1' && document.querySelector('#prs-pr .prfiles h3 .viewedcount')?.textContent === '0 of 1 viewed' && `+since+`.getAttribute('aria-pressed') === 'true'`)
		b.waitFor("no + on the reviewed side", `!`+fileBox(retry)+`.querySelector('.ln.del .addc')`)
		b.hover("the status bar", `document.querySelector('#statusbar')`) // no tooltip in the shot
		shots(b, "since-review")

		// A comment goes on the line of the forge diff, where retry.go is new: it has no old line,
		// though the line is unchanged since the review.
		ret := boxRow(retry, "\treturn err")
		b.hover("a line", ret)
		b.click("+", ret+`.querySelector('.addc')`)
		b.waitFor("the editor", `document.activeElement?.matches('#viewer textarea')`)
		b.typeText("Wrap the error.")
		n := len(forge.calls())
		b.press("submitNote")
		forge.waitFor(t, n, fmt.Sprintf(`{"body":"Wrap the error.","line":%d,"path":%q,"review":"PRR_1","side":"RIGHT"}`, lineOf(retryGo, "\treturn err"), retry))
		b.waitFor("the editor closed", `!document.querySelector('#viewer textarea')`)

		// Moving the switch closes an open comment; + on its line gives the text back.
		b.hover("a line", ret)
		b.click("+", ret+`.querySelector('.addc')`)
		b.waitFor("the editor", `document.activeElement?.matches('#viewer textarea')`)
		b.typeText("Keep me.")
		// A new answer about #477 draws the view again while the comment is typed: the keys still go in.
		n = forge.settle()
		b.press("refreshPrs")
		forge.waitFor(t, n, "pullRequest(number")
		forge.settle()
		b.typeText(" Twice.")
		b.waitFor("the editor kept its focus", `document.activeElement?.matches('#viewer textarea') && document.activeElement.value === 'Keep me. Twice.'`)

		// Show all changes in the bar goes back.
		b.click("Show all changes", bar+`.querySelector('button')`)
		b.waitFor("all changes again", boxes+` === `+jsString(ci+" "+readme+" "+retry)+` && !`+bar+` && `+all+`.getAttribute('aria-pressed') === 'true' && `+rows+` === `+jsString(ci+" "+readme+" "+retry))
		b.waitFor("the comment closed", `!document.querySelector('#viewer textarea') && `+ret)
		b.hover("a line", ret)
		b.click("+", ret+`.querySelector('.addc')`)
		b.waitFor("its text back", `document.activeElement?.matches('#viewer textarea') && document.activeElement.value === 'Keep me. Twice.'`)
		b.key("Escape", kb.Escape)
		b.waitFor("closed", `!document.querySelector('#viewer textarea')`)

		// main moves on and sanne merges it in: main's own file does not show.
		run(t, work, "checkout", "-qb", "mainwork", "origin/main")
		commit("billing.go", "package billing\n", "Add billing")
		run(t, work, "push", "-q", "origin", "HEAD:main")
		main = gitOut(t, work, "rev-parse", "HEAD")
		run(t, work, "checkout", "-q", "sanne")
		run(t, work, "merge", "-q", "--no-edit", "mainwork")
		merged := push(false)
		b.press("refreshPrs")
		b.waitFor("the switch says two commits", since+`?.dataset.tip.endsWith(' · 2 new commits')`)
		b.evalJS(since + `.focus()`)
		b.key("Enter", "\r")
		b.waitFor("the focus still on the switch", `document.activeElement === `+since+` && `+since+`.getAttribute('aria-pressed') === 'true'`)
		b.waitFor("billing.go left out", bar+`?.firstChild.textContent === 'Since your review · 2 commits · 1 file' && `+boxes+` === `+jsString(retry)+` && `+rows+` === `+jsString(retry))

		// Marking the last file not viewed makes every file viewed: the switch hides, all changes show.
		check := fileBox(retry) + `?.querySelector('.fhead .check input')`
		b.waitFor("the Viewed box of retry.go in place", `(r => { const y = Math.round(r.top); const same = window.e2eY === y; window.e2eY = y; return same })(`+check+`.getBoundingClientRect())`)
		b.click("Viewed of retry.go", check)
		b.waitFor("the switch gone", `!`+sw+` && `+reviewed(merged)+` && !`+bar+` && `+boxes+` === `+jsString(ci+" "+readme+" "+retry)+` && [...document.querySelectorAll('#viewer .fbox')].every(f => f.matches('.viewed.shut'))`)

		// Sanne only merges main in again: no file of #477 changed, so there is nothing to show
		// since your review. The switch hides; the head of your last review stays.
		run(t, work, "checkout", "-q", "mainwork")
		commit("billing_test.go", "package billing\n", "Test billing")
		run(t, work, "push", "-q", "origin", "HEAD:main")
		main = gitOut(t, work, "rev-parse", "HEAD")
		run(t, work, "checkout", "-q", "sanne")
		run(t, work, "merge", "-q", "--no-edit", "mainwork")
		push(false)
		b.press("refreshPrs")
		b.waitFor("the new head", `document.querySelector('#viewer .fbox')`)
		forge.settle()
		b.waitFor("the last review kept", reviewed(merged))
		b.waitFor("no switch", `document.querySelector('#prs-pr .prfiles h3') && !`+sw+` && `+boxes+` === `+jsString(ci+" "+readme+" "+retry))

		// After a force push there is nothing to compare: all changes show, with a note.
		run(t, work, "checkout", "-qb", "rebased", "mainwork")
		run(t, work, "merge", "-q", "--squash", "sanne")
		commit(retry, retryGo+"\n// rebased.\n", "Retry, rebased")
		push(true)
		b.press("refreshPrs")
		b.waitFor("the switch after a force push", since)
		b.click("Since your review", since)
		const note = "Rebased or force-pushed since your review. All changes show."
		b.waitFor("all changes, with the note", bar+`?.firstChild.textContent === `+jsString(note)+` && `+bar+`.querySelector('button')?.textContent === 'Show all changes' && document.querySelector('#prs-pr .since-note')?.textContent === `+jsString(note)+` && `+boxes+` === `+jsString(ci+" "+readme+" "+retry)+` && document.querySelector('.allfiles').scrollTop === 0`)
		b.hover("the status bar", `document.querySelector('#statusbar')`) // no tooltip in the shot
		shots(b, "since-rebased")

		// A head reviewed in another clone may not be in this one: that is not a force push.
		b.evalJS(`for (const k of Object.keys(localStorage)) if (k.startsWith('kijk.viewed:') && k.endsWith(':477')) localStorage.setItem(k, JSON.stringify({...JSON.parse(localStorage.getItem(k)), review: {head: 'ab'.repeat(20)}}))`)
		b.act("reload", chromedp.Reload())
		b.waitFor("no age without a time", since+`?.dataset.tip === 'Only what changed since your review'`)
		b.click("Since your review", since)
		const missing = "kijk does not have the commit of your last review in this clone. All changes show."
		b.waitFor("all changes, with why", bar+`?.firstChild.textContent === `+jsString(missing)+` && document.querySelector('#prs-pr .since-note')?.textContent === `+jsString(missing)+` && `+boxes+` === `+jsString(ci+" "+readme+" "+retry))

		// GitHub takes a comment only near a change of the pull request: a line that changed since
		// the review, but is far from every change of #477, says so.
		const session = "internal/auth/session.go"
		onMain := sessionGo("sess, ok := s.byID[c.Value]", "")
		top := strings.Repeat("// The store.\n", 10)
		commit(session, top+strings.Replace(onMain, "return 16 > 0", "return 16 > 1", 1), "Note the session store")
		last := push(false)
		commit(session, top+onMain, "Put check16 back")
		push(false)
		b.evalJS(`for (const k of Object.keys(localStorage)) if (k.startsWith('kijk.viewed:') && k.endsWith(':477')) localStorage.setItem(k, JSON.stringify({...JSON.parse(localStorage.getItem(k)), review: {head: ` + jsString(last) + `}}))`)
		b.act("reload", chromedp.Reload())
		b.click("Since your review", since)
		far := boxRow(session, "func check16() bool {")
		b.waitFor("only the line put back", boxes+` === `+jsString(session)+` && `+far)
		b.hover("a line", far)
		b.click("+", far+`.querySelector('.addc')`)
		b.waitFor("the editor", `document.activeElement?.matches('#viewer textarea')`)
		b.typeText("Why 16?")
		b.press("submitNote")
		b.waitFor("why GitHub refuses it", `document.querySelector('#viewer .thr.editing .error')?.textContent === 'GitHub takes a comment only within 3 lines of a change of the pull request.'`)
		b.key("Escape", kb.Escape)

		// Local mode on your own branch has no switch.
		b.click("back", `document.querySelector('#prs-pr .back')`)
		b.click("this branch", prRow(482))
		b.waitFor("Branch changes", `document.querySelector('#filetabs .filetab.active')?.textContent.startsWith('Branch changes')`)
		b.waitFor("no switch in Local mode", `!document.querySelector('#prs > :not([hidden]) .prmode.since')`)
		b.noErrors()
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
		m, vm := journeyMachine(t)
		publish(t, "0.0.1", exe)
		app := namedRepo(t, "app")
		write(t, filepath.Join(app, "app.go"), "package app\n")
		run(t, app, "add", ".")
		run(t, app, "commit", "-qm", "Add the app")
		appOrigin := filepath.Join(t.TempDir(), "app.git")
		run(t, t.TempDir(), "init", "-q", "--bare", "-b", "main", appOrigin)
		run(t, app, "remote", "add", "origin", "https://github.com/acme/app.git")
		run(t, app, "config", "url."+appOrigin+".insteadOf", "https://github.com/acme/app.git")
		run(t, app, "push", "-q", "origin", "main")
		forge := newFakeForge(t)
		forge.set(t, "gh-list.json", ghData(obj{"viewer": ghUser("you"), "repository": obj{"pullRequests": obj{"totalCount": 1,
			"nodes": []obj{ghRow(9, "Add a health check", "tim", "health", false, 12, 0, "SUCCESS")}}}, "asked": obj{"nodes": []obj{{"number": 9}}}}))
		run(t, app, "checkout", "-qb", "start")
		write(t, filepath.Join(app, "app.go"), "package app\n\n// Start runs the app.\nfunc Start() {}\n")
		run(t, app, "commit", "-qam", "Add Start")
		claudeAgent(t, filepath.Join(m.home(vm), ".claude"), app, "Remote work", "busy")
		home := t.TempDir()
		write(t, filepath.Join(home, ".config", "kijk", "machines.json"), fmt.Sprintf(`{"machines":[{"target":%q,"enabled":true}]}`, vm))
		local := startKijkIn(t, exe, home, forge.env()) // the kijk on the machine gets the fake gh through ssh
		r := openPage(t, newTab(t, tab), local, "/")
		// The first connection installs kijk on the machine.
		r.waitLong(time.Minute, "the agent on the machine", rowLabelIs("Remote work, Claude Code, app · start, Busy"))
		r.click("the remote row", row("Remote work"))
		r.waitFor("app.go in the tree", treeItem("app.go"))
		r.click("app.go", treeItem("app.go"))
		r.waitFor("the remote code", activeFile("app.go")+" && "+viewHas("package app"))

		// The PRs tab of a remote folder comes from the kijk on its machine; its copies count here.
		r.waitFor("the PRs tab", `(b => b && !b.hidden && b.dataset.tip.startsWith('PRs\n'))(`+prsTab+`)`)
		r.press("prsTab")
		r.waitFor("the list from the machine", prRow(9)+`?.querySelector('.prt').textContent === 'Add a health check' && `+prsTab+`.querySelector('.tabcount')?.textContent === '1'`)
		if calls := strings.Join(forge.calls(), "\n"); !strings.Contains(calls, "home="+m.home(vm)+" gh api graphql") {
			t.Fatalf("the list did not come from gh on the machine:\n%s", calls)
		}
		r.click("this branch", `document.querySelector('#prs-list li.prrow[data-n="branch"]')`)
		r.click("Overview", prsRow("Overview"))
		r.waitFor("the Overview of a branch with no pull request", `document.querySelector('.md.ov h1')?.textContent === 'start' && document.querySelector('.md.ov').textContent.includes('There is no pull request for this branch yet.')`)
		r.press("prsTab")
		r.waitFor("the bar", `document.querySelector('#prs .unpushed .t')?.textContent === "This branch is not pushed yet. Reviewers don't see it."`)
		r.waitFor("the change", boxHas("app.go", "add np", "// Start runs the app."))
		r.click("the new line", boxRow("app.go", "func Start() {}")+`.querySelector('.tx')`)
		r.press("addNote")
		r.waitFor("the note editor", `document.activeElement?.matches('.thr.editing textarea')`)
		r.typeText("Return an error.")
		r.press("submitNote")
		r.waitFor("the note", boxRow("app.go", "func Start() {}")+`.nextElementSibling?.matches('.thr.note')`)
		r.evalJS(`window.e2eFetches = []; { const f = window.fetch; window.fetch = (u, o) => f(u, o).then(r => (e2eFetches.push(u + ' ' + r.status), r)); }`)
		r.evalJS(`navigator.clipboard.writeText = t => (window.e2eClip = t, Promise.resolve())`)
		r.click("Copy for agent", byText("#prs .reviewbox button", "Copy for agent…"))
		r.click("Copy 1", byText(".copypanel button", "Copy 1"))
		r.waitFor("the prompt", `window.e2eClip === `+jsString("Review notes for branch start.\nFix each one, then say what you changed.\n\napp.go:4\n  Return an error."))
		r.waitFor("the copy counted by the local kijk", `e2eFetches.includes('/api/count?name=agent-copy 200')`)
		r.noErrors()
	})

	// An agent type that kijk on a remote machine cannot read joins the notice, after the machine's name.
	t.Run("remote agent error", func(t *testing.T) {
		m, vm := journeyMachine(t)
		publish(t, "0.0.1", exe)
		app := namedRepo(t, "app")
		claudeAgent(t, filepath.Join(m.home(vm), ".claude"), app, "Remote work", "idle")
		codex := agentstest.NewFakeCodexAt(t, agentstest.CodexSocket(filepath.Join(m.home(vm), ".codex")))
		codex.Reply(func(c *agentstest.Conn, _ int, _ string) { c.Send(0x1, []byte("{oops")) })
		home := t.TempDir()
		write(t, filepath.Join(home, ".config", "kijk", "machines.json"), fmt.Sprintf(`{"machines":[{"target":%q,"enabled":true}]}`, vm))
		r := openPage(t, newTab(t, tab), startKijkIn(t, exe, home), "/")
		// The first connection installs kijk on the machine.
		r.waitLong(time.Minute, "the notice", noticeSays("Cannot read Codex agents · they are hidden"))
		r.waitFor("the machine's name before the reason", agentNotice+`.dataset.tip.split('\n')[1].startsWith(`+jsString(vm+": ")+`)`)
		r.waitFor("the other agent on the machine", rowLabelIs("Remote work, Claude Code, app · main, Idle"))
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
		a := openPage(t, newTab(t, tab), startKijkIn(t, exe, home, homeFolders()...), "/")
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

		write(t, broken, agentstest.SessionJSON(os.Getpid(), "s-cart", shop, "Add the cart", "idle", time.Now().UnixMilli()))
		e.waitFor("the Codex notice", noticeSays("Cannot read Codex agents · they are hidden"))
		e.waitFor("the Claude Code agent", rowLabelIs("Add the cart, Claude Code, shop · main, Idle"))
		codex.Reply(nil)
		e.waitFor("the notice gone", "!"+agentNotice)
		e.waitFor("the Codex agent again", row("Fix the login bug"))
		e.noErrors()
	})

	t.Run("no agent tools", func(t *testing.T) {
		n := startKijkIn(t, exe, t.TempDir(), homeFolders()...)
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

// listAgentTypes gets the list of agent types from kijk, as the page does.
func listAgentTypes(t *testing.T, k *kijkProcess) []agents.AgentType {
	t.Helper()
	resp, err := k.request("GET", "/api/agenttypes")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var types []agents.AgentType
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
	prsTab   = `document.querySelector('#tabs button[data-tab=prs]')`
)

// shots saves the page in light and dark when KIJK_E2E_SHOTS is set.
func shots(p page, name string) {
	p.t.Helper()
	if os.Getenv("KIJK_E2E_SHOTS") == "" {
		return
	}
	for _, scheme := range []string{"light", "dark"} {
		p.emulate(scheme, 1400)
		p.act("wait for the layout", chromedp.Sleep(300*time.Millisecond))
		p.screenshot(e2eOut(p.t), name+"-"+scheme)
	}
	p.emulate("light", 1400)
}

// prsRow is the row of a file in the PRs tab's file list.
func prsRow(name string) string {
	return fmt.Sprintf(`[...document.querySelectorAll('#prs > :not([hidden]) .prfiles li')].find(e => e.querySelector('.name')?.textContent === %s)`, jsString(name))
}

// prRow is the row of pull request n in the PRs tab's list.
func prRow(n int) string {
	return fmt.Sprintf(`document.querySelector('#prs-list li.prrow[data-n="%d"]')`, n)
}

// fileBox is the box of a file in the Branch changes view.
func fileBox(path string) string {
	return fmt.Sprintf(`document.querySelector('#viewer .fbox[data-path=%s]')`, jsString(path))
}

// boxRow is the row of a box whose text is text.
func boxRow(path, text string) string {
	return fmt.Sprintf(`[...(%s?.querySelectorAll('.ln') || [])].find(e => e.querySelector('.tx')?.textContent === %s)`, fileBox(path), jsString(text))
}

// boxHas is true when a box has a row of the classes cls, such as "add np", with the text.
func boxHas(path, cls, text string) string {
	return fmt.Sprintf(`[...(%s?.querySelectorAll('.ln.%s') || [])].some(e => e.querySelector('.tx')?.textContent === %s)`, fileBox(path), strings.ReplaceAll(cls, " ", "."), jsString(text))
}

// lineOf is the number of the first line of text that is line.
func lineOf(text, line string) int {
	return slices.Index(strings.Split(text, "\n"), line) + 1
}

// changeAt is true when a change starts five lines below the top of the Branch changes view.
const changeAt = `(s => [...s.querySelectorAll('.fbox:not(.shut) .ln.add, .fbox:not(.shut) .ln.del')].some(r => Math.abs(r.getBoundingClientRect().top - s.getBoundingClientRect().top - 124) < 2))(document.querySelector('.allfiles'))`

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
