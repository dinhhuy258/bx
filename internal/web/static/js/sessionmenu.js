import { api } from './api.js';
import { ago, confirmBox, sheet, toast } from './ui.js';

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
// The menu opens at once; the Artifacts row fills in its count and newest page when the list
// has loaded (the server sends the files newest first).
export function sessionMenu({ project, num, session, state, act, refresh, ask, withForce, onDeleted }) {
  const title = session.title || `#${num}`;
  let files = null;
  const load = api.artifacts(project, num).then((r) => { files = r.files; return files; });
  const items = [{ label: 'Artifacts', icon: 'doc', hint: 'Loading…', chevron: true, run: async () => {
    try {
      if (!files) await load;
    } catch (e) {
      toast(e.message);
      return;
    }
    if (!files.length) { toast('No artifacts yet. Ask Claude for a page.'); return; }
    // the tap on a row opens the page: window.open runs inside that click, so it is not blocked
    sheet('Artifacts', files.map((f) => ({ label: f.name, icon: 'doc', hint: agoText(f.modified), run: () => {
      if (f.url) window.open(f.url, '_blank', 'noopener');
      else toast('The file server is off (serve.files_listen = "off").');
    } })), { meta: title });
  } }];
  items.push({ label: 'Rename', icon: 'pen', run: async () => {
    const v = await ask('Rename', [{ name: 'title', label: 'Title', value: session.title }], 'Save');
    if (v && v.title) act('rename', { num, title: v.title });
  } });
  if (['starting', 'running', 'waiting', 'idle'].includes(state)) {
    items.push({ label: 'Stop', icon: 'stop', hint: 'Claude exits; Resume picks it up', run: async () => {
      if (await confirmBox(`Stop ${title}?`, 'Stop', { text: 'Claude exits; Resume picks it up again.' })) act('stop', { num });
    } });
  }
  if (state === 'gone' || state === 'done') {
    items.push({ label: 'Resume', icon: 'play', hint: 'Start Claude again where it left off', run: () => act('resume', { num }) });
  }
  items.push({ label: 'Delete session', icon: 'trash', hint: 'Removes its uploads and artifacts', danger: true, run: async () => {
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
  const [setArtifacts] = sheet(title, items, { meta: `${state} · #${num}` });
  load.then(
    (f) => setArtifacts(f.length ? { hint: `${f[0].name} · ${agoText(f[0].modified)}`, badge: f.length } : { hint: 'None yet' }),
    () => setArtifacts({ hint: '' }));
}

// agoText turns an artifact's modified time (RFC 3339) into "2m ago" or "just now".
function agoText(modified) {
  const a = ago(Date.parse(modified) / 1000);
  return a === 'now' ? 'just now' : `${a} ago`;
}
