// Instant tooltips for elements with data-tip (lines split by \n, the first is bold).
// They open below, or on the side tipSide gives for a part of the page, and also on keyboard focus.
const box = document.createElement('div');
box.id = 'tip';
box.setAttribute('role', 'tooltip');
box.hidden = true;
document.body.append(box);
let target = null;
const sides = []; // [selector, 'right' or 'above']

// tipSide opens the tooltips of elements inside selector on another side.
export function tipSide(selector, side) {
  sides.push([selector, side]);
}

function show(el) {
  target = el;
  box.textContent = el.dataset.tip;
  box.hidden = false;
  const r = el.getBoundingClientRect();
  const b = box.getBoundingClientRect();
  const side = sides.find(([selector]) => el.closest(selector))?.[1];
  const right = side === 'right';
  const above = side === 'above';
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
