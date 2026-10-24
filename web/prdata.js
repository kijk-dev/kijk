import { get } from './api.js';

// The answers about pull requests, per folder and number, shared by the PRs tab, its Changes and
// its Overview. One call at a time per pull request; each answer goes to every listener.
const views = new Map();
const calls = new Map();
const listeners = new Set();
const key = (root, n) => `${root}\n${n}`;

export const cachedView = (root, n) => views.get(key(root, n));

export function loadView(root, n) {
  const k = key(root, n);
  if (!calls.has(k)) {
    const call = get('pr/view', { root, n }).then(res => {
      views.set(k, res);
      for (const fn of listeners) fn(root, n, res);
      return res;
    });
    calls.set(k, call);
    call.finally(() => calls.delete(k)).catch(() => {});
  }
  return calls.get(k);
}

// onView runs fn(root, n, answer) after each new answer.
export const onView = fn => listeners.add(fn);

// age is how long ago an ISO time was, short: 5m, 3h, 2d, 1w.
export function age(iso) {
  const s = Math.max(0, (Date.now() - Date.parse(iso)) / 1000);
  if (!Number.isFinite(s)) return '';
  if (s < 3600) return `${Math.max(1, Math.floor(s / 60))}m`;
  if (s < 86400) return `${Math.floor(s / 3600)}h`;
  if (s < 7 * 86400) return `${Math.floor(s / 86400)}d`;
  return `${Math.floor(s / (7 * 86400))}w`;
}
