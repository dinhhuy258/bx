import { api } from './api.js';
import { h, bar, toast } from './ui.js';

// Drawn from GET /api/settings. A choice saves on change; "" is the program's default, or the
// field's default when it has one.
export function showSettings(root) {
  const body = h('div');
  root.append(bar(h('a', { href: '#/', class: 'icon', 'aria-label': 'Back' }, '‹'), 'Settings'), body);
  const load = () => api.settings().then(render).catch((e) => toast(e.message));

  function render(s) {
    body.replaceChildren(...(s.sections || []).map((sec) => h('section', { class: 'group' },
      h('header', {}, h('span', { class: 'title' }, sec.title)),
      sec.help ? h('p', { class: 'help' }, sec.help) : null,
      h('div', { style: 'padding: 0 14px' }, sec.fields.map((f) => {
        const sel = h('select', { onchange: () => save(f.key, sel.value) },
          (f.choices || []).map((c) => h('option', { value: c, selected: (s.values[f.key] || f.default || '') === c }, c || '(default)')));
        return h('label', { class: 'field' }, h('span', {}, f.label), sel, f.help ? h('small', {}, f.help) : null);
      })))));
  }

  async function save(key, value) {
    try {
      render(await api.saveSettings({ [key]: value }));
      toast('Saved');
    } catch (e) {
      toast(e.message);
      load();
    }
  }

  load();
  return { stop() {} };
}
