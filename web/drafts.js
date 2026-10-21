import { el, post, storeKey } from './api.js';
import { loadView } from './prdata.js';
import { avatar } from './threads.js';
import { iconButton, plural } from './boxes.js';
import { FORGE, forgeMark } from './pr.js';
import { mac } from './keys.js';
import { setReviewed } from './viewed.js';

// Writing a review: drafts on the forge, resolves, the review box and the check before posting.
// Every write asks for the pull request again, so what shows is the forge's state.

const ICON = {
  edit: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M21.17 6.81a1 1 0 0 0-3.99-3.99L3.84 16.17a2 2 0 0 0-.5.83l-1.32 4.35a.5.5 0 0 0 .62.62l4.35-1.32a2 2 0 0 0 .83-.5z"/></svg>',
  trash: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M3 6h18"/><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6"/><path d="M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/></svg>',
};

let hooks = {}; // announce(text), say(text), reviewed(root, n)

export function setupDrafts(h) {
  hooks = h;
}

export const words = res => FORGE[res?.forge?.kind] || FORGE.github;

// writable says whether kijk may write to the pull request of answer res.
export const writable = res => !!res?.write?.ok;

// ----- The ids of the drafts kijk wrote, per folder and pull request -----

const idsKey = (root, n) => `kijk.drafts:${storeKey(root)}:${n}`;

function ids(root, n) {
  try { return new Set(JSON.parse(localStorage.getItem(idsKey(root, n))) || []); } catch { return new Set(); }
}

function remember(root, n, id) {
  if (!id) return;
  const s = ids(root, n);
  s.add(id);
  try { localStorage.setItem(idsKey(root, n), JSON.stringify([...s].slice(-500))); } catch { /* storage blocked */ }
}

export const wroteHere = (root, n, c) => ids(root, n).has(c.id);

// ----- Sending -----

async function send(root, n, route, body) {
  const res = await post(`pr/${route}`, { root, n, ...body });
  await loadView(root, n).catch(() => {});
  return res;
}

// shown sends a write that needs the diff on screen: on 428 (no diff kept, as after a restart) it
// goes once more only when the head is still at, the one on screen; 409 means the head moved.
async function shown(root, n, at, route, body) {
  try {
    return await post(route, { root, n, ...body });
  } catch (err) {
    if (err.status !== 409 && err.status !== 428) throw err;
    const fresh = await loadView(root, n); // shows the new head
    if (err.status === 409) throw err;
    if (fresh.at !== at) throw new Error(`${words(fresh).num}${n} has new commits. kijk shows them now; look again first.`);
    return post(route, { root, n, ...body });
  }
}

async function addComment(root, n, at, c) {
  const res = await shown(root, n, at, 'pr/comment', c);
  remember(root, n, res.id);
  await loadView(root, n).catch(() => {});
}

// ----- Text boxes -----

const ctrlEnter = e => e.key === 'Enter' && (mac ? e.metaKey : e.ctrlKey);

// textBox is a text box with its buttons: ok(text) runs on the button or Ctrl+Enter and may throw
// to show why, cancel() on Cancel or Escape. The text stays when ok fails.
function textBox(label, text, okText, ok, cancel) {
  const wrap = el('div', 'editbox');
  const area = el('textarea');
  area.setAttribute('aria-label', label);
  area.value = text;
  const error = el('p', 'error');
  error.hidden = true;
  const acts = el('div', 'acts');
  const hint = el('span', 'muted');
  hint.append(el('kbd', null, mac ? '⌘' : 'Ctrl'), ' ', el('kbd', null, 'Enter'), okText === 'Save' ? ' saves it' : ' adds it');
  const no = el('button', 'tool', 'Cancel');
  const yes = el('button', 'tool', okText);
  acts.append(hint, no, yes);
  wrap.append(area, error, acts);
  const submit = async () => {
    const body = area.value.trim();
    if (!body || yes.disabled) return;
    yes.disabled = no.disabled = true;
    error.hidden = true;
    try {
      await ok(body);
    } catch (err) {
      error.textContent = err.message;
      error.hidden = false;
      hooks.announce?.(err.message);
    } finally {
      yes.disabled = no.disabled = false;
    }
  };
  yes.onclick = submit;
  no.onclick = cancel;
  area.onkeydown = e => {
    if (ctrlEnter(e)) {
      e.preventDefault();
      submit();
    } else if (e.key === 'Escape') {
      e.preventDefault();
      e.stopPropagation();
      cancel();
    }
  };
  wrap.focus = () => requestAnimationFrame(() => area.isConnected && area.focus({ preventScroll: true }));
  return wrap;
}

// unsent keeps the text of a new comment closed with Escape, by file and line, until it is added.
const unsent = new Map();

// lineEditor is the box under row l of file f where a new comment is written. done() closes it.
// The editor knows the head its row is of; move(row, at) puts it on a row of another head. With
// toForge(row), the row is of another diff, and toForge gives the forge diff's row of its line.
export function lineEditor(root, n, res, f, l, done) {
  const ed = { text: l.t };
  const kept = () => `${root}\n${n}\n${f.path}\n${ed.key}`;
  const box = el('div', 'thr draftonly editing');
  const head = el('div', 'thead');
  const title = el('span');
  head.append(title);
  const c = el('div', 'c');
  const who = el('div', 'who');
  who.append(avatar(res.me || 'you'), el('b', null, res.me || 'you'));
  const text = textBox('Comment', '', 'Add to review', async body => {
    if (!ed.row) throw new Error('This line is not in the diff now. Copy your text and add it on another line.');
    const r = (await ed.toForge?.(ed.row)) || ed.row;
    await addComment(root, n, ed.at, { path: f.path, orig: f.orig || '', line: ed.line, old: ed.old, oldLine: r.k === ' ' ? r.o : 0, body });
    unsent.delete(kept());
    hooks.announce?.(`Comment added to your review on line ${ed.line}`);
    done();
  }, () => {
    ed.keep();
    done();
  });
  // keep holds the text not added yet, so + on the same line gives it back.
  ed.keep = () => {
    const left = text.querySelector('textarea').value;
    if (left.trim()) unsent.set(kept(), left);
    else unsent.delete(kept());
  };
  c.append(who, text);
  box.append(head, c);
  ed.el = box;
  ed.move = (row, at) => {
    ed.at = at;
    ed.row = row;
    if (!row) return;
    ed.old = row.k === '-';
    ed.line = ed.old ? row.o : row.n;
    ed.key = `${ed.old ? 'o' : 'n'}:${ed.line}`;
    title.textContent = `New comment · line ${ed.line}`;
    text.querySelector('textarea').setAttribute('aria-label', `Comment on line ${ed.line}`);
  };
  ed.move(l, res.at);
  text.querySelector('textarea').value = unsent.get(kept()) || '';
  text.focus();
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

const editors = new Map(); // a draft being edited: its text box
const failed = new Map(); // a thread or draft whose write the forge refused: why
const strips = new Map(); // the reply strips of forge mode, kept across redraws so what is typed stays

// threadOpts are the parts of a thread box that write: who wrote each draft where, its text box and
// buttons, the reply strip, and resolve. redraw() draws the threads again.
export function threadOpts(root, n, res, redraw) {
  const can = writable(res);
  const key = id => `${root}\n${n}\n${id}`;
  const drafts = {
    where: c => (wroteHere(root, n, c) ? '' : `· written on ${words(res).name}`),
    body: c => editors.get(key(c.id)) || null,
    acts: c => (!can || editors.has(key(c.id)) ? [] : [
      iconButton(ICON.edit, 'Edit', () => {
        const close = () => {
          editors.delete(key(c.id));
          redraw();
        };
        const box = textBox('Your draft', c.body, 'Save', async body => {
          await send(root, n, 'comment/edit', { id: c.id, body });
          hooks.announce?.('Draft saved');
          close();
        }, close);
        editors.set(key(c.id), box);
        redraw();
        box.focus();
      }),
      iconButton(ICON.trash, 'Delete', () => tried(key(c.id), redraw, async () => {
        await send(root, n, 'comment/delete', { id: c.id });
        hooks.announce?.('Draft deleted');
      }).catch(() => {})), // the thread shows why
    ]),
  };
  // A refusal shows in the thread, under its header.
  const error = t => [t.id, ...t.comments.map(c => c.id)].map(id => failed.get(key(id))).find(Boolean) || '';
  if (!can) return { drafts, error };
  return {
    drafts,
    error,
    reply: t => replyStrip(root, n, res, t),
    resolve: (t, on) => tried(key(t.id), redraw, async () => {
      await send(root, n, 'resolve', { thread: t.id, resolved: on });
      hooks.announce?.(on ? 'Thread resolved' : 'Thread open again');
    }),
  };
}

// tried runs a write; when the forge refuses it, its reason is kept under k and drawn.
async function tried(k, redraw, write) {
  const had = failed.delete(k);
  try {
    await write();
    if (had) redraw();
  } catch (err) {
    failed.set(k, err.message);
    hooks.announce?.(err.message);
    redraw();
    throw err;
  }
}

// replyStrip is an open thread's reply box in forge mode: Enter adds the reply to your review.
function replyStrip(root, n, res, t) {
  const k = `${root}\n${n}\n${t.id}`;
  let s = strips.get(k);
  if (!s) {
    const foot = el('div', 'foot');
    const input = el('input');
    input.placeholder = 'Reply…';
    const error = el('span', 'error');
    foot.append(avatar(res.me || 'you'), input, error);
    s = { foot, input, error };
    strips.set(k, s);
    input.onkeydown = async e => {
      if (e.key === 'Enter' && !e.shiftKey) {
        e.preventDefault();
        const body = input.value.trim();
        if (!body || input.readOnly) return;
        input.readOnly = true;
        error.textContent = '';
        try {
          await addComment(root, n, res.at, { thread: t.id, body });
          if (input.value.trim() === body) input.value = '';
          hooks.announce?.('Reply added to your review');
        } catch (err) {
          error.textContent = err.message;
          hooks.announce?.(err.message);
        } finally {
          input.readOnly = false;
        }
      } else if (e.key === 'Escape') {
        e.preventDefault();
        e.stopPropagation();
        input.value = '';
        input.blur();
      }
    };
  }
  s.input.setAttribute('aria-label', `Reply to ${t.comments[0]?.author}`);
  return s.foot;
}

// ----- The verdict, kept in the browser; the summary is a draft on the forge like the rest -----

const reviewKey = (root, n) => `kijk.review:${storeKey(root)}:${n}`;

function review(root, n) {
  const none = { verdict: 'comment' };
  try { return { ...none, ...JSON.parse(localStorage.getItem(reviewKey(root, n))) }; } catch { return none; }
}

// The review box and the Overview's summary that show, per folder and pull request, so a change in
// one shows in the other without drawing either again.
const live = new Map();

function liveParts(root, n) {
  const k = `${root}\n${n}`;
  if (!live.has(k)) live.set(k, {});
  return live.get(k);
}

function setReview(root, n, patch) {
  const r = { ...review(root, n), ...patch };
  try {
    if (r.verdict === 'comment') localStorage.removeItem(reviewKey(root, n));
    else localStorage.setItem(reviewKey(root, n), JSON.stringify({ verdict: r.verdict }));
  } catch { /* storage blocked */ }
  for (const part of Object.values(liveParts(root, n))) if (part.isConnected) part.sync();
}

// saveSummary sets the summary on the forge: GitHub's pending review body, GitLab's draft note.
async function saveSummary(root, n, at, body) {
  await shown(root, n, at, 'pr/summary', { body });
  await loadView(root, n).catch(() => {});
  hooks.announce?.('Summary saved');
}

// tally sorts your drafts: comments on lines, replies with whom and where they answer, and
// comments not on a line; web(list) counts those written somewhere else.
function tally(root, n, res) {
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
    if (c.replyTo) replies.push({ c, to: `${c.replyTo} on the ${words(res).kind}` });
    else conv.push(c);
  }
  const web = list => list.filter(d => !wroteHere(root, n, d.c || d)).length;
  return { lines, replies, conv, web, all: lines.length + replies.length + conv.length };
}

const VERDICTS = [['comment', 'Comment'], ['approve', 'Approve'], ['changes', 'Request changes']];
const allowed = (res, v) => v === 'comment' || (v === 'approve' ? res.write.approve : res.write.changes);

// verdict is the verdict picked, or Comment when the one picked is not allowed here.
function verdict(root, n, res) {
  const v = review(root, n).verdict;
  return allowed(res, v) ? v : 'comment';
}

const nothing = (root, n, res) => verdict(root, n, res) === 'comment' && !tally(root, n, res).all && !res.summary?.trim();

// reviewBox is the box at the bottom of the sidebar: your drafts, the verdict, the summary and
// Submit review…; or why kijk cannot write here. The box stays one element, so what is typed stays.
export function reviewBox(root, n, res) {
  if (!writable(res)) {
    if (!res?.write?.note || ['merged', 'closed'].includes(res.state)) return null; // the sidebar says it already
    const box = el('div', 'reviewbox');
    box.append(el('p', 'hint', res.write.note));
    return box;
  }
  const parts = liveParts(root, n);
  let box = parts.box;
  if (!box) {
    box = el('div', 'reviewbox');
    const head = el('div', 'head');
    const count = el('span', 'muted');
    head.append(el('b', null, 'Your review'), count);
    const mode = el('div', 'mode');
    mode.setAttribute('role', 'group');
    mode.setAttribute('aria-label', 'Verdict');
    const why = el('div', 'why');
    const area = el('textarea');
    area.rows = 2;
    area.placeholder = 'Summary (optional)';
    area.setAttribute('aria-label', 'Summary');
    // The summary goes to the forge when it is saved, not on every key.
    const saved = el('div', 'saved', `Not saved · ${mac ? '⌘' : 'Ctrl'} Enter saves it`);
    const error = el('div', 'error');
    let known = ''; // the summary the forge had when the box last showed it
    const dirty = () => area.value.trim() !== (box.res.summary || '').trim();
    const save = async () => {
      error.textContent = '';
      try {
        await saveSummary(root, n, box.res.at, area.value);
      } catch (err) {
        error.textContent = err.message;
        hooks.announce?.(err.message);
        throw err;
      }
    };
    area.oninput = () => box.sync();
    area.onkeydown = e => {
      if (ctrlEnter(e)) {
        e.preventDefault();
        if (dirty()) save().catch(() => {});
      }
    };
    const submit = el('button', 'primary', 'Submit review…');
    box.append(head, mode, why, area, saved, error, submit);
    box.sync = () => {
      const r = box.res;
      const t = tally(root, n, r);
      const web = t.web([...t.lines, ...t.replies, ...t.conv]);
      count.textContent = t.all ? `${plural(t.all, 'draft')}${web ? ` · ${web} from ${words(r).name}` : ''}` : 'no drafts yet';
      const picked = verdict(root, n, r);
      mode.replaceChildren(...VERDICTS.map(([v, text]) => {
        const b = el('button', null, text);
        b.setAttribute('aria-pressed', v === picked);
        b.disabled = !allowed(r, v);
        if (b.disabled) b.dataset.tip = r.write.why;
        b.onclick = () => setReview(root, n, { verdict: v });
        return b;
      }));
      why.textContent = r.write.why || '';
      why.hidden = !r.write.why;
      // A new summary from the forge shows. Text of the box's own not saved yet stays after it, so
      // Submit, which saves it first, cannot drop what was added in the Overview or on the website.
      const forge = r.summary || '';
      const mine = area.value;
      if (forge !== known) {
        if (mine.trim() === known.trim() || mine.trim() === forge.trim()) area.value = forge;
        else if (known && mine.startsWith(known)) area.value = forge + mine.slice(known.length);
        else if (forge.trim()) area.value = `${forge}\n\n${mine}`;
      }
      known = forge;
      saved.hidden = !dirty();
      submit.disabled = nothing(root, n, r) && !area.value.trim();
      submit.title = submit.disabled ? 'Nothing to post yet' : '';
    };
    submit.onclick = async () => {
      if (dirty()) {
        try { await save(); } catch { return; }
      }
      openCheck(root, n, box.res);
    };
    parts.box = box;
  }
  box.res = res;
  box.sync();
  return box;
}

// summarySlot is your summary at the end of the Overview's conversation, as a draft.
export function summarySlot(root, n, res) {
  const parts = liveParts(root, n);
  const slot = el('div', 'c mine summary');
  slot.res = res;
  slot.sync = () => {
    const text = (slot.res.summary || '').trim();
    slot.hidden = !text;
    const who = el('div', 'who');
    who.append(avatar(res.me || 'you'), el('b', null, res.me || 'you'), el('span', 'chip draft', 'Draft'), el('span', 'muted', '· sent with your review'));
    const body = el('div', 'cbody', text);
    slot.replaceChildren(who, body);
  };
  slot.sync();
  parts.slot = slot;
  return slot;
}

// commentFoot is the box "Comment on the pull request…" under the conversation: its text goes into
// the summary, since GitHub holds a comment not on a line only as the body of a review.
export function commentFoot(root, n, res) {
  const parts = liveParts(root, n);
  if (parts.foot) {
    parts.foot.res = res;
    return parts.foot; // kept, so what is typed stays when the Overview draws again
  }
  const w = words(res);
  const foot = el('div', 'foot');
  parts.foot = foot;
  foot.res = res;
  foot.sync = () => {};
  const input = el('input');
  input.placeholder = `Comment on the ${w.kind}…`;
  input.setAttribute('aria-label', `Comment on the ${w.kind}`);
  const add = el('button', 'tool', 'Add to review');
  add.dataset.tip = 'Add it to the summary of your review\nSent with Submit review';
  const error = el('span', 'error');
  const take = async () => {
    const text = input.value.trim();
    if (!text || add.disabled) return;
    const summary = (foot.res.summary || '').trim();
    add.disabled = true;
    error.textContent = '';
    try {
      await saveSummary(root, n, foot.res.at, summary ? `${summary}\n\n${text}` : text);
      if (input.value.trim() === text) input.value = '';
      hooks.announce?.('Added to the summary of your review');
    } catch (err) {
      error.textContent = err.message;
      hooks.announce?.(err.message);
    } finally {
      add.disabled = false;
    }
  };
  add.onclick = take;
  input.onkeydown = e => {
    if (e.key === 'Enter') {
      e.preventDefault();
      take();
    }
  };
  foot.append(avatar(res.me || 'you'), input, error, add);
  return foot;
}

// ----- The check before anything is posted -----

function checkRows(root, n, res) {
  const w = words(res);
  const t = tally(root, n, res);
  const rows = [];
  const v = verdict(root, n, res);
  const said = { comment: ['', 'Comment'], approve: ['ok', '✓ Approve'], changes: ['bad', '✗ Request changes'] }[v];
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
  if (res.summary?.trim()) rows.push(['Summary', el('span', 'v', res.summary.trim().split('\n')[0])]);
  return rows.map(([key, value]) => {
    const li = el('li');
    li.append(el('span', 'k', key), value);
    return li;
  });
}

function openCheck(root, n, res) {
  if (document.querySelector('.confirm') || nothing(root, n, res)) return;
  const w = words(res);
  const back = document.activeElement;
  const shade = el('div', 'backdrop');
  const box = el('div', 'confirm');
  box.setAttribute('role', 'dialog');
  box.setAttribute('aria-modal', 'true');
  box.setAttribute('aria-labelledby', 'confirm-title');
  const h = el('h2');
  h.id = 'confirm-title';
  h.append(forgeMark(res.forge?.kind || 'github'), `Post your review on ${w.num}${n}?`);
  const list = el('ul');
  list.append(...checkRows(root, n, res));
  const error = el('p', 'error');
  error.hidden = true;
  const acts = el('div', 'acts');
  const note = el('span', 'muted', `Everyone on the ${w.kind} sees it at once.`);
  const cancel = el('button', 'tool', 'Cancel');
  const ok = el('button', 'primary', 'Post review');
  acts.append(note, cancel, ok);
  box.append(h, el('p', 'sub', `${res.title} · ${res.author}`), list, error, acts);
  const close = () => {
    shade.remove();
    box.remove();
    if (back?.isConnected) back.focus();
  };
  const postIt = async () => {
    if (ok.disabled) return;
    ok.disabled = cancel.disabled = true;
    error.hidden = true;
    note.textContent = 'Posting…';
    try {
      await shown(root, n, res.at, 'pr/submit', { verdict: verdict(root, n, res) });
    } catch (err) {
      error.textContent = err.message;
      error.hidden = false;
      note.textContent = `Everyone on the ${w.kind} sees it at once.`;
      ok.disabled = cancel.disabled = false;
      hooks.announce?.(err.message);
      loadView(root, n).catch(() => {});
      return;
    }
    setReview(root, n, { verdict: 'comment' });
    setReviewed(root, n, res);
    close();
    const said = `Posted your review on ${w.num}${n}`;
    hooks.say?.(said);
    hooks.announce?.(said);
    fetch('/api/count?name=review', { method: 'POST' }).catch(() => {}); // the local kijk counts, also for a remote folder
    await loadView(root, n).catch(() => {});
    hooks.reviewed?.(root, n);
  };
  ok.onclick = postIt;
  cancel.onclick = close;
  box.onkeydown = e => {
    if (e.key === 'Escape') {
      e.preventDefault();
      e.stopPropagation();
      if (!cancel.disabled) close();
    } else if (e.key === 'Enter' && e.target !== cancel) {
      e.preventDefault();
      postIt();
    } else if (e.key === 'Tab') {
      // The focus stays in the dialog: it goes from one button to the other.
      e.preventDefault();
      (document.activeElement === ok ? cancel : ok).focus();
    }
  };
  shade.onclick = () => { if (!cancel.disabled) close(); };
  document.body.append(shade, box);
  ok.focus();
}
