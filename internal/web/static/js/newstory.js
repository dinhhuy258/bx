import { api } from './api.js';
import { ask, confirmBox, pickFrom, sheet, toast } from './ui.js';

const enc = encodeURIComponent;

// TYPES are the branch types of a new branch, in the order of the TUI's type picker.
const TYPES = ['feat', 'fix', 'chore', 'hotfix', 'refactor', 'docs', { value: 'none', label: '(none)' }];

// slug is naming.Slug: lowercase, every character outside [a-z0-9._-] becomes '-', runs of '-'
// collapse, and '-' and '.' are trimmed at both ends.
function slug(s) {
  return s.toLowerCase().replace(/[^a-z0-9._-]+/g, '-').replace(/-+/g, '-').replace(/^[-.]+|[-.]+$/g, '');
}

// cleanBranch is naming.CleanBranch: every '/'-separated part slugged, empty parts dropped.
const cleanBranch = (s) => s.split('/').map(slug).filter(Boolean).join('/');

// storyName is naming.StoryName: the slug of the part after the last '/'.
const storyName = (branch) => slug(branch.slice(branch.lastIndexOf('/') + 1));

// directPR is client.directPR: the filter names one pull request, #123 or its URL.
export const directPR = (q) => /^(#\d+|https?:\/\/[^/\s]+\/[^/\s]+\/[^/\s]+\/pull\/\d+\S*)$/.test(q.trim());

// ago is ui.Ago: how long ago unix time t was, in the largest whole unit.
function ago(t) {
  const s = Date.now() / 1000 - t;
  if (s < 60) return 'now';
  if (s < 3600) return `${Math.floor(s / 60)}m`;
  if (s < 86400) return `${Math.floor(s / 3600)}h`;
  return `${Math.floor(s / 86400)}d`;
}

// newStoryMenu asks where a new story of project starts: a new branch, an existing branch or a
// pull request. snap is the project's snapshot; refresh reloads the project screen. The new
// story's session opens when it started.
export function newStoryMenu({ project, snap, refresh }) {
  const workspace = snap.project.kind === 'workspace';

  async function repos(title, tick) {
    let all;
    try { all = await api.projectRepos(project); } catch (e) { toast(e.message); return null; }
    const v = await ask(title, [{ name: 'repos', label: 'Repositories', type: 'checks', options: all, value: tick ? [tick] : [] }], 'Create');
    return v && v.repos;
  }

  async function newBranch() {
    let info;
    try { info = await api.storyInfo(project); } catch (e) { toast(e.message); return; }
    const last = TYPES.some((t) => (t.value || t) === info.last_type) ? info.last_type : 'feat';
    const base = info.base ? ` ← ${info.base}` : '';
    const fields = [
      { name: 'name', label: 'Name', placeholder: 'login timeout, MON-123 or fix/login', hint: (v) => {
        const b = cleanBranch(v.name || '');
        if (b.includes('/')) return `→ ${b}${base}`;
        const s = slug(v.name || '') || '<name>';
        return `→ ${v.type === 'none' ? s : `${v.type}/${s}`}${base}`;
      } },
      { name: 'type', label: 'Type', type: 'select', options: TYPES, value: last },
    ];
    if (workspace) {
      try { fields.push({ name: 'repos', label: 'Repositories', type: 'checks', options: await api.projectRepos(project) }); }
      catch (e) { toast(e.message); return; }
    }
    const v = await ask('New branch', fields, 'Create');
    if (!v || !v.name) return;
    const body = { name: v.name, repos: v.repos };
    // a full branch ("fix/login") carries its own type
    if (!cleanBranch(v.name).includes('/')) body.type = v.type;
    create(body);
  }

  async function existingBranch() {
    toast('Fetching origin…');
    let l;
    try { l = await api.branches(project, true); } catch (e) { toast(e.message); return; }
    if (l.note) toast(l.note);
    if (!l.branches.length) { toast(`No branches in ${project}`); return; }
    const items = l.branches.map((b) => ({
      branch: b.name,
      label: `${b.name}  ${b.local ? 'local' : 'origin'} · ${ago(b.time)}${workspace && b.repos?.length ? ` · ${b.repos.join(', ')}` : ''}`,
    }));
    const it = await pickFrom('Existing branch', items);
    if (!it) return;
    const body = { source: 'branch', branch: it.branch };
    if (workspace) {
      body.repos = await repos(`Repositories of ${storyName(it.branch)} (branch ${it.branch})`);
      if (!body.repos) return;
    }
    create(body);
  }

  const prItems = (prs) => prs.map((pr) => ({
    pr,
    label: `${workspace ? `${pr.slug || pr.repo} ` : ''}#${pr.number} ${pr.title}   ${pr.author} · ${pr.branch}`,
  }));

  async function pullRequest() {
    let l;
    try { l = await api.prs(project, ''); } catch (e) { toast(e.message); return; }
    const errs = l.errors || [];
    if (!l.prs.length && errs.length) { toast(`gh: ${errs[0]}`); return; }
    const it = await pickFrom(`Pull requests${errs.length ? ` · gh: ${errs[0]}` : ''}`, prItems(l.prs), { direct: directPR, search: async (q) => {
      let r;
      try { r = await api.prs(project, q); } catch (e) { toast(e.message); return null; }
      if (r.direct && r.prs.length === 1) return { pick: prItems(r.prs)[0] };
      if (!r.prs.length) { toast(r.errors?.length ? `gh: ${r.errors[0]}` : `No pull requests match ${q}`); return []; }
      return prItems(r.prs);
    } });
    if (!it) return;
    const { pr } = it;
    const body = { source: 'pr', pr: pr.number, pr_repo: pr.repo };
    if (workspace) {
      body.repos = await repos(`Repositories of ${storyName(pr.branch)} (branch ${pr.branch})`, pr.repo);
      if (!body.repos) return;
    }
    create(body);
  }

  // create sends body; a branch that exists asks to use it and sends it again.
  async function create(body) {
    toast('Creating story…');
    let r;
    try {
      r = await api.newStory(project, body);
    } catch (e) {
      if (e.status !== 409 || !e.result?.exists) { toast(e.message); refresh(); return; }
      const where = { local: 'locally', origin: 'on origin', both: 'locally and on origin' }[e.result.exists] || 'already';
      if (!(await confirmBox(`Branch ${e.result.branch} exists ${where}. Use it?`, 'Use it'))) return;
      try { r = await api.newStory(project, { ...body, use_existing: true }); } catch (e2) { toast(e2.message); refresh(); return; }
    }
    // one toast: a second one would cover the first
    toast(r.session_error ? `${r.message} · session: ${r.session_error}` : r.message);
    if (r.num) location.hash = `#/p/${enc(project)}/s/${r.num}`;
    else refresh();
  }

  sheet('New story', [
    { label: 'New branch', run: newBranch },
    { label: 'Existing branch', run: existingBranch },
    { label: 'Pull request', run: pullRequest },
  ]);
}
