package main

import (
	"runtime/debug"
	"testing"
)

func TestResolveVersion(t *testing.T) {
	rev := []debug.BuildSetting{{Key: "vcs.revision", Value: "7715b0f1a2b3c4d5"}}
	cases := []struct {
		name    string
		stamped string
		bi      *debug.BuildInfo
		want    string
	}{
		{"release build", "0.3.0", &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}, "0.3.0"},
		{"go install of a tag", "", &debug.BuildInfo{Main: debug.Module{Version: "v0.3.0"}}, "0.3.0"},
		{"pseudo-version", "", &debug.BuildInfo{Main: debug.Module{Version: "v0.0.0-20261005120000-7715b0f1a2b3"}, Settings: rev}, "dev (7715b0f)"},
		{"dirty tag", "", &debug.BuildInfo{Main: debug.Module{Version: "v0.3.0+dirty"}, Settings: rev}, "dev (7715b0f)"},
		{"devel", "", &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}, Settings: rev}, "dev (7715b0f)"},
		{"empty, no git", "", &debug.BuildInfo{}, "dev"},
		{"no build info", "", nil, "dev"},
		{"bad stamp falls back", "v0.3.0", &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}, "dev"},
	}
	for _, c := range cases {
		if got := resolveVersion(c.stamped, c.bi); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"0.3.0", "0.2.0", true},
		{"0.10.0", "0.9.9", true},
		{"1.0.0", "0.99.99", true},
		{"0.2.0", "0.2.0", false},
		{"0.2.0", "0.3.0", false},
		{"0.3.0-rc1", "0.2.0", false},
		{"v0.3.0", "0.2.0", false},
		{"0.3", "0.2.0", false},
		{"0.03.0", "0.2.0", false},
		{"0.3.0", "dev (7715b0f)", false},
	}
	for _, c := range cases {
		if got := newer(c.a, c.b); got != c.want {
			t.Errorf("newer(%q, %q) = %v", c.a, c.b, got)
		}
	}
}
