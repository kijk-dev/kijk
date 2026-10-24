import { get, storeKey, splitKey } from './api.js';
import { ownPR } from './pr.js';

// What kijk knows about the pull requests of each folder; parts draw from it and keep no copy.
// A folder: line (/pr, from the kijk.pr: record at first), lists (q → /prs; q '' is the list) and
// pulls (n → {view: /pr/view, floor, since, sinceOn}). An ask numbered at or below floor may be from
// before a write, so its answer is not stored. An answer: res, why the last ask failed, seq of res,
// call. since is {k, a}: what changed from the head of your last review to the head now (k); a is
// null while kijk asks. sinceOn: the switch is at Since your review, in this window.
const folders = new Map();
let seq = 0; // one counter for every ask, so "newer" is one compare

const answer = res => ({ res, why: '', seq: 0, call: null, callSeq: 0 });
const lineKey = root => `kijk.pr:${storeKey(root)}`;

const kept = root => { try { return JSON.parse(localStorage.getItem(lineKey(root))); } catch { return null; } };

function folderOf(root) {
  if (!folders.has(root)) folders.set(root, { line: answer(kept(root)), lists: new Map(), pulls: new Map() });
  return folders.get(root);
}

const resOf = (root, n) => folderOf(root).pulls.get(n)?.view.res || null;

// Viewed marks, one record per machine, forge repo and pull request: per file the blobs of its diff
// when you marked it, so a file that changed since shows, and the number of its last mark change;
// the counts for the list, and the head of your last review:
//   kijk.viewed:<machine>:<host>/<path>:<n> → {marks: {path: blobs}, at: {path: seq}, seq, files, viewed,
//                                            review: {head, time}}
const VIEWED = 'kijk.viewed:';
const blocked = new Map(); // the records while storage is blocked, until a reload
const marksKey = (root, forge, n = '') => `${VIEWED}${splitKey(root).machine}:${forge?.host}/${forge?.path}:${n}`;

function recOf(k) {
  try {
    return JSON.parse(localStorage.getItem(k));
  } catch {
    return blocked.get(k) || null;
  }
}

function keep(k, rec) {
  try {
    if (rec) localStorage.setItem(k, JSON.stringify(rec));
    else localStorage.removeItem(k);
  } catch {
    if (rec) blocked.set(k, rec);
    else blocked.delete(k);
  }
}

const stateIn = marks => f => (!marks[f.path] ? '' : marks[f.path] === f.blobs ? 'viewed' : 'changed');

function marksIn(rec, res) {
  const state = stateIn(rec?.marks || {}), files = res?.files || [];
  return { state, at: f => rec?.at?.[f.path] || 0, seq: rec?.seq || 0, count: files.filter(f => state(f) === 'viewed').length, files: files.length };
}

function save(k, rec, files) {
  // A file the pull request no longer changes loses its mark, so the record does not grow.
  const only = o => Object.fromEntries(Object.entries(o || {}).filter(([p]) => files.some(f => f.path === p)));
  const marks = only(rec.marks), at = only(rec.at), state = stateIn(marks);
  const out = { marks, at, seq: rec.seq || 0, files: files.length, viewed: files.filter(f => state(f) === 'viewed').length };
  if (rec.review) out.review = rec.review;
  keep(k, Object.keys(at).length || rec.review ? out : null);
}

const owned = new WeakMap();
const ownFile = res => {
  if (!res) return () => undefined;
  if (!owned.has(res)) owned.set(res, new Map((res.files || []).map(f => [f.path, f])));
  return f => owned.get(res).get(f.path);
};

const sinceKey = (res, review) => (res?.at && review?.head && review.head !== res.at ? `${review.head}\n${res.at}` : '');

// sinceOf is null when there is nothing to show since your last review.
function sinceOf(p, res, review) {
  const k = sinceKey(res, review), a = (k && p.since?.k === k && p.since.a) || null;
  if (!k || (a && !a.note && !a.files.length)) return null;
  return { on: !!p.sinceOn, from: review.head, time: review.time, answer: a };
}

// pull is what kijk knows about pull request n for one draw:
//   res, why     the last answer, and why the last ask failed
//   marks        {state(f): 'viewed' | 'changed' | '', at(f), seq, count, files}; seq changes with each mark
//   own(f)       the pull request's own file of a shown file's path, which holds its mark
//   since        null, or {on, from, time, answer}; answer is null while kijk asks, and has a note
//                when all changes show instead
//   files, from  the files that show (null while kijk asks what changed since); from is set when
//                they are the files since your review
export function pull(root, n) {
  const p = folderOf(root).pulls.get(n), res = p?.view.res || null;
  const rec = res?.forge ? recOf(marksKey(root, res.forge, n)) : null;
  const since = sinceOf(p, res, rec?.review), a = since?.answer, all = res?.files || [];
  const [files, from] = !since?.on ? [all, ''] : !a ? [null, ''] : a.note ? [all, ''] : [a.files, since.from];
  return { root, n, res, why: p?.view.why || '', marks: marksIn(rec, res), own: ownFile(res), since, files, from };
}

// line is the PR line's answer: the last one, else the one kept in the browser.
export const line = root => (root ? folderOf(root).line.res : null);

// own is the number of the folder's own pull request while it is open, else 0.
export const own = root => ownPR(line(root))?.number || 0;

// list is the answer of /prs, or of a search for q: {prs, total, me, forge, note}.
export const list = (root, q = '') => (root && folderOf(root).lists.get(q)?.res) || null;

// rowCounts is what a row of the list says about your work: {drafts, viewed, files}, from the last
// answer kijk has, else as kept.
export function rowCounts(root, r) {
  if (!r.number) return { drafts: 0, viewed: 0, files: 0 };
  const p = pull(root, r.number), forge = list(root)?.forge;
  const c = p.res?.files?.length ? { viewed: p.marks.count, files: p.marks.files } : forge ? recOf(marksKey(root, forge, r.number)) : null;
  return { drafts: p.res?.drafts ?? r.drafts, viewed: (forge && c?.viewed) || 0, files: c?.files || 0 };
}

// read asks for pull request n. It joins the ask on its way unless fresh; fresh also drops the
// answers of asks on their way, which may be from before a write.
export function read(root, n, fresh) {
  const pulls = folderOf(root).pulls;
  if (!pulls.has(n)) pulls.set(n, { view: answer(null), floor: 0, since: null, sinceOn: false });
  const p = pulls.get(n);
  if (fresh) p.floor = seq;
  else if (p.view.call && p.view.callSeq > p.floor) return p.view.call;
  return ask(root, p.view, () => get('pr/view', { root, n }), p, res => afterView(root, n, res));
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
    if (q || res.note || !res.forge || res.total > res.prs.length) return;
    const prefix = marksKey(root, res.forge), open = res.prs.map(r => r.number);
    let keys = [];
    try { keys = Object.keys(localStorage); } catch { /* storage blocked */ }
    for (const k of new Set([...keys, ...blocked.keys()])) if (k.startsWith(prefix) && !open.includes(Number(k.slice(prefix.length)))) keep(k, null);
  });
}

// mark marks file f of pull request n viewed, or clears its mark. False when it cannot.
export function mark(root, n, f, on) {
  const res = resOf(root, n);
  if (!res?.forge || !f.blobs) return false;
  const k = marksKey(root, res.forge, n), rec = recOf(k) || {};
  const marks = { ...rec.marks };
  if (on) marks[f.path] = f.blobs;
  else delete marks[f.path];
  const s = (rec.seq || 0) + 1;
  // Every file viewed: this head is your last review.
  const all = on && res.at && res.files.every(x => stateIn(marks)(x) === 'viewed');
  save(k, { marks, at: { ...rec.at, [f.path]: s }, seq: s, review: all ? { head: res.at, time: Date.now() } : rec.review }, res.files);
  askSince(root, n);
  changed(root);
  return true;
}

// setReviewed keeps the head of answer res as the head of your last review of pull request n.
export function setReviewed(root, n, res) {
  if (!res?.forge || !res.at) return;
  const k = marksKey(root, res.forge, n), rec = { ...recOf(k), review: { head: res.at, time: Date.now() } };
  if (res.files?.length) save(k, rec, res.files);
  else keep(k, rec); // without files kijk cannot tell which marks still count
  askSince(root, n);
  changed(root);
}

// setSince moves the switch of pull request n between All changes and Since your review.
export function setSince(root, n, on) {
  folderOf(root).pulls.get(n).sinceOn = on;
  changed(root);
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

// changed draws at once what the user did, so the code after it, such as V going on to the next
// file, finds the page as the change left it.
function changed(root) {
  redraw(root);
  flush();
}

function flush() {
  const roots = told || [];
  told = null;
  for (const r of roots) for (const fn of watchers) fn(r);
}

// A mark changed in the other kijk window: every folder draws again.
addEventListener('storage', e => {
  if (e.key !== null && !e.key.startsWith(VIEWED)) return;
  for (const [root, f] of folders) {
    for (const n of f.pulls.keys()) askSince(root, n);
    redraw(root);
  }
});

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

// afterView runs on each new answer about pull request n: the list counts follow it, a failed
// answer of what changed since holds only until it, and kijk asks what changed since.
function afterView(root, n, res) {
  const k = res.forge && marksKey(root, res.forge, n), rec = k && recOf(k), m = rec && marksIn(rec, res);
  if (m && m.files && (m.count !== rec.viewed || m.files !== rec.files)) save(k, rec, res.files);
  const p = folderOf(root).pulls.get(n);
  if (p.since?.a?.failed) p.since = null;
  askSince(root, n);
}

const REBASED = 'Rebased or force-pushed since your review. All changes show.';
const MISSING = 'kijk does not have the commit of your last review in this clone. All changes show.';

// askSince asks what changed in pull request n since your last review, unless kijk knows or asks
// already. With nothing to show, the switch goes back to All changes.
function askSince(root, n) {
  const p = folderOf(root).pulls.get(n), res = p.view.res;
  const review = res?.forge && recOf(marksKey(root, res.forge, n))?.review;
  const k = sinceKey(res, review);
  if (!k) p.sinceOn = false;
  if (!k || p.since?.k === k) return;
  const s = p.since = { k, a: null };
  const once = () => get('pr/since', { root, n, from: review.head });
  once().catch(async err => {
    if (err.status !== 409) throw err;
    await read(root, n); // kijk restarted and lost the forge diff
    return once();
  }).then(a => {
    if (p.since !== s) return;
    // The server has a newer head than the page: show that first; its answer asks again.
    if (a.at !== res.at) return read(root, n, true).finally(() => { if (p.since === s) { p.since = null; askSince(root, n); redraw(root); } });
    s.a = { ...a, note: a.rebased ? REBASED : a.missing ? MISSING : a.error || '' };
  }, err => { if (p.since === s) s.a = { note: err.message, files: [], failed: true }; }).then(() => {
    if (p.since !== s || !s.a) return;
    if (!s.a.note && !s.a.files.length) p.sinceOn = false;
    redraw(root);
  });
}
