import { el } from './api.js';
import { setView, isCurrent } from './view.js';
import { cachedView, loadView, onView, age } from './prdata.js';
import { avatar } from './threads.js';
import { FORGE } from './pr.js';

// The Overview tab of a pull request, in the reading column: its title, who wants to merge what,
// and its description. Tab n 0 is the folder's own branch before it has a pull request.

let hooks = {}; // branch(): {branch, base, ahead, forge, create}
let view = null;

export function setupOverview(h) {
  hooks = h;
}

export async function openOverview(viewer, root, tab) {
  const v = { kind: 'overview', viewer, root, n: tab.n };
  setView(v);
  view = v;
  if (!tab.n) return drawBranch(v);
  const known = cachedView(root, tab.n);
  if (known) draw(v, known);
  else viewer.replaceChildren(el('p', 'empty', `Fetching #${tab.n}…`));
  try {
    await loadView(root, tab.n);
  } catch (err) {
    if (isCurrent(v) && !v.res) viewer.replaceChildren(el('p', 'empty', err.message));
  }
}

onView((root, n, res) => {
  if (view && isCurrent(view) && view.root === root && view.n === n) draw(view, res);
});

function ref(name, cls) {
  return el('span', `ref${cls ? ` ${cls}` : ''}`, name);
}

function column(v, ...parts) {
  const doc = el('article', 'md ov');
  doc.append(...parts);
  const scroll = el('div', 'scroll');
  scroll.append(doc);
  const pane = el('div', 'pane');
  pane.append(scroll);
  const top = v.scroll?.scrollTop;
  v.viewer.replaceChildren(pane);
  v.scroll = scroll;
  if (top) scroll.scrollTop = top;
  return doc;
}

function draw(v, res) {
  v.res = res;
  const words = FORGE[res.forge?.kind] || FORGE.github;
  const h1 = el('h1', null, res.title);
  h1.append(' ', el('span', 'muted', `${words.num}${res.number}`));
  const meta = el('div', 'meta');
  meta.append(avatar(res.author), el('span', null, `${res.author === res.me ? 'You want' : `${res.author} wants`} to merge`), ref(res.head),
    el('span', null, 'into'), ref(res.base, 'lane-main'), el('span', null, `· ${age(res.createdAt)} ago · ${res.commits} ${res.commits === 1 ? 'commit' : 'commits'}`));
  const desc = el('div', 'desc');
  if (res.bodyHtml) {
    const t = document.createElement('template'); // inert until it is in the page
    t.innerHTML = res.bodyHtml; // rendered by kijk's server, which drops raw HTML
    for (const a of t.content.querySelectorAll('a[href]')) {
      a.target = '_blank';
      a.rel = 'noopener';
    }
    desc.append(t.content);
  } else {
    desc.append(el('p', 'muted', 'No description.'));
  }
  column(v, h1, meta, desc);
}

function drawBranch(v) {
  const b = hooks.branch?.() || {};
  const words = FORGE[b.forge] || FORGE.github;
  const meta = el('div', 'meta');
  meta.append(avatar('you'), el('span', null, 'Your branch, against'), ref(b.base || 'main', 'lane-main'));
  if (b.ahead) meta.append(el('span', null, `· ${b.ahead} not pushed`));
  const parts = [el('h1', null, b.branch || 'This branch'), meta,
    el('p', null, `There is no ${words.kind} for this branch yet. You can still review the branch changes and leave agent notes.`)];
  if (b.create && b.forge) {
    const a = el('a', null, `Create ${words.kind} on ${words.name} ↗`);
    Object.assign(a, { href: b.create, target: '_blank', rel: 'noopener' });
    const p = el('p');
    p.append(a);
    parts.push(p);
  }
  column(v, ...parts);
}
