import { api } from './api.js';
import { h, icon, topBar, toast, ask, confirmBox, sheet } from './ui.js';

const enc = encodeURIComponent;

// sideOf and lineOf name the line a diff row is: a removed row is on the old side.
export const sideOf = (row) => (row.kind === 'del' ? 'old' : 'new');
export const lineOf = (row) => (row.kind === 'del' ? row.old : row.new);
export const unsentOf = (comments) => comments.filter((c) => !c.sent_at);
// firstOf is a comment's first line; linesOf names its lines: "line 4" or "lines 4–7".
export const firstOf = (c) => c.start_line || c.line;
export const linesOf = (c) => (firstOf(c) < c.line ? `lines ${firstOf(c)}–${c.line}` : `line ${c.line}`);

// matchFilter says whether path holds every word of filter, ignoring case; a word that starts
// with ! must not be in it. Like the TUI's / filter.
export function matchFilter(path, filter) {
  const p = path.toLowerCase();
  return filter.toLowerCase().split(/\s+/).filter(Boolean).every((w) =>
    (w.startsWith('!') ? w.length === 1 || !p.includes(w.slice(1)) : p.includes(w)));
}

// whereOf names t's lines for a form title: "a.go:4–7 (removed lines)".
function whereOf(t) {
  const range = t.start_line && t.start_line < t.line;
  return `${t.file}:${range ? `${t.start_line}–${t.line}` : t.line}${t.side === 'old' ? (range ? ' (removed lines)' : ' (removed line)') : ''}`;
}

// secs is how long ago unix time started was, in seconds.
const secs = (started) => `${Math.max(0, Math.round(Date.now() / 1000 - started))}s`;
// who names the conversation an explanation used.
const who = (e) => (e.session ? `[${e.session}]` : 'code only');

// quoteOf is ui.QuoteCode: code's first max lines without the indent they share, then how many
// more there are.
export function quoteOf(code, max) {
  if (!code || !code.trim()) return [];
  let lines = code.split('\n'), more = 0;
  if (lines.length > max) { more = lines.length - max; lines = lines.slice(0, max); }
  lines = lines.map((l) => l.replace(/[ \t\r]+$/, ''));
  let indent = null;
  for (const l of lines) {
    if (!l) continue;
    const lead = l.match(/^[ \t]*/)[0];
    if (indent === null) indent = lead;
    while (!lead.startsWith(indent)) indent = indent.slice(0, -1);
  }
  lines = lines.map((l) => (indent && l.startsWith(indent) ? l.slice(indent.length) : l));
  if (more) lines.push(`… ${more} more lines`);
  return lines;
}

// rangeOf is the rows lo..hi of a hunk as comment lines: on the old side when they are all
// removed rows, else on the new side without the removed rows. start_line is 0 for one line.
export function rangeOf(rows) {
  const old = rows.every((r) => r.kind === 'del');
  const mine = rows.filter((r) => (r.kind === 'del') === old);
  return { side: old ? 'old' : 'new', start_line: mine.length > 1 ? lineOf(mine[0]) : 0,
    line: lineOf(mine[mine.length - 1]), code: mine.map((r) => r.text).join('\n'), last: mine[mine.length - 1] };
}

// showReview is #/p/{name}/d/{story}: the story's diff by file, tap a line to comment, Send
// pastes the unsent comments into a session of the story (not submitted). The comment form's
// Select more lines starts a range on its line: taps then move its ends inside that hunk, and
// the bar at the bottom comments on it. ✦ Explain, in that form and in the bar, asks Claude about
// the lines: the answer shows in a violet card under the last line, and the page reloads every
// 2 s while one runs.
export function showReview(root, name, story) {
  const meta = h('span', { class: 'ttl-meta' });
  const send = h('button', { class: 'primary', disabled: true, onclick: () => pickSession() }, 'Send');
  const err = h('div', { class: 'error', hidden: true });
  const body = h('div', { class: 'page rv-page' });
  const dockText = h('div', { class: 'rv-dock-text' });
  const dock = h('div', { class: 'rv-dock', hidden: true }, dockText,
    h('button', { onclick: () => { sel = null; render(); } }, 'Cancel'),
    h('button', { class: 'vio', onclick: () => explainAsk(rangeTarget().target) }, '✦ Explain'),
    h('button', { class: 'primary', onclick: () => commentRange() }, 'Comment'));
  // the filter stays outside body, so a redraw doesn't take its focus; typing only hides cards
  const filter = h('input', { class: 'rv-filter', type: 'search', placeholder: 'Filter files (!word leaves out)',
    'aria-label': 'Filter files', oninput: () => showFiles() });
  root.append(topBar(h('a', { href: `#/p/${enc(name)}`, class: 'ib', 'aria-label': 'Back' }, icon('back')),
    h('span', { class: 'ttl-name' }, story), meta, send), err, h('div', { class: 'page rv-filter-bar' }, filter), body, dock);
  const closed = new Set(); // files folded by a tap on their header
  const opened = new Set(); // viewed files unfolded by a tap
  let hideViewed = false, fileEls = [];
  let data = null, stopped = false;
  let poll = null; // the timer of the next reload while an explanation runs
  // the running seconds of the cards tick on their own, without a redraw
  const tick = setInterval(() => document.querySelectorAll('.rv-ex-t').forEach((el) => { el.textContent = secs(Number(el.dataset.started)); }), 1000);
  const explains = () => data.explains || [];
  // sel is the range being picked: rows lo..hi of hunk `hunk` of file `path`, and the comment
  // typed before. rowEls are the drawn rows, so a tap repaints them without a redraw (which
  // would lose the hunks' sideways scroll).
  let sel = null, rowEls = [];

  async function load() {
    try {
      const d = await api.review(name, story);
      if (stopped) return;
      data = d;
      err.hidden = true;
      sel = null;
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
    rowEls = [];
    fileEls = [];
    const viewed = data.files.filter((f) => f.viewed === 'viewed').length;
    // replaceChildren neither skips null nor flattens arrays (it would print "null" and
    // "[object HTMLElement],…"), so drop the empty slots and spread the file cards.
    body.replaceChildren(...[
      data.note ? h('div', { class: 'warn rv-note' }, data.note) : null,
      h('div', { class: 'rv-tools' }, h('button', { onclick: load }, 'Refresh'),
        sent ? h('button', { onclick: () => clearSent(sent) }, `Clear ${sent} sent`) : null,
        viewed ? h('button', { onclick: () => { hideViewed = !hideViewed; render(); } },
          hideViewed ? `Show ${viewed} viewed` : `Hide ${viewed} viewed`) : null),
      data.files.length ? null : h('p', { class: 'help' }, `No changes since ${data.base || 'HEAD'}.`),
      ...data.files.map(file)].filter(Boolean));
    showFiles();
    paint();
    schedulePoll();
  }

  // schedulePoll reloads the review in 2 s while an explanation runs. It redraws only when
  // something changed, and not while a range is being picked: Cancel and save redraw anyway.
  function schedulePoll() {
    if (stopped || poll || !explains().some((e) => e.state === 'running')) return;
    poll = setTimeout(async () => {
      poll = null;
      let d;
      try { d = await api.review(name, story); } catch { schedulePoll(); return; }
      if (stopped) return;
      const sig = (x) => JSON.stringify([x.version, x.comments, (x.explains || []).map((e) => [e.id, e.state])]);
      const changed = sig(d) !== sig(data);
      data = d;
      if (changed && !sel) render(); else schedulePoll();
    }, 2000);
  }

  // paint shows the range being picked, or none: its rows, the rows it can't take, the bar.
  function paint() {
    for (const r of rowEls) {
      const here = sel && r.path === sel.path && r.hunk === sel.hunk;
      r.el.classList.toggle('rv-out', !!sel && !here);
      if (r.idx >= 0) r.el.classList.toggle('rv-sel', !!here && r.idx >= sel.lo && r.idx <= sel.hi);
    }
    body.classList.toggle('selecting', !!sel);
    dock.hidden = !sel;
    if (!sel) return;
    const t = rangeOf(sel.rows.slice(sel.lo, sel.hi + 1));
    const n = t.start_line ? t.line - t.start_line + 1 : 1;
    dockText.replaceChildren(h('b', {}, t.start_line ? `Lines ${t.start_line}–${t.line}` : `Line ${t.line}`),
      h('span', {}, n > 1 ? `${n} lines · tap a line to move an end` : 'Tap the last line of the range'));
  }

  // pick is a tap on row idx while picking: above the range moves its start, below its end,
  // inside shrinks it to that row.
  function pick(idx) {
    if (idx < sel.lo) sel.lo = idx;
    else if (idx > sel.hi) sel.hi = idx;
    else sel.lo = sel.hi = idx;
    paint();
  }

  // rangeTarget is the lines being picked, and their unsent comment when it is one line.
  function rangeTarget() {
    const f = data.files.find((x) => x.path === sel.path);
    const t = rangeOf(sel.rows.slice(sel.lo, sel.hi + 1));
    const target = { file: sel.path, side: t.side, start_line: t.start_line, line: t.line, code: t.code };
    let existing = null;
    if (!t.start_line) { // one line: like a tap on it
      target.code = t.last.text;
      existing = unsentOf(data.comments).find((c) => c.file === f.path && !c.outdated && c.side === t.side && c.line === t.line);
    }
    return { target, existing };
  }

  function commentRange() {
    const { target, existing } = rangeTarget();
    compose(target, existing, null, sel.draft);
  }

  // showFiles hides the file cards the filter or Hide viewed leave out.
  function showFiles() {
    for (const { f, el } of fileEls) el.hidden = !matchFilter(f.path, filter.value) || (hideViewed && f.viewed === 'viewed');
  }

  // markViewed marks a file viewed at the diff it was shown with, or unmarks it.
  async function markViewed(f) {
    const viewed = f.viewed !== 'viewed';
    try {
      data = await api.markViewed(name, story, { path: f.path, hash: f.hash, viewed });
      opened.delete(f.path);
      render();
    } catch (e) { toast(e.message); }
  }

  function file(f) {
    const mine = data.comments.filter((c) => c.file === f.path);
    const isViewed = f.viewed === 'viewed';
    const open = isViewed ? opened.has(f.path) : !closed.has(f.path);
    const fold = () => {
      const set = isViewed ? opened : closed;
      if (set.has(f.path)) set.delete(f.path); else set.add(f.path);
      render();
    };
    const head = h('div', { class: isViewed ? 'rv-head viewed' : 'rv-head' },
      h('button', { class: 'rv-file', onclick: fold },
        h('span', { class: `rv-st rv-${f.status}` }, f.status[0].toUpperCase()),
        h('span', { class: 'rv-path' }, f.old ? `${f.old} → ${f.path}` : f.path),
        h('span', { class: 'diff' }, h('span', { class: 'add' }, `+${f.add}`), ' ', h('span', { class: 'del' }, `−${f.del}`)),
        mine.length ? h('span', { class: 'badge' }, String(mine.length)) : null),
      f.hash ? h('button', { class: isViewed ? 'rv-viewed on' : 'rv-viewed', 'aria-pressed': isViewed ? 'true' : 'false',
        onclick: () => markViewed(f) }, isViewed ? '✓ Viewed' : 'Viewed') : null);
    const card = (...c) => {
      const el = h('section', { class: 'card rv' }, ...c);
      fileEls.push({ f, el });
      return el;
    };
    if (!open) return card(head);
    const pinned = f.folded ? mine : mine.filter((c) => c.outdated);
    const exs = explains().filter((e) => e.file === f.path);
    return card(head,
      f.viewed === 'changed' ? h('div', { class: 'help rv-changed' }, '◐ Changed since you viewed it') : null,
      pinned.map((c) => bubble(c, true)),
      (f.folded ? exs : exs.filter((e) => e.outdated)).map(explainCard),
      f.folded ? h('div', { class: 'help rv-folded' }, f.folded)
        : h('div', { class: 'rv-hunks' }, f.hunks.map((hk, hi) => {
          const head = h('div', { class: 'rv-hunk' }, hk.header);
          rowEls.push({ el: head, path: f.path, hunk: hi, idx: -1 });
          return [head, hk.rows.map((r, idx) => row(f, hk, hi, idx, r, mine))];
        })));
  }

  function row(f, hk, hi, idx, r, mine) {
    const side = sideOf(r), line = lineOf(r);
    const here = mine.filter((c) => !c.outdated && c.side === side && c.line === line);
    const covered = mine.some((c) => !c.outdated && !c.sent_at && firstOf(c) < c.line && c.side === side && line >= firstOf(c) && line <= c.line);
    const target = { file: f.path, side, line, code: r.text };
    const sign = r.kind === 'add' ? '+' : r.kind === 'del' ? '-' : ' ';
    const at = { path: f.path, hunk: hi, rows: hk.rows, idx };
    const tap = () => {
      if (!sel) compose(target, here.find((c) => !c.sent_at), at);
      else if (sel.path === f.path && sel.hunk === hi) pick(idx);
    };
    const el = h('div', { class: `rv-row rv-${r.kind}${covered ? ' rv-cov' : ''}`, onclick: tap },
      h('span', { class: 'rv-n' }, String(line)), h('span', { class: 'rv-code' }, sign + r.text));
    rowEls.push({ el, path: f.path, hunk: hi, idx });
    const exs = explains().filter((e) => e.file === f.path && !e.outdated && e.side === side && e.line === line);
    return [el, here.map((c) => bubble(c, false)), exs.map(explainCard)];
  }

  // explainCard is an explanation under its last line, or pinned at the top of its file: running
  // (with Stop), done (the answer clipped, Read all, Dismiss) or failed.
  function explainCard(e) {
    const lines = e.outdated ? `outdated · ${linesOf(e)}` : linesOf(e);
    if (e.state === 'running') {
      return h('div', { class: 'rv-ex' }, h('small', {}, h('b', {}, '✦ explain'), ' ', lines, ' · ', who(e)),
        h('div', {}, h('span', { class: 'rv-spin' }), ' Explaining… ', h('span', { class: 'rv-ex-t', 'data-started': e.started }, secs(e.started))),
        h('div', { class: 'rv-ex-acts' }, h('button', { onclick: () => dismiss(e) }, 'Stop')));
    }
    if (e.state === 'failed') {
      return h('div', { class: 'rv-ex' }, h('small', {}, h('b', {}, '✦ explain failed'), ' ', lines, ' · ', who(e)),
        h('div', {}, e.error), h('div', { class: 'rv-ex-acts' }, h('button', { onclick: () => dismiss(e) }, 'Dismiss')));
    }
    const text = h('div', { class: 'rv-ex-body clip' }, e.answer);
    let open = false;
    const read = h('button', { onclick: () => { open = !open; text.classList.toggle('clip', !open); read.textContent = open ? 'Show less' : 'Read all'; } }, 'Read all');
    return h('div', { class: 'rv-ex' },
      h('small', {}, h('b', {}, '✦ explain'), ' ', lines, ' · ', who(e), e.took_ms ? ` · ${Math.round(e.took_ms / 1000)}s` : ''),
      text, h('div', { class: 'rv-ex-acts' }, read, h('button', { onclick: () => dismiss(e) }, 'Dismiss')));
  }

  // dismiss stops a running explanation or drops an answer.
  async function dismiss(e) {
    try { data = await api.deleteExplain(name, story, e.id); render(); } catch (x) { toast(x.message); }
  }

  // bubble is a comment under its line, or pinned at the top of its file. A tap edits an unsent
  // one; a sent one is read-only, so a tap starts a new comment on its line.
  function bubble(c, pinned) {
    const sent = !!c.sent_at;
    const range = firstOf(c) < c.line;
    const label = [sent ? `✓ sent → [${c.sent_to}]` : '', pinned ? `${c.outdated ? 'outdated · ' : ''}${linesOf(c)}` : range ? linesOf(c) : '']
      .filter(Boolean).join(' · ');
    const target = { file: c.file, side: c.side, start_line: c.start_line || 0, line: c.line, code: c.code };
    return h('div', { class: sent ? 'rv-bubble sent' : 'rv-bubble', onclick: () => { if (!sel) compose(target, sent ? null : c); } },
      label ? h('small', {}, label) : null, h('div', { class: 'rv-body' }, c.body));
  }

  // compose is the comment form on t's lines. at is the tapped row: a new comment there can
  // grow into a range (Select more lines), which keeps what was typed as draft.
  async function compose(t, existing, at, draft) {
    const range = t.start_line && t.start_line < t.line;
    const quote = quoteOf(t.code, 4);
    const v = await ask(`Comment · ${whereOf(t)}`, [{ name: 'body', label: quote.length ? h('pre', { class: 'rv-quote' }, quote.join('\n')) : 'Comment',
      type: 'textarea', value: existing ? existing.body : draft || '', placeholder: 'Empty deletes it' }], 'Save',
    { extras: [at && !existing ? { label: '⇕ Select more lines', value: 'select' } : null,
      { label: '✦ Explain', value: 'explain', class: 'vio' }].filter(Boolean) });
    if (v === null) return;
    if (v.$extra === 'explain') { explainAsk(t); return; }
    if (v.$extra === 'select') {
      sel = { path: at.path, hunk: at.hunk, rows: at.rows, lo: at.idx, hi: at.idx, draft: v.body };
      paint();
      return;
    }
    let comments;
    if (existing) {
      comments = v.body ? data.comments.map((c) => (c.id === existing.id ? { ...c, body: v.body } : c))
        : data.comments.filter((c) => c.id !== existing.id);
    } else if (v.body) {
      comments = [...data.comments, { file: t.file, side: t.side, line: t.line, code: t.code, body: v.body,
        ...(range ? { start_line: t.start_line } : {}) }];
    } else return;
    try {
      data = await api.saveReview(name, story, { version: data.version, comments });
      sel = null;
      render();
    } catch (e) {
      toast(e.status === 409 ? 'Comments changed on another device · reloaded' : e.message);
      if (e.status === 409) load();
    }
  }

  // explainAsk asks which conversation Claude uses and an optional question about t's lines,
  // then starts the explanation.
  async function explainAsk(t) {
    let snap;
    try { snap = await api.snapshot(name); } catch (e) { toast(e.message); return; }
    const agents = (snap.live && snap.live.agents) || {};
    const rows = Object.entries(snap.project.sessions || {}).filter(([, s]) => s.story === story)
      .map(([k, s]) => [Number(k), s]).sort((a, b) => b[0] - a[0]);
    const options = rows.map(([num, s]) => ({ value: String(num),
      label: `[${num}] ${s.title || 'untitled'} · ${!s.done && agents[s.uuid] ? 'running' : 'done'}` }));
    options.push({ value: '-1', label: 'Code only (no conversation)' });
    const last = String(data.explain_with || '');
    const quote = quoteOf(t.code, 4);
    const v = await ask(`Explain · ${whereOf(t)}`, [
      { name: 'question', type: 'textarea', label: quote.length ? h('pre', { class: 'rv-quote' }, quote.join('\n')) : 'Question',
        placeholder: 'Optional. Empty asks what it does and why.' },
      { name: 'session', type: 'select', label: 'Conversation', options, value: options.some((o) => o.value === last) ? last : options[0].value }], 'Explain');
    if (v === null) return;
    try {
      data = await api.explain(name, story, { file: t.file, side: t.side, line: t.line, code: t.code, question: v.question,
        session: Number(v.session), ...(t.start_line && t.start_line < t.line ? { start_line: t.start_line } : {}) });
      sel = null;
      render();
    } catch (e) {
      toast(e.message);
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
  return { stop() { stopped = true; clearTimeout(poll); clearInterval(tick); } };
}
