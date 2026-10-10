// Instant tooltips for elements with data-tip (lines split by \n, the first is bold).
// They open to the right inside the rail, above in the status bar, else below, and also on keyboard focus.
const box = document.createElement('div');
box.id = 'tip';
box.setAttribute('role', 'tooltip');
box.hidden = true;
document.body.append(box);
let target = null;

function show(el) {
  target = el;
  box.textContent = el.dataset.tip;
  box.hidden = false;
  const r = el.getBoundingClientRect();
  const b = box.getBoundingClientRect();
  const right = !!el.closest('#rail');
  const above = !!el.closest('#statusbar');
  const x = right ? r.right + 6 : r.left + r.width / 2 - b.width / 2;
  const y = right ? r.top + r.height / 2 - b.height / 2 : above ? r.top - b.height - 6 : r.bottom + 6;
  box.style.left = `${Math.max(4, Math.min(x, innerWidth - b.width - 4))}px`;
  box.style.top = `${Math.max(4, Math.min(y, innerHeight - b.height - 4))}px`;
}

function hide() {
  target = null;
  box.hidden = true;
}

// refreshTip redraws an open tooltip after its element's data-tip changed.
export function refreshTip() {
  if (target?.isConnected && target.dataset.tip) show(target);
  else if (target) hide();
}

for (const [on, off] of [['pointerover', 'pointerout'], ['focusin', 'focusout']]) {
  document.addEventListener(on, e => {
    const el = e.target.closest?.('[data-tip]');
    if (el && el !== target) show(el);
  });
  document.addEventListener(off, e => {
    if (target && !target.contains(e.relatedTarget)) hide();
  });
}
document.addEventListener('pointerdown', hide);
