package render

import (
	"os"
	"strings"
	"testing"
)

func joined(ts []Token) string {
	var b strings.Builder
	for _, t := range ts {
		b.WriteString(t[1])
	}
	return b.String()
}

func classOf(ts []Token, text string) string {
	for _, t := range ts {
		if t[1] == text {
			return t[0]
		}
	}
	return "missing"
}

func TestHighlightKeepsText(t *testing.T) {
	for _, f := range []string{"../../sessions.go", "../../web/app.js", "../../web/style.css"} {
		b, _ := os.ReadFile(f)
		lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
		out := Highlight(LexerFor(f), lines)
		if len(out) != len(lines) {
			t.Fatalf("%s: %d lines out, %d in", f, len(out), len(lines))
		}
		for i := range lines {
			if joined(out[i]) != lines[i] {
				t.Fatalf("%s:%d: got %q, want %q", f, i+1, joined(out[i]), lines[i])
			}
		}
	}
}

func TestHighlightGo(t *testing.T) {
	out := Highlight(LexerFor("x.go"), []string{`func main() {`, `	s := "hi" // greet`, `}`})
	if classOf(out[0], "func") != "k" || classOf(out[0], "main") != "f" {
		t.Fatalf("line 1 = %v", out[0])
	}
	if classOf(out[1], `"hi"`) != "s" || classOf(out[1], "// greet") != "c" {
		t.Fatalf("line 2 = %v", out[1])
	}
}

func TestHighlightPlain(t *testing.T) {
	out := Highlight(LexerFor("notes.unknownext"), []string{"func main()", ""})
	if len(out[0]) != 1 || out[0][0] != (Token{"", "func main()"}) || len(out[1]) != 0 {
		t.Fatalf("got %v", out)
	}
	big := []string{strings.Repeat("x", hlMaxBytes)}
	if out := Highlight(LexerFor("a.go"), big); len(out[0]) != 1 || out[0][0][0] != "" {
		t.Fatal("an oversized window should stay plain")
	}
}
