package git

import (
	"fmt"
	"strings"
	"testing"
)

func jobsText(js []workflowJob) string {
	var s []string
	for _, j := range js {
		s = append(s, fmt.Sprintf("%s(%s)<-%s", j.ID, j.Name, strings.Join(j.Needs, ",")))
	}
	return strings.Join(s, " ")
}

func TestParseWorkflow(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"forms of needs", `
name: CI # the main one
on:
  push:
    branches: [main]
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - run: echo "jobs: not here"
  lint:
    name: Lint
    runs-on: ubuntu-latest
  test:
    name: "test"
    needs: build
    strategy:
      matrix:
        os: [linux, windows]
  integration:
    needs: [test, 'lint']   # flow list
  deploy:
    name: deploy preview
    needs:
      - integration
      - "build"
    environment: preview
`, "build()<- lint(Lint)<- test(test)<-build integration()<-test,lint deploy(deploy preview)<-integration,build"},
		{"four-space indent and quoted ids", "jobs:\n    'a':\n        runs-on: x\n    \"b\":\n        needs: a\n", "a()<- b()<-a"},
		{"jobs not at the end", "jobs:\n  a:\n    runs-on: x\nenv:\n  needs: a\n", "a()<-"},
		{"an anchor", "jobs:\n  a: &base\n    runs-on: x\n  b:\n    <<: *base\n    needs: a\n", "error"},
		{"needs from an expression", "jobs:\n  a:\n    runs-on: x\n  b:\n    needs: ${{ fromJSON(x) }}\n", "error"},
		{"a flow list over two lines", "jobs:\n  a:\n    runs-on: x\n  b:\n    needs: [a,\n      c]\n", "error"},
		{"a job it does not know", "jobs:\n  b:\n    needs: nope\n", "error"},
		{"no jobs", "name: x\non: push\n", "error"},
		{"jobs as a flow map", "jobs: {a: {runs-on: x}}\n", "error"},
	} {
		js, err := parseWorkflow(tc.src)
		got := jobsText(js)
		if err != nil {
			got = "error"
		}
		if got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.name, got, tc.want)
		}
	}
}

func TestMatchJob(t *testing.T) {
	jobs := []workflowJob{{ID: "build"}, {ID: "test", Name: "test"}, {ID: "call", Name: "release"}, {ID: "lint", Name: "Lint ${{ matrix.go }}"}}
	for api, want := range map[string]string{
		"build":                "build",
		"test (linux)":         "test",
		"test (windows, 1.25)": "test",
		"release / publish":    "call",
		"release (a) / x":      "call",
		"Lint 1.25":            "",
		"deploy":               "",
	} {
		if got, _ := matchJob(api, jobs); got != want {
			t.Errorf("matchJob(%q) = %q, want %q", api, got, want)
		}
	}
}
