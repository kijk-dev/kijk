package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeRelease acts like GitHub's release pages: /latest redirects to the tag, and files are
// served under /download/v<latest>/ (and /latest/download/, as the install scripts use).
type fakeRelease struct {
	url     string
	latest  string
	files   map[string][]byte
	lookups atomic.Int32
}

func startFakeRelease(t *testing.T, latest string, files map[string][]byte) *fakeRelease {
	t.Helper()
	f := &fakeRelease{latest: latest, files: files}
	if _, ok := files["checksums.txt"]; !ok && files != nil {
		var sums strings.Builder
		for name, b := range files {
			h := sha256.Sum256(b)
			fmt.Fprintf(&sums, "%s  %s\n", hex.EncodeToString(h[:]), name)
		}
		files["checksums.txt"] = []byte(sums.String())
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case p == "/latest":
			f.lookups.Add(1)
			if f.latest == "" {
				http.NotFound(w, r)
				return
			}
			http.Redirect(w, r, f.url+"/tag/v"+f.latest, http.StatusFound)
		case strings.HasPrefix(p, "/latest/download/"):
			http.Redirect(w, r, "/download/v"+f.latest+"/"+strings.TrimPrefix(p, "/latest/download/"), http.StatusFound)
		case strings.HasPrefix(p, "/download/v"+f.latest+"/"):
			b, ok := f.files[strings.TrimPrefix(p, "/download/v"+f.latest+"/")]
			if !ok {
				http.NotFound(w, r)
				return
			}
			w.Write(b)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	f.url = srv.URL
	t.Setenv("KIJK_RELEASES_URL", srv.URL)
	return f
}

func TestLatestVersion(t *testing.T) {
	startFakeRelease(t, "0.3.0", nil)
	if v, err := latestVersion(); err != nil || v != "0.3.0" {
		t.Fatalf("latest = %q, %v", v, err)
	}

	startFakeRelease(t, "", nil)
	if _, err := latestVersion(); err == nil {
		t.Fatal("no release: want an error")
	}

	startFakeRelease(t, "0.3.0-rc1", nil)
	if _, err := latestVersion(); err == nil {
		t.Fatal("pre-release tag: want an error")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "::not a url")
		w.WriteHeader(http.StatusFound)
	}))
	defer srv.Close()
	t.Setenv("KIJK_RELEASES_URL", srv.URL)
	if _, err := latestVersion(); err == nil {
		t.Fatal("malformed Location: want an error")
	}

	t.Setenv("KIJK_RELEASES_URL", "http://127.0.0.1:1")
	if _, err := latestVersion(); err != errOffline {
		t.Fatalf("offline: got %v", err)
	}
}

func TestDownloadRelease(t *testing.T) {
	name := assetName(runtime.GOOS, runtime.GOARCH)
	bin := []byte("the new kijk")
	sum := sha256.Sum256(bin)
	line := hex.EncodeToString(sum[:]) + "  " + name + "\n"

	startFakeRelease(t, "0.3.0", map[string][]byte{name: bin})
	var got bytes.Buffer
	if err := downloadRelease("0.3.0", &got); err != nil || got.String() != string(bin) {
		t.Fatalf("download = %q, %v", got.String(), err)
	}

	for label, sums := range map[string]string{
		"missing line":   "",
		"duplicate line": line + line,
		"mismatch":       strings.Repeat("0", 64) + "  " + name + "\n",
	} {
		startFakeRelease(t, "0.3.0", map[string][]byte{name: bin, "checksums.txt": []byte(sums)})
		if err := downloadRelease("0.3.0", &bytes.Buffer{}); err == nil {
			t.Errorf("%s: want an error", label)
		}
	}

	startFakeRelease(t, "0.3.0", map[string][]byte{"checksums.txt": []byte(line)})
	if err := downloadRelease("0.3.0", &bytes.Buffer{}); err == nil {
		t.Error("missing binary: want an error")
	}
}
