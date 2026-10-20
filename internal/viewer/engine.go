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
	"sync"
	"time"
)

// engine is a diagram engine. It is downloaded on first use rather than embedded, so kijk does
// not grow by its size; the SHA-256 makes the source irrelevant.
type engine struct {
	name, label, version, url, sum string
}

var engines = map[string]engine{
	"mermaid": {
		name:    "mermaid",
		label:   "Mermaid",
		version: "12.2.0",
		url:     "https://cdn.jsdelivr.net/npm/mermaid@12.2.0/dist/mermaid.min.js",
		sum:     "a8a6a6f805b074a8261a2825c86f99c8054acb3b35f50bb7e24a4f9f2c3c3b9f",
	},
	"plantuml": {
		name:    "plantuml",
		label:   "PlantUML",
		version: "1.2026.9",
		url:     "https://cdn.jsdelivr.net/npm/@plantuml/core@1.2026.9/plantuml.js",
		sum:     "21c049ce47d087eff61ae0836f9c62a2b0c0d2528a5f16c2247057544228fae7",
	},
	"graphviz": { // PlantUML's layout engine
		name:    "graphviz",
		label:   "PlantUML",
		version: "1.2026.9",
		url:     "https://cdn.jsdelivr.net/npm/@plantuml/core@1.2026.9/viz-global.js",
		sum:     "fc6ca2de83dd4e3fca967d5214122777890a5cd1ab101115a1f716fc84e6dc5e",
	},
	"plantuml-themes": {
		name:    "plantuml-themes",
		label:   "PlantUML",
		version: "1.2026.9",
		url:     "https://cdn.jsdelivr.net/npm/@plantuml/core@1.2026.9/themes.js",
		sum:     "d6f508704b276b66631cfff05291de3013ab7807972b7cb2b77a14d4beab2c2d",
	},
	"excalidraw": { // Excalidraw's export, without React; its fonts make it 14 MB
		name:    "excalidraw",
		label:   "Excalidraw",
		version: "0.1.6",
		url:     "https://cdn.jsdelivr.net/npm/@excalidraw/utils@0.1.6/dist/prod/index.js",
		sum:     "70096399885cd5cb6b0ea9a2716e0a0413cfa15ed59cfd2bb50b42ec2b30c29c",
	},
}

const (
	engineMaxBytes = 32 << 20
	engineTimeout  = 5 * time.Minute // Excalidraw is 14 MB, also on a slow connection
	engineRetry    = time.Minute     // a page with ten diagrams makes one try, not ten
)

// src is the URL the frame loads the engine from; a new version gets a new URL, so the
// browser's copy never goes stale.
func (e engine) src() string { return "/api/engine/" + e.name + "?v=" + e.version }

type engineSlot struct {
	mu     sync.Mutex // one download at a time
	failed time.Time
}

// engineCache keeps the engines in the user cache folder.
type engineCache struct {
	slots   map[string]*engineSlot
	now     func() time.Time
	dir     func() (string, error) // the user cache folder
	version string                 // kijk's, for the User-Agent
}

func newEngineCache(dir func() (string, error), version string) *engineCache {
	c := &engineCache{slots: map[string]*engineSlot{}, now: time.Now, dir: dir, version: version}
	for name := range engines {
		c.slots[name] = &engineSlot{}
	}
	return c
}

func (c *engineCache) file(e engine) (string, error) {
	dir, err := c.dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "kijk", "engines", e.name+"-"+e.version+".js"), nil
}

// cached tells whether the engine is in the cache, without downloading it.
func (c *engineCache) cached(e engine) bool {
	p, err := c.file(e)
	return err == nil && fileCached(p)
}

func fileCached(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Size() > 0
}

// path gives the engine's file, downloaded and checked first when it is not in the cache.
func (c *engineCache) path(e engine) (string, error) {
	p, err := c.file(e)
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(p)
	s := c.slots[e.name]
	s.mu.Lock()
	defer s.mu.Unlock()
	if fileCached(p) {
		return p, nil
	}
	if c.now().Sub(s.failed) < engineRetry {
		return "", errors.New("the last download failed less than a minute ago")
	}
	if err := downloadEngine(e, dir, p, c.version); err != nil {
		log.Printf("could not download %s: %v", e.label, err)
		s.failed = c.now()
		return "", err
	}
	// A digit after the name, so a new plantuml does not delete plantuml-themes.
	old, _ := filepath.Glob(filepath.Join(dir, e.name+"-[0-9]*.js"))
	for _, o := range old {
		if o != p {
			os.Remove(o)
		}
	}
	return p, nil
}

func downloadEngine(e engine, dir, p, version string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".download-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	req, err := http.NewRequest("GET", e.url, nil)
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
		return fmt.Errorf("HTTP %d from %s", resp.StatusCode, e.url)
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, engineMaxBytes+1))
	if err != nil {
		return err
	}
	if n > engineMaxBytes {
		return fmt.Errorf("%s is larger than %d bytes", e.url, engineMaxBytes)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != e.sum {
		return fmt.Errorf("%s has SHA-256 %s, want %s", e.url, got, e.sum)
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), p)
}

// serve answers GET /api/engine/{name}. Only the local kijk has this route: a remote kijk never
// downloads an engine.
func (c *engineCache) serve(w http.ResponseWriter, r *http.Request) {
	e, ok := engines[r.PathValue("name")]
	if !ok {
		http.NotFound(w, r)
		return
	}
	p, err := c.path(e)
	if err != nil {
		http.Error(w, "Could not download "+e.label, http.StatusBadGateway)
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
