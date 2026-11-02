// DOM helpers. Text always goes in as text nodes (never innerHTML): titles come from prompts.
export function h(tag, props = {}, ...children) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(props)) {
    if (k.startsWith('on')) el.addEventListener(k.slice(2), v);
    else if (k === 'class') el.className = v;
    else if (v === true) el.setAttribute(k, '');
    else if (v !== false && v != null) el.setAttribute(k, v);
  }
  for (const c of children.flat(Infinity)) {
    if (c != null && c !== false) el.append(c instanceof Node ? c : String(c));
  }
  return el;
}

export const bar = (left, title, right) =>
  h('header', { class: 'bar' }, left, h('h1', {}, title), right || h('span', { class: 'icon' }));

// An open modal dialog makes the rest of the page inert and sits above it, so elements that
// must work while one is open go inside it.
const topEl = () => document.querySelector('dialog[open]') || document.body;
export function toast(msg) {
  const t = h('div', { class: 'toast' }, msg);
  topEl().append(t);
  setTimeout(() => t.remove(), 3000);
}
// copyText puts text on the clipboard and returns whether it worked (a promise of it in a
// secure context). Call it synchronously inside a tap. Pattern from m-terminal's clipboard.ts:
// navigator.clipboard only exists in a secure context (https or localhost), so over plain http
// it falls back to a hidden textarea and document.execCommand('copy'). A rejected writeText
// gets no fallback: it rejects after the tap, and execCommand outside a tap fails too.
export function copyText(text) {
  if (window.isSecureContext && navigator.clipboard?.writeText) {
    try {
      return navigator.clipboard.writeText(text).then(() => true, () => false);
    } catch { /* fall through */ }
  }
  return legacyCopy(text);
}
function legacyCopy(text) {
  const restore = document.activeElement;
  // off-screen but selectable (display:none can't be selected); readonly keeps the iOS keyboard shut
  const ta = h('textarea', { readonly: true });
  ta.value = text;
  ta.style.cssText = 'position:fixed;top:0;left:-9999px;opacity:0;pointer-events:none';
  topEl().append(ta);
  let ok = false;
  try {
    ta.select();
    ta.setSelectionRange(0, text.length); // iOS ignores select() alone
    ok = document.execCommand('copy');
  } catch { ok = false; }
  ta.remove();
  restore?.focus?.();
  return ok;
}

// poll runs fn now and every ms while the page is visible, and at once when it becomes visible.
// It returns stop().
export function poll(fn, ms = 2000) {
  let timer = null, stopped = false, busy = false;
  const tick = async () => {
    clearTimeout(timer);
    if (stopped || busy) return;
    busy = true;
    try { if (!document.hidden) await fn(); } catch { /* fn shows its own errors */ }
    busy = false;
    if (!stopped) timer = setTimeout(tick, ms);
  };
  const onVis = () => { if (!document.hidden) tick(); };
  document.addEventListener('visibilitychange', onVis);
  tick();
  return () => { stopped = true; clearTimeout(timer); document.removeEventListener('visibilitychange', onVis); };
}

// ask shows a form and resolves to {name: value} (checks: an array), or null on Cancel.
// Field: {name, label, type?: 'text'|'select'|'checks', value?, placeholder?, options?, list?, hint?}
// Select options are strings or {value, label}. hint(values) returns a help line shown under the
// field, redrawn as the form changes.
// opts: {danger?: a red OK button, text?: a help line under the title}
export function ask(title, fields, ok = 'OK', opts = {}) {
  return new Promise((resolve) => {
    const inputs = [], hints = [];
    const body = fields.map((f) => {
      let input;
      if (f.type === 'select') {
        input = h('select', {}, f.options.map((o) => {
          const [value, label] = typeof o === 'string' ? [o, o || '(default)'] : [o.value, o.label];
          return h('option', { value, selected: value === f.value }, label);
        }));
      } else if (f.type === 'checks') {
        input = h('div', { class: 'checks' }, f.options.length
          ? f.options.map((o) => h('label', {}, h('input', { type: 'checkbox', value: o, checked: (f.value || []).includes(o) }), o))
          : h('span', { class: 'help' }, 'Nothing to pick'));
      } else {
        const listId = f.list ? `l${Math.random().toString(36).slice(2)}` : null;
        input = h('input', { type: 'text', value: f.value || '', placeholder: f.placeholder || '', list: listId,
          autocapitalize: 'off', autocomplete: 'off', autocorrect: 'off', spellcheck: 'false' });
        if (listId) input = [input, h('datalist', { id: listId }, f.list.map((o) => h('option', { value: o })))];
      }
      inputs.push([f, Array.isArray(input) ? input[0] : input]);
      const hint = f.hint ? h('small', { class: 'help' }) : null;
      if (hint) hints.push([f, hint]);
      return h('div', { class: 'field' }, h('span', {}, f.label), input, hint);
    });
    const read = () => Object.fromEntries(inputs.map(([f, el]) => [f.name, f.type === 'checks'
      ? [...el.querySelectorAll('input:checked')].map((c) => c.value)
      : el.value.trim()]));
    const redraw = () => {
      const v = read();
      for (const [f, el] of hints) el.textContent = f.hint(v) || '';
    };
    // Cancel is type=button: Enter / the keyboard's Go key submits with the first submit button,
    // which must be OK.
    const dlg = h('dialog', {}, h('form', { method: 'dialog', oninput: redraw, onchange: redraw }, h('h2', {}, title),
      opts.text ? h('p', { class: 'help' }, opts.text) : null, body,
      h('menu', {}, h('button', { type: 'button', onclick: () => dlg.close('cancel') }, 'Cancel'),
        h('button', { value: 'ok', class: opts.danger ? 'danger' : 'primary' }, ok))));
    redraw();
    dlg.addEventListener('close', () => {
      const v = dlg.returnValue === 'ok' ? read() : null;
      dlg.remove();
      resolve(v);
    });
    document.body.append(dlg);
    dlg.showModal();
  });
}

// confirmBox asks a yes/no question and resolves to true on OK. opts as for ask.
export const confirmBox = (title, ok, opts) => ask(title, [], ok, opts).then((v) => v !== null);

// ago is ui.Ago: how long ago unix time t was, in the largest whole unit.
export function ago(t) {
  const s = Date.now() / 1000 - t;
  if (s < 60) return 'now';
  if (s < 3600) return `${Math.floor(s / 60)}m`;
  if (s < 86400) return `${Math.floor(s / 3600)}h`;
  return `${Math.floor(s / 86400)}d`;
}

// Stroke icons for sheet rows, 24x24 paths. They are built only when a sheet opens.
const ICONS = {
  doc: 'M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8zM14 3v5h5M9 13h6M9 17h4',
  pen: 'M4 20h4L19 9l-4-4L4 16zM13.5 6.5l4 4',
  stop: 'M8 6h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2z',
  play: 'M8 5l11 7-11 7z',
  trash: 'M4 7h16M10 11v6M14 11v6M6 7l1 13h10l1-13M9 7V4h6v3',
  chev: 'M9 6l6 6-6 6',
};
function icon(name, cls) {
  const ns = 'http://www.w3.org/2000/svg';
  const svg = document.createElementNS(ns, 'svg');
  svg.setAttribute('viewBox', '0 0 24 24');
  svg.setAttribute('class', cls);
  svg.setAttribute('aria-hidden', 'true');
  const path = document.createElementNS(ns, 'path');
  path.setAttribute('d', ICONS[name]);
  svg.append(path);
  return svg;
}

// sheet is a bottom sheet of actions: items [{label, run, danger?, icon?, hint?, badge?,
// chevron?}]. icon names an ICONS entry, hint is a dim second line, badge a count pill and
// chevron says the row opens more. Danger items go in their own group at the bottom, in red.
// opts.meta is a dim line under the title. sheet returns one setter per item,
// set({hint?, badge?}), to fill in a row once its data has loaded.
export function sheet(title, items, opts = {}) {
  const rows = items.map((it) => sheetRow(it, () => { dlg.close(); it.run(); }));
  const group = (keep) => {
    const els = rows.filter((_, i) => !!items[i].danger === keep).map((r) => r.el);
    return els.length ? h('div', { class: 'sheet-group' }, els) : null;
  };
  // the dialog has no padding, so a tap on it (not on .sheet-body) is a tap on the backdrop
  const dlg = h('dialog', { class: 'sheet', onclick: (e) => { if (e.target === dlg) dlg.close(); } },
    h('div', { class: 'sheet-body' },
      h('div', { class: 'sheet-handle' }),
      h('header', { class: 'sheet-head' }, h('h2', {}, title), opts.meta ? h('p', {}, opts.meta) : null),
      group(false), group(true),
      h('button', { class: 'sheet-cancel', onclick: () => dlg.close() }, 'Cancel')));
  dlg.addEventListener('close', () => dlg.remove());
  document.body.append(dlg);
  dlg.showModal();
  return rows.map((r) => r.set);
}

function sheetRow(it, onTap) {
  const hint = h('small', { class: 'sheet-hint', hidden: !it.hint }, it.hint || '');
  const badge = h('span', { class: 'sheet-badge', hidden: it.badge == null }, it.badge ?? '');
  const el = h('button', { class: it.danger ? 'sheet-row is-danger' : 'sheet-row', onclick: onTap },
    it.icon ? icon(it.icon, `sheet-icon icon-${it.icon}`) : null,
    h('span', { class: 'sheet-label' }, h('span', {}, it.label), hint),
    badge, it.chevron ? icon('chev', 'sheet-chev') : null);
  const set = (v) => {
    if ('hint' in v) { hint.textContent = v.hint || ''; hint.hidden = !v.hint; }
    if ('badge' in v) { badge.textContent = v.badge ?? ''; badge.hidden = v.badge == null; }
  };
  return { el, set };
}

// pickFrom lists items [{label, ...}] under a search box that filters them by label, and
// resolves to the tapped item, or null on Cancel. Enter picks the first match. With
// opts.search(q), a search that matches nothing offers a "Search GitHub" button (Enter does
// the same); search resolves to the items to list instead, or {pick: item} to pick at once.
// With opts.direct(q) true (q names one item, like #45), Enter and the button search for it
// instead of picking the first match, which may only contain it (#456).
export function pickFrom(title, items, opts = {}) {
  return new Promise((resolve) => {
    let picked = null, shown = [], seq = 0;
    const head = h('h2', {}, title);
    const q = h('input', { type: 'search', placeholder: 'Filter', enterkeyhint: 'go',
      autocapitalize: 'off', autocomplete: 'off', autocorrect: 'off', spellcheck: 'false' });
    const list = h('div', { class: 'pick-list' });
    const empty = h('p', { class: 'help', hidden: true }, 'Nothing matches');
    const more = h('button', { type: 'button', hidden: true, onclick: () => search() }, 'Search GitHub');
    const pick = (it) => { picked = it; dlg.close(); };
    const isDirect = () => !!(opts.search && opts.direct && opts.direct(q.value.trim()));
    const draw = () => {
      const words = q.value.toLowerCase().split(/\s+/).filter(Boolean);
      shown = items.filter((it) => words.every((w) => it.label.toLowerCase().includes(w)));
      list.replaceChildren(...shown.map((it) => h('button', { type: 'button', onclick: () => pick(it) }, it.label)));
      empty.hidden = shown.length > 0;
      more.hidden = !opts.search || !q.value.trim() || (shown.length > 0 && !isDirect());
    };
    async function search() {
      const text = q.value.trim();
      if (!opts.search || !text) return;
      const n = ++seq;
      more.disabled = true;
      more.textContent = 'Searching…';
      let r = null;
      try { r = await opts.search(text); } finally {
        more.disabled = false;
        more.textContent = 'Search GitHub';
      }
      if (n !== seq || !dlg.open || !r) return;
      if (!Array.isArray(r)) { pick(r.pick); return; }
      if (!r.length) return;
      items = r;
      head.textContent = `Search: ${text}`;
      q.value = '';
      draw();
    }
    q.addEventListener('input', draw);
    q.addEventListener('keydown', (e) => {
      if (e.key !== 'Enter') return;
      e.preventDefault();
      if (shown.length && !isDirect()) pick(shown[0]);
      else search();
    });
    const dlg = h('dialog', { class: 'pick' }, head, q, list, empty, more,
      h('menu', {}, h('button', { type: 'button', onclick: () => dlg.close() }, 'Cancel')));
    dlg.addEventListener('close', () => { dlg.remove(); resolve(picked); });
    draw();
    document.body.append(dlg);
    dlg.showModal();
  });
}
