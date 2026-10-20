package main

import (
	"encoding/json"
	"fmt"
	"html"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// adocHas renders src and checks that each want is in the HTML and no bad is.
func adocHas(t *testing.T, src string, want []string, bad ...string) string {
	t.Helper()
	out, _ := renderAsciiDoc([]byte(src), "", nil)
	adocCheck(t, out, want, bad)
	return out
}

// adocIncluding renders docs/guide.adoc with files as the rest of its folder, and gives the files
// it included.
func adocIncluding(t *testing.T, src string, files map[string]string, want []string, bad ...string) []string {
	t.Helper()
	out, inc := renderAsciiDoc([]byte(src), "docs/guide.adoc", func(p string) ([]byte, error) {
		if s, ok := files[p]; ok {
			return []byte(s), nil
		}
		return nil, fs.ErrNotExist
	})
	adocCheck(t, out, want, bad)
	return inc
}

func adocCheck(t *testing.T, out string, want, bad []string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("missing %s in\n%s", w, out)
		}
	}
	for _, b := range bad {
		if strings.Contains(out, b) {
			t.Errorf("%s kept in\n%s", b, out)
		}
	}
}

func TestAsciiDocBlocks(t *testing.T) {
	for _, c := range []struct {
		name, src string
		want      []string
		bad       []string
	}{
		{"title and header", "= User Guide\nJane Doe <jane@example.com>\nv1.0, 2026-10-20\n:toc:\n\nText.\n",
			[]string{`<h1 data-lines="1-1">User Guide</h1>`, `<p class="doc-header" data-lines="2-5">Jane Doe &lt;jane@example.com&gt;<br>v1.0, 2026-10-20</p>`}, []string{":toc:"}},
		{"sections", "== Install\n\n=== Linux ===\n\n## Markdown style\n",
			[]string{`<h2 data-lines="1-2">Install</h2>`, `<h3 data-lines="3-4">Linux</h3>`, `<h2 data-lines="5-5">Markdown style</h2>`}, nil},
		{"paragraph and break", "One\ntwo +\nthree\n\n'''\n",
			[]string{`<p data-lines="1-4">One` + "\ntwo <br>\nthree</p>", `<hr data-lines="5-5">`}, nil},
		{"hard breaks", "[%hardbreaks]\nOne\ntwo\n", []string{"One<br>\ntwo"}, nil},
		{"listing", "----\nplain <b>\n----\n", []string{`<pre data-lines="1-3"><code>plain &lt;b&gt;</code></pre>`}, nil},
		{"literal", "....\n*not bold*\n....\n\n  indented\n    more\n",
			[]string{`<pre data-lines="1-4"><code>*not bold*</code></pre>`, `<pre data-lines="5-6"><code>indented` + "\n" + `  more</code></pre>`}, nil},
		{"fence", "```go\nfunc f() {}\n```\n", []string{`<pre data-lines="1-3"><code><span class="hk">func</span>`}, nil},
		{"comments", "// gone\n////\nalso gone\n////\nShown\n// dropped\nstill shown\n",
			[]string{"<p data-lines=\"5-7\">Shown\nstill shown</p>"}, []string{"gone", "dropped"}},
		{"admonition paragraph", "WARNING: Back up first.\n",
			[]string{`<blockquote class="admonition warning" data-lines="1-1">`, `<p class="admonition-label">Warning</p>`, `<p>Back up first.</p>`}, nil},
		{"admonition block", ".Before you start\n[TIP]\n====\nOne.\n\nTwo.\n====\n",
			[]string{`<blockquote class="admonition tip" data-lines="1-7">`, `<p class="admonition-label">Tip</p>` + "\n" + `<p class="block-title">Before you start</p>`, "<p>One.</p>\n<p>Two.</p>"}, []string{"data-lines=\"4"}},
		{"admonition style on a paragraph", "[NOTE]\nA note.\n", []string{`class="admonition note"`, "<p>A note.</p>"}, nil},
		{"block title", ".Main code\n[source,go]\n----\nx := 1\n----\n",
			[]string{`<p class="block-title" data-lines="1-2">Main code</p>`, `<pre data-lines="3-5"><code>x <span class="ho">:=</span>`}, nil},
		{"example and sidebar", "====\nIn example.\n====\n\n.Aside\n****\nIn sidebar.\n****\n",
			[]string{`<blockquote class="example" data-lines="1-4">` + "\n<p>In example.</p>", `<blockquote class="sidebar" data-lines="5-8">` + "\n" + `<p class="block-title">Aside</p>`}, nil},
		{"open block adds nothing", "--\nInside.\n\n* item\n--\n", []string{`<p data-lines="2-3">Inside.</p>`, `<li data-lines="4-5">item</li>`}, nil},
		{"quote", "[quote, Abraham Lincoln, Gettysburg Address]\n____\nFour score.\n____\n\n[quote]\nShort.\n",
			[]string{"<blockquote data-lines=\"1-5\">\n<p>Four score.</p>\n<p class=\"quote-by\">— Abraham Lincoln, Gettysburg Address</p>", "<p>Short.</p>"}, nil},
		{"verse", "[verse, Poet]\n____\nLine one\nLine two\n____\n", []string{"Line one<br>\nLine two", "— Poet"}, nil},
		{"block image", ":imagesdir: images/\n\n.Logo\nimage::logo.png[The logo, 200]\n\nimage::https://example.com/a.png[]\n\nimage::my_big-pic.png[]\n",
			[]string{`<p class="block-title" data-lines="3-3">Logo</p>`, `<p data-lines="4-5"><img src="images/logo.png" alt="The logo"></p>`,
				`<img src="https://example.com/a.png" alt="a">`, `<img src="images/my_big-pic.png" alt="my big pic">`}, nil},
		{"include", "include::chapters/intro.adoc[leveloffset=+1]\n",
			[]string{`<p class="include" data-lines="1-1"><a href="chapters/intro.adoc">chapters/intro.adoc</a></p>`}, nil},
		{"passthrough block", "++++\n<script>alert(1)</script>\n++++\n",
			[]string{`<pre data-lines="1-3"><code>&lt;script&gt;alert(1)&lt;/script&gt;</code></pre>`}, []string{"<script"}},
		{"unclosed listing runs to the end", "----\nopen\n\nstill code\n", []string{"<code>open\n\nstill code</code>"}, nil},
		{"page break and toc", "<<<\ntoc::[]\nText\n", []string{`<p data-lines="3-3">Text</p>`}, []string{"toc", "&lt;&lt;&lt;"}},
	} {
		t.Run(c.name, func(t *testing.T) { adocHas(t, c.src, c.want, c.bad...) })
	}
}

func TestAsciiDocLists(t *testing.T) {
	adocHas(t, "* one\n** nested\n*** deeper\n* two\ngoes on\n- other marker\n\n. first\n. second\n\n1. numbered\n",
		[]string{
			`<li data-lines="1-3">one<ul>` + "\n" + `<li data-lines="2-3">nested<ul>` + "\n" + `<li data-lines="3-3">deeper</li>`,
			"<li data-lines=\"4-11\">two\ngoes on<ul>\n<li data-lines=\"6-11\">other marker<ol>",
			`<li data-lines="8-8">first</li>`,
			`<li data-lines="9-11">second<ol>` + "\n" + `<li data-lines="11-11">numbered</li>`,
		}, `<ul data-lines`, `<ol data-lines`)
	adocHas(t, "* a\n\n//\n\n* b\n", []string{"<ul>\n<li data-lines=\"1-4\">a</li>\n</ul>\n<ul>\n<li data-lines=\"5-5\">b</li>"})
	adocHas(t, "* a\n+\n----\ncode\n----\n* b\n\n  literal after a blank\n",
		[]string{"<li data-lines=\"1-5\"><p>a</p>\n<pre><code>code</code></pre>\n</li>", "<li data-lines=\"6-8\"><p>b</p>\n<pre><code>literal after a blank</code></pre>"})
	adocHas(t, "[start=4]\n. four\n. five\n", []string{`<ol start="4">`})
}

func TestAsciiDocDescriptionLists(t *testing.T) {
	adocHas(t, "CPU:: The *brain*.\nRAM::\nThe short-term\nmemory.\nKinds::\n* SRAM\n* DRAM\nOuter::\nInner::: nested\nAlso;; semi\n",
		[]string{
			"<dl>\n" + `<dt data-lines="1-1">CPU</dt>` + "\n" + `<dd data-lines="1-1">The <strong>brain</strong>.</dd>`,
			`<dt data-lines="2-2">RAM</dt>` + "\n" + "<dd data-lines=\"3-4\">The short-term\nmemory.</dd>",
			`<dt data-lines="5-5">Kinds</dt>` + "\n" + `<dd data-lines="5-7"><ul>` + "\n" + `<li data-lines="6-6">SRAM</li>`,
			`<dd data-lines="8-10"><dl>` + "\n" + `<dt data-lines="9-9">Inner</dt>` + "\n" + `<dd data-lines="9-10">nested<dl>` + "\n" + `<dt data-lines="10-10">Also</dt>`,
		}, `<dl data-lines`)
	adocHas(t, "The rule is a::b and C++ is fine.\n", []string{"<p data-lines=\"1-1\">The rule is a::b"}, "<dl")
}

func TestAsciiDocInline(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{"*bold* _it_ `mono`", "<strong>bold</strong> <em>it</em> <code>mono</code>"},
		{"un**bold**ed in__side__ x``y``z", "un<strong>bold</strong>ed in<em>side</em> x<code>y</code>z"},
		{"snake_case_name and 2*3*4 and a * b *", "snake_case_name and 2*3*4 and a * b *"},
		{"`+*not bold*+` and +{x}+ and ++a++b", "<code>*not bold*</code> and {x} and ab"},
		{"C++ and C++ stay", "C++ and C++ stay"},
		{`\*not bold* and \https://example.com`, "*not bold* and https://example.com"},
		{"see https://example.com/a_b_c. And (https://x.com/y)", `see <a href="https://example.com/a_b_c">https://example.com/a_b_c</a>. And (<a href="https://x.com/y">https://x.com/y</a>)`},
		{"https://example.com[*Docs*^] and <https://kijk.dev>", `<a href="https://example.com"><strong>Docs</strong></a> and <a href="https://kijk.dev">https://kijk.dev</a>`},
		{"link:docs/guide.pdf[The guide] and mailto:me@example.com[] and mailto:me@example.com[Mail me]", `<a href="docs/guide.pdf">The guide</a> and <a href="mailto:me@example.com">me@example.com</a> and <a href="mailto:me@example.com">Mail me</a>`},
		{"a hyperlink:x[y] stays", "a hyperlink:x[y] stays"},
		{"icon image:icons/ok.svg[OK] here", `icon <img src="icons/ok.svg" alt="OK"> here`},
		{"https://ci.example.com[image:https://ci.example.com/badge.svg[Build]]", `<a href="https://ci.example.com"><img src="https://ci.example.com/badge.svg" alt="Build"></a>`},
		{"image:badge.svg[CI, link=https://ci.example.com]", `<a href="https://ci.example.com"><img src="badge.svg" alt="CI"></a>`},
		{"[[here]]Anchored text", "Anchored text"},
		{"footnote:[Checked in 2026.] and kbd:[Ctrl+C]", "footnote:[Checked in 2026.] and kbd:[Ctrl+C]"},
		{"a << b >> c", "a &lt;&lt; b &gt;&gt; c"},
	} {
		got := adocInline{}.render(c.src)
		if got != c.want {
			t.Errorf("%s\n got %s\nwant %s", c.src, got, c.want)
		}
	}
}

func TestAsciiDocNoHTML(t *testing.T) {
	src := ":evil: <script>alert(1)</script>\n\n<script>alert(2)</script> {evil}\n\n" +
		"https://example.com[<img src=x onerror=alert(3)>] pass:[<b onclick=alert(4)>x</b>] +++<i>y</i>+++\n\n" +
		"link:javascript:alert(5)[click] image:javascript:alert(6)[pic] xref:javascript:alert(7).adoc[go] <<x\" onclick=\"alert(8)>>\n\n" +
		"image::x.png[\"><script>alert(9)</script>]\n\n.<b>title</b>\n----\n<script>\n----\n\n|===\n| <script> | b\n|===\n"
	out, _ := renderAsciiDoc([]byte(src), "", nil)
	for _, bad := range []string{"<script", "<img src=x", "<b", "<i>", "javascript:", `onclick="`} {
		if strings.Contains(out, bad) {
			t.Errorf("%s kept in\n%s", bad, out)
		}
	}
	for _, want := range []string{"&lt;script&gt;alert(1)&lt;/script&gt;", "&lt;b onclick=alert(4)&gt;x&lt;/b&gt;", "&lt;i&gt;y&lt;/i&gt;", "click pic go"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in\n%s", want, out)
		}
	}
}

func TestAsciiDocAttributes(t *testing.T) {
	adocHas(t, "= Doc\n:product: kijk\n:url-repo: https://github.com/kijk-dev/kijk\n:long: one \\\ntwo\n\n"+
		"Welcome to {product}. {url-repo}[Repo]. {long}. {Product}. {missing} stays. \\{product} too.\n"+
		"{asterisk}not bold{asterisk} a{nbsp}b {lt}tag{gt} {cpp}{empty}.\n\n:product!:\n{product} gone.\n\n"+
		":imagesdir: img\nimage:x.png[] image:/abs.png[] image:https://e.com/y.png[]\n\n[source]\n----\n{url-repo}\n----\n",
		[]string{
			`Welcome to kijk. <a href="https://github.com/kijk-dev/kijk">Repo</a>. one two. kijk. {missing} stays. {product} too.`,
			"*not bold* a b &lt;tag&gt; C++.",
			"<p data-lines=\"11-13\">{product} gone.</p>",
			`<img src="img/x.png" alt="x"> <img src="/abs.png" alt="abs"> <img src="https://e.com/y.png" alt="y">`,
			"<code>{url-repo}</code>",
		})
	adocHas(t, ":v: 2.0\n\n[subs=attributes+]\n----\ngem 'x', '{v}' {lt}\n----\n", []string{"<code>gem 'x', '2.0' &lt;</code>"})
	adocHas(t, "Icon  font.\n", []string{"Icon  font."})
}

func TestAsciiDocConditionals(t *testing.T) {
	readme := "= README\n\nifdef::env-github[]\nimage:https://github.com/badge.svg[GitHub badge]\nendif::[]\n" +
		"ifndef::env-github[]\nimage:https://example.com/badge.svg[Plain badge]\nendif::[]\n\nText.\n"
	adocHas(t, readme, []string{"Plain badge", `<h1 data-lines="1-6">README</h1>`, `<p data-lines="7-9">`, `<p data-lines="10-10">Text.</p>`}, "GitHub badge", "ifdef", "ifndef", "endif")

	src := ":beta:\n:gamma:\n\nifdef::beta[]\nBeta on.\nendif::beta[]\nifdef::nope,beta[Either.]\nifdef::nope+beta[Both.]\n" +
		"ifndef::nope[Not nope.]\nifndef::beta,nope[Neither.]\nifndef::beta+nope[Not both.]\n" +
		"ifdef::nope[]\nifdef::beta[]\nInside a hidden one.\nendif::[]\nendif::[]\n" +
		"ifeval::[{level} > 1]\nEval shows.\nendif::[]\nendif::[]\n\\ifdef::beta[]\nifdef::open[]\nNever closed.\n"
	adocHas(t, src, []string{"Beta on.\nEither.\nNot nope.\nNot both.\nEval shows.\nifdef::beta[]</p>"},
		"Both.", "Neither.", "Inside", "Never closed", "endif", "ifeval")

	without, _ := renderAsciiDoc([]byte("= Doc\n\nifdef::beta[]\nBeta text.\nendif::[]\n"), "", nil)
	with, _ := renderAsciiDoc([]byte("= Doc\n:beta:\n\nifdef::beta[]\nBeta text.\nendif::[]\n"), "", nil)
	if strings.Contains(without, "Beta text") || !strings.Contains(with, "Beta text") {
		t.Errorf("without :beta:\n%s\nwith it\n%s", without, with)
	}
}

func TestAsciiDocSectionIDs(t *testing.T) {
	src := "== Getting Started!\n\n== Getting Started!\n\n[[setup]]\n== Set up\n\n[#more]\n== More\n\n=== [[inline]]In Line\n\n" +
		"== Über  die Größe\n\n== C++ & Go: `2` ways\n\n" +
		"See <<_getting_started>>, <<_getting_started_2,the second>>, <<setup>>, <<more,More here>>, <<inline>>, " +
		"<<_über_die_größe>>, <<_c_go_2_ways>>, <<Set up>>, <<nope>> and <<nope,Nope>>.\n" +
		"Files: xref:install.adoc#linux[Linux install], <<other.adoc#x,Other>>, <<guide#y>>, xref:#setup[Here].\n"
	adocHas(t, src, []string{
		`<a href="#getting-started">Getting Started!</a>`,
		`<a href="#getting-started">the second</a>`,
		`<a href="#set-up">Set up</a>`,
		`<a href="#more">More here</a>`,
		`<a href="#in-line">In Line</a>`,
		`<a href="#über--die-größe">Über  die Größe</a>`,
		`<a href="#c--go-2-ways">C++ &amp; Go: 2 ways</a>`,
		`, <a href="#set-up">Set up</a>, [nope] and Nope.`,
		`<a href="install.adoc">Linux install</a>, <a href="other.adoc">Other</a>, <a href="guide.adoc">guide.adoc</a>, <a href="#set-up">Here</a>`,
		`<h3 data-lines="11-12">In Line</h3>`,
	})
	// The want values are what slug in view.js gives.
	for title, want := range map[string]string{"Getting Started!": "getting-started", "Über  die Größe": "über--die-größe",
		"C++ & Go: 2 ways": "c--go-2-ways", "  Trim me  ": "trim-me", "日本語 タイトル": "日本語-タイトル", "İstanbul": "istanbul",
		"snake_case-and-dash": "snake_case-and-dash", "Tab\there": "tab-here", "a b\ufeffc\u0085d": "a-b-cd"} {
		if got := jsSlug(title); got != want {
			t.Errorf("slug(%q) = %q, want %q", title, got, want)
		}
	}
	adocHas(t, ":idprefix:\n:idseparator: -\n\n== Getting Started\n\nSee <<getting-started>>.\n", []string{`<a href="#getting-started">Getting Started</a>`})
}

func TestAsciiDocTables(t *testing.T) {
	for _, c := range []struct {
		name, src string
		want      []string
		bad       []string
	}{
		{"implicit header", "|===\n| Name | Value\n\n| a | *1*\n| b | 2\n|===\n",
			[]string{"<thead>\n<tr>\n<th>Name</th>\n<th>Value</th>", "<td>a</td>\n<td><strong>1</strong></td>"}, nil},
		{"header option", "[%header]\n|===\n| H1 | H2\n| a | b\n|===\n", []string{"<th>H1</th>", "<td>a</td>"}, nil},
		{"options attribute", "[options=\"header\",cols=\"1,1\"]\n|===\n| H1 | H2\n| a | b\n|===\n", []string{"<th>H1</th>"}, nil},
		{"no header", "|===\n| a | b\n| c | d\n|===\n", []string{"<tbody>\n<tr>\n<td>a</td>\n<td>b</td>"}, []string{"<th>", "thead"}},
		{"noheader option", "[%noheader]\n|===\n| a | b\n\n| c | d\n|===\n", []string{"<td>a</td>"}, []string{"<th>"}},
		{"a row over lines", "[cols=\"3*\"]\n|===\n| a\n| b\n| c\n| d\n|===\n",
			[]string{"<tr>\n<td>a</td>\n<td>b</td>\n<td>c</td>\n</tr>\n<tr>\n<td>d</td>\n<td></td>\n<td></td>"}, nil},
		{"cell text on the next line and specs", "|===\n| a | b\nmore\n2+| wide a| *plain* \\| pipe\n|===\n",
			[]string{"<td>b\nmore</td>", "<td>wide</td>", "<td><strong>plain</strong> | pipe</td>"}, []string{"2+", "a|"}},
		{"text before the first cell", "|===\nloose\n|===\n", []string{"<td>loose</td>"}, nil},
		{"one-letter cells", "|===\n|s|seconds\n|a|b\n|===\n", []string{"<td>s</td>\n<td>seconds</td>", "<td>a</td>\n<td>b</td>"}, nil},
	} {
		t.Run(c.name, func(t *testing.T) { adocHas(t, c.src, c.want, c.bad...) })
	}
	adocHas(t, "Before.\n\n.Sizes\n|===\n| a\n|===\n\nAfter.\n", []string{`<p class="block-title" data-lines="3-3">Sizes</p>`, `<table data-lines="4-7">`, `<p data-lines="8-8">After.</p>`})
}

func TestAsciiDocLines(t *testing.T) {
	src := "= Title\n\nPara one\nline two.\n\n[source,go]\n----\nfunc f() {}\n----\n\n* a\n** b\n* c\n\nTerm:: text\n\n|===\n| x\n|===\n\nifdef::nope[]\nHidden.\nendif::[]\n\nLast.\n\n\n"
	adocHas(t, src, []string{
		`<h1 data-lines="1-2">`,
		`<p data-lines="3-5">`,
		`<pre data-lines="6-10">`,
		`<li data-lines="11-12">a<ul>`, `<li data-lines="12-12">b</li>`, `<li data-lines="13-16">c<dl>`,
		`<dt data-lines="15-15">Term</dt>`, `<dd data-lines="15-16">text</dd>`,
		`<table data-lines="17-24">`,
		`<p data-lines="25-27">Last.</p>`,
	})
}

func TestAsciiDocDiagrams(t *testing.T) {
	adocHas(t, "[plantuml]\n....\nAlice -> Bob: <login>\n....\n\n[mermaid]\n----\ngraph TD; A-->B\n----\n\n[puml, target=x, format=svg]\n----\nA -> B\n----\n\n"+
		"[source,plantuml]\n----\n@startuml\n----\n\n[source,go]\n----\nfunc main() {}\n----\n",
		[]string{
			`<pre class="diagram plantuml" data-lines="1-5"><code>Alice -&gt; Bob: &lt;login&gt;</code></pre>`,
			`<pre class="diagram mermaid" data-lines="6-10"><code>graph TD; A--&gt;B</code></pre>`,
			`<pre class="diagram plantuml" data-lines="11-15"><code>A -&gt; B</code></pre>`,
			`<pre data-lines="16-20"><code><span class="hd">@startuml</span></code></pre>`,
			`<pre data-lines="21-24"><code><span class="hk">func</span> <span class="hf">main</span>() {}</code></pre>`,
		})
}

func TestAsciiDocIncludes(t *testing.T) {
	chapter := map[string]string{
		"docs/chapters/intro.adoc": "= Introduction\n\nThe intro.\n\ninclude::part.adoc[]\n\n== Deeper\n",
		"docs/chapters/part.adoc":  "A part.",
		"shared/note.adoc":         "NOTE: Shared.",
	}
	inc := adocIncluding(t, "= Guide\n\ninclude::chapters/intro.adoc[leveloffset=+1]\n\n== After\n\ninclude::/shared/note.adoc[]\n", chapter, []string{
		`<h2 data-lines="3-3">Introduction</h2>`, `<p data-lines="3-3">The intro.</p>`, `<p data-lines="3-3">A part.</p>`,
		`<h3 data-lines="3-4">Deeper</h3>`, `<h2 data-lines="5-6">After</h2>`, `class="admonition note" data-lines="7-7"`,
	})
	if want := []string{"docs/chapters/intro.adoc", "docs/chapters/part.adoc", "shared/note.adoc"}; !slices.Equal(inc, want) {
		t.Errorf("includes %v", inc)
	}

	code := "package x\n\n// tag::main[]\nfunc main() {\n\t// tag::body[]\n\trun()\n\t// end::body[]\n}\n// end::main[]\n\n// tag::skip[]\nfunc skip() {}\n// end::skip[]\n"
	files := map[string]string{"docs/example.go": code, "docs/attrs.adoc": ":product: kijk\n:beta:\n"}
	for _, c := range []struct{ attrs, want, bad string }{
		{"tag=main", "func main() {\n\trun()\n}", "package"},
		{"tags=body", "\trun()", "main"},
		{"tags=**;!skip", "package x\n\nfunc main() {\n\trun()\n}", "skip"},
		{"tags=!skip", "package x\n\nfunc main() {", "skip"},
		{"tags=*;!body", "func main() {\n}\nfunc skip() {}", "run"},
		{"lines=1;4..5", "package x\nfunc main() {\n\t// tag::body[]", "skip"},
		{"lines=12..-1", "func skip() {}\n// end::skip[]", "main"},
		{"tag=body,indent=0", "<code>run()</code>", "\trun"},
	} {
		t.Run(c.attrs, func(t *testing.T) {
			src := "[source,text]\n----\ninclude::example.go[" + c.attrs + "]\n----\n"
			adocIncluding(t, src, files, []string{"<code>" + strings.SplitN(strings.TrimPrefix(c.want, "<code>"), "\n", 2)[0]}, c.bad, "tag::main")
			out, _ := renderAsciiDoc([]byte(src), "docs/guide.adoc", func(p string) ([]byte, error) { return []byte(files[p]), nil })
			if !strings.Contains(html.UnescapeString(out), strings.TrimPrefix(c.want, "<code>")) {
				t.Errorf("want %q in\n%s", c.want, out)
			}
		})
	}
	adocIncluding(t, "include::attrs.adoc[]\n\nWelcome to {product}.\n\nifdef::beta[]\nBeta.\nendif::[]\n", files,
		[]string{`<p data-lines="3-5">Welcome to kijk.</p>`, "Beta."}, "attrs.adoc")
	adocHas(t, ":leveloffset: +1\n\n= Part\n\n:leveloffset: -1\n\n== Back\n", []string{"<h2", ">Part</h2>", ">Back</h2>"})

	loop := map[string]string{"docs/a.adoc": "A.\n\ninclude::b.adoc[]\n", "docs/b.adoc": "B.\n\ninclude::a.adoc[]\n"}
	adocIncluding(t, "include::a.adoc[]\n", loop, []string{"A.", "B.", `<a href="a.adoc">a.adoc</a>`})
	deep := map[string]string{}
	for i := range 12 {
		deep[fmt.Sprintf("docs/d%d.adoc", i)] = fmt.Sprintf("D%d.\n\ninclude::d%d.adoc[]\n", i, i+1)
	}
	if inc := adocIncluding(t, "include::d0.adoc[]\n", deep, []string{"D7.", `<a href="d8.adoc">`}, "D8."); len(inc) != 8 {
		t.Errorf("deep includes %v", inc)
	}
	big := strings.Repeat("abcdefghi\n", mdMaxBytes/20+1)
	adocIncluding(t, "include::big.txt[]\n\ninclude::big.txt[]\n", map[string]string{"docs/big.txt": big}, []string{`<a href="big.txt">`})
	if inc := adocIncluding(t, "include::missing.adoc[]\n", nil, []string{`<a href="missing.adoc">`}); !slices.Equal(inc, []string{"docs/missing.adoc"}) {
		t.Errorf("a missing include is not watched: %v", inc)
	}
	adocIncluding(t, "include::missing.adoc[]\n\ninclude::../../out.adoc[]\n\ninclude::https://e.com/x.adoc[]\n\ninclude::bin.adoc[]\n\ninclude::latin.adoc[]\n",
		map[string]string{"out.adoc": "Out.", "docs/bin.adoc": "a\x00b", "docs/latin.adoc": "caf\xe9"},
		[]string{`<a href="missing.adoc">`, `<a href="../../out.adoc">`, `<a href="https://e.com/x.adoc">`, `<a href="bin.adoc">`, `<a href="latin.adoc">`}, "Out.")
	adocIncluding(t, "include::{snippets}/curl.adoc[]\n", nil, []string{`<p class="include" data-lines="1-1">{snippets}/curl.adoc</p>`})
}

func TestAsciiDocRoute(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "docs", "Guide.ADOC"), "= Guide\n\nNOTE: Hi.\n")
	write(t, filepath.Join(dir, "big.adoc"), strings.Repeat("x", mdMaxBytes+1))
	get := imageServer(t, dir)
	w := get("/api/markdown", url.Values{"path": {"docs/Guide.ADOC"}})
	var res struct{ HTML string }
	json.Unmarshal(w.Body.Bytes(), &res)
	if w.Code != http.StatusOK || !strings.Contains(res.HTML, `<h1 data-lines="1-2">Guide</h1>`) || !strings.Contains(res.HTML, `class="admonition note"`) {
		t.Errorf("%d %q", w.Code, res.HTML)
	}
	if w := get("/api/markdown", url.Values{"path": {"big.adoc"}}); w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("too large: %d", w.Code)
	}

	outside := filepath.Join(t.TempDir(), "secret.adoc")
	write(t, outside, "Secret.")
	if err := os.Symlink(outside, filepath.Join(dir, "docs", "link.adoc")); err != nil {
		t.Skip(err)
	}
	write(t, filepath.Join(dir, "docs", "chapters", "intro.adoc"), "The intro.")
	write(t, filepath.Join(dir, "docs", "book.adoc"), "include::chapters/intro.adoc[]\n\ninclude::link.adoc[]\n\ninclude::chapters[]\n")
	w = get("/api/markdown", url.Values{"path": {"docs/book.adoc"}})
	var book struct {
		HTML     string
		Includes []string
	}
	json.Unmarshal(w.Body.Bytes(), &book)
	if !strings.Contains(book.HTML, "The intro.") || strings.Contains(book.HTML, "Secret.") || !strings.Contains(book.HTML, `<a href="link.adoc">`) ||
		!strings.Contains(book.HTML, `<a href="chapters">`) || !slices.Equal(book.Includes, []string{"docs/chapters/intro.adoc", "docs/link.adoc", "docs/chapters"}) {
		t.Errorf("%d %+v", w.Code, book)
	}
}

// Quadratic work on a 2 MB file would take minutes.
func TestAsciiDocSpeed(t *testing.T) {
	if testing.Short() {
		t.Skip("slow")
	}
	section := "== Section {n}\n\nA *bold* _word_ and `code`, https://example.com[a link] and <<_section_1>>.\n\n" +
		"* item\n** nested\n\n[source,go]\n----\nfunc main() {}\n----\n\n|===\n| a | b\n|===\n\nTerm:: text\n\n"
	var b strings.Builder
	for b.Len() < mdMaxBytes/2 {
		b.WriteString(section)
	}
	// One long paragraph of marks that never close.
	for b.Len() < mdMaxBytes {
		b.WriteString("*a _b `c +d <<e [[f link:g image:h https://i ")
	}
	start := time.Now()
	out, _ := renderAsciiDoc([]byte(b.String()), "", nil)
	if took := time.Since(start); took > 3*time.Second {
		t.Errorf("rendering 2 MB took %s", took)
	}
	if !strings.Contains(out, "<strong>bold</strong>") {
		t.Error("nothing rendered")
	}
}
