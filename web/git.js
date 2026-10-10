import { el, get } from './api.js';

const KIND = { A: 'added', D: 'deleted', '?': 'untracked', U: 'conflict' };
const kind = status => KIND[status] || 'modified';
const filesOf = c => (c.orig ? [c.path, c.orig] : [c.path]);
const shown = path => path.replace(/\/$/, '');

// marks turns a git status into tree colors: per file, per folder with changes, and ignored paths.
export function marks(st) {
  const file = new Map();
  const untrackedDirs = []; // git lists a folder it does not look into, like a nested repository, as "name/"
  for (const c of st.changes) file.set(c.path, kind(c.status));
  for (const c of st.untracked) {
    if (c.path.endsWith('/')) untrackedDirs.push(c.path);
    else file.set(c.path, 'untracked');
  }
  for (const c of st.staged) file.set(c.path, kind(c.status)); // a staged "added" stays added after later edits
  for (const c of st.conflicts) file.set(c.path, 'conflict');
  const dirs = new Set();
  for (const p of file.keys()) {
    const parts = p.split('/');
    for (let i = 1; i < parts.length; i++) dirs.add(parts.slice(0, i).join('/'));
  }
  const ignored = st.ignored;
  return (path, isDir) => {
    const own = isDir ? (dirs.has(path) ? 'modified' : '') : file.get(path);
    if (own) return own;
    const p = isDir ? `${path}/` : path;
    if (untrackedDirs.some(d => p.startsWith(d))) return 'untracked';
    return ignored.some(i => (i.endsWith('/') ? p.startsWith(i) : p === i)) ? 'ignored' : '';
  };
}

const num = n => n.toLocaleString('en-US');

// lineCount shows "+40 −12", or nothing when both are zero. Screen readers hear words instead.
export function lineCount(add = 0, del = 0) {
  if (!add && !del) return null;
  const s = el('span', 'lc');
  if (add) s.append(el('span', 'lc-add', `+${num(add)}`));
  if (del) s.append(el('span', 'lc-del', `−${num(del)}`));
  for (const part of s.children) part.setAttribute('aria-hidden', 'true');
  const lines = n => `${num(n)} ${n === 1 ? 'line' : 'lines'}`;
  s.append(el('span', 'sr-only', add && del ? `${lines(add)} added, ${num(del)} removed` : add ? `${lines(add)} added` : `${lines(del)} removed`));
  return s;
}

// items is the flat list the arrow keys walk: conflicts, then staged, then unstaged.
let items = [];
let selectedKey = null;
let handlers = null;

function fileRow(c, onClick) {
  const name = shown(c.path);
  const slash = name.lastIndexOf('/');
  const row = el('li', 'item');
  row.tabIndex = -1;
  row.title = c.orig ? `${c.orig} → ${c.path}` : c.path;
  row.append(
    el('span', `letter ${kind(c.status)}`, c.status),
    el('span', `name ${kind(c.status)}`, name.slice(slash + 1)),
    el('span', 'dir', slash > 0 ? name.slice(0, slash) : ''),
  );
  const lc = lineCount(c.add, c.del);
  if (lc) row.append(lc);
  row.onclick = onClick;
  return row;
}

function box(title, list, action) {
  const section = el('section');
  const h = el('h3', null, `${title} ${list.length}`);
  if (title !== 'Conflicts') {
    const lc = lineCount(...list.reduce(([a, d], [c]) => [a + (c.add || 0), d + (c.del || 0)], [0, 0]));
    if (lc) h.append(lc);
  }
  if (action) {
    const b = el('button', 'all', action.label);
    b.title = action.title;
    b.onclick = action.run;
    h.append(b);
  }
  section.append(h);
  return section;
}

// handlers: {onOpen(change, mode, pin), onStage(files), onUnstage(files)}
export function showChanges(container, st, h) {
  handlers = h;
  items = [];
  const unstaged = [...st.changes.map(c => [c, 'unstaged']), ...st.untracked.map(c => [c, 'untracked'])];
  if (!st.conflicts.length && !st.staged.length && !unstaged.length) {
    return container.replaceChildren(el('p', 'note', 'No changes'));
  }
  const lists = [
    ['Conflicts', st.conflicts.map(c => [c, 'conflict']), null],
    ['Staged', st.staged.map(c => [c, 'staged']),
      { label: '− all', title: 'Unstage all', run: () => h.onUnstage(st.staged.flatMap(filesOf)) }],
    ['Unstaged', unstaged,
      { label: '+ all', title: 'Stage all', run: () => h.onStage(unstaged.flatMap(([c]) => filesOf(c))) }],
  ];
  const out = [];
  for (const [title, list, action] of lists) {
    if (!list.length && title !== 'Staged') continue;
    const section = box(title, list, list.length ? action : null);
    const ul = el('ul');
    list.forEach(([c, mode], index) => {
      const item = { key: `${mode}:${c.path}`, change: c, mode, list: title, index };
      item.row = fileRow(c, () => select(item));
      item.row.ondblclick = () => h.onOpen(c, mode, true);
      if (mode !== 'conflict') {
        const check = el('input');
        check.type = 'checkbox';
        check.checked = mode === 'staged';
        check.tabIndex = -1; // keys go to the row, so Space always means "stage and next"
        check.onmousedown = e => e.preventDefault();
        check.setAttribute('aria-label', `${mode === 'staged' ? 'Unstage' : 'Stage'} ${c.path}`);
        check.onclick = e => {
          e.stopPropagation();
          toggle(item);
        };
        item.row.prepend(check);
      }
      if (item.key === selectedKey) mark(item.row, true);
      items.push(item);
      ul.append(item.row);
    });
    if (!list.length) ul.append(el('li', 'note', 'Nothing staged'));
    section.append(ul);
    out.push(section);
  }
  const focused = container.contains(document.activeElement); // a refresh while the user walks the list
  container.replaceChildren(...out);
  const stop = items.find(i => i.key === selectedKey) || items[0];
  if (stop) stop.row.tabIndex = 0;
  if (focused) stop?.row.focus({ preventScroll: true });
}

// The file lists are one Tab stop, on the selected row; ↑/↓ and Space work from there.
function mark(row, on) {
  row.classList.toggle('selected', on);
  row.tabIndex = on ? 0 : -1;
  if (on) row.setAttribute('aria-current', 'true');
  else row.removeAttribute('aria-current');
}

function select(item) {
  for (const i of items) mark(i.row, i === item);
  selectedKey = item.key;
  item.row.focus({ preventScroll: true });
  item.row.scrollIntoView({ block: 'nearest' });
  handlers.onOpen(item.change, item.mode);
}

// toggle stages or unstages a file, then selects the next file of the same list, like an inbox.
function toggle(item) {
  if (item.mode === 'conflict') return;
  const same = items.filter(i => i.list === item.list);
  const next = same[item.index + 1] || same[item.index - 1];
  if (item.key === selectedKey) {
    if (next) select(next);
    else selectedKey = null;
  }
  (item.mode === 'staged' ? handlers.onUnstage : handlers.onStage)(filesOf(item.change));
}

// reviewKey handles Arrow Up/Down and Space; it returns true when it used the key.
export function reviewKey(e) {
  if (!items.length) return false;
  const at = items.findIndex(i => i.key === selectedKey);
  if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
    const step = e.key === 'ArrowDown' ? 1 : -1;
    select(items[at < 0 ? 0 : Math.min(items.length - 1, Math.max(0, at + step))]);
    return true;
  }
  if (e.key === ' ' && at >= 0) {
    toggle(items[at]);
    return true;
  }
  return false;
}

const commitFiles = new Map(); // by hash; a commit's files never change

// The graph is git's text graph ("| * ", "|/") drawn as lines: each character is a stroke in its cell.
const CW = 7; // px per character; a lane is two characters wide
const RH = 22; // px, the height of a commit row
const JOIN = 10; // px, the height of a row that only joins lanes
const cx = c => c * CW + CW / 2;

// Lanes that reach into row i from above, or leave it downwards, at column c.
const fromAbove = (up, c) => '|*'.includes(up[c] || ' ') || up[c + 1] === '/' || up[c - 1] === '\\';
const toBelow = (down, c) => '|*'.includes(down[c] || ' ') || down[c - 1] === '/' || down[c + 1] === '\\';

// Lane colours by branch: this one, the PR target, main. The server says which branch has each commit.
const LANE = ['lane-cur', 'lane-target', 'lane-main'];

// colour gives each character of every row the lane class of the line it draws. A commit starts
// its branch's colour below it; a line keeps the colour it came down with.
function colour(rows) {
  let cols = [];
  for (const r of rows) {
    const g = r.g, next = [...cols];
    r.cls = [...g].map((ch, c) => {
      if (ch === '*') return [cols[c], (next[c] = LANE[r.l || 0])];
      if (ch === '/') {
        if (!'|*'.includes(g[c - 1] || ' ')) next[c - 1] = cols[c + 1]; // two lanes that meet keep the one going on
        return cols[c + 1];
      }
      if (ch === '\\') {
        if (!'|*'.includes(g[c + 1] || ' ')) next[c + 1] = cols[c - 1];
        return cols[c - 1];
      }
      return ch === '|' ? cols[c] : cols[c - 1];
    });
    r.down = next;
    cols = next;
  }
}

function svg(width, h, paths, dot) {
  const s = el('span', 'graph');
  // Only numbers and fixed class names go into this markup, never server text.
  const d = Object.entries(paths).map(([cls, p]) => `<path class="${cls}" d="${p}"/>`).join('');
  s.innerHTML = `<svg width="${width}" height="${h}" aria-hidden="true">${d}${dot || ''}</svg>`;
  return s;
}

function graphCell(rows, i, width, head) {
  const r = rows[i], g = r.g, up = rows[i - 1]?.g || '', down = rows[i + 1]?.g || '';
  const h = r.c ? RH : JOIN;
  const paths = {};
  let dot = '';
  const line = (cls = '', x1, y1, x2, y2) => { paths[cls] = `${paths[cls] || ''}M${x1} ${y1}L${x2} ${y2}`; };
  [...g].forEach((ch, c) => {
    const cls = r.cls[c];
    if (ch === '|') line(cls, cx(c), 0, cx(c), h);
    else if (ch === '/') line(cls, cx(c + 1), 0, cx(c - 1), h);
    else if (ch === '\\') line(cls, cx(c - 1), 0, cx(c + 1), h);
    else if (ch === '_') line(cls, cx(c - 1), h, cx(c + 1), h);
    else if (ch === '-' || ch === '.') line(cls, cx(c - 1), h / 2, cx(c + 1), h / 2);
    else if (ch === '*') {
      if (fromAbove(up, c)) line(cls[0], cx(c), 0, cx(c), h / 2);
      if (toBelow(down, c)) line(cls[1], cx(c), h / 2, cx(c), h);
      dot = `<circle cx="${cx(c)}" cy="${h / 2}" r="3.5" class="${cls[1]}${head ? ' head' : ''}"/>`;
    }
  });
  return svg(width, h, paths, dot);
}

// laneCell continues the lanes of commit row i past the file rows opened under it.
function laneCell(rows, i, width) {
  const r = rows[i], down = rows[i + 1]?.g || '';
  const paths = {};
  [...r.g].forEach((ch, c) => {
    if (ch !== '|' && !(ch === '*' && toBelow(down, c))) return;
    const cls = r.down[c] || '';
    paths[cls] = `${paths[cls] || ''}M${cx(c)} 0L${cx(c)} ${RH}`;
  });
  return svg(width, RH, paths);
}

const BRANCH_ICON = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M6 3v12"/><circle cx="18" cy="6" r="3"/><circle cx="6" cy="18" r="3"/><path d="M18 9a9 9 0 0 1-9 9"/></svg>';

function refPill(ref, lane) {
  if (ref.startsWith('tag: ')) return el('span', 'ref tag', ref.slice(5));
  if (ref === 'HEAD') return el('span', 'ref head', ref); // detached
  const head = ref.startsWith('HEAD -> ');
  const pill = el('span', head ? 'ref head' : `ref ${LANE[lane]}`);
  pill.innerHTML = BRANCH_ICON; // a fixed string; the name goes in as text
  pill.append(head ? ref.slice(8) : ref);
  return pill;
}

// The branch graph starts folded; each commit opens its files. Opening or folding it is kept per browser.
let isOpen = false;
try { isOpen = localStorage.getItem('kijk.graphOpen') === '1'; } catch { /* storage blocked */ }
let call = 0;
let last = null; // the latest showGraph arguments, for revealHead
const openCommits = new Set();

function setOpen(on) {
  isOpen = on;
  try { localStorage.setItem('kijk.graphOpen', on ? '1' : '0'); } catch { /* storage blocked */ }
}

export async function showGraph(container, root, onOpen, target = '') {
  last = [container, root, onOpen, target];
  const mine = ++call;
  const toggleRow = el('button', 'section-toggle');
  toggleRow.setAttribute('aria-expanded', isOpen);
  toggleRow.append(el('span', `chev${isOpen ? ' open' : ''}`, '›'), 'Branch graph');
  toggleRow.onclick = () => {
    setOpen(!isOpen);
    showGraph(container, root, onOpen, target);
  };
  if (!isOpen) return container.replaceChildren(toggleRow);

  let res;
  try {
    res = await get('graph', { root, target });
  } catch (err) {
    res = err;
  }
  if (mine !== call) return;
  if (res instanceof Error) return container.replaceChildren(toggleRow, el('p', 'note', res.message));
  const rows = res.rows;
  if (!rows.length) return container.replaceChildren(toggleRow, el('p', 'note', 'No commits yet'));
  colour(rows);
  const width = Math.max(...rows.map(r => r.g.length)) * CW;
  const ul = el('ul', 'commits');
  rows.forEach((r, i) => {
    const li = el('li');
    ul.append(li);
    if (!r.c) return li.append(graphCell(rows, i, width));
    const c = r.c;
    const head = !!c.refs?.some(x => x.startsWith('HEAD'));
    const fork = c.hash === res.fork && !c.refs; // marked only when no branch label sits on it
    const row = el('div', head ? 'item commit head' : 'item commit');
    row.setAttribute('aria-expanded', openCommits.has(c.hash));
    row.title = `${c.hash}\n${c.author}${fork ? '\nThe branches split here' : ''}`;
    row.append(graphCell(rows, i, width, head), ...(c.refs || []).map(x => refPill(x, r.l || 0)));
    if (fork) row.append(el('span', 'ref fork', 'split'));
    row.append(el('span', 'name', c.subject), el('span', 'dir', ago(c.time * 1000)));
    li.append(row);
    const files = el('ul');
    const fill = list => {
      if (list instanceof Error) return files.replaceChildren(el('li', 'note', list.message));
      files.replaceChildren(...list.map(f => {
        const fr = fileRow(f, () => {
          container.querySelector('.item.selected')?.classList.remove('selected');
          fr.classList.add('selected');
          onOpen({ ...f, hash: c.hash }, 'commit');
        });
        fr.ondblclick = () => onOpen({ ...f, hash: c.hash }, 'commit', true);
        fr.classList.add('sub');
        fr.prepend(laneCell(rows, i, width));
        return fr;
      }));
    };
    // A cached list fills in before the rows are shown, so a refresh does not blink.
    const load = () => {
      if (commitFiles.has(c.hash)) return fill(commitFiles.get(c.hash));
      get('commit-files', { root, hash: c.hash }).then(list => {
        commitFiles.set(c.hash, list);
        fill(list);
      }, fill);
    };
    row.onclick = () => {
      const on = !openCommits.delete(c.hash);
      row.setAttribute('aria-expanded', on);
      if (!on) return files.replaceChildren();
      openCommits.add(c.hash);
      load();
    };
    if (openCommits.has(c.hash)) load();
    li.append(files);
  });
  ul.onkeydown = graphKey;
  const first = ul.querySelector('.item.commit');
  if (first) first.tabIndex = 0;
  container.replaceChildren(toggleRow, ul);
}

// The graph is one Tab stop too: ↑/↓ walk commits and their open files, Enter opens one.
function graphKey(e) {
  const row = e.target.closest?.('.item');
  if (!row || e.metaKey || e.ctrlKey || e.altKey) return;
  const rows = [...e.currentTarget.querySelectorAll('.item')];
  const to = { ArrowDown: rows.indexOf(row) + 1, ArrowUp: rows.indexOf(row) - 1, Home: 0, End: rows.length - 1 }[e.key];
  if (to !== undefined && rows[to]) {
    row.tabIndex = -1;
    rows[to].tabIndex = 0;
    rows[to].focus();
  } else if (e.key === 'Enter' || e.key === ' ') {
    row.click();
  } else {
    return;
  }
  e.preventDefault();
  e.stopPropagation();
}

// revealHead opens the graph and points at the commit HEAD is on.
export async function revealHead() {
  if (!last) return;
  if (!isOpen) setOpen(true);
  await showGraph(...last);
  const row = last[0].querySelector('.item.head');
  if (!row) return;
  row.scrollIntoView({ block: 'nearest' });
  row.classList.remove('flash');
  void row.offsetWidth; // restart the animation
  row.classList.add('flash');
}

export function ago(ms) {
  const s = Math.max(0, (Date.now() - ms) / 1000);
  if (s < 60) return 'just now';
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
  return `${Math.floor(s / 86400)}d ago`;
}
