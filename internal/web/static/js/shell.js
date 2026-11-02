import { api } from './api.js';
import { h, ask, confirmBox, copyText, sheet, toast } from './ui.js';

// Shells of a story ('' = Chats, the project folder) live in bx's tmux server; the screen itself
// is session.js's showShell. This module holds the shell-only pieces: links, labels, the
// new-shell confirm, the ⋯ sheet and the closed card.

const enc = encodeURIComponent;
const PLAIN = ['zsh', 'bash', 'sh', 'fish'];

// shellHref is the route of shell k of a story; Chats is '_' so the path has no empty part.
export const shellHref = (project, story, k) => `#/p/${enc(project)}/sh/${story ? enc(story) : '_'}/${k}`;

// plainShell reports whether cmd is a bare shell (a login shell has a leading '-'), i.e. nothing
// runs in it that a close would kill.
export const plainShell = (cmd) => PLAIN.includes(String(cmd || '').replace(/^-/, ''));

// shellLabel is the rename, else the running command, else 'shell'.
export const shellLabel = (sh) => sh.title || sh.command || 'shell';

// findShell is shell k of a story in a project snapshot, or undefined when it is gone.
export const findShell = (snap, story, k) => (snap.shells || []).find((s) => s.story === story && s.k === k);

// groupName names a story for people: 'Chats' for the project folder.
export const groupName = (story) => story || 'Chats';

// nextShellK is the lowest number not taken by the story's shells, the one the server will pick.
export function nextShellK(snap, story) {
  const used = new Set((snap.shells || []).filter((s) => s.story === story).map((s) => s.k));
  let k = 1;
  while (used.has(k)) k++;
  return k;
}

// confirmNewShell asks before opening a shell in the story's folder and resolves to the server's
// number for it, or null when cancelled or refused (the error is toasted).
export async function confirmNewShell(project, snap, story) {
  const k = nextShellK(snap, story);
  const dir = (snap.dirs || {})[story] || '';
  if (!(await confirmBox(`Open new shell $${k}?`, 'Open', { text: `in ${groupName(story)}${dir ? ` · ${dir}` : ''}` }))) return null;
  try {
    return (await api.newShell(project, story)).k || null;
  } catch (e) {
    toast(e.message);
    return null;
  }
}

// openShell shows the shell screen. The lowest free number can be the one on screen (New shell
// from its closed card), which leaves the hash as it is, so route again by hand.
export function openShell(project, story, k) {
  const href = shellHref(project, story, k);
  if (location.hash === href) window.dispatchEvent(new HashChangeEvent('hashchange'));
  else location.hash = href;
}

// shellMenu opens the ⋯ sheet of shell sh: Rename, Copy path (only with dir) and Close shell.
// refresh reloads the caller's screen; onClosed runs after a close went through.
export function shellMenu({ project, sh, dir, refresh, onClosed }) {
  const name = `$${sh.k} ${shellLabel(sh)}`;
  const items = [{ label: 'Rename', run: async () => {
    const v = await ask('Rename shell', [{ name: 'title', label: 'Name (empty = the command)', value: sh.title || '' }], 'Save');
    if (v === null) return;
    try { await api.renameShell(project, sh.story, sh.k, v.title); } catch (e) { toast(e.message); }
    refresh?.();
  } }];
  // copyText must run inside the tap, which sheet() keeps: it calls run from the button's click
  if (dir) {
    items.push({ label: 'Copy path', run: () => Promise.resolve(copyText(dir)).then((ok) =>
      toast(ok ? 'Path copied' : `Couldn't copy ${dir}`)) });
  }
  items.push({ label: 'Close shell', danger: true, run: async () => {
    const cmd = sh.command || '';
    const text = !cmd || plainShell(cmd) ? '' : `${cmd} is still running and will be killed.`;
    if (!(await confirmBox(`Close shell ${name}?`, 'Close', { danger: true, text }))) return;
    try {
      await api.closeShell(project, sh.story, sh.k);
      onClosed?.();
    } catch (e) {
      toast(e.message);
      refresh?.();
    }
  } });
  sheet(name, items);
}

// closedCard replaces the input row of a closed shell's screen. Without the story there is no
// folder to open a new shell in, so it only offers the way back.
export function closedCard({ k, story, storyGone, onBack, onNew }) {
  return h('div', { class: 'closed-card' },
    h('div', { class: 'hd' }, `Shell $${k} closed`),
    h('div', { class: 'why' }, storyGone ? `Story ${story} was closed.` : 'The shell exited.'),
    h('button', { class: 'primary', onclick: onBack }, '‹ Back to project'),
    storyGone ? null : h('button', { class: 'new', onclick: onNew }, `＋ New shell in ${groupName(story)}`));
}
