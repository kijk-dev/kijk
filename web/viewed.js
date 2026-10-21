import { splitKey } from './api.js';
import { cachedView, onView } from './prdata.js';

// Viewed marks: for each file of a pull request you viewed, the blobs of its diff then. A file
// whose blobs moved since changed after you viewed it. Kept in localStorage per machine, forge repo
// and pull request, so every clone of a repo on a machine shares them:
//   kijk.viewed:<machine>:<host>/<path>:<n> → {marks: {path: blobs}, files, viewed}
// files and viewed are the counts at the last answer, for the list of pull requests.

const PREFIX = 'kijk.viewed:';
const kept = new Map(); // what was written, also when storage is blocked
const listeners = new Set();

const repoKey = (root, forge) => `${PREFIX}${splitKey(root).machine}:${forge?.host}/${forge?.path}:`;

function read(k) {
  if (kept.has(k)) return kept.get(k);
  let rec = null;
  try { rec = JSON.parse(localStorage.getItem(k)); } catch { /* storage blocked */ }
  kept.set(k, rec);
  return rec;
}

function write(k, rec) {
  kept.set(k, rec);
  try {
    if (rec) localStorage.setItem(k, JSON.stringify(rec));
    else localStorage.removeItem(k);
  } catch { /* storage blocked: the marks last until a reload */ }
}

function stateIn(marks) {
  return f => (!marks[f.path] ? '' : marks[f.path] === f.blobs ? 'viewed' : 'changed');
}

// marksOf gives, for the answer res about pull request n, state(f): 'viewed', 'changed' or '',
// and count, the files viewed now.
export function marksOf(root, n, res) {
  const rec = res?.forge ? read(repoKey(root, res.forge) + n) : null;
  const state = stateIn(rec?.marks || {});
  const files = res?.files || [];
  return { state, count: files.filter(f => state(f) === 'viewed').length, files: files.length };
}

function save(k, marks, files) {
  // A mark of a file the pull request no longer changes is of no use.
  for (const p of Object.keys(marks)) if (!files.some(f => f.path === p)) delete marks[p];
  const state = stateIn(marks);
  write(k, Object.keys(marks).length ? { marks, files: files.length, viewed: files.filter(f => state(f) === 'viewed').length } : null);
}

// setViewed marks file f of pull request n viewed, or clears its mark.
export function setViewed(root, n, res, f, on) {
  if (!res?.forge || !f.blobs) return;
  const k = repoKey(root, res.forge) + n;
  const marks = { ...read(k)?.marks };
  if (on) marks[f.path] = f.blobs;
  else delete marks[f.path];
  save(k, marks, res.files);
  for (const fn of listeners) fn(root, n);
}

// onViewed runs fn(root, n) after a mark changed.
export const onViewed = fn => listeners.add(fn);

// listCount is {viewed, files} for a row of the list: from the last answer kijk has, else as kept.
export function listCount(root, forge, n) {
  const res = cachedView(root, n);
  if (res?.files?.length) {
    const m = marksOf(root, n, res);
    return { viewed: m.count, files: m.files };
  }
  const rec = read(repoKey(root, forge) + n);
  return { viewed: rec?.viewed || 0, files: rec?.files || 0 };
}

// sweep drops the marks of the repo's pull requests that are not open.
export function sweep(root, forge, open) {
  const prefix = repoKey(root, forge);
  let keys = [];
  try { keys = Object.keys(localStorage); } catch { /* storage blocked */ }
  for (const k of new Set([...keys, ...kept.keys()])) {
    if (k.startsWith(prefix) && !open.includes(Number(k.slice(prefix.length)))) write(k, null);
  }
}

// A new answer keeps the counts for the list, when there are marks.
onView((root, n, res) => {
  if (!res.forge || !res.files?.length) return;
  const k = repoKey(root, res.forge) + n;
  const rec = read(k);
  if (!rec) return;
  const m = marksOf(root, n, res);
  if (m.count !== rec.viewed || m.files !== rec.files) save(k, { ...rec.marks }, res.files);
});
