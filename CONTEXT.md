# kijk

An agent-native code viewer: a read-only view of the work of running coding agents, their folders, code, diffs and git state.

## Language

**Agent-native**:
Starts from the agents that run on the machine, not from projects the user opens by hand. kijk is "an agent-native code viewer".
_Avoid_: agent-aware, agentic

**Agent**:
A running coding agent and the folder it works in. The UI shows one row per folder.
_Avoid_: Session

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
The anonymous usage events kijk sends about itself: that it started, that it ran on a day, its version, operating system, and how many agents run. Never folder paths, names, code, or an IP address. On by default, and the user can turn it off.
_Avoid_: analytics, tracking, usage stats

**Install ID**:
A random ID that tells one kijk install apart from another, so active days count per install. It says nothing about the user or the machine.
_Avoid_: user ID, device ID, distinct ID

**Active day**:
A calendar day on which kijk ran on an install, even if it was started on an earlier day.
_Avoid_: session, visit
