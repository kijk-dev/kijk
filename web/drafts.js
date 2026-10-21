import { el, post, storeKey } from './api.js';
import { loadView } from './prdata.js';
import { avatar } from './threads.js';
import { iconButton } from './boxes.js';
import { FORGE } from './pr.js';
import { mac } from './keys.js';

// Writing a review: comments on lines and replies as drafts on the forge, their edits and deletes,
// and resolves. Every write asks for the pull request again, so what shows is the forge's state.

const ICON = {
  edit: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M21.17 6.81a1 1 0 0 0-3.99-3.99L3.84 16.17a2 2 0 0 0-.5.83l-1.32 4.35a.5.5 0 0 0 .62.62l4.35-1.32a2 2 0 0 0 .83-.5z"/></svg>',
  trash: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M3 6h18"/><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6"/><path d="M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/></svg>',
};

// The server says this when it has no diff of the pull request, as after a restart.
const NOT_SHOWN = 'Open the pull request again';

let hooks = {}; // announce(text)

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

// addComment adds a draft. at is the head the comment was written on: after a restart kijk sends it
// once more only when the head is still that one, so the line is still the same line.
async function addComment(root, n, at, c) {
  let res;
  try {
    res = await post('pr/comment', { root, n, ...c });
  } catch (err) {
    if (err.status !== 409) throw err;
    const fresh = await loadView(root, n);
    if (c.thread || err.message !== NOT_SHOWN || fresh.at !== at) throw err;
    res = await post('pr/comment', { root, n, ...c });
  }
  remember(root, n, res.id);
  await loadView(root, n).catch(() => {});
}

// ----- Text boxes -----

const typed = e => e.key === 'Enter' && (mac ? e.metaKey : e.ctrlKey);

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
    if (typed(e)) {
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

// lineEditor is the box under row l of file f where a new comment is written. done() closes it.
export function lineEditor(root, n, res, f, l, done) {
  const old = l.k === '-';
  const line = old ? l.o : l.n;
  const box = el('div', 'thr draftonly editing');
  const head = el('div', 'thead');
  head.append(el('span', null, `New comment · line ${line}`));
  const c = el('div', 'c');
  const who = el('div', 'who');
  who.append(avatar(res.me || 'you'), el('b', null, res.me || 'you'));
  const text = textBox(`Comment on line ${line}`, '', 'Add to review', async body => {
    await addComment(root, n, res.at, { path: f.path, orig: f.orig || '', line, old, oldLine: l.k === ' ' ? l.o : 0, body });
    hooks.announce?.(`Comment added to your review on line ${line}`);
    done();
  }, done);
  c.append(who, text);
  box.append(head, c);
  text.focus();
  return { key: `${old ? 'o' : 'n'}:${line}`, el: box };
}

// ----- What a thread box needs to write -----

const editors = new Map(); // a draft being edited: its text box
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
      iconButton(ICON.trash, 'Delete', async () => {
        try {
          await send(root, n, 'comment/delete', { id: c.id });
          hooks.announce?.('Draft deleted');
        } catch (err) {
          hooks.announce?.(err.message);
        }
      }),
    ]),
  };
  if (!can) return { drafts };
  return {
    drafts,
    reply: t => replyStrip(root, n, res, t),
    resolve: async (t, on) => {
      try {
        await send(root, n, 'resolve', { thread: t.id, resolved: on });
        hooks.announce?.(on ? 'Thread resolved' : 'Thread open again');
      } catch (err) {
        hooks.announce?.(err.message);
      }
    },
  };
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
