import { el, h, patch, get } from './api.js';
import { isCurrent } from './lines.js';
import { boxView, showBoxes, noBoxes, goToBox, redraw, drawBox, marksChanged, oldLineOf, plural } from './boxes.js';
import { pull, read, watch, mark, setSince } from './prstore.js';
import { placeForge, threadCount } from './threads.js';
import { lineEditor, moveEditor, threadOpts, writable } from './drafts.js';
import { keyLabel } from './keys.js';

// The Changes tab of a pull request: its forge diff in file boxes, with its threads under their
// lines, and a + on each line that starts a comment for your review. Since your review, it shows
// the diff from the head of your last review instead.

let hooks = {}; // onTop(n, path), openFile(n, path)
let view = null;
const goals = new Map(); // a file to scroll to once the Changes of a pull request show

export function setupChanges(p) {
  hooks = p;
}

const threadsOn = (v, path) => (v.res?.threads || []).filter(t => t.path === path);

// diffOf is the diff of file f in pull request v.n: its forge diff, or since the head from.
async function diffOf(v, f, context, from) {
  const q = { root: v.root, path: f.path, orig: f.orig || '', mode: 'pr', pr: v.n, context };
  if (from) q.from = from;
  try {
    return await get('diff', q);
  } catch (err) {
    if (err.status !== 409) throw err;
    await read(v.root, v.n); // kijk restarted and lost where the forge diff starts
    return get('diff', q);
  }
}

// forgeRow is the forge diff's row of the line of row, a row of the head now: the forge places a
// comment by it, and on GitLab an unchanged line needs its line in the target.
async function forgeRow(v, f, row) {
  const res = await diffOf(v, f, 3, '');
  const hunks = res.hunks || [];
  const same = hunks.flatMap(h => h.lines).find(l => l.k !== '-' && l.n === row.n);
  if (same) return { k: same.k, o: same.o, n: same.n, t: same.t };
  if (v.res.forge?.kind === 'github') throw new Error('GitHub takes a comment only within 3 lines of a change of the pull request.');
  return { k: ' ', o: oldLineOf(hunks, row.n), n: row.n, t: row.t };
}

function source(v) {
  const { root, n } = v;
  return {
    files: () => v.files || [],
    basis: () => `${v.res?.split} ${v.res?.at} ${v.from}`,
    fetch: (f, context) => diffOf(v, f, context, v.from),
    headParts: f => [threadCount(threadsOn(v, f.path))],
    openFile: f => hooks.openFile?.(n, f.path),
    openTip: `Open the whole file\nat ${v.num}${n}`,
    decorate: (v2, b, rows) => {
      const p = pull(root, n);
      let threads = threadsOn(v, b.f.path);
      if (v.from) {
        // The left side is the head of your last review: only threads on a shown line of the head now.
        const lines = new Set(rows.filter(r => r.k !== '-').map(r => r.n));
        threads = threads.filter(t => t.outdated || (!t.old && lines.has(t.line)));
      }
      const placed = placeForge(`${n}:${b.f.path}`, rows, threads, { redraw: () => redraw(v, [b.f.path]), ...threadOpts(p) });
      const e = v.editor;
      if (e?.path === b.f.path) {
        moveEditor(e, rows, v.res.at);
        if (e.row) placed.under.set(e.key, [...(placed.under.get(e.key) || []), e.draw(p)]);
        else placed.top.push(e.draw(p)); // its line is gone: the text stays, at the top of the box
      }
      return placed;
    },
    onTop: path => hooks.onTop?.(n, path),
    marked: f => threadsOn(v, f.path).length > 0,
    viewed: f => (v.own?.(f) && v.marks?.state(v.own?.(f))) || '',
    viewedAt: f => v.marks?.at(f) || 0,
    viewable: f => !!v.own?.(f),
    setViewed: (f, on) => !!v.own?.(f) && mark(root, n, v.own?.(f), on),
    banner: () => v.bar,
  };
}

// openChanges shows the Changes tab of pull request tab.n.
export async function openChanges(viewer, root, tab, keep, save) {
  const v = boxView(viewer, root, tab, keep, save, {});
  v.n = tab.n;
  v.num = tab.num || '#';
  v.label = `Changes of ${v.num}${tab.n}`;
  v.from = '';
  Object.assign(v.src, source(v));
  v.reload = () => {}; // the forge diff does not follow the files on disk
  view = v;
  const known = pull(root, tab.n).res;
  if (known) await show(v);
  else patch(viewer, h('p', 'empty', `Fetching ${v.num}${tab.n}…`));
  await read(root, tab.n);
  const why = pull(root, tab.n).why;
  if (why && isCurrent(v) && !v.scroll) noBoxes(v, why);
}

// A mark that made every file viewed moves your last review: what shows may change.
watch(root => {
  const v = view, p = v && isCurrent(v) && v.root === root && pull(root, v.n);
  if (!p?.res) return;
  if (p.res !== v.res || p.since?.answer !== v.answer || shownKey(p) !== v.shownKey) return show(v);
  if (p.marks.seq !== v.marks.seq) {
    v.marks = p.marks;
    marksChanged(v);
  }
  redraw(v, drawn(v)); // what is busy, refused or typed
});

// drawn are the boxes drawn from the diff they show now; one on its way to a new diff draws with it.
const drawn = v => [...v.boxes.values()].filter(b => b.loaded && !b.loading && b.basis === v.src.basis()).map(b => b.f.path);

const shownKey = p => [p.from, p.since?.on, (p.files || []).map(f => f.path + f.blobs).join()].join('\n');

async function show(v) {
  const before = v.res, p = pull(v.root, v.n), res = p.res;
  Object.assign(v, { res, marks: p.marks, own: p.own, answer: p.since?.answer, shownKey: shownKey(p) });
  if (!p.files) return; // kijk asks what changed since your review; its answer shows them
  const moved = before && (v.from !== p.from || v.since !== !!p.since?.on);
  v.since = !!p.since?.on;
  // Its row is of the other diff: close it, and + on its line gives the text back.
  if (moved) v.editor = null;
  v.files = p.files;
  v.from = p.from;
  v.bar = p.since?.on && p.since.answer ? sinceBar(v, p.since.answer) : null;
  v.scroll?.setAttribute('aria-label', v.from ? `${v.label} since your review` : v.label);
  // The forge knows no line of the head of your last review, and GitHub takes a comment only on a
  // line of its own diff, which shows 3 lines around a change.
  v.src.add = writable(res) && {
    on: (v2, path, l) => openEditor(v, path, l), label: 'Comment on line', tip: `Comment on this line\n${keyLabel('addNote')}`,
    old: !v.from, near: res.forge?.kind === 'github' ? 3 : 0,
  };
  if (!res.files.length) return noBoxes(v, res.error || 'No changes');
  if (!v.scroll) v.goal = goals.get(`${v.root}\n${v.n}`) || '';
  goals.delete(`${v.root}\n${v.n}`);
  await showBoxes(v);
  if (moved && v.scroll && isCurrent(v)) v.scroll.scrollTop = 0;
  // A new thread or draft leaves the diff as it was, so its box would not draw again by itself.
  if (before && isCurrent(v)) redraw(v, drawn(v));
}

// sinceBar is the bar at the top of Changes in Since your review.
function sinceBar(v, a) {
  const bar = el('div', 'sincebar');
  const back = el('button', 'tool', 'Show all changes');
  back.onclick = () => setSince(v.root, v.n, false);
  bar.append(el('span', null, a.note || `Since your review · ${plural(a.commits, 'commit')} · ${plural(v.files.length, 'file')}`), back);
  return bar;
}

// openEditor opens the box for a new comment under row l of a file.
function openEditor(v, path, l) {
  closeEditor(v);
  const f = v.own?.({ path });
  if (!f) return;
  v.editor = lineEditor(pull(v.root, v.n), f, l, () => closeEditor(v, true), row => (v.from ? forgeRow(v, f, row) : null));
  v.editor.path = path;
  const b = v.boxes.get(path);
  if (b?.loaded) drawBox(v, b);
}

function closeEditor(v, focus) {
  if (!v.editor) return;
  const path = v.editor.path;
  v.editor = null;
  redraw(v, [path]);
  if (focus && isCurrent(v)) v.scroll?.focus({ preventScroll: true });
}

// goToChange scrolls the Changes of pull request n to a file, now or once they show.
export function goToChange(root, n, path) {
  if (view && isCurrent(view) && view.root === root && view.n === n && view.scroll) return goToBox(view, path, true);
  goals.set(`${root}\n${n}`, path);
}
