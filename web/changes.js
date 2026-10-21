import { el, get } from './api.js';
import { isCurrent } from './view.js';
import { boxView, showBoxes, noBoxes, goToBox, redraw, drawBox, marksChanged } from './boxes.js';
import { cachedView, loadView, onView } from './prdata.js';
import { placeForge, threadCount } from './threads.js';
import { lineEditor, moveEditor, threadOpts, writable } from './drafts.js';
import { keyLabel } from './keys.js';
import { marksOf, setViewed, onViewed } from './viewed.js';
import { shown, sinceBar, onSince } from './since.js';

// The Changes tab of a pull request: its forge diff in file boxes, with its threads under their
// lines, and a + on each line that starts a comment for your review. Since your review, it shows
// the diff from the head of your last review instead.

let hooks = {}; // onTop(n, path), openFile(n, path)
let view = null;
const goals = new Map(); // a file to scroll to once the Changes of a pull request show

export function setupChanges(h) {
  hooks = h;
}

const threadsOn = (v, path) => (v.res?.threads || []).filter(t => t.path === path);

// A file since your review is marked viewed as the pull request's own file of that path.
const prFile = (v, f) => v.res?.files.find(x => x.path === f.path);

function source(v) {
  const { root, n } = v;
  const redrawFile = path => () => redraw(v, [path]);
  return {
    files: () => v.files || [],
    basis: () => `${v.res?.split} ${v.res?.at} ${v.from}`,
    fetch: async (f, context) => {
      const q = { root, path: f.path, orig: f.orig || '', mode: 'pr', pr: n, context };
      if (v.from) q.from = v.from;
      try {
        return await get('diff', q);
      } catch (err) {
        if (err.status !== 409) throw err;
        await loadView(root, n); // kijk restarted and lost where the forge diff starts
        return get('diff', q);
      }
    },
    headParts: f => [threadCount(threadsOn(v, f.path))],
    openFile: f => hooks.openFile?.(n, f.path),
    openTip: `Open the whole file\nat #${n}`,
    decorate: (v2, b, rows) => {
      const again = redrawFile(b.f.path);
      let threads = threadsOn(v, b.f.path);
      if (v.from) {
        // The left side is the reviewed head: only threads on a shown line of the head now.
        const lines = new Set(rows.filter(r => r.k !== '-').map(r => r.n));
        threads = threads.filter(t => t.outdated || (!t.old && lines.has(t.line)));
      }
      const placed = placeForge(`${n}:${b.f.path}`, rows, threads, { redraw: again, ...threadOpts(root, n, v.res, again) });
      const e = v.editor;
      if (e?.path === b.f.path) {
        moveEditor(e, rows, v.res.at);
        if (e.row) placed.under.set(e.key, [...(placed.under.get(e.key) || []), e.el]);
        else placed.top.push(e.el); // its line is gone: the text stays, at the top of the box
      }
      return placed;
    },
    addOld: true,
    addLabel: 'Comment on line',
    addTip: `Comment on this line\n${keyLabel('addNote')}`,
    onTop: path => hooks.onTop?.(n, path),
    marked: f => threadsOn(v, f.path).length > 0,
    viewed: f => (prFile(v, f) && v.marks?.state(prFile(v, f))) || '',
    viewedAt: f => v.marks?.at(f) || 0,
    viewable: f => !!prFile(v, f),
    setViewed: (f, on) => !!prFile(v, f) && setViewed(root, n, v.res, prFile(v, f), on),
    banner: () => v.bar,
  };
}

// openChanges shows the Changes tab of pull request tab.n.
export async function openChanges(viewer, root, tab, keep, save) {
  const v = boxView(viewer, root, tab, keep, save, {});
  v.n = tab.n;
  v.label = `Changes of #${tab.n}`;
  v.from = '';
  Object.assign(v.src, source(v));
  v.reload = () => {}; // the forge diff does not follow the files on disk
  view = v;
  const known = cachedView(root, tab.n);
  if (known) await show(v, known);
  else viewer.replaceChildren(el('p', 'empty', `Fetching #${tab.n}…`));
  try {
    await loadView(root, tab.n);
  } catch (err) {
    if (isCurrent(v) && !v.scroll) noBoxes(v, err.message);
  }
}

onView((root, n, res) => {
  if (view && isCurrent(view) && view.root === root && view.n === n) show(view, res);
});

onViewed((root, n) => {
  if (!view || !isCurrent(view) || (root && (view.root !== root || view.n !== n)) || !view.res) return;
  view.marks = marksOf(view.root, view.n, view.res);
  // A mark that made every file viewed is a review: what shows may change.
  if (shownKey(view, shown(view.root, view.n, view.res)) !== view.shownKey) show(view, view.res);
  else marksChanged(view);
});

onSince((root, n) => {
  if (view && isCurrent(view) && view.root === root && view.n === n && view.res) show(view, view.res);
});

const shownKey = (v, sh) => [sh.from, sh.since?.on, sh.since?.answer ? 1 : 0, (sh.files || []).map(f => f.path + f.blobs).join()].join('\n');

async function show(v, res) {
  const before = v.res;
  v.res = res;
  v.marks = marksOf(v.root, v.n, res);
  const sh = shown(v.root, v.n, res);
  if (!sh.files) return; // kijk asks what changed since your review; onSince shows it
  const moved = before && (v.from !== sh.from || v.since !== !!sh.since?.on);
  v.since = !!sh.since?.on;
  v.shownKey = shownKey(v, sh);
  v.files = sh.files;
  v.from = sh.from;
  v.bar = sh.since?.answer ? sinceBar(v.root, v.n, sh.since, sh.files) : null;
  v.scroll?.setAttribute('aria-label', v.from ? `Changes of #${v.n} since your review` : v.label);
  v.src.onAdd = writable(res) ? (v2, path, l) => openEditor(v, path, l) : null;
  // The forge knows no line of the reviewed head.
  v.src.addOld = !v.from;
  // GitHub takes a comment only on a line of its own diff, which shows 3 lines around a change.
  v.src.addNear = res.forge?.kind === 'github' ? 3 : 0;
  if (!res.files.length) return noBoxes(v, res.error || 'No changes');
  if (!v.files.length) return noBoxes(v, 'No file of this pull request changed since your review');
  if (!v.scroll) v.goal = goals.get(`${v.root}\n${v.n}`) || '';
  goals.delete(`${v.root}\n${v.n}`);
  await showBoxes(v);
  if (moved && v.scroll && isCurrent(v)) v.scroll.scrollTop = 0;
  // A new thread or draft leaves the diff as it was, so its box would not draw again by itself.
  if (before && isCurrent(v)) {
    const on = (r, path) => JSON.stringify((r.threads || []).filter(t => t.path === path));
    const changed = v.files.map(f => f.path).filter(path => on(before, path) !== on(res, path));
    if (changed.length) redraw(v, changed);
  }
}

// openEditor opens the box for a new comment under row l of a file.
function openEditor(v, path, l) {
  closeEditor(v);
  const f = prFile(v, { path });
  if (!f) return;
  v.editor = lineEditor(v.root, v.n, v.res, f, l, () => closeEditor(v, true));
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
