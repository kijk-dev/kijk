import { el } from './api.js';

// confirm opens a modal dialog. run() does the work; while it runs both buttons are off and the
// note says busy. If run throws, the dialog stays open and shows err.message. It resolves true
// after run succeeded and the dialog closed, false on Cancel / Escape / backdrop.
// rows are [key, value] pairs, where value is a string or a Node; mark is a Node before the title.
export function confirm({ mark, title, sub, rows = [], note, busy, ok: okText, run }) {
  if (document.querySelector('.confirm')) return Promise.resolve(false);
  return new Promise(resolve => {
    const back = document.activeElement;
    const shade = el('div', 'backdrop');
    const box = el('div', 'confirm');
    box.setAttribute('role', 'dialog');
    box.setAttribute('aria-modal', 'true');
    box.setAttribute('aria-labelledby', 'confirm-title');
    const head = el('h2');
    head.id = 'confirm-title';
    if (mark) head.append(mark);
    head.append(title);
    const list = el('ul');
    list.append(...rows.map(([key, value]) => {
      const li = el('li');
      li.append(el('span', 'k', key), typeof value === 'string' ? el('span', 'v', value) : value);
      return li;
    }));
    const error = el('p', 'error');
    error.hidden = true;
    const acts = el('div', 'acts');
    const said = el('span', 'muted', note);
    const cancel = el('button', 'tool', 'Cancel');
    const ok = el('button', 'primary', okText);
    acts.append(said, cancel, ok);
    box.append(head);
    if (sub) box.append(el('p', 'sub', sub));
    box.append(list, error, acts);
    const close = done => {
      shade.remove();
      box.remove();
      if (back?.isConnected) back.focus();
      resolve(done);
    };
    const go = async () => {
      if (ok.disabled) return;
      ok.disabled = cancel.disabled = true;
      error.hidden = true;
      said.textContent = busy;
      try {
        await run();
      } catch (err) {
        error.textContent = err.message;
        error.hidden = false;
        said.textContent = note;
        ok.disabled = cancel.disabled = false;
        ok.focus(); // the disabled button lost it, and Escape and Tab need it in the dialog
        return;
      }
      close(true);
    };
    ok.onclick = go;
    cancel.onclick = () => close(false);
    box.onkeydown = e => {
      if (e.key === 'Escape') {
        e.preventDefault();
        e.stopPropagation();
        if (!cancel.disabled) close(false);
      } else if (e.key === 'Enter' && e.target !== cancel) {
        e.preventDefault();
        go();
      } else if (e.key === 'Tab') {
        // The focus stays in the dialog, going round its inputs and buttons.
        e.preventDefault();
        const stops = [...box.querySelectorAll('input, button')].filter(s => !s.disabled);
        const i = stops.indexOf(document.activeElement);
        stops[(i + (e.shiftKey ? stops.length - 1 : 1)) % stops.length]?.focus();
      }
    };
    shade.onclick = () => { if (!cancel.disabled) close(false); };
    document.body.append(shade, box);
    ok.focus();
  });
}
