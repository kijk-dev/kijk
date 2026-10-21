import { el, get } from './api.js';
import { lineCount } from './git.js';
import { hostMark, forgeWords } from './pr.js';
import { keyLabel } from './keys.js';

// The PRs tab for the folder's own branch: its pull request, what is not pushed yet, and its
// branch changes. It asks no forge; the pull request is what the PR line already knows.

const ICON = {
  branch: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M6 3v12"/><circle cx="18" cy="6" r="3"/><circle cx="6" cy="18" r="3"/><path d="M18 9a9 9 0 0 1-9 9"/></svg>',
  arrow: '<svg class="arrow" viewBox="0 0 24 24" aria-hidden="true"><path d="M5 12h14"/><path d="m12 5 7 7-7 7"/></svg>',
  local: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M10.1 2.18a9.93 9.93 0 0 1 3.8 0"/><path d="M17.6 3.71a9.95 9.95 0 0 1 2.69 2.7"/><path d="M21.82 10.1a9.93 9.93 0 0 1 0 3.8"/><path d="M20.29 17.6a9.95 9.95 0 0 1-2.7 2.69"/><path d="M13.9 21.82a9.94 9.94 0 0 1-3.8 0"/><path d="M6.4 20.29a9.95 9.95 0 0 1-2.69-2.7"/><path d="M2.18 13.9a9.93 9.93 0 0 1 0-3.8"/><path d="M3.71 6.4a9.95 9.95 0 0 1 2.7-2.69"/></svg>',
};

const KIND = { A: 'added', D: 'deleted', '?': 'untracked', U: 'conflict' };
const kind = status => KIND[status] || 'modified';
const num = n => n.toLocaleString('en-US');
const plural = (n, word) => `${num(n)} ${word}${n === 1 ? '' : 's'}`;

const panel = document.getElementById('prs');
let page = null;
let folder = null; // the open folder's key
let status = null; // the folder's git status
let prStatus = null; // the PR line's answer
let target = ''; // the open PR's target branch
let data = null; // the last /api/branch answer
let error = '';
let loading = null; // the /api/branch call in flight: {key, call}
let selected = ''; // the path of the selected file

// setupReview tells the tab about the folder view around it: announce(text) and push().
export function setupReview(p) {
  page = p;
}

export function setBranchFolder(key) {
  folder = key;
  data = null;
  error = '';
  selected = '';
  panel.replaceChildren();
}

// refreshBranch runs after each git status of a folder with branch changes. It asks for the branch
// changes only while the PRs tab shows them.
export async function refreshBranch(st, pr, tgt) {
  status = st;
  prStatus = pr;
  target = tgt;
  if (panel.hidden) return;
  await load();
}

// showBranchPanel draws the panel again after the PR line learned more.
export function showBranchPanel(st, pr, tgt) {
  status = st || status;
  prStatus = pr;
  target = tgt;
  if (data) drawPanel();
}

export function focusBranchList() {
  const list = panel.querySelector('.prfiles ul');
  (list?.querySelector('[tabindex="0"]') || list || panel).focus();
}

// fetchBranch shares one call between refreshes that come at the same time.
function fetchBranch() {
  const key = `${folder}\n${target}`;
  if (loading?.key !== key) {
    const call = get('branch', { root: folder, target }).catch(err => err);
    loading = { key, call };
    call.finally(() => { if (loading?.call === call) loading = null; });
  }
  return loading.call;
}

async function load() {
  const key = folder;
  const res = await fetchBranch();
  if (key !== folder) return;
  if (res instanceof Error) {
    data = null;
    error = res.status === 404 ? 'This branch has no branch changes' : res.message;
  } else {
    data = res;
    error = '';
  }
  drawPanel();
}

// ----- The panel -----

function drawPanel() {
  if (!folder) return;
  if (!data) return panel.replaceChildren(el('p', 'note', error || 'Loading…'));
  // The panel redraws on every refresh, so the focus moves to the same control in the new one.
  const focused = panel.contains(document.activeElement) ? document.activeElement : null;
  const key = focused?.closest('.prfiles ul') ? 'list' : focused?.dataset.k;
  panel.replaceChildren(header(), ...unpushedBar(), fileList(), notesBox());
  if (key === 'list') focusBranchList();
  else if (key) panel.querySelector(`[data-k="${key}"]`)?.focus();
}

function header() {
  const h = el('div', 'prhead');
  const top = el('div', 'pr-top');
  const f = prStatus?.forge;
  const p = prStatus?.pr || data.pr;
  const words = forgeWords(f?.kind || 'github');
  if (f) top.append(hostMark(f.kind));
  if (p) {
    const title = el(p.url ? 'a' : 'span', 'pr-title', p.title);
    if (p.url) Object.assign(title, { href: p.url, target: '_blank', rel: 'noopener', title: `Open on ${words.name}` });
    top.append(el('span', 'pr-num', `${words.num}${p.number}`), title);
  } else {
    const icon = el('span', 'branchmark');
    icon.innerHTML = ICON.branch;
    top.append(icon, el('span', 'pr-title', data.branch));
  }
  const meta = el('div', 'prmeta');
  const arrow = el('span', 'arrow');
  arrow.innerHTML = ICON.arrow;
  const base = el('span', 'ref lane-main', data.base.replace(/^origin\//, ''));
  base.title = `Compared with ${data.base}`;
  meta.append(el('span', 'ref head', data.branch), arrow, base);
  h.append(top, meta);
  return h;
}

// unpushedBar says what reviewers cannot see yet.
function unpushedBar() {
  const d = data;
  if (d.upstream && !d.ahead && !d.uncommitted) return [];
  const bar = el('div', 'unpushed');
  bar.innerHTML = ICON.local;
  const t = el('span', 't');
  if (!d.upstream) {
    t.append('This branch is not pushed yet. ', el('span', 'then', "Reviewers don't see it."));
  } else {
    const what = [d.ahead && plural(d.ahead, 'commit'), d.uncommitted && plural(d.uncommitted, 'file')].filter(Boolean).join(' and ');
    t.append(`${what} not pushed. `, el('span', 'then', "Reviewers don't see them yet."));
  }
  bar.append(t);
  if (status?.remote) {
    const b = el('button', 'tool', d.upstream ? 'Push' : 'Publish');
    b.dataset.k = 'push';
    b.title = `${d.upstream ? `Push to ${d.upstream}` : 'Publish this branch to origin'} (${keyLabel('push')})`;
    b.disabled = !!d.upstream && !d.ahead;
    b.onclick = () => {
      b.disabled = true;
      page.push();
    };
    bar.append(b);
  }
  return [bar];
}

function dashed(tip) {
  const s = el('span', 'np');
  s.innerHTML = ICON.local;
  s.dataset.tip = tip;
  return s;
}

const NOT_PUSHED = "Not pushed yet\nReviewers don't see this file";

function fileList() {
  const wrap = el('div', 'prs-scroll prfiles');
  const h = el('h3', null, `Branch changes ${data.files.length}`);
  const sum = data.files.reduce(([a, d], f) => [a + (f.add || 0), d + (f.del || 0)], [0, 0]);
  const lc = lineCount(...sum);
  if (lc) h.append(lc);
  const ul = el('ul');
  ul.setAttribute('aria-label', 'Branch changes');
  for (const f of data.files) {
    const row = el('li', 'item');
    row.dataset.path = f.path;
    row.tabIndex = -1;
    row.title = f.orig ? `${f.orig} → ${f.path}` : f.path;
    const slash = f.path.lastIndexOf('/');
    row.append(
      el('span', `letter ${kind(f.status)}`, f.status),
      el('span', `name ${kind(f.status)}`, f.path.slice(slash + 1)),
      el('span', 'dir', slash > 0 ? f.path.slice(0, slash) : ''),
    );
    if (f.notPushed) row.append(dashed(NOT_PUSHED));
    const c = lineCount(f.add, f.del);
    if (c) row.append(c);
    row.onclick = () => selectRow(f.path);
    if (f.path === selected) mark(row, true);
    ul.append(row);
  }
  if (!ul.querySelector('[tabindex="0"]') && ul.firstChild) ul.firstChild.tabIndex = 0;
  ul.onkeydown = e => {
    const rows = [...ul.children];
    const at = rows.indexOf(e.target.closest('li'));
    const to = { ArrowDown: at + 1, ArrowUp: at - 1, Home: 0, End: rows.length - 1 }[e.key];
    if (to === undefined || !rows[to]) return;
    selectRow(rows[to].dataset.path);
    rows[to].focus();
    e.preventDefault();
    e.stopPropagation();
  };
  wrap.append(h, ul);
  if (!data.files.length) wrap.append(el('p', 'note', 'No changes yet'));
  return wrap;
}

function mark(row, on) {
  row.classList.toggle('selected', on);
  row.tabIndex = on ? 0 : -1;
  if (on) row.setAttribute('aria-current', 'true');
  else row.removeAttribute('aria-current');
}

function selectRow(path) {
  if (path === selected) return;
  selected = path;
  for (const row of panel.querySelectorAll('.prfiles li')) {
    mark(row, row.dataset.path === path);
    if (row.dataset.path === path) row.scrollIntoView({ block: 'nearest' });
  }
}

function notesBox() {
  const box = el('div', 'reviewbox');
  const head = el('div', 'head');
  head.append(el('b', null, 'Agent notes'), el('span', 'muted', 'none yet'));
  box.append(head);
  return box;
}
