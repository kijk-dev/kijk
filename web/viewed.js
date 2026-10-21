import { splitKey } from './api.js';
import { cachedView, onView } from './prdata.js';

// Viewed marks: per file, the blobs of its diff when you marked it, so a file that changed since
// shows, and the number of its last mark change. One record per machine, forge repo and pull
// request, with the counts for the list and the head of your last review:
//   kijk.viewed:<machine>:<host>/<path>:<n> → {marks: {path: blobs}, at: {path: seq}, seq, files, viewed,
//                                            review: {head, time}}

const PREFIX = 'kijk.viewed:';
const kept = new Map(); // the marks while storage is blocked, until a reload
const listeners = new Set();

const repoKey = (root, forge) => `${PREFIX}${splitKey(root).machine}:${forge?.host}/${forge?.path}:`;

function read(k) {
  try {
    return JSON.parse(localStorage.getItem(k));
  } catch {
    return kept.get(k) || null;
  }
}

function write(k, rec) {
  try {
    if (rec) localStorage.setItem(k, JSON.stringify(rec));
    else localStorage.removeItem(k);
  } catch {
    if (rec) kept.set(k, rec);
    else kept.delete(k);
  }
}

function stateIn(marks) {
  return f => (!marks[f.path] ? '' : marks[f.path] === f.blobs ? 'viewed' : 'changed');
}

// marksOf gives, for the answer res about pull request n, state(f): 'viewed', 'changed' or '';
// at(f), which changes with each mark or clear of f; and count, the files viewed now.
export function marksOf(root, n, res) {
  const rec = res?.forge ? read(repoKey(root, res.forge) + n) : null;
  const state = stateIn(rec?.marks || {});
  const files = res?.files || [];
  return { state, at: f => rec?.at?.[f.path] || 0, count: files.filter(f => state(f) === 'viewed').length, files: files.length };
}

function save(k, rec, files) {
  // A file the pull request no longer changes loses its mark, so the record does not grow.
  const keep = o => Object.fromEntries(Object.entries(o || {}).filter(([p]) => files.some(f => f.path === p)));
  const marks = keep(rec.marks), at = keep(rec.at);
  const state = stateIn(marks);
  const out = { marks, at, seq: rec.seq || 0, files: files.length, viewed: files.filter(f => state(f) === 'viewed').length };
  if (rec.review) out.review = rec.review;
  write(k, Object.keys(at).length || rec.review ? out : null);
}

const tell = (root, n, path) => { for (const fn of listeners) fn(root, n, path); };

// setViewed marks file f of pull request n viewed, or clears its mark. False when it cannot.
export function setViewed(root, n, res, f, on) {
  if (!res?.forge || !f.blobs) return false;
  const k = repoKey(root, res.forge) + n;
  const rec = read(k) || {};
  const marks = { ...rec.marks };
  if (on) marks[f.path] = f.blobs;
  else delete marks[f.path];
  const seq = (rec.seq || 0) + 1;
  let review = rec.review;
  // Every file viewed: keep this head as your last review.
  if (on && res.at && res.files.every(x => stateIn(marks)(x) === 'viewed')) review = { head: res.at, time: Date.now() };
  save(k, { marks, at: { ...rec.at, [f.path]: seq }, seq, review }, res.files);
  tell(root, n, f.path);
  return true;
}

// setReviewed keeps the head of answer res as the head of your last review of pull request n.
export function setReviewed(root, n, res) {
  if (!res?.forge || !res.at) return;
  const k = repoKey(root, res.forge) + n;
  const rec = { ...read(k), review: { head: res.at, time: Date.now() } };
  if (res.files?.length) save(k, rec, res.files);
  else write(k, rec); // without files kijk cannot tell which marks still count
  tell(root, n, '');
}

// moveReview moves the head of your last review from from to at, when no file of the pull request
// changed between them, so the review still holds.
export function moveReview(root, n, forge, from, at) {
  const k = repoKey(root, forge) + n;
  const rec = read(k);
  if (rec?.review?.head !== from) return;
  write(k, { ...rec, review: { ...rec.review, head: at } });
  tell(root, n, '');
}

// reviewOf is {head, time} of your last review of pull request n, or null.
export const reviewOf = (root, n, forge) => read(repoKey(root, forge) + n)?.review || null;

// onViewed runs fn(root, n, path) after a mark changed, and fn(null) after one changed in the other
// kijk window.
export const onViewed = fn => listeners.add(fn);

addEventListener('storage', e => {
  if (e.key === null || e.key.startsWith(PREFIX)) for (const fn of listeners) fn(null);
});

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
  if (m.count !== rec.viewed || m.files !== rec.files) save(k, rec, res.files);
});
