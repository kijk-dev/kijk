import { el, get, post, base, folderKey, splitKey, storeKey } from './api.js';
import { showTree, paint, refreshTree, openFolders, selectFile } from './tree.js';
import { refreshView, jumpChange, showMessage } from './view.js';
import { setTabs, tabState, openTab, activeTab, recentFiles, closeActive, goPlace } from './tabs.js';
import { refreshTip } from './tip.js';
import { showPR } from './pr.js';
import { marks, showChanges, showGraph, revealHead, reviewKey, ago, lineCount } from './git.js';
import { openFind, findAgain, openGotoLine, openGotoFile, openRecent } from './find.js';
import { onKey, keyLabel, keyNames } from './keys.js';
import { showKijk, showCodexError, showUndo, restarting } from './status.js';

// The page is the agents window, or at /?window=folders the folders window: same views, other rows.
const params = new URLSearchParams(location.search);
const foldersMode = params.get('window') === 'folders';
const TITLE = foldersMode ? 'kijk · Folders' : 'kijk';
const PANE = foldersMode ? 'folders' : 'agents';
let arriving = params.get('open'); // a folder kijk <folder> just added, to open and flash
if (arriving) history.replaceState(null, '', '/?window=folders');
let openSeq = null; // the last open request seen; null until the first message of a connection
let listError = '';

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
const start = document.getElementById('start');
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
const lastKey = foldersMode ? 'kijk.lastFolder' : 'kijk.last';
let reopen = null; // the folder open before a reload, opened again once its row shows up
try { reopen = localStorage.getItem(lastKey); } catch { /* storage blocked */ }

// Per-folder state: file tabs, expanded tree folders and sidebar tab. It survives reloads and restarts.
function loadState(key) {
  try { return JSON.parse(localStorage.getItem(`kijk.folder:${storeKey(key)}`)) || {}; } catch { return {}; }
}

function saveState() {
  if (!openKey) return;
  try {
    localStorage.setItem(`kijk.folder:${storeKey(openKey)}`, JSON.stringify({ ...tabState(), open: openFolders(), sidebar }));
    localStorage.setItem(lastKey, openKey);
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
  li.agent = el('span', 'agents');
  li.where = el('span', 'where');
  li.repo.append(li.where);
  li.badge.append(li.initials, li.key);
  top.append(li.agent, li.badge, li.names, li.done, li.when);
  li.append(top, li.repo);
  rowEls.set(key, li);
  return li;
}

// Known agent types: the label for tooltips and an inline icon. An unknown type shows its name only.
const AGENTS = {
  // The Claude mark, from Simple Icons (CC0).
  claude: { label: 'Claude Code', icon: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="m4.7144 15.9555 4.7174-2.6471.079-.2307-.079-.1275h-.2307l-.7893-.0486-2.6956-.0729-2.3375-.0971-2.2646-.1214-.5707-.1215-.5343-.7042.0546-.3522.4797-.3218.686.0608 1.5179.1032 2.2767.1578 1.6514.0972 2.4468.255h.3886l.0546-.1579-.1336-.0971-.1032-.0972L6.973 9.8356l-2.55-1.6879-1.3356-.9714-.7225-.4918-.3643-.4614-.1578-1.0078.6557-.7225.8803.0607.2246.0607.8925.686 1.9064 1.4754 2.4893 1.8336.3643.3035.1457-.1032.0182-.0728-.164-.2733-1.3539-2.4467-1.445-2.4893-.6435-1.032-.17-.6194c-.0607-.255-.1032-.4674-.1032-.7285L6.287.1335 6.6997 0l.9957.1336.419.3642.6192 1.4147 1.0018 2.2282 1.5543 3.0296.4553.8985.2429.8318.091.255h.1579v-.1457l.1275-1.706.2368-2.0947.2307-2.6957.0789-.7589.3764-.9107.7468-.4918.5828.2793.4797.686-.0668.4433-.2853 1.8517-.5586 2.9021-.3643 1.9429h.2125l.2429-.2429.9835-1.3053 1.6514-2.0643.7286-.8196.85-.9046.5464-.4311h1.0321l.759 1.1293-.34 1.1657-1.0625 1.3478-.8804 1.1414-1.2628 1.7-.7893 1.36.0729.1093.1882-.0183 2.8535-.607 1.5421-.2794 1.8396-.3157.8318.3886.091.3946-.3278.8075-1.967.4857-2.3072.4614-3.4364.8136-.0425.0304.0486.0607 1.5482.1457.6618.0364h1.621l3.0175.2247.7892.522.4736.6376-.079.4857-1.2142.6193-1.6393-.3886-3.825-.9107-1.3113-.3279h-.1822v.1093l1.0929 1.0686 2.0035 1.8092 2.5075 2.3314.1275.5768-.3218.4554-.34-.0486-2.2039-1.6575-.85-.7468-1.9246-1.621h-.1275v.17l.4432.6496 2.3436 3.5214.1214 1.0807-.17.3521-.6071.2125-.6679-.1214-1.3721-1.9246L14.38 17.959l-1.1414-1.9428-.1397.079-.674 7.2552-.3156.3703-.7286.2793-.6071-.4614-.3218-.7468.3218-1.4753.3886-1.9246.3157-1.53.2853-1.9004.17-.6314-.0121-.0425-.1397.0182-1.4328 1.9672-2.1796 2.9446-1.7243 1.8456-.4128.164-.7164-.3704.0667-.6618.4008-.5889 2.386-3.0357 1.4389-1.882.929-1.0868-.0062-.1579h-.0546l-6.3385 4.1164-1.1293.1457-.4857-.4554.0608-.7467.2307-.2429 1.9064-1.3114Z"/></svg>' },
  // The OpenAI mark, from lobe-icons (MIT).
  codex: { label: 'Codex', icon: '<svg viewBox="0 0 24 24" aria-hidden="true"><path fill-rule="evenodd" d="M9.205 8.658v-2.26c0-.19.072-.333.238-.428l4.543-2.616c.619-.357 1.356-.523 2.117-.523 2.854 0 4.662 2.212 4.662 4.566 0 .167 0 .357-.024.547l-4.71-2.759a.797.797 0 00-.856 0l-5.97 3.473zm10.609 8.8V12.06c0-.333-.143-.57-.429-.737l-5.97-3.473 1.95-1.118a.433.433 0 01.476 0l4.543 2.617c1.309.76 2.189 2.378 2.189 3.948 0 1.808-1.07 3.473-2.76 4.163zM7.802 12.703l-1.95-1.142c-.167-.095-.239-.238-.239-.428V5.899c0-2.545 1.95-4.472 4.591-4.472 1 0 1.927.333 2.712.928L8.23 5.067c-.285.166-.428.404-.428.737v6.898zM12 15.128l-2.795-1.57v-3.33L12 8.658l2.795 1.57v3.33L12 15.128zm1.796 7.23c-1 0-1.927-.332-2.712-.927l4.686-2.712c.285-.166.428-.404.428-.737v-6.898l1.974 1.142c.167.095.238.238.238.428v5.233c0 2.545-1.974 4.472-4.614 4.472zm-5.637-5.303l-4.544-2.617c-1.308-.761-2.188-2.378-2.188-3.948A4.482 4.482 0 014.21 6.327v5.423c0 .333.143.571.428.738l5.947 3.449-1.95 1.118a.432.432 0 01-.476 0zm-.262 3.9c-2.688 0-4.662-2.021-4.662-4.519 0-.19.024-.38.047-.57l4.686 2.71c.286.167.571.167.856 0l5.97-3.448v2.26c0 .19-.07.333-.237.428l-4.543 2.616c-.619.357-1.356.523-2.117.523zm5.899 2.83a5.947 5.947 0 005.827-4.756C22.287 18.339 24 15.84 24 13.296c0-1.665-.713-3.282-1.998-4.448.119-.5.19-.999.19-1.498 0-3.401-2.759-5.947-5.946-5.947-.642 0-1.26.095-1.88.31A5.962 5.962 0 0010.205 0a5.947 5.947 0 00-5.827 4.757C1.713 5.447 0 7.945 0 10.49c0 1.666.713 3.283 1.998 4.448-.119.5-.19 1-.19 1.499 0 3.401 2.759 5.946 5.946 5.946.642 0 1.26-.095 1.88-.309a5.96 5.96 0 004.162 1.713z"/></svg>' },
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
  const agents = r.agents || [];
  const kind = t => AGENTS[t] || { label: t, icon: '' };
  const types = [...new Set(agents.map(a => a.type))];
  // Only the icon of a type with a Busy agent pulses.
  const working = t => agents.some(a => a.type === t && a.busy);
  const icons = types.map(t => `${t}${working(t) ? '+' : ''}`).join(' ');
  if (li.agent.dataset.kind !== icons) {
    li.agent.dataset.kind = icons;
    li.agent.replaceChildren(...types.map(t => {
      const icon = el('span', `agent ${t}${working(t) ? ' on' : ''}`);
      icon.innerHTML = kind(t).icon; // a fixed string from AGENTS, never server text
      return icon;
    }));
    li.style.setProperty('--icons', Math.max(types.length, 1));
  }
  li.where.textContent = where;
  const labels = types.map(t => kind(t).label);
  const perAgent = r.names.map((name, i) => [kind(agents[i]?.type).label, agents[i]?.busy && !r.stale ? 'Busy' : 'Idle', name]);
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
  const key = n ? `${mac ? '⌃⌥' : 'Ctrl+Alt+'}${n} to open` : '';
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
    open: again && r.cwd ? undefined : state.open, // undefined opens the tree down to r.cwd
    reveal: r.cwd,
    onFile: (path, pin) => { if (!offline) openTab({ kind: 'file', path }, pin); },
    onToggle: saveState,
  });
  setTabs(r.key, state, tabsChanged);
  tabsEl.hidden = true;
  sidebarHead.hidden = false;
  showFolderName(r);
  document.title = foldersMode ? TITLE : `${r.repo} — kijk`;
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
    if (foldersMode) refreshRow();
    refreshGit();
    refreshTree(change);
    refreshView(change);
  };
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
  showMessage(viewer, start);
  filesEl.replaceChildren();
  sidebarHead.hidden = true;
  tabsEl.hidden = true;
  gitNote.hidden = true;
  selectTab('files');
  document.title = TITLE;
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

const events = new EventSource(foldersMode ? '/api/events?window=folders' : '/api/events');
events.onopen = () => { openSeq = null; };
events.onmessage = e => {
  const msg = JSON.parse(e.data);
  showKijk(msg.kijk);
  if (foldersMode) return showFolders(msg);
  machines = msg.machines || null;
  showCodexError([msg.codexError, ...(machines || []).filter(m => m.codexError).map(m => `${m.name}: ${m.codexError}`)]
    .filter(Boolean).join('\n'));
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
  if (msg.error) showStatus(msg.error, true);
  else showStatus(rows.length || machines ? '' : 'No running agents');
  followMachine();
  const last = !openKey && rows.find(r => r.key === reopen && !r.stale);
  if (last) open(last);
  else render();
};
function showFolders(msg) {
  rows = (msg.folders || []).map(r => ({ ...r, key: r.root, machine: '', names: [r.repo] }));
  const req = msg.open || { seq: 0 };
  if (openSeq !== null && req.seq > openSeq && req.root) arriving = req.root;
  openSeq = req.seq;
  listError = msg.error || '';
  if (openKey && rows.find(r => r.key === openKey)?.missing) closeFolder();
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
    closeFolder();
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
