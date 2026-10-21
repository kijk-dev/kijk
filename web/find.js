import { el, get } from './api.js';
import { viewInfo, setHits, reveal, onView, LINE_HEIGHT } from './view.js';
import { keyLabel } from './keys.js';
import { jump } from './tabs.js';

const UP = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="m18 15-6-6-6 6"/></svg>';
const DOWN = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="m6 9 6 6 6-6"/></svg>';
const CLOSE = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M18 6 6 18"/><path d="m6 6 12 12"/></svg>';
const num = n => n.toLocaleString('en-US');

function iconButton(svg, label) {
  const b = el('button', 'icon-btn');
  b.innerHTML = svg; // a fixed string
  b.setAttribute('aria-label', label);
  b.dataset.tip = label;
  b.onmousedown = e => e.preventDefault(); // keep the caret in the field
  return b;
}

// The bar above the view: find in file, or go to line.
const bar = document.getElementById('findbar');
const input = el('input');
input.type = 'text';
input.spellcheck = false;
input.autocomplete = 'off';
const count = el('span', 'count');
count.setAttribute('role', 'status');
const prev = iconButton(UP, 'Previous match');
const next = iconButton(DOWN, 'Next match');
const close = iconButton(CLOSE, 'Close');
prev.classList.add('find-only');
next.classList.add('find-only');
bar.append(input, count, prev, next, close);

let mode = null; // 'find' or 'line'
let query = '';
let matches = []; // {key, f, t}: key is a line number in a file, a row index in a diff
let at = -1;
let more = false;
let call = 0;
let timer = 0; // set while a search waits for the typing to pause

function show(m) {
  mode = m;
  bar.hidden = false;
  bar.dataset.mode = m;
  input.placeholder = m === 'find' ? 'Find' : 'Line number';
  input.setAttribute('aria-label', input.placeholder);
  input.focus();
  input.select();
}

function hideBar() {
  bar.hidden = true;
  mode = null;
  clearTimeout(timer);
  timer = 0;
  setHits(null);
}

const topKey = v => Math.floor(v.scroll.scrollTop / LINE_HEIGHT) + (v.kind === 'file' ? 1 : 0);

// openFind returns false for rendered markdown: all of it is in the page, so the browser's find works.
export function openFind() {
  const v = viewInfo();
  if (!v || v.kind === 'rendered') return false;
  const picked = getSelection().toString();
  if (picked && !picked.includes('\n')) input.value = picked;
  else if (mode !== 'find') input.value = query;
  show('find');
  search(true);
  return true;
}

// findAgain is F3 / Shift+F3: the next or previous match, also after the bar was closed.
export function findAgain(dir) {
  if (mode !== 'find') return openFind();
  step(dir);
  return true;
}

export function openGotoLine() {
  const v = viewInfo();
  if (!v?.gotoLine) return false;
  input.value = '';
  setHits(null);
  show('line');
  count.textContent = `1–${num(v.lines || 1)}`;
  count.classList.remove('none');
  return true;
}

async function search(move) {
  clearTimeout(timer);
  timer = 0;
  const v = viewInfo();
  query = input.value;
  const mine = ++call;
  let list = [], extra = false;
  if (v && query && v.kind === 'diff') {
    const q = query.toLowerCase();
    v.rows.forEach((r, i) => {
      if (r.sep) return;
      const t = r.t.toLowerCase();
      for (let k = t.indexOf(q); k >= 0; k = t.indexOf(q, k + q.length)) list.push({ key: i, f: k, t: k + q.length });
    });
  } else if (v && query && v.kind === 'file') {
    try {
      const res = await get('find', { root: v.root, path: v.path, q: query, ...(v.pr ? { pr: v.pr } : {}) });
      list = res.matches.map(m => ({ key: m.l, f: m.f, t: m.t }));
      extra = !!res.more;
    } catch { /* shown as no matches */ }
    if (mine !== call || viewInfo() !== v) return;
  }
  const was = matches[at]?.key;
  matches = list;
  more = extra;
  const from = move ? (v ? topKey(v) : 0) : was ?? 0;
  at = matches.findIndex(m => m.key >= from);
  if (at < 0) at = matches.length ? 0 : -1;
  if (move) jump(() => paint(true));
  else paint(false);
}

function paint(move) {
  if (mode !== 'find') return;
  const map = new Map();
  matches.forEach((m, i) => {
    if (!map.has(m.key)) map.set(m.key, []);
    map.get(m.key).push([m.f, m.t, i === at]);
  });
  setHits(map.size ? map : null);
  count.textContent = !query ? '' : matches.length ? `${num(at + 1)} of ${num(matches.length)}${more ? '+' : ''}` : 'No matches';
  count.classList.toggle('none', !!query && !matches.length);
  if (move && at >= 0) reveal(matches[at].key);
}

function step(dir) {
  if (!matches.length) return;
  at = (at + dir + matches.length) % matches.length;
  jump(() => paint(true));
}

function goLine() {
  const n = parseInt(input.value, 10);
  const v = viewInfo();
  if (!n || !v?.gotoLine) return;
  hideBar();
  jump(() => v.gotoLine(n));
}

input.oninput = () => {
  if (mode === 'find') {
    clearTimeout(timer);
    timer = setTimeout(() => search(true), 120);
  }
};
input.onkeydown = e => {
  if (e.key === 'Escape') {
    e.preventDefault();
    hideBar();
  } else if (e.key === 'Enter') {
    e.preventDefault();
    if (mode === 'line') goLine();
    else if (timer) search(true);
    else step(e.shiftKey ? -1 : 1);
  }
};
prev.onclick = () => step(-1);
next.onclick = () => step(1);
close.onclick = hideBar;
prev.dataset.tip = `Previous match\n${keyLabel('findPrev')}`;
next.dataset.tip = `Next match\n${keyLabel('findNext')}`;

// A new or reloaded view gets the open search again, without scrolling it.
onView(() => {
  if (mode === 'find' && query) search(false);
  else if (mode === 'line') count.textContent = `1–${num(viewInfo()?.lines || 1)}`;
});

// Go to file: a popup over everything that lists the folder's files matching what is typed.
const dialog = el('dialog', 'goto');
const box = el('div', 'goto-box');
const field = el('input');
field.type = 'text';
field.spellcheck = false;
field.autocomplete = 'off';
field.placeholder = 'Go to file';
field.setAttribute('role', 'combobox');
field.setAttribute('aria-label', 'Go to file');
field.setAttribute('aria-controls', 'goto-list');
field.setAttribute('aria-expanded', 'true');
field.setAttribute('aria-autocomplete', 'list');
const list = el('ul');
list.id = 'goto-list';
list.setAttribute('role', 'listbox');
list.setAttribute('aria-label', 'Files');
const hint = el('p', 'note');
box.append(field, list, hint);
dialog.append(box);
document.body.append(dialog);
dialog.onclick = e => { if (e.target === dialog) dialog.close(); }; // a click outside the box

let gotoRoot = null;
let gotoOpen = null;
let recent = null; // the recent files list while that popup is open, else null
let items = [];
let sel = 0;
let gotoCall = 0;
let gotoTimer = 0; // set while a lookup waits for the typing to pause

function openPopup(label, onOpen) {
  gotoOpen = onOpen;
  field.value = '';
  field.placeholder = label;
  field.setAttribute('aria-label', label);
  dialog.showModal();
  field.focus();
}

export function openGotoFile(root, onOpen) {
  if (!root) return false;
  gotoRoot = root;
  recent = null;
  openPopup('Go to file', onOpen);
  showItems([]);
  return true;
}

// openRecent lists the folder's last opened files, newest first; typing filters them.
export function openRecent(paths, onOpen) {
  if (!paths) return false;
  recent = paths;
  openPopup('Recent files', onOpen);
  showItems(paths.map(path => ({ path, hits: [] })));
  sel = Math.min(1, paths.length - 1); // the first is the file on screen; Enter goes back to the one before
  mark();
  return true;
}

// inOrder gives the code point indexes where q's letters appear in order in path, ignoring case.
function inOrder(q, path) {
  const chars = [...path.toLowerCase()];
  const hits = [];
  let i = 0;
  for (const c of q.toLowerCase().replace(/\s/g, '')) {
    while (i < chars.length && chars[i] !== c) i++;
    if (i === chars.length) return null;
    hits.push(i++);
  }
  return hits;
}

// The bold letters are the ones the typed text matched; hits count code points, as the server does.
function nameParts(path, hits) {
  const chars = [...path];
  const cut = chars.lastIndexOf('/') + 1;
  const on = new Set(hits);
  const part = (cls, from, to) => {
    const s = el('span', cls);
    for (let i = from; i < to; i++) s.append(on.has(i) ? el('b', null, chars[i]) : chars[i]);
    return s;
  };
  return [part('name', cut, chars.length), part('dir', 0, Math.max(0, cut - 1))];
}

function showItems(found) {
  items = found;
  sel = 0;
  list.replaceChildren(...found.map((it, i) => {
    const li = el('li');
    li.id = `goto-${i}`;
    li.setAttribute('role', 'option');
    li.title = it.path;
    li.append(...nameParts(it.path, it.hits || []));
    li.onmousedown = e => e.preventDefault();
    li.onclick = () => choose(i);
    return li;
  }));
  mark();
  const q = field.value.trim();
  hint.textContent = recent ? (found.length ? '' : q ? 'No recent files match' : 'No recent files yet')
    : !q ? `Type part of a file name, like usrsvc for UserService. Recent files: ${keyLabel('recentFiles')}`
      : found.length ? '' : 'No files match';
  hint.hidden = !hint.textContent;
}

function mark() {
  [...list.children].forEach((li, i) => li.setAttribute('aria-selected', i === sel));
  list.children[sel]?.scrollIntoView({ block: 'nearest' });
  if (items.length) field.setAttribute('aria-activedescendant', `goto-${sel}`);
  else field.removeAttribute('aria-activedescendant');
}

function choose(i) {
  const it = items[i];
  if (!it) return;
  dialog.close();
  gotoOpen(it.path);
}

async function lookup() {
  clearTimeout(gotoTimer);
  gotoTimer = 0;
  const q = field.value.trim();
  if (recent) {
    return showItems(recent.map(path => ({ path, hits: q ? inOrder(q, path) : [] })).filter(it => it.hits));
  }
  const mine = ++gotoCall;
  if (!q) return showItems([]);
  const found = await get('goto', { root: gotoRoot, q }).catch(() => []);
  if (mine === gotoCall) showItems(found);
}

field.oninput = () => {
  clearTimeout(gotoTimer);
  gotoTimer = setTimeout(lookup, 60);
};
field.onkeydown = e => {
  const to = { ArrowDown: sel + 1, ArrowUp: sel - 1, PageDown: sel + 10, PageUp: sel - 10 }[e.key];
  if (to !== undefined) {
    e.preventDefault();
    sel = Math.max(0, Math.min(items.length - 1, to));
    mark();
  } else if (e.key === 'Enter') {
    e.preventDefault();
    if (gotoTimer) lookup().then(() => choose(0));
    else choose(sel);
  }
};
