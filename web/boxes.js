import { el, h, patch } from './api.js';
import { line, setView, isCurrent, LINE_HEIGHT } from './view.js';
import { lineCount, kind } from './git.js';
import { onKey, keyLabel } from './keys.js';

// The file boxes of Branch changes and of a pull request's Changes: one box per file, each drawn
// when it comes near the screen. A source says what the boxes show:
//   files()                the files, in order: {path, orig, status, add, del}
//   basis()                what every box depends on besides its own file
//   fetch(f, context)      the diff of one file
//   headParts(f)           extra parts of a box's header, after its line counts
//   openFile(f)            opens the whole file; without it there is no button
//   marks(res)             {line(l) → extra class, hunk(h) → a tag or null}, optional
//   decorate(v, b, rows)   {extra, under, end}: rows needed outside the hunks, the elements under
//                          a row by its key ('n:12' new side, 'o:7' old side), and those at the end
//   add                    the + on a line, or none: {on(v, path, row), tip, label, old, near}; row
//                          is {k, o, n, t}. label names it ("Add a note on line"); old gives removed
//                          lines one too; near gives an unchanged line one only that near a change
//   onTop(path)            the file at the top of the view changed
//   marked(f)              the file has threads or notes, for Alt+↓ and Alt+↑
//   viewed(f), setViewed(f, on)  the viewed mark: 'viewed', 'changed' since, or ''; optional
//   viewedAt(f)            changes with each mark or clear of f, so a fold made before goes
//   viewable(f)            false when f cannot be marked viewed; optional
//   banner()               an element above the boxes, or null; optional

const ICON = {
  plus: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M5 12h14"/><path d="M12 5v14"/></svg>',
  chev: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="m9 18 6-6-6-6"/></svg>',
  doc: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M15 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7Z"/><path d="M14 2v4a2 2 0 0 0 2 2h4"/><path d="M16 13H8"/><path d="M16 17H8"/><path d="M10 9H8"/></svg>',
};

const num = n => n.toLocaleString('en-US');
export const plural = (n, word) => `${num(n)} ${word}${n === 1 ? '' : 's'}`;

// Files whose diff nobody reads start folded.
const BIG = 2000;
const GENERATED = ['go.sum', 'package-lock.json', 'yarn.lock', 'pnpm-lock.yaml', 'Cargo.lock'];
const lines = f => (f.add || 0) + (f.del || 0);
const generated = path => GENERATED.includes(path.split('/').pop()) || path.endsWith('.min.js');
const heavy = f => generated(f.path) || lines(f) > BIG;
const HEAD = 34; // px, a box's sticky header
const AHEAD = 5 * LINE_HEIGHT + HEAD; // a jump puts its target this far below the top

export function iconButton(svg, label, act, props) {
  const onclick = e => {
    e.stopPropagation();
    act();
  };
  return h('button', { class: 'icon-btn', html: svg, data: { tip: label }, onclick, ...props, attrs: { 'aria-label': label, ...props?.attrs } });
}

// boxView makes the view of a tab of boxes. tab.folds keeps the boxes the user folded or opened;
// save stores the tab.
let last = null; // the box view made last, while it is the open view

export function boxView(viewer, root, tab, keep, save, src) {
  const v = { kind: 'boxes', viewer, root, tab, save, keep, src, boxes: new Map() };
  setView(v);
  last = v;
  v.jump = dir => {
    step(v, dir, b => b.starts);
    return true;
  };
  return v;
}

// showBoxes draws the boxes of the source's files, building the scroll the first time.
export async function showBoxes(v, change) {
  if (!v.scroll) return build(v);
  await updateBoxes(v, change);
}

async function build(v) {
  const scroll = el('div', 'allfiles');
  scroll.tabIndex = 0;
  scroll.setAttribute('aria-label', v.label || 'Changes');
  v.scroll = scroll;
  v.end = el('p', 'fend');
  patch(v.viewer, scroll);
  // Two screens ahead, so a box is drawn before it shows.
  v.near = new IntersectionObserver(entries => {
    for (const e of entries) {
      const b = e.target.box;
      b.near = e.isIntersecting;
      if (b.near && b.open && (!b.loaded || b.stale)) loadBox(v, b);
    }
  }, { root: scroll, rootMargin: '200% 0px' });
  await updateBoxes(v);
  if (v.keep?.scrollTop) scroll.scrollTop = v.keep.scrollTop;
  v.keep = null;
  if (v.goal) {
    goToBox(v, v.goal);
    v.goal = '';
  }
  let frame = 0;
  scroll.addEventListener('scroll', () => {
    cancelAnimationFrame(frame);
    frame = requestAnimationFrame(() => {
      if (scroll.scrollTop === v.placed) return;
      v.placed = null;
      const a = anchor(v);
      if (a) v.src.onTop?.(a.b.f.path);
    });
  });
  scroll.addEventListener('keydown', e => viewKey(v, e));
  scroll.addEventListener('click', e => {
    const row = e.target.closest('.ln');
    if (row && e.target.closest('.addc')) v.src.add?.on(v, row.closest('.fbox').dataset.path, rowOf(row));
    else if (row?.dataset.n && !e.target.closest('button')) setCur(v, row);
  });
}

// noBoxes replaces the boxes with a message.
export function noBoxes(v, text) {
  v.near?.disconnect();
  v.scroll = null;
  v.boxes = new Map();
  v.editor = null;
  patch(v.viewer, h('p', 'empty', text));
}

// anchor is the box at the top of the view and how far it is scrolled, so a redraw keeps it there.
function anchor(v) {
  const top = v.scroll.scrollTop;
  for (const b of v.boxes.values()) {
    if (b.el.offsetTop + b.el.offsetHeight > top + 12) return { b, offset: top - b.el.offsetTop };
  }
  return null;
}

function restore(v, a) {
  if (a?.b.el.isConnected) v.scroll.scrollTop = a.b.el.offsetTop + a.offset;
}

// updateBoxes follows the source's files. A changed box fetches its diff when it is near the
// screen; a new basis changes them all.
async function updateBoxes(v, change) {
  if (!v.scroll) return;
  const files = v.src.files();
  const a = anchor(v);
  const old = v.boxes;
  v.boxes = new Map();
  let moved = false;
  for (const f of files) {
    let b = old.get(f.path);
    if (b && (b.f.orig !== f.orig || b.f.status !== f.status)) b = null;
    if (b) b.f = f;
    else b = newBox(v, f);
    v.boxes.set(f.path, b);
    moved = followMark(v, b, false) || moved;
    drawHead(v, b);
  }
  for (const [path, b] of old) if (v.boxes.get(path) !== b) v.near.unobserve(b.el);
  const banner = v.src.banner?.();
  const els = [...(banner ? [banner] : []), ...[...v.boxes.values()].map(b => b.el)];
  v.end.textContent = files.length ? 'End of the changes' : 'No changes yet';
  if (els.length + 1 !== v.scroll.children.length || els.some((e, i) => v.scroll.children[i] !== e)) {
    patch(v.scroll, els, v.end);
    moved = true;
  }
  if (moved) restore(v, a);
  const basis = v.src.basis();
  const changed = b => b.basis !== basis || change?.all || change?.paths?.includes(b.f.path) || (b.f.orig && change?.paths?.includes(b.f.orig));
  const now = [];
  for (const b of v.boxes.values()) {
    if (!b.loaded || !changed(b)) continue;
    if (b.near && b.open) now.push(loadBox(v, b));
    else b.stale = true;
  }
  await Promise.all(now);
}

function newBox(v, f) {
  const b = { f, el: el('section', 'fbox'), head: el('div', 'fhead'), body: el('div', 'fbody'), loaded: false, context: 3, json: '', starts: [] };
  b.state = v.src.viewed?.(f) || '';
  b.at = v.src.viewedAt?.(f) || 0;
  b.open = keptOpen(v, b) ?? startsOpen(b);
  b.note = el('div', 'fnote');
  // Until it is drawn, the box is as high as its diff will about be, so the scrollbar is right.
  // The browser skips painting a box far from the screen, and then takes this height for it too.
  const height = Math.min(lines(f) + 8, 5000) * LINE_HEIGHT;
  b.body.style.height = `${height}px`;
  b.el.style.containIntrinsicSize = `auto ${height + HEAD}px`;
  b.el.append(b.head, b.note, b.body);
  b.el.dataset.path = f.path;
  b.el.box = b;
  showOpen(v, b);
  v.near.observe(b.el);
  return b;
}

// A viewed file starts folded, as do files whose diff nobody reads, unless they changed since viewed.
const startsOpen = b => b.state === 'changed' || !(b.state === 'viewed' || heavy(b.f));

// keptOpen is the user's own fold or open of a box, while its viewed mark is as it was then.
function keptOpen(v, b) {
  const k = v.tab.folds?.[b.f.path];
  return k?.state === b.state && (k.at || 0) === b.at ? k.open : undefined;
}

function showOpen(v, b) {
  b.el.classList.toggle('shut', !b.open);
  b.body.hidden = !b.open;
  b.note.hidden = b.open || b.state === 'viewed' || !heavy(b.f);
  if (!b.note.hidden) {
    patch(b.note, h('span', null, `${generated(b.f.path) ? 'Generated file' : 'Large diff'}, ${plural(lines(b.f), 'line')} · `), h('button', { class: 'more', onclick: () => fold(v, b, true) }, 'Show'));
  }
}

// followMark folds a box that became viewed and opens one that is not viewed any more, as a file
// that changed since. It keeps the view's place unless place is false, when the caller keeps it; a
// box folded across the top of the view scrolls to its header. True when the box folded or opened.
function followMark(v, b, place = true) {
  const state = v.src.viewed?.(b.f) || '';
  const at = v.src.viewedAt?.(b.f) || 0;
  if (state === b.state && at === b.at) return false;
  b.state = state;
  b.at = at;
  const open = keptOpen(v, b) ?? startsOpen(b);
  if (open === b.open) {
    showOpen(v, b);
    return false;
  }
  const a = place && anchor(v);
  b.open = open;
  showOpen(v, b);
  if (a?.b === b && !open) v.scroll.scrollTop = b.el.offsetTop - 12;
  else if (place) restore(v, a);
  if (open && (!b.loaded || b.stale)) loadBox(v, b);
  return true;
}

// marksChanged draws the boxes again after viewed marks changed.
export function marksChanged(v) {
  if (!v.scroll) return;
  for (const b of v.boxes.values()) {
    followMark(v, b);
    drawHead(v, b);
  }
}

function fold(v, b, open) {
  b.open = open;
  v.tab.folds = { ...v.tab.folds, [b.f.path]: { open, state: b.state, at: b.at } };
  v.save();
  drawHead(v, b);
  showOpen(v, b);
  if (open && !b.loaded) loadBox(v, b);
}

function drawHead(v, b) {
  const f = b.f;
  const slash = f.path.lastIndexOf('/');
  const name = f.path.slice(slash + 1);
  const extra = (v.src.headParts?.(f) || []).filter(Boolean);
  const np = p => p.classList?.contains('np');
  b.el.classList.toggle('viewed', b.state === 'viewed');
  const onchange = e => {
    if (!v.src.setViewed(b.f, e.currentTarget.checked)) drawHead(v, b); // refused: the box shows the mark again
    v.scroll.focus({ preventScroll: true }); // so V keeps working
  };
  patch(b.head,
    iconButton(ICON.chev, `${b.open ? 'Fold' : 'Open'} ${name}`, () => fold(v, b, !b.open), { key: 'fold', class: 'icon-btn fold', attrs: { 'aria-expanded': String(b.open) } }),
    h('span', `letter ${kind(f.status)}`, f.status),
    h('span', { class: 'fpath', title: f.orig ? `${f.orig} → ${f.path}` : f.path },
      h('span', 'muted', slash < 0 ? '' : f.path.slice(0, slash + 1)), h('b', f.status === 'D' ? 'deleted' : null, name)),
    extra.filter(np), lineCount(f.add, f.del), extra.filter(p => !np(p)), h('span', 'grow'),
    b.state === 'changed' && h('span', 'again', 'changed since you viewed it'),
    v.src.setViewed && v.src.viewable?.(f) !== false && h('label', { class: 'check', data: { tip: `Viewed\n${keyLabel('markViewed')}` } },
      h('input', { key: 'viewed', type: 'checkbox', checked: b.state === 'viewed', onchange }), ' Viewed'),
    f.status !== 'D' && v.src.openFile && iconButton(ICON.doc, 'Open the whole file', () => v.src.openFile(f), { key: 'open', data: { tip: v.src.openTip || 'Open the whole file' } }));
}

// redraw draws the headers of paths again, and their boxes when they are drawn.
export function redraw(v, paths) {
  for (const path of paths) {
    const b = v.boxes.get(path);
    if (!b) continue;
    drawHead(v, b);
    if (b.loaded) drawBox(v, b);
  }
}

// loadBox fetches a box's diff and draws it when it changed. A call during a fetch fetches again
// after it, so a change that came in the meantime is not lost.
async function loadBox(v, b) {
  if (b.loading) {
    b.again = true;
    return b.loading;
  }
  b.loading = (async () => {
    b.basis = v.src.basis();
    b.stale = false;
    let res;
    try {
      res = await v.src.fetch(b.f, b.context);
    } catch (err) {
      res = { error: err.message };
    }
    if (!isCurrent(v) || v.boxes.get(b.f.path) !== b) return;
    // The basis moved during the fetch, as when the switch moved before the box was first drawn.
    if (b.basis !== v.src.basis()) {
      b.again = true;
      return;
    }
    const json = b.basis + JSON.stringify(res); // the same diff on another branch has other notes
    if (b.loaded && json === b.json) return;
    b.json = json;
    b.res = res;
    await drawBox(v, b);
  })();
  try { await b.loading; } finally { b.loading = null; }
  if (b.again) {
    b.again = false;
    await loadBox(v, b);
  }
}

// oldLineOf is the old line of new line n, which is outside the hunks: n moved by the hunks above.
export function oldLineOf(hunks, n) {
  let delta = 0; // old line minus new line after the last hunk above
  for (const h of hunks) {
    const end = h.newStart + h.lines.filter(l => l.k !== '-').length;
    if (end > n) break;
    delta = h.oldStart + h.lines.filter(l => l.k !== '+').length - end;
  }
  return n + delta;
}

// hunksWith adds one-line hunks for lines outside the diff's hunks, in line order.
function hunksWith(hunks, extra) {
  const all = hunks.map(h => ({ ...h, lines: [...h.lines] }));
  for (const x of [...extra].sort((p, q) => p.n - q.n)) {
    const row = { ...x, o: oldLineOf(all, x.n) };
    const before = all.find(h => h.newStart + h.lines.filter(l => l.k !== '-').length === x.n);
    if (before) before.lines.push(row);
    else all.push({ oldStart: row.o, newStart: x.n, lines: [row] });
  }
  return all.sort((p, q) => p.newStart - q.newStart);
}

export async function drawBox(v, b) {
  const res = b.res;
  const draw = b.draws = (b.draws || 0) + 1; // a newer draw of the box wins over one still waiting
  b.loaded = true;
  b.body.style.height = '';
  const rows = res.hunks ? res.hunks.flatMap(h => h.lines) : [];
  const deco = (await v.src.decorate?.(v, b, rows)) || {};
  if (!isCurrent(v) || draw !== b.draws) return;
  const under = deco.under || new Map();
  if (res.error || res.binary || res.folder || !res.hunks.length) {
    const say = res.error || (res.binary ? 'Binary file changed' : res.folder ? 'Folder with its own git repository'
      : b.f.orig ? `Renamed from ${b.f.orig}, no content changes` : 'No content changes');
    patch(b.body, deco.top, h('p', 'fempty', say), deco.end, [...under.values()]);
    b.starts = [];
    return;
  }
  const add = v.src.add;
  const k = JSON.stringify([deco.extra, add && [add.old, add.near, add.label, add.tip]]);
  if (b.rows?.json !== b.json || b.rows.k !== k) b.rows = { json: b.json, k, ...diffRows(v, b, hunksWith(res.hunks, deco.extra || [])) };
  b.body.style.setProperty('--gutter', b.rows.gutter);
  const out = [deco.top];
  const placed = new Set();
  for (const r of b.rows.rows) {
    out.push(r.el);
    for (const key of r.keys) {
      if (placed.has(key) || !under.has(key)) continue;
      placed.add(key);
      out.push(under.get(key));
    }
  }
  // What has no line left, as on a deleted file, shows at the end of the box.
  for (const [key, els] of under) if (!placed.has(key)) out.push(els);
  patch(b.body, out, deco.end);
  b.starts = b.rows.starts;
}

// diffRows makes a box's rows. drawBox keeps them while the diff, the extra lines and the + stay,
// so most draws only move what is under them. A row's keys name what goes under it: 'n:12', 'o:7'.
function diffRows(v, b, hunks) {
  const marks = v.src.marks?.(b.res) || {};
  const add = v.src.add;
  let maxNo = 0;
  for (const l of hunks.flatMap(h => h.lines)) maxNo = Math.max(maxNo, l.o || 0, l.n || 0);
  const rows = [];
  const starts = [];
  hunks.forEach((hk, i) => {
    const count = k => hk.lines.filter(l => l.k !== k).length;
    const more = i > 0 || hk.newStart > 1 || hk.oldStart > 1;
    const onclick = () => {
      b.context += 20;
      loadBox(v, b);
    };
    // A description, so the focus stays on Show more lines when the rows are made again.
    rows.push({ keys: [], el: h('div', { key: `sep:${i}`, class: 'ln sep' }, h('span', 'gut'),
      h('span', 'tx', `@@ -${hk.oldStart},${count('+')} +${hk.newStart},${count('-')} @@`, marks.hunk?.(hk), more && h('button', { class: 'more', onclick }, 'Show more lines'))) });
    const changes = hk.lines.flatMap((l, j) => (l.k === ' ' ? [] : [j]));
    const near = j => !add.near || changes.some(c => Math.abs(c - j) <= add.near);
    hk.lines.forEach((l, j) => {
      const row = line([l.o, l.n], l.x || l.t, `${l.k === '+' ? 'add ch-add' : l.k === '-' ? 'del' : ''}${marks.line?.(l) || ''}`);
      if (l.k !== ' ' && (j === 0 || hk.lines[j - 1].k === ' ')) starts.push(row);
      row.dataset.k = l.k;
      row.dataset.t = l.t;
      if (l.o) row.dataset.o = l.o;
      if (l.k !== '-') row.dataset.n = l.n;
      if (add && (l.k !== '-' || add.old) && (l.k !== ' ' || near(j))) {
        const plus = el('button', 'addc'); // the scroll's click listener runs it
        plus.innerHTML = ICON.plus;
        plus.tabIndex = -1;
        plus.setAttribute('aria-label', `${add.label || 'Add a note on line'} ${l.k === '-' ? l.o : l.n}`);
        plus.dataset.tip = add.tip;
        row.insertBefore(plus, row.lastChild);
      }
      if (v.cur?.path === b.f.path && v.cur.n === l.n && l.k !== '-') row.classList.add('cur');
      rows.push({ el: row, keys: [l.k !== '-' && `n:${l.n}`, l.k !== '+' && `o:${l.o}`].filter(Boolean) });
    });
  });
  return { rows, starts, gutter: `${String(maxNo).length + 2}ch` };
}

const rowOf = row => ({ k: row.dataset.k, o: Number(row.dataset.o) || 0, n: Number(row.dataset.n) || 0, t: row.dataset.t });

// ----- Keys: the current line, and jumps across boxes -----

function setCur(v, row) {
  v.scroll.querySelector('.ln.cur')?.classList.remove('cur');
  row.classList.add('cur');
  v.cur = { path: row.closest('.fbox').dataset.path, n: Number(row.dataset.n) };
}

// curRow is the current line: the one last clicked or moved to, else the first changed line in view.
function curRow(v) {
  const cur = v.scroll.querySelector('.ln.cur');
  if (cur) return cur;
  const top = v.scroll.scrollTop + HEAD;
  const rows = [...v.scroll.querySelectorAll('.fbox:not(.shut) .ln[data-n]')];
  return rows.find(r => r.classList.contains('add') && offsetIn(v, r) >= top) || rows.find(r => offsetIn(v, r) >= top) || rows[0];
}

const offsetIn = (v, e) => e.getBoundingClientRect().top - v.scroll.getBoundingClientRect().top + v.scroll.scrollTop;

function viewKey(v, e) {
  if (e.target !== v.scroll || e.metaKey || e.ctrlKey || e.altKey || e.shiftKey) return;
  if (e.key !== 'ArrowDown' && e.key !== 'ArrowUp') return;
  const rows = [...v.scroll.querySelectorAll('.fbox:not(.shut) .ln[data-n]')];
  const cur = v.scroll.querySelector('.ln.cur');
  const at = rows.indexOf(cur);
  const next = cur ? rows[at + (e.key === 'ArrowDown' ? 1 : -1)] : curRow(v);
  if (!next) return;
  e.preventDefault();
  setCur(v, next);
  const y = offsetIn(v, next);
  if (y < v.scroll.scrollTop + HEAD + 12) v.scroll.scrollTop = y - HEAD - 12;
  else if (y + LINE_HEIGHT > v.scroll.scrollTop + v.scroll.clientHeight) v.scroll.scrollTop = y + LINE_HEIGHT - v.scroll.clientHeight;
}

// step jumps to the next (dir 1) or previous (dir -1) element that pick(box) gives, across open
// boxes, drawing a box on the way when it is not drawn yet. has(box) skips a box without drawing it.
async function step(v, dir, pick, has = () => true) {
  if (!v.scroll) return;
  const at = v.scroll.scrollTop + AHEAD;
  const boxes = [...v.boxes.values()].filter(b => b.open);
  for (const b of dir > 0 ? boxes : boxes.reverse()) {
    const top = b.el.offsetTop;
    if (dir > 0 ? top + b.el.offsetHeight < at : top > at) continue;
    if (!b.loaded) {
      if (!has(b)) continue;
      await loadBox(v, b);
      if (!isCurrent(v)) return;
    }
    const ys = pick(b).map(e => offsetIn(v, e));
    const y = dir > 0 ? ys.find(y => y > at + 1) : ys.findLast(y => y < at - 1);
    if (y != null) {
      v.scroll.scrollTop = Math.max(0, y - AHEAD);
      return;
    }
  }
}

// Alt+↓ / Alt+↑: the next or previous thread or note.
const markKey = dir => () => {
  const v = last && isCurrent(last) ? last : null;
  if (!v?.scroll || document.activeElement?.closest('textarea, input')) return false;
  step(v, dir, b => [...b.body.querySelectorAll('.thr')], b => !!v.src.marked?.(b.f));
};
onKey('nextNote', markKey(1));
onKey('prevNote', markKey(-1));

// The note key opens the view's editor on the current line: an agent note, or a comment.
onKey('addNote', () => {
  const v = last && isCurrent(last) ? last : null;
  if (!v?.scroll || !v.src.add || document.activeElement?.closest('textarea, input')) return false;
  const row = curRow(v);
  if (!row?.querySelector('.addc')) return; // a line the forge takes no comment on
  setCur(v, row);
  v.src.add.on(v, row.closest('.fbox').dataset.path, rowOf(row));
});

// markCurrent is the key V in the open box view: it marks the current file viewed and goes to the
// next file not viewed, or clears the mark of a viewed file. False when the view does not have it.
export function markCurrent() {
  const v = last && isCurrent(last) ? last : null;
  if (!v?.scroll || !v.src.setViewed || !v.scroll.contains(document.activeElement) || document.activeElement.closest('textarea, input')) return false;
  const b = currentBox(v);
  if (!b) return false;
  const on = b.state !== 'viewed';
  if (!v.src.setViewed(b.f, on)) return false;
  if (!on) return;
  const boxes = [...v.boxes.values()];
  const next = boxes.slice(boxes.indexOf(b) + 1).find(x => x.state !== 'viewed');
  v.scroll.querySelector('.ln.cur')?.classList.remove('cur');
  v.cur = next ? { path: next.f.path, n: 0 } : null;
  if (next) goToBox(v, next.f.path);
}

// currentBox is the box of the current line while it shows, else the box at the top of the view.
function currentBox(v) {
  const b = v.cur && v.boxes.get(v.cur.path);
  if (b) {
    const r = b.el.getBoundingClientRect(), s = v.scroll.getBoundingClientRect();
    if (r.bottom > s.top + HEAD && r.top < s.bottom) return b;
  }
  return anchor(v)?.b;
}

// goToBox scrolls to a file's box. Near the end the view can stop short of it, with another file at
// the top: the box's file stays the one at the top until the view moves.
export function goToBox(v, path, focus) {
  const b = v.boxes.get(path);
  if (b) {
    v.scroll.scrollTop = b.el.offsetTop - 12;
    v.placed = v.scroll.scrollTop;
    v.src.onTop?.(path);
  }
  if (focus) v.scroll.focus({ preventScroll: true });
}
