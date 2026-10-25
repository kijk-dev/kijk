import { el, h } from './api.js';
import { pull, write, submit, type, pick, allowed, redraw } from './prstore.js';
import { avatar } from './threads.js';
import { iconButton, plural } from './boxes.js';
import { forgeMark, forgeWords } from './pr.js';
import { mac } from './keys.js';
import { confirm } from './dialog.js';

// Writing a review: drafts on the forge, resolves, the review box and the check before posting.
// The writes, the text not sent and the refusals are in prstore.js; these draw them.

const ICON = {
  edit: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M21.17 6.81a1 1 0 0 0-3.99-3.99L3.84 16.17a2 2 0 0 0-.5.83l-1.32 4.35a.5.5 0 0 0 .62.62l4.35-1.32a2 2 0 0 0 .83-.5z"/></svg>',
  trash: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M3 6h18"/><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6"/><path d="M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/></svg>',
};

let hooks = {}; // announce(text), say(text), reviewed(root, n)

export function setupDrafts(p) {
  hooks = p;
}

// writable says whether kijk may write to the pull request of answer res.
export const writable = res => !!res?.write?.ok;

const ctrlEnter = e => e.key === 'Enter' && (mac ? e.metaKey : e.ctrlKey);
const told = err => hooks.announce?.(err.message);

// unsentKey names the text of a new comment or note on a line of view's file path, by the text of
// its line, so + on that line gives it back, also after the view closed or the line moved.
export const unsentKey = (view, path, l) => `${view}\n${path}\n${l.k === '-' ? '-' : '+'}${l.t}`;

// textBox is a text box with its buttons, its text kept under k of pull p: ok(text) runs on the
// button or Ctrl+Enter, cancel() on Cancel or Escape. box: {label, text, okText, placeholder, focus},
// where focus false keeps the focus where it is when the box is made.
export function textBox(p, k, box, ok, cancel) {
  const why = p.refused(k);
  const send = area => {
    const body = area.value.trim();
    if (body && !p.busy(k)) Promise.resolve(ok(body)).catch(told);
  };
  return h('div', { key: 'editbox', class: 'editbox' },
    h('textarea', {
      key: 'text', value: p.text(k)?.text ?? (box.text || ''), placeholder: box.placeholder || '', autofocus: box.focus ?? true, attrs: { 'aria-label': box.label },
      oninput: e => type(p.root, p.n, k, e.currentTarget.value, box.text),
      onkeydown: e => {
        if (ctrlEnter(e)) {
          e.preventDefault();
          send(e.currentTarget);
        } else if (e.key === 'Escape') {
          e.preventDefault();
          e.stopPropagation();
          cancel();
        }
      },
    }),
    h('p', { class: 'error', hidden: !why }, why),
    h('div', 'acts', h('span', 'muted', h('kbd', null, mac ? '⌘' : 'Ctrl'), ' ', h('kbd', null, 'Enter'), box.okText === 'Save' ? ' saves it' : ' adds it'),
      h('button', { class: 'tool', disabled: p.busy(k), onclick: cancel }, 'Cancel'),
      h('button', { class: 'tool', disabled: p.busy(k), onclick: e => send(e.currentTarget.closest('.editbox').querySelector('textarea')) }, box.okText)));
}

// lineEditor is the box under row l of file f where a new comment is written: ed.draw(p) describes
// it, and ed.move(row, at) puts it on a row of head at. done() closes it; toForge(row), when set,
// gives the forge diff's row of a row's line. ed.at is the head the user wrote on, not the one now.
export function lineEditor(p, f, l, done, toForge) {
  const ed = { text: l.t };
  const k = unsentKey('comment', f.path, l);
  const add = body => write(p.root, p.n, 'comment', k, (async () => {
    if (!ed.row) throw new Error('This line is not in the diff now. Copy your text and add it on another line.');
    const r = (await toForge?.(ed.row)) || ed.row;
    return { at: ed.at, path: f.path, orig: f.orig || '', line: ed.line, old: ed.old, oldLine: r.k === ' ' ? r.o : 0, body };
  })()).then(() => {
    hooks.announce?.(`Comment added to your review on line ${ed.line}`);
    done();
  });
  ed.draw = p => h('div', { key: 'editor', class: 'thr draftonly editing' },
    h('div', 'thead', h('span', null, `New comment · line ${ed.line}`)),
    h('div', 'c', h('div', 'who', avatar(p.res.me || 'you'), h('b', null, p.res.me || 'you')),
      textBox(p, k, { label: `Comment on line ${ed.line}`, okText: 'Add to review' }, add, done)));
  ed.move = (row, at) => {
    ed.at = at;
    ed.row = row;
    if (!row) return;
    ed.old = row.k === '-';
    ed.line = ed.old ? row.o : row.n;
    ed.key = `${ed.old ? 'o' : 'n'}:${ed.line}`;
  };
  ed.move(l, p.res.at);
  return ed;
}

// moveEditor puts an editor written on another head on the row of rows with its line's text,
// on the same side and nearest to its line; without one it has no row.
export function moveEditor(ed, rows, at) {
  if (ed.at === at) return;
  const same = rows.filter(r => r.t === ed.text && (r.k === '-') === ed.old);
  const line = r => (r.k === '-' ? r.o : r.n);
  same.sort((a, b) => Math.abs(line(a) - ed.line) - Math.abs(line(b) - ed.line));
  ed.move(same[0] || null, at);
}

// ----- What a thread box needs to write -----

const editors = new Set(); // the drafts open in a text box
let opening = ''; // the draft whose text box opened last: its first draw takes the focus, not a later view

// threadOpts are the parts of a thread box of pull p that write: who wrote each draft where, its
// text box and buttons, the reply strip, resolve, and what is busy or was refused.
export function threadOpts(p) {
  const can = writable(p.res);
  const close = id => {
    editors.delete(id);
    type(p.root, p.n, `edit\n${id}`, null);
    redraw(p.root);
  };
  const drafts = {
    where: c => (p.mine(c) ? '' : `· written on ${forgeWords(p.res).name}`),
    body: c => {
      if (!editors.has(c.id)) return null;
      const focus = opening === c.id;
      if (focus) opening = '';
      return textBox(p, `edit\n${c.id}`, { label: 'Your draft', text: c.body, okText: 'Save', focus }, body =>
        write(p.root, p.n, 'edit', `edit\n${c.id}`, { id: c.id, body }).then(() => {
          hooks.announce?.('Draft saved');
          close(c.id);
        }), () => close(c.id));
    },
    acts: c => (!can || editors.has(c.id) ? [] : [
      iconButton(ICON.edit, 'Edit', () => {
        editors.add(c.id);
        opening = c.id;
        redraw(p.root);
      }),
      iconButton(ICON.trash, 'Delete', () => write(p.root, p.n, 'delete', c.id, { id: c.id }).then(() => hooks.announce?.('Draft deleted'), told)),
    ]),
  };
  // A refusal shows in the thread, under its header.
  const error = t => [t.id, ...t.comments.map(c => c.id)].map(p.refused).find(Boolean) || '';
  if (!can) return { drafts, error };
  return {
    drafts,
    error,
    busy: p.busy,
    reply: t => replyStrip(p, t),
    resolve: (t, on) => write(p.root, p.n, 'resolve', t.id, { thread: t.id, resolved: on }).then(() => hooks.announce?.(on ? 'Thread resolved' : 'Thread open again'), err => {
      told(err);
      throw err;
    }),
  };
}

// replyStrip is an open thread's reply box in forge mode: Enter adds the reply to your review.
function replyStrip(p, t) {
  const k = `reply\n${t.id}`;
  return h('div', { key: 'foot', class: 'foot' }, avatar(p.res.me || 'you'),
    h('input', {
      key: 'reply', placeholder: 'Reply…', value: p.text(k)?.text || '', readOnly: p.busy(k), attrs: { 'aria-label': `Reply to ${t.comments[0]?.author}` },
      oninput: e => type(p.root, p.n, k, e.currentTarget.value),
      onkeydown: e => {
        const body = e.currentTarget.value.trim();
        if (e.key === 'Enter' && !e.shiftKey) {
          e.preventDefault();
          if (body && !p.busy(k)) write(p.root, p.n, 'comment', k, { thread: t.id, body }).then(() => hooks.announce?.('Reply added to your review'), told);
        } else if (e.key === 'Escape') {
          e.preventDefault();
          e.stopPropagation();
          type(p.root, p.n, k, '');
          e.currentTarget.blur();
        }
      },
    }),
    h('span', 'error', p.refused(k)));
}

// ----- The review box; the verdict is kept in the browser, the summary is a draft on the forge -----

// tally sorts your drafts: comments on lines, replies with whom and where they answer, and
// comments not on a line; web(list) counts those written somewhere else.
function tally(p) {
  const res = p.res;
  const lines = [];
  const replies = [];
  const conv = [];
  const short = path => path.split('/').pop();
  for (const t of res?.threads || []) {
    for (const c of t.comments) {
      if (!c.draft) continue;
      if (t.draft) lines.push(c);
      else replies.push({ c, to: `${t.comments[0].author} on ${short(t.path)}:${t.line || t.origLine}` });
    }
  }
  for (const c of res?.comments || []) {
    if (!c.draft) continue;
    if (c.replyTo) replies.push({ c, to: `${c.replyTo} on the ${forgeWords(res).kind}` });
    else conv.push(c);
  }
  const web = list => list.filter(d => !p.mine(d.c || d)).length;
  return { lines, replies, conv, web, all: lines.length + replies.length + conv.length };
}

const VERDICTS = [['comment', 'Comment'], ['approve', 'Approve'], ['changes', 'Request changes']];

const nothing = p => p.verdict === 'comment' && !tally(p).all && !p.res.summary?.trim();

// saveSummary sets the summary on the forge: GitHub's pending review body, GitLab's draft note.
const saveSummary = (p, body) => write(p.root, p.n, 'summary', 'summary', { body }).then(() => hooks.announce?.('Summary saved'), err => {
  told(err);
  throw err;
});

// reviewBox is the box at the bottom of the sidebar: your drafts, the verdict, the summary and
// Submit review…; or why kijk cannot write here.
export function reviewBox(p) {
  const res = p.res;
  if (!writable(res)) {
    if (!res?.write?.note || ['merged', 'closed'].includes(res.state)) return null; // the sidebar says it already
    return h('div', { key: 'review', class: 'reviewbox' }, h('p', 'hint', res.write.note));
  }
  const t = tally(p), web = t.web([...t.lines, ...t.replies, ...t.conv]);
  const forge = res.summary || '', value = p.text('summary')?.text ?? forge;
  // The summary goes to the forge when it is saved, not on every key.
  const dirty = area => area.value.trim() !== forge.trim();
  const off = nothing(p) && !value.trim();
  return h('div', { key: 'review', class: 'reviewbox' },
    h('div', 'head', h('b', null, 'Your review'), h('span', 'muted', t.all ? `${plural(t.all, 'draft')}${web ? ` · ${web} from ${forgeWords(res).name}` : ''}` : 'no drafts yet')),
    h('div', { class: 'mode', attrs: { role: 'group', 'aria-label': 'Verdict' } }, VERDICTS.map(([v, text]) => h('button', {
      key: v, disabled: !allowed(res, v), attrs: { 'aria-pressed': String(v === p.verdict) }, data: allowed(res, v) ? {} : { tip: res.write.why },
      onclick: () => pick(p.root, p.n, v),
    }, text))),
    h('div', { class: 'why', hidden: !res.write.why }, res.write.why || ''),
    h('textarea', {
      key: 'summary', rows: 2, placeholder: 'Summary (optional)', value, attrs: { 'aria-label': 'Summary' },
      oninput: e => type(p.root, p.n, 'summary', e.currentTarget.value, forge),
      onkeydown: e => {
        if (!ctrlEnter(e)) return;
        e.preventDefault();
        if (dirty(e.currentTarget)) saveSummary(p, e.currentTarget.value).catch(() => {});
      },
    }),
    h('div', { class: 'saved', hidden: value.trim() === forge.trim() }, `Not saved · ${mac ? '⌘' : 'Ctrl'} Enter saves it`),
    h('div', 'error', p.refused('summary')),
    h('button', {
      class: 'primary', disabled: off, title: off ? 'Nothing to post yet' : '',
      onclick: e => {
        const area = e.currentTarget.parentNode.querySelector('textarea');
        if (!dirty(area)) return openCheck(p.root, p.n);
        saveSummary(p, area.value).then(() => openCheck(p.root, p.n), () => {});
      },
    }, 'Submit review…'));
}

// summarySlot is your summary at the end of the Overview's conversation, as a draft.
export function summarySlot(p) {
  const text = (p.res.summary || '').trim();
  return h('div', { key: 'summary', class: 'c mine summary', hidden: !text },
    h('div', 'who', avatar(p.res.me || 'you'), h('b', null, p.res.me || 'you'), h('span', 'chip draft', 'Draft'), h('span', 'muted', '· sent with your review')),
    h('div', 'cbody', text));
}

// commentFoot is the box "Comment on the pull request…" under the conversation: its text goes into
// the summary, since GitHub holds a comment not on a line only as the body of a review.
export function commentFoot(p) {
  const w = forgeWords(p.res);
  const take = input => {
    const text = input.value.trim(), summary = (p.res.summary || '').trim();
    if (!text || p.busy('foot')) return;
    write(p.root, p.n, 'summary', 'foot', { body: summary ? `${summary}\n\n${text}` : text }, text)
      .then(() => hooks.announce?.('Added to the summary of your review'), told);
  };
  return h('div', { key: 'foot', class: 'foot' }, avatar(p.res.me || 'you'),
    h('input', {
      key: 'text', value: p.text('foot')?.text || '', placeholder: `Comment on the ${w.kind}…`, attrs: { 'aria-label': `Comment on the ${w.kind}` },
      oninput: e => type(p.root, p.n, 'foot', e.currentTarget.value),
      onkeydown: e => {
        if (e.key !== 'Enter') return;
        e.preventDefault();
        take(e.currentTarget);
      },
    }),
    h('span', 'error', p.refused('foot')),
    h('button', {
      class: 'tool', disabled: p.busy('foot'), data: { tip: 'Add it to the summary of your review\nSent with Submit review' },
      onclick: e => take(e.currentTarget.parentNode.querySelector('input')),
    }, 'Add to review'));
}

// ----- The check before anything is posted -----

function checkRows(p) {
  const res = p.res, w = forgeWords(res);
  const t = tally(p);
  const rows = [];
  const said = { comment: ['', 'Comment'], approve: ['ok', '✓ Approve'], changes: ['bad', '✗ Request changes'] }[p.verdict];
  rows.push(['Verdict', el('span', `v verdict ${said[0]}`, said[1])]);
  const where = [];
  if (t.lines.length) where.push(t.lines.length === 1 ? '1 on a line' : `${t.lines.length} on lines`);
  if (t.conv.length) where.push(`${t.conv.length} on the ${w.kind}`);
  // Each row says how many of its drafts were written on the website.
  const elsewhere = (value, list) => {
    const k = t.web(list);
    if (k) value.append(' ', el('span', 'muted', `· ${k} written on ${w.name}`));
    return value;
  };
  if (where.length) rows.push(['Comments', elsewhere(el('span', 'v', where.join(' · ')), [...t.lines, ...t.conv])]);
  if (t.replies.length) {
    const k = t.replies.length;
    rows.push(['Replies', elsewhere(el('span', 'v', k > 2 ? String(k) : `${k}, to ${t.replies.map(r => r.to).join(' and ')}`), t.replies)]);
  }
  if (res.summary?.trim()) rows.push(['Summary', res.summary.trim().split('\n')[0]]);
  return rows;
}

function openCheck(root, n) {
  const p = pull(root, n), res = p.res;
  if (nothing(p)) return;
  const w = forgeWords(res);
  confirm({
    mark: forgeMark(res.forge?.kind || 'github'),
    title: `Post your review on ${w.num}${n}?`,
    sub: `${res.title} · ${res.author}`,
    rows: checkRows(p),
    note: `Everyone on the ${w.kind} sees it at once.`,
    busy: 'Posting…',
    ok: 'Post review',
    run: () => submit(root, n).catch(err => { told(err); throw err; }),
  }).then(done => {
    if (!done) return;
    const said = `Posted your review on ${w.num}${n}`;
    hooks.say?.(said);
    hooks.announce?.(said);
    fetch('/api/count?name=review', { method: 'POST' }).catch(() => {}); // the local kijk counts, also for a remote folder
    hooks.reviewed?.(root, n);
  });
}
