# kijk

Build, test and run steps are in `README.md`. Domain words are in `CONTEXT.md`: use them
in code, UI text and commit messages.

## Layout

- `main` at the root: agents, remote machines, the folders window, update, telemetry, and
  `newServer`, which puts all routes together. Platform code sits in files with a `_darwin`,
  `_linux`, `_windows` or `_unix` suffix. Only CI runs the Linux and Windows code, so keep those
  files small and behind the same functions as the macOS ones.
- `internal/viewer`: one folder's views over HTTP. `internal/render`: text in, HTML out, and the
  list of file types in `filetypes.go`. `internal/git`: runs git. `internal/gittest`: test
  helpers. Imports go `main` → `viewer` → `render`, and `main`, `viewer` → `git`, nothing else:
  viewer code cannot reach agents, machines or update code.
- The UI is plain ES modules and CSS in `web/`, embedded with `go:embed` in `main.go`. There is no
  build step and no npm: edit the files, rebuild, reload. The viewer side is the view of one open
  folder: `folder.js`, `view.js`, `tabs.js`, `tree.js`, `git.js`, `find.js`, `diagram.js`, `pr.js`,
  `api.js`, `keys.js`, `tip.js`, `viewer.css`, and `engines/` with one file per diagram engine. The
  app side is the page around it: `app.js` (the rail), `status.js`, `app.css` and `index.html`. App
  files may import viewer files; viewer files never import app files, and a test in `main_test.go`
  checks it.

## Rules

- kijk stays fast and light. Add a dependency to kijk itself only when the standard library or
  `golang.org/x/sys` cannot do the job. Tests may use a popular, maintained library that saves
  real work, as the browser tests use chromedp. For a change on a hot path (status refresh, the
  commit graph, the file watcher), measure on a large repo before and after.
- The server answers only on `localhost`/`127.0.0.1` and checks a token in `guard`
  (`server.go`). Add new routes in `newServer`, or in `viewer.Register` for a folder view, so
  `guard` covers them.
- Tests that run git use a repo in `t.TempDir()`. `go test -short ./...` is the fast loop; run
  `go test ./...` before a commit.
- A change to what the user sees adds or updates a journey in the browser tests
  (`e2e_test.go`). For a change to the look, also check the screenshots of
  `KIJK_E2E_SHOTS=1 go test -run TestBrowser .`.
- Write docs and UI text in plain, short sentences. Comments say why, in one or two lines.

## Add a file type

1. `internal/render/filetypes.go`: add an entry to `FileTypes`. Name its extensions or file names,
   how it shows and how its diff shows. Set `Lexer` only when chroma's own match is wrong.
2. A type rendered in Go: write its `Renderer` in `internal/render`. Do this only when it is small
   and needs no new library.
3. A type drawn by an engine: give the entry an `Engine` with its download files, each with its
   version, URL and SHA-256. Then write `web/engines/<name>.js`. It exports `load()`, which gets the
   files with `loadScript` or `loadModule` from `diagram.js`, and `draw(text, ctx)`, which returns
   `{svg}` or `{error}`.
4. `testdata/filetypes/`: add one sample file of the type.
5. `README.md`: say what kijk shows for the type.
6. Run `go test ./...`. It checks the entry, opens the sample through the API, and opens it in the
   browser journey "file types".
