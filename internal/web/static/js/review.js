import { api } from './api.js';
import { h, icon, topBar, toast, ask, confirmBox, sheet } from './ui.js';

const enc = encodeURIComponent;

// sideOf and lineOf name the line a diff row is: a removed row is on the old side.
export const sideOf = (row) => (row.kind === 'del' ? 'old' : 'new');
export const lineOf = (row) => (row.kind === 'del' ? row.old : row.new);
export const unsentOf = (comments) => comments.filter((c) => !c.sent_at);

// showReview is #/p/{name}/d/{story}: the story's diff by file, tap a line to comment, Send
// pastes the unsent comments into a session of the story (not submitted).
export function showReview(root, name, story) {
  const meta = h('span', { class: 'ttl-meta' });
  const send = h('button', { class: 'primary', disabled: true, onclick: () => pickSession() }, 'Send');
  const err = h('div', { class: 'error', hidden: true });
  const body = h('div', { class: 'page rv-page' });
  root.append(topBar(h('a', { href: `#/p/${enc(name)}`, class: 'ib', 'aria-label': 'Back' }, icon('back')),
    h('span', { class: 'ttl-name' }, story), meta, send), err, body);
  const closed = new Set(); // files folded by a tap on their header
  let data = null, stopped = false;

  async function load() {
    try {
      const d = await api.review(name, story);
      if (stopped) return;
      data = d;
      err.hidden = true;
      render();
    } catch (e) {
      if (stopped) return;
      if (e.status === 404) { toast(e.message); location.hash = `#/p/${enc(name)}`; return; }
      err.textContent = e.message;
      err.hidden = false;
    }
  }

  function render() {
    const unsent = unsentOf(data.comments).length, sent = data.comments.length - unsent;
    const add = data.files.reduce((n, f) => n + f.add, 0), del = data.files.reduce((n, f) => n + f.del, 0);
    meta.replaceChildren(`vs ${data.base || 'HEAD'} · ${data.total} ${data.total === 1 ? 'file' : 'files'} · `,
      h('span', { class: 'add' }, `+${add}`), ' ', h('span', { class: 'del' }, `−${del}`), sent ? ` · ${sent} sent` : '');
    send.textContent = unsent ? `Send ${unsent}` : 'Send';
    send.disabled = !unsent;
    body.replaceChildren(
      data.note ? h('div', { class: 'warn rv-note' }, data.note) : null,
      h('div', { class: 'rv-tools' }, h('button', { onclick: load }, 'Refresh'),
        sent ? h('button', { onclick: () => clearSent(sent) }, `Clear ${sent} sent`) : null),
      data.files.length ? null : h('p', { class: 'help' }, `No changes since ${data.base || 'HEAD'}.`),
      data.files.map(file));
  }

  function file(f) {
    const mine = data.comments.filter((c) => c.file === f.path);
    const open = !closed.has(f.path);
    const head = h('button', { class: 'rv-file', onclick: () => { if (open) closed.add(f.path); else closed.delete(f.path); render(); } },
      h('span', { class: `rv-st rv-${f.status}` }, f.status[0].toUpperCase()),
      h('span', { class: 'rv-path' }, f.old ? `${f.old} → ${f.path}` : f.path),
      h('span', { class: 'diff' }, h('span', { class: 'add' }, `+${f.add}`), ' ', h('span', { class: 'del' }, `−${f.del}`)),
      mine.length ? h('span', { class: 'badge' }, String(mine.length)) : null);
    if (!open) return h('section', { class: 'card rv' }, head);
    const pinned = f.folded ? mine : mine.filter((c) => c.outdated);
    return h('section', { class: 'card rv' }, head,
      pinned.map((c) => bubble(c, true)),
      f.folded ? h('div', { class: 'help rv-folded' }, f.folded)
        : h('div', { class: 'rv-hunks' }, f.hunks.map((hk) => [h('div', { class: 'rv-hunk' }, hk.header), hk.rows.map((r) => row(f, r, mine))])));
  }

  function row(f, r, mine) {
    const side = sideOf(r), line = lineOf(r);
    const here = mine.filter((c) => !c.outdated && c.side === side && c.line === line);
    const target = { file: f.path, side, line, code: r.text };
    const sign = r.kind === 'add' ? '+' : r.kind === 'del' ? '-' : ' ';
    return [h('div', { class: `rv-row rv-${r.kind}`, onclick: () => compose(target, here.find((c) => !c.sent_at)) },
      h('span', { class: 'rv-n' }, String(line)), h('span', { class: 'rv-code' }, sign + r.text)),
    here.map((c) => bubble(c, false))];
  }

  // bubble is a comment under its line, or pinned at the top of its file. A tap edits an unsent
  // one; a sent one is read-only, so a tap starts a new comment on its line.
  function bubble(c, pinned) {
    const sent = !!c.sent_at;
    const label = [sent ? `✓ sent → [${c.sent_to}]` : '', pinned ? `${c.outdated ? 'outdated · ' : ''}line ${c.line}` : '']
      .filter(Boolean).join(' · ');
    const target = { file: c.file, side: c.side, line: c.line, code: c.code };
    return h('div', { class: sent ? 'rv-bubble sent' : 'rv-bubble', onclick: () => compose(target, sent ? null : c) },
      label ? h('small', {}, label) : null, h('div', { class: 'rv-body' }, c.body));
  }

  async function compose(t, existing) {
    const where = `${t.file}:${t.line}${t.side === 'old' ? ' (removed line)' : ''}`;
    const v = await ask(`Comment · ${where}`, [{ name: 'body', label: t.code ? `> ${t.code.trim()}` : 'Comment',
      type: 'textarea', value: existing ? existing.body : '', placeholder: 'Empty deletes it' }], 'Save');
    if (v === null) return;
    let comments;
    if (existing) {
      comments = v.body ? data.comments.map((c) => (c.id === existing.id ? { ...c, body: v.body } : c))
        : data.comments.filter((c) => c.id !== existing.id);
    } else if (v.body) {
      comments = [...data.comments, { file: t.file, side: t.side, line: t.line, code: t.code, body: v.body }];
    } else return;
    try {
      data = await api.saveReview(name, story, { version: data.version, comments });
      render();
    } catch (e) {
      toast(e.status === 409 ? 'Comments changed on another device · reloaded' : e.message);
      if (e.status === 409) load();
    }
  }

  async function pickSession() {
    let snap;
    try { snap = await api.snapshot(name); } catch (e) { toast(e.message); return; }
    const n = unsentOf(data.comments).length, sent = data.comments.length - n;
    const agents = (snap.live && snap.live.agents) || {};
    const rows = Object.entries(snap.project.sessions || {}).filter(([, s]) => s.story === story)
      .map(([k, s]) => [Number(k), s]).sort((a, b) => b[0] - a[0]);
    const items = rows.map(([num, s]) => ({ label: `[${num}] ${s.title || 'untitled'}`,
      hint: !s.done && agents[s.uuid] ? 'running' : 'done · resumes', run: () => sendTo({ session: num }) }));
    items.push({ label: '+ New session', icon: 'plus', run: () => sendTo({ new: true }) });
    sheet(`Send ${n} ${n === 1 ? 'comment' : 'comments'}`, items,
      { meta: `Pasted into Claude's input, not submitted${sent ? ` · ${sent} already sent, not included` : ''}` });
  }

  async function sendTo(req) {
    toast(req.new ? 'Starting a session…' : `Sending to [${req.session}]…`);
    try {
      const r = await api.sendReview(name, story, req);
      toast(r.message, { label: 'Open', run: () => { location.hash = `#/p/${enc(name)}/s/${r.num}`; } });
    } catch (e) {
      toast(e.message);
    }
    if (!stopped) load();
  }

  async function clearSent(n) {
    if (!(await confirmBox(`Clear ${n} sent ${n === 1 ? 'comment' : 'comments'}?`, 'Clear',
      { text: 'They leave the review for good. Unsent comments stay.' }))) return;
    try { data = await api.clearSent(name, story); render(); } catch (e) { toast(e.message); }
  }

  load();
  return { stop() { stopped = true; } };
}
