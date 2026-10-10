package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func names(es []entry) string {
	var s []string
	for _, e := range es {
		s = append(s, e.Name)
	}
	return strings.Join(s, ",")
}

func TestListDir(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{".git", ".github", "src", "Beta"} {
		os.Mkdir(filepath.Join(root, d), 0o755)
	}
	write(t, filepath.Join(root, "README.md"), "x")
	write(t, filepath.Join(root, "a.go"), "x")
	write(t, filepath.Join(root, "src", "main.go"), "x")

	es, err := listDir(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := names(es); got != ".github,Beta,src,a.go,README.md" {
		t.Fatalf("got %s", got)
	}
	if es, _ := listDir(root, "src"); names(es) != "main.go" {
		t.Fatalf("src: %s", names(es))
	}
}

func TestSymlinks(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	write(t, filepath.Join(outside, "secret"), "s")
	os.Mkdir(filepath.Join(root, "src"), 0o755)
	if err := os.Symlink(outside, filepath.Join(root, "out")); err != nil {
		t.Skip("symlinks not available:", err)
	}
	os.Symlink(filepath.Join(root, "src"), filepath.Join(root, "in"))

	es, _ := listDir(root, "")
	byName := map[string]entry{}
	for _, e := range es {
		byName[e.Name] = e
	}
	if e := byName["out"]; !e.Link || !e.Outside || !e.Dir {
		t.Fatalf("out = %+v", e)
	}
	if e := byName["in"]; !e.Link || e.Outside {
		t.Fatalf("in = %+v", e)
	}
	if _, err := resolve(root, "out/secret"); !errors.Is(err, errOutside) {
		t.Fatalf("link escape: %v", err)
	}
	if _, err := listDir(root, "out"); !errors.Is(err, errOutside) {
		t.Fatalf("list through link: %v", err)
	}
	if _, err := resolve(root, "in"); err != nil {
		t.Fatalf("inside link: %v", err)
	}
}

func TestResolveEscape(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repo")
	os.Mkdir(root, 0o755)
	write(t, filepath.Join(filepath.Dir(root), "passwd"), "x")
	if _, err := resolve(root, "../passwd"); !errors.Is(err, errOutside) {
		t.Fatalf("got %v", err)
	}
	if _, err := resolve(root, "src/../../passwd"); err == nil {
		t.Fatal("want error")
	}
}

func TestIndexAndLines(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	for i := 0; i < 1000; i++ {
		fmt.Fprintf(&b, "line %d\r\n", i)
	}
	b.WriteString("last without newline")
	p := filepath.Join(dir, "big.txt")
	write(t, p, b.String())

	c := newIndexCache()
	idx, err := c.get(p)
	if err != nil {
		t.Fatal(err)
	}
	if idx.Lines != 1001 || idx.Binary || idx.TooLarge {
		t.Fatalf("info = %+v", idx.fileInfo)
	}
	lines, _ := readLines(p, idx, 130, 133)
	if strings.Join(lines, "|") != "line 130|line 131|line 132" {
		t.Fatalf("got %q", lines)
	}
	lines, _ = readLines(p, idx, 999, 5000)
	if strings.Join(lines, "|") != "line 999|last without newline" {
		t.Fatalf("tail %q", lines)
	}

	write(t, p, "changed\n")
	if idx, _ := c.get(p); idx.Lines != 1 {
		t.Fatalf("stale index: %+v", idx.fileInfo)
	}
}

func TestLongLineCut(t *testing.T) {
	p := filepath.Join(t.TempDir(), "min.js")
	write(t, p, strings.Repeat("é", maxLineLen))
	idx, _ := newIndexCache().get(p)
	lines, _ := readLines(p, idx, 0, 1)
	if !strings.HasSuffix(lines[0], "…") || len(lines[0]) > maxLineLen+len("…") || idx.MaxCols != maxLineLen {
		t.Fatalf("len %d, maxCols %d", len(lines[0]), idx.MaxCols)
	}
}

func TestBinaryAndTooLarge(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "logo.png")
	write(t, p, "\x89PNG\x00\x00data")
	if idx, _ := newIndexCache().get(p); !idx.Binary || idx.Size != 10 {
		t.Fatalf("binary: %+v", idx.fileInfo)
	}
	big := filepath.Join(dir, "big.bin")
	f, _ := os.Create(big)
	f.Truncate(maxFileSize + 1)
	f.Close()
	if idx, _ := newIndexCache().get(big); !idx.TooLarge {
		t.Fatalf("too large: %+v", idx.fileInfo)
	}
	if _, err := newIndexCache().get(dir); err == nil {
		t.Fatal("folder should not open as a file")
	}
}

func TestRelCwd(t *testing.T) {
	root := t.TempDir()
	real, _ := filepath.EvalSymlinks(root)
	os.MkdirAll(filepath.Join(root, "services", "api"), 0o755)
	if got := relCwd(real, filepath.Join(root, "services", "api")); got != "services/api" {
		t.Fatalf("got %q", got)
	}
	if got := relCwd(real, filepath.Dir(root)); got != "" {
		t.Fatalf("outside: %q", got)
	}
}

func TestNaturalCompare(t *testing.T) {
	names := []string{"file10.js", "File2.js", "file1.js", "a", "file02.js", "b10", "b9"}
	slices.SortFunc(names, naturalCompare)
	want := []string{"a", "b9", "b10", "file1.js", "file02.js", "File2.js", "file10.js"}
	if !slices.Equal(names, want) {
		t.Fatalf("got %v, want %v", names, want)
	}
}
