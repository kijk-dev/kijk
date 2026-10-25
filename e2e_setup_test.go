package main

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/cdproto"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/input"
	cdppage "github.com/chromedp/cdproto/page"
	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"

	"github.com/kijk-dev/kijk/internal/agentstest"
)

const stepTimeout = 15 * time.Second

// needBrowser skips the test where the browser tests do not run, and gives the browser to drive.
func needBrowser(t *testing.T) string {
	t.Helper()
	ci := os.Getenv("CI") != ""
	switch {
	case testing.Short():
		t.Skip("starts kijk and a browser")
	case runtime.GOOS == "windows":
		t.Skip("the browser tests run on macOS and Linux")
	case ci && runtime.GOOS != "linux":
		t.Skip("CI runs the browser tests on Linux only")
	}
	p := e2eBrowser()
	if p == "" && ci {
		t.Fatal("no Chrome, Edge, Brave, Chromium or Vivaldi found: set KIJK_E2E_BROWSER")
	}
	if p == "" {
		t.Skip("no Chromium-type browser found: set KIJK_E2E_BROWSER to run the browser tests")
	}
	return p
}

// e2eBrowser is $KIJK_E2E_BROWSER, else the first browser of kijk's own list that is installed.
func e2eBrowser() string {
	if p := os.Getenv("KIJK_E2E_BROWSER"); p != "" {
		return p
	}
	s := thisSystem()
	for _, b := range browsers {
		if p := s.locate(b); p != "" {
			if s.goos == "darwin" {
				p = filepath.Join(p, "Contents", "MacOS", b.macApp)
			}
			return p
		}
	}
	return ""
}

// e2eOut is a folder that outlives the test, for its screenshots: $KIJK_E2E_OUT, else kijk-e2e in
// the temp folder, with a folder per test.
func e2eOut(t *testing.T) string {
	dir := filepath.Join(cmp.Or(os.Getenv("KIJK_E2E_OUT"), filepath.Join(os.TempDir(), "kijk-e2e")),
		strings.ReplaceAll(t.Name(), "/", "-"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// startBrowser starts a headless browser that closes when the test ends. Its first tab is ready.
func startBrowser(t *testing.T, exe string) context.Context {
	t.Helper()
	opts := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.ExecPath(exe), chromedp.WindowSize(1400, 900))
	// A container or a CI machine may not allow Chromium's sandbox. The tests open only kijk's pages.
	if os.Getenv("KIJK_E2E_NO_SANDBOX") != "" {
		opts = append(opts, chromedp.NoSandbox)
	}
	alloc, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	ctx, cancel := chromedp.NewContext(alloc)
	t.Cleanup(func() {
		cancel()
		cancelAlloc()
	})
	if err := chromedp.Do(ctx); err != nil {
		t.Fatalf("cannot start %s: %v", exe, err)
	}
	return ctx
}

// page is one browser tab on a kijk window. Its steps wait for the page, and a failed step saves
// what the test needs to see why.
type page struct {
	t   *testing.T
	ctx context.Context
	k   *kijkProcess
	mac bool // kijk then uses the macOS keys
	log *consoleLog
}

type consoleLog struct {
	mu   sync.Mutex
	msgs []chromedp.ConsoleMessage
	seen int // messages checked by noErrors
}

// openPage shows kijk's window at path in tab, a context from startBrowser or newTab.
func openPage(t *testing.T, tab context.Context, k *kijkProcess, path string) page {
	t.Helper()
	p := page{t: t, ctx: tab, k: k, log: &consoleLog{}}
	msgs := chromedp.Console(tab)
	go func() {
		for m, err := range msgs {
			if err != nil {
				return
			}
			p.log.mu.Lock()
			p.log.msgs = append(p.log.msgs, m)
			p.log.mu.Unlock()
		}
	}()
	// patch then throws when it makes the focused element again, and noErrors fails the test.
	p.onEveryPage(`globalThis.kijkCheck = true`)
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	p.act("open "+path, chromedp.Navigate(fmt.Sprintf("http://127.0.0.1:%d%s%stoken=%s", k.port, path, sep, k.token)))
	p.mac = evalJS[bool](p, `/Mac/.test(navigator.platform)`)
	return p
}

func newTab(t *testing.T, tab context.Context) context.Context {
	ctx, cancel := chromedp.NewContext(tab)
	t.Cleanup(cancel)
	return ctx
}

// on gives the page for a subtest, so a failure stops that subtest.
func (p page) on(t *testing.T) page {
	p.t = t
	return p
}

func (p page) act(what string, a chromedp.Action[chromedp.Void]) {
	p.t.Helper()
	ctx, cancel := context.WithTimeout(p.ctx, stepTimeout)
	defer cancel()
	if err := chromedp.Do(ctx, a); err != nil {
		p.fail("%s: %v", what, err)
	}
}

// selectAgent shows the folder of the agent whose row label starts with label.
func (p page) selectAgent(label, folder string) {
	p.t.Helper()
	p.click("the "+folder+" row", row(label))
	p.waitFor("the folder name", fmt.Sprintf(`document.querySelector('#folder-name')?.textContent === %s`, jsString(folder)))
}

// waitFor waits until the JavaScript expression js is true. what says what the test waits for.
func (p page) waitFor(what, js string) {
	p.t.Helper()
	p.waitLong(stepTimeout, what, js)
}

func (p page) waitLong(d time.Duration, what, js string) {
	p.t.Helper()
	// Not chromedp.Poll: it runs in the frame's last-made JS world, which in Brave is Shields' own
	// world, where the page's globals do not exist.
	ctx, cancel := context.WithTimeout(p.ctx, d+5*time.Second)
	defer cancel()
	var last error
	for end := time.Now().Add(d); time.Now().Before(end); time.Sleep(100 * time.Millisecond) {
		ok, err := chromedp.Run(ctx, chromedp.Evaluate[bool]("!!("+js+")"))
		if err == nil && ok {
			return
		}
		last = err
	}
	if last != nil {
		p.fail("waiting for %s: %v", what, last)
	}
	p.fail("waiting for %s: timeout", what)
}

// settle waits two frames, so the page has run what it put off to the next frame, as a scroll event.
func (p page) settle() {
	p.t.Helper()
	ctx, cancel := context.WithTimeout(p.ctx, stepTimeout)
	defer cancel()
	await := func(e *cdpruntime.EvaluateParams) { e.AwaitPromise = new(true) }
	if _, err := chromedp.Run(ctx, chromedp.Evaluate[bool](`new Promise(r => requestAnimationFrame(() => requestAnimationFrame(() => r(true))))`, await)); err != nil {
		p.fail("waiting two frames: %v", err)
	}
}

func evalJS[T any](p page, js string) T {
	p.t.Helper()
	ctx, cancel := context.WithTimeout(p.ctx, stepTimeout)
	defer cancel()
	v, err := chromedp.Run(ctx, chromedp.Evaluate[T](js))
	if err != nil {
		p.fail("evaluate %s: %v", js, err)
	}
	return v
}

func (p page) click(what, el string) {
	p.t.Helper()
	p.act("click "+what, delivered(chromedp.Click(chromedp.JSPath(el))))
}

func (p page) doubleClick(what, el string) {
	p.t.Helper()
	p.act("double-click "+what, delivered(chromedp.DoubleClick(chromedp.JSPath(el))))
}

// delivered clicks again when the page redrew the element during the click, as a Git tab refresh
// does to its list: then its node is gone, or the browser sends no click event at all.
func delivered(click chromedp.Action[chromedp.Void]) chromedp.Action[chromedp.Void] {
	return chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
		for {
			if _, err := chromedp.Evaluate[bool](`(window.e2eClicked = false, document.addEventListener('click', () => window.e2eClicked = true, {capture: true, once: true}), true)`)(ctx, t); err != nil {
				return err
			}
			_, err := click(ctx, t)
			var e *cdproto.Error
			if errors.As(err, &e) && slices.Contains(goneNode, e.Message) && ctx.Err() == nil {
				continue
			}
			if err != nil {
				return err
			}
			// A click that opened another page leaves no flag behind.
			if lost, err := chromedp.Evaluate[bool](`window.e2eClicked === false`)(ctx, t); err != nil || !lost {
				return err
			}
		}
	})
}

// goneNode are the browser's errors for a node that left the page.
var goneNode = []string{"Could not find node with given id", "Node is detached from document", "Node with given id does not belong to the document"}

// typeText types into the element that has the focus.
func (p page) typeText(text string) {
	p.t.Helper()
	p.act("type "+text, chromedp.KeyEvent(text))
}

func (p page) key(name string, k string, mods ...chromedp.Modifier) {
	p.t.Helper()
	p.act("press "+name, chromedp.KeyEvent(k, chromedp.KeyModifiers(mods...)))
}

// press presses the key of a kijk action, as keys.js has it for this system.
func (p page) press(action string) {
	p.t.Helper()
	meta, ctrl, alt, shift := chromedp.ModifierMeta, chromedp.ModifierCtrl, chromedp.ModifierAlt, chromedp.ModifierShift
	type combo struct {
		key  string
		mods []chromedp.Modifier
	}
	keys := map[string][2]combo{ // macOS, other systems
		"back":       {{"[", []chromedp.Modifier{meta}}, {kb.ArrowLeft, []chromedp.Modifier{ctrl, alt}}},
		"forward":    {{"]", []chromedp.Modifier{meta}}, {kb.ArrowRight, []chromedp.Modifier{ctrl, alt}}},
		"find":       {{"f", []chromedp.Modifier{meta}}, {"f", []chromedp.Modifier{ctrl}}},
		"gotoLine":   {{"l", []chromedp.Modifier{meta}}, {"g", []chromedp.Modifier{ctrl}}},
		"gotoFile":   {{"o", []chromedp.Modifier{meta, shift}}, {"n", []chromedp.Modifier{ctrl, shift}}},
		"prsTab":     {{"2", []chromedp.Modifier{meta}}, {"2", []chromedp.Modifier{alt}}},
		"addNote":    {{"n", []chromedp.Modifier{meta, alt}}, {"n", []chromedp.Modifier{ctrl, alt}}},
		"nextNote":   {{kb.ArrowDown, []chromedp.Modifier{alt}}, {kb.ArrowDown, []chromedp.Modifier{alt}}},
		"nextChange": {{kb.F7, nil}, {kb.F7, nil}},
		"prevChange": {{kb.F7, []chromedp.Modifier{shift}}, {kb.F7, []chromedp.Modifier{shift}}},
		"submitNote": {{"\r", []chromedp.Modifier{meta}}, {"\r", []chromedp.Modifier{ctrl}}},
		"refreshPrs": {{"y", []chromedp.Modifier{meta, alt}}, {"y", []chromedp.Modifier{ctrl, alt}}},
		"prsBack":    {{kb.ArrowLeft, []chromedp.Modifier{alt}}, {kb.ArrowLeft, []chromedp.Modifier{alt}}},
		"prevNote":   {{kb.ArrowUp, []chromedp.Modifier{alt}}, {kb.ArrowUp, []chromedp.Modifier{alt}}},
		"markViewed": {{"v", nil}, {"v", nil}},
	}
	c, ok := keys[action]
	if !ok {
		p.t.Fatalf("no key for %s", action)
	}
	k := c[1]
	if p.mac {
		k = c[0]
	}
	p.key(action, k.key, k.mods...)
}

// onEveryPage runs js first in each page that loads from now on, until stop runs.
func (p page) onEveryPage(js string) (stop func()) {
	p.t.Helper()
	var id cdppage.ScriptIdentifier
	p.act("add a script", chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
		res, err := cdp.Call(ctx, t, cdppage.AddScriptToEvaluateOnNewDocument, cdppage.AddScriptToEvaluateOnNewDocumentParams{Source: js})
		id = res.Identifier
		return err
	}))
	return func() {
		p.act("remove the script", chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
			_, err := cdp.Call(ctx, t, cdppage.RemoveScriptToEvaluateOnNewDocument, cdppage.RemoveScriptToEvaluateOnNewDocumentParams{Identifier: id})
			return err
		}))
	}
}

// hover moves the mouse over the left part of an element.
func (p page) hover(what, el string) {
	p.t.Helper()
	at := evalJS[[]float64](p, `(e => { e.scrollIntoView({block: 'center'}); const r = e.getBoundingClientRect(); return [r.left + 40, r.top + r.height / 2]; })(`+el+`)`)
	p.act("hover "+what, chromedp.MouseEvent(input.DispatchMouseEventTypeMouseMoved, at[0], at[1]))
}

func (p page) evalJS(js string) {
	p.t.Helper()
	evalJS[any](p, js+`; null`)
}

// emulate sets the color scheme and the window width the page sees.
func (p page) emulate(scheme string, width int64) {
	p.t.Helper()
	p.act("emulate "+scheme, chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
		_, err := cdp.Call(ctx, t, emulation.SetEmulatedMedia, emulation.SetEmulatedMediaParams{
			Features: []*emulation.MediaFeature{{Name: "prefers-color-scheme", Value: scheme}},
		})
		return err
	}))
	p.act("resize", chromedp.EmulateViewport(width, 900))
	// The rail folds or unfolds a moment later, and moves what the next click aims at.
	p.waitFor("the layout for the new width", fmt.Sprintf(
		`innerWidth === %d && document.body.classList.contains('rail-collapsed') === matchMedia('(max-width: 900px)').matches`, width))
}

func (p page) screenshot(dir, name string) {
	p.t.Helper()
	ctx, cancel := context.WithTimeout(p.ctx, stepTimeout)
	defer cancel()
	b, err := chromedp.Run(ctx, chromedp.CaptureScreenshot())
	if err != nil {
		p.fail("screenshot %s: %v", name, err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".png"), b, 0o644); err != nil {
		p.t.Fatal(err)
	}
}

// noErrors fails the test when the page threw an error that nothing caught since the last check.
func (p page) noErrors() {
	p.t.Helper()
	p.log.mu.Lock()
	var errs []string
	for _, m := range p.log.msgs[p.log.seen:] {
		if m.IsException() {
			errs = append(errs, m.String())
		}
	}
	p.log.seen = len(p.log.msgs)
	p.log.mu.Unlock()
	if len(errs) > 0 {
		p.fail("the page threw:\n%s", strings.Join(errs, "\n"))
	}
}

// fail saves a screenshot, the page, the console and kijk's log, then stops the test.
func (p page) fail(format string, args ...any) {
	p.t.Helper()
	dir := e2eOut(p.t)
	ctx, cancel := context.WithTimeout(p.ctx, 10*time.Second)
	defer cancel()
	if b, err := chromedp.Run(ctx, chromedp.CaptureScreenshot()); err == nil {
		os.WriteFile(filepath.Join(dir, "screenshot.png"), b, 0o644)
	}
	if html, err := chromedp.Run(ctx, chromedp.Evaluate[string](`document.documentElement.outerHTML`)); err == nil {
		os.WriteFile(filepath.Join(dir, "page.html"), []byte(html), 0o644)
	}
	p.log.mu.Lock()
	var console strings.Builder
	for _, m := range p.log.msgs {
		fmt.Fprintln(&console, m.String())
	}
	p.log.mu.Unlock()
	os.WriteFile(filepath.Join(dir, "console.txt"), []byte(console.String()), 0o644)
	if b, err := os.ReadFile(p.k.logFile); err == nil {
		os.WriteFile(filepath.Join(dir, "kijk.log"), b, 0o644)
	}
	p.t.Fatalf(format+"\nThe screenshot, page, console and kijk log are in %s", append(args, dir)...)
}

func jsString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// byText is JavaScript for the first element that matches css and has text as its whole text.
func byText(css, text string) string {
	return fmt.Sprintf(`[...document.querySelectorAll(%s)].find(e => e.textContent.trim() === %s)`, jsString(css), jsString(text))
}

func treeItem(name string) string {
	return byText("#files .tree .item", name)
}

// namedRepo is a git repository in a new folder called name, so kijk shows that name.
func namedRepo(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	run(t, t.TempDir(), "init", "-q", "-b", "main", dir)
	return dir
}

// shopMain is a Go file long enough to scroll, with items in it.
func shopMain(items int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "package main\n\nimport \"fmt\"\n\nconst items = %d\n\nfunc main() {\n\tfmt.Println(total(prices()), items)\n}\n", items)
	for i := 1; i <= 12; i++ {
		fmt.Fprintf(&b, "\nfunc step%02d() int {\n\treturn %d\n}\n", i, i)
	}
	return b.String()
}

// shopRepo is the folder the Claude Code agent works in: code, docs, a diagram and an image, all
// committed, then a changed file and a new one.
func shopRepo(t *testing.T) string {
	t.Helper()
	dir := namedRepo(t, "shop")
	write(t, filepath.Join(dir, "README.md"), "# Shop\n\nA small shop for the browser tests.\n\n- Cart\n- Checkout\n\nRead the [guide](docs/guide.adoc).\n")
	write(t, filepath.Join(dir, "docs", "guide.adoc"), "= Shop guide\n\n== Setup\n\nRun the shop with `go run ./src`.\n")
	write(t, filepath.Join(dir, "docs", "flow.mmd"), "flowchart LR\n  Cart --> Checkout\n")
	write(t, filepath.Join(dir, "src", "main.go"), shopMain(7))
	write(t, filepath.Join(dir, "src", "util.go"), "package main\n\nfunc total(prices []int) int {\n\tsum := 0\n\tfor _, p := range prices {\n\t\tsum += p\n\t}\n\treturn sum\n}\n\nfunc prices() []int { return []int{3, 4} }\n")
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for i := range img.Pix {
		img.Pix[i] = 0xff
	}
	img.Set(3, 3, color.RGBA{0xd0, 0x40, 0x20, 0xff})
	var b bytes.Buffer
	png.Encode(&b, img)
	write(t, filepath.Join(dir, "logo.png"), b.String())
	run(t, dir, "add", ".")
	run(t, dir, "commit", "-qm", "Add the shop")
	write(t, filepath.Join(dir, "src", "main.go"), shopMain(70))
	write(t, filepath.Join(dir, "notes.txt"), "Ask about the cart limit.\n")
	return dir
}

// apiRepo is the folder the Codex agent works in: a clone that is up to date with its origin,
// then a changed file and a new one.
func apiRepo(t *testing.T) (dir, origin string) {
	t.Helper()
	origin = filepath.Join(t.TempDir(), "api.git")
	run(t, t.TempDir(), "init", "-q", "--bare", "-b", "main", origin)
	dir = namedRepo(t, "api")
	write(t, filepath.Join(dir, "handler.go"), "package api\n\nfunc loggedIn(user string) bool {\n\treturn user != \"\"\n}\n")
	run(t, dir, "add", ".")
	run(t, dir, "commit", "-qm", "Add the handler")
	run(t, dir, "remote", "add", "origin", origin)
	run(t, dir, "push", "-q", "-u", "origin", "main")
	write(t, filepath.Join(dir, "handler.go"), "package api\n\nfunc loggedIn(user string) bool {\n\treturn len(user) > 2\n}\n")
	write(t, filepath.Join(dir, "todo.txt"), "Check the session expiry.\n")
	return dir, origin
}

// claudeAgent writes the session file of a Claude Code agent in config, run by this test's own
// process, so kijk sees it running.
// journeyMachine saves one fake machine. The real binary keeps its logins where a user's kijk does,
// so the login goes when the journey ends; its target is a name no one else uses.
func journeyMachine(t *testing.T) (*fakeMachines, string) {
	t.Helper()
	m := newFakeMachines(t)
	vm := m.add("vm", "vm", "")
	t.Cleanup(func() {
		os.Remove(filepath.Join("/tmp", fmt.Sprintf("kijk-%d", os.Getuid()), filepath.Base(newSSHClient(vm).ctl)))
	})
	return m, vm
}

func claudeAgent(t *testing.T, config, cwd, name, status string) {
	t.Helper()
	write(t, filepath.Join(config, "sessions", "1.json"), agentstest.SessionJSON(os.Getpid(), "s-"+name, cwd, name, status, time.Now().UnixMilli()))
}

// keepEngines lets the diagram engines that kijk downloads into home outlive the test, so a
// machine downloads them once. env points kijk's cache into home.
func keepEngines(t *testing.T, home string) (env string) {
	t.Helper()
	user, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	kept := filepath.Join(user, "kijk-e2e", "engines")
	cache := filepath.Join(home, ".cache")
	dir := filepath.Join(cache, "kijk", "engines")
	if runtime.GOOS == "darwin" {
		dir = filepath.Join(home, "Library", "Caches", "kijk", "engines")
	}
	copyEngines(kept, dir)
	t.Cleanup(func() { copyEngines(dir, kept) })
	return "XDG_CACHE_HOME=" + cache
}

func copyEngines(from, to string) {
	files, _ := filepath.Glob(filepath.Join(from, "*.js"))
	for _, f := range files {
		dst := filepath.Join(to, filepath.Base(f))
		if fileCached(dst) {
			continue
		}
		if b, err := os.ReadFile(f); err == nil && os.MkdirAll(to, 0o755) == nil {
			os.WriteFile(dst, b, 0o644)
		}
	}
}

func fileCached(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Size() > 0
}

// sessionGo is the agent's file in the branch review journey: a session store, long enough to scroll.
func sessionGo(lookup, extra string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "package auth\n\nimport \"net/http\"\n\n// Store keeps the sessions.\ntype Store struct{ byID map[string]*Session }\n\n")
	fmt.Fprintf(&b, "func (s *Store) Resume(w http.ResponseWriter, r *http.Request) (*Session, error) {\n\tc, err := r.Cookie(cookieName)\n\tif err != nil {\n\t\treturn nil, ErrNoSession\n\t}\n")
	fmt.Fprintf(&b, "\t%s\n\tif !ok {\n\t\treturn nil, ErrNoSession\n\t}\n\treturn sess, nil\n}\n", lookup)
	for i := 1; i <= 16; i++ {
		fmt.Fprintf(&b, "\nfunc check%02d() bool {\n\treturn %d > 0\n}\n", i, i)
	}
	return b.String() + extra
}

const lookupGo = "\n// lookup reads a session without touching its expiry.\nfunc (s *Store) lookup(id string) (*Session, bool) {\n\tsess, ok := s.byID[id]\n\treturn sess, ok\n}\n"

// reviewRepo is the folder of an agent's branch fix-login, whose origin is on GitHub: one commit
// pushed, one not, an edit not committed and a new file. git pushes to a bare repo in GitHub's place.
func reviewRepo(t *testing.T) (dir, origin string) {
	t.Helper()
	origin = filepath.Join(t.TempDir(), "review.git")
	run(t, t.TempDir(), "init", "-q", "--bare", "-b", "main", origin)
	dir = namedRepo(t, "review")
	write(t, filepath.Join(dir, "README.md"), "# Review\n\nA shop backend.\n")
	write(t, filepath.Join(dir, "go.sum"), "example.com/a v1.0.0 h1:aaa=\n")
	write(t, filepath.Join(dir, "internal", "auth", "session.go"), sessionGo("sess, ok := s.byID[c.Value]", ""))
	run(t, dir, "add", ".")
	run(t, dir, "commit", "-qm", "Add the session store")
	const url = "https://github.com/acme/review.git"
	run(t, dir, "remote", "add", "origin", url)
	run(t, dir, "config", "url."+origin+".insteadOf", url)
	run(t, dir, "push", "-q", "-u", "origin", "main")
	run(t, dir, "checkout", "-qb", "fix-login")
	write(t, filepath.Join(dir, "internal", "auth", "session.go"), sessionGo("sess, ok := s.lookup(c.Value)", ""))
	write(t, filepath.Join(dir, "go.sum"), "example.com/a v1.0.0 h1:aaa=\nexample.com/b v1.2.0 h1:bbb=\nexample.com/c v0.3.0 h1:ccc=\n")
	var js strings.Builder
	for i := 1; i <= 30; i++ {
		fmt.Fprintf(&js, "export const step%02d = () => %d;\n", i, i)
	}
	write(t, filepath.Join(dir, "web", "login.js"), js.String())
	run(t, dir, "add", ".")
	run(t, dir, "commit", "-qm", "Look sessions up")
	run(t, dir, "push", "-q", "-u", "origin", "fix-login")
	write(t, filepath.Join(dir, "internal", "auth", "session.go"), sessionGo("sess, ok := s.lookup(c.Value)", lookupGo))
	write(t, filepath.Join(dir, "internal", "auth", "redirect.go"), "package auth\n\n// safeNext keeps next only when it is a path on this site.\nfunc safeNext(next string) string {\n\tif len(next) == 0 || next[0] != '/' {\n\t\treturn \"/\"\n\t}\n\treturn next\n}\n")
	run(t, dir, "add", ".")
	run(t, dir, "commit", "-qm", "Add safe redirects")
	write(t, filepath.Join(dir, "README.md"), "# Review\n\nA shop backend.\n\nRun it with go run.\n")
	write(t, filepath.Join(dir, "internal", "auth", "redirect_test.go"), "package auth\n\nimport \"testing\"\n\nfunc TestSafeNext(t *testing.T) {\n\tif safeNext(\"//evil.com\") != \"/\" {\n\t\tt.Fatal(\"open redirect\")\n\t}\n}\n")
	return dir, origin
}
