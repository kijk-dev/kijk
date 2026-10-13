import { get, el } from './api.js';

export const LINE_HEIGHT = 18;
const CHUNK = 500;
const FULL = 1000000; // diff context that returns the whole file
let current = null; // the open file or diff; a newer open makes older fetches stale
let viewChanged = () => {};

// onView runs after a file or diff is drawn, also after a reload, so find can search it again.
export function onView(fn) { viewChanged = fn; }

export function showMessage(viewer, text) {
  current = null;
  viewer.replaceChildren(el('p', 'empty', text));
}

// virtual renders only the rows on screen; drawRange(first, last) returns their elements.
// scroll must be in the page already: the first rows are drawn at once, at scrollTop top,
// so a view that replaces another never shows an empty frame.
function virtual(scroll, count, width, top, drawRange) {
  const space = el('div', 'space');
  const rows = el('div', 'rows');
  space.style.height = `${count * LINE_HEIGHT}px`;
  space.style.width = width;
  space.append(rows);
  scroll.append(space);
  const draw = () => {
    const first = Math.max(0, Math.floor(scroll.scrollTop / LINE_HEIGHT) - 20);
    const last = Math.min(count, first + 400, Math.ceil((scroll.scrollTop + scroll.clientHeight) / LINE_HEIGHT) + 20);
    rows.style.transform = `translateY(${first * LINE_HEIGHT}px)`;
    rows.replaceChildren(...drawRange(first, last));
  };
  let frame = 0;
  const render = () => {
    cancelAnimationFrame(frame);
    frame = requestAnimationFrame(draw);
  };
  scroll.addEventListener('scroll', render);
  new ResizeObserver(render).observe(scroll);
  scroll.scrollTop = top;
  draw();
  return render;
}

// content is plain text, or colored pieces [class, text] from the server. marks are find matches.
function line(numbers, content, cls, key) {
  const row = el('div', cls ? `ln ${cls}` : 'ln');
  const flash = current?.flash;
  if (flash?.key === key && Date.now() < flash.until) row.classList.add('flash');
  const gut = el('span', 'gut');
  for (const n of numbers) gut.append(el('span', null, n || ''));
  const tx = el('span', 'tx');
  const pieces = Array.isArray(content) ? content : [['', content]];
  const marks = current?.hits?.get(key) || [];
  let pos = 0, m = 0;
  for (const [c, text] of pieces) {
    for (let i = 0; i < text.length;) {
      while (m < marks.length && marks[m][1] <= pos + i) m++;
      const mark = marks[m];
      const inside = mark && mark[0] <= pos + i;
      const end = Math.min(text.length, (inside ? mark[1] : mark ? mark[0] : Infinity) - pos);
      const part = c ? el('span', `h${c}`, text.slice(i, end)) : document.createTextNode(text.slice(i, end));
      if (inside) {
        const hit = el('mark', mark[2] ? 'hit current' : 'hit');
        hit.append(part);
        tx.append(hit);
      } else {
        tx.append(part);
      }
      i = end;
    }
    pos += text.length;
  }
  row.append(gut, tx);
  return row;
}

// viewInfo tells find and go to line what is open: a file, a diff with its rows, or rendered markdown.
export function viewInfo() {
  if (!current?.scroll) return null;
  return current;
}

// setHits shows find matches on the open view: line number (file) or row index (diff) ->
// [[from, to, isCurrent]]. null clears them.
export function setHits(map) {
  if (!current) return;
  current.hits = map;
  current.render?.();
}

// reveal scrolls a line (file) or row (diff) into view unless it is in view already, and flashes it.
export function reveal(key, flashIt) {
  if (!current?.scroll || current.kind === 'rendered') return;
  const { scroll } = current;
  const i = current.kind === 'file' ? key - 1 : key;
  const top = i * LINE_HEIGHT;
  if (top < scroll.scrollTop + LINE_HEIGHT || top > scroll.scrollTop + scroll.clientHeight - 2 * LINE_HEIGHT) {
    scroll.scrollTop = Math.max(0, top - scroll.clientHeight / 3);
  }
  if (flashIt) current.flash = { key, until: Date.now() + 1200 };
  current.render?.();
}

// gutterMarks gives each line's classes for changes since HEAD: a bar on added or changed
// lines, and a wedge on the border where lines were removed.
function gutterMarks(changes, lines) {
  const marks = [];
  const mark = (n, cls) => { if (n >= 0 && n < lines) marks[n] = marks[n] ? `${marks[n]} ${cls}` : cls; };
  for (const c of changes) {
    if (c.kind !== 'del') for (let n = c.start - 1; n < c.start - 1 + c.count; n++) mark(n, `ch-${c.kind}`);
    else if (c.start < lines) mark(c.start, 'del-above');
    else mark(lines - 1, 'del-below');
  }
  return marks;
}

// ruler shows every change as a tick beside the scrollbar, placed by its share of the file.
function ruler(changes, lines) {
  const r = el('div', 'ruler');
  r.setAttribute('aria-hidden', 'true');
  for (const c of changes) {
    const m = el('div', `mark ${c.kind}`);
    m.style.top = `${((c.kind === 'del' ? c.start : c.start - 1) / lines) * 100}%`;
    m.style.height = `${(c.count / lines) * 100}%`;
    r.append(m);
  }
  return r;
}

// keep is set when an open view comes back or reloads: {scrollTop, info}, info being fetched already,
// or {line} after switching between rendered and source. md holds a markdown tab's view: {source, toggle, open}.
export async function openFile(viewer, root, path, keep, md) {
  const file = { kind: 'file', root, path, chunks: new Map(), pending: new Set() };
  // Agents touch .git all the time; a reload that changes nothing must not redraw.
  file.reload = async () => {
    const info = await get('file', { root, path }).catch(() => null);
    if (current !== file || (info && JSON.stringify(info) === file.json)) return;
    openFile(viewer, root, path, { scrollTop: file.scroll?.scrollTop ?? 0, info }, md);
  };
  current = file;
  let info;
  try {
    info = keep?.info || await get('file', { root, path });
  } catch (err) {
    if (current === file) viewer.replaceChildren(el('p', 'empty', keep && err.message === 'Not found' ? 'Deleted' : err.message));
    return;
  }
  if (current !== file) return;
  file.json = JSON.stringify(info);
  if (info.tooLarge) return viewer.replaceChildren(el('p', 'empty', 'File too large'));
  if (info.binary) return viewer.replaceChildren(el('p', 'empty', `Binary file, ${size(info.size)}`));
  const bar = md && /\.(md|markdown)$/i.test(path) ? modeBar(file, md) : null;
  if (bar && !md.source) return openRendered(viewer, file, info, bar, keep, md);

  const scrollTop = keep?.scrollTop ?? (keep?.line ? (keep.line - 1) * LINE_HEIGHT : 0);
  if (keep) { // load the visible lines first, so a reload does not flash an empty view
    const top = Math.floor(scrollTop / LINE_HEIGHT);
    for (const c of new Set([Math.floor(top / CHUNK), Math.floor((top + 100) / CHUNK)])) {
      if (c * CHUNK >= info.lines) continue;
      try {
        file.chunks.set(c, await get('lines', { root, path, from: c * CHUNK, to: (c + 1) * CHUNK }));
      } catch { /* the view loads it again when drawn */ }
    }
    if (current !== file) return;
  }
  const scroll = el('div', 'scroll');
  const gutter = `${String(info.lines).length + 2}ch`;
  scroll.style.setProperty('--gutter', gutter);
  const marks = gutterMarks(info.changes, info.lines);
  const box = bar ? el('div', 'pane') : viewer;
  if (bar) viewer.replaceChildren(bar, box);
  box.replaceChildren(scroll);
  if (info.changes.length && info.lines) box.append(ruler(info.changes, info.lines));
  file.scroll = scroll;
  file.topLine = () => Math.floor(scroll.scrollTop / LINE_HEIGHT) + 1;
  file.lines = info.lines;
  file.gotoLine = n => reveal(Math.min(Math.max(1, n), info.lines), true);
  file.starts = info.changes.map(c => (c.kind === 'del' ? c.start : c.start - 1));
  file.render = virtual(scroll, info.lines, `calc(${gutter} + ${info.maxCols + 4}ch)`, scrollTop, (first, last) => {
    for (let c = Math.floor(first / CHUNK); c * CHUNK < last; c++) load(file, c);
    const out = [];
    for (let n = first; n < last; n++) {
      out.push(line([n + 1], file.chunks.get(Math.floor(n / CHUNK))?.[n % CHUNK] ?? '', marks[n], n + 1));
    }
    return out;
  });
  viewChanged();
}

function modeBar(file, md) {
  const bar = el('div', 'diffbar');
  const group = el('div', 'mode');
  group.setAttribute('role', 'group');
  group.setAttribute('aria-label', 'View');
  for (const [source, label] of [[false, 'Rendered'], [true, 'Source']]) {
    const b = el('button', null, label);
    b.setAttribute('aria-pressed', md.source === source);
    b.onclick = () => { if (md.source !== source) md.toggle(source, file.topLine?.() ?? 1); };
    group.append(b);
  }
  bar.append(group);
  return bar;
}

async function openRendered(viewer, file, info, bar, keep, md) {
  let res;
  try {
    res = await get('markdown', { root: file.root, path: file.path });
  } catch (err) {
    if (current === file) viewer.replaceChildren(bar, el('p', 'empty', err.message));
    return;
  }
  if (current !== file) return;
  const t = document.createElement('template'); // inert: nothing loads before prepare runs
  t.innerHTML = res.html; // the server drops HTML inside the file, and the CSP blocks script as well
  const doc = el('article', 'md');
  doc.append(prepare(t.content, file, md, doc));
  const gutter = el('div', 'md-marks');
  gutter.setAttribute('aria-hidden', 'true');
  doc.append(gutter);
  const scroll = el('div', 'scroll');
  scroll.append(doc);
  const pane = el('div', 'pane');
  pane.append(scroll);
  viewer.replaceChildren(bar, pane);
  file.scroll = scroll;

  const blocks = [...doc.querySelectorAll('[data-lines]')].map(b => {
    const [first, last] = b.dataset.lines.split('-').map(Number);
    return { b, first, last };
  });
  const offset = b => b.getBoundingClientRect().top - doc.getBoundingClientRect().top;
  file.topLine = () => blocks.find(x => offset(x.b) + x.b.offsetHeight > scroll.scrollTop)?.first ?? 1;
  file.kind = 'rendered';
  file.lines = info.lines;
  file.gotoLine = n => {
    const hit = blocks.filter(x => x.first <= n).at(-1);
    if (hit) scroll.scrollTop = offset(hit.b) - 8;
  };

  const changed = changedBlocks(blocks, info.changes);
  const rule = el('div', 'ruler');
  rule.setAttribute('aria-hidden', 'true');
  if (changed.length) pane.append(rule);
  const draw = () => {
    const height = doc.offsetHeight || 1;
    gutter.replaceChildren();
    rule.replaceChildren();
    for (const { b, inner, kind } of changed) {
      const top = offset(b);
      const h = (inner ? offset(inner) : top + b.offsetHeight) - top;
      const bar = el('div', `mark ${kind}`);
      bar.style.top = `${top}px`;
      bar.style.height = `${h}px`;
      const tick = el('div', `mark ${kind}`);
      tick.style.top = `${(top / height) * 100}%`;
      tick.style.height = `${(h / height) * 100}%`;
      gutter.append(bar);
      rule.append(tick);
    }
  };
  draw();
  new ResizeObserver(draw).observe(doc);

  if (keep?.scrollTop != null) scroll.scrollTop = keep.scrollTop;
  else if (keep?.line) {
    const hit = blocks.filter(x => x.first <= keep.line && keep.line <= x.last).at(-1);
    if (hit) scroll.scrollTop = offset(hit.b) - 8;
  }
  viewChanged();
}

// changedBlocks finds the blocks that hold changed lines; removed lines count for the line above them.
// A list item's own lines stop where its nested list starts, and so does its bar.
function changedBlocks(blocks, changes) {
  const kinds = [];
  for (const c of changes) {
    if (c.kind === 'del') kinds[Math.max(1, c.start)] ??= 'del';
    else for (let n = c.start; n < c.start + c.count; n++) kinds[n] = c.kind;
  }
  const out = [];
  blocks.forEach((x, i) => {
    const inner = blocks.slice(i + 1).find(y => x.b.contains(y.b));
    const last = inner ? inner.first - 1 : x.last;
    let kind = '';
    for (let n = x.first; n <= last; n++) if (kinds[n]) kind = !kind || kind === kinds[n] ? kinds[n] : 'mod';
    if (kind) out.push({ b: x.b, inner: inner?.b, kind });
  });
  return out;
}

const external = url => /^([a-z][a-z0-9+.-]*:|\/\/)/i.test(url);
const slug = s => s.trim().toLowerCase().replace(/[^\p{L}\p{N}\s_-]/gu, '').replace(/\s/g, '-');

// What renderMarkdown makes. The HTML of a remote folder comes from that machine's kijk, so the
// page keeps nothing else.
const mdTags = new Set(['A', 'BLOCKQUOTE', 'BR', 'CODE', 'DEL', 'EM', 'H1', 'H2', 'H3', 'H4', 'H5', 'H6', 'HR',
  'IMG', 'INPUT', 'LI', 'OL', 'P', 'PRE', 'SPAN', 'STRONG', 'TABLE', 'TBODY', 'TD', 'TH', 'THEAD', 'TR', 'UL']);
const mdAttrs = new Set(['href', 'src', 'alt', 'title', 'class', 'data-lines', 'align', 'type', 'checked', 'disabled', 'start']);
// Browsers skip control characters in a URL, so java\tscript: is still a scheme.
const safeURL = url => {
  const u = url.replace(/[\x00-\x20]/g, '');
  return !/^[a-z][a-z0-9+.-]*:/i.test(u) || /^(https?:|mailto:|data:image\/(png|gif|jpeg|webp);)/i.test(u);
};

function clean(frag) {
  for (const n of [...frag.querySelectorAll('*')]) {
    if (!mdTags.has(n.tagName)) {
      n.remove();
      continue;
    }
    for (const { name, value } of [...n.attributes]) {
      if (!mdAttrs.has(name) || ((name === 'href' || name === 'src') && !safeURL(value))) n.removeAttribute(name);
    }
    if (n.tagName === 'INPUT') n.disabled = true;
  }
}

// prepare opens web links in a new tab and links to files in a kijk tab.
function prepare(frag, file, md, doc) {
  clean(frag);
  for (const img of frag.querySelectorAll('img')) {
    if (!external(img.getAttribute('src') || '')) {
      img.replaceWith(el('span', 'md-img', `Image: ${img.getAttribute('alt') || img.getAttribute('src')}`));
    }
  }
  for (const a of frag.querySelectorAll('a[href]')) {
    const href = a.getAttribute('href');
    if (external(href)) {
      a.target = '_blank';
      a.rel = 'noopener noreferrer';
      continue;
    }
    a.title = href.startsWith('#') ? '' : `Open ${href}`;
    a.onclick = e => {
      e.preventDefault();
      follow(href, file, md, doc);
    };
  }
  for (const table of frag.querySelectorAll('table')) {
    const wrap = el('div', 'md-table');
    table.replaceWith(wrap);
    wrap.append(table);
  }
  return frag;
}

function follow(href, file, md, doc) {
  let target;
  try {
    target = decodeURIComponent(href.split(/[?#]/)[0]);
  } catch {
    return;
  }
  if (!target) {
    const id = slug(decodeURIComponent(href.slice(1)));
    [...doc.querySelectorAll('h1, h2, h3, h4, h5, h6')].find(h => slug(h.textContent) === id)?.scrollIntoView();
    return;
  }
  const parts = target.startsWith('/') ? [] : file.path.split('/').slice(0, -1);
  for (const s of target.split('/')) {
    if (s === '..') {
      if (!parts.length) return;
      parts.pop();
    } else if (s && s !== '.') {
      parts.push(s);
    }
  }
  md.open(parts.join('/'));
}

async function load(file, c) {
  if (file.chunks.has(c) || file.pending.has(c)) return;
  file.pending.add(c);
  try {
    const lines = await get('lines', { root: file.root, path: file.path, from: c * CHUNK, to: (c + 1) * CHUNK });
    file.chunks.set(c, lines);
    if (current === file) file.render();
  } finally {
    file.pending.delete(c);
  }
}

// openDiff shows one file's change; mode is "staged", "unstaged", "untracked" or "commit".
// keep is set when the diff reloads: {scrollTop, res}, res being fetched already.
export async function openDiff(viewer, root, change, mode, context = 3, keep) {
  const d = { kind: 'diff', starts: [], path: change.path, mode, hash: change.hash || '' };
  const params = { root, path: change.path, orig: change.orig || '', mode, hash: change.hash || '', context };
  d.reload = async () => {
    const res = await get('diff', params).catch(() => null);
    if (current !== d || (res && JSON.stringify(res) === d.json)) return;
    openDiff(viewer, root, change, mode, context, { scrollTop: d.scroll?.scrollTop ?? 0, res });
  };
  current = d;
  let res;
  try {
    res = keep?.res || await get('diff', params);
  } catch (err) {
    if (current === d) viewer.replaceChildren(el('p', 'empty', err.message));
    return;
  }
  if (current !== d) return;
  d.json = JSON.stringify(res);

  if (res.folder) return viewer.replaceChildren(el('p', 'empty', 'Untracked folder with its own git repository'));
  if (res.binary) return viewer.replaceChildren(el('p', 'empty', 'Binary file changed'));
  if (!res.hunks.length) return viewer.replaceChildren(el('p', 'empty', change.orig ? `Renamed from ${change.orig}, no content changes` : 'No content changes'));

  // The tab names the file; this bar keeps what the tab cannot show.
  const full = context >= FULL;
  const bar = el('div', 'diffbar', change.orig ? `Renamed from ${change.orig}` : '');
  const toggle = el('button', 'tool', full ? 'Show changes only' : 'Show full file');
  toggle.onclick = () => openDiff(viewer, root, change, mode, full ? 3 : FULL);
  bar.append(toggle);

  const rows = [];
  const more = () => openDiff(viewer, root, change, mode, context + 20);
  res.hunks.forEach((h, i) => {
    if (!full && (i > 0 || h.oldStart > 1 || h.newStart > 1)) rows.push({ sep: true });
    for (const l of h.lines) {
      if (l.k !== ' ' && (rows.length === 0 || rows.at(-1).sep || rows.at(-1).k === ' ')) d.starts.push(rows.length);
      rows.push(l);
    }
  });

  let maxNo = 0, cols = 0;
  for (const r of rows) {
    maxNo = Math.max(maxNo, r.o || 0, r.n || 0);
    cols = Math.max(cols, r.t?.length ?? 0);
  }
  const digits = String(maxNo).length;
  const scroll = el('div', 'scroll');
  const gutter = `${digits + 2}ch`;
  scroll.style.setProperty('--gutter', gutter);
  viewer.replaceChildren(bar, scroll);
  d.scroll = scroll;
  const top = keep ? keep.scrollTop : Math.max(0, ((d.starts[0] ?? 0) - 5) * LINE_HEIGHT);
  d.rows = rows;
  d.lines = maxNo;
  d.gotoLine = n => { // the line number on the new side, else the old side
    let i = rows.findIndex(r => r.n >= n);
    if (i < 0) i = rows.findIndex(r => r.o >= n);
    if (i >= 0) reveal(i, true);
  };
  d.render = virtual(scroll, rows.length, `calc(${gutter} * 2 + ${cols + 6}ch)`, top, (first, last) => {
    const out = [];
    for (let i = first; i < last; i++) {
      const r = rows[i];
      if (r.sep) {
        const sep = el('div', 'ln sep');
        const btn = el('button', 'more', '⋯ Show more lines');
        btn.onclick = more;
        sep.append(btn);
        out.push(sep);
      } else {
        out.push(line([r.o, r.n], r.x || r.t, r.k === '+' ? 'add' : r.k === '-' ? 'del' : '', i));
      }
    }
    return out;
  });
  viewChanged();
}

// viewScroll is the scroll position of the open file or diff, once it is drawn.
export const viewScroll = () => current?.scroll?.scrollTop;

export function setViewScroll(top) {
  if (!current?.scroll) return;
  current.scroll.scrollTop = top;
  current.render?.();
}

// refreshView reloads the open file when it or git changed (its change marks), and the open diff on any change.
export function refreshView(change) {
  if (!current?.reload) return;
  if (current.kind === 'file' && !change.all && !change.git && !change.paths.includes(current.path)) return;
  current.reload();
}

// jumpChange is F7 / Shift+F7: the next or previous change of a diff, or of a file since HEAD.
export function jumpChange(dir) {
  if (!current?.scroll || !current.starts) return false;
  const { scroll, starts } = current;
  const at = Math.floor(scroll.scrollTop / LINE_HEIGHT) + 5;
  const target = dir < 0 ? starts.findLast(s => s < at) : starts.find(s => s > at);
  if (target != null) scroll.scrollTop = Math.max(0, (target - 5) * LINE_HEIGHT);
  return true;
}

function size(n) {
  if (n < 1024) return `${n} bytes`;
  if (n < 1024 * 1024) return `${Math.round(n / 1024)} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}
