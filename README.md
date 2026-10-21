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
| Codex | Supported, version 0.157 or newer, started as `codex` in a terminal |
| Pi | Planned |

### Codex

kijk asks the Codex app server, which `codex` 0.157 and newer start by themselves. kijk only reads
from it. It never starts Codex or the app server, and never answers an approval.

- A Codex agent stays in the list for 60 seconds after you quit it. That is when the app server
  lets it go. When you set `thread_unload_delay_secs` in your Codex config, it is that long.
- A Codex agent that waits for your approval shows as finished.
- When kijk cannot read the Codex agents, for example after a Codex update, the status bar says
  so. The Claude Code agents still show.
- kijk does not see `codex exec`, Codex before 0.157, or a `codex` that could not reach its app
  server and runs its own. It probably does not see the Codex desktop app or the IDE extensions.

## Features

### Agents

- The left rail lists every supported agent that runs on your machine. You do not open projects by hand.
- Each row shows the agent's name, the icon of its agent type, Busy or Idle, the time of its last activity, the repository and the branch.
- Agents that work in the same folder share one row, also a Claude Code and a Codex agent. Only the icon of the agent type that works pulses. An agent in a git worktree gets the row of that worktree.
- The newest activity is on top. Rows appear, change and go away as agents start, work and stop.
- When kijk cannot read the agents of an agent type, for example after an update of that tool, the status bar says so. The agents of the other types still show.

### Folders without agents

You can also use kijk as a light viewer for folders you pick, without agents.

```sh
kijk ~/code/api ~/notes
```

- This opens a second window, "kijk · Folders". It lists only your folders, with the same files, code and Git views.
- When the folders window is open, `kijk <folder>` adds the folder to it and opens it there. `kijk folders` opens the window with the saved list.
- kijk keeps the list across restarts and updates. To remove a folder, click the ✕ on its row or press Delete. Undo puts it back.
- A folder inside a git repository shows as the whole repository, opened at that folder.
- A folder that is no longer on disk stays in the list, marked missing.
- kijk refuses your home folder, `/`, and a folder in a git repository that starts at one of them. Folders are on the local machine only.
- While no agents window is open, kijk does not look for agents or connect to remote machines.

### Files

- The Files tab shows the whole folder as a tree, not only the changed files.
- Colors show the git status: modified, added, deleted, untracked and conflicted. Ignored files are dimmed.
- The tree opens at the folder where the agent works.

### Code

- Files open read-only, with line numbers and syntax colors, also for AsciiDoc, PlantUML and Mermaid.
- Large files scroll smoothly, because kijk draws only the lines on the screen.
- Markdown files show rendered, with a switch to the source. Changed blocks have a bar beside them. Images from the folder show too.
- AsciiDoc files (`.adoc`, `.asciidoc`) show rendered the same way. kijk renders the common parts: sections, lists, tables, code, images, admonitions, cross references and attributes. Other syntax shows as plain text. `include::` shows the included file from the same folder in its place, and the view redraws when that file changes.
- Images show as pictures: png, jpg, gif, webp, svg, ico, bmp and avif. A picture fits the pane and is never enlarged. Click it, or use the Fit / 100% switch, to see it at 100%.
- SVG images show rendered, with a switch to the source.
- Mermaid diagrams show as pictures: `mermaid` blocks in markdown, `[mermaid]` blocks in AsciiDoc, and `.mmd` and `.mermaid` files, with a switch to the source. They follow the light or dark theme. A diagram with an error shows its text and the error.
- PlantUML diagrams show as pictures: `plantuml` and `puml` blocks in markdown and AsciiDoc, and `.puml`, `.plantuml`, `.pu` and `.wsd` files, with a switch to the source. They keep their own colors, on a light card. `!include` reads files from the same folder, and a diagram redraws when an included file changes.
- Excalidraw drawings in `.excalidraw` files show as pictures, with a switch to the source. They follow the light or dark theme.
- A single click opens a preview tab. A double click opens a tab that stays.
- The open file reloads when an agent changes it, and keeps your scroll position.
- Find in file, go to line, go to file (fuzzy match) and recent files.
- Back and forward take you to the places you were.

### Git

- The Git tab lists the changed files with `+N −N` line counts. Click a file to see its diff, or review with the arrow keys and Space to stage.
- A changed image shows the before and after pictures side by side. An SVG image can switch to its line diff.
- A changed Mermaid, PlantUML or Excalidraw file shows the before and after diagrams side by side, with a switch to its line diff.
- A branch graph shows your branch, the main line and the pull request target, with the commits that are not pushed yet.
- Stage, commit, push and fetch. There is no discard, checkout, pull or stash, so a click cannot destroy an agent's work.
- kijk uses your installed `git`, so your config, credentials and hooks apply.

### Pull requests

If you use the `gh` (GitHub) or `glab` (GitLab) command and are logged in, the Git tab shows the pull request or merge request of the branch. It shows the state, the checks or pipeline, and the review.
kijk asks `gh` and `glab`. It never stores a token itself. The PRs tab shows more of them: see [Review a pull request](#review-a-pull-request).

### Review a branch

The PRs tab shows your own branch the way a reviewer will see it, before you push. It shows when
the branch has changes against its pull request target or the main line, and in every repo on
GitHub or GitLab. Its tooltip says PRs on GitHub, MRs on GitLab, and Branch with no forge.

- **This branch**, the first row of the PRs tab, shows the branch, the branch it is compared with,
  and its pull request when there is one. Without `gh` or `glab`, the tab opens on it at once.
- A bar says what reviewers cannot see yet, such as "2 commits and 1 file not pushed", with Push or
  Publish.
- **Branch changes** shows every changed file in one scroll: the commits since the branch split,
  work not committed, and new files. Each file is a box. Its header stays at the top while you
  scroll through it, and a click on the chevron folds it. Lock files, minified files and diffs over
  2,000 lines start folded. The list in the tab follows the scroll; click a file to go to it.
- Lines, hunks and files that are only on your machine have dashed marks.
- **Agent notes** are notes for the agent on a line. Hover a line and click **+**, or press the
  key. A note stays in kijk, per folder and branch, and never goes to GitHub or GitLab. When the
  line moves, the note moves with it. When the line changes, the note says Outdated and shows the
  line as it was.
- **Copy for agent** shows the notes with a box each and the prompt they make. Copy puts the prompt
  on the clipboard, to paste into the agent, and marks the notes Copied. **Clear copied** deletes
  them. The open threads of the branch's pull request are in the list too, under "Threads on
  GitHub".
- When the branch has a pull request, a switch under its title picks **Local**, everything on
  your machine, or **As on GitHub** (As on GitLab), only what reviewers see. In Local mode the
  reviewers' threads show next to the agent's newer work, on the line with the same text. What you
  type in a thread's box there is an agent note.

### Review a pull request

The PRs tab lists the open pull requests of the repo: **This branch**, **Review requested**,
**Yours**, and **All open**, with a search box for a number, title, author or branch. The tab's
icon counts the pull requests that wait for your review.

- Click a pull request to open it. The sidebar shows its title, author, branches, checks and
  approvals, and its files. **Changes** shows all of them in one scroll, as the forge shows the
  diff. A button on each file opens the whole file as it is in the pull request.
- Threads show under their lines. A resolved thread folds to one line; click it to read it. A
  thread whose lines changed after it was written is in a bar at the top of its file.
- **Overview** shows the description, the reviewers and what they said, the conversation, and
  the checks. The checks of each GitHub workflow or GitLab pipeline are a graph: each job a pill,
  with lines to the jobs it needs. The first three failed jobs show the last lines of their logs.
  On your own branch, **Copy for agent** hands a failure to the agent.
- Clicking the pull request's title in the Git tab opens it in the PRs tab.
- `kijk https://github.com/acme/shop/pull/477` opens that pull request in a clone of the repo
  that kijk knows: a saved folder or an agent's folder. kijk never clones a repo.
- kijk asks GitHub or GitLab again when you open a pull request, when the window gets focus,
  every 2 minutes while the PRs tab shows, and when you press the refresh key.
- kijk reads pull requests only. It does not comment, approve or merge.

To diff a pull request, kijk fetches its head commit into your repo as `refs/kijk/pr/<number>`,
with no checkout. When the target branch on GitHub has commits you have not fetched, kijk puts
them in `refs/kijk/base/<number>`, so your `origin/main` stays as it was. These refs do not show in
`git branch`. kijk fetches again only when the pull request moved, and deletes the refs when it
closes.

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
| Open row 1 to 9 in the list | ⌃⌥1 … ⌃⌥9 | Ctrl+Alt+1 … Ctrl+Alt+9 |
| Go to file | ⇧⌘O | Ctrl+Shift+N |
| Recent files | ⌘E | Ctrl+E |
| Find in file | ⌘F | Ctrl+F |
| Next and previous match | ⌘G, ⇧⌘G | F3, Shift+F3 |
| Go to line | ⌘L | Ctrl+G |
| Next and previous change | F7, ⇧F7 | F7, Shift+F7 |
| Back and forward | ⌘[, ⌘] | Ctrl+Alt+←, Ctrl+Alt+→ |
| Files tab | ⌘1 | Alt+1 |
| Git tab | ⌘0 | Alt+0 |
| PRs tab | ⌘2 | Alt+2 |
| Note for the agent on the current line | ⌘⌥N | Ctrl+Alt+N |
| Add the note | ⌘Enter | Ctrl+Enter |
| Next and previous thread or note | ⌥↓, ⌥↑ | Alt+↓, Alt+↑ |
| Ask GitHub or GitLab again (PRs tab) | ⌘⌥Y | Ctrl+Alt+Y |
| Back to the list of pull requests | ⌥← | Alt+← |
| Commit | ⌘K | Ctrl+K |
| Push | ⇧⌘K | Ctrl+Shift+K |
| Close tab | ⌘W | Ctrl+F4 |
| Remove the focused folder (folders window) | Delete or Backspace | Delete or Backspace |
| Undo the removal | ⌘Z | Ctrl+Z |

A normal browser tab keeps some of these keys for itself. For those actions, kijk also has a second key. The tooltip of the action shows the key that works in your window.

## Requirements

- A [supported agent](#supported-agents). kijk finds Claude Code agents through the files in `~/.claude`, or in `CLAUDE_CONFIG_DIR` when you set it. It finds Codex agents through the app server socket in `~/.codex`, or in `CODEX_HOME` when you set it.
- `git`. Without it, kijk shows the files but no Git tab.
- Optional: a Chromium-based browser (Chrome, Edge, Brave, Chromium or Vivaldi), for the app window.
- Optional: `gh` or `glab`, for the pull request line and the PRs tab.

kijk runs on macOS, Linux and Windows.

## Remote machines

kijk can also show the agents that run on another machine, such as a VM without a screen. They
appear in the same Agents list, under a header per machine. You can read their files and diffs,
and stage, commit, push and fetch there.

Save a machine with an alias from `~/.ssh/config` or `user@host`:

```sh
kijk machine add azvm
```

kijk tests the SSH login first. When the login needs a passphrase or a password, kijk offers to
log in in the terminal, and `ssh` asks for it. kijk never sees it.

| Command | What it does |
|---|---|
| `kijk machine add <ssh-target>` | Save a machine |
| `kijk machine login <machine>` | Log in again, for example when a key with a passphrase is not in ssh-agent |
| `kijk machine list` | Show the saved machines and their states |
| `kijk machine disable <machine>` | Stop connecting, for example while the VM is shut down |
| `kijk machine enable <machine>` | Connect again |
| `kijk machine remove <machine>` | Forget a machine |

How it works:

- kijk uses your own `ssh`, with your SSH config, keys, ssh-agent and known hosts. It opens no
  port on either machine.
- kijk runs itself on the remote machine, in a mode without a window (`kijk serve`). When the
  remote machine has no kijk of the same version, kijk copies its own release there, into
  `~/.local/share/kijk/remote/`. Adding the machine allows this. kijk does not change a kijk that
  you installed there yourself.
- The remote kijk stops by itself 10 minutes after the last local kijk disconnects.
- When the machine is unreachable, its open folder stays on screen and Git actions are off until
  it is back.

Requirements:

- The local machine runs macOS or Linux. The remote machine runs Linux or macOS, on x86_64 or
  arm64. Remote machines are not supported on Windows yet.
- OpenSSH on both machines, and `AllowTcpForwarding` on in the remote SSH server. It is on by
  default.
- On the remote machine: `git`, and optionally `gh` or `glab` for the pull request line and the
  PRs tab. kijk asks them, and fetches pull requests, on the remote machine.
- kijk looks for Codex agents in `~/.codex` on the remote machine. When Codex uses another folder
  there, set `CODEX_HOME` where a non-interactive SSH login reads it: `~/.zshenv` for zsh,
  `~/.bashrc` for bash.

## Limits

- You cannot edit files in kijk.
- kijk does not download Git LFS files. An image stored in Git LFS shows its size in place of the picture.

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
| `kijk <folder>…` | Adds the folders to the folders window and opens it. |
| `kijk <PR link>` | Opens the pull request in the folders window, in a clone of its repo that kijk knows. |
| `kijk folders` | Opens the folders window. |
| `kijk version` | Shows the version. |
| `kijk update` | Installs the newest version. |

`folders`, `update`, `version`, `machine` and `serve` are commands. To add a folder with one of
these names, give it as a path, such as `kijk ./update`.

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

## Telemetry

kijk sends anonymous usage data, so we know how many people use it and on which systems.
It never sends your code, your folder or file names, or anything about you.
It sends the number of folders in the folders window. On a day when kijk looked for agents, it
also sends the number of agents on your machine, in total and per agent type, and how many remote
machines you saved and how many agents run on them. The count per agent type is
`<type>_agents` for each type kijk shows, such as `claude_agents` for Claude Code and
`codex_agents` for Codex. It never sends their names or addresses. It sends how many times you
used Copy for agent and how many reviews you submitted since its last report, never the notes
or the reviews. kijk on a remote machine sends nothing.

To turn it off, set `KIJK_NO_TELEMETRY=1` or `DO_NOT_TRACK=1`, or start kijk with `-no-telemetry`.

## Uninstall

1. Stop kijk.
2. Delete the `kijk` binary. Run `command -v kijk` (macOS, Linux) or `where.exe kijk` (Windows) to find it.
3. Delete kijk's folders:

| System | Folders |
|---|---|
| macOS | `~/.config/kijk` (or `$XDG_CONFIG_HOME/kijk`) and `~/Library/Application Support/kijk` |
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
go test -short ./...           # fast
go test ./...                  # also builds kijk, runs the install scripts and the browser tests
go test -run TestBrowser .     # only the browser tests
```

The browser tests start kijk with fake agents and use it in a headless Chrome, Edge, Brave,
Chromium or Vivaldi, the first one installed. Set `KIJK_E2E_BROWSER` to the path of another
browser. They run on macOS and Linux. A failed test saves a screenshot, the page, the console and
kijk's log, and says where. With `KIJK_E2E_SHOTS=1` they also save the main views in light,
dark and a narrow window.

## License

kijk is released under the [MIT License](LICENSE).
