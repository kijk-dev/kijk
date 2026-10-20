package render

import (
	"bufio"
	"bytes"
	"fmt"
	"html"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// AsciiDoc is the Renderer of AsciiDoc: it renders the common parts in the shape of Markdown.
// Every piece of the files' text is escaped, so a file cannot add HTML. read may be nil.
func AsciiDoc(src []byte, path string, read func(path string) ([]byte, error)) (string, []string, error) {
	raw := adocSplit(src)
	p := &adocParser{
		r: &adocReader{
			files: []*adocFile{{raw: raw, path: path}},
			attrs: adocDefaults(),
			inc:   &adocIncludes{read: read, cache: map[string][]string{}, budget: MaxSourceBytes},
		},
		secs:   map[string]adocSection{},
		titles: map[string]adocSection{},
		idUsed: map[string]int{},
	}
	p.r.sub = p.sub
	nodes := p.blocks("")
	adocMark(nodes, len(raw), true)
	var out bytes.Buffer
	w := adocWriter{bufio.NewWriter(&out), p}
	for _, n := range nodes {
		w.node(n)
	}
	w.Flush()
	return out.String(), p.r.inc.files, nil
}

func adocSplit(src []byte) []string {
	raw := strings.Split(string(src), "\n")
	if len(raw) > 1 && raw[len(raw)-1] == "" {
		raw = raw[:len(raw)-1]
	}
	for i, l := range raw {
		raw[i] = strings.TrimRight(l, " \t\r")
	}
	return raw
}

// adocLine is a line the conditionals keep, with its 1-based line in the open file.
type adocLine struct {
	s string
	n int
}

// adocReader hands out the lines that conditionals keep, with included files in place. It
// evaluates ifdef, ifndef and include when it reaches them, against the attributes set so far.
type adocReader struct {
	files []*adocFile // the open file, then the files being included in it
	ahead []adocLine
	attrs map[string]string
	open  []bool // for each open conditional, whether its lines show
	inc   *adocIncludes
	sub   func(string) string
}

// adocFile is a file the reader is in. An included file's lines all carry at, the line of the
// include in the open file, so change bars and go to line stay on lines of that file.
type adocFile struct {
	raw    []string
	i      int
	path   string
	at     int
	offset *string // the leveloffset to put back at the end, when the include set one
}

type adocIncludes struct {
	read   func(path string) ([]byte, error)
	cache  map[string][]string
	budget int      // bytes of included text left
	files  []string // every file it tried to include, for live reload
}

const adocMaxIncludeDepth = 8

var adocDirectiveRx = regexp.MustCompile(`^(\\?)(ifdef|ifndef|ifeval|endif)::([^\[\s]*)\[(.*)\]$`)

func (r *adocReader) fill(k int) bool {
	for len(r.ahead) < k {
		f := r.files[len(r.files)-1]
		if f.i == len(f.raw) {
			if len(r.files) == 1 {
				break
			}
			r.files = r.files[:len(r.files)-1]
			if f.offset != nil {
				r.attrs["leveloffset"] = *f.offset
			}
			continue
		}
		s, n := f.raw[f.i], f.i+1
		f.i++
		if f.at > 0 {
			n = f.at
		}
		shown := len(r.open) == 0 || r.open[len(r.open)-1]
		if strings.HasPrefix(s, "if") || strings.HasPrefix(s, "endif") || strings.HasPrefix(s, `\if`) || strings.HasPrefix(s, `\endif`) {
			if m := adocDirectiveRx.FindStringSubmatch(s); m != nil {
				if m[1] != "" {
					s = s[1:]
				} else if r.directive(m[2], m[3], m[4], n, shown) {
					continue
				}
			}
		}
		if shown && strings.HasPrefix(s, "include::") && r.include(s, n) {
			continue
		}
		if shown {
			r.ahead = append(r.ahead, adocLine{s, n})
		}
	}
	return len(r.ahead) >= k
}

// include puts the file an include line names in its place, and reports whether it could. A file
// it cannot read leaves the line, which shows as a link.
func (r *adocReader) include(s string, n int) bool {
	m := adocInclRx.FindStringSubmatch(s)
	if m == nil || r.inc.read == nil || len(r.files) > adocMaxIncludeDepth {
		return false
	}
	target := adocUnhideSyntax(r.sub(m[1]))
	if strings.Contains(target, "{") || adocSchemeRx.MatchString(target) {
		return false
	}
	p := path.Join(path.Dir(r.files[len(r.files)-1].path), target)
	if strings.HasPrefix(target, "/") {
		p = path.Clean(target[1:])
	}
	if p == ".." || strings.HasPrefix(p, "../") || slices.ContainsFunc(r.files, func(f *adocFile) bool { return f.path == p }) {
		return false
	}
	raw, ok := r.inc.cache[p]
	if !ok {
		if !slices.Contains(r.inc.files, p) { // also when missing, so the view redraws once it exists
			r.inc.files = append(r.inc.files, p)
		}
		src, err := r.inc.read(p)
		if err != nil || isBinary(src) || !utf8.Valid(src) {
			return false
		}
		raw = adocSplit(src)
		r.inc.cache[p] = raw
	}
	_, named := adocAttrList(r.sub(m[2]))
	switch {
	case named["lines"] != "":
		raw = adocPickLines(raw, named["lines"])
	case named["tags"] != "" || named["tag"] != "":
		raw = adocPickTags(raw, named["tags"]+";"+named["tag"])
	}
	if in, err := strconv.Atoi(named["indent"]); err == nil && in >= 0 {
		raw = adocDedent(raw)
		for i, l := range raw {
			if l != "" {
				raw[i] = strings.Repeat(" ", in) + l
			}
		}
	}
	size := len(raw)
	for _, l := range raw {
		size += len(l)
	}
	if r.inc.budget -= size; r.inc.budget < 0 {
		return false
	}
	f := &adocFile{raw: raw, path: p, at: n}
	if v, ok := named["leveloffset"]; ok {
		old := r.attrs["leveloffset"]
		f.offset = &old
		r.attrs["leveloffset"] = adocOffset(old, v)
	}
	r.files = append(r.files, f)
	return true
}

// adocOffset is leveloffset after the value v: +n and -n move the current one, n sets it.
func adocOffset(cur, v string) string {
	n, err := strconv.Atoi(strings.TrimPrefix(v, "+"))
	if err != nil {
		return cur
	}
	if v[0] == '+' || v[0] == '-' {
		c, _ := strconv.Atoi(cur)
		n += c
	}
	return strconv.Itoa(n)
}

// adocPickLines keeps the lines of ranges such as 1..5;10..-1, in file order.
func adocPickLines(raw []string, ranges string) []string {
	keep := make([]bool, len(raw))
	for _, rg := range strings.FieldsFunc(ranges, func(r rune) bool { return r == ';' || r == ',' }) {
		from, to, isRange := strings.Cut(strings.TrimSpace(rg), "..")
		a, err := strconv.Atoi(from)
		if err != nil || a < 1 {
			continue
		}
		b := a
		if isRange {
			if b, err = strconv.Atoi(to); err != nil || b < 0 {
				b = len(raw)
			}
		}
		for i := a; i <= min(b, len(raw)); i++ {
			keep[i-1] = true
		}
	}
	var out []string
	for i, l := range raw {
		if keep[i] {
			out = append(out, l)
		}
	}
	return out
}

var adocTagLineRx = regexp.MustCompile(`\b(tag|end)::(\S+?)\[\](?:$|[ \r])`)

// adocPickTags keeps the lines inside the tags named in spec, such as x;y, !x, * or **, and drops
// the tag lines. The innermost tag a line is in that spec names decides it.
func adocPickTags(raw []string, spec string) []string {
	keep := map[string]bool{}
	tagged, untagged, onlyNot := false, false, true
	for _, t := range strings.FieldsFunc(spec, func(r rune) bool { return r == ';' || r == ',' }) {
		t = strings.TrimSpace(t)
		not := strings.HasPrefix(t, "!")
		name := strings.TrimPrefix(t, "!")
		onlyNot = onlyNot && not
		switch name {
		case "**":
			tagged, untagged = !not, !not
		case "*":
			tagged = !not
		default:
			keep[name] = !not
		}
	}
	if onlyNot { // only exclusions: everything else shows
		tagged, untagged = true, true
	}
	var out, open []string
	for _, l := range raw {
		if m := adocTagLineRx.FindStringSubmatch(l); m != nil {
			if m[1] == "tag" {
				open = append(open, m[2])
			} else if i := slices.Index(open, m[2]); i >= 0 {
				open = slices.Delete(open, i, i+1)
			}
			continue
		}
		show := untagged
		if len(open) > 0 {
			show = tagged
			for i := len(open) - 1; i >= 0; i-- {
				if k, ok := keep[open[i]]; ok {
					show = k
					break
				}
			}
		}
		if show {
			out = append(out, l)
		}
	}
	return out
}

// directive applies a conditional line and reports whether it was one.
func (r *adocReader) directive(kind, target, text string, n int, shown bool) bool {
	switch kind {
	case "endif":
		if len(r.open) > 0 {
			r.open = r.open[:len(r.open)-1]
		}
		return true
	case "ifeval": // not evaluated: its lines show
		if target != "" {
			return false
		}
		r.open = append(r.open, shown)
		return true
	}
	if target == "" {
		return false
	}
	var keep bool
	if strings.Contains(target, ",") {
		keep = slices.ContainsFunc(strings.Split(target, ","), r.set)
	} else {
		keep = !slices.ContainsFunc(strings.Split(target, "+"), func(a string) bool { return !r.set(a) })
	}
	if kind == "ifndef" {
		keep = !keep
	}
	if text != "" { // the one-line form
		if shown && keep {
			r.ahead = append(r.ahead, adocLine{text, n})
		}
		return true
	}
	r.open = append(r.open, shown && keep)
	return true
}

func (r *adocReader) set(name string) bool {
	_, ok := r.attrs[strings.ToLower(name)]
	return ok
}

func (r *adocReader) peek() (adocLine, bool) {
	if !r.fill(1) {
		return adocLine{}, false
	}
	return r.ahead[0], true
}

func (r *adocReader) next() (adocLine, bool) {
	l, ok := r.peek()
	if ok {
		r.ahead = r.ahead[1:]
	}
	return l, ok
}

// adocSyntaxChars are the built-in attributes for characters that mark up text. Until the text is
// escaped they stand in as Unicode noncharacters, so {asterisk} never starts bold.
var adocSyntaxChars = [][2]string{
	{"plus", "+"}, {"startsb", "["}, {"endsb", "]"}, {"vbar", "|"}, {"caret", "^"}, {"asterisk", "*"},
	{"tilde", "~"}, {"apostrophe", "'"}, {"backslash", `\`}, {"backtick", "`"}, {"lt", "<"}, {"gt", ">"},
	{"amp", "&"}, {"quot", `"`}, {"pp", "++"}, {"cpp", "C++"},
}

const adocSyntax0 = '\uFDD0'

func adocDefaults() map[string]string {
	a := map[string]string{"nbsp": "\u00a0", "sp": " ", "empty": "", "blank": "", "zwsp": "\u200b"}
	for i, m := range adocSyntaxChars {
		a[m[0]] = string(rune(adocSyntax0 + i))
	}
	return a
}

func adocEsc(s string) string { return html.EscapeString(adocUnhideSyntax(s)) }

func adocUnhideSyntax(s string) string {
	if !strings.ContainsFunc(s, adocIsHiddenSyntax) {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		if adocIsHiddenSyntax(r) {
			b.WriteString(adocSyntaxChars[r-adocSyntax0][1])
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func adocIsHiddenSyntax(r rune) bool {
	return r >= adocSyntax0 && int(r-adocSyntax0) < len(adocSyntaxChars)
}

// adocNode is a block of the output. Inline text is rendered last, when every section is known,
// so cross references can point forward.
type adocNode struct {
	tag, class  string
	first, last int // source lines; first is 0 for parts that are never marked
	mark        bool
	attrs       string // more attributes, finished
	html        string // finished HTML
	text        string // inline AsciiDoc, after attribute references
	ctx         adocCtx
	code        *adocCode
	kids        []*adocNode
}

type adocCode struct {
	lang    string
	lines   []string
	diagram bool
}

// adocCtx is what inline text needs from the attributes at the place it was written.
type adocCtx struct {
	imagesdir string
	hard      bool
}

type adocSection struct{ slug, title string }

type adocParser struct {
	r       *adocReader
	close   string                 // the delimiter that ends the open compound block
	secs    map[string]adocSection // by id
	titles  map[string]adocSection // by title, for <<Title>>
	idUsed  map[string]int         // the last number given to a repeated automatic id
	started bool                   // a block was read, so = Title is no longer the document title
}

func (p *adocParser) ctx() adocCtx {
	_, hard := p.r.attrs["hardbreaks-option"]
	return adocCtx{imagesdir: p.r.attrs["imagesdir"], hard: hard}
}

// adocMeta holds the attribute, anchor and title lines above a block.
type adocMeta struct {
	first int // first metadata line, 0 when there is none
	title string
	id    string
	pos   []string // positional attributes; pos[0] is the style
	named map[string]string
	opts  []string
}

func (m *adocMeta) at(n int) {
	if m.first == 0 {
		m.first = n
	}
}

func (m *adocMeta) style() string { return strings.ToLower(m.arg(0)) }

func (m *adocMeta) arg(i int) string {
	if i < len(m.pos) {
		return m.pos[i]
	}
	return ""
}

func (m *adocMeta) has(opt string) bool { return slices.Contains(m.opts, opt) }

// parse reads an attribute line's list, with the style#id.role%option shorthand in the first.
func (m *adocMeta) parse(s string) {
	pos, named := adocAttrList(s)
	if len(pos) > 0 {
		first := pos[0]
		if cut := strings.IndexAny(first, "#.%"); cut >= 0 {
			pos[0] = first[:cut]
			rest := first[cut:]
			for rest != "" {
				end := strings.IndexAny(rest[1:], "#.%") + 1
				if end == 0 {
					end = len(rest)
				}
				switch part := rest[:end]; part[0] {
				case '#':
					m.id = part[1:]
				case '%':
					m.opts = append(m.opts, part[1:])
				}
				rest = rest[end:]
			}
		}
		m.pos = pos
	}
	if m.named == nil {
		m.named = map[string]string{}
	}
	for k, v := range named {
		m.named[k] = v
		switch k {
		case "id":
			m.id = v
		case "options", "opts":
			for _, o := range strings.Split(v, ",") {
				m.opts = append(m.opts, strings.TrimSpace(o))
			}
		}
	}
}

var adocNamedRx = regexp.MustCompile(`^(\w[\w-]*)[ \t]*=[ \t]*(.*)$`)

// adocAttrList splits name=value and positional attributes at commas outside quotes.
func adocAttrList(s string) (pos []string, named map[string]string) {
	named = map[string]string{}
	unquote := func(v string) string {
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			return v[1 : len(v)-1]
		}
		return v
	}
	var quote byte
	start := 0
	for i := 0; i <= len(s); i++ {
		if i < len(s) {
			c := s[i]
			if quote != 0 {
				if c == quote {
					quote = 0
				}
				continue
			}
			if c == '"' || c == '\'' {
				if strings.TrimSpace(s[start:i]) == "" || strings.HasSuffix(strings.TrimSpace(s[start:i]), "=") {
					quote = c
				}
				continue
			}
			if c != ',' {
				continue
			}
		}
		part := strings.TrimSpace(s[start:i])
		start = i + 1
		if m := adocNamedRx.FindStringSubmatch(part); m != nil {
			named[strings.ToLower(m[1])] = unquote(strings.TrimSpace(m[2]))
		} else {
			pos = append(pos, unquote(part))
		}
	}
	if len(pos) == 1 && pos[0] == "" && len(named) == 0 {
		pos = nil
	}
	return pos, named
}

var (
	adocEntryRx  = regexp.MustCompile(`^:(!?)(\w[\w-]*)(!?):(?:[ \t]+(.*))?$`)
	adocTitleRx  = regexp.MustCompile(`^(={1,6}|#{1,6})[ \t]+(\S.*?)(?:[ \t]+[=#]+)?$`)
	adocItemRx   = regexp.MustCompile(`^[ \t]*(\*{1,5}|-|\.{1,5}|\d{1,9}\.)[ \t]+(\S.*)$`)
	adocTermRx   = regexp.MustCompile(`^[ \t]*(\S.*?)(:{2,4}|;;)(?:[ \t]+(.*))?$`)
	adocAnchorRx = regexp.MustCompile(`^\[\[([\p{L}_:][\p{L}\p{N}_:.\-]*)(?:,[ \t]*(.+))?\]\]`)
	adocAdmonRx  = regexp.MustCompile(`^(NOTE|TIP|IMPORTANT|WARNING|CAUTION):[ \t]+(.*)$`)
	adocImageRx  = regexp.MustCompile(`^image::([^\s\[][^\[]*)\[(.*)\]$`)
	adocInclRx   = regexp.MustCompile(`^include::([^\[]+)\[(.*)\]$`)
	adocBreakRx  = regexp.MustCompile(`^('{3,}|-{3}|\*{3}|- - -|\* \* \*)$`)
	adocTagRx    = regexp.MustCompile(`<[^>]*>`)
	adocBadIDRx  = regexp.MustCompile(`<[^>]+>|&(?:[a-z][a-z]+\d{0,2}|#\d\d\d{0,4}|#x[\da-f][\da-f][\da-f]{0,3});|[^ \p{L}\p{M}\p{Nd}\p{Pc}\-.]+`)
)

var adocAdmonitions = map[string]string{"note": "Note", "tip": "Tip", "important": "Important", "warning": "Warning", "caution": "Caution"}

func adocComment(s string) bool { return strings.HasPrefix(s, "//") && !strings.HasPrefix(s, "///") }

// adocAttrLine reports a block attribute line, such as [source,go] or [#id].
func adocAttrLine(s string) bool {
	if len(s) < 2 || s[0] != '[' || s[len(s)-1] != ']' || s[1] == '[' {
		return false
	}
	r, _ := utf8.DecodeRuneInString(s[1:])
	return r == ']' || adocWord(r) || strings.ContainsRune(`.#%{,"'`, r)
}

// adocDelim returns the kind of a block delimiter line: its character, 'o' for an open block,
// '`' for a fence, or 0.
func adocDelim(s string) byte {
	switch {
	case s == "--":
		return 'o'
	case strings.HasPrefix(s, "```"):
		if !strings.Contains(s[3:], "`") {
			return '`'
		}
	case len(s) >= 4 && s[0] == '|' && strings.Trim(s[1:], "=") == "":
		return '|'
	case len(s) >= 4 && strings.IndexByte("-.=*_+/", s[0]) >= 0 && strings.Count(s, s[:1]) == len(s):
		return s[0]
	}
	return 0
}

// blocks reads blocks up to close, the delimiter of the compound block they sit in, or the end.
func (p *adocParser) blocks(close string) []*adocNode {
	outer := p.close
	p.close = close
	defer func() { p.close = outer }()
	var out []*adocNode
	for {
		l, ok := p.r.peek()
		if !ok {
			return out
		}
		if close != "" && l.s == close {
			p.r.next()
			return out
		}
		out = append(out, p.block()...)
	}
}

// block reads one block with the attribute, anchor and title lines above it. It reads at least
// one line, and returns nothing for blank lines, comments and attribute entries.
func (p *adocParser) block() []*adocNode {
	var m adocMeta
	for {
		l, ok := p.r.peek()
		if !ok || (p.close != "" && l.s == p.close) {
			return nil
		}
		s := l.s
		switch {
		case s == "":
			p.r.next()
			if m.first == 0 {
				return nil
			}
			continue
		case adocComment(s):
			p.r.next()
			continue
		case adocDelim(s) == '/':
			p.r.next()
			p.verbatim(s)
			continue
		case s[0] == ':' && p.entry(l):
			continue
		case s[0] == '[':
			if a := adocAnchorRx.FindStringSubmatch(s); a != nil && len(a[0]) == len(s) {
				m.id = a[1]
				m.at(l.n)
				p.r.next()
				continue
			}
			if adocAttrLine(s) {
				m.parse(s[1 : len(s)-1])
				m.at(l.n)
				p.r.next()
				continue
			}
		case s[0] == '.' && len(s) > 1 && s[1] != '.' && s[1] != ' ' && s[1] != '\t':
			m.title = s[1:]
			m.at(l.n)
			p.r.next()
			continue
		}
		out := p.body(&m, l)
		p.started = true
		return out
	}
}

// entry applies an attribute entry line, :name: value or :name!:, and reports whether it was one.
func (p *adocParser) entry(l adocLine) bool {
	m := adocEntryRx.FindStringSubmatch(l.s)
	if m == nil {
		return false
	}
	p.r.next()
	name := strings.ToLower(m[2])
	if m[1] != "" || m[3] != "" {
		delete(p.r.attrs, name)
		return true
	}
	v := m[4]
	for v == `\` || strings.HasSuffix(v, ` \`) { // a value that goes on on the next line
		nl, ok := p.r.next()
		v = strings.TrimRight(strings.TrimSuffix(v, `\`), " ")
		if !ok {
			break
		}
		v += " " + strings.TrimSpace(nl.s)
	}
	if v = p.sub(v); name == "leveloffset" {
		v = adocOffset(p.r.attrs[name], v)
	}
	p.r.attrs[name] = v
	return true
}

func (p *adocParser) body(m *adocMeta, l adocLine) []*adocNode {
	first := l.n
	if m.first != 0 {
		first = m.first
	}
	s := l.s
	if d := adocDelim(s); d != 0 {
		p.r.next()
		return p.delimited(m, l, d, first)
	}
	if s[0] == '=' || s[0] == '#' {
		if t := adocTitleRx.FindStringSubmatch(s); t != nil {
			p.r.next()
			off, _ := strconv.Atoi(p.r.attrs["leveloffset"])
			return p.section(m, min(max(len(t[1])+off, 1), 6), t[2], first)
		}
	}
	switch {
	case adocBreakRx.MatchString(s):
		p.r.next()
		return p.titled(m, first, &adocNode{tag: "hr"}, l.n)
	case s == "<<<" || s == "toc::[]":
		p.r.next()
		return nil
	case strings.HasPrefix(s, "image::"):
		if im := adocImageRx.FindStringSubmatch(s); im != nil {
			p.r.next()
			img := adocImage(p.sub(strings.TrimSpace(im[1])), p.sub(im[2]), p.r.attrs["imagesdir"], false)
			return p.titled(m, first, &adocNode{tag: "p", html: img}, l.n)
		}
	case strings.HasPrefix(s, "include::"):
		if in := adocInclRx.FindStringSubmatch(s); in != nil {
			p.r.next()
			target := p.sub(in[1])
			h := adocEsc(target)
			if adocSafeURL(target) && !strings.Contains(target, "{") { // an unknown {name} links nowhere
				h = `<a href="` + h + `">` + h + `</a>`
			}
			return p.titled(m, first, &adocNode{tag: "p", class: "include", html: h}, l.n)
		}
	}
	if it, ok := adocListItem(s); ok {
		p.r.next()
		it.n = l.n
		list := p.list(it, nil)
		if n, err := strconv.Atoi(m.named["start"]); err == nil && list.tag == "ol" {
			list.attrs = fmt.Sprintf(` start="%d"`, n)
		}
		return p.titled(m, first, list, l.n)
	}
	if s[0] == ' ' || s[0] == '\t' { // an indented paragraph is literal
		lines := p.until(true)
		if adocCodeStyle(m.style()) {
			return p.titled(m, first, p.code(m, adocDedent(lines), ""), l.n)
		}
		return p.titled(m, first, adocPre(adocDedent(lines)), l.n)
	}
	style := m.style()
	label := ""
	if a := adocAdmonRx.FindStringSubmatch(s); a != nil && style == "" {
		label = adocAdmonitions[strings.ToLower(a[1])]
	}
	lines := p.until(false)
	if label != "" {
		lines[0] = strings.TrimLeft(lines[0][len(label)+1:], " \t")
	}
	switch {
	case adocCodeStyle(style):
		return p.titled(m, first, p.code(m, lines, ""), l.n)
	case style == "pass" || style == "stem" || style == "latexmath" || style == "asciimath":
		return p.titled(m, first, adocPre(lines), l.n)
	}
	ctx := p.ctx()
	ctx.hard = ctx.hard || m.has("hardbreaks") || style == "verse"
	para := &adocNode{tag: "p", text: p.sub(strings.Join(lines, "\n")), ctx: ctx}
	if adm, ok := adocAdmonitions[style]; ok {
		label = adm
	}
	switch {
	case label != "":
		return []*adocNode{p.admonition(m, first, label, []*adocNode{para})}
	case style == "quote" || style == "verse":
		return []*adocNode{p.quote(m, first, []*adocNode{para})}
	}
	return p.titled(m, first, para, l.n)
}

func isDiagram(style string) bool {
	return FenceType(style) != nil
}

func adocCodeStyle(style string) bool {
	return style == "source" || style == "listing" || style == "literal" || isDiagram(style)
}

func adocPre(lines []string) *adocNode { return &adocNode{tag: "pre", code: &adocCode{lines: lines}} }

func (p *adocParser) blockTitle(m *adocMeta, first int) *adocNode {
	return &adocNode{tag: "p", class: "block-title", first: first, text: p.sub(m.title), ctx: p.ctx()}
}

// until reads the lines of a paragraph: up to a blank line, a block attribute line, a delimiter
// or an include. A literal paragraph ends only at a blank line.
func (p *adocParser) until(literal bool) []string {
	var lines []string
	for {
		l, ok := p.r.peek()
		if !ok || l.s == "" || (p.close != "" && l.s == p.close) {
			return lines
		}
		if len(lines) > 0 && !literal && (adocAttrLine(l.s) || adocDelim(l.s) != 0 || strings.HasPrefix(l.s, "include::")) {
			return lines
		}
		p.r.next()
		if !literal && adocComment(l.s) {
			continue
		}
		lines = append(lines, l.s)
	}
}

// titled puts a block title above a block. The title owns the metadata lines.
func (p *adocParser) titled(m *adocMeta, first int, b *adocNode, own int) []*adocNode {
	if m.title == "" {
		b.first = first
		return []*adocNode{b}
	}
	b.first = own
	return []*adocNode{p.blockTitle(m, first), b}
}

// inner is the block title of a box block, such as an admonition, which shows inside it.
func (p *adocParser) inner(m *adocMeta) []*adocNode {
	if m.title == "" {
		return nil
	}
	return []*adocNode{p.blockTitle(m, 0)}
}

func (p *adocParser) admonition(m *adocMeta, first int, label string, kids []*adocNode) *adocNode {
	head := []*adocNode{{tag: "p", class: "admonition-label", html: label}}
	return &adocNode{tag: "blockquote", class: "admonition " + strings.ToLower(label), first: first,
		kids: slices.Concat(head, p.inner(m), kids)}
}

func (p *adocParser) quote(m *adocMeta, first int, kids []*adocNode) *adocNode {
	q := &adocNode{tag: "blockquote", first: first, kids: slices.Concat(p.inner(m), kids)}
	if by := m.arg(1); by != "" {
		if cite := m.arg(2); cite != "" {
			by += ", " + cite
		}
		q.kids = append(q.kids, &adocNode{tag: "p", class: "quote-by", text: "— " + p.sub(by), ctx: p.ctx()})
	}
	return q
}

// code makes a listing: a diagram for a diagram style, colored for a source language, else plain.
func (p *adocParser) code(m *adocMeta, lines []string, fence string) *adocNode {
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if strings.Contains(m.named["subs"], "attributes") {
		for i, l := range lines {
			lines[i] = adocUnhideSyntax(p.sub(l))
		}
	}
	style := m.style()
	if isDiagram(style) {
		return &adocNode{tag: "pre", code: &adocCode{lang: style, lines: lines, diagram: true}}
	}
	lang := fence
	if style == "source" || (style == "" && m.arg(1) != "") {
		if lang = m.arg(1); lang == "" {
			lang = p.r.attrs["source-language"]
		}
	}
	return &adocNode{tag: "pre", code: &adocCode{lang: strings.TrimSpace(lang), lines: lines}}
}

// verbatim reads lines as they are, up to the line close.
func (p *adocParser) verbatim(close string) []string {
	var lines []string
	for {
		l, ok := p.r.next()
		if !ok || l.s == close {
			return lines
		}
		lines = append(lines, l.s)
	}
}

func adocDedent(lines []string) []string {
	indent := -1
	for _, l := range lines {
		if t := strings.TrimLeft(l, " \t"); t != "" && (indent < 0 || len(l)-len(t) < indent) {
			indent = len(l) - len(t)
		}
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = l[min(max(indent, 0), len(l)-len(strings.TrimLeft(l, " \t"))):]
	}
	return out
}

func (p *adocParser) delimited(m *adocMeta, l adocLine, d byte, first int) []*adocNode {
	close := l.s
	style := m.style()
	switch d {
	case '`':
		return p.titled(m, first, p.code(m, p.verbatim("```"), l.s[3:]), l.n)
	case '-', '.':
		lines := p.verbatim(close)
		if d == '-' || style == "source" || isDiagram(style) {
			return p.titled(m, first, p.code(m, lines, ""), l.n)
		}
		return p.titled(m, first, adocPre(lines), l.n)
	case '+': // passthrough shows as text
		return p.titled(m, first, adocPre(p.verbatim(close)), l.n)
	case '|':
		return p.titled(m, first, p.table(m, close), l.n)
	}
	if style == "verse" {
		ctx := p.ctx()
		ctx.hard = true
		para := &adocNode{tag: "p", text: p.sub(strings.Join(p.verbatim(close), "\n")), ctx: ctx}
		return []*adocNode{p.quote(m, first, []*adocNode{para})}
	}
	if d == 'o' && adocCodeStyle(style) {
		return p.titled(m, first, p.code(m, p.verbatim(close), ""), l.n)
	}
	kids := p.blocks(close)
	if label, ok := adocAdmonitions[style]; ok && (d == '=' || d == 'o') {
		return []*adocNode{p.admonition(m, first, label, kids)}
	}
	switch {
	case d == '_' || style == "quote":
		return []*adocNode{p.quote(m, first, kids)}
	case d == '=':
		return []*adocNode{{tag: "blockquote", class: "example", first: first, kids: slices.Concat(p.inner(m), kids)}}
	case d == '*':
		return []*adocNode{{tag: "blockquote", class: "sidebar", first: first, kids: slices.Concat(p.inner(m), kids)}}
	}
	if m.title != "" { // an open block adds nothing but its title
		kids = append([]*adocNode{p.blockTitle(m, first)}, kids...)
	}
	return kids
}

func (p *adocParser) section(m *adocMeta, level int, raw string, first int) []*adocNode {
	id := m.id
	if a := adocAnchorRx.FindStringSubmatch(raw); a != nil {
		if id == "" {
			id = a[1]
		}
		raw = strings.TrimLeft(raw[len(a[0]):], " \t")
	}
	h := adocInline{adocCtx: adocCtx{imagesdir: p.r.attrs["imagesdir"]}}.render(p.sub(raw))
	plain := html.UnescapeString(adocTagRx.ReplaceAllString(h, ""))
	if id == "" {
		id = p.autoID(h)
	}
	sec := adocSection{slug: jsSlug(plain), title: plain}
	if _, ok := p.secs[id]; !ok {
		p.secs[id] = sec
	}
	if _, ok := p.titles[plain]; !ok {
		p.titles[plain] = sec
	}
	out := []*adocNode{{tag: "h" + strconv.Itoa(level), first: first, html: h}}
	if level != 1 || p.started {
		return out
	}
	// The document header: an author line and a revision line, between attribute entries.
	var lines []adocLine
	for len(lines) < 2 {
		l, ok := p.r.peek()
		if !ok || l.s == "" {
			break
		}
		if adocComment(l.s) {
			p.r.next()
			continue
		}
		if l.s[0] == ':' && p.entry(l) {
			continue
		}
		p.r.next()
		lines = append(lines, l)
	}
	if len(lines) > 0 {
		parts := make([]string, len(lines))
		for i, l := range lines {
			parts[i] = adocInline{}.render(p.sub(l.s))
		}
		out = append(out, &adocNode{tag: "p", class: "doc-header", first: lines[0].n, html: strings.Join(parts, "<br>")})
	}
	return out
}

// autoID is Asciidoctor's id for a section, from its title's HTML: idprefix, then the title in
// lower case with idseparator between words.
func (p *adocParser) autoID(title string) string {
	pre, sep := "_", "_"
	if v, ok := p.r.attrs["idprefix"]; ok {
		pre = v
	}
	if v, ok := p.r.attrs["idseparator"]; ok {
		sep = v
		if r, size := utf8.DecodeRuneInString(v); size > 0 {
			sep = string(r)
		}
	}
	id := pre + adocBadIDRx.ReplaceAllString(strings.ToLower(title), "")
	if sep == "" {
		id = strings.ReplaceAll(id, " ", "")
	} else {
		set := " " + sep + ".-"
		var b strings.Builder
		run := false
		for _, r := range id {
			if strings.ContainsRune(set, r) {
				if !run {
					b.WriteString(sep)
				}
				run = true
				continue
			}
			b.WriteRune(r)
			run = false
		}
		id = strings.TrimSuffix(b.String(), sep)
		if pre == "" {
			id = strings.TrimPrefix(id, sep)
		}
	}
	if _, taken := p.secs[id]; !taken {
		return id
	}
	n := max(p.idUsed[id], 1)
	for {
		n++
		if _, taken := p.secs[id+sep+strconv.Itoa(n)]; !taken {
			p.idUsed[id] = n
			return id + sep + strconv.Itoa(n)
		}
	}
}

// adocItem is a list item line. For a description list, text is the term and desc the text
// after it.
type adocItem struct {
	marker, text, desc string
	term               bool
	n                  int
}

func adocListItem(s string) (adocItem, bool) {
	t := strings.TrimLeft(s, " \t")
	if t == "" {
		return adocItem{}, false
	}
	if strings.IndexByte("*-.0123456789", t[0]) >= 0 {
		if m := adocItemRx.FindStringSubmatch(s); m != nil {
			marker := m[1]
			if marker[0] >= '0' && marker[0] <= '9' {
				marker = "1."
			}
			return adocItem{marker: marker, text: m[2]}, true
		}
	}
	if (strings.Contains(s, "::") || strings.Contains(s, ";;")) && !adocComment(t) {
		if m := adocTermRx.FindStringSubmatch(s); m != nil {
			return adocItem{marker: m[2], text: m[1], desc: m[3], term: true}, true
		}
	}
	return adocItem{}, false
}

func adocListTag(marker string) string {
	switch marker[0] {
	case '*', '-':
		return "ul"
	case '.', '1':
		return "ol"
	}
	return "dl"
}

// list reads a list from its first item, whose line is read. A different marker nests a list in
// the item before it; a marker of an outer list ends this one.
func (p *adocParser) list(it adocItem, outer []string) *adocNode {
	l := &adocNode{tag: adocListTag(it.marker), first: it.n}
	stack := append(outer[:len(outer):len(outer)], it.marker)
	for {
		box := p.item(l, it)
		blank := false
	scan:
		for {
			nl, ok := p.r.peek()
			if !ok || (p.close != "" && nl.s == p.close) {
				return l
			}
			switch {
			case nl.s == "":
				blank = true
				p.r.next()
				continue
			case adocComment(nl.s):
				if blank { // a comment between blank lines ends a list
					return l
				}
				p.r.next()
				continue
			case nl.s == "+":
				p.r.next()
				p.attach(box, p.block())
				blank = false
				continue
			}
			ni, ok := adocListItem(nl.s)
			if !ok {
				if blank && (nl.s[0] == ' ' || nl.s[0] == '\t') {
					p.attach(box, []*adocNode{{tag: "pre", first: nl.n, code: &adocCode{lines: adocDedent(p.until(true))}}})
					blank = false
					continue
				}
				return l
			}
			ni.n = nl.n
			switch {
			case ni.marker == it.marker:
				p.r.next()
				it = ni
				break scan
			case slices.Contains(stack, ni.marker):
				return l
			}
			p.r.next()
			box.kids = append(box.kids, p.list(ni, stack))
			blank = false
		}
	}
}

// item adds a list item, or a term and its description, and returns the part that takes the
// blocks attached to it.
func (p *adocParser) item(l *adocNode, it adocItem) *adocNode {
	if !it.term {
		li := &adocNode{tag: "li", first: it.n, text: p.sub(p.itemText([]string{it.text})), ctx: p.ctx()}
		l.kids = append(l.kids, li)
		return li
	}
	dt := &adocNode{tag: "dt", first: it.n, text: p.sub(it.text), ctx: p.ctx()}
	dd := &adocNode{tag: "dd", first: it.n, ctx: p.ctx()}
	var start []string
	if it.desc != "" {
		start = []string{it.desc}
	} else if nl, ok := p.r.peek(); ok {
		dd.first = nl.n
	}
	if dd.text = p.sub(p.itemText(start)); dd.text == "" {
		dd.first = it.n
	}
	l.kids = append(l.kids, dt, dd)
	return dd
}

// itemText reads the lines that go on a list item's text.
func (p *adocParser) itemText(lines []string) string {
	for {
		l, ok := p.r.peek()
		if !ok || l.s == "" || l.s == "+" || (p.close != "" && l.s == p.close) {
			break
		}
		if adocComment(l.s) {
			p.r.next()
			continue
		}
		if _, item := adocListItem(l.s); item || adocDelim(l.s) != 0 || adocAttrLine(l.s) || strings.HasPrefix(l.s, "include::") {
			break
		}
		p.r.next()
		lines = append(lines, strings.TrimLeft(l.s, " \t"))
	}
	return strings.Join(lines, "\n")
}

// attach adds blocks to a list item. Its text moves into a paragraph above them.
func (p *adocParser) attach(box *adocNode, blocks []*adocNode) {
	if len(blocks) == 0 {
		return
	}
	if box.text != "" {
		box.kids = append([]*adocNode{{tag: "p", text: box.text, ctx: box.ctx}}, box.kids...)
		box.text = ""
	}
	box.kids = append(box.kids, blocks...)
}

// table reads a |=== table. Each | starts a cell; the column count comes from cols or from the
// first line.
func (p *adocParser) table(m *adocMeta, close string) *adocNode {
	var lines []string
	for {
		l, ok := p.r.next()
		if !ok || l.s == close {
			break
		}
		lines = append(lines, l.s)
	}
	var cells []string
	firstCells := -1
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		pre, row := adocCells(l)
		if pre = strings.TrimSpace(pre); pre != "" && len(cells) > 0 {
			cells[len(cells)-1] += "\n" + pre
		} else if pre != "" {
			cells = append(cells, pre)
		}
		cells = append(cells, row...)
		if firstCells < 0 {
			firstCells = len(row)
		}
	}
	cols := adocCols(m.named["cols"])
	if cols == 0 {
		cols = max(firstCells, 1)
	}
	header := m.has("header") || (!m.has("noheader") && len(lines) > 1 && strings.TrimSpace(lines[0]) != "" &&
		strings.TrimSpace(lines[1]) == "" && firstCells == cols)
	t := &adocNode{tag: "table"}
	body := &adocNode{tag: "tbody"}
	ctx := p.ctx()
	for i := 0; i < len(cells); i += cols {
		tr := &adocNode{tag: "tr"}
		tag := "td"
		if header && i == 0 {
			tag = "th"
		}
		for j := range cols {
			text := ""
			if i+j < len(cells) {
				text = p.sub(strings.TrimSpace(cells[i+j]))
			}
			tr.kids = append(tr.kids, &adocNode{tag: tag, text: text, ctx: ctx})
		}
		if tag == "th" {
			t.kids = append(t.kids, &adocNode{tag: "thead", kids: []*adocNode{tr}})
		} else {
			body.kids = append(body.kids, tr)
		}
	}
	if len(body.kids) > 0 {
		t.kids = append(t.kids, body)
	}
	return t
}

var adocSpecRx = regexp.MustCompile(`^(\d+\*|\d*(\.\d+)?\+)?[<^>]?(\.[<^>])?[adehlmsv]?$`)

// adocCells splits a table line at each | that starts a cell. pre is the text before the first.
// Cell specs, such as 2+ or a, are dropped: they show their cell as plain text. A spec starts the
// line or follows a space, so the cell in |s|seconds keeps its s.
func adocCells(s string) (pre string, cells []string) {
	var b strings.Builder
	cur := -1
	end := func() string {
		seg := b.String()
		b.Reset()
		cut := strings.LastIndexAny(seg, " \t") + 1
		if spec := seg[cut:]; spec != "" && (cut > 0 || cur < 0) && adocSpecRx.MatchString(spec) {
			seg = seg[:cut]
		}
		return seg
	}
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '\\' && i+1 < len(s) && s[i+1] == '|':
			b.WriteByte('|')
			i++
		case s[i] == '|':
			if seg := end(); cur < 0 {
				pre = seg
			} else {
				cells[cur] = seg
			}
			cells = append(cells, "")
			cur = len(cells) - 1
		default:
			b.WriteByte(s[i])
		}
	}
	if cur < 0 {
		return b.String(), nil
	}
	cells[cur] = b.String()
	return pre, cells
}

// adocCols counts the columns of a cols attribute, such as "1,2,1" or "3*".
func adocCols(cols string) int {
	if cols == "" {
		return 0
	}
	n := 0
	for _, c := range strings.FieldsFunc(cols, func(r rune) bool { return r == ',' || r == ';' }) {
		if k, _, ok := strings.Cut(strings.TrimSpace(c), "*"); ok {
			if v, err := strconv.Atoi(k); err == nil && v > 0 && v < 1000 {
				n += v
				continue
			}
		}
		n++
	}
	return n
}

// adocMark gives blocks their lines with lineRanges. Top-level blocks are marked, and in lists
// the items, terms and descriptions.
func adocMark(kids []*adocNode, end int, top bool) {
	var marked []*adocNode
	var firsts []int
	for _, k := range kids {
		if k.first > 0 {
			marked = append(marked, k)
			firsts = append(firsts, k.first)
		}
	}
	for i, last := range lineRanges(firsts, end) {
		k := marked[i]
		k.last = last
		list := k.tag == "ul" || k.tag == "ol" || k.tag == "dl"
		item := k.tag == "li" || k.tag == "dt" || k.tag == "dd"
		k.mark = (top && !list) || item
		if list || item {
			adocMark(k.kids, last, false)
		}
	}
}

type adocWriter struct {
	*bufio.Writer
	p *adocParser
}

func (w adocWriter) node(n *adocNode) {
	if n.tag == "dd" && n.text == "" && len(n.kids) == 0 {
		return
	}
	attrs := func() {
		if n.class != "" {
			fmt.Fprintf(w, ` class="%s"`, n.class)
		}
		if n.mark {
			fmt.Fprintf(w, ` data-lines="%d-%d"`, n.first, n.last)
		}
		w.WriteString(n.attrs)
	}
	if c := n.code; c != nil {
		if c.diagram {
			writeDiagram(w.Writer, FenceType(c.lang).Name, attrs, []byte(strings.Join(c.lines, "\n")))
		} else {
			writeColored(w.Writer, c.lang, c.lines, attrs)
		}
		return
	}
	w.WriteString("<" + n.tag)
	attrs()
	w.WriteString(">")
	if n.tag == "hr" {
		w.WriteString("\n")
		return
	}
	w.WriteString(n.html)
	if n.text != "" {
		w.WriteString(adocInline{adocCtx: n.ctx, p: w.p}.render(n.text))
	}
	if len(n.kids) > 0 && n.tag != "li" && n.tag != "dd" {
		w.WriteString("\n")
	}
	for _, k := range n.kids {
		w.node(k)
	}
	w.WriteString("</" + n.tag + ">\n")
}

// adocInline renders inline text: marks, links, cross references, images and passthroughs.
type adocInline struct {
	adocCtx
	p      *adocParser // for cross references; nil while parsing, when they show as text
	depth  int
	inLink bool
}

const adocMaxDepth = 8

func (c adocInline) render(s string) string {
	if c.depth > adocMaxDepth {
		return adocEsc(s)
	}
	var b strings.Builder
	sc := &adocScan{s: s}
	text := 0
	for i := 0; i < len(s); {
		n, out := c.at(sc, i)
		if n == 0 {
			i++
			continue
		}
		b.WriteString(adocEsc(s[text:i]))
		b.WriteString(out)
		i += n
		text = i
	}
	b.WriteString(adocEsc(s[text:]))
	return b.String()
}

func (c adocInline) deeper() adocInline {
	c.depth++
	return c
}

// at renders the inline element that starts at i, and returns its length, or 0 when none does.
func (c adocInline) at(sc *adocScan, i int) (int, string) {
	s := sc.s
	switch s[i] {
	case '\\':
		if i+1 < len(s) && (strings.IndexByte("*_`+<[", s[i+1]) >= 0 || adocMacroAt(s, i+1)) {
			return 2, adocEsc(s[i+1 : i+2])
		}
	case '\n':
		if c.hard {
			return 1, "<br>\n"
		}
	case '+':
		if i > 0 && s[i-1] == ' ' && (i+1 == len(s) || s[i+1] == '\n') {
			return 1, "<br>"
		}
		if n, inner := adocPass(sc, i); n > 0 {
			return n, adocEsc(inner)
		}
	case '*':
		return c.quoted(sc, i, "strong", adocFindStrong, adocFindStrong2)
	case '_':
		return c.quoted(sc, i, "em", adocFindEm, adocFindEm2)
	case '`':
		return c.quoted(sc, i, "code", adocFindMono, adocFindMono2)
	case '<':
		if strings.HasPrefix(s[i:], "<<") {
			return c.xrefAngle(sc, i)
		}
		if adocURLAt(s, i+1) > 0 && !c.inLink {
			end := sc.find(adocFindTargetEnd, i+1, func(j int) bool { return adocTargetStop(s[j]) })
			if end > 0 && s[end] == '>' {
				return end + 1 - i, c.link(s[i+1:end], "")
			}
		}
	case '[':
		if strings.HasPrefix(s[i:], "[[") {
			if a := adocAnchorRx.FindStringIndex(s[i:min(len(s), i+256)]); a != nil {
				return a[1], ""
			}
		}
	case 'h':
		return c.url(sc, i)
	case 'l', 'm', 'x', 'i', 'p':
		return c.macro(sc, i)
	}
	return 0, ""
}

// adocScan finds closing marks in a text. Each finder remembers its last answer, so a scan that
// moves forward reads the text once per finder however many marks never close.
type adocScan struct {
	s     string
	cache [adocFinders]struct {
		set      bool
		from, at int
	}
}

const (
	adocFindStrong = iota
	adocFindStrong2
	adocFindEm
	adocFindEm2
	adocFindMono
	adocFindMono2
	adocFindPass
	adocFindPass2
	adocFindPass3
	adocFindBracket
	adocFindAngle
	adocFindTargetEnd
	adocFinders
)

// find returns the first j >= from where ok holds, or -1.
func (sc *adocScan) find(k, from int, ok func(j int) bool) int {
	c := &sc.cache[k]
	if c.set && c.from <= from && (c.at < 0 || c.at >= from) {
		return c.at
	}
	at := -1
	for j := from; j < len(sc.s); j++ {
		if ok(j) {
			at = j
			break
		}
	}
	c.set, c.from, c.at = true, from, at
	return at
}

func adocSpace(b byte) bool { return b == ' ' || b == '\t' || b == '\n' }

func adocWord(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.Is(unicode.M, r) || unicode.Is(unicode.Pc, r)
}

func adocWordAt(s string, j int) bool {
	if j >= len(s) {
		return false
	}
	r, _ := utf8.DecodeRuneInString(s[j:])
	return adocWord(r)
}

func adocWordBefore(s string, i int) bool {
	if i <= 0 {
		return false
	}
	r, _ := utf8.DecodeLastRuneInString(s[:i])
	return adocWord(r)
}

// adocOpens reports whether the mark at i can open a constrained span: no word before it, and
// no space after it.
func adocOpens(s string, i int) bool {
	return i+1 < len(s) && !adocSpace(s[i+1]) && !adocWordBefore(s, i) && (i == 0 || strings.IndexByte(";:}\\", s[i-1]) < 0)
}

// quoted renders **x** anywhere, or *x* between non-word characters, and the same for _ and `.
func (c adocInline) quoted(sc *adocScan, i int, tag string, one, two int) (int, string) {
	s, m := sc.s, sc.s[i]
	wrap := func(inner string) string { return "<" + tag + ">" + c.deeper().render(inner) + "</" + tag + ">" }
	if i+1 < len(s) && s[i+1] == m {
		if j := sc.find(two, i+3, func(j int) bool { return s[j] == m && j+1 < len(s) && s[j+1] == m }); j > 0 {
			return j + 2 - i, wrap(s[i+2 : j])
		}
	}
	if !adocOpens(s, i) {
		return 0, ""
	}
	j := sc.find(one, i+2, func(j int) bool { return s[j] == m && !adocSpace(s[j-1]) && !adocWordAt(s, j+1) })
	if j < 0 {
		return 0, ""
	}
	return j + 1 - i, wrap(s[i+1 : j])
}

// adocPass returns the length and text of a +++x+++, ++x++ or +x+ passthrough at i.
func adocPass(sc *adocScan, i int) (int, string) {
	s := sc.s
	if adocWordBefore(s, i) || (i > 0 && strings.IndexByte(";:\\", s[i-1]) >= 0) {
		return 0, ""
	}
	for _, f := range []struct {
		mark string
		k    int
	}{{"+++", adocFindPass3}, {"++", adocFindPass2}} {
		if strings.HasPrefix(s[i:], f.mark) {
			if j := sc.find(f.k, i+len(f.mark), func(j int) bool { return strings.HasPrefix(s[j:], f.mark) }); j >= 0 {
				return j + len(f.mark) - i, s[i+len(f.mark) : j]
			}
		}
	}
	if !adocOpens(s, i) {
		return 0, ""
	}
	j := sc.find(adocFindPass, i+2, func(j int) bool { return s[j] == '+' && !adocSpace(s[j-1]) && !adocWordAt(s, j+1) })
	if j < 0 {
		return 0, ""
	}
	return j + 1 - i, s[i+1 : j]
}

var adocMacros = []string{"https://", "http://", "mailto:", "link:", "xref:", "image:", "pass:", "footnote:", "kbd:", "btn:", "menu:"}

func adocMacroAt(s string, i int) bool {
	for _, m := range adocMacros {
		if strings.HasPrefix(s[i:], m) {
			return true
		}
	}
	return false
}

// adocURLAt returns the length of the scheme of a web link at i, or 0.
func adocURLAt(s string, i int) int {
	for _, scheme := range []string{"https://", "http://"} {
		if strings.HasPrefix(s[i:], scheme) {
			return len(scheme)
		}
	}
	return 0
}

func adocTargetStop(b byte) bool { return adocSpace(b) || strings.IndexByte("[]<>\"", b) >= 0 }

// bracket returns the ] that closes the [ at j, or -1. One level of brackets may nest, for an
// image inside a link's text.
func (c adocInline) bracket(sc *adocScan, j int) int {
	s := sc.s
	ok := func(k int) bool { return s[k] == ']' && s[k-1] != '\\' }
	k := sc.find(adocFindBracket, j+1, ok)
	if k > 0 && strings.Contains(s[j+1:k], "[") {
		if k2 := sc.find(adocFindBracket, k+1, ok); k2 > 0 {
			return k2
		}
	}
	return k
}

func (c adocInline) url(sc *adocScan, i int) (int, string) {
	s := sc.s
	n := adocURLAt(s, i)
	if n == 0 || c.inLink || (i > 0 && !adocSpace(s[i-1]) && strings.IndexByte("<>()[];\"'", s[i-1]) < 0) {
		return 0, ""
	}
	end := sc.find(adocFindTargetEnd, i, func(j int) bool { return adocTargetStop(s[j]) })
	if end < 0 {
		end = len(s)
	}
	if end < len(s) && s[end] == '[' {
		if k := c.bracket(sc, end); k > 0 {
			return k + 1 - i, c.link(s[i:end], s[end+1:k])
		}
	}
	target := strings.TrimRight(s[i:end], ".,;:!?)")
	if len(target) <= n {
		return 0, ""
	}
	return len(target), c.link(target, "")
}

// macro renders link:, mailto:, xref:, image: and pass: at i.
func (c adocInline) macro(sc *adocScan, i int) (int, string) {
	s := sc.s
	name, _, ok := strings.Cut(s[i:min(len(s), i+9)], ":")
	if !ok || adocWordBefore(s, i) {
		return 0, ""
	}
	switch name {
	case "link", "mailto", "xref", "image":
	case "pass":
		j := i + 5
		for j < len(s) && (s[j] >= 'a' && s[j] <= 'z' || s[j] == ',') {
			j++
		}
		if j < len(s) && s[j] == '[' {
			if k := c.bracket(sc, j); k > 0 {
				return k + 1 - i, adocEsc(adocUnbracket(s[j+1 : k]))
			}
		}
		return 0, ""
	default:
		return 0, ""
	}
	from := i + len(name) + 1
	if from >= len(s) || adocSpace(s[from]) || s[from] == '[' || (name == "image" && s[from] == ':') {
		return 0, ""
	}
	if name == "link" && c.inLink {
		return 0, ""
	}
	end := sc.find(adocFindTargetEnd, from, func(j int) bool { return adocTargetStop(s[j]) })
	if end < 0 || s[end] != '[' {
		return 0, ""
	}
	k := c.bracket(sc, end)
	if k < 0 {
		return 0, ""
	}
	target, text := s[from:end], s[end+1:k]
	switch name {
	case "link":
		return k + 1 - i, c.link(target, text)
	case "mailto":
		if text == "" {
			text = target
		}
		return k + 1 - i, c.link("mailto:"+target, text)
	case "xref":
		return k + 1 - i, c.xref(target, text)
	}
	return k + 1 - i, adocImage(target, text, c.imagesdir, c.inLink)
}

func adocUnbracket(s string) string { return strings.ReplaceAll(s, `\]`, "]") }

func (c adocInline) link(href, text string) string {
	text = strings.TrimSuffix(adocUnbracket(text), "^")
	label := adocEsc(strings.TrimPrefix(href, "mailto:"))
	if text != "" {
		in := c.deeper()
		in.inLink = true
		label = in.render(text)
	}
	if c.inLink || !adocSafeURL(href) {
		return label
	}
	return `<a href="` + adocEsc(href) + `">` + label + `</a>`
}

func (c adocInline) xrefAngle(sc *adocScan, i int) (int, string) {
	s := sc.s
	j := sc.find(adocFindAngle, i+2, func(j int) bool { return strings.HasPrefix(s[j:], ">>") })
	if j <= i+2 {
		return 0, ""
	}
	if r, _ := utf8.DecodeRuneInString(s[i+2:]); !adocWord(r) && !strings.ContainsRune("#/.:{", r) {
		return 0, ""
	}
	target, text, _ := strings.Cut(s[i+2:j], ",")
	return j + 2 - i, c.xref(target, strings.TrimSpace(text))
}

// adocDocument tells whether an xref target names another AsciiDoc file.
func adocDocument(target string) bool {
	t := TypeOf(target)
	return t != nil && t.Name == "asciidoc"
}

// xref links a section of this file by the slug the page finds headings with, or another file.
func (c adocInline) xref(target, text string) string {
	target = strings.TrimSpace(target)
	file, id, hash := strings.Cut(target, "#")
	if !hash && !adocDocument(target) {
		file, id = "", target
	}
	label := func(def string) string {
		if text == "" {
			return adocEsc(def)
		}
		in := c.deeper()
		in.inLink = true
		return in.render(adocUnbracket(text))
	}
	if file != "" {
		if path.Ext(file) == "" {
			file += ".adoc"
		}
		if c.inLink || !adocSafeURL(file) {
			return label(file)
		}
		return `<a href="` + adocEsc(file) + `">` + label(file) + `</a>`
	}
	var sec adocSection
	ok := false
	if c.p != nil {
		if sec, ok = c.p.secs[id]; !ok {
			sec, ok = c.p.titles[id]
		}
	}
	if !ok || c.inLink {
		return label("[" + id + "]")
	}
	return `<a href="#` + adocEsc(sec.slug) + `">` + label(sec.title) + `</a>`
}

// adocImage returns an img tag; a relative path goes below imagesdir.
func adocImage(target, attrs, dir string, inLink bool) string {
	pos, named := adocAttrList(attrs)
	alt := named["alt"]
	if alt == "" && len(pos) > 0 {
		alt = pos[0]
	}
	if alt == "" {
		base := path.Base(target)
		alt = strings.NewReplacer("-", " ", "_", " ").Replace(strings.TrimSuffix(base, path.Ext(base)))
	}
	src := target
	if dir != "" && !adocSchemeRx.MatchString(target) && !strings.HasPrefix(target, "/") {
		src = strings.TrimSuffix(dir, "/") + "/" + target
	}
	if !adocSafeURL(src) {
		return adocEsc(alt)
	}
	img := `<img src="` + adocEsc(src) + `" alt="` + adocEsc(alt) + `">`
	if l := named["link"]; l != "" && !inLink && adocSafeURL(l) {
		img = `<a href="` + adocEsc(l) + `">` + img + `</a>`
	}
	return img
}

var (
	adocSchemeRx = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:`)
	adocCtrlRx   = regexp.MustCompile(`[\x00-\x20]`)
)

// adocSafeURL lets through relative paths and web and mail links: safeURL in view.js, without data:.
func adocSafeURL(u string) bool {
	u = strings.ToLower(adocCtrlRx.ReplaceAllString(u, ""))
	return !adocSchemeRx.MatchString(u) || strings.HasPrefix(u, "http:") || strings.HasPrefix(u, "https:") || strings.HasPrefix(u, "mailto:")
}

// sub puts attribute values in for {name}. Passthroughs keep their text, an unknown name stays as
// written, and \{name} stays without its backslash.
func (p *adocParser) sub(s string) string {
	if !strings.Contains(s, "{") {
		return s
	}
	var b strings.Builder
	sc := &adocScan{s: s}
	text := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			if i+1 < len(s) && s[i+1] == '{' {
				b.WriteString(s[text:i])
				text = i + 1
				i++
			}
		case '+':
			if n, _ := adocPass(sc, i); n > 0 {
				i += n - 1
			}
		case 'p':
			if strings.HasPrefix(s[i:], "pass:") && !adocWordBefore(s, i) {
				if j := strings.IndexByte(s[i:min(len(s), i+16)], '['); j > 0 {
					if k := (adocInline{}).bracket(sc, i+j); k > 0 {
						i = k
					}
				}
			}
		case '{':
			j := i + 1
			for j < len(s) && (s[j] == '_' || s[j] == '-' && j > i+1 || s[j] < utf8.RuneSelf && unicode.IsLetter(rune(s[j])) || s[j] >= '0' && s[j] <= '9') {
				j++
			}
			if j > i+1 && j < len(s) && s[j] == '}' {
				if v, ok := p.r.attrs[strings.ToLower(s[i+1:j])]; ok {
					b.WriteString(s[text:i])
					b.WriteString(v)
					text = j + 1
				}
				i = j
			}
		}
	}
	b.WriteString(s[text:])
	return b.String()
}

// jsSlug is slug in view.js, which finds the heading a #link names.
func jsSlug(s string) string {
	s = strings.ToLower(strings.TrimFunc(s, jsSpace))
	var b strings.Builder
	for _, r := range s {
		switch {
		case jsSpace(r):
			b.WriteByte('-')
		case unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_' || r == '-':
			b.WriteRune(r)
		}
	}
	return b.String()
}

// jsSpace is \s in a JavaScript regular expression.
func jsSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

// isBinary is git's own test: a NUL byte in the first 8000 bytes. A copy of git.IsBinary,
// as render imports no other kijk package.
func isBinary(head []byte) bool {
	return bytes.IndexByte(head[:min(len(head), 8000)], 0) >= 0
}
