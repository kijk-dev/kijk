package render

import (
	"path/filepath"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

const hlMaxBytes = 512 << 10 // larger windows stay plain; chroma is too slow for them

// Token is one colored piece of a line: [class, text]. Class "" is plain text.
// It is an alias, so git can hold colored diff lines without importing render.
type Token = [2]string

func LexerFor(path string) chroma.Lexer {
	if DiagramKind(filepath.ToSlash(path)) == "excalidraw" { // a drawing is JSON
		return chroma.Coalesce(lexers.Get("json"))
	}
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

// Highlight colors lines as one text; out[i] belongs to lines[i].
func Highlight(lexer chroma.Lexer, lines []string) [][]Token {
	out := make([][]Token, len(lines))
	for i := range out {
		out[i] = []Token{}
	}
	plain := func() [][]Token {
		for i, l := range lines {
			if l != "" {
				out[i] = []Token{{"", l}}
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
				out[i] = append(out[i], Token{cls, part})
			}
		}
	}
	return out
}
