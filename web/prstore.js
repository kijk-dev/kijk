import { get, storeKey } from './api.js';
import { ownPR } from './pr.js';
import { keepCounts, sweep } from './viewed.js';

// What kijk knows about the pull requests of each folder; parts draw from it and keep no copy.
// A folder: line (/pr, from the kijk.pr: record at first), lists (q → /prs; q '' is the list) and
// pulls (n → {view: /pr/view, floor}). An ask numbered at or below floor may be from before a
// write, so its answer is not stored. An answer: res, why the last ask failed, seq of res, call.
const folders = new Map();
let seq = 0; // one counter for every ask, so "newer" is one compare

const answer = res => ({ res, why: '', seq: 0, call: null, callSeq: 0 });
const lineKey = root => `kijk.pr:${storeKey(root)}`;

const kept = root => { try { return JSON.parse(localStorage.getItem(lineKey(root))); } catch { return null; } };

function folderOf(root) {
  if (!folders.has(root)) folders.set(root, { line: answer(kept(root)), lists: new Map(), pulls: new Map() });
  return folders.get(root);
}

// pull is what kijk knows about pull request n for one draw: res, the last answer, and why.
export function pull(root, n) {
  const a = folderOf(root).pulls.get(n)?.view;
  return { root, n, res: a?.res || null, why: a?.why || '' };
}

// line is the PR line's answer: the last one, else the one kept in the browser.
export const line = root => (root ? folderOf(root).line.res : null);

// own is the number of the folder's own pull request while it is open, else 0.
export const own = root => ownPR(line(root))?.number || 0;

// list is the answer of /prs, or of a search for q: {prs, total, me, forge, note}.
export const list = (root, q = '') => (root && folderOf(root).lists.get(q)?.res) || null;

// read asks for pull request n. It joins the ask on its way unless fresh; fresh also drops the
// answers of asks on their way, which may be from before a write.
export function read(root, n, fresh) {
  const pulls = folderOf(root).pulls;
  if (!pulls.has(n)) pulls.set(n, { view: answer(null), floor: 0 });
  const p = pulls.get(n);
  if (fresh) p.floor = seq;
  else if (p.view.call && p.view.callSeq > p.floor) return p.view.call;
  return ask(root, p.view, () => get('pr/view', { root, n }), p, res => keepCounts(root, n, res));
}

// askLine asks for the PR line. On an error the line stays as it was.
export const askLine = root => ask(root, folderOf(root).line, () => get('pr', { root }), null, res => {
  try { localStorage.setItem(lineKey(root), JSON.stringify(res)); } catch { /* storage blocked */ }
});

// askList asks for the list, or searches for q, and joins the ask on its way unless fresh. A full
// list drops the viewed marks of the pull requests that are not open.
export function askList(root, q = '', fresh) {
  const lists = folderOf(root).lists;
  if (!lists.has(q)) lists.set(q, answer(null));
  const a = lists.get(q);
  if (a.call && !fresh) return a.call;
  return ask(root, a, () => get('prs', q ? { root, q } : { root }).catch(err => ({ note: err.message, prs: [] })), null, res => {
    if (!q && !res.note && res.forge && res.total <= res.prs.length) sweep(root, res.forge, res.prs.map(r => r.number));
  });
}

// watch runs fn(root) once after each task in which something about folder root changed.
const watchers = new Set();
let told = null;
export const watch = fn => { watchers.add(fn); };

// redraw has the parts of folder root draw again, for state that lives outside the store.
export function redraw(root) {
  if (!told) queueMicrotask(flush);
  (told ||= new Set()).add(root);
}

function flush() {
  const roots = told;
  told = null;
  for (const r of roots) for (const fn of watchers) fn(r);
}

// ask runs fetch and stores its answer in a when no newer one is stored and it began after the floor.
function ask(root, a, fetch, p, then) {
  const my = ++seq;
  const old = () => my <= a.seq || my <= (p?.floor || 0);
  const call = fetch().then(res => {
    if (old()) return;
    Object.assign(a, { res, why: '', seq: my });
    then?.(res);
  }, err => { if (!old()) a.why = err.message; }).finally(() => {
    if (a.call === call) a.call = null;
    redraw(root);
  });
  Object.assign(a, { call, callSeq: my });
  return call;
}
