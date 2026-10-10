import { get, el } from './api.js';

const CHEVRON = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="m9 18 6-6-6-6"/></svg>';

// One tree is shown at a time. loaded maps a folder path ('' is the root) to its list element.
let tree = null;
let colorOf = () => '';

// paint colors every tree entry with git status; new entries pick up the same colors.
export function paint(container, fn) {
  colorOf = fn;
  for (const item of container.querySelectorAll('.tree .item')) color(item);
}

function color(item) {
  item.lastChild.className = `name ${item.isLink ? 'link ' : ''}${colorOf(item.path, item.isDir)}`;
}

// open lists the expanded folders; without it the tree opens down to reveal, e.g. 'services/api'.
// onFile(path, pin) runs on a click (pin false) and a double click (pin true); onToggle after expand or collapse.
export function showTree(container, root, { open, reveal, onFile, onToggle }) {
  colorOf = () => '';
  const ul = el('ul', 'tree');
  ul.setAttribute('role', 'tree');
  ul.setAttribute('aria-label', 'Files');
  ul.onkeydown = treeKey;
  ul.addEventListener('focusin', e => { if (e.target.classList.contains('item')) setStop(e.target); });
  container.replaceChildren(ul);
  tree = { root, onFile, onToggle, ul, loaded: new Map(), open: new Set(open), selected: null, reveal: open ? null : reveal };
  const parts = !open && reveal ? reveal.split('/') : [];
  parts.forEach((_, i) => tree.open.add(parts.slice(0, i + 1).join('/')));
  loadDir(tree, ul, '', 0);
}

export const openFolders = () => (tree ? [...tree.open] : []);

// selectFile marks the file of the active tab; null clears the mark.
export function selectFile(path) {
  if (!tree) return;
  tree.selected = path;
  for (const item of tree.ul.querySelectorAll('.item')) markSelected(item);
  if (!tree.ul.contains(document.activeElement)) ensureStop();
}

function markSelected(item) {
  const on = item.path === tree.selected;
  item.classList.toggle('selected', on);
  if (on) item.setAttribute('aria-selected', 'true');
  else item.removeAttribute('aria-selected');
}

// The tree is one Tab stop: the focused row keeps it, else the selected file, else the first row.
function setStop(item) {
  for (const i of tree.ul.querySelectorAll('.item[tabindex="0"]')) if (i !== item) i.tabIndex = -1;
  item.tabIndex = 0;
}

function ensureStop() {
  const items = [...tree.ul.querySelectorAll('.item[role=treeitem]')];
  if (!items.length || items.includes(document.activeElement)) return;
  setStop(items.find(i => i.path === tree.selected) || items.find(i => i.tabIndex === 0) || items[0]);
}

// treeKey follows the ARIA tree pattern: ↑/↓ move, → opens or steps in, ← closes or steps out.
function treeKey(e) {
  const item = e.target.closest?.('.item[role=treeitem]');
  if (!item || e.metaKey || e.ctrlKey || e.altKey) return;
  const items = [...tree.ul.querySelectorAll('.item[role=treeitem]')];
  const at = items.indexOf(item);
  const go = i => items[i]?.focus();
  const open = item.getAttribute('aria-expanded');
  switch (e.key) {
    case 'ArrowDown': go(at + 1); break;
    case 'ArrowUp': go(at - 1); break;
    case 'Home': go(0); break;
    case 'End': go(items.length - 1); break;
    case 'ArrowRight':
      if (open === 'false') item.click();
      else if (open === 'true') item.nextElementSibling?.querySelector('.item')?.focus();
      break;
    case 'ArrowLeft':
      if (open === 'true') item.click();
      else item.parentElement.parentElement.closest('li')?.querySelector(':scope > .item')?.focus();
      break;
    case 'Enter': case ' ': item.click(); break;
    default: return;
  }
  e.preventDefault();
}

// refreshTree reloads the loaded folders whose entries may have changed.
export function refreshTree(change) {
  if (!tree) return;
  const dirs = change.all ? [...tree.loaded.keys()] : change.paths.map(p => p.includes('/') ? p.slice(0, p.lastIndexOf('/')) : '');
  for (const d of new Set(dirs)) {
    const spot = tree.loaded.get(d);
    if (spot) loadDir(tree, spot.ul, d, spot.depth);
  }
}

async function loadDir(t, ul, rel, depth) {
  t.loaded.set(rel, { ul, depth });
  try {
    const entries = await get('tree', { root: t.root, path: rel });
    if (t !== tree) return;
    if (rel) ul.setAttribute('role', 'group');
    // Unchanged entries keep their row, so an open subfolder does not empty and fill again.
    const old = new Map([...ul.children].map(li => [li.key, li]));
    ul.replaceChildren(...entries.map(e => {
      const path = rel ? `${rel}/${e.name}` : e.name;
      return old.get(JSON.stringify([path, e])) || node(t, e, path, depth);
    }));
    ensureStop();
  } catch (err) {
    const li = el('li', 'item muted', err.message);
    li.style.paddingLeft = indent(depth);
    ul.replaceChildren(li);
  }
}

const indent = depth => `${8 + depth * 14}px`;

function node(t, e, path, depth) {
  const li = el('li');
  li.key = JSON.stringify([path, e]);
  const item = el('div', 'item');
  item.style.paddingLeft = indent(depth);
  item.tabIndex = -1;
  item.setAttribute('role', 'treeitem');
  item.setAttribute('aria-level', depth + 1);
  item.title = e.link ? `${path} (link)` : path;
  const caret = el('span', 'caret');
  const folder = e.dir && !e.outside;
  if (folder) caret.innerHTML = CHEVRON;
  item.append(caret, el('span', 'name', e.name));
  item.path = path;
  item.isDir = e.dir;
  item.isLink = e.link;
  color(item);
  markSelected(item);
  if (folder) item.setAttribute('aria-expanded', 'false');
  li.setAttribute('role', 'none');
  li.append(item);

  const expand = () => {
    const children = el('ul');
    li.append(children);
    item.classList.add('open');
    item.setAttribute('aria-expanded', 'true');
    t.open.add(path);
    loadDir(t, children, path, depth + 1);
  };
  const collapse = () => {
    li.querySelector('ul')?.remove();
    item.classList.remove('open');
    item.setAttribute('aria-expanded', 'false');
    for (const p of [...t.open]) if (p === path || p.startsWith(`${path}/`)) t.open.delete(p);
    for (const p of [...t.loaded.keys()]) if (p === path || p.startsWith(`${path}/`)) t.loaded.delete(p);
  };
  item.onclick = () => {
    if (!folder) return t.onFile(path, false);
    if (item.classList.contains('open')) collapse();
    else expand();
    t.onToggle();
  };
  item.ondblclick = () => { if (!folder) t.onFile(path, true); };
  if (folder && t.open.has(path)) {
    expand();
    if (path === t.reveal) {
      t.reveal = null;
      requestAnimationFrame(() => item.scrollIntoView({ block: 'nearest' }));
    }
  }
  return li;
}
