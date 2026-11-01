import { api } from './api.js';
import { h, bar, poll, toast, ask, sheet } from './ui.js';

// Every project, waiting ones first, then by name. There is no opened list on the phone.
export function showProjects(root) {
  const list = h('ul', { class: 'list' });
  const err = h('div', { class: 'error', hidden: true });
  root.append(bar(h('a', { href: '#/settings', class: 'icon', 'aria-label': 'Settings' }, '⚙'), 'bx',
    h('button', { class: 'icon', 'aria-label': 'New project', onclick: newProject }, '＋')), err, list);

  const stop = poll(async () => {
    try {
      const ps = await api.projects();
      err.hidden = true;
      ps.sort((a, b) => (b.waiting > 0) - (a.waiting > 0) || a.name.localeCompare(b.name));
      list.replaceChildren(...ps.map((p) => h('li', {},
        h('a', { class: 'row', href: `#/p/${encodeURIComponent(p.name)}` },
          h('span', { class: 'title' }, p.name),
          h('span', { class: 'meta' },
            p.waiting ? h('span', { class: 'badge waiting' }, `${p.waiting} waiting`) : null,
            p.running ? h('span', { class: 'badge running' }, `${p.running} running`) : null)))));
      if (!ps.length) list.append(h('li', { class: 'empty' }, 'No projects yet. Tap ＋ to add one.'));
    } catch (e) {
      err.textContent = e.message;
      err.hidden = false;
    }
  });
  return { stop };
}

function newProject() {
  sheet('New project', [
    { label: 'Folder on the server', run: async () => {
      const v = await ask('Add folder', [{ name: 'path', label: 'Absolute path on the server', placeholder: '/home/bx/src/app' }], 'Add');
      if (v && v.path) create({ kind: 'folder', path: v.path });
    } },
    { label: 'Clone from GitHub', run: async () => {
      let repos = [];
      try { repos = await api.githubRepos(); } catch (e) { toast(`GitHub: ${e.message}`); }
      const v = await ask('Clone', [{ name: 'repo', label: 'owner/repo', list: repos }], 'Clone');
      if (v && v.repo) create({ kind: 'clone', repo: v.repo });
    } },
    { label: 'New empty project', run: async () => {
      const v = await ask('New project', [{ name: 'name', label: 'Name' }], 'Create');
      if (v && v.name) create({ kind: 'new', name: v.name });
    } },
  ]);
}

async function create(body) {
  toast(body.kind === 'clone' ? 'Cloning…' : 'Working…');
  try {
    const r = await api.createProject(body);
    location.hash = `#/p/${encodeURIComponent(r.name)}`;
  } catch (e) {
    toast(e.message);
  }
}
