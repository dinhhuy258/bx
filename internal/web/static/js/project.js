import { api } from './api.js';
import { h, icon, topBar, toneOf, dotted, tile, poll, toast, ask, confirmBox, sheet } from './ui.js';
import { sessionMenu, withForce } from './sessionmenu.js';
import { newStoryMenu } from './newstory.js';
import { prBadgeText } from './pr.js';
import { shellHref, shellLabel, plainShell, shellMenu, confirmNewShell, openShell } from './shell.js';

const enc = encodeURIComponent;

export function showProject(root, name) {
  // the header's second line is the branch and its uncommitted files, or "workspace"
  const metaEl = h('span', { class: 'ttl-meta' });
  const err = h('div', { class: 'error', hidden: true });
  const body = h('div', { class: 'page' });
  root.append(topBar(h('a', { href: '#/', class: 'ib', 'aria-label': 'Back' }, icon('back')),
    h('span', { class: 'ttl-name' }, name), metaEl,
    h('button', { class: 'ib', 'aria-label': 'New story', onclick: () => { if (snap) newStoryMenu({ project: name, snap, refresh }); } }, icon('plus'))), err, body);
  let snap = null;

  async function refresh() {
    try {
      snap = await api.snapshot(name);
      err.hidden = true;
      render();
    } catch (e) {
      if (e.status === 404) { toast(`No project ${name}`); location.hash = '#/'; return; }
      err.textContent = e.message;
      err.hidden = false;
    }
  }
  const stop = poll(refresh);

  function render() {
    const p = snap.project, live = snap.live;
    metaEl.replaceChildren(...(p.kind === 'workspace' ? [icon('folder'), 'workspace']
      : [icon('branch'), live.branch || 'main',
        live.dirty_files ? h('span', { class: 'warn' }, ` · ${live.dirty_files} uncommitted`) : '']));
    const groups = new Map([['', []]]);
    for (const s of Object.keys(p.stories || {}).sort()) groups.set(s, []);
    for (const [n, s] of Object.entries(p.sessions || {})) {
      const g = s.story || '';
      if (!groups.has(g)) groups.set(g, []);
      groups.get(g).push([Number(n), s]);
    }
    // open sessions first, newest first; done ones at the bottom
    const order = (a, b) => (a[1].done - b[1].done) || (b[0] - a[0]);
    body.replaceChildren(...[...groups].map(([story, rows]) => group(story, rows.sort(order))));
  }

  // changed: a story's diff has lines or uncommitted files to show; +0 −0 is no change
  const changed = (d) => !!d && !!(d.add || d.del || d.dirty);

  // behindOf is the commits origin has that a story's branch ('' = the project folder's) lacks,
  // as last fetched: what a sync would bring in
  const behindOf = (story) => (story ? ((snap.live.diff || {})[story] || {}).behind : snap.live.behind) || 0;

  // shellsOf lists a story's shells ('' = Chats), by number (the server sorts them)
  const shellsOf = (story) => (snap.shells || []).filter((s) => s.story === story);

  // prPills are a story's PR on its section head: the PR and CI in the state's colour, and the
  // threads waiting for you
  function prPills(story) {
    const b = story ? ((snap.live && snap.live.pr) || {})[story] : null;
    if (!b || !b.count) return null;
    const cls = b.fail || b.changes || b.conflict ? 'p-red' : b.pending ? 'p-yel' : 'p-grn';
    return [h('a', { class: `pill ${cls}`, href: prHref(story) }, prBadgeText({ ...b, threads: 0 })),
      b.threads ? h('a', { class: 'pill p-yel', href: prHref(story) }, `💬${b.threads}`) : null];
  }
  const needsYou = (story) => { const b = story ? ((snap.live && snap.live.pr) || {})[story] : null; return !!b && (b.fail || b.conflict); };
  const prHref = (story) => `#/p/${enc(name)}/pr/${enc(story)}`;

  // group lists a story's sessions, then its shells, in a card under the story's label. An empty
  // group is just its label; its ⋯ menu starts the first session.
  function group(story, rows) {
    const d = story && snap.live.diff ? snap.live.diff[story] : null;
    const all = shellsOf(story);
    const runs = all.filter((sh) => sh.run);
    const shells = all.filter((sh) => !sh.run);
    const n = behindOf(story);
    return h('section', { class: 'sec' },
      h('div', { class: 'sec-head' },
        h('span', { class: 'sec-label' }, story ? [icon('branch'), h('span', { class: 'story' }, story)] : [icon('chat'), 'Chats']),
        prPills(story),
        n ? h('span', { class: 'behind', title: `origin has ${n} new ${n === 1 ? 'commit' : 'commits'}` }, `↓${n}`) : null,
        changed(d) ? h('span', { class: 'diff' }, h('span', { class: 'add' }, `+${d.add}`), h('span', { class: 'del' }, `−${d.del}`),
          d.dirty ? h('span', {}, `· ${d.dirty} uncommitted`) : null) : null,
        h('button', { class: 'ib', 'aria-label': story ? 'Story actions' : 'Chats actions', onclick: () => storyMenu(story) }, icon('more'))),
      runCard(story, runs),
      rows.length || shells.length
        ? h('div', { class: needsYou(story) ? 'card attn' : 'card' }, rows.map(([n, s]) => sessionRow(n, s)), shells.map(shellRow))
        : null);
  }

  // runCard is a story's running runs: their links (new tab), then the run shells, with Stop and
  // Restart. Only ports a run owns (by name or by its shell): a plain shell's or an unowned one
  // has nothing Stop could end. Nothing while the story runs no app.
  function runCard(story, runs) {
    const ks = new Set(runs.map((sh) => sh.k));
    const ports = (((snap.live && snap.live.ports) || {})[story || ''] || []).filter((p) => p.run || ks.has(p.shell));
    if (!runs.length && !ports.length) return null;
    const link = (p) => p.error
      ? h('div', { class: 'item' }, h('span', { class: 'item-text' }, h('span', { class: 'item-title' }, `:${p.port}`), h('span', { class: 'item-meta' }, p.error)))
      : h('a', { class: 'item run-link', href: p.url, target: '_blank', rel: 'noopener' },
        h('span', { class: 'item-text' }, h('span', { class: 'item-title' }, p.url), p.forwarded ? h('span', { class: 'item-meta' }, '⇄ forwarded') : null),
        h('span', { class: 'run-go', 'aria-hidden': 'true' }, '↗'));
    return h('div', { class: 'card' },
      h('div', { class: 'sec-head' }, h('span', { class: 'sec-label' }, '▶ Runs'),
        h('button', { class: 'run-btn', onclick: () => runAction(story, 'stop') }, 'Stop'),
        h('button', { class: 'run-btn', onclick: () => runRestart(story) }, 'Restart')),
      ports.map(link), runs.map(shellRow));
  }

  // runAction posts runs/run or runs/stop; a run refused because another story runs asks first.
  async function runAction(story, action, replace) {
    try {
      await api.action(name, `runs/${action}`, { story, replace: !!replace });
      toast(action === 'stop' ? `${story}: runs stopped` : `${story}: starting`);
    } catch (e) {
      if (e.status === 409 && e.result && e.result.run_story) {
        const r = e.result;
        if (await confirmBox(`Stop ${r.run_story} and run ${story}?`, 'Stop and run', { text: `${r.run_story} runs ${(r.run_names || []).join(', ')}. One story of a project runs at a time.` })) {
          return runAction(story, action, true);
        }
      } else toast(e.message);
    }
    refresh();
  }

  async function runRestart(story) {
    try { await api.action(name, 'runs/stop', { story }); } catch (e) { toast(e.message); return; }
    return runAction(story, 'run');
  }

  // shellRow is [$k][ label / command or "shell" ][busy][⋯]: the command shows after a rename,
  // and busy while something other than the shell itself runs in it
  function shellRow(sh) {
    return h('div', { class: 'item' },
      h('a', { class: 'item-link', href: shellHref(name, sh.story, sh.k) },
        tile(`$${sh.k}`, 'shell'),
        h('span', { class: 'item-text' }, h('span', { class: 'item-title' }, sh.run && !sh.title ? sh.run : shellLabel(sh)),
          h('span', { class: 'item-meta' }, sh.title && sh.command ? h('span', { class: 'cmd' }, sh.command) : 'shell'))),
      sh.command && !plainShell(sh.command) ? h('span', { class: 'pill busy' }, 'busy') : null,
      h('button', { class: 'ib', 'aria-label': 'Shell actions', onclick: () => shellMenu({ project: name, sh, refresh, onClosed: refresh }) }, icon('more')));
  }

  // sessionRow is [tile][ title / ● state ][⋯], the tile and dot coloured by the state
  function sessionRow(n, s) {
    const st = (snap.session_states || {})[n] || { state: 'idle', text: '' };
    const tone = toneOf(st.state, st.unread);
    const text = st.state === 'gone' || st.state === 'done' ? st.state : st.text || st.state;
    return h('div', { class: s.done ? 'item done' : 'item' },
      h('a', { class: 'item-link', href: `#/p/${enc(name)}/s/${n}` },
        tile(icon('spark'), tone),
        h('span', { class: 'item-text' }, h('span', { class: 'item-title' }, s.title || `#${n}`),
          h('span', { class: 'item-meta' }, dotted(tone, text)))),
      h('button', { class: 'ib', 'aria-label': 'Session actions', onclick: () => sessionMenu({ project: name, num: n, session: s, state: st.state, act, refresh, ask, withForce: force, onDeleted: refresh }) }, icon('more')));
  }

  function storyMenu(story) {
    const shellItem = { label: 'New shell', run: () => newShell(story) };
    const n = behindOf(story);
    const syncItem = { label: 'Sync with origin', icon: 'sync', hint: n ? `${n} new ${n === 1 ? 'commit' : 'commits'} on origin` : 'Fast-forward from origin', run: () => sync(story) };
    if (!story) {
      const items = [{ label: 'New session', run: () => newSession('') }, shellItem];
      if (snap.project.kind === 'workspace') items.push({ label: 'Edit workspace', icon: 'folder', hint: 'Link or unlink repositories', run: () => editWorkspace() });
      if (snap.project.kind !== 'workspace') items.push(syncItem); // a workspace folder is no repository
      sheet('Chats', items);
      return;
    }
    const rv = ((snap.live && snap.live.review) || {})[story] || 0, dd = ((snap.live && snap.live.diff) || {})[story];
    const d = changed(dd) ? dd : null;
    const hint = rv || d ? [rv ? `${rv} to send` : null, rv && d ? ' · ' : null,
      d ? h('span', { class: 'add' }, `+${d.add}`) : null, d ? ' ' : null, d ? h('span', { class: 'del' }, `−${d.del}`) : null]
      .filter(Boolean) : '';
    const pb = ((snap.live && snap.live.pr) || {})[story];
    const items = [pb && pb.count ? { label: 'Pull request', icon: 'branch', hint: prBadgeText(pb), run: () => { location.hash = prHref(story); } } : null,
      rv || d ? { label: 'Review diff', hint, badge: rv || null, run: () => { location.hash = `#/p/${enc(name)}/d/${enc(story)}`; } } : null,
      { label: 'New session', run: () => newSession(story) }, shellItem,
      { label: 'Start runs', icon: 'play', hint: 'Start the runs of Settings › Runs (TUI)', run: () => runAction(story, 'run') }, syncItem].filter(Boolean);
    if (snap.project.kind === 'workspace') items.push({ label: 'Add repositories', run: () => addRepos(story) });
    // closing removes the worktree: red, in its own group, and asked twice when files would be lost
    items.push({ label: 'Close story', icon: 'trash', hint: 'Stops its sessions and removes its worktree', danger: true, run: async () => {
      const n = shellsOf(story).length;
      const shells = n ? ` and ${n} ${n === 1 ? 'shell' : 'shells'}` : '';
      const branch = (snap.project.stories[story] || {}).branch;
      const del = branch ? `branch ${branch}` : 'its branch';
      if (!(await confirmBox(`Close ${story}?`, 'Close story', { danger: true, text: `Its sessions${shells} stop, its worktree is removed and ${del} is deleted here and on origin.` }))) return;
      const r = await force('stories/close', { name: story }, `Close ${story}`, {
        ok: 'Discard and close', danger: true,
        message: (r) => `${story} has ${r.dirty} uncommitted ${r.dirty === 1 ? 'file' : 'files'}${r.detail ? ` (${r.detail})` : ''}. They are lost with its worktree.`,
      });
      if (r) toast(r.message ? `Closed ${story} · ${r.message}` : `Closed ${story}`);
    } });
    sheet(story, items);
  }

  // sync fast-forwards a story ('' = the project folder) to origin; the server says what it did.
  // While Claude works there it asks first.
  async function sync(story) {
    const what = story || snap.live.branch || 'main';
    toast(`Syncing ${what} with origin…`);
    const r = await force('sync', { story }, `Sync ${what}`, {
      ok: 'Sync',
      message: (res) => {
        const s = (snap.project.sessions || {})[res.num];
        const who = s && s.title ? `${s.title} is running` : 'Claude is working';
        return `Sync ${story || 'Chats'} while Claude works in it? ${who}. Files can change under it.`;
      },
    });
    if (r && r.message) toast(r.message);
  }

  async function newSession(story) {
    const v = await ask(`New session in ${story || 'chats'}`,
      [{ name: 'title', label: 'Title (optional; named after the first prompt)' }], 'Start');
    if (v === null) return;
    const r = await act('sessions', { story, title: v.title });
    if (r) location.hash = `#/p/${enc(name)}/s/${r.num}`;
  }

  async function newShell(story) {
    const k = await confirmNewShell(name, snap, story);
    if (k) openShell(name, story, k);
  }

  async function addRepos(story) {
    let all;
    try { all = await api.projectRepos(name); } catch (e) { toast(e.message); return; }
    const have = new Set(((snap.project.stories[story] || {}).repos || []).map((r) => r.rel));
    const v = await ask(`Add to ${story}`, [{ name: 'repos', label: 'Repositories', type: 'checks', options: all.filter((r) => !have.has(r)) }], 'Add');
    if (v && v.repos.length) force('stories/repos', { name: story, repos: v.repos }, `Add to ${story}`);
  }

  // editWorkspace ticks the repo projects linked into the workspace folder; the changes are
  // applied together, and the server refuses to unlink a repository an open story uses.
  async function editWorkspace() {
    let links, projects;
    try { [links, projects] = await Promise.all([api.workspaceLinks(name), api.projects()]); }
    catch (e) { toast(e.message); return; }
    const current = links.map((l) => l.name);
    const repos = projects.filter((p) => p.kind !== 'workspace' && p.name !== name).map((p) => p.name);
    const options = [...new Set([...repos, ...current])].sort();
    const label = (n) => (links.some((l) => l.name === n && l.missing) ? `${n} (missing)` : n);
    const v = await ask(`Edit ${name}`, [
      { name: 'links', label: 'Linked repositories', type: 'checks', options: options.map(label), value: current.map(label) },
    ], 'Apply');
    if (!v) return;
    const picked = v.links.map((s) => s.replace(/ \(missing\)$/, ''));
    const link = picked.filter((n) => !current.includes(n));
    const unlink = current.filter((n) => !picked.includes(n));
    if (!link.length && !unlink.length) { toast('No change'); return; }
    try {
      await api.editWorkspace(name, link, unlink);
      toast([link.length && `Linked ${link.join(', ')}`, unlink.length && `Unlinked ${unlink.join(', ')}`].filter(Boolean).join(' · '));
    } catch (e) { toast(e.message); }
    refresh();
  }

  async function act(action, body) {
    try { return await api.action(name, action, body); }
    catch (e) { toast(e.message); return null; }
    finally { refresh(); }
  }

  // force is withForce for this project; the list reloads afterwards
  const force = (action, body, what, o) => withForce(name, action, body, what, o).finally(refresh);

  return { stop };
}
