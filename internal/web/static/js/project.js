import { api } from './api.js';
import { h, icon, topBar, toneOf, dotted, tile, poll, toast, ask, confirmBox, sheet } from './ui.js';
import { sessionMenu, withForce } from './sessionmenu.js';
import { newStoryMenu } from './newstory.js';
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

  // shellsOf lists a story's shells ('' = Chats), by number (the server sorts them)
  const shellsOf = (story) => (snap.shells || []).filter((s) => s.story === story);

  // group lists a story's sessions, then its shells, in a card under the story's label. An empty
  // story offers its first session.
  function group(story, rows) {
    const d = story && snap.live.diff ? snap.live.diff[story] : null;
    const shells = shellsOf(story);
    return h('section', { class: 'sec' },
      h('div', { class: 'sec-head' },
        h('span', { class: 'sec-label' }, story ? [icon('branch'), h('span', { class: 'story' }, story)] : [icon('chat'), 'Chats']),
        d ? h('span', { class: 'diff' }, h('span', { class: 'add' }, `+${d.add}`), h('span', { class: 'del' }, `−${d.del}`),
          d.dirty ? h('span', {}, `· ${d.dirty} uncommitted`) : null) : null,
        h('button', { class: 'ib', 'aria-label': story ? 'Story actions' : 'Chats actions', onclick: () => storyMenu(story) }, icon('more'))),
      h('div', { class: 'card' }, rows.length || shells.length
        ? [rows.map(([n, s]) => sessionRow(n, s)), shells.map(shellRow)]
        : h('button', { class: 'card-add', onclick: () => newSession(story) }, icon('plus'), 'New session')));
  }

  // shellRow is [$k][ label / command or "shell" ][busy][⋯]: the command shows after a rename,
  // and busy while something other than the shell itself runs in it
  function shellRow(sh) {
    return h('div', { class: 'item' },
      h('a', { class: 'item-link', href: shellHref(name, sh.story, sh.k) },
        tile(`$${sh.k}`, 'shell'),
        h('span', { class: 'item-text' }, h('span', { class: 'item-title' }, shellLabel(sh)),
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
    if (!story) { sheet('Chats', [{ label: 'New session', run: () => newSession('') }, shellItem]); return; }
    const items = [{ label: 'New session', run: () => newSession(story) }, shellItem];
    if (snap.project.kind === 'workspace') items.push({ label: 'Add repositories', run: () => addRepos(story) });
    // closing removes the worktree: red, in its own group, and asked twice when files would be lost
    items.push({ label: 'Close story', icon: 'trash', hint: 'Stops its sessions and removes its worktree', danger: true, run: async () => {
      const n = shellsOf(story).length;
      const shells = n ? ` and ${n} ${n === 1 ? 'shell' : 'shells'}` : '';
      if (!(await confirmBox(`Close ${story}?`, 'Close story', { danger: true, text: `Its sessions${shells} stop and its worktree is removed.` }))) return;
      force('stories/close', { name: story }, `Close ${story}`, {
        ok: 'Discard and close', danger: true,
        message: (r) => `${story} has ${r.dirty} uncommitted ${r.dirty === 1 ? 'file' : 'files'}${r.detail ? ` (${r.detail})` : ''}. They are lost with its worktree.`,
      });
    } });
    sheet(story, items);
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

  async function act(action, body) {
    try { return await api.action(name, action, body); }
    catch (e) { toast(e.message); return null; }
    finally { refresh(); }
  }

  // force is withForce for this project; the list reloads afterwards
  const force = (action, body, what, o) => withForce(name, action, body, what, o).finally(refresh);

  return { stop };
}
