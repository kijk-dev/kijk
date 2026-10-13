import { el, get, post, base, folderKey, splitKey, storeKey } from './api.js';
import { showTree, paint, refreshTree, openFolders, selectFile } from './tree.js';
import { refreshView, jumpChange, showMessage } from './view.js';
import { setTabs, tabState, openTab, activeTab, recentFiles, closeActive, goPlace } from './tabs.js';
import { refreshTip } from './tip.js';
import { showPR } from './pr.js';
import { marks, showChanges, showGraph, revealHead, reviewKey, ago, lineCount } from './git.js';
import { openFind, findAgain, openGotoLine, openGotoFile, openRecent } from './find.js';
import { onKey, keyLabel, keyNames } from './keys.js';
import { showKijk, restarting } from './status.js';

const rowsEl = document.getElementById('rows');
const statusEl = document.getElementById('status');
const filesEl = document.getElementById('files');
const gitEl = document.getElementById('git');
const tabsEl = document.getElementById('tabs');
const sidebarHead = document.getElementById('sidebar-head');
const folderName = document.getElementById('folder-name');
const gitNote = document.getElementById('git-note');
const changesEl = document.getElementById('changes');
const commitForm = document.getElementById('commit');
const messageEl = document.getElementById('message');
const commitBtn = document.getElementById('commit-btn');
const outputEl = document.getElementById('git-output');
const branchEl = document.getElementById('branch-name');
const aheadBtn = document.getElementById('ahead');
const totalEl = document.getElementById('branch-total');
const pushBtn = document.getElementById('push-btn');
const graphEl = document.getElementById('graph');
const behindEl = document.getElementById('behind');
const syncEl = document.getElementById('syncline');
const syncText = document.getElementById('sync-text');
const fetchBtn = document.getElementById('fetch-btn');
const fetchedEl = el('span');
const prLine = document.getElementById('prline');
let pr = null; // pull request status of the open folder
const prs = new Map(); // last status per folder, shown at once when a folder opens again

// cachedPR also looks in localStorage, so the last status survives a reload.
function cachedPR(key) {
  if (!prs.has(key)) {
    try { prs.set(key, JSON.parse(localStorage.getItem(`kijk.pr:${storeKey(key)}`))); } catch { prs.set(key, null); }
  }
  return prs.get(key);
}
let prCall = 0;
let prTimer = 0;
let upHash = null; // where the upstream was, so a push, also the agent's own, refreshes the PR
const pushAfterEl = document.getElementById('push-after');
const pushAfterText = document.getElementById('push-after-text');
const railBtn = document.getElementById('rail-toggle');
const gotoBtn = document.getElementById('goto-btn');
const drafts = new Map(); // unsent commit message per folder
let busy = false;
let git = null; // last status of the open folder
const viewer = document.getElementById('viewer');
const staleBar = document.getElementById('stalebar');
const staleText = document.getElementById('stale-text');
let rows = []; // every row in the list, local and remote, each with its folder key and machine
let machines = null; // the saved remote machines from the last message; null when none is saved
let numbered = []; // the rows that have a Ctrl+Alt number, in order
let openKey = null; // stays open when its session ends, until the user picks another row
let openMachine = '';
let offline = false; // the open folder's machine is not connected, so the view is frozen
let folderEvents = null;
const finished = new Set(); // folders whose agent went idle since the user last opened them
const collapsed = new Set(); // machine groups folded by the user; '' is Local
try { for (const g of JSON.parse(localStorage.getItem('kijk.collapsed')) || []) collapsed.add(g); } catch { /* storage blocked */ }
const mac = /Mac/.test(navigator.platform);
let sidebar = 'files'; // the sidebar tab chosen for the open folder
let reopen = null; // the folder open before a reload, opened again once its row shows up
try { reopen = localStorage.getItem('kijk.last'); } catch { /* storage blocked */ }

// Per-folder state: file tabs, expanded tree folders and sidebar tab. It survives reloads and restarts.
function loadState(key) {
  try { return JSON.parse(localStorage.getItem(`kijk.folder:${storeKey(key)}`)) || {}; } catch { return {}; }
}

function saveState() {
  if (!openKey) return;
  try {
    localStorage.setItem(`kijk.folder:${storeKey(openKey)}`, JSON.stringify({ ...tabState(), open: openFolders(), sidebar }));
    localStorage.setItem('kijk.last', openKey);
  } catch { /* storage blocked */ }
}

function tabsChanged() {
  const t = activeTab();
  selectFile(t?.kind === 'file' ? t.path : null);
  saveState();
}

// announce reads a message to screen readers; the region is always in the page, so it is heard.
const announcer = document.getElementById('announce');
function announce(text) {
  if (text && announcer.textContent !== text) announcer.textContent = text;
}

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
  li.agent = el('span', 'agent');
  li.where = el('span', 'where');
  li.repo.append(li.where);
  li.badge.append(li.initials, li.key);
  top.append(li.agent, li.badge, li.names, li.done, li.when);
  li.append(top, li.repo);
  rowEls.set(key, li);
  return li;
}

// Known agents: the label for tooltips and an inline icon. An unknown agent shows its name only.
const AGENTS = {
  // The Claude mark, from Simple Icons (CC0).
  claude: { label: 'Claude Code', icon: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="m4.7144 15.9555 4.7174-2.6471.079-.2307-.079-.1275h-.2307l-.7893-.0486-2.6956-.0729-2.3375-.0971-2.2646-.1214-.5707-.1215-.5343-.7042.0546-.3522.4797-.3218.686.0608 1.5179.1032 2.2767.1578 1.6514.0972 2.4468.255h.3886l.0546-.1579-.1336-.0971-.1032-.0972L6.973 9.8356l-2.55-1.6879-1.3356-.9714-.7225-.4918-.3643-.4614-.1578-1.0078.6557-.7225.8803.0607.2246.0607.8925.686 1.9064 1.4754 2.4893 1.8336.3643.3035.1457-.1032.0182-.0728-.164-.2733-1.3539-2.4467-1.445-2.4893-.6435-1.032-.17-.6194c-.0607-.255-.1032-.4674-.1032-.7285L6.287.1335 6.6997 0l.9957.1336.419.3642.6192 1.4147 1.0018 2.2282 1.5543 3.0296.4553.8985.2429.8318.091.255h.1579v-.1457l.1275-1.706.2368-2.0947.2307-2.6957.0789-.7589.3764-.9107.7468-.4918.5828.2793.4797.686-.0668.4433-.2853 1.8517-.5586 2.9021-.3643 1.9429h.2125l.2429-.2429.9835-1.3053 1.6514-2.0643.7286-.8196.85-.9046.5464-.4311h1.0321l.759 1.1293-.34 1.1657-1.0625 1.3478-.8804 1.1414-1.2628 1.7-.7893 1.36.0729.1093.1882-.0183 2.8535-.607 1.5421-.2794 1.8396-.3157.8318.3886.091.3946-.3278.8075-1.967.4857-2.3072.4614-3.4364.8136-.0425.0304.0486.0607 1.5482.1457.6618.0364h1.621l3.0175.2247.7892.522.4736.6376-.079.4857-1.2142.6193-1.6393-.3886-3.825-.9107-1.3113-.3279h-.1822v.1093l1.0929 1.0686 2.0035 1.8092 2.5075 2.3314.1275.5768-.3218.4554-.34-.0486-2.2039-1.6575-.85-.7468-1.9246-1.621h-.1275v.17l.4432.6496 2.3436 3.5214.1214 1.0807-.17.3521-.6071.2125-.6679-.1214-1.3721-1.9246L14.38 17.959l-1.1414-1.9428-.1397.079-.674 7.2552-.3156.3703-.7286.2793-.6071-.4614-.3218-.7468.3218-1.4753.3886-1.9246.3157-1.53.2853-1.9004.17-.6314-.0121-.0425-.1397.0182-1.4328 1.9672-2.1796 2.9446-1.7243 1.8456-.4128.164-.7164-.3704.0667-.6618.4008-.5889 2.386-3.0357 1.4389-1.882.929-1.0868-.0062-.1579h-.0546l-6.3385 4.1164-1.1293.1457-.4857-.4554.0608-.7467.2307-.2429 1.9064-1.3114Z"/></svg>' },
};

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
  const key = n ? `${mac ? '⌃⌥' : 'Ctrl+Alt+'}${n} to open` : '';
  li.initials.textContent = initials(r.repo);
  li.badge.style.setProperty('--hue', hue(r.machine + r.root));
  li.key.textContent = n || '';
  li.names.textContent = r.names.join(', ');
  li.when.textContent = ago(r.lastActivity);
  const agent = AGENTS[r.agent] || { label: r.agent, icon: '' };
  if (li.agent.dataset.kind !== r.agent) {
    li.agent.dataset.kind = r.agent;
    li.agent.className = `agent ${r.agent}`;
    li.agent.innerHTML = agent.icon; // a fixed string from AGENTS, never server text
  }
  li.where.textContent = where;
  li.dataset.tip = [r.names.join(', '), where, `${agent.label} · ${state} · ${ago(r.lastActivity)}`, r.machine ? `${r.machine}:${r.root}` : r.root, key].filter(Boolean).join('\n');
  li.setAttribute('aria-label', `${r.names.join(', ')}, ${agent.label}, ${where}, ${state}`);
  return li;
}

const CHEVRON = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="m9 18 6-6-6-6"/></svg>';
const LAPTOP = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M20 16V7a2 2 0 0 0-2-2H6a2 2 0 0 0-2 2v9m16 0H4m16 0 1.28 2.55a1 1 0 0 1-.9 1.45H3.62a1 1 0 0 1-.9-1.45L4 16"/></svg>';
const SERVER = '<svg viewBox="0 0 24 24" aria-hidden="true"><rect x="3" y="4" width="18" height="7" rx="1.5"/><rect x="3" y="13" width="18" height="7" rx="1.5"/><path d="M7 7.5h.01M7 16.5h.01"/></svg>';
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
  const lines = open ? note(local ? { state: 'connected' } : m, own.length) : [];
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
    if (r.stale || numbered.length >= 9) return 0;
    numbered.push(r);
    return numbered.length;
  };
  if (!machines) {
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

function open(r) {
  if (r.key === openKey || r.stale) return;
  if (openKey) drafts.set(openKey, messageEl.value);
  saveState();
  openKey = r.key;
  openMachine = r.machine;
  offline = false;
  showOffline();
  finished.delete(r.key);
  const state = loadState(r.key);
  sidebar = state.sidebar || 'files';
  messageEl.value = drafts.get(r.key) || '';
  git = null;
  showOutput('');
  updateCommit();
  showTree(filesEl, r.key, {
    open: state.open,
    reveal: r.cwd,
    onFile: (path, pin) => { if (!offline) openTab({ kind: 'file', path }, pin); },
    onToggle: saveState,
  });
  setTabs(r.key, state, tabsChanged);
  tabsEl.hidden = true;
  sidebarHead.hidden = false;
  showFolderName(r);
  document.title = `${r.repo} — kijk`;
  gitNote.hidden = true;
  pr = cachedPR(r.key);
  upHash = null;
  showPR(prLine, pr);
  refreshPR();
  selectTab('files');
  refreshGit();
  render();
  followFolder();
}

// showFolderName names the open folder and, for a remote folder, its machine.
function showFolderName(r) {
  const parts = [r.repo];
  if (r.machine) {
    const on = el('span', 'onm');
    on.innerHTML = SERVER;
    on.append(r.machine);
    on.dataset.tip = `On ${r.machine}\n${r.root}`;
    parts.push(on);
  }
  folderName.replaceChildren(...parts);
  folderName.title = r.machine ? '' : r.root;
}

function followFolder() {
  folderEvents?.close();
  const { machine, root } = splitKey(openKey);
  folderEvents = new EventSource(`${base(machine)}/api/folder-events?` + new URLSearchParams({ root }));
  folderEvents.onmessage = e => {
    if (offline) return;
    const change = JSON.parse(e.data);
    refreshGit();
    refreshTree(change);
    refreshView(change);
  };
}

// closeFolder empties the viewer, as when kijk starts: for a folder whose machine was disabled.
function closeFolder() {
  if (openKey) drafts.set(openKey, messageEl.value);
  saveState();
  folderEvents?.close();
  folderEvents = null;
  openKey = null;
  openMachine = '';
  offline = false;
  showOffline();
  git = null;
  pr = null;
  setTabs(null, {}, () => {});
  showMessage(viewer, 'Select an agent');
  filesEl.replaceChildren();
  sidebarHead.hidden = true;
  tabsEl.hidden = true;
  gitNote.hidden = true;
  selectTab('files');
  document.title = 'kijk';
}

// followMachine keeps the open remote folder in step with its machine: frozen while the machine
// is away, reloaded when it is back, closed when it is disabled or removed.
function followMachine() {
  if (!openKey || !openMachine) return;
  const m = machines?.find(x => x.name === openMachine);
  if (!m || m.state === 'disabled') return closeFolder();
  const down = m.state !== 'connected';
  if (down !== offline) {
    offline = down;
    showOffline(m);
    if (!down) reload();
  } else if (down) {
    showOffline(m);
  }
}

function showOffline(m) {
  document.body.classList.toggle('offline', offline);
  staleBar.hidden = !offline;
  if (offline && m) {
    const name = el('b', null, m.name);
    const first = m.state === 'needs-login' ? [name, ` needs a login. Run kijk machine login ${m.name}`]
      : m.state === 'reconnecting' ? ['The connection to ', name, ' ended. Reconnecting…']
      : [name, ' is unreachable. Reconnecting…'];
    staleText.replaceChildren(...first, el('br'), el('span', 'then', 'Git actions and opening files are off until it is back.'));
  }
  updateCommit();
  updatePush();
}

// reload refreshes everything on screen after the open folder's machine is back.
function reload() {
  const all = { all: true, git: true, paths: [] };
  followFolder();
  refreshGit();
  refreshTree(all);
  refreshView(all);
  refreshPR();
}

// An open PR's target branch is what "behind", conflicts and the graph compare with.
const prTarget = () => (['open', 'draft'].includes(pr?.pr?.state) ? pr.pr.target : '');

// refreshPR asks gh or glab about this branch's pull request: on open, after a push, on Fetch,
// and every minute while CI runs.
async function refreshPR() {
  clearTimeout(prTimer);
  if (offline) return;
  const root = openKey, call = ++prCall;
  let res;
  try {
    res = await get('pr', { root });
  } catch {
    res = cachedPR(root); // keep what was shown
  }
  prs.set(root, res);
  try { localStorage.setItem(`kijk.pr:${storeKey(root)}`, JSON.stringify(res)); } catch { /* storage blocked */ }
  if (root !== openKey || call !== prCall) return;
  const before = prTarget();
  pr = res;
  showPR(prLine, pr);
  if (prTarget() !== before) refreshGit();
  if (pr?.pr?.ci === 'running') prTimer = setTimeout(refreshPR, 60000);
}

async function refreshGit() {
  if (offline) return; // keep what is on screen until the machine is back
  const root = openKey;
  let st;
  try {
    st = await get('status', { root, target: prTarget() });
  } catch (err) {
    st = { git: false, error: err.message };
  }
  if (root !== openKey || offline) return;
  tabsEl.hidden = !st.git;
  gitNote.hidden = !st.error;
  gitNote.textContent = st.error || '';
  if (!st.git) return selectTab('files');
  if (!git) selectTab(sidebar);
  git = st;
  if (upHash !== null && st.upHash !== upHash) refreshPR();
  upHash = st.upHash || '';
  paint(filesEl, marks(st));
  updateCommit();

  branchEl.textContent = st.branch || 'detached HEAD';
  const total = st.total && lineCount(st.total.add, st.total.del);
  if (total) {
    total.tabIndex = 0;
    total.dataset.tip = `Compared with ${st.total.base}\nEverything since this branch split from it, including work not committed yet.`;
  }
  totalEl.replaceChildren(...(total ? [total] : []));
  const canPush = !!st.branch && st.remote;
  aheadBtn.hidden = !st.ahead;
  aheadBtn.textContent = `↑${st.ahead}`;
  aheadBtn.title = `${st.ahead} not pushed yet${st.base ? ` (compared with ${st.base})` : ''}. Click to show in the graph.`;
  behindEl.hidden = !st.behind;
  behindEl.textContent = `↓${st.behind}`;
  behindEl.title = `${st.behind} on ${st.upstream} not in this branch yet`;
  showSync(st);
  pushBtn.hidden = !canPush;
  pushBtn.textContent = st.upstream ? 'Push' : 'Publish';
  pushBtn.title = `${st.upstream ? `Push to ${st.upstream}` : 'Publish this branch to origin'} (${keyLabel('push')})`;
  pushAfterEl.parentElement.hidden = !canPush;
  pushAfterText.textContent = st.upstream ? `Push to ${st.upstream}` : 'Publish to origin';
  updatePush();

  const openChange = (change, mode, pin) => !offline && openTab(mode === 'conflict'
    ? { kind: 'file', path: change.path }
    : { kind: 'diff', mode, path: change.path, orig: change.orig, hash: change.hash }, pin);
  showGraph(graphEl, root, openChange, prTarget());
  showChanges(changesEl, st, {
    onOpen: openChange,
    onStage: paths => run('stage', { paths }),
    onUnstage: paths => run('unstage', { paths }),
  });
}

// The sync line tells how this branch stands against main, and when the remote was last fetched.
function showSync(st) {
  const files = (what, list) => {
    const s = el('span', 'conflict', `${what} may conflict: ${list.slice(0, 3).join(', ')}${list.length > 3 ? ` +${list.length - 3}` : ''}`);
    s.title = list.join('\n');
    return s;
  };
  const parts = [];
  if (st.upConflicts) parts.push(files('Pull', st.upConflicts));
  if (st.main) {
    const behind = el('span', st.mainBehind ? 'modified' : null,
      st.mainBehind ? `${st.mainBehind} behind ${st.main}` : `Up to date with ${st.main}`);
    if (st.mainBehind) behind.title = `${st.main} has commits this branch does not. Rebase to get them.`;
    parts.push(behind);
    if (st.mainConflicts) parts.push(files('Rebase', st.mainConflicts));
  }
  if (st.remote) parts.push(fetchedEl);
  showFetched();
  syncText.replaceChildren(...parts);
  syncEl.hidden = !parts.length;
  fetchBtn.hidden = !st.remote;
}

function showFetched() {
  fetchedEl.textContent = git?.fetchedAt ? `fetched ${ago(git.fetchedAt)}` : 'never fetched';
}

function showOutput(text, ok) {
  outputEl.hidden = !text;
  outputEl.textContent = text;
  outputEl.classList.toggle('ok', !!ok);
  announce(text);
}

function updateCommit() {
  commitBtn.disabled = busy || offline || !git?.staged.length || !messageEl.value.trim();
  commitBtn.textContent = busy ? 'Working…' : 'Commit';
}

function updatePush() {
  pushBtn.disabled = busy || offline || !git || (!!git.upstream && !git.ahead);
  fetchBtn.disabled = busy || offline;
}

// run sends a git action for the open folder and refreshes. On failure it shows git's output.
async function run(kind, body = {}) {
  if (busy || offline) return null;
  busy = true;
  updateCommit();
  updatePush();
  showOutput('');
  const root = openKey;
  let res = null;
  try {
    res = await post(kind, { root, ...body });
  } catch (err) {
    if (root === openKey) showOutput(err.message);
  }
  busy = false;
  updateCommit();
  updatePush();
  if (root === openKey) refreshGit();
  return res;
}

async function pushNow() {
  const res = await run('push');
  if (res) showOutput(res.output || 'Pushed', true);
}

pushBtn.onclick = pushNow;
fetchBtn.onclick = async () => {
  if (await run('fetch')) refreshPR();
};
aheadBtn.onclick = revealHead;
try { pushAfterEl.checked = localStorage.getItem('kijk.pushAfter') === '1'; } catch { /* storage blocked */ }
pushAfterEl.onchange = () => {
  try { localStorage.setItem('kijk.pushAfter', pushAfterEl.checked ? '1' : '0'); } catch { /* storage blocked */ }
};

messageEl.oninput = updateCommit;
messageEl.onkeydown = e => {
  if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) commitForm.requestSubmit();
};
commitForm.onsubmit = async e => {
  e.preventDefault();
  if (commitBtn.disabled) return;
  if (!(await run('commit', { message: messageEl.value }))) return;
  messageEl.value = '';
  drafts.delete(openKey);
  updateCommit();
  if (pushAfterEl.checked && !pushAfterEl.parentElement.hidden) await pushNow();
};

// Review keys: Arrow Up/Down walk the changed files, Space stages or unstages.
document.addEventListener('keydown', e => {
  if (gitEl.hidden || e.metaKey || e.ctrlKey || e.altKey) return;
  if (e.target.closest?.('textarea, input:not([type=checkbox]), button, [role=separator]')) return;
  if (reviewKey(e)) e.preventDefault();
});

function selectTab(tab) {
  for (const b of tabsEl.querySelectorAll('button')) {
    b.classList.toggle('active', b.dataset.tab === tab);
    b.setAttribute('aria-pressed', b.dataset.tab === tab);
  }
  filesEl.hidden = tab !== 'files';
  gitEl.hidden = tab !== 'git';
}

function switchSidebar(tab) {
  sidebar = tab;
  selectTab(tab);
  saveState();
  refreshGit();
}

tabsEl.onclick = e => {
  const tab = e.target.closest('button[data-tab]')?.dataset.tab;
  if (tab) switchSidebar(tab);
};

// The sidebar keys also move the focus there, as IntelliJ's tool window keys do.
function showSidebar(tab) {
  if (!openKey || (tab === 'git' && tabsEl.hidden)) return;
  switchSidebar(tab);
  const list = tab === 'files' ? filesEl : changesEl;
  (list.querySelector('[tabindex="0"]') || list).focus();
}
for (const b of tabsEl.querySelectorAll('button[data-tab]')) {
  const action = b.dataset.tab === 'files' ? 'filesTab' : 'gitTab';
  b.dataset.tip = `${b.dataset.tip}\n${keyLabel(action)}`;
  b.setAttribute('aria-keyshortcuts', keyNames(action));
}
commitBtn.title = `Commit (${keyLabel('commit')})`;
onKey('filesTab', () => showSidebar('files'));
onKey('gitTab', () => showSidebar('git'));
onKey('commit', () => {
  if (!openKey || tabsEl.hidden) return;
  switchSidebar('git');
  messageEl.focus();
});
onKey('push', () => {
  if (!pushBtn.hidden && !pushBtn.disabled) pushNow();
});
// Swallowed even with no tab open: in an app window the browser would close the window.
onKey('closeTab', () => closeActive());
// Taken even with nowhere to go: ⌘[ is also the browser's Back, which would leave kijk.
onKey('back', () => goPlace(-1));
onKey('forward', () => goPlace(1));
onKey('nextChange', () => jumpChange(1));
onKey('prevChange', () => jumpChange(-1));
onKey('recentFiles', () => openRecent(openKey && !offline && recentFiles(), path => openTab({ kind: 'file', path }, true)));

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
  railBtn.dataset.tip = on ? 'Show agents' : 'Hide agents';
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
addEventListener('pagehide', saveState);

const gotoFile = () => openGotoFile(!offline && openKey, path => openTab({ kind: 'file', path }, true));
gotoBtn.dataset.tip = `Go to file\n${keyLabel('gotoFile')}`;
gotoBtn.onclick = gotoFile;
onKey('gotoFile', gotoFile);
onKey('find', openFind);
onKey('findNext', () => findAgain(1));
onKey('findPrev', () => findAgain(-1));
onKey('gotoLine', openGotoLine);

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

const events = new EventSource('/api/events');
events.onmessage = e => {
  const msg = JSON.parse(e.data);
  showKijk(msg.kijk);
  machines = msg.machines || null;
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
  // With machines, an empty group says so under its own header.
  if (msg.error) showStatus(msg.error, true);
  else showStatus(rows.length || machines ? '' : 'No running agents');
  followMachine();
  const last = !openKey && rows.find(r => r.key === reopen && !r.stale);
  if (last) open(last);
  else render();
};
events.onerror = () => {
  if (!restarting()) showStatus('Lost connection to kijk. Retrying…', true);
};
setInterval(() => {
  render();
  showFetched();
}, 15000);
