import { el, get } from './api.js';
import { loadView, freshView, onView, age } from './prdata.js';
import { plural } from './boxes.js';
import { reviewOf } from './viewed.js';
import { FORGE } from './pr.js';

// Since your review: what changed in a pull request from the head of your last review to the head
// now. The switch is kept per window and pull request, and starts at All changes.

const REBASED = 'Rebased or force-pushed since your review. All changes show.';
const MISSING = 'kijk does not have the commit of your last review in this clone. All changes show.';
// why is the note when kijk cannot show what changed since, and all changes show.
const why = a => (a.rebased ? REBASED : a.missing ? MISSING : a.error || '');

const on = new Set(); // root and number of the pull requests switched to Since your review
const answers = new Map(); // per pull request, last review and head now: the answer, or the call asking
const listeners = new Set();
const key = (root, n) => `${root}\n${n}`;

// onSince runs fn(root, n) after the switch moved or an answer came.
export const onSince = fn => listeners.add(fn);
const tell = (root, n) => { for (const fn of listeners) fn(root, n); };

function ask(root, n, from, at, k) {
  if (answers.size > 100) for (const [k2, a] of answers) if (!a.then) answers.delete(k2);
  const once = () => get('pr/since', { root, n, from });
  // The call stays in answers until it is done, so a draw meanwhile does not ask again.
  const call = once().catch(async err => {
    if (err.status !== 409) throw err;
    await loadView(root, n); // kijk restarted and lost the forge diff
    return once();
  });
  answers.set(k, call);
  call.then(res => {
    if (answers.get(k) !== call) return;
    if (res.at !== at) {
      // The server has a newer head than the page: show it first, which asks again. The entry stays
      // pending until then, so a draw meanwhile does not ask again.
      const wait = freshView(root, n).catch(() => {}).finally(() => {
        if (answers.get(k) !== wait) return;
        answers.delete(k);
        tell(root, n);
      });
      answers.set(k, wait);
      return;
    }
    answers.set(k, res);
    tell(root, n);
  }, err => {
    if (answers.get(k) !== call) return;
    answers.set(k, { error: err.message, files: [], failed: true });
    tell(root, n);
  });
}

// A failed answer holds until the next answer about its pull request.
onView((root, n) => {
  for (const [k, a] of answers) if (a.failed && k.startsWith(`${key(root, n)}\n`)) answers.delete(k);
});

// sinceOf is null when pull request n has no last review, is at its head, or no file of it changed
// since; else {on, from, time, answer}, where answer is null while kijk asks.
function sinceOf(root, n, res) {
  const r = res?.at && res.forge ? reviewOf(root, n, res.forge) : null;
  const k = r?.head && r.head !== res.at && `${key(root, n)}\n${r.head}\n${res.at}`;
  if (k && !answers.has(k)) ask(root, n, r.head, res.at, k);
  const a = k && answers.get(k);
  if (!k || (a && !a.then && !why(a) && !a.files.length)) {
    on.delete(key(root, n));
    return null;
  }
  return { on: on.has(key(root, n)), from: r.head, time: r.time, answer: a?.then ? null : a || null };
}

// ownFile gives, for a file that shows, the pull request's own file of that path, which holds its
// viewed mark.
const owned = new WeakMap();
export function ownFile(res) {
  if (!res) return () => undefined;
  if (!owned.has(res)) owned.set(res, new Map((res.files || []).map(f => [f.path, f])));
  const m = owned.get(res);
  return f => m.get(f.path);
}

// shown is what Changes and the file list show: {files, since, from}. files is null while kijk asks;
// from is set when the files are those since your review.
export function shown(root, n, res) {
  const since = sinceOf(root, n, res);
  const all = res?.files || [];
  if (!since?.on) return { files: all, since: null, from: '' };
  const a = since.answer;
  if (!a) return { files: null, since, from: '' };
  if (why(a)) return { files: all, since, from: '' };
  return { files: a.files, since, from: since.from };
}

function setSince(root, n, yes) {
  if (yes) on.add(key(root, n));
  else on.delete(key(root, n));
  tell(root, n);
}

// sinceParts are the switch under a pull request's header, and the note after a rebase; pick(yes)
// runs when the user moves it.
export function sinceParts(root, n, res, pick) {
  const s = sinceOf(root, n, res);
  if (!s) return [];
  const wrap = el('div', 'prmode since');
  const group = el('div', 'mode');
  group.setAttribute('role', 'group');
  group.setAttribute('aria-label', 'Which changes');
  const a = s.answer;
  const ago = s.time ? `\nReviewed ${age(new Date(s.time).toISOString())} ago` : '';
  const count = a && !why(a) ? `${ago ? ' · ' : '\n'}${plural(a.commits, 'new commit')}` : '';
  const kind = (FORGE[res.forge?.kind] || FORGE.github).kind;
  for (const [yes, text, tip] of [[false, 'All changes', `All changes\nEvery file of the ${kind}`], [true, 'Since your review', `Only what changed since your review${ago}${count}`]]) {
    const b = el('button', null, text);
    b.setAttribute('aria-pressed', yes === s.on);
    b.dataset.tip = tip;
    b.onclick = () => {
      if (yes !== s.on) setSince(root, n, yes);
      pick?.(yes);
    };
    group.append(b);
  }
  wrap.append(group);
  const note = s.on && a && why(a);
  return note ? [wrap, el('p', 'since-note', note)] : [wrap];
}

// sinceBar is the bar at the top of Changes in Since your review.
export function sinceBar(root, n, s, files) {
  const a = s.answer;
  const bar = el('div', 'sincebar');
  const text = why(a) || `Since your review · ${plural(a.commits, 'commit')} · ${plural(files.length, 'file')}`;
  const back = el('button', 'tool', 'Show all changes');
  back.onclick = () => setSince(root, n, false);
  bar.append(el('span', null, text), back);
  return bar;
}
