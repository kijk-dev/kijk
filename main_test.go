package main

import (
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestParseArgs(t *testing.T) {
	shop := prLink{Kind: "github", Host: "github.com", Path: "acme/shop", Number: 477}
	for _, c := range []struct {
		args    []string
		port    int
		folders []string
		list    string
		pr      prLink
		flags   []string
		err     string
	}{
		{args: nil, port: 7717},
		{args: []string{"./update"}, port: 7717, folders: []string{"./update"}},
		{args: []string{"-port", "7800", "~/a", "b"}, port: 7800, folders: []string{"~/a", "b"}, flags: []string{"-port", "7800"}},
		{args: []string{"folders"}, port: 7717, list: listFolders},
		{args: []string{"folders", "-no-open"}, port: 7717, list: listFolders, flags: []string{"-no-open"}},
		{args: []string{"-no-open", "https://github.com/acme/shop/pull/477"}, port: 7717, pr: shop, flags: []string{"-no-open"}},
		{args: []string{"folders", "a"}, err: "takes no folder"},
		{args: []string{"https://github.com/acme/shop/pull/477", "~/code"}, err: "takes one link and no folder"},
		{args: []string{"-nope"}, err: "not defined"},
	} {
		a, err := parseArgs(c.args)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%q: want error %q, got %v", c.args, c.err, err)
			}
			continue
		}
		if err != nil || a.port != c.port || !slices.Equal(a.folders, c.folders) || a.list != c.list || a.pr != c.pr || !slices.Equal(a.flags, c.flags) {
			t.Errorf("%q: got %+v %v", c.args, a, err)
		}
	}
}

// What each command sends to the kijk that shows it.
func TestAskOf(t *testing.T) {
	adds := []savedFolder{{Path: "/code/kijk", Open: "/code/kijk/web"}, {Path: "/code/api"}}
	for _, c := range []struct {
		a    cliArgs
		adds []savedFolder
		want string
	}{
		{cliArgs{}, nil, `{}`},
		{cliArgs{folders: []string{"web", "~/code/api"}}, adds, `{"folders":["/code/kijk/web","/code/api"]}`},
		{cliArgs{list: listFolders}, nil, `{"list":"folders"}`},
		{cliArgs{pr: prLink{Kind: "github", Host: "github.com", Path: "acme/shop", Number: 477}}, nil, `{"pr":{"kind":"github","host":"github.com","path":"acme/shop","number":477},"cwd":"/code/shop/web"}`},
	} {
		if got := jsonOf(askOf(c.a, c.adds, "/code/shop/web")); got != c.want {
			t.Errorf("%+v: got %s, want %s", c.a, got, c.want)
		}
	}
}

func TestAddedText(t *testing.T) {
	kijk := []savedFolder{{Path: "/code/kijk", Open: "/code/kijk/web"}}
	api := []savedFolder{{Path: "/code/api"}}
	two := append(api, kijk...)
	for _, c := range []struct {
		adds   []savedFolder
		added  []bool
		window bool
		want   string
	}{
		{kijk, []bool{true}, false, "Added kijk, opened at web/."},
		{api, []bool{true}, true, "Added api. It is open in the kijk window."},
		{api, []bool{false}, true, "api is already in the Folders list. It is open in the kijk window."},
		{two, []bool{true, true}, false, "Added 2 folders to the Folders list."},
		{two, []bool{true, false}, true, "Added 1 folder to the Folders list. 1 was already in it. The last one is open in the kijk window."},
		{two, []bool{false, false}, false, "All 2 folders are already in the Folders list."},
	} {
		if got := addedText(c.adds, c.added, c.window); got != c.want {
			t.Errorf("got %q, want %q", got, c.want)
		}
	}
}

// The folder view must work on a page without the rail, so viewer files never import app files.
func TestViewerImportsNoAppFile(t *testing.T) {
	app := []string{"app.js", "status.js", "rail.js"}
	imports := regexp.MustCompile(`\b(?:from|import)\s*\(?\s*['"]\./([^'"]+)['"]`)
	files, _ := fs.Glob(webFiles, "web/*.js")
	for _, f := range files {
		if slices.Contains(app, path.Base(f)) {
			continue
		}
		b, err := fs.ReadFile(webFiles, f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range imports.FindAllStringSubmatch(string(b), -1) {
			if slices.Contains(app, m[1]) {
				t.Errorf("%s imports %s, an app file", f, m[1])
			}
		}
	}
	if len(files) < 10 {
		t.Fatalf("found only %d JavaScript files", len(files))
	}
}

// TestPageKnowsEveryList checks that the page can show every list an open request names.
func TestPageKnowsEveryList(t *testing.T) {
	b, err := fs.ReadFile(webFiles, "web/rail.js")
	if err != nil {
		t.Fatal(err)
	}
	lists := regexp.MustCompile(`LISTS = \[([^\]]*)\]`).FindSubmatch(b)
	if lists == nil || !strings.Contains(string(lists[1]), "'"+listFolders+"'") {
		t.Errorf("rail.js LISTS does not have %q", listFolders)
	}
}

// prsTabFiles are the web files of the PRs tab, besides prstore.js.
var prsTabFiles = []string{"pr.js", "prs.js", "review.js", "drafts.js", "boxes.js", "changes.js", "overview.js", "threads.js"}

// TestPRsTabAsksThroughTheStore checks that only prstore.js asks the forge about pull requests or
// writes to it, so every answer goes through its one rule of which answer is newer.
func TestPRsTabAsksThroughTheStore(t *testing.T) {
	asks := regexp.MustCompile(`(?:^|[^.\w])get\(\s*['"](?:prs?|pr/view|pr/since)['"]`)
	posts := regexp.MustCompile(`\bpost\(`)
	files, _ := fs.Glob(webFiles, "web/*.js")
	for _, f := range files {
		name := path.Base(f)
		if name == "prstore.js" {
			continue
		}
		b, err := fs.ReadFile(webFiles, f)
		if err != nil {
			t.Fatal(err)
		}
		if asks.Match(b) {
			t.Errorf("%s asks the forge about pull requests; ask through prstore.js", f)
		}
		if slices.Contains(prsTabFiles, name) && posts.Match(b) {
			t.Errorf("%s writes to the forge; write through prstore.js", f)
		}
	}
}

// TestPRsTabDrawsInPlace checks that the PRs tab draws through patch, which keeps the elements the
// user holds, instead of building them again and putting back the focus or the scroll place by hand.
func TestPRsTabDrawsInPlace(t *testing.T) {
	rebuilds := regexp.MustCompile(`\bkeepFocus\b|\.replaceChildren\(|\.scrollTop\s*=[^=]`)
	// The lines that move the view on purpose.
	allowed := []struct{ file, line, why string }{
		{"boxes.js", "scroll.scrollTop = v.keep.scrollTop", "a tab shown again starts where it was"},
		{"boxes.js", "v.scroll.scrollTop = a.b.el.offsetTop + a.offset", "the box at the top stays while boxes above it fold, open or load"},
		{"boxes.js", "v.scroll.scrollTop = b.el.offsetTop - 12", "a jump to a file, or to the header of a box folded across the top"},
		{"boxes.js", "v.scroll.scrollTop = y", "the arrow keys keep the current line in view"},
		{"boxes.js", "v.scroll.scrollTop = Math.max(0, y - AHEAD)", "a jump to the next change, thread or note"},
		{"changes.js", "v.scroll.scrollTop = 0", "the other diff of the Since switch starts at the top"},
	}
	used := make([]bool, len(allowed))
	for _, name := range prsTabFiles {
		b, err := fs.ReadFile(webFiles, "web/"+name)
		if err != nil {
			t.Fatal(err)
		}
	lines:
		for i, line := range strings.Split(string(b), "\n") {
			if !rebuilds.MatchString(line) {
				continue
			}
			for j, a := range allowed {
				if a.file == name && strings.Contains(line, a.line) {
					used[j] = true
					continue lines
				}
			}
			t.Errorf("%s:%d: draw through patch: %s", name, i+1, strings.TrimSpace(line))
		}
	}
	for j, a := range allowed {
		if !used[j] {
			t.Errorf("%s no longer has %q; drop it from the allowed lines", a.file, a.line)
		}
	}
}

// boxFiles are the web files another page copies to show file boxes.
var boxFiles = []string{"api.js", "lines.js", "layout.js", "keys.js", "boxes.js"}

// TestBoxFilesDoNothingOnLoad checks that the box files need no other web file, and look up no
// element, call no server and add no listener when they load. A page starts them when it is ready.
func TestBoxFilesDoNothingOnLoad(t *testing.T) {
	imports := regexp.MustCompile(`\b(?:from|import)\s*\(?\s*['"]\./([^'"]+)['"]`)
	for _, name := range boxFiles {
		b, err := fs.ReadFile(webFiles, "web/"+name)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range imports.FindAllStringSubmatch(string(b), -1) {
			if !slices.Contains(boxFiles, m[1]) {
				t.Errorf("%s imports %s, which is not a box file", name, m[1])
			}
		}
		for _, h := range onLoad(string(b)) {
			t.Errorf("%s:%d: %s runs when the file loads; move it into a function", name, h.line, h.what)
		}
	}
}

func TestOnLoad(t *testing.T) {
	for _, c := range []struct {
		src  string
		want []string
	}{
		{"const a = document.body;", []string{"document."}},
		{"if (x) {\n  addEventListener('resize', f);\n}", []string{"addEventListener"}},
		{"const types = await get('filetypes');", []string{"await", "get("}},
		{"new ResizeObserver(f).observe(v);", []string{"new ResizeObserver"}},
		{"const t = `a ${document.title} b`;", []string{"document."}},
		{"const f = x => x, g = document.getElementById('v');", []string{"document.", "getElementById"}},
		{"function f() { document.body; }", nil},
		{"export async function f() { await get('x'); }", nil},
		{"const f = () => document.body;", nil},
		{"const f = async (a, b) => { await fetch(a); };", nil},
		{"onKey('next', () => { document.activeElement.blur(); });", nil},
		{"const o = { draw() { addEventListener('x', f); } };", nil},
		{"const t = `${xs.map(x => document.title)}`;", nil},
		{"export async function get(path) { return fetch(path); }", nil},
		{"m.get(k);\n// document.body\nconst s = 'document.', r = /document\\./g;\n/* await */", nil},
	} {
		var got []string
		for _, h := range onLoad(c.src) {
			got = append(got, h.what)
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("onLoad(%q) = %q, want %q", c.src, got, c.want)
		}
	}
}

type onLoadHit struct {
	line int
	what string
}

var onLoadCode = regexp.MustCompile(`\bawait\b|\bdocument\.|\bgetElementById\b|\baddEventListener\b|\bnew\s+ResizeObserver\b|(?:^|[^.\w$])(?:get|post|fetch)\(`)

// onLoad finds the code of a JavaScript module that touches the page or the server when it loads.
func onLoad(src string) []onLoadHit {
	code := topLevel(src)
	var hits []onLoadHit
	for _, m := range onLoadCode.FindAllStringIndex(code, -1) {
		if strings.HasSuffix(strings.TrimSpace(code[:m[0]+1]), "function") {
			continue // the name of a function, not a call
		}
		what := strings.TrimLeft(code[m[0]:m[1]], " \t\n(=,;{}[!&|?:")
		hits = append(hits, onLoadHit{strings.Count(code[:m[0]], "\n") + 1, what})
	}
	return hits
}

// topLevel blanks the comments, strings, regular expressions and function bodies of JavaScript src
// and keeps its lines. What is left runs when the module loads.
func topLevel(src string) string {
	type frame struct {
		fn, arrow, tmpl bool   // a function body; an arrow function without braces; a template's ${ }
		before          string // the token before a (
	}
	out := []byte(src)
	var stack []frame
	blank := func(from, to int) {
		for i := from; i < to && i < len(out); i++ {
			if out[i] != '\n' {
				out[i] = ' '
			}
		}
	}
	inFn := func() bool {
		return slices.ContainsFunc(stack, func(f frame) bool { return f.fn })
	}
	ident := func(c byte) bool {
		return c == '_' || c == '$' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
	}
	regexAfter := func(tok string) bool {
		return tok == "" || tok == "=>" || len(tok) == 1 && strings.Contains("(,=:[!&|?{};+-*%<>~^", tok) ||
			slices.Contains([]string{"return", "typeof", "case", "do", "else", "in", "of", "new", "delete", "void", "throw", "yield", "await"}, tok)
	}
	i, last, closed := 0, "", ""
	// template blanks a template string from i to its end, or to its next ${, which it opens.
	template := func() {
		for start := i; i < len(src); i++ {
			switch {
			case src[i] == '\\':
				i++
			case src[i] == '`':
				blank(start, i)
				i++
				last = "`"
				return
			case strings.HasPrefix(src[i:], "${"):
				blank(start, i)
				i += 2
				stack = append(stack, frame{tmpl: true})
				last = "{"
				return
			}
		}
	}
	endArrows := func() {
		for len(stack) > 0 && stack[len(stack)-1].arrow {
			stack = stack[:len(stack)-1]
		}
	}
	for i < len(src) {
		c, start := src[i], i
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
			continue
		case strings.HasPrefix(src[i:], "//"):
			if n := strings.IndexByte(src[i:], '\n'); n >= 0 {
				i += n
			} else {
				i = len(src)
			}
			blank(start, i)
			continue
		case strings.HasPrefix(src[i:], "/*"):
			if n := strings.Index(src[i+2:], "*/"); n >= 0 {
				i += n + 4
			} else {
				i = len(src)
			}
			blank(start, i)
			continue
		case c == '\'' || c == '"':
			for i++; i < len(src) && src[i] != c; i++ {
				if src[i] == '\\' {
					i++
				}
			}
			i++
			blank(start+1, i-1)
			last = "'"
		case c == '`':
			i++
			template()
		case c == '/' && regexAfter(last):
			class := false
			for i++; i < len(src) && (class || src[i] != '/'); i++ {
				switch src[i] {
				case '\\':
					i++
				case '[':
					class = true
				case ']':
					class = false
				}
			}
			for i++; i < len(src) && ident(src[i]); i++ {
			}
			blank(start+1, i)
			last = "/"
		case ident(c):
			for i < len(src) && ident(src[i]) {
				i++
			}
			last = src[start:i]
		case strings.HasPrefix(src[i:], "=>"):
			i += 2
			last = "=>"
			j := i
			for j < len(src) && strings.IndexByte(" \t\n\r", src[j]) >= 0 {
				j++
			}
			if j < len(src) && src[j] != '{' {
				stack = append(stack, frame{fn: true, arrow: true})
			}
		case c == '(' || c == '[' || c == '{':
			f := frame{before: last}
			// A { after => or after the ( ) of a function or method opens its body; after if ( ) a block.
			f.fn = c == '{' && (last == "=>" || last == ")" && closed != "" && ident(closed[0]) &&
				!slices.Contains([]string{"if", "for", "while", "switch", "catch", "with"}, closed))
			stack = append(stack, f)
			i++
			last = string(c)
		case c == ')' || c == ']' || c == '}':
			endArrows()
			i++
			last = string(c)
			if len(stack) > 0 {
				f := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				if f.tmpl {
					template()
				}
				closed = f.before
			}
		case c == ',' || c == ';':
			endArrows()
			i++
			last = string(c)
		default:
			i++
			last = string(c)
		}
		if inFn() {
			blank(start, i)
		}
	}
	return string(out)
}

// TestDarkThemeMatchesTheSystem checks that every rule kijk applies when the system is dark is
// skipped with data-theme="kijk-light" and has a copy for data-theme="kijk-dark" that sets the same.
func TestDarkThemeMatchesTheSystem(t *testing.T) {
	const system, chosen = `:not([data-theme="kijk-light"])`, `[data-theme="kijk-dark"]`
	n := 0
	for _, name := range []string{"viewer.css", "app.css"} {
		b, err := fs.ReadFile(webFiles, "web/"+name)
		if err != nil {
			t.Fatal(err)
		}
		css := string(b)
		for rest := css; ; {
			at := strings.Index(rest, "@media (prefers-color-scheme: dark) {")
			if at < 0 {
				break
			}
			media, after := cssBlock(rest[at:])
			rest = after
			for media = strings.TrimSpace(media); media != ""; media = strings.TrimSpace(media) {
				selector, _, _ := strings.Cut(media, "{")
				selector = strings.TrimSpace(selector)
				var body string
				body, media = cssBlock(media)
				if !strings.Contains(selector, system) {
					t.Errorf("%s: %s applies when the system is dark, also with kijk-light", name, selector)
					continue
				}
				twin := strings.Replace(selector, system, chosen, 1)
				at := strings.Index(css, "\n"+twin+" {")
				if at < 0 {
					t.Errorf("%s: %s has no copy %s", name, selector, twin)
					continue
				}
				copied, _ := cssBlock(css[at:])
				if got, want := declarations(copied), declarations(body); !slices.Equal(got, want) {
					t.Errorf("%s: %s sets %q, want %q as %s", name, twin, got, want, selector)
				}
				n += len(declarations(body))
			}
		}
	}
	if n < 30 {
		t.Errorf("found %d dark declarations; the test no longer reads the CSS", n)
	}
}

// cssBlock gives the inside of the first { } of css and what follows it.
func cssBlock(css string) (inside, after string) {
	open := strings.Index(css, "{")
	depth := 0
	for i := open; i < len(css); i++ {
		switch css[i] {
		case '{':
			depth++
		case '}':
			if depth--; depth == 0 {
				return css[open+1 : i], css[i+1:]
			}
		}
	}
	return css[open+1:], ""
}

// declarations gives the sorted declarations of a rule, without color-scheme, which kijk-dark adds.
func declarations(body string) []string {
	var out []string
	for d := range strings.SplitSeq(regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(body, ""), ";") {
		if d = strings.TrimSpace(d); d != "" && !strings.HasPrefix(d, "color-scheme:") {
			out = append(out, d)
		}
	}
	slices.Sort(out)
	return out
}
