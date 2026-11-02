import { api } from './api.js';
import { h, bar, poll, toast, ask, confirmBox, sheet } from './ui.js';
import { sessionMenu, withForce } from './sessionmenu.js';

const enc = encodeURIComponent;

export function showProject(root, name) {
  const sub = h('div', { class: 'sub' });
  const err = h('div', { class: 'error', hidden: true });
  const body = h('div');
  root.append(bar(h('a', { href: '#/', class: 'icon', 'aria-label': 'Back' }, '‹'), name,
    h('button', { class: 'icon', 'aria-label': 'New', onclick: () => addMenu() }, '＋')), sub, err, body);
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
    sub.textContent = p.kind === 'workspace' ? 'workspace'
      : `${live.branch || 'main'}${live.dirty_files ? ` · ${live.dirty_files} uncommitted` : ''}`;
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

  function group(story, rows) {
    const d = story && snap.live.diff ? snap.live.diff[story] : null;
    const meta = d ? `+${d.add} −${d.del}${d.dirty ? ` · ${d.dirty} uncommitted` : ''}` : '';
    return h('section', { class: 'group' },
      h('header', {}, h('span', { class: 'title' }, story || 'Chats'), h('span', {}, meta),
        h('button', { class: 'icon', 'aria-label': 'Story actions', onclick: () => storyMenu(story) }, '⋯')),
      h('ul', { class: 'list' }, rows.length ? rows.map(([n, s]) => sessionRow(n, s)) : h('li', { class: 'empty' }, 'No sessions')));
  }

  function sessionRow(n, s) {
    const st = (snap.session_states || {})[n] || { state: 'idle', text: '' };
    return h('li', { class: s.done ? 'done' : '' },
      h('a', { class: 'row', href: `#/p/${enc(name)}/s/${n}` },
        h('span', { class: 'title' }, s.title || `#${n}`),
        h('span', { class: `badge ${st.state}` }, st.state === 'gone' || st.state === 'done' ? st.state : st.text)),
      h('button', { class: 'icon', 'aria-label': 'Session actions', onclick: () => sessionMenu({ project: name, num: n, session: s, state: st.state, act, refresh, ask, withForce: force, onDeleted: refresh }) }, '⋯'));
  }

  function storyMenu(story) {
    if (!story) { sheet('Chats', [{ label: 'New chat', run: () => newSession('') }]); return; }
    const items = [{ label: 'New session', run: () => newSession(story) }];
    if (snap.project.kind === 'workspace') items.push({ label: 'Add repositories', run: () => addRepos(story) });
    items.push({ label: 'Close story', run: async () => {
      if (await confirmBox(`Close ${story}? Its sessions stop and the worktree is removed.`, 'Close')) {
        force('stories/close', { name: story }, `Close ${story}`);
      }
    } });
    sheet(story, items);
  }

  function addMenu() {
    sheet(name, [
      { label: 'New chat', run: () => newSession('') },
      { label: 'New story', run: newStory },
    ]);
  }

  async function newSession(story) {
    const v = await ask(story ? `New session in ${story}` : 'New chat',
      [{ name: 'title', label: 'Title (optional; named after the first prompt)' }], 'Start');
    if (v === null) return;
    const r = await act('sessions', { story, title: v.title });
    if (r) location.hash = `#/p/${enc(name)}/s/${r.num}`;
  }

  async function newStory() {
    const fields = [{ name: 'name', label: 'Story name (the branch)' }];
    if (snap.project.kind === 'workspace') {
      try { fields.push({ name: 'repos', label: 'Repositories', type: 'checks', options: await api.projectRepos(name) }); }
      catch (e) { toast(e.message); return; }
    }
    const v = await ask('New story', fields, 'Create');
    if (v && v.name) act('stories', { name: v.name, repos: v.repos });
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
