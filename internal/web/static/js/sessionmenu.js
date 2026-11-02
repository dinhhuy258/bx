import { api } from './api.js';
import { confirmBox, sheet, toast } from './ui.js';

// withForce runs an action that the server may refuse with 409 (uncommitted files, or a
// session that is still open) and offers to resend it with force. message(result) builds the
// question; without it the question lists the uncommitted files. It resolves to true when the
// action went through, false otherwise; errors are toasted.
export async function withForce(project, action, body, what, { message, ok = 'Continue', danger = false } = {}) {
  try {
    await api.action(project, action, body);
    return true;
  } catch (e) {
    if (e.status !== 409 || !e.result) { toast(e.message); return false; }
    const detail = e.result.detail ? ` (${e.result.detail})` : '';
    const text = message ? message(e.result) : `${what}: ${e.result.dirty} uncommitted files${detail}. Continue anyway?`;
    if (!(await confirmBox(text, ok, { danger }))) return false;
    try {
      await api.action(project, action, { ...body, force: true });
      return true;
    } catch (e2) {
      toast(e2.message);
      return false;
    }
  }
}

// sessionMenu opens the actions sheet for session num, from the project list or the session
// screen. The screen passes in everything the menu needs: act(action, body) runs an action and
// toasts errors, refresh reloads the screen, ask is ui.ask, withForce(action, body, what, opts)
// is withForce bound to the project, and onDeleted runs after a delete went through.
export function sessionMenu({ project, num, session, state, act, refresh, ask, withForce, onDeleted }) {
  const title = session.title || `#${num}`;
  const items = [];
  if (['starting', 'running', 'waiting', 'idle'].includes(state)) {
    items.push({ label: 'Stop', run: async () => {
      if (await confirmBox(`Stop ${title}?`, 'Stop', { text: 'Claude exits; Resume picks it up again.' })) act('stop', { num });
    } });
  }
  if (state === 'gone' || state === 'done') items.push({ label: 'Resume', run: () => act('resume', { num }) });
  items.push({ label: 'Rename', run: async () => {
    const v = await ask('Rename', [{ name: 'title', label: 'Title', value: session.title }], 'Save');
    if (v && v.title) act('rename', { num, title: v.title });
  } });
  items.push({ label: 'Delete', danger: true, run: async () => {
    if (!(await confirmBox(`Delete ${title}?`, 'Delete',
      { danger: true, text: 'bx forgets it. The Claude transcript in ~/.claude is kept.' }))) return;
    try {
      if (await withForce('delete', { num }, title, {
        message: () => `${title} is still open. Delete stops Claude first. Delete anyway?`, ok: 'Delete', danger: true })) {
        onDeleted?.();
      }
    } finally {
      refresh?.();
    }
  } });
  sheet(title, items);
}
