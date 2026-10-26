package viewer

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kijk-dev/kijk/internal/git"
	"github.com/kijk-dev/kijk/internal/gittest"
)

func TestImageType(t *testing.T) {
	for p, want := range map[string]string{
		"logo.png":           "image/png",
		"docs/Logo.PNG":      "image/png",
		"photo.JPeG":         "image/jpeg",
		"a.jpg":              "image/jpeg",
		"anim.gif":           "image/gif",
		"x.webp":             "image/webp",
		"icons/logo.svg":     "image/svg+xml",
		"favicon.ico":        "image/x-icon",
		"old.bmp":            "image/bmp",
		"new.avif":           "image/avif",
		"report.html":        "",
		"scan.tiff":          "",
		"png":                "",
		"notes.png.txt":      "",
		"assets.png/data.go": "",
	} {
		if got := imageType(p); got != want {
			t.Errorf("imageType(%q) = %q, want %q", p, got, want)
		}
	}
}

func TestIsImageType(t *testing.T) {
	for ct, want := range map[string]bool{"image/png": true, "image/svg+xml": true, "": false, "text/html": false} {
		if IsImageType(ct) != want {
			t.Errorf("%q: %v", ct, !want)
		}
	}
}

func TestLFSSize(t *testing.T) {
	pointer := "version https://git-lfs.github.com/spec/v1\noid sha256:4d7a214614ab2935c943f9e0ff69d22eadbb8f32b1258daaa5e2ca24d17e2393\nsize 2400000\n"
	for in, want := range map[string]int64{
		pointer: 2400000,
		strings.ReplaceAll(pointer, "\n", "\r\n"):                     2400000,
		"version https://git-lfs.github.com/spec/v1\noid sha256:ab\n": 0,
		"\x89PNG\r\n\x1a\n\x00\x00":                                   0,
		pointer + strings.Repeat("x", 1024):                           0,
	} {
		if got := lfsSize([]byte(in)); got != want {
			t.Errorf("lfsSize(%.40q) = %d, want %d", in, got, want)
		}
	}
}

// imageServer answers GET requests for root as the window would send them.
func imageServer(t *testing.T, root string) func(route string, q url.Values, header ...string) *httptest.ResponseRecorder {
	rts := &Roots{}
	rts.Set([]string{root})
	srv := http.NewServeMux()
	Register(srv, rts, Options{})
	return func(route string, q url.Values, header ...string) *httptest.ResponseRecorder {
		q.Set("root", root)
		r := httptest.NewRequest("GET", route+"?"+q.Encode(), nil)
		for i := 0; i+1 < len(header); i += 2 {
			r.Header.Set(header[i], header[i+1])
		}
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		return w
	}
}

func sandboxed(h http.Header) bool {
	for _, v := range h.Values("Content-Security-Policy") {
		if v == "sandbox" {
			return h.Get("X-Content-Type-Options") == "nosniff"
		}
	}
	return false
}

const pngBytes = "\x89PNG\r\n\x1a\n\x00\x00\x00\x0dIHDR"

func TestImageFromDisk(t *testing.T) {
	root := t.TempDir()
	gittest.Write(t, filepath.Join(root, "docs", "Logo.PNG"), pngBytes)
	gittest.Write(t, filepath.Join(root, "logo.svg"), `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)
	gittest.Write(t, filepath.Join(root, "report.html"), "<script>alert(1)</script>")
	big := filepath.Join(root, "big.png")
	gittest.Write(t, big, "")
	if err := os.Truncate(big, git.MaxFileSize+1); err != nil {
		t.Fatal(err)
	}
	get := imageServer(t, root)
	img := func(p string) *httptest.ResponseRecorder { return get("/api/image", url.Values{"path": {p}}) }

	if w := img("docs/Logo.PNG"); w.Code != http.StatusOK || w.Header().Get("Content-Type") != "image/png" || w.Body.String() != pngBytes {
		t.Fatalf("png: %d %q", w.Code, w.Header().Get("Content-Type"))
	}
	w := img("logo.svg")
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "image/svg+xml" || !sandboxed(w.Header()) {
		t.Fatalf("svg: %d %v", w.Code, w.Header())
	}
	if w := img("report.html"); w.Code != http.StatusNotFound || strings.Contains(w.Body.String(), "script") {
		t.Fatalf("html: %d %q", w.Code, w.Body.String())
	}
	if w := img("../../etc/passwd.png"); w.Code != http.StatusForbidden && w.Code != http.StatusNotFound {
		t.Fatalf("path escape: %d", w.Code)
	}
	if w := img("big.png"); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("big: %d", w.Code)
	}
	if w := img("missing.png"); w.Code != http.StatusNotFound {
		t.Fatalf("missing: %d", w.Code)
	}
}

func TestLFSPointerOnDisk(t *testing.T) {
	root := t.TempDir()
	gittest.Write(t, filepath.Join(root, "hero.png"), "version https://git-lfs.github.com/spec/v1\noid sha256:ab\nsize 2400000\n")
	gittest.Write(t, filepath.Join(root, "logo.png"), pngBytes)
	gittest.Write(t, filepath.Join(root, "notes.txt"), "version https://git-lfs.github.com/spec/v1\noid sha256:ab\nsize 9\n")
	get := imageServer(t, root)
	for p, want := range map[string]int64{"hero.png": 2400000, "logo.png": 0, "notes.txt": 0} {
		var info struct{ LFS int64 }
		w := get("/api/file", url.Values{"path": {p}})
		if err := json.Unmarshal(w.Body.Bytes(), &info); err != nil || info.LFS != want {
			t.Errorf("%s: lfs = %d, want %d (%s)", p, info.LFS, want, w.Body.String())
		}
	}
}

func hashObject(t *testing.T, content string) string {
	t.Helper()
	cmd := exec.Command("git", "hash-object", "--stdin")
	cmd.Dir = t.TempDir()
	cmd.Stdin = strings.NewReader(content)
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func TestChangeSides(t *testing.T) {
	gittest.Isolate(t)
	repo := gittest.Repo(t)
	names := map[string]string{}
	put := func(p, content string) {
		gittest.Write(t, filepath.Join(repo, p), content)
		names[hashObject(t, content)] = content
	}
	show := func(b *blobRef) string {
		switch {
		case b == nil:
			return "none"
		case b.oid == "":
			return "disk " + b.path
		}
		return names[b.oid] + " " + b.path
	}
	check := func(name, path, orig, mode, hash, want string) {
		t.Helper()
		before, after, err := changeSides(repo, path, orig, mode, hash)
		if got := show(before) + " | " + show(after); err != nil || got != want {
			t.Errorf("%s: %s (%v), want %s", name, got, err, want)
		}
	}

	put("a.png", "A1")
	put("old.png", "R1")
	put("gone.png", "G1")
	gittest.Run(t, repo, "add", ".")
	gittest.Run(t, repo, "commit", "-qm", "first")
	first := strings.TrimSpace(string(must(exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output())))
	check("root commit", "a.png", "", "commit", first, "none | A1 a.png")

	put("a.png", "A2")
	gittest.Run(t, repo, "add", "a.png")
	put("a.png", "A3")
	gittest.Run(t, repo, "mv", "old.png", "new.png")
	gittest.Run(t, repo, "rm", "-q", "gone.png")
	put("added.png", "N1")
	gittest.Run(t, repo, "add", "added.png")
	put("icon.webp", "W1")

	check("unstaged", "a.png", "", "unstaged", "", "A2 a.png | disk a.png")
	check("staged", "a.png", "", "staged", "", "A1 a.png | A2 a.png")
	check("rename", "new.png", "old.png", "staged", "", "R1 old.png | R1 new.png")
	check("delete", "gone.png", "", "staged", "", "G1 gone.png | none")
	check("add", "added.png", "", "staged", "", "none | N1 added.png")
	check("untracked", "icon.webp", "", "untracked", "", "none | disk icon.webp")

	gittest.Run(t, repo, "commit", "-qm", "second")
	second := strings.TrimSpace(string(must(exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output())))
	check("commit", "a.png", "", "commit", second, "A1 a.png | A2 a.png")
	check("commit rename", "new.png", "old.png", "commit", second, "R1 old.png | R1 new.png")
	check("commit delete", "gone.png", "", "commit", second, "G1 gone.png | none")

	for _, bad := range []struct{ mode, hash string }{{"commit", "zzz"}, {"commit", "--output=/tmp/x"}, {"commit", "-p"}, {"rebase", ""}} {
		if _, _, err := changeSides(repo, "a.png", "", bad.mode, bad.hash); err == nil {
			t.Errorf("%q %q: no error", bad.mode, bad.hash)
		}
	}
	if _, _, err := changeSides(repo, "../a.png", "", "staged", ""); err != git.ErrOutside {
		t.Errorf("path escape: %v", err)
	}
}

func must(b []byte, err error) []byte {
	if err != nil {
		panic(err)
	}
	return b
}

func TestImageVersions(t *testing.T) {
	gittest.Isolate(t)
	repo := gittest.Repo(t)
	gittest.Write(t, filepath.Join(repo, "a.png"), "A1")
	gittest.Write(t, filepath.Join(repo, "logo.svg"), "<svg>\n</svg>\n")
	gittest.Run(t, repo, "add", ".")
	gittest.Run(t, repo, "commit", "-qm", "first")
	gittest.Write(t, filepath.Join(repo, "a.png"), "A2")
	gittest.Run(t, repo, "add", "a.png")
	gittest.Write(t, filepath.Join(repo, "a.png"), "A3")
	gittest.Write(t, filepath.Join(repo, "new.png"), "N1")
	gittest.Write(t, filepath.Join(repo, "logo.svg"), "<svg>\n<g/>\n</svg>\n")
	get := imageServer(t, repo)
	img := func(mode, side, path string, more ...string) *httptest.ResponseRecorder {
		q := url.Values{"mode": {mode}, "side": {side}, "path": {path}}
		for i := 0; i+1 < len(more); i += 2 {
			q.Set(more[i], more[i+1])
		}
		return get("/api/image", q)
	}

	for _, c := range []struct{ mode, side, path, want string }{
		{"unstaged", "before", "a.png", "A2"},
		{"unstaged", "after", "a.png", "A3"},
		{"staged", "before", "a.png", "A1"},
		{"staged", "after", "a.png", "A2"},
		{"untracked", "after", "new.png", "N1"},
	} {
		w := img(c.mode, c.side, c.path)
		if w.Code != http.StatusOK || w.Body.String() != c.want || w.Header().Get("Content-Type") != "image/png" || !sandboxed(w.Header()) {
			t.Errorf("%s %s: %d %q %v", c.mode, c.side, w.Code, w.Body.String(), w.Header())
		}
	}
	if w := img("untracked", "before", "new.png"); w.Code != http.StatusNotFound {
		t.Errorf("missing side: %d", w.Code)
	}
	if w := img("staged", "sideways", "a.png"); w.Code != http.StatusBadRequest {
		t.Errorf("bad side: %d", w.Code)
	}
	for _, h := range []string{"zzz", "-p", "--output=/tmp/x", "HEAD", "a1b2^:a.png"} {
		if w := img("commit", "after", "a.png", "hash", h); w.Code != http.StatusBadRequest {
			t.Errorf("hash %q: %d", h, w.Code)
		}
	}
	if w := img("staged", "after", "../a.png"); w.Code != http.StatusForbidden {
		t.Errorf("path escape: %d", w.Code)
	}
	w := img("staged", "before", "a.png")
	if w2 := get("/api/image", url.Values{"mode": {"staged"}, "side": {"before"}, "path": {"a.png"}}, "If-None-Match", w.Header().Get("ETag")); w2.Code != http.StatusNotModified {
		t.Errorf("same version again: %d", w2.Code)
	}

	var res diffReply
	if err := json.Unmarshal(get("/api/diff", url.Values{"mode": {"unstaged"}, "path": {"logo.svg"}}).Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Image == nil || res.Image.Before == nil || res.Image.Before.OID == "" || res.Image.After == nil || res.Image.After.Mtime == 0 || len(res.Hunks) == 0 {
		t.Errorf("svg diff: %+v", res)
	}
	res = diffReply{}
	json.Unmarshal(get("/api/diff", url.Values{"mode": {"untracked"}, "path": {"new.png"}}).Body.Bytes(), &res)
	if res.Image == nil || res.Image.Before != nil || res.Image.After == nil || res.Image.After.Size != 2 {
		t.Errorf("untracked diff: %+v", res.Image)
	}

	gittest.Write(t, filepath.Join(repo, "hero.png"), "version https://git-lfs.github.com/spec/v1\noid sha256:ab\nsize 2400000\n")
	gittest.Run(t, repo, "add", "hero.png")
	gittest.Run(t, repo, "commit", "-qm", "lfs")
	head := strings.TrimSpace(string(must(exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output())))
	res = diffReply{}
	json.Unmarshal(get("/api/diff", url.Values{"mode": {"commit"}, "hash": {head}, "path": {"hero.png"}}).Body.Bytes(), &res)
	if res.Image == nil || res.Image.Before != nil || res.Image.After == nil || res.Image.After.LFS != 2400000 {
		t.Errorf("lfs in a commit: %+v", res.Image)
	}
	gittest.Write(t, filepath.Join(repo, "hero.png"), "H2")
	gittest.Run(t, repo, "commit", "-qam", "real")
	next := strings.TrimSpace(string(must(exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output())))
	res = diffReply{}
	json.Unmarshal(get("/api/diff", url.Values{"mode": {"commit"}, "hash": {next}, "path": {"hero.png"}}).Body.Bytes(), &res)
	if res.Image == nil || res.Image.Before == nil || res.Image.Before.LFS != 2400000 || res.Image.After.LFS != 0 || !strings.HasPrefix(head, res.Image.Parent) || res.Image.Parent == "" {
		t.Errorf("commit after lfs: %+v", res.Image)
	}
	if w := img("commit", "after", "hero.png", "hash", head); w.Code != http.StatusOK {
		t.Errorf("commit after: %d", w.Code)
	}
}
