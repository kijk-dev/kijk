import { el, get } from './api.js';
import { isCurrent } from './view.js';
import { boxView, showBoxes, noBoxes, goToBox, redraw } from './boxes.js';
import { cachedView, loadView, onView } from './prdata.js';
import { placeForge, threadCount } from './threads.js';

// The Changes tab of a pull request: its forge diff in file boxes, with its threads under their lines.

let hooks = {}; // onTop(n, path), openFile(n, path)
let view = null;
const goals = new Map(); // a file to scroll to once the Changes of a pull request show

export function setupChanges(h) {
  hooks = h;
}

const threadsOn = (v, path) => (v.res?.threads || []).filter(t => t.path === path);

function source(v) {
  const { root, n } = v;
  return {
    files: () => v.res?.files || [],
    basis: () => `${v.res?.split} ${v.res?.at}`,
    fetch: async (f, context) => {
      const q = { root, path: f.path, orig: f.orig || '', mode: 'pr', pr: n, context };
      try {
        return await get('diff', q);
      } catch (err) {
        if (err.status !== 409) throw err;
        await loadView(root, n); // kijk restarted and lost where the forge diff starts
        return get('diff', q);
      }
    },
    headParts: f => [threadCount(threadsOn(v, f.path))],
    openFile: f => hooks.openFile?.(n, f.path),
    openTip: `Open the whole file\nat #${n}`,
    decorate: (v2, b, rows) => placeForge(`${n}:${b.f.path}`, rows, threadsOn(v, b.f.path), { redraw: () => redraw(v, [b.f.path]) }),
    onTop: path => hooks.onTop?.(n, path),
    marked: f => threadsOn(v, f.path).length > 0,
  };
}

// openChanges shows the Changes tab of pull request tab.n.
export async function openChanges(viewer, root, tab, keep, save) {
  const v = boxView(viewer, root, tab, keep, save, {});
  v.n = tab.n;
  v.label = `Changes of #${tab.n}`;
  Object.assign(v.src, source(v));
  v.reload = () => {}; // the forge diff does not follow the files on disk
  view = v;
  const known = cachedView(root, tab.n);
  if (known) await show(v, known);
  else viewer.replaceChildren(el('p', 'empty', `Fetching #${tab.n}…`));
  try {
    await loadView(root, tab.n);
  } catch (err) {
    if (isCurrent(v) && !v.scroll) noBoxes(v, err.message);
  }
}

onView((root, n, res) => {
  if (view && isCurrent(view) && view.root === root && view.n === n) show(view, res);
});

async function show(v, res) {
  v.res = res;
  if (!res.files.length) return noBoxes(v, res.error || 'No changes');
  if (!v.scroll) v.goal = goals.get(`${v.root}\n${v.n}`) || '';
  goals.delete(`${v.root}\n${v.n}`);
  await showBoxes(v);
}

// goToChange scrolls the Changes of pull request n to a file, now or once they show.
export function goToChange(root, n, path) {
  if (view && isCurrent(view) && view.root === root && view.n === n && view.scroll) return goToBox(view, path, true);
  goals.set(`${root}\n${n}`, path);
}
