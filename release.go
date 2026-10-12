package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"
)

// Change together with install.sh, install.ps1, go.mod and the README if the repo moves.
const defaultReleases = "https://github.com/kijk-dev/kijk/releases"

var errOffline = errors.New("could not reach GitHub")

func releasesURL() string {
	if v := os.Getenv("KIJK_RELEASES_URL"); v != "" {
		return strings.TrimRight(v, "/")
	}
	return defaultReleases
}

func assetName(goos, goarch string) string {
	name := "kijk-" + goos + "-" + goarch
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

// latestVersion reads the newest release from the /latest redirect, which skips drafts and
// pre-releases and, unlike the API, has no 60-an-hour limit.
func latestVersion() (string, error) {
	client := &http.Client{
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, _ := http.NewRequest("GET", releasesURL()+"/latest", nil)
	req.Header.Set("User-Agent", "kijk/"+kijkVersion)
	resp, err := client.Do(req)
	if err != nil {
		return "", errOffline
	}
	resp.Body.Close()
	loc, err := url.Parse(resp.Header.Get("Location"))
	if resp.StatusCode/100 != 3 || err != nil {
		return "", errors.New("could not find the latest kijk release")
	}
	v := strings.TrimPrefix(path.Base(loc.Path), "v")
	if !isRelease(v) {
		return "", errors.New("could not find the latest kijk release")
	}
	return v, nil
}

func download(client *http.Client, u string, w io.Writer) error {
	resp, err := client.Get(u)
	if err != nil {
		return errOffline
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("could not download %s (HTTP %d)", path.Base(u), resp.StatusCode)
	}
	if _, err := io.Copy(w, resp.Body); err != nil {
		return fmt.Errorf("download of %s broke off: %w", path.Base(u), err)
	}
	return nil
}

// checksumFor finds the one line for name in a sha256sum-style file.
func checksumFor(sums []byte, name string) (string, error) {
	found := ""
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) != 2 || strings.TrimPrefix(f[1], "*") != name {
			continue
		}
		if found != "" {
			return "", fmt.Errorf("checksums.txt lists %s twice", name)
		}
		if b, err := hex.DecodeString(f[0]); err != nil || len(b) != sha256.Size {
			return "", fmt.Errorf("checksums.txt has a bad line for %s", name)
		}
		found = strings.ToLower(f[0])
	}
	if found == "" {
		return "", fmt.Errorf("checksums.txt has no line for %s", name)
	}
	return found, nil
}

// downloadRelease writes the goos/goarch binary of release v to w, checked against checksums.txt.
func downloadRelease(v, goos, goarch string, w io.Writer) error {
	client := &http.Client{Timeout: 5 * time.Minute}
	base := releasesURL() + "/download/v" + v + "/"
	name := assetName(goos, goarch)
	var sums bytes.Buffer
	if err := download(client, base+"checksums.txt", &sums); err != nil {
		return err
	}
	want, err := checksumFor(sums.Bytes(), name)
	if err != nil {
		return err
	}
	h := sha256.New()
	if err := download(client, base+name, io.MultiWriter(w, h)); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != want {
		return fmt.Errorf("%s does not match checksums.txt", name)
	}
	return nil
}
