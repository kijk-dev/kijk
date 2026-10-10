import { el } from './api.js';
import { openFile, openDiff, showMessage, viewScroll, setViewScroll, LINE_HEIGHT } from './view.js';
import { keyLabel, keyNames } from './keys.js';

// A tab is {kind: 'file', path, source} or {kind: 'diff', mode, path, orig, hash}, plus preview and scrollTop.
// At most one tab is a preview; a single click replaces it, a double click keeps it.
const bar = document.getElementById('filetabs');
const viewer = document.getElementById('viewer');
let root = null;
let tabs = [];
let active = -1;
let recent = []; // paths of the last opened files, newest first
// Back and forward: the places the user went to, per folder, until the page reloads.
const histories = new Map();
let history = { places: [], at: -1 };
let going = false; // set while back or forward shows a place, so that is not recorded again
let changed = () => {};

const keyOf = t => `${t.kind}:${t.mode || ''}:${t.hash || ''}:${t.path}`;

// setTabs shows another folder's saved tabs; changed runs after every change, to save and sync.
export function setTabs(folder, saved, onChange) {
  root = folder;
  if (!histories.has(folder)) histories.set(folder, { places: [], at: -1 });
  history = histories.get(folder);
  tabs = saved?.tabs || [];
  recent = saved?.recent || [];
  changed = onChange;
  active = -1;
  show(saved?.active ?? -1);
}

export function tabState() {
  remember();
  return { tabs, active, recent };
}

export const recentFiles = () => recent;

// here is the place on screen: the tab, without its preview and scroll fields, and its position.
function here() {
  const t = tabs[active];
  if (!t) return null;
  const { preview, scrollTop, ...tab } = t;
  return { tab, scrollTop: viewScroll() ?? scrollTop ?? 0 };
}

// touch moves the newest place to where the user is now, so back returns there.
function touch() {
  const p = here(), cur = history.places[history.at];
  if (p && cur && keyOf(cur.tab) === keyOf(p.tab)) cur.scrollTop = p.scrollTop;
}

// record adds the place on screen. A place within 10 lines of the last one in the same tab
// only moves it, so stepping through nearby find matches does not fill the history.
function record() {
  const p = here();
  if (!p || going) return;
  const cur = history.places[history.at];
  if (cur && keyOf(cur.tab) === keyOf(p.tab) && Math.abs(cur.scrollTop - p.scrollTop) < 10 * LINE_HEIGHT) {
    cur.scrollTop = p.scrollTop;
    return;
  }
  history.places.splice(history.at + 1, Infinity, p);
  if (history.places.length > 50) history.places.shift();
  history.at = history.places.length - 1;
}

// jump wraps a move inside the open view, such as go to line, so back can undo it.
export function jump(move) {
  touch();
  move();
  record();
}

// goPlace steps back (dir -1) or forward (dir 1). A place whose tab was closed opens again.
export function goPlace(dir) {
  touch();
  const p = history.places[history.at + dir];
  if (!p) return;
  history.at += dir;
  going = true;
  try {
    const i = tabs.findIndex(x => keyOf(x) === keyOf(p.tab));
    if (i === active) {
      setViewScroll(p.scrollTop);
    } else if (i >= 0) {
      remember();
      tabs[i].scrollTop = p.scrollTop;
      show(i);
    } else {
      openTab({ ...p.tab, scrollTop: p.scrollTop });
    }
  } finally {
    going = false;
  }
}

export function closeActive() {
  if (tabs[active]) close(active);
}

export const activeTab = () => tabs[active];

export function openTab(t, pin) {
  remember();
  let i = tabs.findIndex(x => keyOf(x) === keyOf(t));
  if (i < 0) {
    i = tabs.findIndex(x => x.preview);
    if (i >= 0) tabs[i] = { ...t, preview: true };
    else tabs.splice(i = active + 1, 0, { ...t, preview: true });
    if (i === active) active = -1; // the preview shown now holds another file
  }
  if (pin) tabs[i].preview = false;
  if (i === active) {
    render();
    changed();
  } else {
    show(i);
  }
}

function remember() {
  const top = viewScroll();
  if (tabs[active] && top != null) tabs[active].scrollTop = top;
}

// changed runs after the view switches, so it never reads the old view's scroll position.
function show(i) {
  touch();
  active = tabs[i] ? i : -1;
  render();
  const t = tabs[active];
  if (t?.kind === 'file') recent = [t.path, ...recent.filter(p => p !== t.path)].slice(0, 30);
  const keep = t?.scrollTop != null ? { scrollTop: t.scrollTop } : undefined;
  if (!t) showMessage(viewer, 'Select a file');
  else if (t.kind === 'file') openFile(viewer, root, t.path, keep, viewOptions(t));
  else openDiff(viewer, root, t, t.mode, 3, keep);
  record(); // after the open, so the place takes the tab's position, not the old view's
  changed();
}

// viewOptions lets a markdown tab switch between rendered and source, and open the files it links to.
function viewOptions(t) {
  return {
    source: !!t.source,
    toggle: (source, line) => {
      t.source = source || undefined;
      t.scrollTop = undefined;
      changed();
      openFile(viewer, root, t.path, { line }, viewOptions(t));
    },
    open: path => openTab({ kind: 'file', path }),
  };
}

// The tab strip is one Tab stop: ←/→ (and Home/End) switch tabs, Delete or Backspace closes one.
function tabKey(e, i) {
  const to = { ArrowRight: i + 1, ArrowLeft: i - 1, Home: 0, End: tabs.length - 1 }[e.key];
  if (to !== undefined) {
    if (!tabs[to]) return;
    remember();
    show(to);
  } else if (e.key === 'Delete' || e.key === 'Backspace') {
    close(i);
  } else if (e.key === 'Enter' || e.key === ' ') {
    openTab(tabs[i], true);
  } else {
    return;
  }
  e.preventDefault();
  bar.children[active]?.focus();
}

function close(i) {
  remember();
  tabs.splice(i, 1);
  if (i === active) return show(Math.min(i, tabs.length - 1));
  if (i < active) active--;
  render();
  changed();
}

// folderOf gives just enough parent folders to tell apart tabs whose file names clash, else ''.
function folderOf(t) {
  const dirs = p => p.replace(/\/$/, '').split('/');
  const own = dirs(t.path);
  const name = own.pop();
  const others = tabs.map(x => dirs(x.path)).filter(p => p.pop() === name && p.join('/') !== own.join('/'));
  if (!others.length) return '';
  for (let n = 1; n <= own.length; n++) {
    const dir = own.slice(-n).join('/');
    if (!others.some(p => p.slice(-n).join('/') === dir)) return dir;
  }
  return own.join('/');
}

function render() {
  bar.hidden = !tabs.length;
  bar.replaceChildren(...tabs.map((t, i) => {
    const tab = el('div', `filetab${i === active ? ' active' : ''}${t.preview ? ' preview' : ''}`);
    tab.setAttribute('role', 'tab');
    tab.setAttribute('aria-selected', i === active);
    tab.setAttribute('aria-keyshortcuts', `Delete ${keyNames('closeTab')}`);
    tab.tabIndex = i === active ? 0 : -1;
    tab.title = t.preview ? `${t.path} (preview: double-click to keep)` : t.path;
    tab.append(el('span', null, t.path.replace(/\/$/, '').split('/').pop()));
    const folder = folderOf(t);
    if (folder) tab.append(el('span', 'muted', folder));
    if (t.kind === 'diff') tab.append(el('span', 'muted', t.hash ? t.hash.slice(0, 7) : t.mode));
    const x = el('button', 'close', '×');
    x.title = `Close (${keyLabel('closeTab')})`;
    x.tabIndex = -1; // the focused tab closes with Delete; a button inside a tab is not announced well
    x.setAttribute('aria-hidden', 'true');
    x.onclick = e => {
      e.stopPropagation();
      close(i);
    };
    tab.append(x);
    tab.onclick = () => {
      if (i === active) return;
      remember();
      show(i);
    };
    tab.ondblclick = () => openTab(t, true);
    tab.onauxclick = e => { if (e.button === 1) close(i); };
    tab.onkeydown = e => tabKey(e, i);
    return tab;
  }));
  bar.children[active]?.scrollIntoView({ block: 'nearest', inline: 'nearest' });
}
