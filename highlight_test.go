package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func joined(ts []tok) string {
	var b strings.Builder
	for _, t := range ts {
		b.WriteString(t[1])
	}
	return b.String()
}

func classOf(ts []tok, text string) string {
	for _, t := range ts {
		if t[1] == text {
			return t[0]
		}
	}
	return "missing"
}

func TestHighlightKeepsText(t *testing.T) {
	for _, f := range []string{"sessions.go", "web/app.js", "web/style.css"} {
		b, _ := os.ReadFile(f)
		lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
		out := highlight(lexerFor(f), lines)
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
	out := highlight(lexerFor("x.go"), []string{`func main() {`, `	s := "hi" // greet`, `}`})
	if classOf(out[0], "func") != "k" || classOf(out[0], "main") != "f" {
		t.Fatalf("line 1 = %v", out[0])
	}
	if classOf(out[1], `"hi"`) != "s" || classOf(out[1], "// greet") != "c" {
		t.Fatalf("line 2 = %v", out[1])
	}
}

func TestHighlightWindowInsideComment(t *testing.T) {
	var b strings.Builder
	b.WriteString("package main\n/*\n")
	for i := 0; i < 1000; i++ {
		b.WriteString("func notCode() {}\n")
	}
	b.WriteString("*/\nfunc real() {}\n")
	p := filepath.Join(t.TempDir(), "c.go")
	write(t, p, b.String())
	idx, _ := newIndexCache().get(p)
	c := newHlCache()

	mid, err := c.lines(p, idx, 600, 605)
	if err != nil || len(mid) != 5 {
		t.Fatalf("got %d lines, %v", len(mid), err)
	}
	for _, l := range mid {
		if len(l) != 1 || l[0][0] != "c" {
			t.Fatalf("line inside the comment is not a comment: %v", l)
		}
	}
	end, _ := c.lines(p, idx, idx.Lines-1, idx.Lines)
	if classOf(end[0], "func") != "k" {
		t.Fatalf("code after the comment: %v", end[0])
	}
}

func TestHighlightPlain(t *testing.T) {
	out := highlight(lexerFor("notes.unknownext"), []string{"func main()", ""})
	if len(out[0]) != 1 || out[0][0] != (tok{"", "func main()"}) || len(out[1]) != 0 {
		t.Fatalf("got %v", out)
	}
	big := []string{strings.Repeat("x", hlMaxBytes)}
	if out := highlight(lexerFor("a.go"), big); len(out[0]) != 1 || out[0][0][0] != "" {
		t.Fatal("an oversized window should stay plain")
	}
}
