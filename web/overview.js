import { el, h, get, patch } from './api.js';
import { setView, isCurrent } from './view.js';
import { pull, read, watch } from './prstore.js';
import { avatar, comment, age, newTab } from './threads.js';
import { forgeWords } from './pr.js';
import { mac } from './keys.js';
import { threadOpts, writable, summarySlot, commentFoot } from './drafts.js';

// The Overview tab of a pull request: title, reviewers, description, checks as graphs and the
// conversation. Tab n 0 is the folder's own branch before it has a pull request.

// hooks: branch(): {branch, base, ahead, forge, create}, with forge as in the PR line's answer;
// own(n), whether n is the branch's pull request; say(text) and announce(text).
let hooks = {};
let view = null;
let asList = false; // the checks show as a list, not graphs
const logs = new Map(); // job id: its last lines, or why there are none and the answer it was asked under

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

function jobIcon(state) {
  if (state === 'run') return h('span', 'ji', h('span', 'rundot'));
  return h('span', { class: `ji ${{ ok: 'ok', bad: 'bad' }[state] || 'skip'}`, html: { ok: ICON.check, bad: ICON.x, skip: ICON.skip, cancel: ICON.skip, wait: ICON.wait }[state] || ICON.wait });
}

export function setupOverview(p) {
  hooks = p;
}

export async function openOverview(viewer, root, tab) {
  const v = { kind: 'overview', viewer, root, n: tab.n, conv: el('div', 'thr conv') };
  setView(v);
  view = v;
  if (!tab.n) return drawBranch(v);
  const known = pull(root, tab.n).res;
  if (known) draw(v, known);
  else patch(viewer, h('p', 'empty', `Fetching ${tab.num || '#'}${tab.n}…`));
  await read(root, tab.n);
  const why = pull(root, tab.n).why;
  if (why && isCurrent(v) && !v.res) patch(viewer, h('p', 'empty', why));
}

watch(root => {
  const res = view && isCurrent(view) && view.root === root && pull(root, view.n).res;
  if (res && res !== view.res) draw(view, res);
  else if (res) drawConv(view);
});

function ref(name, cls) {
  return h('span', `ref${cls ? ` ${cls}` : ''}`, name);
}

// column puts the view's page in the viewer, once per view; each draw then changes it in place.
function column(v, ...parts) {
  if (!v.doc) {
    v.doc = el('article', 'md ov');
    v.scroll = el('div', 'scroll');
    v.scroll.append(v.doc);
    patch(v.viewer, h('div', 'pane', v.scroll));
  }
  patch(v.doc, parts);
}

function draw(v, res) {
  v.res = res;
  column(v, content(v, res));
  requestAnimationFrame(() => drawEdges(v));
}

function content(v, res) {
  const words = forgeWords(res);
  const said = res.comments.filter(c => !c.draft).length;
  if (writable(res) || res.comments.length) drawConv(v);
  return [
    h('h1', null, res.title, ' ', h('span', 'muted', `${words.num}${res.number}`)),
    h('div', 'meta', avatar(res.author), h('span', null, `${res.author === res.me ? 'You want' : `${res.author} wants`} to merge`), ref(res.head),
      h('span', null, 'into'), ref(res.base, 'lane-main'), h('span', null, `· ${age(res.createdAt)} ago · ${res.commits} ${res.commits === 1 ? 'commit' : 'commits'}`)),
    res.reviewers?.length > 0 && h('div', { key: 'people', class: 'people' }, h('span', 'muted', 'Reviewers'), res.reviewers.map(r => {
      const [cls, icon, word] = REVIEW[r.state] || REVIEW.requested;
      return h('span', { class: `person${r.name === res.me ? ' me' : ''}`, data: { tip: `${r.name}\n${word}` }, attrs: { 'aria-label': `${r.name}: ${word}` } },
        avatar(r.name), r.name, h('span', { class: `rs ${cls}`, html: icon }));
    })),
    res.bodyHtml ? h('div', { key: 'desc', class: 'desc', html: res.bodyHtml, onclick: newTab }) : h('div', { key: 'nodesc', class: 'desc' }, h('p', 'muted', 'No description.')),
    checks(v, res, words),
    h('h2', { key: 'talk' }, 'Conversation', h('span', 'muted', said ? `${said} ${said === 1 ? 'comment' : 'comments'}` : 'no comments yet')),
    // Where kijk may write, the conversation ends in your drafts, your summary and a box for more.
    (res.comments.length || writable(res)) && v.conv,
  ];
}

// drawConv draws the conversation in place: what is typed, busy or refused in it changes alone.
function drawConv(v) {
  const p = pull(v.root, v.n), drafts = threadOpts(p).drafts;
  patch(v.conv, p.res.comments.map(c => comment(c, drafts)), writable(p.res) && [summarySlot(p), commentFoot(p)]);
}

// checks are the CI of the head: a graph per workflow run or pipeline, the end of each failed
// job's log, and the checks of other apps.
function checks(v, res, words) {
  const c = res.checks;
  if (!c || (!c.groups.length && !c.other?.length)) return h('h2', { key: 'ci' }, words.ci, h('span', 'muted', 'none'));
  const jobs = [...c.groups.flatMap(g => g.jobs), ...(c.other || [])];
  const count = st => jobs.filter(j => j.state === st).length;
  const said = [['bad', 'failed'], ['cancel', 'cancelled'], ['run', 'running'], ['wait', 'waiting'], ['skip', 'skipped']]
    .filter(([st]) => count(st)).map(([st, word]) => `${count(st)} ${word}`);
  // A graph stays while its run is the same, with the lines drawEdges put in it.
  const was = v.graphs || new Map();
  v.graphs = new Map();
  const dag = g => {
    const k = JSON.stringify(g);
    v.graphs.set(k, was.get(k) || graph(g));
    return v.graphs.get(k);
  };
  const run = words.ci === 'Pipeline' ? 'pipeline' : 'run';
  const own = hooks.own?.(res.number);
  return [
    h('h2', { key: 'ci' }, words.ci, h('span', 'muted', said.length ? said.join(' · ') : 'all passed'),
      c.groups.some(g => g.graph) && h('span', { class: 'mode h2mode', attrs: { role: 'group', 'aria-label': 'Show as' } },
        [[false, 'Graph'], [true, 'List']].map(([list, text]) => h('button', {
          key: text, attrs: { 'aria-pressed': String(asList === list) },
          onclick: () => {
            asList = list;
            draw(v, v.res);
          },
        }, text)))),
    c.groups.map((g, i) => h('div', { key: `run:${i}`, class: 'dagwrap' },
      h('div', 'dagtitle', h('b', null, g.title), !!g.number && h('span', 'muted', `· ${run} #${g.number}`),
        g.url && h('a', {
          href: g.url, target: '_blank', rel: 'noopener', data: { tip: `Open on ${words.name}` }, attrs: { 'aria-label': `Open the ${run} on ${words.name}` },
        }, '↗')),
      g.graph && !asList ? dag(g) : jobList(g.jobs, 'jobs'))),
    c.groups.flatMap(g => g.jobs).filter(j => j.state === 'bad' && j.id).slice(0, 3).map(j => jobLog(v, res, j, own, words)),
    c.other?.length > 0 && [h('div', { key: 'other', class: 'dagtitle' }, h('b', null, 'Other checks')), jobList(c.other, 'other')],
  ];
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
  const edges = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
  edges.classList.add('edges');
  edges.setAttribute('aria-hidden', 'true');
  const dag = el('div', 'dag');
  patch(dag, edges, h('div', 'dagcols', cols.filter(Boolean).map(col => h('div', 'dagcol', col.map(j => h(j.url ? 'a' : 'span', {
    class: `job ${j.state}`, ...(j.url && { href: j.url, target: '_blank', rel: 'noopener' }),
    data: { name: j.name, needs: JSON.stringify((j.needs || []).filter(n => byName.has(n))), tip: jobTip(j) }, attrs: { 'aria-label': `${j.name}: ${STATE[j.state]}` },
  }, jobIcon(j.state), h('span', 'jn', j.name)))))));
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
    patch(svgEl, paths);
  }
}

function jobList(jobs, cls) {
  return h('ul', `ovlist ${cls}`, jobs.map(j => h('li', { title: jobTip(j) }, jobIcon(j.state), h('span', 'grow', j.name), (j.note || j.time) && h('span', 'muted', j.note || j.time),
    j.url && h('a', { href: j.url, target: '_blank', rel: 'noopener' }, 'Details ↗'))));
}

// jobLog shows the last lines of a failed job's log, and on your own branch hands them to the agent.
function jobLog(v, res, j, own, words) {
  // A log that could not be read, as while its run still went, is asked for again with a new answer.
  if (logs.get(j.id)?.error && logs.get(j.id).res !== res) logs.delete(j.id);
  const known = logs.get(j.id);
  if (!known) {
    logs.set(j.id, { loading: true });
    get('pr/log', { root: v.root, job: j.id })
      .then(r => logs.set(j.id, { lines: r.lines }))
      .catch(err => logs.set(j.id, { error: err.message, res }))
      .then(() => { if (isCurrent(v) && v.res) draw(v, v.res); });
  }
  return h('div', { key: `log:${j.id}`, class: 'joblog' },
    h('div', 'jlhead', jobIcon('bad'), h('b', null, j.name), h('span', 'muted', `failed${j.time ? ` after ${j.time}` : ''} · last lines of the log`),
      own && known?.lines?.length > 0 && h('button', {
        class: 'tool', html: `${ICON.copy}Copy for agent`,
        onclick: e => copyFailure(res, j, known.lines, e.currentTarget.closest('.joblog').querySelector('pre'), words),
      }),
      j.url && h('a', { class: 'grow-end', href: j.url, target: '_blank', rel: 'noopener' }, 'Full log ↗')),
    h('pre', { tabIndex: 0 }, known?.lines ? known.lines.join('\n') : known?.error || 'Reading the log…'));
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
  const words = forgeWords(b);
  column(v, h('h1', null, b.branch || 'This branch'),
    h('div', 'meta', avatar('you'), h('span', null, 'Your branch, against'), ref(b.base || 'main', 'lane-main'), b.ahead > 0 && h('span', null, `· ${b.ahead} not pushed`)),
    h('p', null, `There is no ${words.kind} for this branch yet. You can still review the branch changes and leave agent notes.`),
    b.create && b.forge && h('p', null, h('a', { href: b.create, target: '_blank', rel: 'noopener' }, `Create ${words.kind} on ${words.name} ↗`)));
}
