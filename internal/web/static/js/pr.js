import { api } from './api.js';
import { h, icon, topBar, toast } from './ui.js';
import { pickSessionOf } from './review.js';

const enc = encodeURIComponent;

// prBadgeText is a story's PR segment on the phone, like the list's badge but compact:
// "#142 ✗ · 💬3", "2 PRs ✗", "merged #131". '' when the story has no PR.
export function prBadgeText(b) {
  if (!b || !b.count) return '';
  const merged = b.merged || 0, closed = b.closed || 0;
  if (merged + closed === b.count) {
    if (b.count === 1) return `${merged ? 'merged' : 'closed'} #${b.number}`;
    return merged ? `merged ${merged}/${b.count}` : `closed ${closed}/${b.count}`;
  }
  const head = b.count === 1 ? `#${b.number}` : `${b.count} PRs`;
  const ci = b.fail ? '✗' : b.pending ? '◌' : b.ready ? '✓ ready' : b.pass ? '✓' : '';
  const parts = [ci ? `${head} ${ci}` : head];
  if (merged) parts.push(`merged ${merged}/${b.count}`);
  if (b.threads) parts.push(`💬${b.threads}`);
  if (b.changes) parts.push('changes');
  if (b.conflict) parts.push('conflict');
  return parts.join(' · ');
}

const failedChecks = (pr) => (pr.checks || []).filter((c) => c.state === 'fail');
const lastAuthor = (t) => (t.comments && t.comments.length ? t.comments[t.comments.length - 1].author : '');

// openThread is ui.PRThread.Open: unresolved, and its last comment is not mine.
export function openThread(t, me) {
  if (t.resolved || !t.comments || !t.comments.length) return false;
  return lastAuthor(t).toLowerCase() !== String(me || '').toLowerCase();
}

// openCount is how many threads of pr wait for me.
const openCount = (pr, me) => (pr.threads || []).filter((t) => openThread(t, me)).length;

// firstFocus is the repo of the PR to open on: repo if it has a PR, else the first that needs
// you (a failed check, then changes requested or open threads, then a conflict), else the first.
export function firstFocus(data, repo) {
  const found = (data.prs || []).filter((p) => p.number);
  if (!found.length) return '';
  if (repo && found.some((p) => p.repo === repo)) return repo;
  const rank = (p) => (failedChecks(p).length ? 0 : p.decision === 'CHANGES_REQUESTED' || openCount(p, data.me) ? 1 : p.merge === 'DIRTY' ? 2 : 3);
  return found.reduce((best, p) => (rank(p) < rank(best) ? p : best)).repo || '';
}

// initialPicks are the threads picked when the page opens: open and never sent.
export const initialPicks = (pr, me) => (pr.threads || []).filter((t) => openThread(t, me) && !t.sent_at).map((t) => t.id);

// agoText is "just now", "20s ago", "6m ago", "2h ago".
export function agoText(unix, now = Date.now() / 1000) {
  const s = Math.max(0, now - unix);
  if (s < 10) return 'just now';
  if (s < 60) return `${Math.floor(s)}s ago`;
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
  return `${Math.floor(s / 86400)}d ago`;
}

const GLYPH = { fail: '✗', pending: '◌', pass: '✓', skip: '–' };
const ORDER = ['fail', 'pending', 'pass', 'skip'];

// showPR is #/p/{name}/pr/{story}: the story's pull request, its checks and review threads.
// With several repositories a switcher picks the PR. Fix CI and Address send to a session of
// the story and submit the prompt. Picks live here, per repository, while the page is open.
export function showPR(root, name, story, repo) {
  const err = h('div', { class: 'error', hidden: true });
  const body = h('div', { class: 'page pr-page' });
  const meta = h('span', { class: 'ttl-meta' });
  const web = h('a', { class: 'ib', target: '_blank', rel: 'noopener', 'aria-label': 'Open on GitHub', hidden: true }, '↗');
  const reload = h('button', { class: 'ib', 'aria-label': 'Refresh', onclick: () => load(true) }, icon('sync'));
  root.append(topBar(h('a', { href: `#/p/${enc(name)}`, class: 'ib', 'aria-label': 'Back' }, icon('back')),
    h('span', { class: 'ttl-name' }, story), meta, h('span', { class: 'ttl-acts' }, reload, web)), err, body);
  let data = null, focus = '', stopped = false;
  const picks = new Map(); // repo → Set of thread ids
  const seen = new Set(); // threads whose pick was set once: a reload keeps the user's choice
  const picksOf = (r) => { if (!picks.has(r)) picks.set(r, new Set()); return picks.get(r); };
  // pickedOf is the picked threads of pr that can still be sent: a thread resolved since it was
  // picked is left out, as the TUI's Picked does.
  const pickedOf = (pr) => [...picksOf(pr.repo)].filter((id) => (pr.threads || []).some((t) => t.id === id && !t.resolved));
  const found = () => data.prs.filter((p) => p.number);
  const current = () => found().find((p) => p.repo === focus);

  async function load(refresh) {
    try {
      const d = await (refresh ? api.refreshPR(name, story) : api.pr(name, story));
      if (stopped) return;
      const first = !data;
      data = d;
      err.hidden = true;
      if (first || !found().some((p) => p.repo === focus)) focus = firstFocus(d, first ? repo : '');
      for (const p of d.prs) {
        for (const t of p.threads || []) {
          if (seen.has(t.id)) continue;
          seen.add(t.id);
          if (openThread(t, d.me) && !t.sent_at) picksOf(p.repo).add(t.id);
        }
      }
      render();
    } catch (e) {
      if (stopped) return;
      if (e.status === 404) { toast(e.message); location.hash = `#/p/${enc(name)}`; return; }
      err.textContent = e.message;
      err.hidden = false;
    }
  }

  function empty() {
    const notPushed = data.prs.length > 0 && data.prs.every((p) => p.missing === 'not pushed');
    return h('p', { class: 'help' }, notPushed ? 'Not pushed. Push the branch and open a PR.' : 'No pull request for this branch yet.');
  }

  function switcher() {
    return h('div', { class: 'seg' }, found().map((p) => {
      const mark = failedChecks(p).length ? ' ✗' : (p.checks || []).some((c) => c.state === 'pending') ? ' ◌' : '';
      return h('button', { class: p.repo === focus ? 'on' : '', onclick: () => { focus = p.repo; render(); } },
        `${p.repo || 'PR'} #${p.number}${mark}`);
    }));
  }

  function pills(pr) {
    const out = [];
    const add = (cls, text) => out.push(h('span', { class: `pill ${cls}` }, text));
    if (pr.state === 'MERGED') add('p-grn', 'merged');
    if (pr.state === 'CLOSED') add('p-dim', 'closed');
    if (pr.draft) add('p-dim', 'draft');
    const bad = failedChecks(pr).length;
    if (bad) add('p-red', `✗ ${bad} ${bad === 1 ? 'check' : 'checks'} failed`);
    else if ((pr.checks || []).some((c) => c.state === 'pending')) add('p-yel', '◌ checks running');
    const open = openCount(pr, data.me);
    if (open) add('p-yel', `💬 ${open} open ${open === 1 ? 'thread' : 'threads'}`);
    if (pr.decision === 'CHANGES_REQUESTED') add('p-red', 'changes requested');
    if (pr.decision === 'APPROVED') add('p-grn', 'approved');
    if (pr.decision === 'REVIEW_REQUIRED') add('p-dim', 'review required');
    if (pr.merge === 'DIRTY') add('p-red', `⇄ conflicts with ${pr.base}`);
    else if (pr.merge === 'BEHIND') add('p-dim', `⇄ behind ${pr.base}`);
    else if (pr.merge === 'CLEAN' && pr.state === 'OPEN' && (pr.checks || []).length && !failedChecks(pr).length
      && !(pr.checks || []).some((c) => c.state === 'pending')) add('p-grn', '✓ ready');
    return h('div', { class: 'pr-pills' }, out);
  }

  function checksCard(pr) {
    const cs = ORDER.flatMap((s) => (pr.checks || []).filter((c) => c.state === s));
    if (!cs.length) return null;
    return h('section', { class: 'sec' }, h('div', { class: 'sec-head' }, h('span', { class: 'sec-label' }, 'Checks')),
      h('div', { class: 'card' }, cs.map((c) => {
        const inner = [h('span', { class: `pr-g g-${c.state}` }, GLYPH[c.state]), h('span', { class: 'pr-name' }, c.name)];
        return c.url ? h('a', { class: 'item pr-row', href: c.url, target: '_blank', rel: 'noopener' }, inner) : h('div', { class: 'item pr-row' }, inner);
      })));
  }

  function whereOf(t) {
    return `${t.path}:${t.start_line && t.start_line < t.line ? `${t.start_line}–${t.line}` : t.line}`;
  }

  function threadsCard(pr) {
    const ts = [...(pr.threads || [])];
    if (!ts.length) return null;
    const rank = (t) => (t.resolved ? 2 : t.sent_at ? 1 : 0);
    ts.sort((a, b) => rank(a) - rank(b)); // stable: the file order inside each group
    const mine = picksOf(pr.repo);
    return h('section', { class: 'sec' },
      h('div', { class: 'sec-head' }, h('span', { class: 'sec-label' }, 'Threads'), mine.size ? h('span', { class: 'diff' }, `${mine.size} picked`) : null),
      h('div', { class: 'card' }, ts.map((t) => {
        const first = (t.comments && t.comments[0]) || { author: '', body: '' };
        const note = t.resolved ? 'resolved' : t.sent_at ? `⇢ sent ${agoText(t.sent_at)}` : '';
        const box = t.resolved ? h('span', { class: 'pr-g g-pass' }, '✓')
          : h('input', { type: 'checkbox', class: 'check', checked: mine.has(t.id), 'aria-label': `Pick ${whereOf(t)}`,
            onchange: (e) => { if (e.target.checked) mine.add(t.id); else mine.delete(t.id); render(); } });
        return h('label', { class: t.resolved ? 'item pr-row pr-thread done' : 'item pr-row pr-thread' }, box,
          h('span', { class: 'item-text' },
            h('span', { class: 'item-title' }, whereOf(t)),
            h('span', { class: 'item-meta' }, first.author, note ? ` · ${note}` : ''),
            h('span', { class: 'pr-body' }, first.body)));
      })));
  }

  function render() {
    meta.textContent = data.checked_at ? `checked ${agoText(data.checked_at)}` : '';
    const pr = current();
    web.hidden = !pr;
    if (pr) web.href = pr.url;
    if (!pr) { body.replaceChildren(empty()); return; }
    const n = pickedOf(pr).length;
    const fix = h('button', { hidden: !failedChecks(pr).length, onclick: () => pick('fix', pr) }, 'Fix CI');
    const address = h('button', { class: 'primary', disabled: !n, onclick: () => pick('address', pr) }, n ? `Address ${n}` : 'Address');
    body.replaceChildren(...[
      found().length > 1 ? switcher() : null,
      h('div', { class: 'card pr-sum' }, h('div', { class: 'pr-title' }, h('b', {}, `#${pr.number} `), pr.title),
        h('div', { class: 'pr-sub' }, `${pr.repo ? `${pr.repo} ` : ''}→ ${pr.base}`), pills(pr)),
      checksCard(pr), threadsCard(pr),
      h('div', { class: 'btns' }, fix, address)].filter(Boolean));
  }

  function pick(kind, pr) {
    const ids = pickedOf(pr);
    const title = kind === 'fix' ? `Fix CI of #${pr.number}` : `Address ${ids.length} ${ids.length === 1 ? 'comment' : 'comments'} of #${pr.number}`;
    pickSessionOf(name, story, title, "Pasted into Claude's input and submitted", (req) => send({ kind, repo: pr.repo, threads: kind === 'address' ? ids : undefined, ...req }));
  }

  async function send(req) {
    toast(req.new ? 'Starting a session…' : `Sending to [${req.session}]…`);
    try {
      const r = await api.sendPR(name, story, req);
      toast(r.message, { label: 'Open', run: () => { location.hash = `#/p/${enc(name)}/s/${r.num}`; } });
    } catch (e) {
      toast(e.message);
    }
    if (!stopped) load(false);
  }

  load(false);
  return { stop() { stopped = true; } };
}
