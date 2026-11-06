import { api } from './api.js';
import { cloneFrom } from './clone.js';
import { h, icon, topBar, dotted, tile, poll, toast, ask, sheet } from './ui.js';

// Every project by name; those with a session that needs you (waiting for an answer, or unread:
// finished a turn nobody has opened since) get their own section at the top. There is no
// opened list on the phone.
export function showProjects(root) {
  const body = h('div', { class: 'page' });
  const err = h('div', { class: 'error', hidden: true });
  root.append(topBar(h('a', { href: '#/settings', class: 'ib', 'aria-label': 'Settings' }, icon('gear')),
    h('span', { class: 'ttl-name' }), null,
    h('button', { class: 'ib', 'aria-label': 'New project', onclick: newProject }, icon('plus'))),
  h('div', { class: 'big' }, 'Projects'), err, body);

  const stop = poll(async () => {
    try {
      const ps = await api.projects();
      err.hidden = true;
      ps.sort((a, b) => a.name.localeCompare(b.name));
      const needs = (p) => p.waiting > 0 || p.unread > 0;
      const need = ps.filter(needs), rest = ps.filter((p) => !needs(p));
      const parts = [];
      if (need.length) parts.push(section(dotted('wait', 'Needs you'), need, 'card attn'));
      if (rest.length) parts.push(section(need.length ? 'All projects' : null, rest, 'card'));
      if (!ps.length) parts.push(emptyState());
      body.replaceChildren(...parts);
    } catch (e) {
      err.textContent = e.message;
      err.hidden = false;
    }
  });
  return { stop };
}

const section = (label, ps, cls) => h('section', { class: 'sec' },
  label ? h('div', { class: 'sec-head' }, h('span', { class: 'sec-label' }, label)) : null,
  h('div', { class: cls }, ps.map(projectRow)));

// projectRow: [folder tile][ name / ● 1 waiting ● 1 unread ● 2 running ][›], the tile tinted by state
function projectRow(p) {
  const counts = [p.waiting ? dotted('wait', `${p.waiting} waiting`) : null, p.unread ? dotted('unread', `${p.unread} unread`) : null,
    p.running ? dotted('run', `${p.running} running`) : null];
  return h('a', { class: 'item-link', href: `#/p/${encodeURIComponent(p.name)}` },
    tile(icon('folder'), p.waiting ? 'wait' : p.unread ? 'unread' : p.running ? 'run' : ''),
    h('span', { class: 'item-text' }, h('span', { class: 'item-title' }, p.name),
      h('span', { class: 'item-meta' }, p.waiting || p.unread || p.running ? counts : 'Nothing running')),
    icon('chev', 'i chev'));
}

const emptyState = () => h('div', { class: 'empty-state' },
  tile(icon('folder')), h('b', {}, 'No projects yet'),
  h('p', {}, 'Add a folder on the server, clone from GitHub, or start an empty project.'),
  h('button', { class: 'primary', onclick: newProject }, icon('plus'), 'Add project'));

function newProject() {
  sheet('New project', [
    { label: 'Folder on the server', run: async () => {
      const v = await ask('Add folder', [{ name: 'path', label: 'Absolute path on the server', placeholder: '/home/bx/src/app' }], 'Add');
      if (v && v.path) create({ kind: 'folder', path: v.path });
    } },
    { label: 'Clone from GitHub', run: async () => {
      const repo = await cloneFrom();
      if (repo) create({ kind: 'clone', repo });
    } },
    { label: 'New empty project', run: async () => {
      const v = await ask('New project', [{ name: 'name', label: 'Name' }], 'Create');
      if (v && v.name) create({ kind: 'new', name: v.name });
    } },
    { label: 'Workspace from projects', run: async () => {
      let all = [];
      try { all = (await api.projects()).filter((p) => p.kind !== 'workspace').map((p) => p.name).sort(); }
      catch (e) { toast(e.message); return; }
      if (all.length < 2) { toast('Need at least 2 repo projects'); return; }
      const v = await ask('New workspace', [
        { name: 'name', label: 'Name' },
        { name: 'projects', label: 'Repositories to link', type: 'checks', options: all, value: [] },
      ], 'Create');
      if (!v) return;
      if (v.projects.length < 2) { toast('Pick at least 2 projects'); return; }
      create({ kind: 'workspace', name: v.name, projects: v.projects });
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
