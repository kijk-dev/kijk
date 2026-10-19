import { get, url, el } from './api.js';

export const LINE_HEIGHT = 18;
// The same types as imageTypes in image.go.
const IMAGE = /\.(png|jpe?g|gif|webp|svg|ico|bmp|avif)$/i;
const SVG = /\.svg$/i;
// The same types as diagramKinds in diagram.go.
const DIAGRAM = /\.(mmd|mermaid)$/i;
const RENDERED = /\.(md|markdown|svg|mmd|mermaid)$/i;
const CHUNK = 500;
const FULL = 1000000; // diff context that returns the whole file
let current = null; // the open file or diff; a newer open makes older fetches stale
let viewChanged = () => {};

// onView runs after a file or diff is drawn, also after a reload, so find can search it again.
export function onView(fn) { viewChanged = fn; }

export function showMessage(viewer, msg) {
  current = null;
  viewer.replaceChildren(typeof msg === 'string' ? el('p', 'empty', msg) : msg);
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

// keep is set when an open view comes back or reloads: {scrollTop, info, image}, info being fetched
// already and image a picture's zoom, or {line} after switching between rendered and source.
// md holds the view of a tab that has a rendered view: {source, toggle, open}.
export async function openFile(viewer, root, path, keep, md) {
  const file = { kind: 'file', root, path, chunks: new Map(), pending: new Set() };
  // Image URLs in rendered markdown carry a version: new on each open, so the page never reuses an
  // older copy, and kept across a redraw, so an image that did not change is not fetched again.
  file.versions = keep?.versions || { at: Date.now(), of: new Map() };
  // Agents touch .git all the time; a reload that changes nothing must not redraw.
  file.reload = async () => {
    const info = await get('file', { root, path }).catch(() => null);
    if (current !== file || (info && JSON.stringify(info) === file.json)) return;
    openFile(viewer, root, path, { scrollTop: file.scroll?.scrollTop ?? 0, info, image: file.image?.(), versions: file.versions }, md);
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
  let bar = md && RENDERED.test(path) ? modeBar(file, md) : null;
  const say = text => viewer.replaceChildren(...(bar ? [bar] : []), el('p', 'empty', text));
  if (IMAGE.test(path) && !(bar && md.source)) return openImage(viewer, file, info, bar, keep, say);
  if (info.tooLarge) return say('File too large');
  if (info.binary) return say(`Binary file, ${size(info.size)}`);
  if (bar && !md.source) {
    if (await openRendered(viewer, file, info, bar, keep, md) !== 'too large') return;
    bar = modeBar(file, { ...md, source: true }); // Rendered stays off: there is nothing to draw
    bar.prepend(el('span', 'facts', 'Diagram too large to draw'));
  }
  if (bar && SVG.test(path)) picture(imageURL(file, info), true).then(img => img && bar.prepend(facts(img, info.size)));

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

function modeGroup(label, options, value, pick) {
  const group = el('div', 'mode');
  group.setAttribute('role', 'group');
  group.setAttribute('aria-label', label);
  for (const [v, text] of options) {
    const b = el('button', null, text);
    b.setAttribute('aria-pressed', v === value);
    b.onclick = () => pick(v);
    group.append(b);
  }
  return group;
}

// sourceSwitch is the Rendered / Source switch of markdown, SVG images, diagram files and their diffs.
function sourceSwitch(md, line) {
  return modeGroup('View', [[false, 'Rendered'], [true, 'Source']], md.source, source => {
    if (md.source !== source) md.toggle(source, line());
  });
}

function modeBar(file, md) {
  const bar = el('div', 'diffbar');
  bar.append(sourceSwitch(md, () => file.topLine?.() ?? 1));
  return bar;
}

const imageURL = (file, info) => url('image', { root: file.root, path: file.path, v: info.mtime });

// picture loads an image before it is shown, so a reload does not flash an empty pane. It is
// null when the browser cannot show the image.
async function picture(src, svg) {
  const img = el('img', 'pic checker');
  img.alt = '';
  img.src = src;
  try {
    const [, noSize] = await Promise.all([img.decode(), svg && sizeless(src)]);
    if (noSize) img.classList.add('sizeless');
    return img;
  } catch {
    return null;
  }
}

// sizeless tells whether an SVG has neither a width nor a height. Such an SVG has no size of its
// own to keep, so it fills the pane.
async function sizeless(src) {
  try {
    const tag = /<svg\b[^>]*>/i.exec(await (await fetch(src)).text())?.[0] ?? '';
    const has = name => new RegExp(`\\s${name}\\s*=\\s*["']\\s*[\\d.]+\\s*[a-z]*\\s*["']`, 'i').test(tag);
    return !has('width') && !has('height');
  } catch {
    return false;
  }
}

function facts(img, bytes) {
  const f = el('span', 'facts');
  if (!img.classList.contains('sizeless')) f.append(`${img.naturalWidth} × ${img.naturalHeight}`, el('span', 'sep', '·'));
  f.append(size(bytes));
  return f;
}

// openImage fits the picture to the pane and never enlarges it. While it is shrunk, the Fit /
// 100% switch and a click show it at 100%. Find and go to line have nothing to do here.
async function openImage(viewer, file, info, bar, keep, say) {
  if (info.tooLarge) return say(`Image too large, ${size(info.size)}`);
  if (info.lfs) return say(`Stored in Git LFS, ${size(info.lfs)}`);
  const img = await picture(imageURL(file, info), SVG.test(file.path));
  if (current !== file) return;
  if (!img) return say('Cannot show this image');
  img.alt = file.path.split('/').pop();
  bar ??= el('div', 'diffbar');
  bar.classList.add('imgbar');
  const view = el('div', 'imgview');
  view.append(img);
  let actual = !!keep?.image?.actual;
  const zoom = modeGroup('Zoom', [[false, 'Fit'], [true, '100%']], actual, v => { actual = v; update(); });
  bar.prepend(facts(img, info.size), zoom);
  const shrunk = () => !img.classList.contains('sizeless') && img.naturalWidth > view.clientWidth - 2 * parseFloat(getComputedStyle(view).paddingLeft);
  const update = () => {
    zoom.hidden = !shrunk() && !actual;
    view.classList.toggle('actual', actual);
    img.classList.toggle('zoomable', !zoom.hidden);
    for (const [i, b] of [...zoom.children].entries()) b.setAttribute('aria-pressed', (i === 1) === actual);
  };
  img.onclick = () => {
    if (zoom.hidden) return;
    actual = !actual;
    update();
  };
  viewer.replaceChildren(bar, view);
  update();
  new ResizeObserver(update).observe(view);
  if (keep?.image) [view.scrollTop, view.scrollLeft] = [keep.image.top, keep.image.left];
  file.image = () => ({ actual, top: view.scrollTop, left: view.scrollLeft });
  viewChanged();
}

// openRendered returns 'too large' for a diagram file too large to draw, which shows as source.
async function openRendered(viewer, file, info, bar, keep, md) {
  let res;
  try {
    res = await get('markdown', { root: file.root, path: file.path });
  } catch (err) {
    if (current === file && DIAGRAM.test(file.path) && err.message === 'Diagram too large to draw') return 'too large';
    if (current === file) viewer.replaceChildren(bar, el('p', 'empty', err.message));
    return;
  }
  if (current !== file) return;
  const t = document.createElement('template'); // inert: nothing loads before prepare runs
  t.innerHTML = res.html; // the server drops HTML inside the file, and the CSP blocks script as well
  const doc = el('article', 'md');
  doc.append(prepare(t.content, file, md, doc));
  await settle(drawDiagrams(file));
  if (current !== file) return;
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

  if (DIAGRAM.test(file.path)) diagramZoom(file, doc, scroll, bar, keep);
  if (keep?.scrollTop != null) scroll.scrollTop = keep.scrollTop;
  else if (keep?.line) {
    const hit = blocks.filter(x => x.first <= keep.line && keep.line <= x.last).at(-1);
    if (hit) scroll.scrollTop = offset(hit.b) - 8;
  }
  viewChanged();
}

// diagramZoom fits a diagram file to the pane and never enlarges it. While it is shrunk, the
// Fit / 100% switch and a click show it at 100%, as in the image view.
function diagramZoom(file, doc, scroll, bar, keep) {
  doc.classList.add('diagram-file');
  let actual = !!keep?.image?.actual;
  const zoom = modeGroup('Zoom', [[false, 'Fit'], [true, '100%']], actual, v => { actual = v; update(); });
  bar.classList.add('imgbar');
  bar.prepend(zoom);
  const update = () => {
    const img = doc.querySelector('.diagram img');
    zoom.hidden = !img || (!actual && img.naturalWidth <= img.clientWidth);
    doc.classList.toggle('actual', actual);
    img?.classList.toggle('zoomable', !zoom.hidden);
    for (const [i, b] of [...zoom.children].entries()) b.setAttribute('aria-pressed', (i === 1) === actual);
  };
  doc.addEventListener('click', e => {
    if (zoom.hidden || !e.target.matches('.diagram img')) return;
    actual = !actual;
    update();
  });
  file.onDiagram = update;
  new ResizeObserver(update).observe(scroll);
  update();
  file.image = () => ({ actual });
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

// prepare opens web links in a new tab and links to files in a kijk tab, and shows the images of
// the folder. file.pics reloads them one by one. file.diagrams are the blocks to draw.
function prepare(frag, file, md, doc) {
  clean(frag);
  file.pics = [];
  file.diagrams = [];
  for (const pre of frag.querySelectorAll('pre.diagram')) {
    const kind = [...pre.classList].find(c => ENGINES[c]);
    if (!kind) continue; // a kind this kijk does not know, from a newer remote kijk
    const box = el('div', 'diagram');
    box.dataset.lines = pre.dataset.lines;
    pre.removeAttribute('data-lines');
    pre.removeAttribute('class');
    pre.replaceWith(box);
    box.append(pre);
    file.diagrams.push({ box, pre, kind, text: pre.textContent });
  }
  for (const img of frag.querySelectorAll('img')) {
    const src = img.getAttribute('src') || '';
    if (external(src)) continue;
    const box = el('span', 'md-img', `Image: ${img.getAttribute('alt') || src}`);
    const path = inFolder(src, file);
    if (path == null || !IMAGE.test(path)) {
      img.replaceWith(box);
      continue;
    }
    const { at, of } = file.versions;
    const load = () => { img.src = url('image', { root: file.root, path, v: of.get(path) ?? at }); };
    img.loading = 'lazy';
    img.onerror = () => img.replaceWith(box);
    load();
    file.pics.push({
      path,
      reload: () => {
        of.set(path, Date.now());
        load();
        if (box.isConnected) box.replaceWith(img);
      },
    });
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
  if (!href.split(/[?#]/)[0]) {
    let id;
    try {
      id = slug(decodeURIComponent(href.slice(1)));
    } catch {
      return;
    }
    [...doc.querySelectorAll('h1, h2, h3, h4, h5, h6')].find(h => slug(h.textContent) === id)?.scrollIntoView();
    return;
  }
  const path = inFolder(href, file);
  if (path != null) md.open(path);
}

// inFolder turns a link or image of a markdown file into a path from the folder root: relative
// to the file's folder, or to the root when it starts with /. null when it is above the root.
function inFolder(href, file) {
  let target;
  try {
    target = decodeURIComponent(href.split(/[?#]/)[0]);
  } catch {
    return null;
  }
  const parts = target.startsWith('/') ? [] : file.path.split('/').slice(0, -1);
  for (const s of target.split('/')) {
    if (s === '..') {
      if (!parts.length) return null;
      parts.pop();
    } else if (s && s !== '.') {
      parts.push(s);
    }
  }
  return parts.join('/');
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
// keep is set when the diff reloads: {scrollTop, res}, res being fetched already. md holds the
// view of an SVG or diagram diff: {source, toggle}.
export async function openDiff(viewer, root, change, mode, context = 3, keep, md) {
  const d = { kind: 'diff', starts: [], path: change.path, mode, hash: change.hash || '' };
  const params = { root, path: change.path, orig: change.orig || '', mode, hash: change.hash || '', context };
  const again = (ctx, k) => openDiff(viewer, root, change, mode, ctx, k, md);
  d.reload = async () => {
    const res = await get('diff', params).catch(() => null);
    if (current !== d || (res && JSON.stringify(res) === d.json)) return;
    again(context, { scrollTop: d.scroll?.scrollTop ?? 0, res });
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
  // An SVG or diagram diff can show its source: the line diff below.
  const modes = md && ((res.image && SVG.test(change.path)) || res.diagram) ? sourceSwitch(md, () => 1) : null;
  if ((res.image || res.diagram) && !(modes && md.source)) {
    const bar = change.orig || modes ? el('div', 'diffbar', change.orig ? `Renamed from ${change.orig}` : '') : null;
    if (modes) bar.append(modes);
    if (res.diagram) return diagramDiff(viewer, d, res.diagram, change, mode, bar);
    return imageDiff(viewer, d, res.image, root, change, mode, bar);
  }
  const say = text => {
    const bar = el('div', 'diffbar');
    if (modes) bar.append(modes);
    viewer.replaceChildren(...(modes ? [bar] : []), el('p', 'empty', text));
  };
  if (res.binary) return say('Binary file changed');
  if (!res.hunks.length) return say(change.orig ? `Renamed from ${change.orig}, no content changes` : 'No content changes');

  // The tab names the file; this bar keeps what the tab cannot show.
  const full = context >= FULL;
  const bar = el('div', 'diffbar', change.orig ? `Renamed from ${change.orig}` : '');
  const toggle = el('button', 'tool', full ? 'Show changes only' : 'Show full file');
  toggle.onclick = () => again(full ? 3 : FULL);
  bar.append(toggle);
  if (modes) bar.append(modes);

  const rows = [];
  const more = () => again(context + 20);
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

// imageDiff shows the before and after pictures side by side, each fit to its column.
async function imageDiff(viewer, d, image, root, change, mode, bar) {
  const versions = sideNames(mode, image.parent, change);
  const sides = await Promise.all(['before', 'after'].map(async (name, i) => {
    const s = image[name];
    if (!s) return { name, note: i === 0 ? 'Added' : 'Deleted' };
    if (s.size > MAX_SIZE) return { name, s, note: `Image too large, ${size(s.size)}` };
    if (s.lfs) return { name, s, note: `Stored in Git LFS, ${size(s.lfs)}` };
    const src = url('image', { root, path: change.path, orig: change.orig || '', mode, hash: change.hash || '', side: name, v: s.oid || s.mtime });
    const img = await picture(src, SVG.test(i === 0 && change.orig ? change.orig : change.path));
    if (!img) return { name, s, note: 'Cannot show this image' };
    img.alt = i === 0 ? 'Before' : 'After';
    return { name, s, img };
  }));
  if (current !== d) return;
  const shown = sides.find(x => x.img)?.img;
  const grid = el('div', 'imgdiff');
  sides.forEach(({ name, s, img, note }, i) => {
    const side = el('section', `side ${name}`);
    const head = sideHead(i, versions);
    if (img) head.append(facts(img, s.size));
    else if (s) head.append(el('span', 'facts', size(s.size)));
    const body = el('div', 'side-body');
    if (img) {
      body.append(img);
    } else {
      const box = el('div', 'side-none', note);
      if (shown && !shown.classList.contains('sizeless')) { // as big as the other picture, so the two line up
        box.style.setProperty('--w', `${shown.naturalWidth}px`);
        box.style.setProperty('--ratio', `${shown.naturalWidth} / ${shown.naturalHeight}`);
      }
      body.append(box);
    }
    side.append(head, body);
    grid.append(side);
  });
  viewer.replaceChildren(...(bar ? [bar] : []), grid);
  viewChanged();
}

const sideNames = (mode, parent, change) =>
  ({ unstaged: ['Staged', 'On disk'], staged: ['HEAD', 'Staged'], untracked: ['', 'On disk'], commit: [parent, change.hash?.slice(0, 7)] })[mode];

function sideHead(i, versions) {
  const head = el('div', 'side-head');
  head.append(el('span', 'tag', i === 0 ? 'Before' : 'After'), el('span', 'ver', versions[i] || ''));
  return head;
}

// diagramDiff draws the before and after diagrams side by side, in the image diff layout.
async function diagramDiff(viewer, d, diagram, change, mode, bar) {
  const versions = sideNames(mode, diagram.parent, change);
  const grid = el('div', 'imgdiff');
  d.diagrams = [];
  ['before', 'after'].forEach((name, i) => {
    const s = diagram[name];
    const side = el('section', `side ${name}`);
    const body = el('div', 'side-body');
    if (!s || s.tooLarge) {
      body.append(el('div', 'side-none', !s ? (i === 0 ? 'Added' : 'Deleted') : 'Diagram too large to draw'));
    } else {
      const pre = el('pre');
      pre.append(el('code', null, s.text));
      const box = el('div', 'diagram');
      box.append(pre);
      body.append(box);
      d.diagrams.push({ box, pre, kind: diagram.kind, text: s.text });
    }
    side.append(sideHead(i, versions), body);
    grid.append(side);
  });
  await settle(drawDiagrams(d));
  if (current !== d) return;
  viewer.replaceChildren(...(bar ? [bar] : []), grid);
  viewChanged();
}

// Diagrams are drawn in one hidden frame per window, with its own CSP, and come back as SVG.
// The frame always comes from the local kijk, also for a remote folder.
const ENGINES = { mermaid: 'Mermaid' };
const DRAWN_MAX = 100;
const drawn = new Map(); // kind, theme and text -> {promise, result}, oldest first
const waiting = new Map();
let frame = null;
let frameReady = null;
let drawId = 0;
const darkTheme = matchMedia('(prefers-color-scheme: dark)');

function diagramFrame() {
  frameReady ??= new Promise(resolve => {
    frame = el('iframe', 'diagram-frame');
    frame.setAttribute('sandbox', 'allow-scripts allow-same-origin'); // same origin, so its requests carry the cookie
    frame.setAttribute('aria-hidden', 'true');
    frame.tabIndex = -1;
    frame.src = '/diagram';
    addEventListener('message', e => {
      if (e.source !== frame.contentWindow || e.origin !== location.origin) return;
      if (e.data?.ready) return resolve();
      waiting.get(e.data?.id)?.(e.data);
      waiting.delete(e.data?.id);
    });
    document.body.append(frame);
  });
  return frameReady;
}

// themeVars gives Mermaid kijk's colors, read from the page: the frame cannot see its CSS.
function themeVars() {
  const css = getComputedStyle(document.documentElement);
  const v = name => css.getPropertyValue(name).trim();
  return {
    darkMode: darkTheme.matches, fontFamily: 'system-ui, -apple-system, "Segoe UI", sans-serif', fontSize: '14px',
    background: v('--bg'), textColor: v('--text'), lineColor: v('--muted'),
    primaryColor: v('--sep-bg'), mainBkg: v('--sep-bg'), primaryTextColor: v('--text'),
    primaryBorderColor: v('--edge'), nodeBorder: v('--edge'),
    secondaryColor: v('--select'), tertiaryColor: v('--panel'),
    clusterBkg: v('--panel'), clusterBorder: v('--border'), edgeLabelBackground: v('--bg'),
  };
}

// drawDiagram gives {svg, width, height} or {error}. An unchanged diagram reuses its SVG, so a
// reload does not flicker; a failed download is not kept, so the next open tries again.
function drawDiagram(kind, text) {
  const key = `${kind}\n${darkTheme.matches}\n${text}`;
  let entry = drawn.get(key);
  if (entry) {
    drawn.delete(key);
    drawn.set(key, entry);
    return entry;
  }
  const vars = themeVars();
  entry = {};
  entry.promise = diagramFrame().then(() => new Promise(resolve => {
    const id = ++drawId;
    waiting.set(id, resolve);
    frame.contentWindow.postMessage({ id, kind, text, vars }, location.origin);
  })).then(r => {
    entry.result = r;
    if (r.error?.startsWith('Could not download') && drawn.get(key) === entry) drawn.delete(key);
    return r;
  });
  drawn.set(key, entry);
  if (drawn.size > DRAWN_MAX) drawn.delete(drawn.keys().next().value);
  return entry;
}

// drawDiagrams shows the diagrams of a view as pictures. Until one is drawn, or when it cannot
// be, its block shows the text.
function drawDiagrams(view) {
  return Promise.all((view.diagrams || []).map(async x => {
    const entry = drawDiagram(x.kind, x.text);
    const r = entry.result ?? await entry.promise;
    if (r.error) {
      x.box.replaceChildren(el('p', 'diagram-note', r.error), x.pre);
    } else {
      const img = el('img', 'diagram-pic');
      img.alt = `${ENGINES[x.kind]} diagram`;
      if (r.width) [img.width, img.height] = [r.width, r.height];
      img.src = `data:image/svg+xml,${encodeURIComponent(r.svg)}`;
      await img.decode().catch(() => {});
      x.box.replaceChildren(img);
    }
    view.onDiagram?.();
  }));
}

// settle waits for the diagrams before a view shows, so a reload does not flash their text,
// but not so long that a first download keeps the view empty.
const settle = work => Promise.race([work, new Promise(r => setTimeout(r, 1000))]);

darkTheme.addEventListener('change', () => { if (current?.diagrams?.length) drawDiagrams(current); });

// viewScroll is the scroll position of the open file or diff, once it is drawn.
export const viewScroll = () => current?.scroll?.scrollTop;

export function setViewScroll(top) {
  if (!current?.scroll) return;
  current.scroll.scrollTop = top;
  current.render?.();
}

// refreshView reloads the open file when it or git changed (its change marks), and the open diff on any change.
// In rendered markdown, an image that changed reloads on its own, so the page does not move.
export function refreshView(change) {
  if (!current?.reload) return;
  for (const pic of current.pics || []) if (change.all || change.paths.includes(pic.path)) pic.reload();
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

const MAX_SIZE = 50 * 1024 * 1024; // maxFileSize in files.go

function size(n) {
  if (n < 1024) return `${n} bytes`;
  if (n < 1024 * 1024) return `${Math.round(n / 1024)} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}
