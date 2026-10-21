import { el, get, post, folderKey } from './api.js';
import { refreshTip, tipSide } from './tip.js';
import { openKey, openFolder, closeFolder, setMachine, setupFolder, showFetched, SERVER } from './folder.js';
import { ago, lineCount } from './git.js';
import { mac } from './keys.js';
import { showKijk, showAgentErrors, showUndo, restarting } from './status.js';

// The agent types kijk knows, in its order. They come from the local kijk, also for remote rows.
const agentTypes = await get('agenttypes');
// agentType finds a type by name. An unknown type shows its name and no icon.
const agentType = name => agentTypes.find(a => a.name === name) || { label: name, icon: '' };
// What the Agents list says when the tool of no agent type is on this machine.
const NONE_INSTALLED = `No running agents · kijk shows ${andList(agentTypes.map(a => a.label))} agents`;

// The page is the agents window, or at /?window=folders the folders window: same views, other rows.
const params = new URLSearchParams(location.search);
const foldersMode = params.get('window') === 'folders';
const TITLE = foldersMode ? 'kijk · Folders' : 'kijk';
const PANE = foldersMode ? 'folders' : 'agents';
let arriving = params.get('open'); // a folder kijk <folder> just added, to open and flash
if (arriving) history.replaceState(null, '', '/?window=folders');
let openSeq = null; // the last open request seen; null until the first message of a connection
let listError = '';
let noAgents = 'No running agents'; // what the Agents list says for the local machine when it has none

const rowsEl = document.getElementById('rows');
const statusEl = document.getElementById('status');
const railBtn = document.getElementById('rail-toggle');
const viewer = document.getElementById('viewer');
const start = document.getElementById('start');
let rows = []; // every row in the list, local and remote, each with its folder key and machine
let machines = null; // the saved remote machines from the last message; null when none is saved
let numbered = []; // the rows that have a Ctrl+Alt number, in order
let openMachine = ''; // the machine of the open folder
const finished = new Set(); // folders whose agent went idle since the user last opened them
const collapsed = new Set(); // machine groups folded by the user; '' is Local
try { for (const g of JSON.parse(localStorage.getItem('kijk.collapsed')) || []) collapsed.add(g); } catch { /* storage blocked */ }
const lastKey = foldersMode ? 'kijk.lastFolder' : 'kijk.last';
let reopen = null; // the folder open before a reload, opened again once its row shows up
try { reopen = localStorage.getItem(lastKey); } catch { /* storage blocked */ }

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
setupFolder({ lastKey, start, announce, agentIcon, onFolderEvent: () => { if (foldersMode) refreshRow(); } });
tipSide('#rail', 'right');
tipSide('#statusbar', 'above');

function showStatus(text, isError) {
  statusEl.hidden = !text;
  statusEl.textContent = text || '';
  statusEl.classList.toggle('error', !!isError);
  announce(text);
}

// Row elements are kept and updated in place, so a click is not lost when the list refreshes.
const rowEls = new Map();

function rowEl(key) {
  let li = rowEls.get(key);
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
  li.agent = el('span', 'agents');
  li.where = el('span', 'where');
  li.repo.append(li.where);
  li.badge.append(li.initials, li.key);
  top.append(li.agent, li.badge, li.names, li.done, li.when);
  li.append(top, li.repo);
  rowEls.set(key, li);
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
  const agents = r.agents || [];
  const types = [...new Set(agents.map(a => a.type))];
  // Only the icon of a type with a Busy agent pulses.
  const working = t => agents.some(a => a.type === t && a.busy);
  const icons = types.map(t => `${t}${working(t) ? '+' : ''}`).join(' ');
  if (li.agent.dataset.kind !== icons) {
    li.agent.dataset.kind = icons;
    li.agent.replaceChildren(...types.map(t => {
      const { icon: svg, color } = agentType(t);
      const icon = el('span', `agent${working(t) ? ' on' : ''}`);
      icon.innerHTML = svg; // from the local kijk's own files, never a remote machine's text
      if (color) icon.style.setProperty('--agent-color', color);
      return icon;
    }));
    li.style.setProperty('--icons', Math.max(types.length, 1));
  }
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

const FOLDER = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z"/></svg>';
const REMOVE = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M18 6 6 18"/><path d="m6 6 12 12"/></svg>';

function folderEl(key) {
  let li = rowEls.get(key);
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
  const icon = el('span', 'ficon');
  icon.innerHTML = FOLDER;
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
  top.append(icon, li.badge, li.names, li.lc, li.rm);
  li.append(top, repo);
  rowEls.set(key, li);
  return li;
}

function folderItem(r, n) {
  const li = folderEl(r.key);
  li.row = r;
  li.classList.toggle('selected', r.key === openKey);
  li.classList.toggle('missing', !!r.missing);
  li.toggleAttribute('aria-disabled', !!r.missing);
  li.initials.textContent = initials(r.repo);
  li.badge.style.setProperty('--hue', hue(r.root));
  li.key.textContent = n || '';
  li.names.textContent = r.repo;
  const lc = (!r.missing && lineCount(r.add, r.del)) || el('span', 'lc');
  li.lc.replaceWith(lc);
  li.lc = lc;
  if (r.missing) li.where.replaceChildren(el('span', 'gone', 'missing'), ` · ${r.parent}`);
  else li.where.textContent = r.branch ? `${r.branch} · ${r.parent}` : r.parent;
  const changed = r.add || r.del ? `+${r.add} −${r.del} not committed` : r.branch ? 'Nothing to commit' : '';
  const key = openHint(n);
  li.dataset.tip = r.missing
    ? [r.repo, r.root, 'Missing: this folder is not on disk.', 'It comes back when the folder does.'].join('\n')
    : [r.repo, r.branch ? `${r.repo} · ${r.branch}` : 'Not a git repository', changed, r.cwd ? `${r.root} · opens at ${r.cwd}/` : r.root, key]
      .filter(Boolean).join('\n');
  li.setAttribute('aria-label', r.missing ? `${r.repo}, missing, ${r.parent}`
    : [r.repo, r.branch || 'not a git repository', r.parent, changed].filter(Boolean).join(', '));
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

function render() {
  numbered = [];
  const number = r => {
    if (r.stale || r.missing || numbered.length >= 9) return 0;
    numbered.push(r);
    return numbered.length;
  };
  if (foldersMode) {
    sync(rowsEl, rows.map(r => folderItem(r, number(r))));
  } else if (!machines) {
    sync(rowsEl, rows.map(r => rowItem(r, number(r))));
    groupEls.clear();
  } else {
    const groups = [{ name: '', state: 'connected' }, ...machines];
    sync(rowsEl, groups.map(m => {
      const own = rows.filter(r => r.machine === m.name);
      const open = m.state !== 'disabled' && !collapsed.has(m.name);
      return groupItem(m, own, open, own.map(r => rowItem(r, open ? number(r) : 0)));
    }));
    for (const name of groupEls.keys()) if (!groups.some(m => m.name === name)) groupEls.delete(name);
  }
  for (const key of rowEls.keys()) if (!rows.some(r => r.key === key)) rowEls.delete(key);
  refreshTip();
}

// open shows a row's folder. again opens it anew when it is open already, for kijk <folder>.
function open(r, again) {
  if ((r.key === openKey && !again) || r.stale || r.missing) return;
  openMachine = r.machine;
  finished.delete(r.key);
  openFolder(r, again);
  document.title = foldersMode ? TITLE : `${r.repo} — kijk`;
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
  openMachine = '';
  document.title = TITLE;
}

// followMachine passes the open remote folder's machine to the view, and closes the folder when the
// machine is disabled or removed.
function followMachine() {
  if (!openKey || !openMachine) return;
  const m = machines?.find(x => x.name === openMachine);
  if (!m || m.state === 'disabled') return closeOpen();
  setMachine(m);
}

// Ctrl+Alt+1…9 opens the folder of rows 1 to 9. e.code, because Alt changes e.key on macOS.
document.addEventListener('keydown', e => {
  const n = /^Digit([1-9])$/.exec(e.code)?.[1];
  const r = n && numbered[n - 1];
  if (!r || !e.ctrlKey || !e.altKey || e.metaKey || e.shiftKey) return;
  e.preventDefault();
  open(r);
});

function collapseRail(on) {
  document.body.classList.toggle('rail-collapsed', on);
  railBtn.dataset.tip = `${on ? 'Show' : 'Hide'} ${PANE}`;
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

const events = new EventSource(foldersMode ? '/api/events?window=folders' : '/api/events');
events.onopen = () => { openSeq = null; };
events.onmessage = e => {
  const msg = JSON.parse(e.data);
  showKijk(msg.kijk);
  if (foldersMode) return showFolders(msg);
  machines = msg.machines || null;
  showAgentErrors(agentErrors(msg.errors, machines));
  noAgents = msg.noneInstalled ? NONE_INSTALLED : 'No running agents';
  const next = (msg.rows || []).map(r => ({ ...r, key: r.root, machine: '' }));
  for (const m of machines || []) {
    for (const r of m.rows) next.push({ ...r, key: folderKey(m.name, r.root), machine: m.name, stale: m.stale });
  }
  for (const r of next) {
    if (r.stale) continue;
    if (r.busy) finished.delete(r.key);
    else if (r.key !== openKey && rows.find(x => x.key === r.key)?.busy) finished.add(r.key);
  }
  rows = next;
  showStart();
  // With machines, an empty group says so under its own header.
  showStatus(rows.length || machines ? '' : noAgents);
  followMachine();
  const last = !openKey && rows.find(r => r.key === reopen && !r.stale);
  if (last) open(last);
  else render();
};

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

function showFolders(msg) {
  rows = (msg.folders || []).map(r => ({ ...r, key: r.root, machine: '', names: [r.repo] }));
  const req = msg.open || { seq: 0 };
  if (openSeq !== null && req.seq > openSeq && req.root) arriving = req.root;
  openSeq = req.seq;
  listError = msg.error || '';
  const current = openKey && rows.find(r => r.key === openKey);
  if (openKey && (!current || current.missing)) closeOpen(); // removed in another window, or gone from disk
  showList();
  if (arriving) {
    arrive(arriving);
    arriving = null;
    return;
  }
  const last = !openKey && rows.find(r => r.key === reopen && !r.missing);
  if (last) open(last);
  else render();
}

// arrive opens a folder that kijk <folder> added, and flashes its row once.
function arrive(root) {
  const r = rows.find(x => x.key === root && !x.missing);
  if (!r) return render();
  open(r, true);
  const li = rowEls.get(r.key);
  li.classList.remove('arrived');
  void li.offsetWidth; // restarts the flash when the row flashed a moment ago
  li.classList.add('arrived');
  li.onanimationend = () => li.classList.remove('arrived');
  li.scrollIntoView({ block: 'nearest' });
  announce(`Added ${r.repo}`);
}

function showList() {
  if (listError) showStatus(listError, true);
  else showStatus(rows.length ? '' : 'No folders yet');
  showStart();
}

const startText = start.querySelector('p');
const startHelp = el('div', 'help');
if (foldersMode) {
  startHelp.append(command('kijk ~/code/my-project'), el('p', 'hint', 'Run it in a terminal to add a folder.'));
  start.append(startHelp);
}

function showStart() {
  if (foldersMode) {
    startText.textContent = rows.length ? 'Select a folder' : 'No folders yet';
    startHelp.hidden = !!rows.length;
  } else {
    startText.textContent = rows.some(r => !r.stale) ? 'Select an agent' : 'Start a coding agent to see its work here';
  }
}

let undo = null; // the last removal while its notice shows: { row, at, wasOpen, timer }

// removeFolder takes a row off the list at once, with an Undo notice instead of a question.
async function removeFolder(r) {
  if (!r) return;
  const at = rows.findIndex(x => x.key === r.key);
  try {
    await post('folders/remove', { root: r.root });
  } catch (err) {
    announce(err.message);
    return;
  }
  const wasOpen = r.key === openKey;
  const hadFocus = rowEls.get(r.key)?.contains(document.activeElement);
  rows = rows.filter(x => x.key !== r.key);
  if (wasOpen) {
    closeOpen();
    try { localStorage.removeItem(lastKey); } catch { /* storage blocked */ }
  }
  render();
  showList();
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
  if (!rows.some(x => x.key === u.row.key)) rows.splice(Math.min(u.at, rows.length), 0, u.row);
  showList();
  if (u.wasOpen) open(u.row);
  else render();
  announce(`${u.row.repo} is back in the list`);
}

// Cmd+Z or Ctrl+Z undoes a removal while its notice shows; a text field keeps its own undo.
document.addEventListener('keydown', e => {
  if (!undo || e.code !== 'KeyZ' || e.shiftKey || e.altKey || e.metaKey !== mac || e.ctrlKey === mac) return;
  if (e.target.closest?.('textarea, input')) return;
  e.preventDefault();
  undoRemove();
});

if (foldersMode) {
  document.title = TITLE;
  document.getElementById('rail').setAttribute('aria-label', 'Folders');
  document.querySelector('#rail header span').textContent = 'Folders';
  document.querySelector('.split[data-var="--rail"]').setAttribute('aria-label', 'Resize folders pane');
  showStart();
}

events.onerror = () => {
  if (!restarting()) showStatus('Lost connection to kijk. Retrying…', true);
};
setInterval(() => {
  render();
  showFetched();
}, 15000);
