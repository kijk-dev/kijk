import { el } from './api.js';

// The lines of a diff or a file, a file's line counts and status, and which view is open, for
// view.js, git.js and the box view of boxes.js.
// Nothing here touches the page or the network when it loads.

export const LINE_HEIGHT = 18;
export let current = null; // the open file or diff; a newer open makes older fetches stale

// setView makes another kind of view, such as Branch changes, the open view: {kind, scroll, reload(change), jump(dir)}.
export function setView(v) { current = v; }

export const isCurrent = v => current === v;

const KIND = { A: 'added', D: 'deleted', '?': 'untracked', U: 'conflict' };
export const kind = status => KIND[status] || 'modified';

const num = n => n.toLocaleString('en-US');

// lineCount shows "+40 −12", or nothing when both are zero. Screen readers hear words instead.
export function lineCount(add = 0, del = 0) {
  if (!add && !del) return null;
  const s = el('span', 'lc');
  if (add) s.append(el('span', 'lc-add', `+${num(add)}`));
  if (del) s.append(el('span', 'lc-del', `−${num(del)}`));
  for (const part of s.children) part.setAttribute('aria-hidden', 'true');
  const lines = n => `${num(n)} ${n === 1 ? 'line' : 'lines'}`;
  s.append(el('span', 'sr-only', add && del ? `${lines(add)} added, ${num(del)} removed` : add ? `${lines(add)} added` : `${lines(del)} removed`));
  return s;
}

// content is plain text, or colored pieces [class, text] from the server. marks are find matches;
// side ('l' or 'r') takes only those of one half of a split row. words are [from, to] to underline.
export function line(numbers, content, cls, key, side, words = []) {
  const row = el('div', cls ? `ln ${cls}` : 'ln');
  const flash = current?.flash;
  if (flash && flash.key === key && Date.now() < flash.until) row.classList.add('flash');
  const gut = el('span', 'gut');
  for (const n of numbers) gut.append(el('span', null, n || ''));
  const tx = el('span', 'tx');
  const pieces = Array.isArray(content) ? content : [['', content]];
  let marks = current?.hits?.get(key) || [];
  if (side) marks = marks.filter(m => m[3] === side);
  let pos = 0, m = 0, w = 0;
  for (const [c, text] of pieces) {
    for (let i = 0; i < text.length;) {
      while (m < marks.length && marks[m][1] <= pos + i) m++;
      while (w < words.length && words[w][1] <= pos + i) w++;
      const mark = marks[m], word = words[w];
      const inside = mark && mark[0] <= pos + i;
      const changed = word && word[0] <= pos + i;
      const end = Math.min(text.length, (inside ? mark[1] : mark ? mark[0] : Infinity) - pos, (changed ? word[1] : word ? word[0] : Infinity) - pos);
      let part = c ? el('span', `h${c}`, text.slice(i, end)) : document.createTextNode(text.slice(i, end));
      if (inside) {
        const hit = el('mark', mark[2] ? 'hit current' : 'hit');
        hit.append(part);
        part = hit;
      }
      if (changed) {
        const wd = el('span', 'w');
        wd.append(part);
        part = wd;
      }
      tx.append(part);
      i = end;
    }
    pos += text.length;
  }
  row.append(gut, tx);
  return row;
}

// pairRows turns hunk lines into rows {l, r} of old and new line; an unchanged line is both. Split
// pairs the nth removed line of a block with its nth added line; unified gives each its own row.
export function pairRows(lines, split) {
  const rows = [];
  for (let i = 0; i < lines.length;) {
    if (lines[i].k === ' ') {
      rows.push({ l: lines[i], r: lines[i++] });
      continue;
    }
    const dels = [], adds = [];
    for (; i < lines.length && lines[i].k !== ' '; i++) {
      if (split) (lines[i].k === '-' ? dels : adds).push(lines[i]);
      else rows.push(lines[i].k === '-' ? { l: lines[i], r: null } : { l: null, r: lines[i] });
    }
    for (let j = 0; j < Math.max(dels.length, adds.length); j++) rows.push({ l: dels[j] || null, r: adds[j] || null });
  }
  return rows;
}

// splitRow draws row {l, r} of a split diff: old left, new right, each with its own line number, and
// fine hatching where a side has no line. cls(l) gives a line's classes.
export function splitRow(row, key, cls) {
  const words = row.l && row.r && row.l !== row.r ? (row.words ??= changedWords(row.l.t, row.r.t)) : [];
  const half = (l, side, w) => (l ? line([side === 'l' ? l.o : l.n], l.x || l.t, cls(l), key, side, w) : el('div', 'fill'));
  const out = el('div', 'srow');
  out.append(half(row.l, 'l', words[0]), half(row.r, 'r', words[1]));
  return out;
}

const TOKEN = /\w+|\s+|[^\w\s]/g;
const MAX_TOKENS = 500; // the word diff takes tokens², so a minified line gets no marks

// changedWords gives [old, new], the [from, to] of the words that differ in a pair of lines. None
// when more than 60% of either line changed: then the marks would only be noise.
function changedWords(a, b) {
  const ta = a.match(TOKEN) || [], tb = b.match(TOKEN) || [];
  if (ta.length > MAX_TOKENS || tb.length > MAX_TOKENS) return [];
  const w = tb.length + 1;
  const same = new Uint16Array((ta.length + 1) * w); // tokens in common from i, j on
  for (let i = ta.length - 1; i >= 0; i--) {
    for (let j = tb.length - 1; j >= 0; j--) {
      same[i * w + j] = ta[i] === tb[j] ? same[(i + 1) * w + j + 1] + 1 : Math.max(same[(i + 1) * w + j], same[i * w + j + 1]);
    }
  }
  const ra = [], rb = [];
  const add = (rs, from, len) => {
    if (rs.length && rs.at(-1)[1] === from) rs.at(-1)[1] += len;
    else rs.push([from, from + len]);
  };
  let i = 0, j = 0, pa = 0, pb = 0;
  while (i < ta.length || j < tb.length) {
    if (i < ta.length && j < tb.length && ta[i] === tb[j]) {
      pa += ta[i++].length;
      pb += tb[j++].length;
    } else if (j === tb.length || (i < ta.length && same[(i + 1) * w + j] >= same[i * w + j + 1])) {
      add(ra, pa, ta[i].length);
      pa += ta[i++].length;
    } else {
      add(rb, pb, tb[j].length);
      pb += tb[j++].length;
    }
  }
  const solid = s => s.replace(/\s/g, '').length;
  const share = (rs, s) => rs.reduce((n, [from, to]) => n + solid(s.slice(from, to)), 0) / Math.max(1, solid(s));
  return share(ra, a) > 0.6 || share(rb, b) > 0.6 ? [] : [ra, rb];
}
