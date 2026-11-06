import { api } from './api.js';
import { h, ago, toast } from './ui.js';

// The clone picker lists one owner at a time: the gh login, then its organizations, as chips.
// A chip shows the owner's latest pushed repositories (one page, fetched the first time it is
// tapped); typing filters them at once and, after a pause, searches GitHub for the names in
// that owner. It opens on the owner last cloned from.

const OWNER_KEY = 'bx.cloneOwner';
const SEARCH_DELAY = 300;

const ownerRepoRe = /^([A-Za-z0-9][A-Za-z0-9-]*)\/([A-Za-z0-9_.-]+)$/;
const repoURLRe = /^(?:https?:\/\/)?github\.com\/([A-Za-z0-9][A-Za-z0-9-]*)\/([A-Za-z0-9_.-]+?)(?:\.git)?\/?(?:[#?].*)?$/;

// typedRepo is the owner/repo that q names, as owner/repo or a github.com URL, else ''.
export function typedRepo(q) {
  const t = q.trim();
  const m = t.match(repoURLRe) || t.match(ownerRepoRe);
  return m ? `${m[1]}/${m[2]}` : '';
}

// searchQuery is the name to search owner for: the part after "owner/", and nothing when it
// names another owner (someone/thing is cloned by name, not searched) or for a URL.
export function searchQuery(q, owner) {
  let t = q.trim();
  if (t.includes('://')) return '';
  const i = t.indexOf('/');
  if (i >= 0) {
    if (t.slice(0, i).toLowerCase() !== owner.toLowerCase()) return '';
    t = t.slice(i + 1);
  }
  return t.trim();
}

function lastOwner() {
  try { return localStorage.getItem(OWNER_KEY) || ''; } catch { return ''; }
}

function rememberOwner(repo) {
  try { localStorage.setItem(OWNER_KEY, repo.split('/')[0]); } catch { /* storage off */ }
}

// cloneFrom resolves to the owner/repo to clone, or null on Cancel.
export async function cloneFrom() {
  let owners;
  try { owners = await api.githubOwners(); } catch (e) { toast(`GitHub: ${e.message}`); return null; }
  if (!owners.length) { toast('GitHub: no account to clone from (gh auth login)'); return null; }
  return new Promise((resolve) => {
    let picked = null, seq = 0, timer = 0, shown = [];
    const want = lastOwner().toLowerCase();
    let owner = owners.find((o) => o.toLowerCase() === want) || owners[0];
    const latest = new Map(); // owner → its latest page, once it has come
    let found = { owner: '', q: '', repos: [] }; // the reply of the last search
    let loading = false, searching = '', error = '';

    const q = h('input', { type: 'search', placeholder: 'Search repositories', enterkeyhint: 'go',
      autocapitalize: 'off', autocomplete: 'off', autocorrect: 'off', spellcheck: 'false' });
    const chips = h('div', { class: 'clone-owners', role: 'tablist' });
    const list = h('div', { class: 'clone-list' });
    const note = h('p', { class: 'help clone-note' });
    const pick = (name) => { picked = name; dlg.close(); };

    const drawChips = () => chips.replaceChildren(...owners.map((o) => h('button', {
      type: 'button', class: 'clone-owner', role: 'tab', 'aria-selected': String(o === owner),
      onclick: () => { if (o !== owner) { owner = o; showOwner(); } },
    }, o)));

    const rows = () => {
      const all = [...(latest.get(owner) || [])];
      if (found.owner === owner && found.q === searchQuery(q.value, owner)) all.push(...found.repos);
      const seen = new Set();
      return all.filter((r) => !seen.has(r.name) && seen.add(r.name));
    };

    const row = (r) => {
      const i = r.name.indexOf('/');
      const meta = [r.private ? '🔒' : '', r.pushed ? ago(r.pushed) : ''].filter(Boolean).join(' ');
      return h('button', { type: 'button', class: 'clone-row', onclick: () => pick(r.name) },
        h('span', { class: 'clone-text' },
          h('span', { class: 'clone-name' }, h('span', { class: 'clone-org' }, r.name.slice(0, i + 1)), r.name.slice(i + 1)),
          r.description ? h('small', {}, r.description) : null),
        meta ? h('small', { class: 'clone-meta' }, meta) : null);
    };

    const draw = () => {
      const text = q.value.trim(), words = text.toLowerCase().split(/\s+/).filter(Boolean);
      shown = rows().filter((r) => words.every((w) => r.name.toLowerCase().includes(w)));
      const typed = typedRepo(text);
      const direct = typed && !shown.some((r) => r.name.toLowerCase() === typed.toLowerCase())
        ? h('button', { type: 'button', class: 'clone-row clone-direct', onclick: () => pick(typed) }, `Clone ${typed}`) : null;
      list.replaceChildren(...[direct, ...shown.map(row)].filter(Boolean));
      note.textContent = error || (loading ? `Loading ${owner}…`
        : searching ? `Searching ${owner} for “${searching}”…`
          : !text ? (shown.length ? `Latest ${shown.length} pushed. Type to search ${owner}.` : `${owner} has no repositories you can see. An org with SSO needs gh authorized for it.`)
            : !shown.length && !direct ? `No match in ${owner}` : '');
      note.hidden = !note.textContent;
    };

    async function fetchPage(n, o, query) {
      let repos = null;
      try { repos = await api.githubRepos(o, query); } catch (e) { if (o === owner) error = `GitHub: ${e.message}`; }
      if (!dlg.open) return;
      if (!query) {
        if (repos) latest.set(o, repos);
        if (o === owner) loading = false;
      } else {
        if (n !== seq || o !== owner || query !== searchQuery(q.value, owner)) return;
        searching = '';
        if (repos) found = { owner: o, q: query, repos };
      }
      draw();
    }

    function search() {
      const query = searchQuery(q.value, owner);
      if (!query || (found.owner === owner && found.q === query)) return;
      searching = query;
      draw();
      fetchPage(seq, owner, query);
    }

    function showOwner() {
      seq++;
      clearTimeout(timer);
      error = '';
      searching = '';
      drawChips();
      chips.querySelector?.('[aria-selected="true"]')?.scrollIntoView?.({ inline: 'nearest', block: 'nearest' });
      loading = !latest.has(owner);
      if (loading) fetchPage(seq, owner, '');
      search();
      draw();
    }

    q.addEventListener('input', () => {
      // "owner/" opens that owner's chip
      const o = q.value.trim().split('/');
      if (o.length > 1) {
        const hit = owners.find((x) => x.toLowerCase() === o[0].toLowerCase());
        if (hit && hit !== owner) { owner = hit; showOwner(); return; }
      }
      seq++;
      error = '';
      clearTimeout(timer);
      draw();
      if (searchQuery(q.value, owner)) { const n = seq; timer = setTimeout(() => { if (n === seq) search(); }, SEARCH_DELAY); }
    });
    q.addEventListener('keydown', (e) => {
      if (e.key !== 'Enter') return;
      e.preventDefault();
      const typed = typedRepo(q.value);
      const exact = typed && shown.find((r) => r.name.toLowerCase() === typed.toLowerCase());
      if (exact) pick(exact.name);
      else if (typed && !shown.length) pick(typed);
      else if (shown.length) pick(shown[0].name);
    });

    const dlg = h('dialog', { class: 'clone' },
      h('h2', {}, 'Clone from GitHub'), q, chips, list, note,
      h('menu', {}, h('button', { type: 'button', onclick: () => dlg.close() }, 'Cancel')));
    dlg.addEventListener('close', () => {
      clearTimeout(timer);
      dlg.remove();
      if (picked) rememberOwner(picked);
      resolve(picked);
    });
    document.body.append(dlg);
    dlg.showModal();
    showOwner();
  });
}
