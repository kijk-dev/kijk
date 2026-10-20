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

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"

	"github.com/kijk-dev/kijk/internal/gittest"
	"github.com/kijk-dev/kijk/internal/render"
)

// TestSamples opens each sample of testdata/filetypes as the page does and checks the way its
// entry says it shows: picture, rendered HTML or diagram, its diff, and its colored source.
func TestSamples(t *testing.T) {
	gittest.Isolate(t)
	repo := gittest.Repo(t)
	files, err := os.ReadDir("../../testdata/filetypes")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join("../../testdata/filetypes", f.Name()))
		if err != nil {
			t.Fatal(err)
		}
		gittest.Write(t, filepath.Join(repo, f.Name()), string(b))
	}
	get := imageServer(t, repo)
	for _, f := range files {
		name, ft := f.Name(), render.TypeOf(f.Name())
		t.Run(name, func(t *testing.T) {
			if ft == nil {
				t.Fatal("no file type")
			}
			q := url.Values{"path": {name}}
			switch ft.Show {
			case render.ShowImage:
				if w := get("/api/image", q); w.Code != http.StatusOK || w.Header().Get("Content-Type") != ft.MIME {
					t.Errorf("image: %d %s", w.Code, w.Header().Get("Content-Type"))
				}
			case render.ShowRendered, render.ShowDrawn:
				w := get("/api/markdown", q)
				var res struct{ HTML string }
				json.Unmarshal(w.Body.Bytes(), &res)
				diagram := strings.HasPrefix(res.HTML, `<pre class="diagram `+ft.Name+`" data-lines="1-`)
				if w.Code != http.StatusOK || !strings.Contains(res.HTML, ` data-lines="`) || diagram != (ft.Show == render.ShowDrawn) {
					t.Errorf("rendered view: %d %.200q", w.Code, w.Body.String())
				}
			default:
				t.Errorf("show %q", ft.Show)
			}

			w := get("/api/diff", url.Values{"path": {name}, "mode": {"untracked"}})
			var d diffReply
			if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil {
				t.Fatalf("diff: %d %s", w.Code, w.Body.String())
			}
			pictures := ft.Diff == render.DiffPictures
			if (d.Image != nil) != (pictures && ft.Show == render.ShowImage) || (d.Diagram != nil) != (pictures && ft.Show == render.ShowDrawn) {
				t.Errorf("diff: image %v, diagram %v", d.Image != nil, d.Diagram != nil)
			}
			if d.Diagram != nil && (d.Diagram.Type != ft.Name || d.Diagram.After == nil || d.Diagram.After.Text == "") {
				t.Errorf("diagram diff: %+v", d.Diagram)
			}

			var info fileInfo
			json.Unmarshal(get("/api/file", q).Body.Bytes(), &info)
			if info.Binary {
				return
			}
			lexer := lexers.Match(name)
			if ft.Lexer != "" {
				lexer = lexers.Get(ft.Lexer)
			}
			if lexer == nil {
				t.Fatal("no lexer colors the source")
			}
			src, _ := os.ReadFile(filepath.Join(repo, name))
			want := render.Highlight(chroma.Coalesce(lexer), strings.Split(strings.TrimSuffix(string(src), "\n"), "\n"))
			var got [][]render.Token
			json.Unmarshal(get("/api/lines", url.Values{"path": {name}, "from": {"0"}, "to": {"1000"}}).Body.Bytes(), &got)
			colored := slices.ContainsFunc(got, func(l []render.Token) bool {
				return slices.ContainsFunc(l, func(tk render.Token) bool { return tk[0] != "" })
			})
			if !colored || !slices.EqualFunc(got, want, slices.Equal) {
				t.Errorf("source: got %v, want %s's %v", got, lexer.Config().Name, want)
			}
		})
	}
}
