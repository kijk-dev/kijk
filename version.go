package main

import (
	"runtime/debug"
	"strconv"
	"strings"
)

// version is set by the release build: -ldflags "-X main.version=0.3.0".
var version string

var kijkVersion = resolveVersion(version, readBuildInfo())

func readBuildInfo() *debug.BuildInfo {
	bi, _ := debug.ReadBuildInfo()
	return bi
}

// resolveVersion gives "X.Y.Z" for a release, else "dev" plus the short commit when known.
// go install …@v0.3.0 records v0.3.0; any other source build records a pseudo-version or (devel).
func resolveVersion(stamped string, bi *debug.BuildInfo) string {
	if _, ok := parseVersion(stamped); ok {
		return stamped
	}
	if bi == nil {
		return "dev"
	}
	if v := strings.TrimPrefix(bi.Main.Version, "v"); bi.Main.Version != v {
		if _, ok := parseVersion(v); ok {
			return v
		}
	}
	for _, s := range bi.Settings {
		if s.Key == "vcs.revision" && len(s.Value) >= 7 {
			return "dev (" + s.Value[:7] + ")"
		}
	}
	return "dev"
}

func isRelease(v string) bool {
	_, ok := parseVersion(v)
	return ok
}

// parseVersion accepts only plain "X.Y.Z"; pre-releases and build suffixes are not releases.
func parseVersion(v string) ([3]int, bool) {
	var n [3]int
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return n, false
	}
	for i, p := range parts {
		x, err := strconv.Atoi(p)
		if err != nil || x < 0 || strconv.Itoa(x) != p {
			return n, false
		}
		n[i] = x
	}
	return n, true
}

// newer reports whether release a is later than release b. A build that is not a release compares as neither.
func newer(a, b string) bool {
	x, ok := parseVersion(a)
	y, ok2 := parseVersion(b)
	if !ok || !ok2 {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return x[i] > y[i]
		}
	}
	return false
}
