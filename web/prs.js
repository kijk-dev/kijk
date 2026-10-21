import { el, get, svg } from './api.js';
import { lineCount, fileRow } from './git.js';
import { FORGE, forgeMark, prChips } from './pr.js';
import { onKey, keyLabel } from './keys.js';
import { cachedView, loadView, onView, age } from './prdata.js';
import { iconButton, plural, markCurrent } from './boxes.js';
import { avatar, threadCount } from './threads.js';
import { goToChange, setupChanges } from './changes.js';
import { setupOverview } from './overview.js';
import { setupDrafts, reviewBox } from './drafts.js';
import { setBranchThreads, redrawBranchPanel, wakeBranch } from './review.js';
import { marksOf, setViewed, onViewed, listCount, sweep } from './viewed.js';

// The PRs tab: the list of open pull requests, one pull request opened in the sidebar, the count
// on the tab, and when to ask the forge again. The folder's own branch in Local mode is review.js.

const ICON = {
  pr: '<svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="18" cy="18" r="3"/><circle cx="6" cy="6" r="3"/><path d="M13 6h3a2 2 0 0 1 2 2v7"/><path d="M6 9v12"/></svg>',
  draft: '<svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="18" cy="18" r="3"/><circle cx="6" cy="6" r="3"/><path d="M18 6V5"/><path d="M18 11v-1"/><path d="M6 9v12"/></svg>',
  branch: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M6 3v12"/><circle cx="18" cy="6" r="3"/><circle cx="6" cy="18" r="3"/><path d="M18 9a9 9 0 0 1-9 9"/></svg>',
  back: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="m15 18-6-6 6-6"/></svg>',
  arrow: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M5 12h14"/><path d="m12 5 7 7-7 7"/></svg>',
  search: '<svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="11" cy="11" r="7"/><path d="m21 21-4.3-4.3"/></svg>',
  chev: '<svg viewBox="0 0 24 24" aria-hidden="true" width="10" height="10"><path d="m9 18 6-6-6-6"/></svg>',
  doc: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M15 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7Z"/><path d="M14 2v4a2 2 0 0 0 2 2h4"/><path d="M16 13H8"/><path d="M16 17H8"/><path d="M10 9H8"/></svg>',
};
const CI = { passed: ['pass', 'Checks passed'], failed: ['fail', 'Checks failed'], running: ['run', 'Checks running'] };

const tabEl = document.getElementById('prs');
const listEl = document.getElementById('prs-list');
const branchEl = document.getElementById('prs-branch');
const prEl = document.getElementById('prs-pr');

let page = null;
let folder = null;
let saved = { open: 'list', mode: 'local' }; // what the tab shows, kept per folder
let list = null; // the last /api/prs answer
let listCall = null;
let st = null; // the folder's git status
let line = null; // the PR line's answer
let query = '';
let found = []; // rows the forge's search found for query
let searchTimer = 0;
let allOpen = false;
let selectedFile = '';
let lastAsk = 0;
let timer = 0;
const failed = new Map(); // pull request number: why kijk could not read it

// setupPrs tells the tab about the page: announce(text), say(text), openTab(tab, pin), save(),
// setCount(n), openBranch(), openFile(n, path) and focusBranch().
export function setupPrs(p) {
  page = p;
  setupChanges({ onTop: (n, path) => selectFile(path), openFile: p.openFile });
  setupDrafts({ announce: p.announce, say: p.say, reviewed: root => { if (root === folder) loadList(); } });
  setupOverview({
    branch: () => ({ branch: st?.branch, base: st?.total?.base?.replace(/^origin\//, ''), ahead: st?.ahead, forge: line?.forge?.kind, create: line?.create }),
    own: n => n === ownNumber() && onBranch(),
    say: p.say,
    announce: p.announce,
  });
}

export function setPrsFolder(key, state) {
  folder = key;
  saved = { open: 'list', mode: 'local', ...state };
  wanted = null;
  list = null;
  listCall = null;
  st = null;
  line = null;
  query = '';
  found = [];
  allOpen = false;
  failed.clear();
  lastAsk = 0;
  listEl.replaceChildren();
  prEl.replaceChildren();
  page?.setCount(0);
}

export const prsState = () => ({ open: saved.open, mode: saved.mode });

const words = () => FORGE[line?.forge?.kind || list?.forge?.kind] || FORGE.github;
const forgeKind = () => line?.forge?.kind || list?.forge?.kind || '';
const ownNumber = () => (['open', 'draft'].includes(line?.pr?.state) ? line.pr.number : 0);
// A list needs the forge and its tool; a note on the PR line without a PR says the tool is missing.
const maybeList = () => !!line?.forge && !(line.note && !line.pr);
const hasList = () => maybeList() && !list?.note;
const onBranch = () => !!st?.total;

// which is what the tab shows: the list, the own branch in Local mode, or a pull request.
function which() {
  const o = saved.open;
  if (o === 'branch' && onBranch()) return ownNumber() && saved.mode === 'forge' && hasList() ? { panel: 'pr', n: ownNumber(), mine: true } : { panel: 'branch' };
  if (typeof o === 'number' && o > 0 && hasList()) return { panel: 'pr', n: o, mine: false };
  if (maybeList() || !onBranch()) return { panel: 'list' };
  return { panel: 'branch' };
}

// prsStatus runs after each git status and PR line refresh of the open folder.
export function prsStatus(status, pr) {
  st = status || st;
  line = pr;
  // The tab may have shown before kijk knew the forge, and then asked nothing.
  if (!tabEl.hidden && !lastAsk && line?.forge) refresh();
  else if (maybeList() && !list && !listCall) loadList();
  draw();
}

// prsShown says the PRs tab showed or hid; it asks the forge only while the tab shows.
export function prsShown(on) {
  clearInterval(timer);
  timer = 0;
  if (!on || !folder) return;
  timer = setInterval(refresh, 120000);
  if (Date.now() - lastAsk > 10000) refresh();
}

// showPrsItem opens in the viewer what the tab shows, when the tab is picked.
export function showPrsItem() {
  const w = which();
  if (w.panel === 'branch') page.openBranch();
  else if (w.panel === 'pr') page.openTab({ kind: 'pr', n: w.n, path: '' }, true);
}

// openPR opens pull request n in the tab, as the PR line's title and kijk <PR link> do.
export function openPR(n) {
  open(n);
}

// wanted is an item just opened. It shows in the viewer once kijk knows enough to show it, as when
// kijk <PR link> opens a folder whose forge is not known yet.
let wanted = null;

function open(item) {
  if (item === ownNumber() && onBranch()) item = 'branch';
  saved.open = item;
  page.save();
  wanted = item === 'list' ? null : item;
  draw();
}

function setMode(mode) {
  saved.mode = mode;
  page.save();
  draw();
  showPrsItem();
  if (mode === 'forge' && ownNumber()) load(ownNumber());
}

export function focusPrs() {
  const w = which();
  if (w.panel === 'branch') return page.focusBranch();
  const host = w.panel === 'list' ? listEl : prEl;
  (host.querySelector('li[tabindex="0"]') || host.querySelector('input, ul') || host).focus();
}

// ----- Asking the forge -----

async function loadList() {
  const key = folder;
  const call = get('prs', { root: folder }).catch(err => ({ note: err.message, prs: [] }));
  listCall = call;
  const res = await call;
  if (key !== folder || listCall !== call) return;
  listCall = null;
  list = res;
  if (!res.note && res.forge && res.total <= res.prs.length) sweep(key, res.forge, res.prs.map(r => r.number));
  page.setCount(res.note ? 0 : res.prs.filter(r => r.requested && r.number !== ownNumber()).length);
  draw();
  redrawBranchPanel();
  if (query) search();
}

async function load(n) {
  const key = folder;
  try {
    await loadView(folder, n);
    failed.delete(n);
  } catch (err) {
    if (key !== folder) return;
    failed.set(n, err.message);
    draw();
  }
}

onView((root, n, res) => {
  if (root !== folder) return;
  if (n === ownNumber()) setBranchThreads(res);
  draw();
});

onViewed(root => {
  if (root === folder) draw();
});

// refresh asks again about what the tab shows. The list is asked too, for the count.
function refresh() {
  if (!folder || !line?.forge) return;
  lastAsk = Date.now();
  const w = which();
  if (w.panel === 'pr') load(w.n);
  else if (ownNumber()) load(ownNumber()); // the threads in Local mode
  if (maybeList()) loadList();
}

addEventListener('focus', () => {
  if (!tabEl.hidden && Date.now() - lastAsk > 60000) refresh();
});

// search asks the forge too when it has more open pull requests than the list holds.
function search() {
  clearTimeout(searchTimer);
  found = [];
  if (!query || !list || list.total <= list.prs.length) return;
  const q = query, key = folder;
  searchTimer = setTimeout(async () => {
    let res;
    try { res = await get('prs', { root: folder, q }); } catch { return; }
    if (key !== folder || q !== query) return;
    found = res.prs || [];
    draw();
  }, 300);
}

// ----- Drawing -----

function draw() {
  if (!folder) return;
  if (saved.open === ownNumber() && onBranch()) {
    if (wanted === saved.open) wanted = 'branch';
    saved.open = 'branch';
  }
  const w = which();
  if (wanted !== null && (wanted === 'branch' ? w.panel === 'branch' || w.mine : w.panel === 'pr' && w.n === wanted)) {
    wanted = null;
    showPrsItem();
    if (w.panel === 'pr') load(w.n);
  }
  if (list?.note) wanted = null; // no list: the item cannot show
  listEl.hidden = w.panel !== 'list';
  branchEl.hidden = w.panel !== 'branch';
  prEl.hidden = w.panel !== 'pr';
  if (w.panel === 'list') drawList();
  if (w.panel === 'pr') drawPR(w.n, w.mine);
  if (w.panel === 'branch') wakeBranch();
}

function searchBox() {
  const box = el('div', 'prsearch');
  const input = el('input');
  input.type = 'search';
  input.oninput = () => {
    query = input.value.trim();
    search();
    draw();
  };
  input.onkeydown = e => {
    if (e.key === 'Escape' && input.value) {
      e.preventDefault();
      e.stopPropagation();
      input.value = '';
      query = '';
      found = [];
      draw();
    } else if (e.key === 'ArrowDown') {
      e.preventDefault();
      listEl.querySelector('li.prrow')?.focus();
    }
  };
  box.append(svg(ICON.search), input);
  return box;
}

function section(title, n, open, toggle) {
  const b = el('button', 'section-toggle');
  b.append(svg(ICON.chev, `chev${open ? ' open' : ''}`), title);
  if (n !== '') b.append(' ', el('span', 'n', String(n)));
  b.setAttribute('aria-expanded', open);
  if (toggle) b.onclick = toggle;
  else b.disabled = true;
  return b;
}

function prRowEl(r) {
  const li = el('li', 'prrow');
  li.dataset.n = r.number || 'branch';
  li.tabIndex = -1;
  const l1 = el('div', 'l1');
  l1.append(svg(!r.number ? ICON.branch : r.draft ? ICON.draft : ICON.pr, `prstate${r.draft ? ' draft' : ''}`));
  if (r.number) l1.append(el('span', 'prnum', `${words().num}${r.number}`));
  l1.append(el('span', 'prt', r.title));
  if (!r.number && line?.create) {
    const a = el('a', 'pr-link', 'Create ↗');
    Object.assign(a, { href: line.create, target: '_blank', rel: 'noopener', title: `Create a ${words().kind} on ${words().name}` });
    a.onclick = e => e.stopPropagation();
    l1.append(a);
  }
  const l2 = el('div', 'l2');
  if (CI[r.ci]) {
    const dot = el('span', `ci ${CI[r.ci][0]}`);
    dot.title = CI[r.ci][1];
    l2.append(dot);
  }
  l2.append(el('span', 'who', r.number ? `${r.author} · ${age(r.createdAt)}` : `No ${words().kind} yet`));
  const drafts = r.number ? cachedView(folder, r.number)?.drafts ?? r.drafts : 0;
  const seen = r.number && list?.forge ? listCount(folder, list.forge, r.number) : null;
  const yours = [drafts ? plural(drafts, 'draft') : '', seen?.viewed ? `${seen.viewed}/${seen.files} viewed` : ''].filter(Boolean);
  if (yours.length) l2.append(el('span', 'yours', yours.join(' · ')));
  const lc = lineCount(r.add, r.del);
  if (lc) l2.append(lc);
  li.append(l1, l2);
  li.title = r.number ? `${words().num}${r.number} ${r.title}\n${r.head} → ${r.base}` : r.title;
  li.onclick = () => open(r.number ? r.number : 'branch');
  return li;
}

// thisBranch is the row of the folder's own branch: its pull request, or the branch alone.
function thisBranch() {
  const n = ownNumber();
  const row = list?.prs.find(r => r.number === n);
  if (row) return row;
  if (n) return { number: n, title: line.pr.title, author: list?.me || 'you', createdAt: '', ci: line.pr.ci, add: st.total.add, del: st.total.del };
  return { number: 0, title: st.branch || 'This branch', add: st.total.add, del: st.total.del };
}

function matches(r, q) {
  q = q.toLowerCase().replace(/^[#!]/, '');
  return String(r.number) === q || [r.title, r.author, r.head].some(s => s?.toLowerCase().includes(q));
}

function drawList() {
  if (!listEl.firstChild) listEl.append(searchBox(), el('div', 'prs-scroll'));
  const input = listEl.querySelector('.prsearch input');
  const w = words();
  input.placeholder = list?.total ? `Search all ${list.total} open ${w.kind}s` : `Search open ${w.kind}s`;
  input.setAttribute('aria-label', `Search open ${w.kind}s`);
  input.disabled = !hasList();
  const scroll = listEl.querySelector('.prs-scroll');
  const focused = listEl.contains(document.activeElement) && document.activeElement.closest('li.prrow')?.dataset.n;
  const parts = [];
  const rows = list?.prs || [];
  const mine = ownNumber();
  const ul = items => {
    const u = el('ul');
    u.append(...items.map(prRowEl));
    return u;
  };
  if (query) {
    const hits = [...rows.filter(r => matches(r, query)), ...found.filter(r => !rows.some(x => x.number === r.number))];
    parts.push(section('Found', hits.length), ul(hits));
    if (!hits.length) parts.push(el('p', 'prmore', `No open ${w.kind} matches “${query}”.`));
  } else {
    if (onBranch()) parts.push(section('This branch', ''), ul([thisBranch()]));
    if (list?.note || (line?.forge && !maybeList())) {
      parts.push(el('p', 'note', list?.note || line.note)); // the tool is missing or not logged in
    } else if (!list) {
      parts.push(el('p', 'note', 'Loading…'));
    } else {
      const asked = rows.filter(r => r.requested && r.number !== mine);
      const yours = rows.filter(r => r.author === list.me && r.number !== mine);
      parts.push(section('Review requested', asked.length), ul(asked));
      parts.push(section('Yours', yours.length), ul(yours));
      parts.push(section('All open', list.total, allOpen, () => {
        allOpen = !allOpen;
        draw();
      }));
      if (allOpen) parts.push(ul(rows));
      if (allOpen && list.total > rows.length) parts.push(el('p', 'prmore', `Search above to find any of the ${list.total}.`));
    }
  }
  scroll.replaceChildren(...parts);
  const all = [...scroll.querySelectorAll('li.prrow')];
  const keep = all.find(li => li.dataset.n === focused);
  (keep || all[0])?.setAttribute('tabindex', '0');
  if (keep) keep.focus();
  scroll.onkeydown = e => {
    const rowsNow = [...scroll.querySelectorAll('li.prrow')];
    const at = rowsNow.indexOf(e.target.closest?.('li.prrow'));
    if (at < 0) return;
    const to = { ArrowDown: at + 1, ArrowUp: at - 1, Home: 0, End: rowsNow.length - 1 }[e.key];
    if (to !== undefined && rowsNow[to]) {
      rowsNow[at].tabIndex = -1;
      rowsNow[to].tabIndex = 0;
      rowsNow[to].focus();
    } else if (e.key === 'ArrowUp' && at === 0) {
      input.focus();
    } else if (e.key === 'Enter') {
      rowsNow[at].click();
    } else {
      return;
    }
    e.preventDefault();
  };
}

function backButton() {
  const b = iconButton(ICON.back, `Back to ${words().short}s`, () => {
    open('list');
    focusPrs();
  });
  b.classList.add('back');
  b.dataset.tip = `Back to ${words().short}s\n${keyLabel('prsBack')}`;
  return b;
}

function modeSwitch(active) {
  const wrap = el('div', 'prmode');
  const group = el('div', 'mode');
  group.setAttribute('role', 'group');
  group.setAttribute('aria-label', 'Which diff');
  for (const [mode, text, tip] of [['local', 'Local', 'What the agent did, also work not pushed'], ['forge', `As on ${words().name}`, 'What reviewers see']]) {
    const b = el('button', null, text);
    b.setAttribute('aria-pressed', mode === active);
    b.dataset.tip = `${text}\n${tip}`;
    b.onclick = () => { if (mode !== saved.mode) setMode(mode); };
    group.append(b);
  }
  wrap.append(group);
  return wrap;
}

// branchHead is what review.js adds to the head of the own branch: back, and the switch.
export function branchHead() {
  const out = { before: [], after: [], note: '' };
  if (maybeList()) out.before.push(backButton());
  if (ownNumber() && hasList()) out.after.push(modeSwitch('local'));
  // Without a list, the tab says why: the tool is missing or not logged in.
  if (line?.forge) out.note = list?.note || (!maybeList() ? line.note : '') || '';
  return out;
}

// overviewRow is the first row of a file list: the Overview of pull request n, or of the branch.
export function overviewRow(n, res) {
  const li = el('li', 'item ov');
  li.tabIndex = -1;
  li.dataset.path = '';
  const parts = [];
  const open = (res?.threads || []).filter(t => !t.resolved && !t.outdated && !t.draft).length;
  if (open) parts.push(plural(open, 'open thread'));
  const said = (res?.comments || []).filter(c => !c.draft).length;
  if (!open && said) parts.push(plural(said, 'comment'));
  li.append(svg(ICON.doc), el('span', 'name', 'Overview'), el('span', 'dir', parts.join(' · ')));
  li.onclick = () => {
    page.openTab({ kind: 'overview', n, path: '' }, true);
    selectFile('');
  };
  return li;
}

function prHead(n, mine, res, row) {
  const p = res || row || {};
  const w = words();
  const h = el('div', 'prhead');
  const top = el('div', 'pr-top');
  top.append(backButton(), forgeMark(forgeKind() || 'github'), el('span', 'pr-num', `${w.num}${n}`), el('span', 'pr-title', p.title || ''));
  if (p.url) {
    const a = el('a', 'pr-link', '↗');
    Object.assign(a, { href: p.url, target: '_blank', rel: 'noopener' });
    a.dataset.tip = `Open on ${w.name}`;
    a.setAttribute('aria-label', `Open on ${w.name}`);
    top.append(a);
  }
  const meta = el('div', 'prmeta');
  if (p.author) meta.append(avatar(p.author), el('span', null, p.author));
  if (p.head) meta.append(el('span', `ref${mine ? ' head' : ''}`, p.head), svg(ICON.arrow, 'arrow'), el('span', 'ref lane-main', p.base));
  h.append(top, meta);
  if (mine) h.append(modeSwitch('forge'));
  const ci = res?.checks ? res.ci : p.ci;
  const chips = prChips({ ...p, ci, ciUrl: p.url && forgeKind() === 'github' ? `${p.url}/checks` : '', target: p.base }, w, line?.default || p.base);
  if (chips) h.append(chips);
  return h;
}

function drawPR(n, mine) {
  const res = cachedView(folder, n);
  const row = list?.prs.find(r => r.number === n);
  const focused = prEl.contains(document.activeElement) ? (document.activeElement.closest('.prfiles ul') ? 'list' : document.activeElement.className) : null;
  const parts = [prHead(n, mine, res, row)];
  const why = failed.get(n) || res?.error;
  if (why) parts.push(el('p', 'note error', why));
  const wrap = el('div', 'prs-scroll prfiles');
  const files = res?.files || [];
  const h = el('h3', null, `Files ${files.length}`);
  const sum = files.reduce(([a, d], f) => [a + (f.add || 0), d + (f.del || 0)], [0, 0]);
  const lc = lineCount(...sum);
  if (lc) h.append(lc);
  const marks = marksOf(folder, n, res);
  if (files.length) h.append(el('span', 'viewedcount', `${marks.count} of ${files.length} viewed`));
  const ul = el('ul');
  ul.setAttribute('aria-label', `Files of ${words().num}${n}`);
  ul.append(overviewRow(n, res));
  for (const f of files) {
    const r = fileRow(f, () => {
      page.openTab({ kind: 'pr', n, path: '' }, true);
      goToChange(folder, n, f.path);
      selectFile(f.path);
    });
    r.dataset.path = f.path;
    const tc = threadCount((res.threads || []).filter(t => t.path === f.path));
    if (tc) r.insertBefore(tc, r.querySelector('.lc'));
    const state = marks.state(f);
    r.classList.toggle('viewed', state === 'viewed');
    if (state === 'changed') {
      const again = el('span', 'again', 'changed');
      again.dataset.tip = 'Changed since you viewed it';
      r.insertBefore(again, r.querySelector('.tc, .lc'));
    }
    r.append(viewedBox(n, res, f, state));
    if (f.path === selectedFile) r.classList.add('selected');
    ul.append(r);
  }
  (ul.querySelector('.selected') || ul.firstChild).tabIndex = 0;
  ul.onkeydown = e => {
    const rows = [...ul.children];
    const at = rows.indexOf(e.target.closest('li'));
    const to = { ArrowDown: at + 1, ArrowUp: at - 1, Home: 0, End: rows.length - 1 }[e.key];
    if (to !== undefined && rows[to]) {
      rows[at].tabIndex = -1;
      rows[to].tabIndex = 0;
      rows[to].focus();
      rows[to].click();
    } else if (e.key === 'Enter' && rows[at]) {
      rows[at].click();
    } else {
      return;
    }
    e.preventDefault();
    e.stopPropagation();
  };
  wrap.append(h, ul);
  if (!res && !why) wrap.append(el('p', 'note', `Fetching ${words().num}${n}…`));
  parts.push(wrap);
  const box = res && reviewBox(folder, n, res);
  if (box) parts.push(box);
  // The review box is one element that moves into the new panel; a move loses the focus.
  const typing = box?.contains(document.activeElement) ? document.activeElement : null;
  const at = typing && [typing.selectionStart, typing.selectionEnd];
  prEl.replaceChildren(...parts);
  if (focused === 'list') ul.querySelector('[tabindex="0"]')?.focus();
  if (typing?.isConnected) {
    typing.focus();
    if (at[0] != null) typing.setSelectionRange(...at);
  }
}

// viewedBox is a file row's Viewed box. It takes no Tab stop: V works on the focused row.
function viewedBox(n, res, f, state) {
  const box = el('input', 'seen');
  box.type = 'checkbox';
  box.checked = state === 'viewed';
  box.tabIndex = -1;
  box.setAttribute('aria-label', 'Viewed');
  box.dataset.tip = `Viewed\n${keyLabel('markViewed')}`;
  box.onclick = e => e.stopPropagation();
  box.onchange = () => setViewed(folder, n, res, f, box.checked);
  return box;
}

// markRow is V on a file row: it marks or clears the row's file, and after a mark goes to the next
// row not viewed.
function markRow(li) {
  const w = which();
  const res = w.panel === 'pr' && cachedView(folder, w.n);
  const f = res?.files.find(x => x.path === li.dataset.path);
  if (!f) return;
  const marks = marksOf(folder, w.n, res);
  const on = marks.state(f) !== 'viewed';
  setViewed(folder, w.n, res, f, on);
  const rows = [...prEl.querySelectorAll('.prfiles li')];
  const after = res.files.slice(res.files.indexOf(f) + 1);
  const next = on && after.find(x => marksOf(folder, w.n, res).state(x) !== 'viewed');
  const to = rows.find(r => r.dataset.path === (next ? next.path : f.path));
  if (!to) return;
  for (const r of rows) r.tabIndex = r === to ? 0 : -1;
  if (next) to.click(); // shows it in Changes, which takes the focus
  to.focus();
}

function selectFile(path) {
  selectedFile = path;
  for (const li of prEl.querySelectorAll('.prfiles li')) {
    const on = li.dataset.path === path;
    li.classList.toggle('selected', on);
    if (on) li.scrollIntoView({ block: 'nearest' });
  }
}

onKey('refreshPrs', () => {
  if (!folder || !line?.forge) return false;
  refresh();
  page.announce(`Asking ${words().name} again`);
});

onKey('markViewed', () => {
  const li = document.activeElement?.closest?.('#prs-pr .prfiles li');
  if (!li?.dataset.path) return markCurrent();
  markRow(li);
});

onKey('prsBack', () => {
  if (!tabEl.contains(document.activeElement) || which().panel === 'list' || !maybeList()) return false;
  open('list');
  focusPrs();
});
