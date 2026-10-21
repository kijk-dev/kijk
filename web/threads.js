import { el } from './api.js';
import { age } from './prdata.js';
import { plural } from './boxes.js';

// Forge threads as boxes under their lines: open threads with a tinted header, resolved ones folded
// to one line, and outdated ones in a bar at the top of their file box.

const ICON = {
  msg: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M7.9 20A9 9 0 1 0 4 16.1L2 22Z"/></svg>',
  check: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M20 6 9 17l-5-5"/></svg>',
  chev: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="m9 18 6-6-6-6"/></svg>',
};

const opened = new Set(); // resolved threads the user opened
const shown = new Set(); // file boxes whose outdated threads show

function svg(markup, cls) {
  const s = el('span', cls);
  s.innerHTML = markup; // a fixed string
  return s;
}

// avatar is a name's first letter on a color from the name; kijk loads no pictures from the forge.
export function avatar(name) {
  const a = el('span', 'av', (name || '?')[0].toUpperCase());
  a.setAttribute('aria-hidden', 'true');
  a.style.setProperty('--hue', [...(name || '')].reduce((h, c) => (h * 31 + c.charCodeAt(0)) % 360, 7));
  return a;
}

function comment(c) {
  const box = el('div', 'c');
  const who = el('div', 'who');
  const when = el('span', 'muted', age(c.at));
  when.title = new Date(c.at).toLocaleString();
  who.append(avatar(c.author), el('b', null, c.author), when);
  const body = el('div', 'cbody');
  if (c.html) body.innerHTML = c.html; // rendered by kijk's server, which drops raw HTML
  else body.textContent = c.body;
  for (const a of body.querySelectorAll('a[href]')) {
    a.target = '_blank';
    a.rel = 'noopener';
  }
  box.append(who, body);
  return box;
}

// threadEl draws a thread. opts: {words, redraw(), line, foot}. foot is the reply strip, when the
// thread takes one; line is where it shows, when that is not its own line.
export function threadEl(t, opts) {
  const line = opts.line || t.line;
  if (t.resolved && !opened.has(t.id)) {
    const box = el('div', 'thr folded');
    box.dataset.id = t.id;
    box.tabIndex = 0;
    box.setAttribute('role', 'button');
    box.setAttribute('aria-expanded', 'false');
    const first = t.comments[0];
    const text = el('span');
    text.append(el('b', null, 'Resolved'), ` · ${first.author}: ${first.body.split('\n')[0]}`);
    const replies = t.comments.length - 1;
    if (replies) text.append(` · ${replies} ${replies === 1 ? 'reply' : 'replies'}`);
    box.append(svg(ICON.chev), svg(ICON.check, 'ok'), text);
    box.title = `Resolved thread on line ${line}. Click to show it.`;
    const open = () => {
      opened.add(t.id);
      opts.redraw();
    };
    box.onclick = open;
    box.onkeydown = e => {
      if (e.key === 'Enter' || e.key === ' ') {
        e.preventDefault();
        open();
      }
    };
    return box;
  }
  const box = el('div', `thr${t.resolved ? ' resolved' : ''}`);
  box.dataset.id = t.id;
  const head = el('div', 'thead');
  head.append(svg(ICON.msg), el('span', null, `${t.resolved ? 'Resolved thread' : t.outdated ? 'Outdated thread' : 'Open thread'} · line ${line}`),
    el('span', 'muted', `· ${plural(t.comments.length, 'comment')}`));
  if (t.resolved) {
    const fold = el('button', 'all', 'Fold');
    fold.onclick = () => {
      opened.delete(t.id);
      opts.redraw();
    };
    const end = el('span', 'end');
    end.append(fold);
    head.append(end);
  }
  box.append(head);
  if (t.outdated && t.was) box.append(el('span', 'was', t.was));
  for (const c of t.comments) box.append(comment(c));
  if (opts.foot) box.append(opts.foot);
  return box;
}

// outdatedBar lists the threads whose lines changed after they were written, at the top of a box.
export function outdatedBar(key, threads, opts) {
  const bar = el('div', 'fnote obar');
  const text = el('span');
  text.append(`${plural(threads.length, 'outdated thread')} `,
    el('span', 'muted', threads.length === 1 ? '· its lines changed after it was written' : '· their lines changed after they were written'));
  const show = el('button', 'all', shown.has(key) ? 'Hide' : 'Show');
  show.onclick = () => {
    if (shown.has(key)) shown.delete(key);
    else shown.add(key);
    opts.redraw();
  };
  bar.append(svg(ICON.msg), text, show);
  if (!shown.has(key)) return [bar];
  return [bar, ...threads.map(t => threadEl({ ...t, resolved: false, outdated: true }, { ...opts, line: t.line || t.origLine }))];
}

// placeForge puts threads under their rows: by new line, or by old line for a removed one. What
// the forge calls outdated, or what has no row here, goes to the bar.
export function placeForge(key, rows, threads, opts) {
  const keys = new Set();
  for (const r of rows) {
    if (r.k !== '-') keys.add(`n:${r.n}`);
    if (r.k !== '+') keys.add(`o:${r.o}`);
  }
  const under = new Map();
  const bar = [];
  for (const t of threads) {
    const k = `${t.old ? 'o' : 'n'}:${t.line}`;
    if (!t.outdated && t.line && keys.has(k)) under.set(k, [...(under.get(k) || []), threadEl(t, opts)]);
    else bar.push(t);
  }
  return { under, top: bar.length ? outdatedBar(key, bar, opts) : [] };
}

// threadCount is the speech bubble with the number of threads on a file, blue while one is open.
export function threadCount(threads, notes = 0) {
  const n = threads.length + notes;
  if (!n) return null;
  const open = threads.filter(t => !t.resolved && !t.outdated).length;
  const c = el('span', `tc${open || notes ? ' open' : ''}`);
  c.append(svg(ICON.msg), String(n));
  const parts = [];
  if (open) parts.push(`${open} open ${open === 1 ? 'thread' : 'threads'}`);
  const resolved = threads.filter(t => t.resolved).length;
  if (resolved) parts.push(`${resolved} resolved`);
  const old = threads.filter(t => t.outdated && !t.resolved).length;
  if (old) parts.push(`${old} outdated`);
  if (notes) parts.push(plural(notes, 'agent note'));
  c.dataset.tip = parts.join(', ');
  return c;
}
