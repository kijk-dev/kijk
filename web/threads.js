import { el, svg, h } from './api.js';
import { plural } from './boxes.js';

// Forge threads as boxes under their lines: open threads with a tinted header, resolved ones folded
// to one line, and outdated ones in a bar at the top of their file box. Your drafts show inside.

const ICON = {
  msg: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M7.9 20A9 9 0 1 0 4 16.1L2 22Z"/></svg>',
  check: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M20 6 9 17l-5-5"/></svg>',
  chev: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="m9 18 6-6-6-6"/></svg>',
};

const opened = new Set(); // resolved threads the user opened
const shown = new Set(); // file boxes whose outdated threads show

// age is how long ago an ISO time was, short: 5m, 3h, 2d, 1w.
export function age(iso) {
  const s = Math.max(0, (Date.now() - Date.parse(iso)) / 1000);
  if (!Number.isFinite(s)) return '';
  if (s < 3600) return `${Math.max(1, Math.floor(s / 60))}m`;
  if (s < 86400) return `${Math.floor(s / 3600)}h`;
  if (s < 7 * 86400) return `${Math.floor(s / 86400)}d`;
  return `${Math.floor(s / (7 * 86400))}w`;
}

// avatar is a name's first letter on a color from the name; kijk loads no pictures from the forge.
export function avatar(name) {
  const a = el('span', 'av', (name || '?')[0].toUpperCase());
  a.setAttribute('aria-hidden', 'true');
  a.style.setProperty('--hue', [...(name || '')].reduce((h, c) => (h * 31 + c.charCodeAt(0)) % 360, 7));
  return a;
}

// comment draws one comment. A draft of yours is a dashed box; drafts, when given, says who wrote
// it where and gives its text box and its buttons: {where(c), body(c), acts(c)}.
export function comment(c, drafts) {
  const where = c.draft && drafts?.where(c), acts = (c.draft && drafts?.acts(c)) || [];
  return h('div', { key: c.id, class: `c${c.draft ? ' mine' : ''}` },
    h('div', 'who', avatar(c.author), h('b', null, c.author), c.at && h('span', { class: 'muted', title: new Date(c.at).toLocaleString() }, age(c.at)),
      c.draft && h('span', 'chip draft', 'Draft'), where && h('span', 'muted', where), acts.length > 0 && h('span', 'end', acts)),
    (c.draft && drafts?.body(c)) || (c.html ? h('div', { class: 'cbody', html: c.html, onclick: newTab }) : h('div', 'cbody', c.body)));
}

// The html is rendered by kijk's server, which drops raw HTML; its links open in a new tab.
export const newTab = e => Object.assign(e.target.closest('a[href]') || {}, { target: '_blank', rel: 'noopener' });

// threadEl draws a thread. opts: {redraw(), line, foot, drafts, reply(t), resolve(t, on), busy(id), error(t)}.
// foot is Local mode's reply strip, reply(t) forge mode's; line is where it shows, if not its own.
export function threadEl(t, opts) {
  const line = opts.line || t.line;
  const key = `t:${t.id}`;
  if (t.resolved && !opened.has(t.id)) {
    const first = t.comments[0], replies = t.comments.length - 1;
    const open = () => {
      opened.add(t.id);
      opts.redraw();
    };
    const onkeydown = e => {
      if (e.key === 'Enter' || e.key === ' ') {
        e.preventDefault();
        open();
      }
    };
    return h('div', { key, class: 'thr folded', data: { id: t.id }, tabIndex: 0, attrs: { role: 'button', 'aria-expanded': 'false' }, title: `Resolved thread on line ${line}. Click to show it.`, onclick: open, onkeydown },
      h('span', { html: ICON.chev }), h('span', { class: 'ok', html: ICON.check }),
      h('span', null, h('b', null, 'Resolved'), ` · ${first.author}: ${first.body.split('\n')[0]}`, replies > 0 && ` · ${replies} ${replies === 1 ? 'reply' : 'replies'}`));
  }
  const fold = e => {
    e.stopPropagation(); // the thread is folded before the click gets to it, and would open again
    opened.delete(t.id);
    opts.redraw();
  };
  const end = t.resolved ? [t.canUnresolve && opts.resolve && resolveButton(t, 'Unresolve', false, opts), h('button', { class: 'all', onclick: fold }, 'Fold')]
    : [t.canResolve && opts.resolve && resolveButton(t, 'Resolve', true, opts)];
  return h('div', { key, class: `thr${t.resolved ? ' resolved' : ''}${t.draft ? ' draftonly' : ''}`, data: { id: t.id } },
    h('div', 'thead', t.draft ? h('span', null, `New comment · line ${line}`) : [h('span', { html: ICON.msg }),
      h('span', null, `${t.resolved ? 'Resolved thread' : t.outdated ? 'Outdated thread' : 'Open thread'} · line ${line}`),
      h('span', 'muted', `· ${plural(t.comments.length, 'comment')}`)], end.some(Boolean) && h('span', 'end', end)),
    opts.error?.(t) && h('p', 'error', opts.error(t)),
    t.outdated && t.was && h('span', 'was', t.was),
    t.comments.map(c => comment(c, opts.drafts)),
    opts.foot || (!t.draft && !t.resolved && t.canReply && opts.reply?.(t)));
}

function resolveButton(t, text, on, opts) {
  const onclick = () => opts.resolve(t, on).then(() => {
    if (on) opened.delete(t.id);
    else opened.add(t.id);
    opts.redraw();
  }, () => {}); // the thread shows why
  const tip = on ? 'Resolve this thread\nIt happens at once' : 'Open this thread again\nIt happens at once';
  return h('button', { key: 'resolve', class: 'tool', disabled: opts.busy(t.id), data: { tip }, onclick }, text);
}

// outdatedBar lists the threads whose lines changed after they were written, at the top of a box.
export function outdatedBar(key, threads, opts) {
  const on = shown.has(key);
  const onclick = () => {
    if (on) shown.delete(key);
    else shown.add(key);
    opts.redraw();
  };
  return [h('div', { key: 'obar', class: 'fnote obar' }, h('span', { html: ICON.msg }),
    h('span', null, `${plural(threads.length, 'outdated thread')} `,
      h('span', 'muted', threads.length === 1 ? '· its lines changed after it was written' : '· their lines changed after they were written')),
    h('button', { class: 'all', onclick }, on ? 'Hide' : 'Show')),
  on && threads.map(t => threadEl({ ...t, outdated: true }, { ...opts, line: t.line || t.origLine }))];
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

// threadsChanged are the paths whose threads differ between two answers about a pull request, so
// a box draws again also when its last thread went.
export function threadsChanged(before, after, paths) {
  const on = (r, path) => JSON.stringify((r?.threads || []).filter(t => t.path === path));
  return paths.filter(path => on(before, path) !== on(after, path));
}

// sent is the threads without your drafts, for Local mode, where only agent notes are written.
export function sent(threads) {
  return threads.filter(t => !t.draft).map(t => (t.comments.some(c => c.draft) ? { ...t, comments: t.comments.filter(c => !c.draft) } : t));
}

// threadCount is the speech bubble with the number of threads on a file, blue while one is open.
export function threadCount(threads, notes = 0) {
  const n = threads.length + notes;
  if (!n) return null;
  const open = threads.filter(t => !t.resolved && !t.outdated && !t.draft).length;
  const drafts = threads.reduce((k, t) => k + t.comments.filter(c => c.draft).length, 0);
  const c = el('span', `tc${open || notes || drafts ? ' open' : ''}`);
  c.append(svg(ICON.msg), String(n));
  const parts = [];
  if (open) parts.push(`${open} open ${open === 1 ? 'thread' : 'threads'}`);
  const resolved = threads.filter(t => t.resolved).length;
  if (resolved) parts.push(`${resolved} resolved`);
  const old = threads.filter(t => t.outdated && !t.resolved).length;
  if (old) parts.push(`${old} outdated`);
  if (drafts) parts.push(plural(drafts, 'draft'));
  if (notes) parts.push(plural(notes, 'agent note'));
  c.dataset.tip = parts.join(', ');
  return c;
}
