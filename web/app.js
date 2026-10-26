import { el, get, post } from './api.js';
import { refreshTip, tipSide } from './tip.js';
import { openKey, openFolder, openFolderPR, closeFolder, setMachine, setupFolder, showFetched, SERVER } from './folder.js';
import { ago, lineCount } from './git.js';
import { mac, typing } from './keys.js';
import { showKijk, showAgentErrors, showUndo, showSaid, restarting } from './status.js';
import { LISTS, inList, keyed, listRows, cwdIn, target, closeRule, finish } from './rail.js';

// The agent types kijk knows, in its order. They come from the local kijk, also for remote rows.
const agentTypes = await get('agenttypes');
// agentType finds a type by name. An unknown type shows its name and no icon.
const agentType = name => agentTypes.find(a => a.name === name) || { label: name, icon: '' };
// What the Agents list says when the tool of no agent type is on this machine.
const NONE_INSTALLED = `No running agents · kijk shows ${andList(agentTypes.map(a => a.label))} agents`;

// A new window that a command opened acts on that command's request, once: a reload must not.
const params = new URLSearchParams(location.search);
let wantSeq = Number(params.get('open')) || 0;
if (params.has('open') || params.has('window')) history.replaceState(null, '', '/');
let openSeq = null; // the last open request seen; null until the first message of a connection
let listError = '';
let scanned = false; // the agent scan of this look has run, so the Agents list is known
let noAgents = 'No running agents'; // what the Agents list says for the local machine when it has none

const rowsEl = document.getElementById('rows');
const statusEl = document.getElementById('status');
const railEl = document.getElementById('rail');
const railBtn = document.getElementById('rail-toggle');
const viewer = document.getElementById('viewer');
const start = document.getElementById('start');
let list = startList(); // the list that shows: 'agents' or 'folders'
let rows = new Map(); // every row of both lists, local and remote, by folder key
let machines = null; // the saved remote machines from the last message; null when none is saved
let numbered = []; // the rows that have a Ctrl+Alt number, in order
let openRow = null; // the row of the open folder as it was opened; it may have left both lists since
const finished = new Set(); // folders whose agent went idle since the user last opened them
const collapsed = new Set(); // machine groups folded by the user; '' is Local
try { for (const g of JSON.parse(localStorage.getItem('kijk.collapsed')) || []) collapsed.add(g); } catch { /* storage blocked */ }
const lastKey = 'kijk.last';
let reopen = null; // the folder open before a reload, opened again once its row shows up
try {
  reopen = localStorage.getItem(lastKey) || localStorage.getItem('kijk.lastFolder'); // an older kijk's folders window
  localStorage.removeItem('kijk.lastFolder');
} catch { /* storage blocked */ }

// startList is the folders window's old address, else the list that showed last.
function startList() {
  if (params.get('window') === 'folders') return 'folders';
  try { return LISTS.find(l => l === localStorage.getItem('kijk.list')) || 'agents'; } catch { return 'agents'; }
}

// announce reads a message to screen readers; the region is always in the page, so it is heard.
const announcer = document.getElementById('announce');
function announce(text) {
  if (text && announcer.textContent !== text) announcer.textContent = text;
}
// agentIcon is the icon of the row's first agent type, for the agent notes of its folder.
function agentIcon(r) {
  const type = agentType(r.agents?.[0]?.type);
  if (!type.icon) return null;
  const icon = el('span', 'agent');
  icon.innerHTML = type.icon; // from the local kijk's own files, never a remote machine's text
  if (type.color) icon.style.setProperty('--agent-color', type.color);
  return icon;
}
setupFolder({ lastKey, start, announce, say: showSaid, agentIcon, onGone,
  onFolderEvent: () => { if (rows.get(openKey)?.saved) refreshRow(); } });
tipSide('#rail', 'right');
tipSide('#statusbar', 'above');

function showStatus(text, isError) {
  statusEl.hidden = !text;
  statusEl.textContent = text || '';
  statusEl.classList.toggle('error', !!isError);
  announce(text);
}

// Row elements are kept and updated in place, so a click is not lost when the list refreshes. A repo
// in both lists has an element in each; only one list shows at a time.
const els = { agents: new Map(), folders: new Map() };

function rowEl(key) {
  let li = els.agents.get(key);
  if (li) return li;
  li = el('li');
  li.tabIndex = 0;
  li.setAttribute('role', 'button');
  li.onclick = () => open(li.row);
  li.onkeydown = e => {
    if (e.key !== 'Enter' && e.key !== ' ') return;
    e.preventDefault();
    open(li.row);
  };
  const top = el('div', 'line');
  li.badge = el('span', 'badge');
  li.initials = el('span');
  li.key = el('span', 'key');
  li.names = el('span', 'names');
  li.done = el('span', 'done');
  li.when = el('span', 'when');
  li.repo = el('div', 'repo');
  li.icons = el('span', 'agents');
  li.where = el('span', 'where');
  li.repo.append(li.where);
  li.badge.append(li.initials, li.key);
  top.append(li.icons, li.badge, li.names, li.done, li.when);
  li.append(top, li.repo);
  els.agents.set(key, li);
  return li;
}

// initials turns "web-server", "code_viewer" or "adminUi" into two letters.
function initials(name) {
  const words = name.replace(/([a-z])([A-Z])/g, '$1 $2').split(/[^A-Za-z0-9]+/).filter(Boolean);
  return (words.length > 1 ? words[0][0] + words[1][0] : (words[0] || name).slice(0, 2)).toUpperCase();
}

// hue gives each folder a stable badge tint, so two repos with the same initials still differ.
const hue = s => [...s].reduce((h, c) => (h * 31 + c.charCodeAt(0)) % 360, 0);

function rowItem(r, n) {
  const li = rowEl(r.key);
  li.row = r;
  li.classList.toggle('selected', r.key === openKey);
  li.classList.toggle('busy', r.busy && !r.stale);
  li.classList.toggle('finished', finished.has(r.key));
  li.classList.toggle('stale', !!r.stale);
  li.toggleAttribute('aria-disabled', !!r.stale);
  const gone = machines?.find(m => m.name === r.machine)?.state === 'reconnecting' ? 'is reconnecting' : 'is unreachable';
  const state = r.stale ? `${r.machine} ${gone}` : r.busy ? 'Busy' : finished.has(r.key) ? 'Finished since you last looked' : 'Idle';
  const where = r.branch ? `${r.repo} · ${r.branch}` : r.repo;
  const key = openHint(n);
  li.initials.textContent = initials(r.repo);
  li.badge.style.setProperty('--hue', hue(r.machine + r.root));
  li.key.textContent = n || '';
  li.names.textContent = r.names.join(', ');
  li.when.textContent = ago(r.lastActivity);
  const agents = r.agents;
  const types = showIcons(li, agents);
  li.where.textContent = where;
  const labels = types.map(t => agentType(t).label);
  const perAgent = r.names.map((name, i) => [agentType(agents[i]?.type).label, agents[i]?.busy && !r.stale ? 'Busy' : 'Idle', name]);
  const who = types.length > 1
    ? [...perAgent.map(a => a.join(' · ')), `${state} · ${ago(r.lastActivity)}`]
    : [`${labels[0] || 'Agent'} · ${state} · ${ago(r.lastActivity)}`];
  li.dataset.tip = [r.names.join(', '), where, ...who, r.machine ? `${r.machine}:${r.root}` : r.root, key].filter(Boolean).join('\n');
  li.setAttribute('aria-label', types.length > 1
    ? `${perAgent.map(a => a.join(', ')).join('; ')}; ${where}, ${state}`
    : `${r.names.join(', ')}, ${labels.join(' and ')}, ${where}, ${state}`);
  return li;
}

// showIcons shows a row's agent types, one icon each, or the folder icon where no agent works. Only
// the icon of a type with a Busy agent pulses.
function showIcons(li, agents = []) {
  const types = [...new Set(agents.map(a => a.type))];
  const working = t => agents.some(a => a.type === t && a.busy);
  const kind = types.map(t => `${t}${working(t) ? '+' : ''}`).join(' ');
  if (li.icons.dataset.kind === kind) return types;
  li.icons.dataset.kind = kind;
  li.icons.className = types.length ? 'agents' : 'ficon';
  if (!types.length) li.icons.innerHTML = FOLDER;
  else li.icons.replaceChildren(...types.map(t => {
    const { icon: svg, color } = agentType(t);
    const icon = el('span', `agent${working(t) ? ' on' : ''}`);
    icon.innerHTML = svg; // from the local kijk's own files, never a remote machine's text
    if (color) icon.style.setProperty('--agent-color', color);
    return icon;
  }));
  li.style.setProperty('--icons', Math.max(types.length, 1));
  return types;
}

const FOLDER = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z"/></svg>';
const REMOVE = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M18 6 6 18"/><path d="m6 6 12 12"/></svg>';

function folderEl(key) {
  let li = els.folders.get(key);
  if (li) return li;
  li = el('li', 'folder');
  li.tabIndex = 0;
  li.setAttribute('role', 'button');
  li.setAttribute('aria-keyshortcuts', 'Delete');
  li.onclick = () => open(li.row);
  li.onkeydown = e => {
    if (e.target !== li) return;
    if (e.key === 'Delete' || e.key === 'Backspace') {
      e.preventDefault();
      removeFolder(li.row);
    } else if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      open(li.row);
    }
  };
  const top = el('div', 'line');
  li.icons = el('span');
  li.badge = el('span', 'badge');
  li.initials = el('span');
  li.key = el('span', 'key');
  li.badge.append(li.initials, li.key);
  li.names = el('span', 'names');
  li.lc = el('span', 'lc');
  // The row's Delete key is the keyboard way, so the button stays out of the tab order.
  li.rm = el('button', 'icon-btn rm');
  li.rm.type = 'button';
  li.rm.tabIndex = -1;
  li.rm.innerHTML = REMOVE;
  li.rm.dataset.tip = 'Remove from the list';
  li.rm.onclick = e => {
    e.stopPropagation();
    removeFolder(li.row);
  };
  li.where = el('span', 'where');
  const repo = el('div', 'repo');
  repo.append(li.where);
  top.append(li.icons, li.badge, li.names, li.lc, li.rm);
  li.append(top, repo);
  els.folders.set(key, li);
  return li;
}

function folderItem(r, n) {
  const li = folderEl(r.key);
  const s = r.saved;
  li.row = r;
  li.classList.toggle('selected', r.key === openKey);
  li.classList.toggle('missing', !!s.missing);
  li.classList.toggle('busy', !!r.busy);
  li.toggleAttribute('aria-disabled', !!s.missing);
  li.initials.textContent = initials(r.repo);
  li.badge.style.setProperty('--hue', hue(r.root));
  li.key.textContent = n || '';
  li.names.textContent = r.repo;
  showIcons(li, r.agents);
  const lc = (!s.missing && lineCount(s.add, s.del)) || el('span', 'lc');
  li.lc.replaceWith(lc);
  li.lc = lc;
  if (s.missing) li.where.replaceChildren(el('span', 'gone', 'missing'), ` · ${s.parent}`);
  else li.where.textContent = r.branch ? `${r.branch} · ${s.parent}` : s.parent;
  const changed = s.add || s.del ? `+${s.add} −${s.del} not committed` : r.branch && s.counted ? 'Nothing to commit' : '';
  const agents = (r.names || []).map((name, i) => [agentType(r.agents[i]?.type).label, r.agents[i]?.busy ? 'Busy' : 'Idle', name]);
  const key = openHint(n);
  li.dataset.tip = s.missing
    ? [r.repo, r.root, 'Missing: this folder is not on disk.', 'It comes back when the folder does.'].join('\n')
    : [r.repo, r.branch ? `${r.repo} · ${r.branch}` : 'Not a git repository', changed, ...agents.map(a => a.join(' · ')),
      s.open ? `${r.root} · opens at ${s.open}/` : r.root, key].filter(Boolean).join('\n');
  li.setAttribute('aria-label', s.missing ? `${r.repo}, missing, ${s.parent}`
    : [r.repo, r.branch || 'not a git repository', s.parent, changed, ...agents.map(([type, state, name]) => `${name}, ${type}, ${state}`)]
      .filter(Boolean).join(', '));
  li.rm.setAttribute('aria-label', `Remove ${r.repo} from the list`);
  return li;
}

const CHEVRON = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="m9 18 6-6-6-6"/></svg>';
const LAPTOP = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M20 16V7a2 2 0 0 0-2-2H6a2 2 0 0 0-2 2v9m16 0H4m16 0 1.28 2.55a1 1 0 0 1-.9 1.45H3.62a1 1 0 0 1-.9-1.45L4 16"/></svg>';
const COPY = '<svg viewBox="0 0 24 24" aria-hidden="true"><rect x="9" y="9" width="12" height="12" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>';

// Each machine state: the class of its dot and the word on its header.
const STATES = {
  connected: ['connected', 'connected'],
  connecting: ['connecting', 'connecting…'],
  reconnecting: ['connecting', 'reconnecting…'],
  'needs-login': ['login', 'needs login'],
  unreachable: ['unreachable', 'unreachable'],
  error: ['error', 'error'],
  'install-failed': ['error', 'install failed'],
  disabled: ['disabled', 'disabled'],
};

const groupEls = new Map();

function toggleGroup(name) {
  if (collapsed.has(name)) collapsed.delete(name);
  else collapsed.add(name);
  try { localStorage.setItem('kijk.collapsed', JSON.stringify([...collapsed])); } catch { /* storage blocked */ }
  render();
}

function groupEl(name) {
  let g = groupEls.get(name);
  if (g) return g;
  g = el('li', 'mgroup');
  g.rail = el('div', 'mdiv');
  g.head = el('button', 'mhead');
  g.head.type = 'button';
  g.railIcon = el('span', 'micon');
  g.icon = el('span', 'micon');
  g.name = el('span', 'mname');
  g.right = el('span', 'mright');
  g.note = el('div', 'mnote');
  g.list = el('ul');
  const caret = el('span', 'caret');
  caret.innerHTML = CHEVRON;
  g.rail.append(g.railIcon);
  g.head.append(caret, g.icon, g.name, el('span', 'mrule'), g.right);
  g.append(g.rail, g.head, g.note, g.list);
  g.head.onclick = g.rail.onclick = () => {
    if (!g.classList.contains('disabled')) toggleGroup(name);
  };
  groupEls.set(name, g);
  return g;
}

function command(text) {
  const box = el('div', 'cmd');
  const copy = el('button', 'icon-btn');
  copy.type = 'button';
  copy.innerHTML = COPY;
  copy.setAttribute('aria-label', `Copy ${text}`);
  copy.dataset.tip = 'Copy';
  copy.onclick = () => {
    navigator.clipboard?.writeText(text).catch(() => {});
    copy.dataset.tip = 'Copied';
    refreshTip();
    setTimeout(() => { copy.dataset.tip = 'Copy'; }, 1500);
  };
  box.append(el('span', null, text), copy);
  return box;
}

// note is what a group shows under its header when it has no rows to show, or nothing.
function note(m, count) {
  switch (m.state) {
    case 'connected':
      return count ? [] : [el('p', null, m.message || 'No running agents')];
    case 'needs-login':
      return [el('p', null, 'Log in from a terminal:'), command(`kijk machine login ${m.name}`)];
    case 'unreachable':
    case 'reconnecting':
      return count ? [] : [el('p', null, 'Trying again in a moment.'), el('p', 'said', m.message)];
    case 'error':
    case 'install-failed':
      return [el('p', null, m.message)];
  }
  return [];
}

function groupItem(m, own, open, items) {
  const local = m.name === '';
  const off = m.state === 'disabled';
  const live = m.state === 'connected';
  const [dot, word] = STATES[m.state] || ['error', m.state];
  const g = groupEl(m.name);
  g.classList.toggle('open', open);
  g.classList.toggle('disabled', off);
  const icon = (local ? LAPTOP : SERVER) + (local ? '' : `<span class="mdot ${dot}"></span>`);
  if (g.icon.dataset.icon !== icon) {
    g.icon.dataset.icon = icon;
    g.icon.innerHTML = g.railIcon.innerHTML = icon; // fixed strings, never server text
  }
  g.name.textContent = local ? 'Local' : m.name;
  const agents = `${own.length} agent${own.length === 1 ? '' : 's'}`;
  const tip = local ? `Local\nThis computer · ${agents}`
    : off ? `${m.name}\nDisabled · kijk does not connect to it\nkijk machine enable ${m.name} enables it again`
    : [`${m.name}\n${word[0].toUpperCase()}${word.slice(1)}${live ? `${m.system ? ` · ${m.system}` : ''} · ${agents}` : ''}`, live ? '' : m.message].filter(Boolean).join('\n');
  g.head.dataset.tip = g.rail.dataset.tip = tip;
  g.head.setAttribute('aria-label', `${local ? 'Local' : m.name}, ${local ? 'this computer' : word}, ${agents}`);
  if (off) {
    g.head.removeAttribute('aria-expanded');
    g.head.setAttribute('aria-disabled', 'true');
  } else {
    g.head.setAttribute('aria-expanded', open);
    g.head.removeAttribute('aria-disabled');
  }
  const right = [];
  if (!local && !live) right.push(el('span', null, word));
  if (!open && own.length) {
    const dots = el('span', 'mact');
    dots.setAttribute('aria-hidden', 'true');
    for (const r of own.slice(0, 6)) dots.append(el('i', r.stale ? '' : r.busy ? 'busy' : finished.has(r.key) ? 'fin' : ''));
    if (own.length > 6) dots.append(el('b', null, `+${own.length - 6}`));
    right.push(dots);
  }
  g.right.replaceChildren(...right);
  const lines = open ? note(local ? { state: 'connected', message: noAgents } : m, own.length) : [];
  g.note.replaceChildren(...lines);
  g.note.hidden = !lines.length;
  sync(g.list, open ? items : []);
  return g;
}

// sync puts items into list, touching the page only when the order changed.
function sync(list, items) {
  const same = items.length === list.children.length && items.every((li, i) => list.children[i] === li);
  if (!same) list.replaceChildren(...items);
}

// render draws the list that shows: agents grouped by machine once one is saved, or saved folders.
function render() {
  numbered = [];
  const number = r => {
    if (r.stale || r.saved?.missing || numbered.length >= 9) return 0;
    numbered.push(r);
    return numbered.length;
  };
  const shown = listRows(rows, list);
  if (list === 'folders') {
    sync(rowsEl, shown.map(r => folderItem(r, number(r))));
  } else if (!machines) {
    sync(rowsEl, shown.map(r => rowItem(r, number(r))));
    groupEls.clear();
  } else {
    const groups = [{ name: '', state: 'connected' }, ...machines];
    sync(rowsEl, groups.map(m => {
      const own = shown.filter(r => r.machine === m.name);
      const open = m.state !== 'disabled' && !collapsed.has(m.name);
      return groupItem(m, own, open, own.map(r => rowItem(r, open ? number(r) : 0)));
    }));
    for (const name of groupEls.keys()) if (!groups.some(m => m.name === name)) groupEls.delete(name);
  }
  for (const [l, m] of Object.entries(els)) {
    for (const key of m.keys()) if (!rows.has(key) || !inList(rows.get(key), l)) m.delete(key);
  }
  refreshTip();
}

// open shows a row's folder at cwd, by default where the list that shows opens it. again opens it
// anew when it is open already, for kijk <folder>.
function open(r, again, cwd = cwdIn(r, list)) {
  if ((r.key === openKey && !again) || r.stale || r.saved?.missing) return;
  openRow = r;
  finished.delete(r.key);
  openFolder({ ...r, cwd }, again);
  document.title = `${r.repo} — kijk`;
  render();
}

// refreshRow asks for the open folder's row now, so its line counts follow live updates. Once a
// second at most: a build can change many files at once.
let rowTimer = 0;
function refreshRow() {
  if (rowTimer) return;
  rowTimer = setTimeout(() => {
    rowTimer = 0;
    if (openKey) post('folders/refresh', { root: openKey }).catch(() => {});
  }, 1000);
}

// closeOpen closes the open folder and gives the page its title back.
function closeOpen() {
  closeFolder();
  openRow = null;
  document.title = 'kijk';
}

// onGone closes the open folder when kijk refuses to show it any more, as after a restart once its
// agent ended.
function onGone() {
  const { repo } = openRow;
  closeOpen();
  showSaid(`${repo} is no longer in kijk.`);
}

// followRule applies the close rule to the open folder, and passes its machine to the view.
function followRule() {
  if (!openKey) return;
  if (closeRule(openKey, rows, machines) === 'close') return closeOpen();
  const m = openRow.machine && machines?.find(x => x.name === openRow.machine);
  if (m) setMachine(m);
}

// setList shows another list. It draws only: no request, and the open folder stays open.
function setList(l) {
  if (!LISTS.includes(l) || l === list) return;
  list = l;
  drawList();
}

function drawList() {
  try { localStorage.setItem('kijk.list', list); } catch { /* storage blocked */ }
  const name = list === 'agents' ? 'Agents' : 'Folders';
  railEl.setAttribute('aria-label', name);
  railEl.querySelector('header span').textContent = name;
  document.querySelector('.split[data-var="--rail"]').setAttribute('aria-label', `Resize ${list} pane`);
  collapseRail(document.body.classList.contains('rail-collapsed'));
  showListStatus();
  showStart();
  render();
}

// Ctrl+Alt+0 switches lists, and Ctrl+Alt+1…9 open rows 1 to 9 of the list that shows. Not while
// typing, where AltGr sends Ctrl+Alt on Windows. e.code, because Alt changes e.key on macOS.
document.addEventListener('keydown', e => {
  const n = /^Digit([0-9])$/.exec(e.code)?.[1];
  if (!n || !e.ctrlKey || !e.altKey || e.metaKey || e.shiftKey || typing(e.target)) return;
  if (n === '0') setList(list === 'agents' ? 'folders' : 'agents');
  else if (numbered[n - 1]) open(numbered[n - 1]);
  else return;
  e.preventDefault();
});

function collapseRail(on) {
  document.body.classList.toggle('rail-collapsed', on);
  railBtn.dataset.tip = `${on ? 'Show' : 'Hide'} ${list}`;
  railBtn.setAttribute('aria-label', railBtn.dataset.tip);
  refreshTip();
}
railBtn.onclick = () => {
  const on = !document.body.classList.contains('rail-collapsed');
  collapseRail(on);
  try { localStorage.setItem('kijk.railCollapsed', on ? '1' : '0'); } catch { /* storage blocked */ }
};
// A narrow window folds the rail for the code's sake, without changing the saved choice.
const narrow = matchMedia('(max-width: 900px)');
const fitRail = () => {
  let saved = false;
  try { saved = localStorage.getItem('kijk.railCollapsed') === '1'; } catch { /* storage blocked */ }
  collapseRail(narrow.matches || saved);
};
narrow.onchange = fitRail;
fitRail();

// Pane borders move by drag or arrow keys; widths are kept per browser.
for (const handle of document.querySelectorAll('.split')) {
  const name = handle.dataset.var;
  const pane = handle.previousElementSibling;
  const setWidth = w => {
    const max = pane.offsetWidth + viewer.offsetWidth - 300;
    const px = Math.round(Math.max(120, Math.min(w, max)));
    document.body.style.setProperty(name, `${px}px`);
    handle.setAttribute('aria-valuenow', px);
    handle.setAttribute('aria-valuemax', Math.round(max));
  };
  const save = () => {
    try { localStorage.setItem(`kijk${name}`, document.body.style.getPropertyValue(name)); } catch { /* storage blocked */ }
  };
  try {
    const w = parseFloat(localStorage.getItem(`kijk${name}`));
    if (w) setWidth(w);
  } catch { /* storage blocked */ }
  handle.setAttribute('aria-valuemin', 120);
  handle.onfocus = () => {
    handle.setAttribute('aria-valuenow', Math.round(pane.offsetWidth));
    handle.setAttribute('aria-valuemax', Math.round(pane.offsetWidth + viewer.offsetWidth - 300));
  };
  handle.onfocus();
  handle.onpointerdown = e => {
    e.preventDefault();
    handle.setPointerCapture(e.pointerId);
    const offset = pane.offsetWidth - e.clientX;
    handle.onpointermove = m => setWidth(offset + m.clientX);
    handle.onpointerup = handle.onpointercancel = handle.onlostpointercapture = () => {
      if (!handle.onpointermove) return;
      handle.onpointermove = null;
      save();
    };
  };
  handle.onkeydown = e => {
    const step = { ArrowLeft: -16, ArrowRight: 16 }[e.key];
    if (!step) return;
    e.preventDefault();
    setWidth(pane.offsetWidth + step);
    save();
  };
}

// One stream for the window: every message holds every row of both lists.
const events = new EventSource('/api/events');
events.onopen = () => { openSeq = null; };
events.onmessage = e => {
  const msg = JSON.parse(e.data);
  showKijk(msg.kijk);
  machines = msg.machines || null;
  scanned = msg.scanned;
  listError = msg.listError || '';
  showAgentErrors(agentErrors(msg.errors, machines));
  noAgents = msg.noneInstalled ? NONE_INSTALLED : 'No running agents';
  const next = keyed(msg.rows, machines);
  finish(rows, next, openKey, finished);
  rows = next;
  takeRequest(msg.open);
  followRule();
  const last = !openKey && rows.get(reopen);
  if (last) open(last);
  showListStatus();
  showStart();
  render();
};

// takeRequest acts once on each open request that kijk <folder>, kijk folders and kijk <PR link> send.
function takeRequest(req) {
  const fresh = openSeq === null ? wantSeq > 0 && req.seq >= wantSeq : req.seq > openSeq;
  openSeq = req.seq;
  wantSeq = 0;
  if (!fresh) return;
  const t = target(req, rows, list);
  setList(t.list);
  if (!t.row) return;
  open(t.row, true, req.cwd || cwdIn(t.row, list));
  if (req.pr) openFolderPR(req.pr);
  flash(els[t.list].get(t.row.key));
  announce(`${req.added ? 'Added' : 'Opened'} ${t.row.repo}`);
}

// flash shows once which row a command opened.
function flash(li) {
  if (!li) return;
  li.classList.remove('arrived');
  void li.offsetWidth; // restarts the flash when the row flashed a moment ago
  li.classList.add('arrived');
  li.onanimationend = () => li.classList.remove('arrived');
  li.scrollIntoView({ block: 'nearest' });
}

// showListStatus says why the list that shows is empty, or that the folders list was not saved.
function showListStatus() {
  if (list === 'folders') return showStatus(listError || (listRows(rows, list).length ? '' : 'No folders yet'), !!listError);
  // With machines, an empty group says so under its own header.
  showStatus(!scanned ? 'Looking for agents…' : listRows(rows, list).length || machines ? '' : noAgents);
}

// andList joins words as "a, b and c".
function andList(words) {
  return words.length > 1 ? `${words.slice(0, -1).join(', ')} and ${words.at(-1)}` : words.join('');
}

// typeRank orders agent types as kijk does; an unknown type comes last.
function typeRank(name) {
  const i = agentTypes.findIndex(a => a.name === name);
  return i < 0 ? agentTypes.length : i;
}

// agentErrors groups the errors of the local and remote machines by agent type, in kijk's order.
function agentErrors(local, machines) {
  const all = [...(local || []).map(e => ({ type: e.type, reason: e.error })),
    ...(machines || []).flatMap(m => (m.errors || []).map(e => ({ type: e.type, reason: `${m.name}: ${e.error}` })))];
  return [...new Set(all.map(e => e.type))].sort((a, b) => typeRank(a) - typeRank(b))
    .map(t => ({ label: agentType(t).label, reasons: all.filter(e => e.type === t).map(e => e.reason) }));
}

function openHint(n) {
  return n ? `${mac ? '⌃⌥' : 'Ctrl+Alt+'}${n} to open` : '';
}

const startText = start.querySelector('p');
const startHelp = el('div', 'help');
startHelp.append(command('kijk ~/code/my-project'), el('p', 'hint', 'Run it in a terminal to add a folder.'));
start.append(startHelp);

function showStart() {
  const any = listRows(rows, list).some(r => !r.stale);
  if (list === 'folders') startText.textContent = any ? 'Select a folder' : 'No folders yet';
  else startText.textContent = any ? 'Select an agent' : 'Start a coding agent to see its work here';
  startHelp.hidden = list !== 'folders' || any;
}

let undo = null; // the last removal while its notice shows: { row, at, wasOpen, timer }

// removeFolder takes a row off the list at once, with an Undo notice instead of a question.
async function removeFolder(r) {
  if (!r) return;
  const at = listRows(rows, 'folders').findIndex(x => x.key === r.key);
  try {
    await post('folders/remove', { root: r.root });
  } catch (err) {
    announce(err.message);
    return;
  }
  // A folder where agents work stays in the Agents list, and stays open.
  const wasOpen = r.key === openKey && !r.agents;
  const hadFocus = els.folders.get(r.key)?.contains(document.activeElement);
  if (r.agents) rows.set(r.key, { ...r, saved: undefined });
  else rows.delete(r.key);
  if (wasOpen) {
    closeOpen();
    reopen = null;
    try { localStorage.removeItem(lastKey); } catch { /* storage blocked */ }
  }
  render();
  showListStatus();
  showStart();
  if (hadFocus) rowsEl.children[Math.min(at, rowsEl.children.length - 1)]?.focus();
  clearTimeout(undo?.timer);
  undo = { row: r, at, wasOpen, timer: setTimeout(endUndo, 10000) };
  showUndo({ name: r.repo, root: r.root, onUndo: undoRemove });
  announce(`Removed ${r.repo}. Press ${mac ? 'Command' : 'Control'} Z to undo.`);
}

function endUndo() {
  clearTimeout(undo?.timer);
  undo = null;
  showUndo(null);
}

async function undoRemove() {
  const u = undo;
  if (!u) return;
  endUndo();
  try {
    await post('folders/restore', { root: u.row.root });
  } catch (err) {
    announce(err.message);
    return;
  }
  if (!rows.get(u.row.key)?.saved) rows.set(u.row.key, u.row);
  showListStatus();
  showStart();
  if (u.wasOpen) open(u.row);
  render();
  announce(`${u.row.repo} is back in the list`);
}

// Cmd+Z or Ctrl+Z undoes a removal while its notice shows; a text field keeps its own undo.
document.addEventListener('keydown', e => {
  if (!undo || e.code !== 'KeyZ' || e.shiftKey || e.altKey || e.metaKey !== mac || e.ctrlKey === mac) return;
  if (e.target.closest?.('textarea, input')) return;
  e.preventDefault();
  undoRemove();
});

drawList();

events.onerror = () => {
  if (!restarting()) showStatus('Lost connection to kijk. Retrying…', true);
};
setInterval(() => {
  render();
  showFetched();
}, 15000);
