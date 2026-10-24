import { el, get, storeKey, keepFocus } from './api.js';
import { isCurrent } from './view.js';
import { lineCount, ago, fileRow } from './git.js';
import { forgeMark, FORGE } from './pr.js';
import { keyLabel, mac } from './keys.js';
import { textBox, unsentKey } from './drafts.js';
import { boxView, showBoxes, noBoxes, redraw, drawBox, iconButton, goToBox, plural } from './boxes.js';
import { threadEl, outdatedBar, threadCount, avatar, sent } from './threads.js';

// The PRs tab for the folder's own branch: the panel in the sidebar, the Branch changes view, agent
// notes and Copy for agent. Its pull request's threads come from prs.js.

const ICON = {
  branch: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M6 3v12"/><circle cx="18" cy="6" r="3"/><circle cx="6" cy="18" r="3"/><path d="M18 9a9 9 0 0 1-9 9"/></svg>',
  arrow: '<svg class="arrow" viewBox="0 0 24 24" aria-hidden="true"><path d="M5 12h14"/><path d="m12 5 7 7-7 7"/></svg>',
  local: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M10.1 2.18a9.93 9.93 0 0 1 3.8 0"/><path d="M17.6 3.71a9.95 9.95 0 0 1 2.69 2.7"/><path d="M21.82 10.1a9.93 9.93 0 0 1 0 3.8"/><path d="M20.29 17.6a9.95 9.95 0 0 1-2.7 2.69"/><path d="M13.9 21.82a9.94 9.94 0 0 1-3.8 0"/><path d="M6.4 20.29a9.95 9.95 0 0 1-2.69-2.7"/><path d="M2.18 13.9a9.93 9.93 0 0 1 0-3.8"/><path d="M3.71 6.4a9.95 9.95 0 0 1 2.7-2.69"/></svg>',
  msg: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M7.9 20A9 9 0 1 0 4 16.1L2 22Z"/></svg>',
  copy: '<svg viewBox="0 0 24 24" aria-hidden="true"><rect width="14" height="14" x="8" y="8" rx="2"/><path d="M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2"/></svg>',
  edit: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M21.17 6.81a1 1 0 0 0-3.99-3.99L3.84 16.17a2 2 0 0 0-.5.83l-1.32 4.35a.5.5 0 0 0 .62.62l4.35-1.32a2 2 0 0 0 .83-.5z"/></svg>',
  trash: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M3 6h18"/><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6"/><path d="M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/></svg>',
};

const tabEl = document.getElementById('prs');
const panel = document.getElementById('prs-branch');
const shown = () => !tabEl.hidden && !panel.hidden;
let page = null;
let folder = null; // the open folder's key
let agentIcon = null; // the icon of the folder's agent type
let status = null; // the folder's git status
let prStatus = null; // the PR line's answer
let target = ''; // the open PR's target branch
let data = null; // the last /api/branch answer
let error = '';
let loading = null; // the /api/branch call in flight: {key, call}
let notes = []; // the agent notes of this folder and branch
let notesKey = '';
let view = null; // the Branch changes view, while it is open
let selected = ''; // the path of the file at the top of the view
let goal = ''; // a file to scroll to once the view is drawn
let copyPanel = null;
let branchPR = null; // the answer about the branch's pull request, with its threads

// setupReview tells the tab about the folder view around it: announce(text), say(text),
// push(), openFile(path), openBranch(), which opens the Branch changes tab, branchHead(), what the
// PRs tab adds to the header, and overviewRow(n), the first row of the file list.
export function setupReview(p) {
  page = p;
}

export function setBranchFolder(key, icon) {
  closeCopy();
  branchPR = null;
  strips.clear();
  folder = key;
  agentIcon = icon || null;
  data = null;
  error = '';
  notes = [];
  notesKey = '';
  selected = '';
  panel.replaceChildren();
}

// refreshBranch runs with each git status refresh. It asks for the branch changes only while the
// PRs tab or the Branch changes view shows them.
export async function refreshBranch(st, pr, tgt) {
  status = st;
  prStatus = pr;
  target = tgt;
  if (!shown() && !openView()) return;
  await load();
}

// showBranchPanel draws the panel again after the git status or the PR line learned more, and
// asks for the branch changes when the panel just showed without them.
export function showBranchPanel(st, pr, tgt) {
  status = st || status;
  prStatus = pr;
  target = tgt;
  if (loading?.key === `${folder}\n${target}`) return; // the load draws the panel when it is done
  if (data) drawPanel();
  else if (shown()) load();
}

// ownPR is the number of the branch's open pull request, or 0.
function ownPR() {
  const p = prStatus?.pr;
  return p && ['open', 'draft'].includes(p.state) ? p.number : 0;
}

// wakeBranch loads the branch changes when the panel just showed without them.
export function wakeBranch() {
  if (!data && folder && shown() && !loading) load();
}

export function redrawBranchPanel() {
  if (data) drawPanel();
}

// setBranchThreads takes the answer about the branch's pull request, for its threads in Local mode.
export function setBranchThreads(res) {
  branchPR = res;
  if (data) drawPanel();
  const v = openView();
  if (v) redraw(v, [...new Set((res?.threads || []).map(t => t.path))]);
}

export function focusBranchList() {
  const list = panel.querySelector('.prfiles ul');
  (list?.querySelector('[tabindex="0"]') || list || panel).focus();
}

// basis is what every box depends on besides its own file: the branch and its notes, the split
// point and the upstream.
const basis = () => `${data?.branch} ${data?.split} ${data?.upHash || ''}`;

const openView = () => (view && isCurrent(view) ? view : null);

// fetchBranch shares one call between the status refresh and the view's reload of the same change.
function fetchBranch() {
  const key = `${folder}\n${target}`;
  if (loading?.key !== key) {
    const call = get('branch', { root: folder, target }).catch(err => err);
    loading = { key, call };
    call.finally(() => { if (loading?.call === call) loading = null; });
  }
  return loading.call;
}

async function load(change) {
  const key = `${folder}\n${target}`;
  const res = await fetchBranch();
  if (key !== `${folder}\n${target}`) return;
  if (res instanceof Error && res.status === 404) {
    // On another branch, such as the main line, nothing of this branch may show or be added to.
    closeCopy();
    data = null;
    error = 'This branch has no branch changes';
    notes = [];
    notesKey = '';
  } else if (res instanceof Error) {
    error = res.message; // a git call failed once: keep what shows until the next refresh
  } else {
    data = res;
    error = '';
    loadNotes();
  }
  drawPanel();
  const v = openView();
  if (!v) return;
  if (!data) noChanges(v);
  else if (!v.scroll) await build(v);
  else if (!error) await updateView(v, change);
}

// ----- Agent notes: {id, path, line, text, body, at, copiedAt, outdated}, kept per folder and branch -----

function loadNotes() {
  const k = `kijk.notes:${storeKey(folder)}:${data.branch}`;
  if (k === notesKey) return;
  notesKey = k;
  try { notes = JSON.parse(localStorage.getItem(k)) || []; } catch { notes = []; }
  try { copiedThreads = new Set(JSON.parse(localStorage.getItem(threadsKey())) || []); } catch { copiedThreads = new Set(); }
}

// The forge threads copied for the agent are kept by id, next to the notes.
let copiedThreads = new Set();
const threadsKey = () => notesKey.replace(/^kijk\.notes:/, 'kijk.copied-threads:');

function saveCopiedThreads() {
  try { localStorage.setItem(threadsKey(), JSON.stringify([...copiedThreads])); } catch { /* storage blocked */ }
}

// openThreads are the threads of the branch's pull request that still want an answer.
const openThreads = () => (branchPR && branchPR.number === ownPR() ? sent(branchPR.threads).filter(t => !t.resolved && !t.outdated) : []);
const threadLines = new Map(); // thread id: the line on disk it shows under

function saveNotes() {
  try {
    if (notes.length) localStorage.setItem(notesKey, JSON.stringify(notes));
    else localStorage.removeItem(notesKey);
  } catch { /* storage blocked */ }
}

const notesOn = path => notes.filter(n => n.path === path).sort((a, b) => a.line - b.line);
const inBranch = path => !!data?.files.some(f => f.path === path);

// notesChanged saves the notes and draws them again in the panel and in the boxes of paths.
function notesChanged(paths) {
  saveNotes();
  drawPanel();
  const v = openView();
  if (v) redraw(v, paths);
}

// ----- The panel -----

function drawPanel() {
  if (!folder) return;
  if (!data) return panel.replaceChildren(el('p', 'note', error || 'Loading…'));
  keepFocus(panel, () => panel.replaceChildren(header(), ...(error ? [el('p', 'note error', error)] : []), ...unpushedBar(), fileList(), notesBox()));
}

function header() {
  const h = el('div', 'prhead');
  const top = el('div', 'pr-top');
  const f = prStatus?.forge;
  const p = prStatus?.pr || data.pr;
  const words = FORGE[f?.kind || 'github'];
  if (f) top.append(forgeMark(f.kind));
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
  const extra = page.branchHead?.() || {};
  top.prepend(...(extra.before || []));
  h.append(...(extra.after || []));
  if (extra.note) h.append(el('p', 'pr-note', extra.note));
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
    const row = fileRow(f, () => goTo(f.path));
    row.dataset.path = f.path;
    const counts = row.querySelector('.lc');
    if (f.notPushed) row.insertBefore(dashed(NOT_PUSHED), counts);
    const tc = threadCount(threadsOn(f.path), notesOn(f.path).length);
    if (tc) row.insertBefore(tc, counts);
    if (f.path === selected) mark(row, true);
    ul.append(row);
  }
  if (!ul.querySelector('[tabindex="0"]') && ul.firstChild) ul.firstChild.tabIndex = 0;
  ul.onkeydown = e => {
    const rows = [...ul.children];
    const at = rows.indexOf(e.target.closest('li'));
    const to = { ArrowDown: at + 1, ArrowUp: at - 1, Home: 0, End: rows.length - 1 }[e.key];
    if (to !== undefined && rows[to]) {
      if (rows[to].dataset.path) goTo(rows[to].dataset.path);
      for (const r of rows) r.tabIndex = r === rows[to] ? 0 : -1;
      rows[to].focus();
    } else if (e.key === 'Enter' && rows[at]) {
      if (rows[at].dataset.path) goTo(rows[at].dataset.path, true);
      else rows[at].click();
    } else {
      return;
    }
    e.preventDefault();
    e.stopPropagation();
  };
  const ov = page.overviewRow?.(ownPR());
  if (ov) {
    ov.tabIndex = ul.querySelector('[tabindex="0"]') ? -1 : 0;
    ul.prepend(ov);
  }
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
  const copied = notes.filter(n => n.copiedAt).length;
  head.append(el('b', null, 'Agent notes'), el('span', 'muted', notes.length ? `${notes.length}${copied ? ` · ${copied} copied` : ''}` : 'none yet'));
  if (copied) {
    const clear = el('button', 'all', 'Clear copied');
    clear.dataset.k = 'clear';
    clear.title = 'Delete the notes you copied';
    clear.onclick = () => {
      const gone = notes.filter(n => n.copiedAt);
      notes = notes.filter(n => !n.copiedAt);
      notesChanged(new Set(gone.map(n => n.path)));
      page.announce('Copied notes deleted');
    };
    head.append(clear);
  }
  box.append(head);
  const gone = notes.filter(n => !inBranch(n.path));
  if (gone.length) {
    const ul = el('ul', 'gone');
    for (const n of gone) {
      const li = el('li');
      li.append(el('span', 'where', `${n.path}:${n.line} · file no longer changed`), el('span', 'body', n.body));
      li.append(iconButton(ICON.trash, 'Delete', () => {
        notes = notes.filter(x => x !== n);
        notesChanged([n.path]);
      }));
      ul.append(li);
    }
    box.append(ul);
  }
  if (!notes.length) box.append(el('p', 'hint', 'Hover a line and press + to leave a note for the agent.'));
  const copy = el('button', 'tool wide');
  copy.dataset.k = 'copy';
  copy.innerHTML = ICON.copy;
  copy.append('Copy for agent…');
  copy.disabled = !notes.length && !openThreads().length;
  copy.onclick = startCopy;
  box.append(copy);
  return box;
}

// goTo scrolls the Branch changes view to a file's box, opening the view first when it is not.
function goTo(path, focus) {
  selectRow(path);
  const v = openView();
  if (!v?.scroll) {
    goal = path;
    page.openBranch();
    return;
  }
  goToBox(v, path, focus);
}

// ----- The Branch changes view: the boxes of the branch changes -----

// The source of the Branch changes boxes: the branch changes, their marks for work not pushed, and
// the notes.
const branchSource = {
  files: () => data?.files || [],
  basis,
  fetch: (f, context) => get('diff', { root: folder, path: f.path, orig: f.orig || '', mode: 'branch', target, context }),
  headParts: f => [f.notPushed && dashed(NOT_PUSHED), threadCount(threadsOn(f.path), notesOn(f.path).length)],
  openFile: f => page.openFile(f.path),
  marks: res => {
    const np = res.notPushed || [];
    const notPushed = n => np.some(([from, to]) => n >= from && n <= to);
    return {
      line: l => (l.k === '+' && notPushed(l.n) ? ' np' : ''),
      hunk: h => {
        const added = h.lines.filter(l => l.k === '+');
        if (!added.length || !added.every(l => notPushed(l.n))) return null;
        const tag = el('span', 'np-tag');
        tag.innerHTML = ICON.local;
        tag.append('Not pushed');
        tag.dataset.tip = "Not pushed yet\nReviewers don't see these lines";
        return tag;
      },
    };
  },
  decorate: async (v, b, rows) => {
    const { extra, where, file } = await place(v, b, rows);
    const th = await placeThreads(v, b, rows, extra, file);
    const under = new Map();
    const end = [];
    const put = (key, e) => under.set(key, [...(under.get(key) || []), e]);
    const texts = new Map([...rows.filter(r => r.k !== '-'), ...extra].map(r => [r.n, r.t]));
    const redrawBox = () => redraw(v, [b.f.path]);
    for (const [t, at] of th.at) {
      threadLines.set(t.id, at);
      put(`n:${at}`, threadEl(t, { redraw: redrawBox, line: at, foot: replyStrip(t, b.f.path, at, texts.get(at)) }));
    }
    for (const [n, at] of where) {
      if (texts.has(at)) put(`n:${at}`, noteEl(v, n));
      else end.push(noteEl(v, n));
    }
    if (v.editor?.path === b.f.path && !v.editor.id) put(`n:${v.editor.n}`, v.editor.el);
    return { extra, under, end, top: th.bar.length ? outdatedBar(`local:${b.f.path}`, th.bar, { redraw: redrawBox }) : [] };
  },
  onAdd: (v, path, l) => openEditor(v, path, l.n, l.t),
  addTip: `Note for the agent\n${keyLabel('addNote')}`,
  onTop: path => selectRow(path),
  drawn: (v, b) => {
    if (v.editor?.path === b.f.path) v.editor.el.querySelector('textarea')?.focus({ preventScroll: true });
  },
  marked: f => notesOn(f.path).length > 0 || threadsOn(f.path).length > 0,
};

// openBranch shows the Branch changes tab. tab.folds keeps the boxes the user folded or opened;
// save stores the tab.
export async function openBranch(viewer, root, tab, keep, save) {
  const v = boxView(viewer, root, tab, keep, save, branchSource);
  v.label = 'Branch changes';
  view = v;
  v.reload = change => { if (isCurrent(v)) load(change); };
  if (root !== folder) return;
  // Draw what is known at once, then check it: the branch may have changed while the tab was hidden.
  if (data) await build(v);
  if (isCurrent(v)) await load();
}

// build draws the view of the branch changes in data, and scrolls to a file asked for before.
async function build(v) {
  v.goal = goal;
  goal = '';
  await showBoxes(v);
}

function noChanges(v) {
  noBoxes(v, error || 'This branch has no branch changes');
}

async function updateView(v, change) {
  if (data) await showBoxes(v, change);
}

// fileLines reads a file on disk: its text per line, and its colored pieces. A file that is gone
// has no lines; null means it could not be read now, so its notes must stay as they are.
async function fileLines(root, path) {
  const out = [];
  for (let from = 0; from < 40000; from += 2000) {
    let part;
    try {
      part = await get('lines', { root, path, from, to: from + 2000 });
    } catch (err) {
      if (err.status !== 404) return null;
      break;
    }
    out.push(...part);
    if (part.length < 2000) break;
  }
  return { texts: out.map(l => (Array.isArray(l) ? l.map(p => p[1]).join('') : l)), pieces: out };
}

// place keeps each note on its line: the same text there, else the nearest line with that text, else
// it is outdated. It gives the line each note shows under, and the lines notes need outside the hunks.
async function place(v, b, rows) {
  const mine = notesOn(b.f.path);
  const byN = new Map(rows.filter(r => r.n && r.k !== '-').map(r => [r.n, r]));
  let file;
  let moved = false;
  for (const n of mine) {
    if (byN.get(n.line)?.t === n.text) {
      moved = relocate(n, { [n.line - 1]: n.text }) || moved;
      continue;
    }
    if (file === undefined) file = await fileLines(v.root, b.f.path);
    if (file) moved = relocate(n, file.texts) || moved;
  }
  if (moved) {
    saveNotes();
    drawPanel();
  }
  const extra = [];
  const where = new Map();
  for (const n of mine) {
    const at = n.outdated && file ? Math.min(n.line, file.texts.length) : n.line;
    where.set(n, at);
    if (at && !byN.has(at) && file?.texts[at - 1] != null && !extra.some(x => x.n === at)) {
      extra.push({ k: ' ', n: at, t: file.texts[at - 1], x: file.pieces[at - 1] });
    }
  }
  return { extra, where, file };
}

// threadsOn are the threads of the branch's pull request on a file.
const threadsOn = path => (branchPR && branchPR.number === ownPR() ? sent(branchPR.threads).filter(t => t.path === path) : []);

// placeThreads puts each thread of the branch's pull request on the line on disk that has the text
// its line has at the head: the same line, else the nearest one. Other threads go to the bar.
async function placeThreads(v, b, rows, extra, file) {
  const at = new Map();
  const bar = [];
  const ts = threadsOn(b.f.path);
  const byN = new Map(rows.filter(r => r.n && r.k !== '-').map(r => [r.n, r]));
  for (const t of ts) {
    let line = !t.old && !t.outdated && t.text != null && byN.get(t.line)?.t === t.text ? t.line : 0;
    if (!line && !t.old && !t.outdated && t.text != null) {
      if (file === undefined) file = await fileLines(v.root, b.f.path);
      line = nearest(file?.texts || [], t.text, t.line);
    }
    if (!line) {
      bar.push(t);
      continue;
    }
    at.set(t, line);
    if (!byN.has(line) && !extra.some(x => x.n === line)) extra.push({ k: ' ', n: line, t: file.texts[line - 1], x: file.pieces[line - 1] });
  }
  return { at, bar };
}

// The reply strips of threads, kept across redraws so what is typed in one stays.
const strips = new Map();

// replyStrip is a thread's reply box in Local mode: what the user types there is an agent note.
function replyStrip(t, path, line, text) {
  let s = strips.get(t.id);
  if (!s) {
    const foot = el('div', 'foot');
    const input = el('input');
    input.placeholder = 'Note for the agent…';
    const hint = el('span', 'muted hint');
    foot.append(avatar(branchPR?.me || 'you'), input, hint);
    s = { foot, input, hint };
    strips.set(t.id, s);
    input.onkeydown = e => {
      if (e.key === 'Enter' && !e.shiftKey) {
        e.preventDefault();
        const body = input.value.trim();
        if (!body) return;
        input.value = '';
        notes.push({ id: newId(), path: s.path, line: s.line, text: s.text, body, at: Date.now(), copiedAt: 0 });
        notesChanged([s.path]);
        page.announce(`Note added on line ${s.line}`);
      } else if (e.key === 'Escape') {
        e.preventDefault();
        e.stopPropagation();
        input.value = '';
        input.blur();
      }
    };
  }
  Object.assign(s, { path, line, text });
  const words = FORGE[prStatus?.forge?.kind] || FORGE.github;
  s.input.setAttribute('aria-label', `Note for the agent on line ${line}`);
  s.hint.textContent = `Reply to ${t.comments[0]?.author} in “As on ${words.name}”`;
  return s.foot;
}

const newId = () => `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`;

// nearest is the line of texts with text that is nearest to line near, or 0.
function nearest(texts, text, near) {
  if (texts[near - 1] === text) return near;
  let line = 0;
  for (const [i, t] of Object.entries(texts)) {
    if (t === text && (!line || Math.abs(+i + 1 - near) < Math.abs(line - near))) line = +i + 1;
  }
  return line;
}

// relocate puts a note on the line of texts that has its text: the same line, else the nearest one,
// else it is outdated and keeps its line number. It tells whether the note changed.
function relocate(n, texts) {
  const line = nearest(texts, n.text, n.line);
  const outdated = !line;
  const changed = (line && line !== n.line) || !!n.outdated !== outdated;
  if (line) n.line = line;
  n.outdated = outdated || undefined;
  return changed;
}

// placeAll puts every note on its line, also notes whose box is not drawn, so the prompt has their
// lines as they are now.
async function placeAll() {
  const key = folder;
  const paths = [...new Set(notes.map(n => n.path))];
  const files = await Promise.all(paths.map(path => fileLines(folder, path)));
  if (key !== folder) return false;
  const moved = new Set();
  paths.forEach((path, i) => {
    if (!files[i]) return;
    for (const n of notes) if (n.path === path && relocate(n, files[i].texts)) moved.add(path);
  });
  if (moved.size) notesChanged(moved);
  return true;
}

function noteEl(v, n) {
  if (v.editor?.id === n.id) return v.editor.el;
  const box = el('div', `thr note${n.copiedAt ? ' copied' : ''}`);
  box.dataset.id = n.id;
  const c = el('div', 'c');
  const who = el('div', 'who');
  who.append(agentMark(), el('b', null, 'Agent note'), el('span', 'muted', ago(n.at)));
  if (n.copiedAt) who.append(el('span', 'chip', 'Copied'));
  if (n.outdated) who.append(el('span', 'chip', 'Outdated'));
  if (!n.copiedAt && !n.outdated) who.append(el('span', 'muted', '· only in kijk'));
  const end = el('span', 'end');
  end.append(
    iconButton(ICON.edit, 'Edit', () => openEditor(v, n.path, n.line, n.text, n)),
    iconButton(ICON.trash, 'Delete', () => {
      notes = notes.filter(x => x !== n);
      notesChanged([n.path]);
      page.announce('Note deleted');
    }),
  );
  who.append(end);
  c.append(who);
  if (n.outdated) c.append(el('span', 'was', n.text));
  c.append(el('p', null, n.body));
  box.append(c);
  return box;
}

function agentMark() {
  if (agentIcon) return agentIcon.cloneNode(true);
  const s = el('span', 'agent plain');
  s.innerHTML = ICON.msg;
  return s;
}

// openEditor opens the note editor under line n, for a new note or to edit note.
function openEditor(v, path, n, text, note) {
  closeEditor(v);
  const box = el('div', 'thr note editing');
  const c = el('div', 'c');
  const who = el('div', 'who');
  who.append(agentMark(), el('b', null, 'Agent note'), el('span', 'muted', `· line ${n}`));
  const close = () => {
    closeEditor(v);
    v.scroll.focus({ preventScroll: true });
  };
  const edit = textBox(`Note for the agent on line ${n}`, note?.body || '', note ? 'Save' : 'Add note', body => {
    if (note) {
      note.body = body;
      note.copiedAt = 0; // the agent has not seen the new text
    } else {
      notes.push({ id: newId(), path, line: n, text, body, at: Date.now(), copiedAt: 0 });
    }
    v.editor = null;
    notesChanged([path]);
    page.announce(note ? 'Note saved' : `Note added on line ${n}`);
    v.scroll.focus({ preventScroll: true });
  }, close, note ? '' : unsentKey(notesKey, path, { k: '+', t: text }));
  edit.querySelector('textarea').placeholder = 'Note for the agent';
  c.append(who, edit);
  box.append(c);
  v.editor = { path, n, id: note?.id, el: box };
  const b = v.boxes.get(path);
  if (b?.loaded) drawBox(v, b);
}

function closeEditor(v) {
  if (!v.editor) return;
  const b = v.boxes.get(v.editor.path);
  v.editor = null;
  if (b?.loaded) drawBox(v, b);
}


// placeThreadsAll finds the line on disk of every open thread, also of those whose box is not
// drawn, so the prompt has the line the thread is on now.
async function placeThreadsAll() {
  const key = folder;
  const ts = openThreads().filter(t => !t.old && t.text != null);
  const paths = [...new Set(ts.map(t => t.path))];
  const files = await Promise.all(paths.map(path => fileLines(folder, path)));
  if (key !== folder) return false;
  for (const t of ts) {
    const file = files[paths.indexOf(t.path)];
    const line = file && nearest(file.texts, t.text, threadLines.get(t.id) || t.line);
    if (line) threadLines.set(t.id, line);
  }
  return true;
}

// ----- Copy for agent -----

// prompt is the text the agent gets: two fixed lines, then each note under its file and line.
function prompt(picked, threads = []) {
  const f = prStatus?.forge;
  const p = prStatus?.pr || data.pr;
  const words = FORGE[f?.kind || 'github'];
  const pr = p ? ` (${words.short} ${words.num}${p.number})` : '';
  const out = [`Review notes for branch ${data.branch}${pr}.`, 'Fix each one, then say what you changed.'];
  for (const n of picked) {
    out.push('', `${n.path}:${n.line}`, ...n.body.split('\n').map(l => `  ${l}`));
  }
  for (const t of threads) {
    const [first, ...replies] = t.comments;
    out.push('', `${t.path}:${threadLines.get(t.id) || t.line} (${first.author} on ${words.name})`, ...first.body.split('\n').map(l => `  ${l}`));
    for (const c of replies) {
      const [l1, ...more] = c.body.split('\n');
      out.push(`  ${c.author}: ${l1}`, ...more.map(l => `  ${l}`));
    }
  }
  return out.join('\n');
}

function closeCopy() {
  copyPanel?.close();
}

async function startCopy() {
  if (!(await placeAll()) || !(await placeThreadsAll())) return;
  const button = panel.querySelector('[data-k=copy]');
  if (button) openCopy(button);
}

function openCopy(button) {
  closeCopy();
  const all = [...notes].sort((a, b) => a.path.localeCompare(b.path) || a.line - b.line);
  const box = el('div', 'copypanel');
  box.setAttribute('role', 'dialog');
  box.setAttribute('aria-label', 'Copy for agent');
  const list = el('ul');
  const picks = all.map(n => {
    const li = el('li', n.copiedAt ? 'used' : '');
    const check = el('input');
    check.type = 'checkbox';
    check.checked = !n.copiedAt;
    check.setAttribute('aria-label', `${n.path}:${n.line}`);
    const what = el('label', 'what');
    const state = [n.copiedAt && 'copied', n.outdated && 'outdated', !inBranch(n.path) && 'file no longer changed'].filter(Boolean).join(', ');
    what.append(el('span', 'where', `${n.path}:${n.line}${state ? ` · ${state}` : ''}`), el('span', 'body', n.body));
    what.onclick = () => { check.checked = !check.checked; update(); };
    check.onchange = update;
    li.append(check, what);
    list.append(li);
    return { n, check };
  });
  const words = FORGE[prStatus?.forge?.kind] || FORGE.github;
  const threadList = el('ul');
  const tpicks = openThreads().sort((a, b) => a.path.localeCompare(b.path) || a.line - b.line).map(t => {
    const used = copiedThreads.has(t.id);
    const li = el('li', used ? 'used' : '');
    const check = el('input');
    check.type = 'checkbox';
    check.checked = !used;
    const where = `${t.path}:${threadLines.get(t.id) || t.line}`;
    check.setAttribute('aria-label', `${where}, ${t.comments[0].author} on ${words.name}`);
    const what = el('label', 'what');
    what.append(el('span', 'where', `${where} · ${t.comments[0].author}${used ? ' · copied' : ''}`), el('span', 'body', t.comments[0].body.split('\n')[0]));
    what.onclick = () => { check.checked = !check.checked; update(); };
    check.onchange = update;
    li.append(check, what);
    threadList.append(li);
    return { t, check };
  });
  const pre = el('pre');
  pre.tabIndex = 0;
  const count = el('span', 'muted');
  const cancel = el('button', 'tool', 'Cancel');
  const copy = el('button', 'tool');
  const acts = el('div', 'acts');
  acts.append(count, cancel, copy);
  box.append(el('h2', null, 'Copy for agent'));
  if (all.length || !tpicks.length) box.append(el('div', 'label', 'Agent notes'), list);
  if (tpicks.length) box.append(el('div', 'label', `Threads on ${words.name}`), threadList);
  box.append(pre, acts);
  function picked() { return picks.filter(p => p.check.checked).map(p => p.n); }
  function pickedThreads() { return tpicks.filter(p => p.check.checked).map(p => p.t); }
  function update() {
    const k = picked().length + pickedThreads().length;
    pre.textContent = prompt(picked(), pickedThreads());
    count.textContent = `${k} of ${all.length + tpicks.length} picked`;
    copy.textContent = `Copy ${k}`;
    copy.disabled = !k;
  }
  update();
  const markCopied = (list, threads) => {
    const at = Date.now();
    for (const n of list) n.copiedAt = at;
    for (const t of threads) copiedThreads.add(t.id);
    if (threads.length) saveCopiedThreads();
    close();
    notesChanged(new Set(list.map(n => n.path)));
    const said = `Copied ${plural(list.length, 'note')}${threads.length ? ` and ${plural(threads.length, 'thread')}` : ''}`;
    page.say(said);
    page.announce(said);
    fetch('/api/count?name=agent-copy', { method: 'POST' }).catch(() => {}); // the local kijk counts, also for a remote folder
  };
  const doCopy = async () => {
    const list = picked();
    const threads = pickedThreads();
    if (!list.length && !threads.length) return;
    try {
      await navigator.clipboard.writeText(prompt(list, threads));
    } catch {
      // The browser refused: keep the prompt selected, so the copy key copies it.
      getSelection().selectAllChildren(pre);
      pre.focus();
      box.classList.add('refused');
      page.announce(`Press ${mac ? '⌘C' : 'Ctrl+C'} to copy the prompt`);
      return;
    }
    markCopied(list, threads);
  };
  const onCopy = () => {
    if (getSelection().toString().trim() === pre.textContent.trim() && picked().length + pickedThreads().length) setTimeout(() => markCopied(picked(), pickedThreads()));
  };
  const outside = e => { if (!box.contains(e.target) && !e.target.closest?.('[data-k=copy]')) close(); };
  function close() {
    box.remove();
    document.removeEventListener('copy', onCopy);
    document.removeEventListener('mousedown', outside, true);
    copyPanel = null;
    if (button.isConnected) button.focus();
  }
  copy.onclick = doCopy;
  cancel.onclick = close;
  // Enter copies when it is let go, so the rest of its key press cannot reach the button that
  // gets the focus back; it must also have gone down here, not on the button that opened the panel.
  let enter = false;
  box.onkeydown = e => {
    if (e.key === 'Escape') {
      e.preventDefault();
      e.stopPropagation();
      close();
    } else if (e.key === 'Enter' && !e.target.closest('button')) {
      e.preventDefault();
      enter = true;
    }
  };
  box.onkeyup = e => {
    if (e.key === 'Enter' && enter) doCopy();
    enter = false;
  };
  document.addEventListener('copy', onCopy);
  document.addEventListener('mousedown', outside, true);
  document.body.append(box);
  const r = button.getBoundingClientRect();
  box.style.left = `${Math.max(8, Math.min(r.left, innerWidth - box.offsetWidth - 8))}px`;
  box.style.bottom = `${innerHeight - r.top + 6}px`;
  copyPanel = { close };
  (picks[0]?.check || tpicks[0]?.check || copy).focus();
}
