import { el, get } from './api.js';
import { line, setView, isCurrent, LINE_HEIGHT } from './view.js';
import { lineCount } from './git.js';
import { hostMark, forgeWords } from './pr.js';
import { keyLabel } from './keys.js';

// The PRs tab for the folder's own branch: the panel in the sidebar and the Branch changes view.
// It asks no forge; the pull request is what the PR line already knows.

const ICON = {
  branch: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M6 3v12"/><circle cx="18" cy="6" r="3"/><circle cx="6" cy="18" r="3"/><path d="M18 9a9 9 0 0 1-9 9"/></svg>',
  arrow: '<svg class="arrow" viewBox="0 0 24 24" aria-hidden="true"><path d="M5 12h14"/><path d="m12 5 7 7-7 7"/></svg>',
  local: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M10.1 2.18a9.93 9.93 0 0 1 3.8 0"/><path d="M17.6 3.71a9.95 9.95 0 0 1 2.69 2.7"/><path d="M21.82 10.1a9.93 9.93 0 0 1 0 3.8"/><path d="M20.29 17.6a9.95 9.95 0 0 1-2.7 2.69"/><path d="M13.9 21.82a9.94 9.94 0 0 1-3.8 0"/><path d="M6.4 20.29a9.95 9.95 0 0 1-2.69-2.7"/><path d="M2.18 13.9a9.93 9.93 0 0 1 0-3.8"/><path d="M3.71 6.4a9.95 9.95 0 0 1 2.7-2.69"/></svg>',
  chev: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="m9 18 6-6-6-6"/></svg>',
  doc: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M15 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7Z"/><path d="M14 2v4a2 2 0 0 0 2 2h4"/><path d="M16 13H8"/><path d="M16 17H8"/><path d="M10 9H8"/></svg>',
};

const KIND = { A: 'added', D: 'deleted', '?': 'untracked', U: 'conflict' };
const kind = status => KIND[status] || 'modified';
const num = n => n.toLocaleString('en-US');
const plural = (n, word) => `${num(n)} ${word}${n === 1 ? '' : 's'}`;

// Files whose diff nobody reads start folded.
const BIG = 2000;
const GENERATED = ['go.sum', 'package-lock.json', 'yarn.lock', 'pnpm-lock.yaml', 'Cargo.lock'];
const lines = f => (f.add || 0) + (f.del || 0);
const generated = path => GENERATED.includes(path.split('/').pop()) || path.endsWith('.min.js');
const HEAD = 34; // px, a box's sticky header
const AHEAD = 5 * LINE_HEIGHT + HEAD; // a jump puts its target this far below the top

const panel = document.getElementById('prs');
let page = null;
let folder = null; // the open folder's key
let status = null; // the folder's git status
let prStatus = null; // the PR line's answer
let target = ''; // the open PR's target branch
let data = null; // the last /api/branch answer
let error = '';
let loading = null; // the /api/branch call in flight: {key, call}
let view = null; // the Branch changes view, while it is open
let selected = ''; // the path of the file at the top of the view
let goal = ''; // a file to scroll to once the view is drawn

// setupReview tells the tab about the folder view around it: announce(text), push(),
// openFile(path) and openBranch(), which opens the Branch changes tab.
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
// changes only while the PRs tab or the Branch changes view shows them.
export async function refreshBranch(st, pr, tgt) {
  status = st;
  prStatus = pr;
  target = tgt;
  if (panel.hidden && !openView()) return;
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

// basis is what every diff of the branch depends on besides its own file: the split point and the upstream.
const basis = () => `${data?.split} ${data?.upHash || ''}`;

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
  const v = openView();
  if (v) await updateView(v, change);
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
    row.onclick = () => goTo(f.path);
    if (f.path === selected) mark(row, true);
    ul.append(row);
  }
  if (!ul.querySelector('[tabindex="0"]') && ul.firstChild) ul.firstChild.tabIndex = 0;
  ul.onkeydown = e => {
    const rows = [...ul.children];
    const at = rows.indexOf(e.target.closest('li'));
    const to = { ArrowDown: at + 1, ArrowUp: at - 1, Home: 0, End: rows.length - 1 }[e.key];
    if (to !== undefined && rows[to]) {
      goTo(rows[to].dataset.path);
      rows[to].focus();
    } else if (e.key === 'Enter' && rows[at]) {
      goTo(rows[at].dataset.path, true);
    } else {
      return;
    }
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

function iconButton(svg, label, act) {
  const b = el('button', 'icon-btn');
  b.innerHTML = svg; // a fixed string
  b.setAttribute('aria-label', label);
  b.dataset.tip = label;
  b.onclick = e => {
    e.stopPropagation();
    act();
  };
  return b;
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
  const b = v.boxes.get(path);
  if (b) v.scroll.scrollTop = b.el.offsetTop - 12;
  if (focus) v.scroll.focus({ preventScroll: true });
}

// ----- The Branch changes view: one box per file, drawn when it comes near the screen -----

// openBranch shows the Branch changes tab. tab.folds keeps the boxes the user folded or opened;
// save stores the tab.
export async function openBranch(viewer, root, tab, keep, save) {
  const v = { kind: 'branch', root, tab, save, boxes: new Map() };
  view = v;
  setView(v);
  v.reload = change => { if (isCurrent(v)) load(change); };
  v.jump = dir => {
    step(v, dir, b => b.starts);
    return true;
  };
  if (root !== folder) return;
  if (!data) await load();
  if (!isCurrent(v)) return;
  if (!data) return viewer.replaceChildren(el('p', 'empty', error || 'This branch has no branch changes'));
  const scroll = el('div', 'allfiles');
  scroll.tabIndex = 0;
  scroll.setAttribute('aria-label', 'Branch changes');
  v.scroll = scroll;
  v.end = el('p', 'fend');
  viewer.replaceChildren(scroll);
  // Two screens ahead, so a box is drawn before it shows.
  v.near = new IntersectionObserver(entries => {
    for (const e of entries) {
      const b = e.target.box;
      b.near = e.isIntersecting;
      if (b.near && b.open && (!b.loaded || b.stale)) loadBox(v, b);
    }
  }, { root: scroll, rootMargin: '200% 0px' });
  await updateView(v);
  if (keep?.scrollTop) scroll.scrollTop = keep.scrollTop;
  if (goal) {
    goTo(goal);
    goal = '';
  }
  let frame = 0;
  scroll.addEventListener('scroll', () => {
    cancelAnimationFrame(frame);
    frame = requestAnimationFrame(() => {
      const a = anchor(v);
      if (a) selectRow(a.b.f.path);
    });
  });
}

// anchor is the box at the top of the view and how far it is scrolled, so a redraw keeps it there.
function anchor(v) {
  const top = v.scroll.scrollTop;
  for (const b of v.boxes.values()) {
    if (b.el.offsetTop + b.el.offsetHeight > top + 12) return { b, offset: top - b.el.offsetTop };
  }
  return null;
}

function restore(v, a) {
  if (a?.b.el.isConnected) v.scroll.scrollTop = a.b.el.offsetTop + a.offset;
}

// updateView follows the branch changes: boxes come and go, and a box whose file changed fetches
// its diff again, or when it is far from the screen, once it comes near. A commit leaves every diff
// as it was; only a new split point or a push changes them all. A box redraws only when its diff
// changed.
async function updateView(v, change) {
  if (!v.scroll || !data) return;
  const a = anchor(v);
  const old = v.boxes;
  v.boxes = new Map();
  for (const f of data.files) {
    let b = old.get(f.path);
    if (b && (b.f.orig !== f.orig || b.f.status !== f.status)) b = null;
    if (b) b.f = f;
    else b = newBox(v, f);
    v.boxes.set(f.path, b);
    drawHead(v, b);
  }
  for (const [path, b] of old) if (v.boxes.get(path) !== b) v.near.unobserve(b.el);
  const els = [...v.boxes.values()].map(b => b.el);
  v.end.textContent = data.files.length ? 'End of the changes' : 'No changes yet';
  if (els.length + 1 !== v.scroll.children.length || els.some((e, i) => v.scroll.children[i] !== e)) {
    v.scroll.replaceChildren(...els, v.end);
    restore(v, a);
  }
  const changed = b => b.basis !== basis() || change?.all || change?.paths.includes(b.f.path) || (b.f.orig && change?.paths.includes(b.f.orig));
  const now = [];
  for (const b of v.boxes.values()) {
    if (!b.loaded || !changed(b)) continue;
    if (b.near && b.open) now.push(loadBox(v, b));
    else b.stale = true;
  }
  await Promise.all(now);
}

function newBox(v, f) {
  const b = { f, el: el('section', 'fbox'), head: el('div', 'fhead'), body: el('div', 'fbody'), loaded: false, context: 3, json: '', starts: [] };
  b.open = v.tab.folds?.[f.path] ?? !(generated(f.path) || lines(f) > BIG);
  b.note = el('div', 'fnote');
  // Until it is drawn, the box is as high as its diff will about be, so the scrollbar is right.
  // The browser skips painting a box far from the screen, and then takes this height for it too.
  const height = Math.min(lines(f) + 8, 5000) * LINE_HEIGHT;
  b.body.style.height = `${height}px`;
  b.el.style.containIntrinsicSize = `auto ${height + HEAD}px`;
  b.el.append(b.head, b.note, b.body);
  b.el.dataset.path = f.path;
  b.el.box = b;
  showOpen(v, b);
  v.near.observe(b.el);
  return b;
}

function showOpen(v, b) {
  b.el.classList.toggle('shut', !b.open);
  b.body.hidden = !b.open;
  b.note.hidden = b.open || !(generated(b.f.path) || lines(b.f) > BIG);
  if (!b.note.hidden) {
    const show = el('button', 'more', 'Show');
    show.onclick = () => fold(v, b, true);
    b.note.replaceChildren(el('span', null, `${generated(b.f.path) ? 'Generated file' : 'Large diff'}, ${plural(lines(b.f), 'line')} · `), show);
  }
}

function fold(v, b, open) {
  b.open = open;
  v.tab.folds = { ...v.tab.folds, [b.f.path]: open };
  v.save();
  drawHead(v, b);
  showOpen(v, b);
  if (open && !b.loaded) loadBox(v, b);
}

function drawHead(v, b) {
  const f = b.f;
  const slash = f.path.lastIndexOf('/');
  const name = f.path.slice(slash + 1);
  const chev = iconButton(ICON.chev, `${b.open ? 'Fold' : 'Open'} ${name}`, () => fold(v, b, !b.open));
  chev.classList.add('fold');
  chev.setAttribute('aria-expanded', b.open);
  const path = el('span', 'fpath');
  path.title = f.orig ? `${f.orig} → ${f.path}` : f.path;
  path.append(el('span', 'muted', slash < 0 ? '' : f.path.slice(0, slash + 1)), el('b', f.status === 'D' ? 'deleted' : null, name));
  const parts = [chev, el('span', `letter ${kind(f.status)}`, f.status), path];
  if (f.notPushed) parts.push(dashed(NOT_PUSHED));
  const lc = lineCount(f.add, f.del);
  if (lc) parts.push(lc);
  parts.push(el('span', 'grow'));
  if (f.status !== 'D') parts.push(iconButton(ICON.doc, 'Open the whole file', () => page.openFile(f.path)));
  b.head.replaceChildren(...parts);
}

// loadBox fetches a box's diff and draws it when it changed. A call during a fetch fetches again
// after it, so a change that came in the meantime is not lost.
async function loadBox(v, b) {
  if (b.loading) {
    b.again = true;
    return b.loading;
  }
  b.loading = (async () => {
    b.basis = basis();
    b.stale = false;
    let res;
    try {
      res = await get('diff', { root: v.root, path: b.f.path, orig: b.f.orig || '', mode: 'branch', target, context: b.context });
    } catch (err) {
      res = { error: err.message };
    }
    if (!isCurrent(v) || v.boxes.get(b.f.path) !== b) return;
    const json = JSON.stringify(res);
    if (b.loaded && json === b.json) return;
    b.json = json;
    b.res = res;
    await drawBox(v, b);
  })();
  try { await b.loading; } finally { b.loading = null; }
  if (b.again) {
    b.again = false;
    await loadBox(v, b);
  }
}

async function drawBox(v, b) {
  const res = b.res;
  b.loaded = true;
  b.starts = [];
  b.body.style.height = '';
  if (res.error || res.binary || res.folder || !res.hunks.length) {
    const say = res.error || (res.binary ? 'Binary file changed' : res.folder ? 'Folder with its own git repository'
      : b.f.orig ? `Renamed from ${b.f.orig}, no content changes` : 'No content changes');
    b.body.replaceChildren(el('p', 'fempty', say));
    return;
  }
  const np = res.notPushed || [];
  const notPushed = n => np.some(([from, to]) => n >= from && n <= to);
  const hunks = res.hunks;
  let maxNo = 0;
  for (const l of hunks.flatMap(h => h.lines)) maxNo = Math.max(maxNo, l.o || 0, l.n || 0);
  b.body.style.setProperty('--gutter', `${String(maxNo).length + 2}ch`);
  const out = [];
  hunks.forEach((h, i) => {
    const added = h.lines.filter(l => l.k === '+');
    const sep = el('div', 'ln sep');
    sep.append(el('span', 'gut'));
    const tx = el('span', 'tx');
    const count = k => h.lines.filter(l => l.k !== k).length;
    tx.append(`@@ -${h.oldStart},${count('+')} +${h.newStart},${count('-')} @@`);
    if (added.length && added.every(l => notPushed(l.n))) {
      const tag = el('span', 'np-tag');
      tag.innerHTML = ICON.local;
      tag.append('Not pushed');
      tag.dataset.tip = "Not pushed yet\nReviewers don't see these lines";
      tx.append(tag);
    }
    if (i > 0 || h.newStart > 1 || h.oldStart > 1) {
      const more = el('button', 'more', 'Show more lines');
      more.onclick = () => {
        b.context += 20;
        loadBox(v, b);
      };
      tx.append(more);
    }
    sep.append(tx);
    out.push(sep);
    h.lines.forEach((l, j) => {
      const local = l.k === '+' && notPushed(l.n);
      const row = line([l.o, l.n], l.x || l.t, `${l.k === '+' ? 'add ch-add' : l.k === '-' ? 'del' : ''}${local ? ' np' : ''}`);
      if (l.k !== ' ' && (j === 0 || h.lines[j - 1].k === ' ')) b.starts.push(row);
      out.push(row);
    });
  });
  b.body.replaceChildren(...out);
}

// ----- Keys: jumps across boxes -----

const offsetIn = (v, e) => e.getBoundingClientRect().top - v.scroll.getBoundingClientRect().top + v.scroll.scrollTop;

// step jumps to the next (dir 1) or previous (dir -1) element that pick(box) gives, across open
// boxes, drawing a box on the way when it is not drawn yet.
async function step(v, dir, pick) {
  if (!v.scroll) return;
  const at = v.scroll.scrollTop + AHEAD;
  const boxes = [...v.boxes.values()].filter(b => b.open);
  for (const b of dir > 0 ? boxes : boxes.reverse()) {
    const top = b.el.offsetTop;
    if (dir > 0 ? top + b.el.offsetHeight < at : top > at) continue;
    if (!b.loaded) {
      await loadBox(v, b);
      if (!isCurrent(v)) return;
    }
    const ys = pick(b).map(e => offsetIn(v, e));
    const y = dir > 0 ? ys.find(y => y > at + 1) : ys.findLast(y => y < at - 1);
    if (y != null) {
      v.scroll.scrollTop = Math.max(0, y - AHEAD);
      return;
    }
  }
}
