import { h, patch } from './api.js';
import { current } from './lines.js';

// The layout of every line diff: unified, or split into old and new side by side. The choice is
// one for all diffs; a viewer too narrow for two halves shows unified and keeps the choice.
const KEY = 'kijk.diffLayout';
const WIDE = 700; // px: below this a half shows too little of a line
let slot = null;
let chosen = 'unified';
let narrow = false;

export const diffLayout = () => (chosen === 'split' && !narrow ? 'split' : 'unified');

// set redraws the open view when the layout its diffs show changes: by the switch, or across the width.
function set(next) {
  const was = diffLayout();
  next();
  draw();
  if (diffLayout() !== was) current?.relayout?.();
}

// The Unified / Split switch at the end of the tab strip; the CSS shows it while a line diff shows.
function draw() {
  const now = diffLayout();
  patch(slot, h('div', { class: 'mode', attrs: { role: 'group', 'aria-label': 'Diff layout' } },
    [['unified', 'Unified'], ['split', 'Split']].map(([l, text]) => h('button', {
      key: l, disabled: l === 'split' && narrow, attrs: { 'aria-pressed': String(l === now) },
      data: l === 'split' && narrow ? { tip: 'Split needs a wider view' } : {},
      onclick: () => set(() => {
        chosen = l;
        try { localStorage.setItem(KEY, l); } catch { /* storage blocked */ }
      }),
    }, text))));
}

// initLayout puts the switch in slot and follows the width of viewer. Before it diffs show unified.
export function initLayout(viewer, at) {
  slot = at;
  try { if (localStorage.getItem(KEY) === 'split') chosen = 'split'; } catch { /* storage blocked */ }
  narrow = viewer.clientWidth < WIDE;
  draw();
  new ResizeObserver(() => {
    if ((viewer.clientWidth < WIDE) !== narrow) set(() => { narrow = !narrow; });
  }).observe(viewer);
}
