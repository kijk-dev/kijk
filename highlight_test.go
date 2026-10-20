package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/kijk-dev/kijk/internal/render"
)

func classOf(ts []render.Token, text string) string {
	for _, t := range ts {
		if t[1] == text {
			return t[0]
		}
	}
	return "missing"
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
