import { el, get, post, base, splitKey, storeKey } from './api.js';
import { showTree, paint, refreshTree, openFolders, selectFile } from './tree.js';
import { refreshView, jumpChange, showMessage } from './view.js';
import { setTabs, tabState, openTab, activeTab, recentFiles, closeActive, goPlace } from './tabs.js';
import { showPR, ownPR, FORGE } from './pr.js';
import { setupReview, setBranchFolder, refreshBranch, focusBranchList } from './review.js';
import { setupPrs, setPrsFolder, prsState, prsStatus, prsShown, showPrsItem, focusPrs, branchHead, overviewRow, openPR } from './prs.js';
import { marks, showChanges, showGraph, revealHead, reviewKey, ago, lineCount } from './git.js';
import { openFind, findAgain, openGotoLine, openGotoFile, openRecent } from './find.js';
import { onKey, keyLabel, keyNames } from './keys.js';

// The view of the open folder: its tree, file tabs, and the Files, Git and PRs sidebar tabs.
const filesEl = document.getElementById('files');
const gitEl = document.getElementById('git');
const prsEl = document.getElementById('prs');
const tabsEl = document.getElementById('tabs');
const prsBtn = tabsEl.querySelector('[data-tab=prs]');
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
const gotoBtn = document.getElementById('goto-btn');
const drafts = new Map(); // unsent commit message per folder
let busy = false;
let git = null; // last status of the open folder
const viewer = document.getElementById('viewer');
const staleBar = document.getElementById('stalebar');
const staleText = document.getElementById('stale-text');
export let openKey = null; // stays open when its session ends, until the user picks another row
let offline = false; // the open folder's machine is not connected, so the view is frozen
let folderEvents = null;
let sidebar = 'files'; // the sidebar tab chosen for the open folder
let page = null;

// setupFolder tells the view about the page around it: lastKey, the localStorage key that keeps the
// open folder; start, shown when no folder is open; announce(text); say(text), a short note in the
// status bar; agentIcon(row), the icon of the row's agent type; and onFolderEvent, run on each live
// update of the folder.
export function setupFolder(p) {
  page = p;
  const openBranch = () => openTab({ kind: 'branch', path: '' }, true);
  setupReview({
    announce: p.announce,
    say: p.say,
    push: pushNow,
    openFile: path => { if (!offline) openTab({ kind: 'file', path }, true); },
    openBranch,
    branchHead,
    overviewRow,
  });
  setupPrs({
    announce: p.announce,
    say: p.say,
    openTab: (t, pin) => { if (!offline) openTab({ ...t, num: FORGE[pr?.forge?.kind]?.num }, pin); },
    save: saveState,
    setCount: showCount,
    openBranch: () => { if (!offline) openBranch(); },
    openFile: (n, path) => { if (!offline) openTab({ kind: 'file', path, pr: n, num: FORGE[pr?.forge?.kind]?.num }, true); },
    focusBranch: focusBranchList,
  });
}

export const SERVER = '<svg viewBox="0 0 24 24" aria-hidden="true"><rect x="3" y="4" width="18" height="7" rx="1.5"/><rect x="3" y="13" width="18" height="7" rx="1.5"/><path d="M7 7.5h.01M7 16.5h.01"/></svg>';

// Per-folder state: file tabs, expanded tree folders and sidebar tab. It survives reloads and restarts.
function loadState(key) {
  try { return JSON.parse(localStorage.getItem(`kijk.folder:${storeKey(key)}`)) || {}; } catch { return {}; }
}

function saveState() {
  if (!openKey) return;
  try {
    localStorage.setItem(`kijk.folder:${storeKey(openKey)}`, JSON.stringify({ ...tabState(), open: openFolders(), sidebar, prs: prsState() }));
    localStorage.setItem(page.lastKey, openKey);
  } catch { /* storage blocked */ }
}

function tabsChanged() {
  const t = activeTab();
  selectFile(t?.kind === 'file' ? t.path : null);
  saveState();
}

// openFolder shows the folder of row r. again, for kijk <folder>, opens its tree down to r.cwd.
export function openFolder(r, again) {
  if (openKey) drafts.set(openKey, messageEl.value);
  saveState();
  openKey = r.key;
  offline = false;
  showOffline();
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
  setBranchFolder(r.key, page.agentIcon?.(r)); // before the tabs, which may open Branch changes
  setPrsFolder(r.key, state.prs);
  setTabs(r.key, state, tabsChanged);
  tabsEl.hidden = true;
  sidebarHead.hidden = false;
  showFolderName(r);
  gitNote.hidden = true;
  pr = cachedPR(r.key);
  upHash = null;
  showPR(prLine, pr, openFolderPR);
  showPrsTab();
  refreshPR();
  selectTab('files');
  refreshGit();
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
    page.onFolderEvent();
    refreshGit();
    refreshTree(change);
    refreshView(change);
  };
}

// closeFolder empties the viewer, as when kijk starts: for a folder whose machine was disabled.
export function closeFolder() {
  if (openKey) drafts.set(openKey, messageEl.value);
  saveState();
  folderEvents?.close();
  folderEvents = null;
  openKey = null;
  offline = false;
  showOffline();
  git = null;
  pr = null;
  setBranchFolder(null);
  setPrsFolder(null);
  setTabs(null, {}, () => {});
  showMessage(viewer, page.start);
  filesEl.replaceChildren();
  sidebarHead.hidden = true;
  tabsEl.hidden = true;
  gitNote.hidden = true;
  selectTab('files');
}

// setMachine freezes the open remote folder while its machine m is away, and reloads it when m is back.
export function setMachine(m) {
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
const prTarget = () => ownPR(pr)?.target || '';

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
  if (git?.git) {
    prsBtn.hidden = !git.total && !pr?.forge;
    if (prsEl.hidden === (shownTab() === 'prs')) selectTab(shownTab());
  }
  showPR(prLine, pr, openFolderPR);
  showPrsTab();
  if (prTarget() !== before) refreshGit();
  prsStatus(git, pr);
  if (pr?.pr?.ci === 'running') prTimer = setTimeout(refreshPR, 60000);
}

async function refreshGit() {
  if (offline) return; // keep what is on screen until the machine is back
  const root = openKey;
  refreshBranch(git, pr); // at the same time as the status, which takes as long
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
  prsBtn.hidden = !st.total && !pr?.forge;
  if (!git || prsEl.hidden === (shownTab() === 'prs')) selectTab(shownTab());
  git = st;
  prsStatus(st, pr);
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

export function showFetched() {
  fetchedEl.textContent = git?.fetchedAt ? `fetched ${ago(git.fetchedAt)}` : 'never fetched';
}

function showOutput(text, ok) {
  outputEl.hidden = !text;
  outputEl.textContent = text;
  outputEl.classList.toggle('ok', !!ok);
  page.announce(text);
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
  const was = !prsEl.hidden;
  prsEl.hidden = tab !== 'prs';
  if (was !== !prsEl.hidden) prsShown(!prsEl.hidden);
}

// shownTab is the chosen sidebar tab, or Git while the branch has no PRs tab.
const shownTab = () => (sidebar === 'prs' && prsBtn.hidden ? 'git' : sidebar);

// The PRs tab is named for the forge: pull requests on GitHub, merge requests on GitLab. Its count
// is the pull requests that wait for your review.
let waiting = 0;
function showPrsTab() {
  const kind = pr?.forge?.kind;
  const word = FORGE[kind] ? `${FORGE[kind].short}s` : 'Branch';
  const wait = waiting ? `\n${waiting} ${waiting === 1 ? 'waits' : 'wait'} for your review` : '';
  prsBtn.dataset.tip = `${word}${wait}\n${keyLabel('prsTab')}`;
  prsBtn.setAttribute('aria-label', waiting ? `${word}, ${waiting} for your review` : word);
  let count = prsBtn.querySelector('.tabcount');
  if (!waiting) return count?.remove();
  if (!count) prsBtn.append(count = el('span', 'tabcount'));
  count.textContent = waiting > 99 ? '99+' : String(waiting);
  count.setAttribute('aria-hidden', 'true');
}

function showCount(n) {
  waiting = n;
  showPrsTab();
}

// openFolderPR opens pull request n of the open folder in the PRs tab: from the PR line's title,
// and for kijk <PR link>.
export function openFolderPR(n) {
  if (!openKey || offline) return;
  openPR(n);
  switchSidebar('prs');
}

function switchSidebar(tab) {
  sidebar = tab;
  selectTab(tab);
  saveState();
  refreshGit();
  if (tab === 'prs' && !offline) showPrsItem();
}

tabsEl.onclick = e => {
  const tab = e.target.closest('button[data-tab]')?.dataset.tab;
  if (tab) switchSidebar(tab);
};

// The sidebar keys also move the focus there, as IntelliJ's tool window keys do.
function showSidebar(tab) {
  if (!openKey || (tab !== 'files' && tabsEl.hidden) || (tab === 'prs' && prsBtn.hidden)) return;
  switchSidebar(tab);
  if (tab === 'prs') return focusPrs();
  const list = tab === 'files' ? filesEl : changesEl;
  (list.querySelector('[tabindex="0"]') || list).focus();
}
const TAB_KEYS = { files: 'filesTab', git: 'gitTab', prs: 'prsTab' };
for (const b of tabsEl.querySelectorAll('button[data-tab]')) {
  const action = TAB_KEYS[b.dataset.tab];
  b.dataset.tip = `${b.dataset.tip}\n${keyLabel(action)}`;
  b.setAttribute('aria-keyshortcuts', keyNames(action));
}
commitBtn.title = `Commit (${keyLabel('commit')})`;
onKey('filesTab', () => showSidebar('files'));
onKey('gitTab', () => showSidebar('git'));
onKey('prsTab', () => showSidebar('prs'));
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
addEventListener('pagehide', saveState);

const gotoFile = () => openGotoFile(!offline && openKey, path => openTab({ kind: 'file', path }, true));
gotoBtn.dataset.tip = `Go to file\n${keyLabel('gotoFile')}`;
gotoBtn.onclick = gotoFile;
onKey('gotoFile', gotoFile);
onKey('find', openFind);
onKey('findNext', () => findAgain(1));
onKey('findPrev', () => findAgain(-1));
onKey('gotoLine', openGotoLine);
