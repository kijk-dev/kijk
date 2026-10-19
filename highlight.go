package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

const (
	hlWholeFile = 256 << 10 // files up to this size are colored in one pass, so every line has the right state
	hlContext   = 400       // bigger files are colored in windows with this many extra lines around them
	hlMaxBytes  = 512 << 10 // larger windows stay plain; chroma is too slow for them
)

// tok is one colored piece of a line: [class, text]. Class "" is plain text.
type tok [2]string

func lexerFor(path string) chroma.Lexer {
	if l := lexers.Match(filepath.Base(path)); l != nil {
		return chroma.Coalesce(l)
	}
	return nil
}

// classFor maps a token type onto a short CSS class; the class repeats once per token on the wire.
func classFor(t chroma.TokenType) string {
	switch {
	case t.InCategory(chroma.Comment):
		return "c"
	case t == chroma.KeywordType:
		return "t"
	case t.InCategory(chroma.Keyword):
		return "k"
	case t.InSubCategory(chroma.LiteralString):
		return "s"
	case t.InSubCategory(chroma.LiteralNumber):
		return "n"
	case t == chroma.NameFunction || t == chroma.NameFunctionMagic:
		return "f"
	case t == chroma.NameClass || t == chroma.NameException:
		return "t"
	case t == chroma.NameBuiltin || t == chroma.NameBuiltinPseudo:
		return "b"
	case t == chroma.NameTag:
		return "g"
	case t == chroma.NameAttribute:
		return "a"
	case t == chroma.NameDecorator:
		return "d"
	case t.InCategory(chroma.Operator):
		return "o"
	case t == chroma.GenericHeading || t == chroma.GenericSubheading:
		return "k"
	}
	return ""
}

// highlight colors lines as one text; out[i] belongs to lines[i].
func highlight(lexer chroma.Lexer, lines []string) [][]tok {
	out := make([][]tok, len(lines))
	for i := range out {
		out[i] = []tok{}
	}
	plain := func() [][]tok {
		for i, l := range lines {
			if l != "" {
				out[i] = []tok{{"", l}}
			}
		}
		return out
	}
	text := strings.Join(lines, "\n") + "\n"
	if lexer == nil || len(text) > hlMaxBytes {
		return plain()
	}
	it, err := lexer.Tokenise(nil, text)
	if err != nil {
		return plain()
	}
	i := 0
	for t := it(); t != chroma.EOF; t = it() {
		cls := classFor(t.Type)
		for j, part := range strings.Split(t.Value, "\n") {
			if j > 0 {
				i++
			}
			if i >= len(lines) {
				return out
			}
			if part == "" {
				continue
			}
			if n := len(out[i]); n > 0 && out[i][n-1][0] == cls {
				out[i][n-1][1] += part
			} else {
				out[i] = append(out[i], tok{cls, part})
			}
		}
	}
	return out
}

// hlCache keeps colored windows of recently viewed files.
type hlCache struct {
	mu sync.Mutex
	m  map[string][][]tok
}

func newHlCache() *hlCache { return &hlCache{m: map[string][][]tok{}} }

// lines returns lines [from, to) of the file, colored.
func (c *hlCache) lines(path string, idx *fileIndex, from, to int) ([][]tok, error) {
	to = min(to, idx.Lines)
	if from >= to {
		return [][]tok{}, nil
	}
	start, end := max(0, from-hlContext), min(idx.Lines, to+hlContext)
	if idx.Size <= hlWholeFile {
		start, end = 0, idx.Lines
	}
	key := fmt.Sprintf("%s|%d|%d|%d|%d", path, idx.Mtime, idx.Size, start, end)
	c.mu.Lock()
	colored, ok := c.m[key]
	c.mu.Unlock()
	if !ok {
		raw, err := readLines(path, idx, start, end)
		if err != nil {
			return nil, err
		}
		colored = highlight(lexerFor(path), raw)
		c.mu.Lock()
		if len(c.m) >= 16 {
			for k := range c.m {
				delete(c.m, k)
				break
			}
		}
		c.m[key] = colored
		c.mu.Unlock()
	}
	return colored[min(from-start, len(colored)):min(to-start, len(colored))], nil
}

// highlightDiff colors each hunk as one text, so strings and comments that span lines keep their color.
func highlightDiff(path string, d *diffResult) {
	lexer := lexerFor(path)
	for h := range d.Hunks {
		lines := d.Hunks[h].Lines
		text := make([]string, len(lines))
		for i, l := range lines {
			text[i] = l.Text
		}
		for i, toks := range highlight(lexer, text) {
			lines[i].Tokens = toks
		}
	}
}
