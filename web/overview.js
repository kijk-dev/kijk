import { el, get } from './api.js';
import { setView, isCurrent } from './view.js';
import { cachedView, loadView, onView, age } from './prdata.js';
import { avatar, comment } from './threads.js';
import { FORGE } from './pr.js';
import { mac } from './keys.js';

// The Overview tab of a pull request: title, reviewers, description, checks as graphs and the
// conversation. Tab n 0 is the folder's own branch before it has a pull request.

// hooks: branch(): {branch, base, ahead, forge, create}; own(n), whether n is the branch's pull
// request; say(text) and announce(text).
let hooks = {};
let view = null;
let asList = false; // the checks show as a list, not graphs
const logs = new Map(); // job id: its last lines, or the reason there are none

const ICON = {
  check: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M20 6 9 17l-5-5"/></svg>',
  x: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M18 6 6 18"/><path d="m6 6 12 12"/></svg>',
  msg: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M7.9 20A9 9 0 1 0 4 16.1L2 22Z"/></svg>',
  skip: '<svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="12" cy="12" r="9"/><path d="m5.7 5.7 12.6 12.6"/></svg>',
  wait: '<svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/></svg>',
  copy: '<svg viewBox="0 0 24 24" aria-hidden="true"><rect width="14" height="14" x="8" y="8" rx="2"/><path d="M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2"/></svg>',
};
const STATE = { ok: 'Passed', bad: 'Failed', cancel: 'Cancelled', run: 'Running', skip: 'Skipped', wait: 'Waiting' };
const REVIEW = { approved: ['ok', ICON.check, 'Approved'], commented: ['', ICON.msg, 'Commented'], changes: ['bad', ICON.x, 'Changes requested'], requested: ['', ICON.wait, 'Review requested'] };

function svg(markup, cls) {
  const s = el('span', cls);
  s.innerHTML = markup; // a fixed string
  return s;
}

function jobIcon(state) {
  if (state === 'run') {
    const s = el('span', 'ji');
    s.append(el('span', 'rundot'));
    return s;
  }
  return svg({ ok: ICON.check, bad: ICON.x, skip: ICON.skip, cancel: ICON.skip, wait: ICON.wait }[state] || ICON.wait, `ji ${{ ok: 'ok', bad: 'bad' }[state] || 'skip'}`);
}

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
  const parts = [h1, meta];
  if (res.reviewers?.length) {
    const people = el('div', 'people');
    people.append(el('span', 'muted', 'Reviewers'));
    for (const r of res.reviewers) {
      const [cls, icon, word] = REVIEW[r.state] || REVIEW.requested;
      const p = el('span', `person${r.name === res.me ? ' me' : ''}`);
      p.append(avatar(r.name), r.name, svg(icon, `rs ${cls}`));
      p.dataset.tip = `${r.name}\n${word}`;
      p.setAttribute('aria-label', `${r.name}: ${word}`);
      people.append(p);
    }
    parts.push(people);
  }
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
  parts.push(desc, ...checks(v, res, words));
  const talk = el('h2', null, 'Conversation');
  talk.append(el('span', 'muted', res.comments.length ? `${res.comments.length} ${res.comments.length === 1 ? 'comment' : 'comments'}` : 'no comments yet'));
  parts.push(talk);
  if (res.comments.length) {
    const box = el('div', 'thr conv');
    box.append(...res.comments.map(comment));
    parts.push(box);
  }
  column(v, ...parts);
  requestAnimationFrame(() => drawEdges(v));
}

// checks are the CI of the head: a graph per workflow run or pipeline, the end of each failed
// job's log, and the checks of other apps.
function checks(v, res, words) {
  const c = res.checks;
  const head = el('h2', null, words.ci);
  if (!c || (!c.groups.length && !c.other?.length)) {
    head.append(el('span', 'muted', 'none'));
    return [head];
  }
  const jobs = [...c.groups.flatMap(g => g.jobs), ...(c.other || [])];
  const count = st => jobs.filter(j => j.state === st).length;
  const said = [['bad', 'failed'], ['cancel', 'cancelled'], ['run', 'running'], ['wait', 'waiting'], ['skip', 'skipped']]
    .filter(([st]) => count(st)).map(([st, word]) => `${count(st)} ${word}`);
  head.append(el('span', 'muted', said.length ? said.join(' · ') : 'all passed'));
  const out = [head];
  if (c.groups.some(g => g.graph)) {
    const mode = el('span', 'mode h2mode');
    mode.setAttribute('role', 'group');
    mode.setAttribute('aria-label', 'Show as');
    for (const [list, text] of [[false, 'Graph'], [true, 'List']]) {
      const b = el('button', null, text);
      b.setAttribute('aria-pressed', asList === list);
      b.onclick = () => {
        asList = list;
        draw(v, v.res);
      };
      mode.append(b);
    }
    head.append(mode);
  }
  for (const g of c.groups) {
    const wrap = el('div', 'dagwrap');
    const title = el('div', 'dagtitle');
    title.append(el('b', null, g.title));
    if (g.number) title.append(el('span', 'muted', `· ${words.ci === 'Pipeline' ? 'pipeline' : 'run'} #${g.number}`));
    if (g.url) {
      const a = el('a', null, '↗');
      Object.assign(a, { href: g.url, target: '_blank', rel: 'noopener', title: `Open on ${words.name}` });
      title.append(a);
    }
    wrap.append(title, g.graph && !asList ? graph(g) : jobList(g.jobs, 'jobs'));
    out.push(wrap);
  }
  const own = hooks.own?.(res.number);
  for (const j of c.groups.flatMap(g => g.jobs).filter(j => j.state === 'bad' && j.id).slice(0, 3)) out.push(jobLog(v, res, j, own, words));
  if (c.other?.length) {
    const title = el('div', 'dagtitle');
    title.append(el('b', null, 'Other checks'));
    out.push(title, jobList(c.other, 'other'));
  }
  return out;
}

function jobTip(j) {
  return [j.name, [STATE[j.state], j.time].filter(Boolean).join(' · '), j.stage && `Stage ${j.stage}`, j.note].filter(Boolean).join('\n');
}

// graph draws the jobs in columns by their depth in the needs chain; drawEdges adds the lines.
function graph(g) {
  const byName = new Map(g.jobs.map(j => [j.name, j]));
  const depth = new Map();
  const deep = (j, seen = new Set()) => {
    if (depth.has(j.name)) return depth.get(j.name);
    if (seen.has(j.name)) return 0; // a loop kijk did not expect: no deeper
    seen.add(j.name);
    const d = Math.max(-1, ...(j.needs || []).filter(n => byName.has(n)).map(n => deep(byName.get(n), seen))) + 1;
    depth.set(j.name, d);
    return d;
  };
  const cols = [];
  for (const j of g.jobs) (cols[deep(j)] ||= []).push(j);
  const dag = el('div', 'dag');
  const edges = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
  edges.classList.add('edges');
  edges.setAttribute('aria-hidden', 'true');
  const row = el('div', 'dagcols');
  for (const col of cols.filter(Boolean)) {
    const c = el('div', 'dagcol');
    for (const j of col) {
      const pill = el(j.url ? 'a' : 'span', `job ${j.state}`);
      if (j.url) Object.assign(pill, { href: j.url, target: '_blank', rel: 'noopener' });
      pill.dataset.name = j.name;
      pill.dataset.needs = JSON.stringify((j.needs || []).filter(n => byName.has(n)));
      pill.dataset.tip = jobTip(j);
      pill.setAttribute('aria-label', `${j.name}: ${STATE[j.state]}`);
      pill.append(jobIcon(j.state), el('span', 'jn', j.name));
      c.append(pill);
    }
    row.append(c);
  }
  dag.append(edges, row);
  return dag;
}

function drawEdges(v) {
  if (!isCurrent(v) || !v.scroll) return;
  for (const d of v.scroll.querySelectorAll('.dag')) {
    const svgEl = d.querySelector('.edges');
    const box = d.getBoundingClientRect();
    svgEl.setAttribute('width', d.scrollWidth);
    svgEl.setAttribute('height', d.scrollHeight);
    const pills = new Map([...d.querySelectorAll('.job')].map(p => [p.dataset.name, p]));
    const paths = [];
    for (const j of pills.values()) {
      const b = j.getBoundingClientRect();
      for (const n of JSON.parse(j.dataset.needs)) {
        const a = pills.get(n)?.getBoundingClientRect();
        if (!a) continue;
        const x1 = a.right - box.left + d.scrollLeft, y1 = a.top + a.height / 2 - box.top;
        const x2 = b.left - box.left + d.scrollLeft, y2 = b.top + b.height / 2 - box.top;
        const mx = (x1 + x2) / 2;
        const path = document.createElementNS('http://www.w3.org/2000/svg', 'path');
        path.setAttribute('d', `M${x1} ${y1} C${mx} ${y1} ${mx} ${y2} ${x2} ${y2}`);
        // A job that did not run, or waits, has a dashed line to it.
        if (j.matches('.skip, .wait, .cancel')) path.classList.add('dim');
        paths.push(path);
      }
    }
    svgEl.replaceChildren(...paths);
  }
}

function jobList(jobs, cls) {
  const ul = el('ul', `ovlist ${cls}`);
  for (const j of jobs) {
    const li = el('li');
    li.append(jobIcon(j.state), el('span', 'grow', j.name));
    if (j.note || j.time) li.append(el('span', 'muted', j.note || j.time));
    if (j.url) {
      const a = el('a', null, 'Details ↗');
      Object.assign(a, { href: j.url, target: '_blank', rel: 'noopener' });
      li.append(a);
    }
    li.title = jobTip(j);
    ul.append(li);
  }
  return ul;
}

// jobLog shows the last lines of a failed job's log, and on your own branch hands them to the agent.
function jobLog(v, res, j, own, words) {
  const box = el('div', 'joblog');
  const head = el('div', 'jlhead');
  head.append(jobIcon('bad'), el('b', null, j.name), el('span', 'muted', `failed${j.time ? ` after ${j.time}` : ''} · last lines of the log`));
  const pre = el('pre');
  pre.tabIndex = 0;
  const known = logs.get(j.id);
  if (known?.lines) pre.textContent = known.lines.join('\n');
  else pre.textContent = known?.error || 'Reading the log…';
  if (own && known?.lines?.length) {
    const copy = el('button', 'tool');
    copy.innerHTML = ICON.copy;
    copy.append('Copy for agent');
    copy.onclick = () => copyFailure(res, j, known.lines, pre, words);
    head.append(copy);
  }
  if (j.url) {
    const a = el('a', 'grow-end', 'Full log ↗');
    Object.assign(a, { href: j.url, target: '_blank', rel: 'noopener' });
    head.append(a);
  }
  box.append(head, pre);
  if (!known) {
    logs.set(j.id, { loading: true });
    get('pr/log', { root: v.root, job: j.id })
      .then(r => logs.set(j.id, { lines: r.lines }))
      .catch(err => logs.set(j.id, { error: err.message }))
      .then(() => { if (isCurrent(v) && v.res) draw(v, v.res); });
  }
  return box;
}

async function copyFailure(res, j, lines, pre, words) {
  const text = [`CI job ${j.name} failed on branch ${res.head} (${words.short} ${words.num}${res.number}).`, 'Fix it, then say what you changed.', '', ...lines].join('\n');
  try {
    await navigator.clipboard.writeText(text);
  } catch {
    getSelection().selectAllChildren(pre); // the browser refused: the copy key copies it
    pre.focus();
    hooks.announce?.(`Press ${mac ? '⌘C' : 'Ctrl+C'} to copy the log`);
    return;
  }
  hooks.say?.(`Copied the failure of ${j.name}`);
  hooks.announce?.(`Copied the failure of ${j.name}`);
  fetch('/api/count?name=agent-copy', { method: 'POST' }).catch(() => {}); // the local kijk counts, also for a remote folder
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
