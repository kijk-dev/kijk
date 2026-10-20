# kijk

Build, test and run steps are in `README.md`. Domain words are in `CONTEXT.md`: use them
in code, UI text and commit messages.

## Layout

- One Go package (`main`) at the root. Platform code sits in files with a `_darwin`, `_linux`,
  `_windows` or `_unix` suffix. Only CI runs the Linux and Windows code, so keep those files
  small and behind the same functions as the macOS ones.
- The UI is plain ES modules and CSS in `web/`, embedded with `go:embed`. There is no build step
  and no npm: edit the files, rebuild, reload.

## Rules

- kijk stays fast and light. Add a dependency to kijk itself only when the standard library or
  `golang.org/x/sys` cannot do the job. Tests may use a popular, maintained library that saves
  real work, as the browser tests use chromedp. For a change on a hot path (status refresh, the
  commit graph, the file watcher), measure on a large repo before and after.
- The server answers only on `localhost`/`127.0.0.1` and checks a token in `guard`
  (`server.go`). Add new routes to the mux in `newServer`, so `guard` covers them.
- Tests that run git use a repo in `t.TempDir()`. `go test -short ./...` is the fast loop; run
  `go test ./...` before a commit.
- A change to what the user sees adds or updates a journey in the browser tests
  (`e2e_test.go`). For a change to the look, also check the screenshots of
  `KIJK_E2E_SHOTS=1 go test -run TestBrowser .`.
- Write docs and UI text in plain, short sentences. Comments say why, in one or two lines.
