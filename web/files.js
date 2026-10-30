import { h } from './api.js';
import { kind, lineCount } from './lines.js';

// The rows of changed files: in the Git tab, in Branch changes, in a pull request, and on another
// page next to its file boxes. Nothing here touches the page when it loads.

// fileRow describes the row of a changed file f ({path, orig, status, add, del}): its status letter,
// name, folder and line counts. before goes in front of the letter, marks between the folder and the
// counts, after behind the counts; the other props are the row's own, and class adds to "item".
export function fileRow(f, { before, marks, after, class: cls, ...props } = {}) {
  const name = f.path.replace(/\/$/, ''), slash = name.lastIndexOf('/'), k = kind(f.status);
  return h('li', {
    key: f.path, class: cls ? `item ${cls}` : 'item', tabIndex: -1, title: f.orig ? `${f.orig} → ${f.path}` : f.path, data: { path: f.path }, ...props,
  }, before, h('span', `letter ${k}`, f.status), h('span', `name ${k}`, name.slice(slash + 1)), h('span', 'dir', slash > 0 ? name.slice(0, slash) : ''),
  marks, lineCount(f.add, f.del), after);
}

// fileList describes a list of file rows with one selected row. The list is one Tab stop, on the
// row of stop: by default the selected row, else the first. A click or an arrow key picks a row with
// pick(path); Enter picks it with pick(path, true), to move the focus to what it shows.
//   label    names the list for screen readers
//   first    a row before the files, with no path, or null; an arrow key to it picks '', Enter clicks it
//   row(f)   more fileRow props for f's row, optional
export function fileList({ label, files, selected, stop, first, row, pick }) {
  stop ??= files.some(f => f.path === selected) ? selected : files[0]?.path;
  return h('ul', { class: 'filelist', attrs: { 'aria-label': label }, onkeydown: e => rowKeys(e, pick) }, first, files.map(f => {
    const more = row?.(f) || {}, on = f.path === selected;
    return fileRow(f, {
      ...more, class: [more.class, on && 'selected'].filter(Boolean).join(' '), tabIndex: f.path === stop ? 0 : -1,
      attrs: on ? { 'aria-current': 'true' } : {}, onclick: () => pick(f.path),
    });
  }));
}

// rowKeys moves in a file list: a row the arrows reach shows at once.
function rowKeys(e, pick) {
  const rows = [...e.currentTarget.children];
  const at = rows.indexOf(e.target.closest('li'));
  const to = { ArrowDown: at + 1, ArrowUp: at - 1, Home: 0, End: rows.length - 1 }[e.key];
  if (to !== undefined && rows[to]) {
    rows[to].focus();
    pick(rows[to].dataset.path || '');
  } else if (e.key === 'Enter' && rows[at]) {
    if (rows[at].dataset.path) pick(rows[at].dataset.path, true);
    else rows[at].click();
  } else {
    return;
  }
  e.preventDefault();
  e.stopPropagation();
}
