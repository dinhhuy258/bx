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

export function toast(msg) {
  const t = h('div', { class: 'toast' }, msg);
  document.body.append(t);
  setTimeout(() => t.remove(), 3000);
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
// Field: {name, label, type?: 'text'|'select'|'checks', value?, placeholder?, options?, list?}
export function ask(title, fields, ok = 'OK') {
  return new Promise((resolve) => {
    const inputs = [];
    const body = fields.map((f) => {
      let input;
      if (f.type === 'select') {
        input = h('select', {}, f.options.map((o) => h('option', { value: o, selected: o === f.value }, o || '(default)')));
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
      return h('div', { class: 'field' }, h('span', {}, f.label), input);
    });
    const read = () => Object.fromEntries(inputs.map(([f, el]) => [f.name, f.type === 'checks'
      ? [...el.querySelectorAll('input:checked')].map((c) => c.value)
      : el.value.trim()]));
    // Cancel is type=button: Enter / the keyboard's Go key submits with the first submit button,
    // which must be OK.
    const dlg = h('dialog', {}, h('form', { method: 'dialog' }, h('h2', {}, title), body,
      h('menu', {}, h('button', { type: 'button', onclick: () => dlg.close('cancel') }, 'Cancel'),
        h('button', { value: 'ok', class: 'primary' }, ok))));
    dlg.addEventListener('close', () => {
      const v = dlg.returnValue === 'ok' ? read() : null;
      dlg.remove();
      resolve(v);
    });
    document.body.append(dlg);
    dlg.showModal();
  });
}

export const confirmBox = (title, ok) => ask(title, [], ok).then((v) => v !== null);

// sheet is a list of actions: items [{label, run}].
export function sheet(title, items) {
  const dlg = h('dialog', { class: 'sheet' }, h('h2', {}, title),
    items.map((it) => h('button', { onclick: () => { dlg.close(); it.run(); } }, it.label)),
    h('button', { onclick: () => dlg.close() }, 'Cancel'));
  dlg.addEventListener('close', () => dlg.remove());
  document.body.append(dlg);
  dlg.showModal();
}
