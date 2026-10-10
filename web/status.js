import { el, post } from './api.js';

// The status bar: the kijk version, or in its place the news about a newer one.
const bar = document.getElementById('statusbar');
const announcer = document.getElementById('announce');
const CLOSE = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M18 6 6 18"/><path d="m6 6 12 12"/></svg>';

let loaded = null; // the version this page came from
let last = null;
let shown = '';
let restartSince = 0;

// Each notice remembers what it was closed for (a version, or the error text): closing "is out"
// still lets "is installed" show.
function closedFor(kind) {
  try { return localStorage.getItem(`kijk.closed.${kind}`) || ''; } catch { return ''; }
}

function newer(a, b) {
  const x = (a || '').split('.').map(Number);
  const y = (b || '').split('.').map(Number);
  if (x.length !== 3 || x.some(isNaN)) return false;
  if (y.length !== 3 || y.some(isNaN)) return true;
  for (let i = 0; i < 3; i++) if (x[i] !== y[i]) return x[i] > y[i];
  return false;
}

// A new kijk that has not answered after 15 seconds failed to start, so the window says so.
export function restarting() {
  return last?.busy === 'restarting' && Date.now() - restartSince < 15000;
}

// showKijk draws the bar from the kijk field of an /api/events message.
export function showKijk(k) {
  if (!k) return;
  if (loaded === null) loaded = k.version;
  else if (k.version !== loaded) {
    location.reload(); // the server now runs another version, with other web files
    return;
  }
  if (k.busy === 'restarting' && last?.busy !== 'restarting') restartSince = Date.now();
  last = k;
  draw();
}

function draw() {
  const k = last;
  const parts = [];
  const tip = `You have kijk ${k.version}`;
  if (k.busy === 'updating') parts.push(news('busy', `Updating to kijk ${k.latest}…`, 'Updating…', tip));
  else if (k.busy === 'restarting') parts.push(news('busy', 'Restarting…', 'Restarting…', tip));
  else if (k.error && k.error !== closedFor('error')) {
    parts.push(news('error', [`Update failed: ${k.error} `, then('· run kijk update in a terminal')], 'Update failed',
      `Update failed: ${k.error}\nRun kijk update in a terminal, or reinstall with the install script.\n${tip}`,
      closeButton(() => remember('error', k.error))));
  } else if (k.installed && newer(k.installed, closedFor('installed'))) {
    const v = k.installed;
    parts.push(k.canRestart
      ? news('', `kijk ${v} is installed`, `${v} installed`, tip,
          button('Restart', `Restart kijk on ${v}. This window reloads.`, () => post('restart', {})),
          closeButton(() => remember('installed', v)))
      : news('', [`kijk ${v} is installed `, then('· restart kijk to use it')], [`${v} installed `, then('· restart kijk')], tip,
          closeButton(() => remember('installed', v))));
  } else if (k.latest && newer(k.latest, closedFor('out'))) {
    const v = k.latest;
    parts.push(news('', `kijk ${v} is out`, `${v} is out`, tip,
      button('Update', `Download and install kijk ${v}`, () => post('update', {})),
      closeButton(() => remember('out', v))));
  } else {
    parts.push(el('span', 'kijk', `kijk ${k.version}`));
  }

  const key = parts.map(p => p.outerHTML).join('');
  if (key === shown) return;
  shown = key;
  const hadFocus = bar.contains(document.activeElement);
  bar.replaceChildren(...parts);
  if (hadFocus) (bar.querySelector('button') || bar).focus();
  const text = parts[0].querySelector('.long')?.textContent;
  if (text && announcer.textContent !== text) announcer.textContent = text;
}

function remember(kind, v) {
  try { localStorage.setItem(`kijk.closed.${kind}`, v); } catch { /* storage blocked */ }
}

function then(text) {
  return el('span', 'then', text);
}

function news(dot, long, short, tip, ...rest) {
  const s = el('span', 'kijk news');
  s.dataset.tip = tip;
  const msg = el('span', 'msg');
  const l = el('span', 'long');
  const sh = el('span', 'short');
  l.append(...[long].flat());
  sh.append(...[short].flat());
  msg.append(l, sh);
  s.append(el('span', `dot ${dot}`.trim()), msg, ...rest);
  return s;
}

function button(label, tip, act) {
  const b = el('button', 'tool', label);
  b.dataset.tip = tip;
  b.onclick = async () => {
    b.disabled = true;
    try { await act(); } catch (e) { b.disabled = false; announcer.textContent = e.message; }
  };
  return b;
}

function closeButton(onClose) {
  const b = el('button', 'icon-btn');
  b.innerHTML = CLOSE;
  b.setAttribute('aria-label', 'Hide until the next version');
  b.dataset.tip = 'Hide until the next version';
  b.onclick = () => {
    onClose();
    draw();
  };
  return b;
}
