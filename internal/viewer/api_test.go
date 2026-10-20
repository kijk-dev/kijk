package viewer

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kijk-dev/kijk/internal/gittest"
	"github.com/kijk-dev/kijk/internal/render"
)

func TestAsciiDocRoute(t *testing.T) {
	dir := t.TempDir()
	gittest.Write(t, filepath.Join(dir, "docs", "Guide.ADOC"), "= Guide\n\nNOTE: Hi.\n")
	gittest.Write(t, filepath.Join(dir, "big.adoc"), strings.Repeat("x", render.MaxSourceBytes+1))
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
	gittest.Write(t, outside, "Secret.")
	if err := os.Symlink(outside, filepath.Join(dir, "docs", "link.adoc")); err != nil {
		t.Skip(err)
	}
	gittest.Write(t, filepath.Join(dir, "docs", "chapters", "intro.adoc"), "The intro.")
	gittest.Write(t, filepath.Join(dir, "docs", "book.adoc"), "include::chapters/intro.adoc[]\n\ninclude::link.adoc[]\n\ninclude::chapters[]\n")
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

func TestFileTypesRoute(t *testing.T) {
	get := imageServer(t, t.TempDir())
	w := get("/api/filetypes", url.Values{})
	var list []struct {
		Name   string
		Show   string
		Engine *struct {
			Label    string
			Includes bool
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || len(list) != len(render.FileTypes) {
		t.Fatalf("%d %v %s", w.Code, err, w.Body.String())
	}
	for i, ft := range list {
		want := render.FileTypes[i]
		if ft.Name != want.Name || ft.Show != string(want.Show) || (ft.Engine == nil) != (want.Engine == nil) {
			t.Errorf("%+v, want %s", ft, want.Name)
		}
	}
	if strings.Contains(w.Body.String(), "sha256") || strings.Contains(w.Body.String(), "https:") {
		t.Errorf("the page gets no download data: %s", w.Body.String())
	}
}

func TestNoRenderedView(t *testing.T) {
	dir := t.TempDir()
	gittest.Write(t, filepath.Join(dir, "main.go"), "package main\n")
	gittest.Write(t, filepath.Join(dir, "logo.svg"), "<svg/>")
	get := imageServer(t, dir)
	for _, p := range []string{"main.go", "logo.svg"} {
		if w := get("/api/markdown", url.Values{"path": {p}}); w.Code != http.StatusNotFound {
			t.Errorf("%s: %d %q", p, w.Code, w.Body.String())
		}
	}
}
