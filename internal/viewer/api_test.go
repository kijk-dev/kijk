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
