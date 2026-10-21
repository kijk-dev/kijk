// The folder view's keys, IntelliJ's defaults for the platform; the page around it binds its own.
// Letters and digits match the physical key (e.code), because Alt changes e.key on macOS.
export const mac = /Mac/.test(navigator.platform);

// A normal browser tab keeps some keys for itself; an app window gives them to the page. Each such
// action also has a fallback key. Both always work; tooltips show the one for this window.
const appWindow = matchMedia('(display-mode: standalone)');

const KEYS = {
  gotoFile: { mac: ['Meta+Shift+O'], other: ['Ctrl+Shift+N'], fallback: { other: 'Alt+Shift+N' } },
  recentFiles: { mac: ['Meta+E'], other: ['Ctrl+E'] },
  find: { mac: ['Meta+F'], other: ['Ctrl+F'] },
  findNext: { mac: ['Meta+G', 'F3'], other: ['F3'] },
  findPrev: { mac: ['Meta+Shift+G', 'Shift+F3'], other: ['Shift+F3'] },
  gotoLine: { mac: ['Meta+L'], other: ['Ctrl+G'] },
  nextChange: { mac: ['F7'], other: ['F7'] },
  prevChange: { mac: ['Shift+F7'], other: ['Shift+F7'] },
  filesTab: { mac: ['Meta+1'], other: ['Alt+1'], fallback: { mac: 'Alt+1' } },
  gitTab: { mac: ['Meta+0'], other: ['Alt+0'] },
  prsTab: { mac: ['Meta+2'], other: ['Alt+2'], fallback: { mac: 'Alt+2' } },
  commit: { mac: ['Meta+K'], other: ['Ctrl+K'] },
  push: { mac: ['Meta+Shift+K'], other: ['Ctrl+Shift+K'] },
  closeTab: { mac: ['Meta+W'], other: ['Ctrl+F4'], fallback: { mac: 'Alt+W', other: 'Alt+W' } },
  back: { mac: ['Meta+[', 'Meta+Alt+ArrowLeft'], other: ['Ctrl+Alt+ArrowLeft'] },
  forward: { mac: ['Meta+]', 'Meta+Alt+ArrowRight'], other: ['Ctrl+Alt+ArrowRight'] },
};

const os = mac ? 'mac' : 'other';
const combos = action => {
  const k = KEYS[action];
  return k.fallback?.[os] ? [...k[os], k.fallback[os]] : k[os];
};

function matches(e, combo) {
  const parts = combo.split('+');
  const key = parts.pop();
  for (const [mod, on] of [['Meta', e.metaKey], ['Ctrl', e.ctrlKey], ['Alt', e.altKey], ['Shift', e.shiftKey]]) {
    if (parts.includes(mod) !== on) return false;
  }
  if (/^[A-Z]$/.test(key)) return e.code === `Key${key}`;
  if (/^[0-9]$/.test(key)) return e.code === `Digit${key}`;
  if (key === '[' || key === ']') return e.code === (key === '[' ? 'BracketLeft' : 'BracketRight');
  return e.key === key;
}

const handlers = new Map();

// onKey binds an action. A handler that returns false leaves the key to the browser.
export function onKey(action, fn) {
  handlers.set(action, fn);
}

const typing = t => t.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(t.tagName);

document.addEventListener('keydown', e => {
  // On macOS ⌥ alone types a character, such as ∑ for ⌥W, so a text field keeps it.
  if (mac && e.altKey && !e.metaKey && !e.ctrlKey && typing(e.target)) return;
  for (const [action, fn] of handlers) {
    if (!combos(action).some(c => matches(e, c))) continue;
    if (fn(e) === false) return;
    e.preventDefault();
    e.stopPropagation();
    return;
  }
}, true);

const SYMBOL = { Meta: '⌘', Ctrl: '⌃', Alt: '⌥', Shift: '⇧' };

function label(combo) {
  if (!mac) return combo;
  const parts = combo.split('+');
  const key = parts.pop();
  return ['Ctrl', 'Alt', 'Shift', 'Meta'].filter(m => parts.includes(m)).map(m => SYMBOL[m]).join('') + key;
}

// keyLabel shows the key that works in this window, as the platform writes it: ⇧⌘O, Ctrl+Shift+N.
export function keyLabel(action) {
  const k = KEYS[action];
  return label(k.fallback?.[os] && !appWindow.matches ? k.fallback[os] : k[os][0]);
}

// keyNames lists every key of an action for aria-keyshortcuts.
export const keyNames = action => combos(action).map(c => c.replace('Ctrl', 'Control')).join(' ');
