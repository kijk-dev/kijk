package viewer

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/kijk-dev/kijk/internal/render"
)

// download is one file of an engine, with the name of its file type and the engine's label.
type download struct {
	render.Download
	fileType, label string
}

// downloads are the files of every engine in the list, by name.
func downloads() map[string]download {
	m := map[string]download{}
	for _, t := range render.FileTypes {
		if t.Engine != nil {
			for _, f := range t.Engine.Files {
				m[f.Name] = download{f, t.Name, t.Engine.Label}
			}
		}
	}
	return m
}

// frameEngine is what the diagram frame gets of one engine: its label and the URLs of its files.
type frameEngine struct {
	Label string            `json:"label"`
	Files map[string]string `json:"files"`
}

// frame gives the diagram frame the engines by the name of their file type, and the files not in
// the cache yet.
func (c *engineCache) frame() (map[string]frameEngine, []string) {
	engines := map[string]frameEngine{}
	var missing []string
	for name, d := range c.files {
		e, ok := engines[d.fileType]
		if !ok {
			e = frameEngine{d.label, map[string]string{}}
			engines[d.fileType] = e
		}
		e.Files[name] = d.src()
		if !c.cached(d) {
			missing = append(missing, name)
		}
	}
	slices.Sort(missing)
	return engines, missing
}

const (
	engineMaxBytes = 32 << 20
	engineTimeout  = 5 * time.Minute // Excalidraw is 14 MB, also on a slow connection
	engineRetry    = time.Minute     // a page with ten diagrams makes one try, not ten
)

// src is the URL the frame loads the file from; a new version gets a new URL, so the
// browser's copy never goes stale.
func (d download) src() string { return "/api/engine/" + d.Name + "?v=" + d.Version }

type engineSlot struct {
	mu     sync.Mutex // one download at a time
	failed time.Time
}

// engineCache keeps the engines' files in the user cache folder.
type engineCache struct {
	files   map[string]download
	slots   map[string]*engineSlot
	now     func() time.Time
	dir     func() (string, error) // the user cache folder
	version string                 // kijk's, for the User-Agent
}

func newEngineCache(dir func() (string, error), version string) *engineCache {
	c := &engineCache{files: downloads(), slots: map[string]*engineSlot{}, now: time.Now, dir: dir, version: version}
	for name := range c.files {
		c.slots[name] = &engineSlot{}
	}
	return c
}

func (c *engineCache) file(d download) (string, error) {
	dir, err := c.dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "kijk", "engines", d.Name+"-"+d.Version+".js"), nil
}

// cached tells whether the file is in the cache, without downloading it.
func (c *engineCache) cached(d download) bool {
	p, err := c.file(d)
	return err == nil && fileCached(p)
}

func fileCached(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Size() > 0
}

// path gives the file, downloaded and checked first when it is not in the cache.
func (c *engineCache) path(d download) (string, error) {
	p, err := c.file(d)
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(p)
	s := c.slots[d.Name]
	s.mu.Lock()
	defer s.mu.Unlock()
	if fileCached(p) {
		return p, nil
	}
	if c.now().Sub(s.failed) < engineRetry {
		return "", errors.New("the last download failed less than a minute ago")
	}
	if err := downloadEngine(d, dir, p, c.version); err != nil {
		log.Printf("could not download %s: %v", d.label, err)
		s.failed = c.now()
		return "", err
	}
	// A digit after the name, so a new plantuml does not delete plantuml-themes.
	old, _ := filepath.Glob(filepath.Join(dir, d.Name+"-[0-9]*.js"))
	for _, o := range old {
		if o != p {
			os.Remove(o)
		}
	}
	return p, nil
}

func downloadEngine(d download, dir, p, version string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".download-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	req, err := http.NewRequest("GET", d.URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "kijk/"+version)
	resp, err := (&http.Client{Timeout: engineTimeout}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d from %s", resp.StatusCode, d.URL)
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, engineMaxBytes+1))
	if err != nil {
		return err
	}
	if n > engineMaxBytes {
		return fmt.Errorf("%s is larger than %d bytes", d.URL, engineMaxBytes)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != d.SHA256 {
		return fmt.Errorf("%s has SHA-256 %s, want %s", d.URL, got, d.SHA256)
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), p)
}

// serve answers GET /api/engine/{name}. Only the local kijk has this route: a remote kijk never
// downloads an engine.
func (c *engineCache) serve(w http.ResponseWriter, r *http.Request) {
	d, ok := c.files[r.PathValue("name")]
	if !ok {
		http.NotFound(w, r)
		return
	}
	p, err := c.path(d)
	if err != nil {
		http.Error(w, "Could not download "+d.label, http.StatusBadGateway)
		return
	}
	f, err := os.Open(p)
	if err != nil {
		fail(w, err)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "text/javascript")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	http.ServeContent(w, r, "", time.Time{}, f)
}
