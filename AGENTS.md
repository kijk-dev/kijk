# kijk

Build, test and run steps are in `README.md`. Domain words are in `CONTEXT.md`: use them
in code, UI text and commit messages.

## Layout

- `main` at the root: the agent scan and its rows, remote machines, the folders window, update,
  telemetry, and `newServer`, which puts all routes together. Platform code sits in files with a
  `_darwin`, `_linux`, `_windows` or `_unix` suffix. Only CI runs the Linux and Windows code, so
  keep those files small and behind the same functions as the macOS ones.
- `internal/agents`: the list of agent types. Each type is one file that adds itself to `Types`,
  with its reader and an embedded icon, and has a test folder in `testdata/<name>/`.
  `internal/agentstest`: test helpers for agent types, such as a fake Codex app server.
- `internal/viewer`: one folder's views over HTTP. `internal/render`: text in, HTML out, and the
  list of file types in `filetypes.go`. `internal/git`: runs git. `internal/gittest`: test
  helpers. Imports go `main` → `viewer` → `render`, `main` → `agents`, and `main`, `viewer` →
  `git`, nothing else: viewer code cannot reach agents, machines or update code.
- The UI is plain ES modules and CSS in `web/`, embedded with `go:embed` in `main.go`. There is no
  build step and no npm: edit the files, rebuild, reload. The viewer side is the view of one open
  folder: `folder.js`, `view.js`, `tabs.js`, `tree.js`, `git.js`, `find.js`, `diagram.js`, `pr.js`,
  the PRs tab (`prs.js` the list and an opened pull request, `review.js` the folder's own branch,
  `boxes.js` the file boxes of Branch changes and Changes, `changes.js`, `threads.js`, `drafts.js`
  what writes a review, `overview.js` with the checks graph, `prdata.js` the answers they share,
  `viewed.js` the viewed marks), `api.js`, `keys.js`, `tip.js`, `viewer.css`, and `engines/` with
  one file per diagram engine.
  The app side is the page around it: `app.js` (the rail), `status.js`, `app.css` and
  `index.html`. App files may import viewer files; viewer files never import app files, and a
  test in `main_test.go` checks it.

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
   how it shows and how its diff shows. Set `Lexer` only when chroma's own match is wrong. A type
   that chroma cannot color shows plain source: a lexer in `internal/render/lexers.go` adds colors,
   and the tests do not need one.
2. A type rendered in Go: write its `Renderer` in `internal/render`. Do this only when it is small
   and needs no new library.
3. A type drawn by an engine: give the entry an `Engine` with its download files, each with its
   version, URL and SHA-256. Then write `web/engines/<name>.js`. It exports `load()`, which gets the
   files with `loadScript` or `loadModule` from `diagram.js`, and `draw(text, ctx)`, which returns
   `{svg}`, or returns `{error}` or throws when it cannot draw. An engine with `Includes` also
   returns `includes`, the files it read.
4. `testdata/filetypes/`: add one sample file of the type.
5. `README.md`: say what kijk shows for the type.
6. Run `go test ./...`. It checks the entry, opens the sample through the API, and opens it in the
   browser journey "file types".

## Add an agent type

1. `internal/agents/<name>.go`: the reader, and an `init` that adds the type with `add`: its name
   (small letters and digits; rows and telemetry use it, as `<name>_agents`), label, icon and
   color. The reader finds the tool's folder with `env.folder`; with no folder it returns no agents
   and no error. `env.running` checks a pid, `state` keeps data between scans, and `lastMatch`
   reads the end of a JSONL file. The reader returns each agent's name, folder, Busy and last
   activity, or an error when it cannot read the tool's data.
2. `internal/agents/<name>.svg`: the type's mark, embedded by the Go file. Say where it comes from
   and its license above the `go:embed`.
3. `internal/agents/testdata/<name>/`: `home/` with a copy of the tool's folder from a real run,
   with every prompt, reply, name and path replaced by neutral text, and `want.json` with the
   agents the reader finds in it. A type that asks a server keeps the server's recorded replies
   there instead, as Codex does in `replies.json`. When the copy needs a running process or a
   server to look live, add a step to `live` in `<name>_test.go`.
4. `internal/agentstest/<name>.go`: only for such a type, a starter that runs one agent for the
   journey "agent types". Any other type gets the agents of its test folder.
5. `README.md`: add the type to Supported agents, with where kijk finds its agents.
6. Run `go test ./...`. The table test reads the test folder, and the journey "agent types" shows
   the type's icon, color and label.
