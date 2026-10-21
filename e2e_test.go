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
		b.waitFor("the open thread", open+`?.matches('.thr') && `+open+`.querySelector('.thead').textContent === 'Open thread · line 15· 3 comments' && `+open+`.querySelectorAll('.c').length === 3`)
		b.waitFor("its comments as markdown", open+`.querySelector('.c .cbody p')?.textContent === 'Should a 429 count as retryable?'`)
		resolved := boxRow(retry, "\t\tselect {") + `.nextElementSibling`
		b.waitFor("the resolved thread folded", resolved+`?.matches('.thr.folded') && `+resolved+`.textContent === 'Resolved · priya: Check ctx before the first sleep? · 2 replies'`)
		b.click("the resolved thread", resolved)
		b.waitFor("the resolved thread open", resolved+`?.matches('.thr.resolved') && `+resolved+`.querySelectorAll('.c').length === 3`)
		b.click("Fold", resolved+`.querySelector('.thead button')`)
		b.waitFor("folded again", resolved+`?.matches('.thr.folded')`)
		b.waitFor("the outdated bar", fileBox(retry)+`.querySelector('.obar')?.textContent.startsWith('1 outdated thread · its lines changed after it was written')`)
		b.click("retry.go in the list", prsRow("retry.go"))
		b.click("Show", fileBox(retry)+`.querySelector('.obar button')`)
		b.waitFor("the outdated thread with its old line", fileBox(retry)+`.querySelector('.obar + .thr .thead')?.textContent.startsWith('Outdated thread · line 20') && `+fileBox(retry)+`.querySelector('.obar + .thr .was')?.textContent === '\ttime.Sleep(time.Second)'`)
		b.waitFor("thread counts", fileBox(retry)+`.querySelector('.fhead .tc')?.textContent === '3' && `+prsRow("retry.go")+`.querySelector('.tc')?.textContent === '3'`)
		b.evalJS(`document.querySelector('.allfiles').scrollTop = 0`)
		b.evalJS(`document.querySelector('.allfiles').focus()`)
		b.press("nextNote")
		b.waitFor("the first thread below the top", `(s => [...s.querySelectorAll('.thr')].some(n => Math.abs(n.getBoundingClientRect().top - s.getBoundingClientRect().top - 124) < 2))(document.querySelector('.allfiles'))`)
		shots(b, "prs-changes")
		b.click("README.md in the list", prsRow("README.md"))
		b.waitFor("README.md at the top", `(s => s.scrollTop > 0 && Math.abs(`+fileBox("README.md")+`.offsetTop - 12 - s.scrollTop) < 2)(document.querySelector('.allfiles'))`)
		b.click("Open the whole file", fileBox("internal/webhook/retry.go")+`.querySelector('[aria-label="Open the whole file"]')`)
		b.waitFor("retry.go at #477", `(t => t?.firstChild.textContent === 'retry.go' && t.querySelector('.muted')?.textContent === '#477')(document.querySelector('#filetabs .filetab.active')) && `+viewHas("func (s *Sender) deliver(ctx context.Context, ev Event) error {"))

		// The Overview: who wants what, reviewers, the description, checks as a graph, the conversation.
		b.click("Overview", prsRow("Overview"))
		b.waitFor("the Overview tab", `(t => t?.firstChild.textContent === 'Overview' && t.querySelector('.muted')?.textContent === '#477')(document.querySelector('#filetabs .filetab.active'))`)
		b.waitFor("title and meta", `document.querySelector('.md.ov h1')?.textContent === 'Retry webhook delivery with backoff #477' && document.querySelector('.md.ov .meta').textContent.includes('sanne wants to merge') && document.querySelector('.md.ov .meta').textContent.includes('· 2 commits')`)
		b.waitFor("the reviewers", `[...document.querySelectorAll('.md.ov .person')].map(p => p.textContent + ':' + p.dataset.tip.split('\n')[1]).join('|') === 'Ppriya:Approved|Ttim:Commented|Yyou:Review requested'`)
		b.waitFor("the description", `document.querySelector('.md.ov .desc strong')?.textContent === 'one' && document.querySelector('.md.ov .desc li')?.textContent === 'Retry up to five times.'`)
		b.waitFor("the checks graph", `(d => d && [...d.querySelectorAll('.dagcol')].map(c => [...c.querySelectorAll('.job')].map(j => j.textContent).join('+')).join(' | ') === 'build | test (linux)+test (windows)')(document.querySelector('.md.ov .dag'))`)
		b.waitFor("the failed job and the lines", `document.querySelector('.md.ov .job.bad')?.textContent === 'test (windows)' && document.querySelectorAll('.md.ov .dag .edges path').length === 2`)
		b.waitFor("other checks", `[...document.querySelectorAll('.md.ov .ovlist.other li')].map(l => l.textContent).join('|').startsWith('Vercel – Preview')`)
		b.waitFor("the end of the failed log", `document.querySelector('.md.ov .joblog .jlhead b')?.textContent === 'test (windows)' && document.querySelector('.md.ov .joblog pre')?.textContent.endsWith('--- FAIL: TestBackoffJitter (0.00s)') && !document.querySelector('.md.ov .joblog button')`)
		b.waitFor("the conversation", `document.querySelector('.md.ov .thr.conv .cbody')?.textContent.trim() === 'Do we want the attempt number in the delivery log?'`)
		shots(b, "prs-overview")
		b.click("List", byText(".md.ov .h2mode button", "List"))
		b.waitFor("the checks as a list", `!document.querySelector('.md.ov .dag') && document.querySelectorAll('.md.ov .ovlist.jobs li').length === 3`)
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

		// A failed job of your own branch goes to the agent.
		b.click("Overview", prsRow("Overview"))
		b.waitFor("the failed job of #482", `document.querySelector('.md.ov .joblog .jlhead b')?.textContent === 'test (linux)'`)
		b.click("Copy for agent", `document.querySelector('.md.ov .joblog button')`)
		b.waitFor("the failure on the clipboard", `window.e2eClip === `+jsString("CI job test (linux) failed on branch fix-login (PR #482).\nFix it, then say what you changed.\n\n=== RUN   TestResume\n    session_test.go:12: got nil session\n--- FAIL: TestResume (0.00s)"))
		b.waitFor("the status bar", `document.querySelector('#statusbar').textContent.includes('Copied the failure of test (linux)')`)

		// Refresh asks the forge again.
		before := len(forge.calls())
		b.press("refreshPrs")
		forge.waitFor(t, before, "pullRequest(number")
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
		run(t, app, "checkout", "-qb", "start")
		write(t, filepath.Join(app, "app.go"), "package app\n\n// Start runs the app.\nfunc Start() {}\n")
		run(t, app, "commit", "-qam", "Add Start")
		claudeAgent(t, filepath.Join(m.home(vm), ".claude"), app, "Remote work", "busy")
		home := t.TempDir()
		write(t, filepath.Join(home, ".config", "kijk", "machines.json"), fmt.Sprintf(`{"machines":[{"target":%q,"enabled":true}]}`, vm))
		local := startKijkIn(t, exe, home)
		r := openPage(t, newTab(t, tab), local, "/")
		// The first connection installs kijk on the machine.
		r.waitLong(time.Minute, "the agent on the machine", rowLabelIs("Remote work, Claude Code, app · start, Busy"))
		r.click("the remote row", row("Remote work"))
		r.waitFor("app.go in the tree", treeItem("app.go"))
		r.click("app.go", treeItem("app.go"))
		r.waitFor("the remote code", activeFile("app.go")+" && "+viewHas("package app"))

		// The PRs tab of a remote folder comes from the kijk on its machine; its copies count here.
		r.waitFor("the PRs tab", `(b => b && !b.hidden && b.dataset.tip.startsWith('Branch\n'))(`+prsTab+`)`)
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
