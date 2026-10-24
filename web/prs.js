import { el, svg, keepFocus } from './api.js';
import { lineCount, fileRow } from './git.js';
import { forgeMark, forgeWords, prChips } from './pr.js';
import { onKey, keyLabel } from './keys.js';
import { pull, read, line, own, list, askList, watch, redraw, mark, setSince, rowCounts } from './prstore.js';
import { iconButton, plural, markCurrent } from './boxes.js';
import { avatar, threadCount, age } from './threads.js';
import { goToChange, setupChanges } from './changes.js';
import { setupOverview } from './overview.js';
import { setupDrafts, reviewBox } from './drafts.js';
import { wakeBranch } from './review.js';

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
let query = '';
let searchTimer = 0;
let allOpen = false;
let selectedFile = '';
let lastAsk = 0;
let timer = 0;
let ownSeen = 0; // the own pull request the tab last asked about

// setupPrs tells the tab about the page: announce(text), say(text), openTab(tab, pin), save(),
// setCount(n), openBranch(), openFile(n, path), focusBranch() and status(), the git status.
export function setupPrs(p) {
  page = p;
  setupChanges({ onTop: (n, path) => selectFile(path), openFile: p.openFile });
  setupDrafts({ announce: p.announce, say: p.say, reviewed: root => { if (root === folder) askLists(true); } });
  setupOverview({
    branch: () => {
      const st = page.status(), l = line(folder);
      return { branch: st?.branch, base: st?.total?.base?.replace(/^origin\//, ''), ahead: st?.ahead, forge: l?.forge, create: l?.create };
    },
    own: n => n === own(folder) && onBranch(),
    say: p.say,
    announce: p.announce,
  });
}

export function setPrsFolder(key, state) {
  folder = key;
  saved = { open: 'list', mode: 'local', ...state };
  wanted = null;
  query = '';
  allOpen = false;
  lastAsk = 0;
  ownSeen = 0;
  listEl.replaceChildren();
  prEl.replaceChildren();
  page?.setCount(0);
}

export const prsState = () => ({ open: saved.open, mode: saved.mode });

const words = () => forgeWords(line(folder)?.forge ? line(folder) : list(folder));
const forgeKind = () => line(folder)?.forge?.kind || list(folder)?.forge?.kind || '';
// A list needs the forge and its tool; a note on the PR line without a PR says the tool is missing.
const maybeList = () => !!line(folder)?.forge && !(line(folder).note && !line(folder).pr);
const hasList = () => maybeList() && !list(folder)?.note;
const onBranch = () => !!page.status()?.total;

// which is what the tab shows: the list, the own branch in Local mode, or a pull request.
function which() {
  const o = saved.open;
  if (o === 'branch' && onBranch()) return own(folder) && saved.mode === 'forge' && hasList() ? { panel: 'pr', n: own(folder), mine: true } : { panel: 'branch' };
  if (typeof o === 'number' && o > 0 && hasList()) return { panel: 'pr', n: o, mine: false };
  if (maybeList() || !onBranch()) return { panel: 'list' };
  return { panel: 'branch' };
}

// prsStatus runs after each git status refresh of the open folder.
export const prsStatus = () => redraw(folder);

watch(root => {
  if (root !== folder || !folder) return;
  if (own(folder) !== ownSeen) lastAsk = 0; // as after git switch: kijk asks about the new one now
  ownSeen = own(folder);
  // The tab may have shown before kijk knew the forge, and then asked nothing.
  if (!tabEl.hidden && !lastAsk && line(folder)?.forge) refresh();
  else if (maybeList() && !list(folder)) askLists();
  const l = list(folder);
  page.setCount(!l || l.note ? 0 : l.prs.filter(r => r.requested && r.number !== own(folder)).length);
  draw();
});

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
  if (item === own(folder) && onBranch()) item = 'branch';
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
  if (mode === 'forge' && own(folder)) read(folder, own(folder));
}

export function focusPrs() {
  const w = which();
  if (w.panel === 'branch') return page.focusBranch();
  const host = w.panel === 'list' ? listEl : prEl;
  (host.querySelector('li[tabindex="0"]') || host.querySelector('input, ul') || host).focus();
}

// ----- Asking the forge -----

// askLists asks for the list, then searches again: the list may now tell that it is not all.
const askLists = (fresh, key = folder) => askList(folder, '', fresh).then(() => { if (key === folder && query) search(); });

// refresh asks again about what the tab shows. The list is asked too, for the count. fresh, as for
// the refresh key, does not take the answer of a read already on its way.
function refresh(fresh) {
  if (!folder || !line(folder)?.forge) return;
  lastAsk = Date.now();
  const w = which();
  if (w.panel === 'pr') read(folder, w.n, fresh);
  else if (own(folder)) read(folder, own(folder), fresh); // the threads in Local mode
  if (maybeList()) askLists(fresh);
}

addEventListener('focus', () => {
  if (!tabEl.hidden && Date.now() - lastAsk > 60000) refresh();
});

// search asks the forge too when it has more open pull requests than the list holds.
function search() {
  clearTimeout(searchTimer);
  const l = list(folder);
  if (!query || !l || l.total <= l.prs.length) return;
  const q = query, key = folder;
  searchTimer = setTimeout(() => { if (key === folder && q === query) askList(folder, q, true); }, 300);
}

// ----- Drawing -----

function draw() {
  if (!folder) return;
  if (saved.open === own(folder) && onBranch()) {
    if (wanted === saved.open) wanted = 'branch';
    saved.open = 'branch';
  }
  const w = which();
  if (wanted !== null && (wanted === 'branch' ? w.panel === 'branch' || w.mine : w.panel === 'pr' && w.n === wanted)) {
    wanted = null;
    showPrsItem();
    if (w.panel === 'pr') read(folder, w.n);
  }
  if (list(folder)?.note) wanted = null; // no list: the item cannot show
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
  if (!r.number && line(folder)?.create) {
    const a = el('a', 'pr-link', 'Create ↗');
    Object.assign(a, { href: line(folder).create, target: '_blank', rel: 'noopener', title: `Create a ${words().kind} on ${words().name}` });
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
  const c = rowCounts(folder, r);
  const yours = [c.drafts ? plural(c.drafts, 'draft') : '', c.viewed ? `${c.viewed}/${c.files} viewed` : ''].filter(Boolean);
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
  const n = own(folder), l = list(folder), st = page.status();
  const row = l?.prs.find(r => r.number === n);
  if (row) return row;
  if (n) return { number: n, title: line(folder).pr.title, author: l?.me || 'you', createdAt: '', ci: line(folder).pr.ci, add: st.total.add, del: st.total.del };
  return { number: 0, title: st.branch || 'This branch', add: st.total.add, del: st.total.del };
}

function matches(r, q) {
  q = q.toLowerCase().replace(/^[#!]/, '');
  return String(r.number) === q || [r.title, r.author, r.head].some(s => s?.toLowerCase().includes(q));
}

function drawList() {
  if (!listEl.firstChild) listEl.append(searchBox(), el('div', 'prs-scroll'));
  const input = listEl.querySelector('.prsearch input');
  const w = words(), l = list(folder);
  input.placeholder = l?.total ? `Search all ${l.total} open ${w.kind}s` : `Search open ${w.kind}s`;
  input.setAttribute('aria-label', `Search open ${w.kind}s`);
  input.disabled = !hasList();
  const scroll = listEl.querySelector('.prs-scroll');
  const focused = listEl.contains(document.activeElement) && document.activeElement.closest('li.prrow')?.dataset.n;
  const parts = [];
  const rows = l?.prs || [];
  const mine = own(folder);
  const ul = items => {
    const u = el('ul');
    u.append(...items.map(prRowEl));
    return u;
  };
  if (query) {
    const found = list(folder, query)?.prs || [];
    const hits = [...rows.filter(r => matches(r, query)), ...found.filter(r => !rows.some(x => x.number === r.number))];
    parts.push(section('Found', hits.length), ul(hits));
    if (!hits.length) parts.push(el('p', 'prmore', `No open ${w.kind} matches “${query}”.`));
  } else {
    if (onBranch()) parts.push(section('This branch', ''), ul([thisBranch()]));
    if (l?.note || (line(folder)?.forge && !maybeList())) {
      parts.push(el('p', 'note', l?.note || line(folder).note)); // the tool is missing or not logged in
    } else if (!l) {
      parts.push(el('p', 'note', 'Loading…'));
    } else {
      const asked = rows.filter(r => r.requested && r.number !== mine);
      const yours = rows.filter(r => r.author === l.me && r.number !== mine);
      parts.push(section('Review requested', asked.length), ul(asked));
      parts.push(section('Yours', yours.length), ul(yours));
      parts.push(section('All open', l.total, allOpen, () => {
        allOpen = !allOpen;
        draw();
      }));
      if (allOpen) parts.push(ul(rows));
      if (allOpen && l.total > rows.length) parts.push(el('p', 'prmore', `Search above to find any of the ${l.total}.`));
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
  if (own(folder) && hasList()) out.after.push(modeSwitch('local'));
  // Without a list, the tab says why: the tool is missing or not logged in.
  if (line(folder)?.forge) out.note = list(folder)?.note || (!maybeList() ? line(folder).note : '') || '';
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

function prHead(pr, mine, row) {
  const n = pr.n, res = pr.res, p = res || row || {};
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
  const chips = prChips({ ...p, ci, ciUrl: p.url && forgeKind() === 'github' ? `${p.url}/checks` : '', target: p.base }, w, line(folder)?.default || p.base);
  if (chips) h.append(chips);
  h.append(...sinceParts(pr, yes => { if (yes) page.openTab({ kind: 'pr', n, path: '' }, true); }));
  return h;
}

// sinceParts are the switch under a pull request's header, and the note after a rebase; pick(yes)
// runs when the user moves it.
function sinceParts(p, pick) {
  const s = p.since;
  if (!s) return [];
  const wrap = el('div', 'prmode since');
  const group = el('div', 'mode');
  group.setAttribute('role', 'group');
  group.setAttribute('aria-label', 'Which changes');
  const a = s.answer;
  const ago = s.time ? `\nReviewed ${age(new Date(s.time).toISOString())} ago` : '';
  const count = a && !a.note ? `${ago ? ' · ' : '\n'}${plural(a.commits, 'new commit')}` : '';
  const kind = forgeWords(p.res).kind;
  for (const [yes, text, tip] of [[false, 'All changes', `All changes\nEvery file of the ${kind}`], [true, 'Since your review', `Only what changed since your review${ago}${count}`]]) {
    const b = el('button', null, text);
    b.setAttribute('aria-pressed', yes === s.on);
    b.dataset.tip = tip;
    b.onclick = () => {
      if (yes !== s.on) setSince(folder, p.n, yes);
      pick?.(yes);
    };
    group.append(b);
  }
  wrap.append(group);
  return s.on && a?.note ? [wrap, el('p', 'since-note', a.note)] : [wrap];
}

// The review box is a kept element that the draw moves, so keepFocus wraps all of it.
function drawPR(n, mine) {
  const top = prEl.querySelector('.prs-scroll')?.scrollTop;
  keepFocus(prEl, () => prEl.replaceChildren(...prParts(n, mine)));
  if (top) prEl.querySelector('.prs-scroll').scrollTop = top;
}

function prParts(n, mine) {
  const p = pull(folder, n), res = p.res;
  const row = list(folder)?.prs.find(r => r.number === n);
  const parts = [prHead(p, mine, row)];
  const why = p.why || res?.error;
  if (why) parts.push(el('p', 'note error', why));
  const wrap = el('div', 'prs-scroll prfiles');
  const files = p.files || res?.files || [];
  const h = el('h3', null, `Files ${files.length}`);
  const sum = files.reduce(([a, d], f) => [a + (f.add || 0), d + (f.del || 0)], [0, 0]);
  const lc = lineCount(...sum);
  if (lc) h.append(lc);
  const { marks, own } = p;
  const viewedNow = files.filter(f => own(f) && marks.state(own(f)) === 'viewed').length;
  if (files.length) h.append(el('span', 'viewedcount', `${viewedNow} of ${files.length} viewed`));
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
    const state = own(f) ? marks.state(own(f)) : '';
    r.classList.toggle('viewed', state === 'viewed');
    if (state === 'changed') {
      const again = el('span', 'again', 'changed');
      again.dataset.tip = 'Changed since you viewed it';
      r.insertBefore(again, r.querySelector('.tc, .lc'));
    }
    if (own(f)) r.append(viewedBox(n, own(f), state));
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
  return parts;
}

// viewedBox is a file row's Viewed box. It takes no Tab stop: V works on the focused row.
function viewedBox(n, f, state) {
  const box = el('input', 'viewedbox');
  box.type = 'checkbox';
  box.checked = state === 'viewed';
  box.tabIndex = -1;
  box.setAttribute('aria-label', 'Viewed');
  box.dataset.tip = `Viewed\n${keyLabel('markViewed')}`;
  box.onclick = e => e.stopPropagation();
  box.onchange = () => { if (!mark(folder, n, f, box.checked)) box.checked = !box.checked; };
  return box;
}

// markRow is V on a file row: it marks or clears the row's file, and after a mark goes to the next
// row not viewed.
function markRow(li) {
  const w = which();
  const before = w.panel === 'pr' ? pull(folder, w.n) : null;
  const f = before?.res?.files?.find(x => x.path === li.dataset.path);
  const on = f && before.marks.state(f) !== 'viewed';
  if (!f || !mark(folder, w.n, f, on)) return;
  const p = pull(folder, w.n);
  const rows = [...prEl.querySelectorAll('.prfiles li')];
  const files = (p.files || p.res.files).map(p.own).filter(Boolean);
  const next = on && files.slice(files.indexOf(f) + 1).find(x => p.marks.state(x) !== 'viewed');
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
  if (!folder || !line(folder)?.forge) return false;
  refresh(true);
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
