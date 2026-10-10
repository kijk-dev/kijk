<h1>
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset=".github/assets/kijk-logo-dark.svg">
    <img alt="kijk" src=".github/assets/kijk-logo.svg" height="72">
  </picture>
</h1>

kijk is an agent-native code viewer: a small, fast, read-only view of the work of your running coding agents.

Agent-native means that kijk starts from your agents, not from projects. It knows which agents run on your machine and lists them. One click shows the folder of an agent: the file tree, the code, the diff, and the git state.

kijk is for people who run many agents at the same time. A full IDE is too heavy to keep 20 folders open. kijk is one small binary that you keep open all day, next to your terminals. You switch between agents in a second, and you can review and commit their work without an IDE.

The name is Dutch for "look".

## Supported agents

| Agent | Status |
|---|---|
| Claude Code | Supported |
| Codex | Planned |
| Pi | Planned |

## Features

### Agents

- The left rail lists every supported agent that runs on your machine. You do not open projects by hand.
- Each row shows the agent's name, Busy or Idle, the time of its last activity, the repository and the branch.
- Agents that work in the same folder share one row. An agent in a git worktree gets the row of that worktree.
- The newest activity is on top. Rows appear, change and go away as agents start, work and stop.

### Files

- The Files tab shows the whole folder as a tree, not only the changed files.
- Colors show the git status: modified, added, deleted, untracked and conflicted. Ignored files are dimmed.
- The tree opens at the folder where the agent works.

### Code

- Files open read-only, with line numbers and syntax colors.
- Large files scroll smoothly, because kijk draws only the lines on the screen.
- Markdown files show rendered, with a switch to the source. Changed blocks have a bar beside them.
- A single click opens a preview tab. A double click opens a tab that stays.
- The open file reloads when an agent changes it, and keeps your scroll position.
- Find in file, go to line, go to file (fuzzy match) and recent files.
- Back and forward take you to the places you were.

### Git

- The Git tab lists the changed files with `+N −N` line counts. Click a file to see its diff, or review with the arrow keys and Space to stage.
- A branch graph shows your branch, the main line and the pull request target, with the commits that are not pushed yet.
- Stage, commit, push and fetch. There is no discard, checkout, pull or stash, so a click cannot destroy an agent's work.
- kijk uses your installed `git`, so your config, credentials and hooks apply.

### Pull requests

If you use the `gh` (GitHub) or `glab` (GitLab) command and are logged in, the Git tab shows the pull request or merge request of the branch. It shows the state, the checks or pipeline, and the review.
kijk asks `gh` and `glab`. It never stores a token itself.

### Live updates

kijk watches the open folder. The tree, the Git tab and the open file update within a second after a change.

### The window

- `kijk` opens in an app window, without an address bar or browser tabs. This works with Chrome, Edge, Brave, Chromium and Vivaldi. With other browsers, kijk opens a normal tab.
- kijk follows the light or dark mode of your system.
- kijk remembers, per folder, the open tabs, the expanded folders and the sidebar tab. This lasts across reloads and restarts.
- The status bar shows the version, and offers an update when a new version is out.

## Keys

kijk uses the IntelliJ default keys.

| Action | macOS | Windows and Linux |
|---|---|---|
| Open the folder of agent 1 to 9 | ⌃⌥1 … ⌃⌥9 | Ctrl+Alt+1 … Ctrl+Alt+9 |
| Go to file | ⇧⌘O | Ctrl+Shift+N |
| Recent files | ⌘E | Ctrl+E |
| Find in file | ⌘F | Ctrl+F |
| Next and previous match | ⌘G, ⇧⌘G | F3, Shift+F3 |
| Go to line | ⌘L | Ctrl+G |
| Next and previous change | F7, ⇧F7 | F7, Shift+F7 |
| Back and forward | ⌘[, ⌘] | Ctrl+Alt+←, Ctrl+Alt+→ |
| Files tab | ⌘1 | Alt+1 |
| Git tab | ⌘0 | Alt+0 |
| Commit | ⌘K | Ctrl+K |
| Push | ⇧⌘K | Ctrl+Shift+K |
| Close tab | ⌘W | Ctrl+F4 |

A normal browser tab keeps some of these keys for itself. For those actions, kijk also has a second key. The tooltip of the action shows the key that works in your window.

## Requirements

- A [supported agent](#supported-agents). kijk finds Claude Code agents through the files in `~/.claude`, or in `CLAUDE_CONFIG_DIR` when you set it.
- `git`. Without it, kijk shows the files but no Git tab.
- Optional: a Chromium-based browser (Chrome, Edge, Brave, Chromium or Vivaldi), for the app window.
- Optional: `gh` or `glab`, for the pull request line.

kijk runs on macOS, Linux and Windows.

## Limits

- kijk reads the files that each agent keeps for its own use. A new release of an agent can change these files. Then kijk needs an update.
- You cannot edit files in kijk.

## Install

### macOS and Linux

```sh
curl -fsSL https://github.com/kijk-dev/kijk/releases/latest/download/install.sh | sh
```

The script downloads the newest release for your system and makes sure that it matches `checksums.txt`.
It puts `kijk` in the first of `~/.local/bin`, `~/bin` and `/usr/local/bin` that is on your PATH and that you can write to.
If none of these folders is on your PATH, the script uses `~/.local/bin` and shows the command that adds it.
The script does not use `sudo` and does not change your shell files.

To use a different folder, set `KIJK_INSTALL_DIR`:

```sh
curl -fsSL https://github.com/kijk-dev/kijk/releases/latest/download/install.sh | KIJK_INSTALL_DIR="$HOME/tools" sh
```

### Windows

Run this command in PowerShell:

```powershell
irm https://github.com/kijk-dev/kijk/releases/latest/download/install.ps1 | iex
```

The script puts `kijk.exe` in `%LOCALAPPDATA%\Programs\kijk` and adds that folder to your user PATH.
It does not need administrator rights.
Open a new terminal after the install, so that the terminal finds `kijk`.

### With Go

```sh
go install github.com/kijk-dev/kijk@latest
```

This needs Go 1.27 or later.
On macOS, install the Xcode command line tools first (`xcode-select --install`).
Without a C compiler, kijk watches files with a slower method that opens one file handle per file.

### Files from the Releases page

You can also download a binary from the [Releases page](https://github.com/kijk-dev/kijk/releases).
The binaries are not signed, so macOS and Windows show a warning for a file that comes from a browser.
The install scripts and `kijk update` do not get this warning.

- On macOS, run `xattr -d com.apple.quarantine kijk` once, in the folder of the file.
- On Windows, click **More info**, then **Run anyway**.

## Run

```sh
kijk
```

kijk starts a server on `127.0.0.1:7717` and opens its window.
If kijk already runs, the command only opens a new window.

| Command or option | What it does |
|---|---|
| `-port 7800` | Uses another port. |
| `-no-open` | Does not open a window. |
| `-no-update-check` | Does not look for a new version. |
| `kijk version` | Shows the version. |
| `kijk update` | Installs the newest version. |

## Update

When a new version is out, the status bar at the bottom of the window shows **Update**.
Click **Update** to install the new version.
Then click **Restart**. kijk restarts in the same terminal, and the window reloads.
On Windows, there is no **Restart** button. Stop kijk with Ctrl+C and start it again.

You can also update from a terminal:

```sh
kijk update
```

To install the newest release again, run the install script again.

### The update check

kijk never installs anything by itself.
A few seconds after it starts, and then once a day, it asks GitHub for the number of the newest release.
This request sends no data about you or your folders. GitHub sees your IP address, as it does for every download.
If the request fails, kijk shows nothing and tries again later.

To turn off the check, set this environment variable:

```sh
export KIJK_NO_UPDATE_CHECK=1
```

You can also start kijk with `-no-update-check`. `kijk update` and the **Update** button still work.

## Uninstall

1. Stop kijk.
2. Delete the `kijk` binary. Run `command -v kijk` (macOS, Linux) or `where.exe kijk` (Windows) to find it.
3. Delete the `kijk` folder in your user config folder:

| System | Folder |
|---|---|
| macOS | `~/Library/Application Support/kijk` |
| Linux | `~/.config/kijk` (or `$XDG_CONFIG_HOME/kijk`) |
| Windows | `%AppData%\kijk` |

On Windows, also delete `%LOCALAPPDATA%\Programs\kijk` from your user PATH.

## Build from source

You need Go 1.27 or later. On macOS, you also need the Xcode command line tools.

```sh
git clone https://github.com/kijk-dev/kijk.git
cd kijk
go build .
./kijk
```

A build from source shows its version as `dev`, followed by the commit. It does not look for updates, and `kijk update` does not replace it.

To run the tests:

```sh
go test -short ./...   # fast
go test ./...          # also builds kijk and runs the install scripts
```

## License

kijk is released under the [MIT License](LICENSE).
