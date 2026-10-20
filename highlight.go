package main

import (
	"fmt"
	"sync"

	"github.com/kijk-dev/kijk/internal/render"
)

const (
	hlWholeFile = 256 << 10 // files up to this size are colored in one pass, so every line has the right state
	hlContext   = 400       // bigger files are colored in windows with this many extra lines around them
)

// hlCache keeps colored windows of recently viewed files.
type hlCache struct {
	mu sync.Mutex
	m  map[string][][]render.Token
}

func newHlCache() *hlCache { return &hlCache{m: map[string][][]render.Token{}} }

// lines returns lines [from, to) of the file, colored.
func (c *hlCache) lines(path string, idx *fileIndex, from, to int) ([][]render.Token, error) {
	to = min(to, idx.Lines)
	if from >= to {
		return [][]render.Token{}, nil
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
		colored = render.Highlight(render.LexerFor(path), raw)
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
	lexer := render.LexerFor(path)
	for h := range d.Hunks {
		lines := d.Hunks[h].Lines
		text := make([]string, len(lines))
		for i, l := range lines {
			text[i] = l.Text
		}
		for i, toks := range render.Highlight(lexer, text) {
			lines[i].Tokens = toks
		}
	}
}
