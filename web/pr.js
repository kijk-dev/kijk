import { el, h, patch, icon } from './api.js';

// Forge marks from Simple Icons (CC0).
export const FORGE = {
  github: {
    name: 'GitHub', num: '#', kind: 'pull request', short: 'PR', ci: 'Checks',
    icon: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 .297c-6.63 0-12 5.373-12 12 0 5.303 3.438 9.8 8.205 11.385.6.113.82-.258.82-.577 0-.285-.01-1.04-.015-2.04-3.338.724-4.042-1.61-4.042-1.61C4.422 18.07 3.633 17.7 3.633 17.7c-1.087-.744.084-.729.084-.729 1.205.084 1.838 1.236 1.838 1.236 1.07 1.835 2.809 1.305 3.495.998.108-.776.417-1.305.76-1.605-2.665-.3-5.466-1.332-5.466-5.93 0-1.31.465-2.38 1.235-3.22-.135-.303-.54-1.523.105-3.176 0 0 1.005-.322 3.3 1.23.96-.267 1.98-.399 3-.405 1.02.006 2.04.138 3 .405 2.28-1.552 3.285-1.23 3.285-1.23.645 1.653.24 2.873.12 3.176.765.84 1.23 1.91 1.23 3.22 0 4.61-2.805 5.625-5.475 5.92.42.36.81 1.096.81 2.22 0 1.606-.015 2.896-.015 3.286 0 .315.21.69.825.57C20.565 22.092 24 17.592 24 12.297c0-6.627-5.373-12-12-12"/></svg>',
  },
  gitlab: {
    name: 'GitLab', num: '!', kind: 'merge request', short: 'MR', ci: 'Pipeline',
    icon: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="m23.6004 9.5927-.0337-.0862L20.3.9814a.851.851 0 0 0-.3362-.405.8748.8748 0 0 0-.9997.0539.8748.8748 0 0 0-.29.4399l-2.2055 6.748H7.5375l-2.2057-6.748a.8573.8573 0 0 0-.29-.4412.8748.8748 0 0 0-.9997-.0537.8585.8585 0 0 0-.3362.4049L.4332 9.5015l-.0325.0862a6.0657 6.0657 0 0 0 2.0119 7.0105l.0113.0087.03.0213 4.976 3.7264 2.462 1.8633 1.4995 1.1321a1.0085 1.0085 0 0 0 1.2197 0l1.4995-1.1321 2.4619-1.8633 5.006-3.7489.0125-.01a6.0682 6.0682 0 0 0 2.0094-7.003z"/></svg>',
  },
};
// forgeWords are the words of the forge of an answer about pull requests: GitHub's when kijk does
// not know it.
export const forgeWords = res => FORGE[res?.forge?.kind] || FORGE.github;

// ownPR is the pull request of the PR line's answer res while it is open: the folder's own one.
export const ownPR = res => (['open', 'draft'].includes(res?.pr?.state) ? res.pr : null);

export const MERGE = '<svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="18" cy="18" r="3"/><circle cx="6" cy="6" r="3"/><path d="M6 21V9a9 9 0 0 0 9 9"/></svg>';
const STATE = { draft: 'Draft', merged: 'Merged', closed: 'Closed' };

const link = (cls, href, props, ...children) => h(href ? 'a' : 'span', { class: cls, ...(href && { href, target: '_blank', rel: 'noopener' }), ...props }, children);

// forgeMark is the forge's own mark, with its name as the tooltip.
export function forgeMark(kind) {
  const mark = el('span', `hostmark ${kind}`);
  mark.innerHTML = FORGE[kind].icon; // a fixed string, never server text
  mark.title = FORGE[kind].name;
  return mark;
}

// showPR fills the pull request line: the PR with its CI and review state, else links to the forge.
// open(n), when given, opens the pull request in the PRs tab.
export function showPR(line, res, open) {
  const f = res?.forge;
  line.hidden = !f;
  if (!f) return patch(line);
  const w = FORGE[f.kind], p = res.pr;
  if (!p && (res.note || !res.tool)) {
    return patch(line, h('div', 'pr-top', forgeMark(f.kind), link('pr-link', f.web, {}, `Open on ${w.name} ↗`), res.create && link('pr-link', res.create, {}, `Create ${w.short} ↗`)),
      res.note && h('div', 'pr-note', res.note));
  }
  if (!p) return patch(line, h('div', 'pr-top', forgeMark(f.kind), h('span', 'pr-title none', `No ${w.kind}`), link('pr-link', res.create, {}, 'Create ↗')));
  const go = e => {
    e.preventDefault();
    open(p.number);
  };
  // The title opens the pull request in the PRs tab; ↗ still opens the website.
  const title = open
    ? h('a', {
      class: 'pr-title', tabIndex: 0, attrs: { role: 'link' }, data: { tip: 'Open in the PRs tab' }, onclick: go, onkeydown: e => { if (e.key === 'Enter') go(e); },
    }, p.title)
    : link('pr-title', p.url, {}, p.title);
  patch(line, h('div', 'pr-top', forgeMark(f.kind), h('span', 'pr-num', `${w.num}${p.number}`), title,
    link('pr-link', p.url, { data: { tip: `Open on ${w.name}` }, attrs: { 'aria-label': `Open on ${w.name}` } }, '↗')), prChips(p, w, res.default));
}

// prChips are the chips of a pull request's state, CI and review, or null when there are none.
export function prChips(p, w, defaultBranch) {
  const chip = (cls, text, href, tip) => link(`chip ${cls}`, href, tip ? { title: tip } : {}, text);
  // Open is the normal case, so only other states get a chip.
  const chips = [
    STATE[p.state] && chip(p.state, STATE[p.state]),
    p.ci === 'failed' && chip('fail', `✗ ${w.ci} failed`, p.ciUrl, p.failed ? `Failed: ${p.failed.join(', ')}` : ''),
    p.ci === 'running' && chip('run', [h('span', 'dot'), `${w.ci} running`], p.ciUrl),
    p.ci === 'passed' && chip('pass', `✓ ${w.ci} passed`, p.ciUrl),
    p.review === 'approved' ? chip('pass', '✓ Approved')
      : p.review === 'changes' ? chip('fail', 'Changes requested')
        : p.approvals ? chip('pass', `✓ ${p.approvals} ${p.approvals === 1 ? 'approval' : 'approvals'}`)
          : p.review === 'needed' && chip('', 'Review needed'),
    p.target && p.target !== defaultBranch && link('chip target', null, { title: `Merges into ${p.target}`, attrs: { 'aria-label': `Merges into ${p.target}` } }, icon(MERGE), p.target),
  ].filter(Boolean);
  return chips.length ? h('div', { key: 'chips', class: 'chips' }, chips) : null;
}
