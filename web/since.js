import { el, get } from './api.js';
import { loadView, age } from './prdata.js';
import { plural } from './boxes.js';
import { reviewOf } from './viewed.js';
import { FORGE } from './pr.js';

// Since your review: what changed in a pull request from the head of your last review to the head
// now. The switch is kept per window and pull request, and starts at All changes.

export const REBASED = 'Rebased or force-pushed since your review. All changes show.';

const on = new Set(); // root and number of the pull requests switched to Since your review
const answers = new Map(); // root, number, reviewed head and head: the answer, or the call asking
const listeners = new Set();
const retried = new Set(); // asked again once after a 409, so a lost diff cannot loop
const key = (root, n) => `${root}\n${n}`;

// onSince runs fn(root, n) after the switch moved or an answer came.
export const onSince = fn => listeners.add(fn);
const tell = (root, n) => { for (const fn of listeners) fn(root, n); };

function ask(root, n, from, at, k) {
  if (answers.size > 100) answers.clear();
  const call = get('pr/since', { root, n, from }).catch(async err => {
    if (err.status !== 409 || retried.has(k)) return { error: err.message, files: [] };
    retried.add(k);
    answers.delete(k); // kijk restarted and lost the forge diff: the new answer asks again
    await loadView(root, n).catch(() => {});
    return null;
  });
  answers.set(k, call);
  call.then(res => {
    if (!res || answers.get(k) !== call) return;
    answers.set(k, res);
    tell(root, n);
  });
}

// sinceOf is null when pull request n has no reviewed head, or is at it; else {on, from, time,
// answer}, where answer is null while kijk asks.
export function sinceOf(root, n, res) {
  const r = res?.at && res.forge ? reviewOf(root, n, res.forge) : null;
  if (!r?.head || r.head === res.at) {
    on.delete(key(root, n));
    return null;
  }
  const k = `${key(root, n)}\n${r.head}\n${res.at}`;
  if (!answers.has(k)) ask(root, n, r.head, res.at, k);
  const a = answers.get(k);
  return { on: on.has(key(root, n)), from: r.head, time: r.time, answer: a?.then ? null : a || null };
}

// shown is what Changes and the file list show: {files, since, from}. files is null while kijk asks;
// from is set when the files are those since your review.
export function shown(root, n, res) {
  const since = sinceOf(root, n, res);
  const all = res?.files || [];
  if (!since?.on) return { files: all, since: null, from: '' };
  const a = since.answer;
  if (!a) return { files: null, since, from: '' };
  if (a.rebased || a.error) return { files: all, since, from: '' };
  return { files: a.files, since, from: since.from };
}

export function setSince(root, n, yes) {
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
  const ago = age(new Date(s.time || 0).toISOString());
  const count = a && !a.rebased && !a.error ? ` · ${plural(a.commits, 'new commit')}` : '';
  const kind = (FORGE[res.forge?.kind] || FORGE.github).kind;
  for (const [yes, text, tip] of [[false, 'All changes', `All changes\nEvery file of the ${kind}`], [true, 'Since your review', `Only what changed since your review\nReviewed ${ago} ago${count}`]]) {
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
  const why = s.on && a && (a.rebased ? REBASED : a.error);
  return why ? [wrap, el('p', 'since-note', why)] : [wrap];
}

// sinceBar is the bar at the top of Changes in Since your review.
export function sinceBar(root, n, s, files) {
  const a = s.answer;
  const bar = el('div', `sincebar${a.rebased || a.error ? ' all' : ''}`);
  const text = a.rebased ? REBASED : a.error || `Since your review · ${plural(a.commits, 'commit')} · ${plural(files.length, 'file')}`;
  const back = el('button', 'tool', 'Show all changes');
  back.onclick = () => setSince(root, n, false);
  bar.append(el('span', null, text), back);
  return bar;
}
