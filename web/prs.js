import { h, patch } from './api.js';
import { lineCount, kind } from './lines.js';
import { forgeMark, forgeWords, prChips } from './pr.js';
import { onKey, keyLabel } from './keys.js';
import { pull, read, line, own, list, askList, watch, redraw, mark, setSince, rowCounts } from './prstore.js';
import { plural, markCurrent } from './boxes.js';
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
    merged: root => { if (root === folder) askLists(true); },
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
  listAt = '';
  patch(listEl);
  patch(prEl);
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
  const w = words(), l = list(folder);
  return h('div', { key: 'search', class: 'prsearch' }, h('span', { html: ICON.search }), h('input', {
    type: 'search', disabled: !hasList(), placeholder: l?.total ? `Search all ${l.total} open ${w.kind}s` : `Search open ${w.kind}s`,
    attrs: { 'aria-label': `Search open ${w.kind}s` },
    oninput: e => {
      query = e.currentTarget.value.trim();
      search();
      draw();
    },
    onkeydown: e => {
      if (e.key === 'Escape' && e.currentTarget.value) {
        e.preventDefault();
        e.stopPropagation();
        e.currentTarget.value = '';
        query = '';
        draw();
      } else if (e.key === 'ArrowDown') {
        e.preventDefault();
        listEl.querySelector('li.prrow')?.focus();
      }
    },
  }));
}

function section(title, n, open, toggle) {
  return h('button', { key: title, class: 'section-toggle', attrs: { 'aria-expanded': String(open) }, onclick: toggle, disabled: !toggle },
    h('span', { class: `chev${open ? ' open' : ''}`, html: ICON.chev }), title, n !== '' && [' ', h('span', 'n', n)]);
}

function prRow(r) {
  const w = words(), c = rowCounts(folder, r);
  const yours = [c.drafts ? plural(c.drafts, 'draft') : '', c.viewed ? `${c.viewed}/${c.files} viewed` : ''].filter(Boolean);
  return h('li', {
    key: r.number || 'branch', class: 'prrow', tabIndex: -1, data: { n: r.number || 'branch' },
    title: r.number ? `${w.num}${r.number} ${r.title}\n${r.head} → ${r.base}` : r.title,
    onclick: () => open(r.number ? r.number : 'branch'),
    onfocus: () => {
      if (listAt === String(r.number || 'branch')) return;
      listAt = String(r.number || 'branch');
      draw();
    },
  },
  h('div', 'l1', h('span', { class: `prstate${r.draft ? ' draft' : ''}`, html: !r.number ? ICON.branch : r.draft ? ICON.draft : ICON.pr }),
    r.number > 0 && h('span', 'prnum', `${w.num}${r.number}`), h('span', 'prt', r.title),
    !r.number && line(folder)?.create && h('a', {
      class: 'pr-link', href: line(folder).create, target: '_blank', rel: 'noopener', title: `Create a ${w.kind} on ${w.name}`, onclick: e => e.stopPropagation(),
    }, 'Create ↗')),
  h('div', 'l2', CI[r.ci] && h('span', { class: `ci ${CI[r.ci][0]}`, title: CI[r.ci][1] }),
    h('span', 'who', r.number ? `${r.author} · ${age(r.createdAt)}` : `No ${w.kind} yet`),
    yours.length > 0 && h('span', 'yours', yours.join(' · ')), lineCount(r.add, r.del)));
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

let listAt = ''; // the data-n of the list row that holds the Tab stop

function drawList() {
  const w = words(), l = list(folder);
  const rows = l?.prs || [];
  const mine = own(folder);
  const ul = (key, items) => h('ul', { key }, items.map(prRow));
  const parts = [];
  if (query) {
    const found = list(folder, query)?.prs || [];
    const hits = [...rows.filter(r => matches(r, query)), ...found.filter(r => !rows.some(x => x.number === r.number))];
    parts.push(section('Found', hits.length), ul('found', hits));
    if (!hits.length) parts.push(h('p', 'prmore', `No open ${w.kind} matches “${query}”.`));
  } else {
    if (onBranch()) parts.push(section('This branch', ''), ul('branch', [thisBranch()]));
    if (l?.note || (line(folder)?.forge && !maybeList())) {
      parts.push(h('p', 'note', l?.note || line(folder).note)); // the tool is missing or not logged in
    } else if (!l) {
      parts.push(h('p', 'note', 'Loading…'));
    } else {
      const asked = rows.filter(r => r.requested && r.number !== mine);
      const yours = rows.filter(r => r.author === l.me && r.number !== mine);
      parts.push(section('Review requested', asked.length), ul('asked', asked));
      parts.push(section('Yours', yours.length), ul('yours', yours));
      parts.push(section('All open', l.total, allOpen, () => {
        allOpen = !allOpen;
        draw();
      }));
      if (allOpen) parts.push(ul('all', rows));
      if (allOpen && l.total > rows.length) parts.push(h('p', 'prmore', `Search above to find any of the ${l.total}.`));
    }
  }
  const made = parts.flatMap(d => (d.tag === 'ul' ? d.children.flat() : []));
  const stop = made.find(d => String(d.props.data.n) === listAt) || made[0];
  if (stop) stop.props.tabIndex = 0;
  patch(listEl, searchBox(), h('div', {
    key: 'list', class: 'prs-scroll',
    onkeydown: e => {
      const rowsNow = [...e.currentTarget.querySelectorAll('li.prrow')];
      const at = rowsNow.indexOf(e.target.closest?.('li.prrow'));
      if (at < 0) return;
      const to = { ArrowDown: at + 1, ArrowUp: at - 1, Home: 0, End: rowsNow.length - 1 }[e.key];
      if (to !== undefined && rowsNow[to]) rowsNow[to].focus();
      else if (e.key === 'ArrowUp' && at === 0) listEl.querySelector('.prsearch input').focus();
      else if (e.key === 'Enter') rowsNow[at].click();
      else return;
      e.preventDefault();
    },
  }, parts));
}

function backButton() {
  return h('button', {
    key: 'back', class: 'icon-btn back', html: ICON.back,
    attrs: { 'aria-label': `Back to ${words().short}s` }, data: { tip: `Back to ${words().short}s\n${keyLabel('prsBack')}` },
    onclick: e => {
      e.stopPropagation();
      open('list');
      focusPrs();
    },
  });
}

function modeSwitch(active) {
  const modes = [['local', 'Local', 'What the agent did, also work not pushed'], ['forge', `As on ${words().name}`, 'What reviewers see']];
  return h('div', { key: 'mode', class: 'prmode' }, h('div', { class: 'mode', attrs: { role: 'group', 'aria-label': 'Which diff' } },
    modes.map(([mode, text, tip]) => h('button', {
      key: mode, attrs: { 'aria-pressed': String(mode === active) }, data: { tip: `${text}\n${tip}` },
      onclick: () => { if (mode !== saved.mode) setMode(mode); },
    }, text))));
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
// stop says it holds the list's Tab stop.
export function overviewRow(n, res, stop) {
  const open = (res?.threads || []).filter(t => !t.resolved && !t.outdated && !t.draft).length;
  const said = (res?.comments || []).filter(c => !c.draft).length;
  return h('li', {
    key: 'overview', class: 'item ov', tabIndex: stop ? 0 : -1, data: { path: '' },
    onclick: () => {
      page.openTab({ kind: 'overview', n, path: '' }, true);
      selectFile('');
    },
  }, h('span', { html: ICON.doc }), h('span', 'name', 'Overview'), h('span', 'dir', open ? plural(open, 'open thread') : said ? plural(said, 'comment') : ''));
}

function prHead(p, mine, row) {
  const n = p.n, res = p.res, d = res || row || {};
  const w = words();
  const ci = res?.checks ? res.ci : d.ci;
  return h('div', 'prhead',
    h('div', 'pr-top', backButton(), forgeMark(forgeKind() || 'github'), h('span', 'pr-num', `${w.num}${n}`), h('span', 'pr-title', d.title || ''),
      d.url && h('a', { class: 'pr-link', href: d.url, target: '_blank', rel: 'noopener', data: { tip: `Open on ${w.name}` }, attrs: { 'aria-label': `Open on ${w.name}` } },
        '↗')),
    h('div', 'prmeta', d.author && [avatar(d.author), h('span', null, d.author)],
      d.head && [h('span', `ref${mine ? ' head' : ''}`, d.head), h('span', { class: 'arrow', html: ICON.arrow }), h('span', 'ref lane-main', d.base)]),
    mine && modeSwitch('forge'),
    prChips({ ...d, ci, ciUrl: d.url && forgeKind() === 'github' ? `${d.url}/checks` : '', target: d.base }, w, line(folder)?.default || d.base),
    sinceParts(p, yes => { if (yes) page.openTab({ kind: 'pr', n, path: '' }, true); }));
}

// sinceParts are the switch under a pull request's header, and the note after a rebase; pick(yes)
// runs when the user moves it.
function sinceParts(p, pick) {
  const s = p.since;
  if (!s) return [];
  const a = s.answer;
  const ago = s.time ? `\nReviewed ${age(new Date(s.time).toISOString())} ago` : '';
  const count = a && !a.note ? `${ago ? ' · ' : '\n'}${plural(a.commits, 'new commit')}` : '';
  const kind = forgeWords(p.res).kind;
  const modes = [[false, 'All changes', `All changes\nEvery file of the ${kind}`], [true, 'Since your review', `Only what changed since your review${ago}${count}`]];
  return [h('div', { key: 'since', class: 'prmode since' }, h('div', { class: 'mode', attrs: { role: 'group', 'aria-label': 'Which changes' } },
    modes.map(([yes, text, tip]) => h('button', {
      key: String(yes), attrs: { 'aria-pressed': String(yes === s.on) }, data: { tip },
      onclick: () => {
        if (yes !== s.on) setSince(folder, p.n, yes);
        pick?.(yes);
      },
    }, text)))),
  s.on && a?.note && h('p', 'since-note', a.note)];
}

function drawPR(n, mine) {
  const p = pull(folder, n);
  patch(prEl, prParts(p, mine), p.res && reviewBox(p));
}

function prParts(p, mine) {
  const n = p.n, res = p.res, files = p.files || res?.files || [], why = p.why || res?.error;
  const sum = files.reduce(([a, d], f) => [a + (f.add || 0), d + (f.del || 0)], [0, 0]);
  const state = f => (p.own(f) ? p.marks.state(p.own(f)) : '');
  const viewedNow = files.filter(f => state(f) === 'viewed').length;
  return [
    prHead(p, mine, list(folder)?.prs.find(r => r.number === n)),
    why && h('p', 'note error', why),
    h('div', { key: 'files', class: 'prs-scroll prfiles' },
      h('h3', null, `Files ${files.length}`, lineCount(...sum), files.length > 0 && h('span', 'viewedcount', `${viewedNow} of ${files.length} viewed`)),
      h('ul', { attrs: { 'aria-label': `Files of ${words().num}${n}` }, onkeydown: rowKeys },
        overviewRow(n, res, !files.some(f => f.path === selectedFile)),
        files.map(f => fileItem(f, state(f), () => {
          page.openTab({ kind: 'pr', n, path: '' }, true);
          goToChange(folder, n, f.path);
          selectFile(f.path);
        }, threadCount((res?.threads || []).filter(t => t.path === f.path)), p.own(f) && viewedBox(n, p.own(f), state(f))))),
      !res && !why && h('p', 'note', `Fetching ${words().num}${n}…`)),
  ];
}

// fileItem is a file row of a pull request, as fileRow draws it in the Git tab, with its mark.
function fileItem(f, state, onclick, tc, box) {
  const slash = f.path.lastIndexOf('/'), k = kind(f.status);
  return h('li', {
    key: f.path, class: `item${state === 'viewed' ? ' viewed' : ''}${f.path === selectedFile ? ' selected' : ''}`,
    tabIndex: f.path === selectedFile ? 0 : -1, title: f.orig ? `${f.orig} → ${f.path}` : f.path, data: { path: f.path }, onclick,
  },
  h('span', `letter ${k}`, f.status), h('span', `name ${k}`, f.path.slice(slash + 1)), h('span', 'dir', slash > 0 ? f.path.slice(0, slash) : ''),
  state === 'changed' && h('span', { class: 'again', data: { tip: 'Changed since you viewed it' } }, 'changed'), tc, lineCount(f.add, f.del), box);
}

// rowKeys moves in a file list: a row the arrows reach shows at once.
function rowKeys(e) {
  const rows = [...e.currentTarget.children];
  const at = rows.indexOf(e.target.closest('li'));
  const to = { ArrowDown: at + 1, ArrowUp: at - 1, Home: 0, End: rows.length - 1 }[e.key];
  if (to !== undefined && rows[to]) {
    rows[to].focus();
    rows[to].click();
  } else if (e.key === 'Enter' && rows[at]) {
    rows[at].click();
  } else {
    return;
  }
  e.preventDefault();
  e.stopPropagation();
}

// viewedBox is a file row's Viewed box. It takes no Tab stop: V works on the focused row.
function viewedBox(n, f, state) {
  return h('input', {
    key: 'viewed', class: 'viewedbox', type: 'checkbox', checked: state === 'viewed', tabIndex: -1,
    attrs: { 'aria-label': 'Viewed' }, data: { tip: `Viewed\n${keyLabel('markViewed')}` },
    onclick: e => e.stopPropagation(),
    onchange: e => { if (!mark(folder, n, f, e.currentTarget.checked)) e.currentTarget.checked = !e.currentTarget.checked; },
  });
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
  const files = (p.files || p.res.files).map(p.own).filter(Boolean);
  const next = on && files.slice(files.indexOf(f) + 1).find(x => p.marks.state(x) !== 'viewed');
  const to = [...prEl.querySelectorAll('.prfiles li')].find(r => r.dataset.path === (next ? next.path : f.path));
  if (!to) return;
  if (next) to.click(); // shows it in Changes, which takes the focus
  to.focus();
}

function selectFile(path) {
  const was = selectedFile, w = which();
  selectedFile = path;
  if (path !== was && w.panel === 'pr') drawPR(w.n, w.mine);
  prEl.querySelector('.prfiles li.selected')?.scrollIntoView({ block: 'nearest' });
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
