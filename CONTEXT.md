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

**kijk window**:
The one window of kijk. Its rail shows the Agents list or the Folders list, and the rest of the window shows the open folder. `kijk` opens it, or says that it is open already.
_Avoid_: main window, dashboard, agents window, folders window

**Row**:
One folder on one machine in the rail. It is listed for its agents, as a saved folder, or both. A repo in both lists is one row: opening it from either list opens the same folder.
_Avoid_: entry, item

**Agents list**:
The list of the rail that shows the rows where agents work, grouped by machine once a remote machine is saved.
_Avoid_: agents window, sessions

**Folders list**:
The list of the rail that shows the saved folders, in the order the user added them. `kijk <folder>` adds to it.
_Avoid_: folders window, workspace, project view

**Saved folder**:
A folder the user added to the Folders list, shown as one row. It stays until the user removes it. A folder inside a git repo shows as the whole repo, as an agent's folder does. Agents also work in folders, but a saved folder does not need an agent. Saved folders are on the local machine only.
_Avoid_: project, workspace, pinned folder

**Missing folder**:
A saved folder that is no longer on disk, for example a deleted worktree or a drive that is not mounted. kijk keeps it in the list until the user removes it.
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

**File type**:
A group of files that kijk shows the same way, found by extension or exact file name, such as PNG images or Mermaid diagrams. It says how a file opens: as an image, rendered by kijk, or drawn by an engine as a diagram. It also says how its diff shows and how large a file kijk renders. kijk keeps one list of file types. A file of no file type shows as source.
_Avoid_: format, kind, language

**Image**:
A file that kijk shows as a picture, chosen by its extension: png, jpg, jpeg, gif, webp, svg, ico, bmp or avif, in any case. Not the same as a binary file, which kijk cannot show at all.
_Avoid_: picture, media, asset

**Diagram**:
A picture that kijk draws from text written in Mermaid or PlantUML, or from an Excalidraw drawing. It is a whole diagram file, such as `flow.mmd`, `login.puml` or `arch.excalidraw`. A Mermaid or PlantUML block inside a markdown or AsciiDoc file is a diagram too. A diagram that kijk cannot draw shows as its text.
_Avoid_: chart, graph, image (an image is a picture file)

**Rendered view**:
How a markdown file, an AsciiDoc file, a diagram file or an SVG image opens: as a reader sees it, not as its text. The diff of an SVG image or a diagram file also opens this way, with the before and after pictures side by side. The user can switch each tab to the source view and back.
_Avoid_: preview (a preview tab is the tab a single click opens), visual mode

**Source view**:
The text of a file that has a rendered view, with line numbers and syntax colors where kijk knows the language. The source view of the diff of an SVG image or a diagram file is its line diff.
_Avoid_: raw, code view

**Unified diff**:
A line diff in one column: each block of removed lines above the lines added in its place, each row with an old and a new line number. It is how every line diff shows until the user picks split.
_Avoid_: inline diff

**Split diff**:
A line diff in two halves: the old file on the left and the new file on the right, each with its own line numbers. A removed line faces the added line at the same place in its block, and a line with no partner faces an empty, hatched cell. The user picks unified or split once, for every line diff. A view too narrow for two halves shows unified.
_Avoid_: side-by-side diff (side by side is the picture diff of an image or a diagram), two-pane diff

**Change**:
One file entry in the Git tab: a file that differs, with git's status letter (added, modified, deleted, renamed, untracked, conflict).
_Avoid_: using "change" for a whole body of work

**Line counts**:
The number of lines added and removed, shown as `+40 −12`. A new untracked file counts all its lines as added. A binary file has none.
_Avoid_: diff size, diffstat, churn

**Branch total**:
The line counts of everything a branch adds to its PR target or main line: its commits since they split, plus its uncommitted work. A folder that is on the main line itself has no branch total.
_Avoid_: PR size, branch diff

**Branch changes**:
Every change a branch makes against its PR target or main line, taken as one body of work: its commits since they split, plus its uncommitted work. The branch total is its line counts.
_Avoid_: branch diff, PR diff, "the changes"

**Forge**:
The site that hosts a repo's origin: GitHub or GitLab.
_Avoid_: host (a machine is not a forge), remote, provider

**Pull request**:
A request on a forge to merge a branch into another. GitLab calls it a merge request, and the UI uses the forge's own word, but in code and docs "pull request" (PR) covers both.
_Avoid_: MR (except in GitLab UI text), change request

**Forge diff**:
The diff a pull request shows on its forge: only what has been pushed. A teammate's pull request has only a forge diff; your own branch also has its branch changes, which include work not yet pushed.
_Avoid_: remote diff, PR changes

**Review**:
Your comments on a pull request and a verdict (approve, request changes, or comment only), sent to the forge together when you submit it.
_Avoid_: feedback, approval (an approval is one kind of verdict)

**Agent note**:
A comment you leave on your own branch changes for the agent, not for people. It stays in kijk, never goes to the forge, and you hand it to the agent by copying it.
_Avoid_: local comment, draft (a draft is part of a review not yet submitted)

**Thread**:
A discussion on a line of a pull request, on its forge: a reviewer's comment and the replies to it. It is open, resolved, or outdated when its lines changed after it was written.
_Avoid_: comment chain, conversation (the conversation is the comments not on a line)

**Viewed mark**:
A per-file mark that says you have read that file's diff in a pull request. It clears when the file changes again after you marked it.
_Avoid_: seen, read, checked

**Last review**:
The head commit of a pull request when you last posted a review in kijk, or marked every file viewed. kijk keeps it on this machine. "Since your review" shows what changed after it.
_Avoid_: last seen, reviewed commit

**Telemetry**:
The anonymous usage events kijk sends about itself: that it started, that it ran on a day, its version, operating system, how many folders are in the Folders list, how many times Copy for agent was used, how many reviews were submitted, and, on a day kijk looked for agents (the kijk window was open), how many agents of each agent type run on the local machine and how many remote machines and agents on them it sees. Never folder paths, names, code, or an IP address. On by default, and the user can turn it off.
_Avoid_: analytics, tracking, usage stats

**Install ID**:
A random ID that tells one kijk install apart from another, so active days count per install. It says nothing about the user or the machine.
_Avoid_: user ID, device ID, distinct ID

**Active day**:
A calendar day on which kijk ran on an install, even if it was started on an earlier day.
_Avoid_: session, visit
