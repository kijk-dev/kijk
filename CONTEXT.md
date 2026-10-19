# kijk

An agent-native code viewer: a read-only view of the work of running coding agents, their folders, code, diffs and git state.

## Language

**Agent-native**:
Starts from the agents that run on the machine, not from projects the user opens by hand. kijk is "an agent-native code viewer".
_Avoid_: agent-aware, agentic

**Agent**:
A running coding agent, the folder it works in, and the machine it runs on. The UI shows one row per folder on each machine.
_Avoid_: Session, thread

**Agent type**:
The program an agent is, such as Claude Code or Codex. One folder can have agents of more than one type.
_Avoid_: tool, harness, product

**Agents window**:
The kijk window that lists the running agents. `kijk` with no folder opens it.
_Avoid_: main window, dashboard

**Folders window**:
The kijk window that lists only the folders the user chose, with no agents. There is one, and `kijk <folder>` adds to it.
_Avoid_: workspace, project view

**Folder**:
In the folders window, one folder the user added, shown as one row. It stays until the user removes it. A folder inside a git repo shows as the whole repo, as an agent's folder does. Agents also work in folders, but a folder here does not need an agent.
_Avoid_: project, workspace, pinned folder

**Missing folder**:
A folder in the folders window that is no longer on disk, for example a deleted worktree or a drive that is not mounted. kijk keeps it in the list until the user removes it.
_Avoid_: broken, stale (stale is for an agent row whose data is old)

**Machine**:
A computer where agents run and kijk can see them. Either the local machine or a remote machine.
_Avoid_: host, remote, device

**Local machine**:
The machine the kijk window runs on.
_Avoid_: localhost, this device

**Remote machine**:
A machine the user has saved in kijk and reaches over SSH, such as a headless VM, together with the user kijk logs in as. One computer saved with two users is two remote machines; one saved twice with the same user is a duplicate.
_Avoid_: remote, server, host

**Disabled machine**:
A remote machine the user has turned off in kijk, for example while it is shut down for the night. kijk does not connect to it or show its agents, and the UI marks it "disabled".
_Avoid_: paused, offline, unreachable (unreachable means kijk tries and fails)

**Remote kijk**:
The kijk that runs without a window on a remote machine, started by a local kijk to serve it. A remote machine runs one remote kijk per kijk version and user, and local kijks of that version share it.
_Avoid_: server, daemon, headless kijk

**Image**:
A file that kijk shows as a picture, chosen by its extension: png, jpg, jpeg, gif, webp, svg, ico, bmp or avif, in any case. Not the same as a binary file, which kijk cannot show at all.
_Avoid_: picture, media, asset

**Diagram**:
A picture that kijk draws from text written in Mermaid or PlantUML. It is either a whole diagram file, such as `flow.mmd` or `login.puml`, or a block inside a markdown file. A diagram that kijk cannot draw shows as its text.
_Avoid_: chart, graph, image (an image is a picture file)

**Rendered view**:
How a markdown file, a diagram file or an SVG image opens: as a reader sees it, not as its text. The diff of an SVG image or a diagram file also opens this way, with the before and after pictures side by side. The user can switch each tab to the source view and back.
_Avoid_: preview (a preview tab is the tab a single click opens), visual mode

**Source view**:
The text of a file that has a rendered view, with line numbers and syntax colors where kijk knows the language. The source view of the diff of an SVG image or a diagram file is its line diff.
_Avoid_: raw, code view

**Change**:
One file entry in the Git tab: a file that differs, with git's status letter (added, modified, deleted, renamed, untracked, conflict).
_Avoid_: using "change" for a whole body of work

**Line counts**:
The number of lines added and removed, shown as `+40 −12`. A new untracked file counts all its lines as added. A binary file has none.
_Avoid_: diff size, diffstat, churn

**Branch total**:
The line counts of everything a branch adds to its PR target or main line: its commits since they split, plus its uncommitted work. A folder that is on the main line itself has no branch total.
_Avoid_: PR size, branch diff

**Telemetry**:
The anonymous usage events kijk sends about itself: that it started, that it ran on a day, its version, operating system, how many folders are in the folders window, and, on a day kijk looked for agents, how many agents of each agent type run on the local machine and how many remote machines and agents on them it sees. Never folder paths, names, code, or an IP address. On by default, and the user can turn it off.
_Avoid_: analytics, tracking, usage stats

**Install ID**:
A random ID that tells one kijk install apart from another, so active days count per install. It says nothing about the user or the machine.
_Avoid_: user ID, device ID, distinct ID

**Active day**:
A calendar day on which kijk ran on an install, even if it was started on an earlier day.
_Avoid_: session, visit
