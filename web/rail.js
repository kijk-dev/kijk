// The rows of the rail and the rules over them. app.js draws them.
import { folderKey, splitKey } from './api.js';

// The two lists are filters over the same rows: a row shows in each list it has a source for.
export const LISTS = ['agents', 'folders'];

export const inList = (r, list) => (list === 'agents' ? !!r.agents : !!r.saved);

// keyed turns a message's rows into a Map by folder key. The rows of a machine that dropped are stale.
export function keyed(rows, machines) {
  const stale = new Set((machines || []).filter(m => m.stale).map(m => m.name));
  return new Map((rows || []).map(r => {
    const machine = r.machine || '';
    const key = folderKey(machine, r.root);
    return [key, { ...r, key, machine, stale: stale.has(machine) }];
  }));
}

// listRows gives the rows of list: agents in the message's order, folders in the folders list's order.
export function listRows(rows, list) {
  const out = [...rows.values()].filter(r => inList(r, list));
  return list === 'folders' ? out.sort((a, b) => a.saved.at - b.saved.at) : out;
}

// cwdIn is where a row opens from list: an agent's folder, or the folder the user gave.
export const cwdIn = (r, list) => (list === 'folders' ? r.saved?.open : r.cwd) || '';

// target says which row an open request opens, null when it is not listed, and the list to show:
// the one it names, else the one that shows when it has the row, else the one that has it.
export function target(req, rows, list) {
  const r = req.root ? rows.get(folderKey('', req.root)) : null;
  const show = req.list || (r && !inList(r, list) ? (r.saved ? 'folders' : 'agents') : list);
  return { row: r && !r.saved?.missing ? r : null, list: show };
}

// closeRule says what a message does to the open folder: 'keep', 'freeze' or 'close'. A row that lost
// its last source is kept: the server keeps a folder readable while a window shows it.
export function closeRule(key, rows, machines) {
  const { machine } = splitKey(key);
  if (machine) {
    const m = machines?.find(x => x.name === machine);
    if (!m || m.state === 'disabled') return 'close';
    if (m.state !== 'connected') return 'freeze';
  }
  return rows.get(key)?.saved?.missing ? 'close' : 'keep';
}

// finish keeps the finished set: an agent row that went from Busy to Idle while it was not open.
export function finish(prev, next, openKey, finished) {
  for (const r of next.values()) {
    if (!r.agents || r.stale) continue;
    if (r.busy) finished.delete(r.key);
    else if (r.key !== openKey && prev.get(r.key)?.busy) finished.add(r.key);
  }
}
