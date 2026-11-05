package web

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// prPickScript runs pickFrom on a tiny fake DOM: "#45" with #456 listed must search for #45
// on Enter, "lease" must pick its row, and directPR must name only #<n> and PR URLs.
// storyBranches must leave out checked-out branches.
const prPickScript = `
class Node {}
class El extends Node {
  constructor(tag) { super(); this.tag = tag; this.children = []; this.on = {}; this.value = ''; this.hidden = false; this.open = false; }
  addEventListener(k, f) { (this.on[k] ||= []).push(f); }
  fire(k, e = {}) { for (const f of this.on[k] || []) f(e); }
  setAttribute(k, v) { this[k] = v; }
  append(...c) { this.children.push(...c); }
  replaceChildren(...c) { this.children = c; }
  showModal() { this.open = true; }
  close() { this.open = false; this.fire('close'); }
  remove() {}
}
const made = [];
globalThis.Node = Node;
globalThis.document = { createElement: (t) => { const e = new El(t); made.push(e); return e; }, body: new El('body') };

const { pickFrom } = await import(process.argv[2] + '/ui.js');
const { directPR, storyBranches } = await import(process.argv[2] + '/newstory.js');
const fail = (m) => { console.error(m); process.exit(1); };

const items = [{ label: '#456 Newer   eve · feat/newer', n: 456 }, { label: '#7 Add lease   bob · feat/lease', n: 7 }];
async function enter(text) {
  const asked = [];
  made.length = 0;
  const p = pickFrom('Pull requests', items, { direct: directPR, search: async (q) => { asked.push(q); return { pick: { n: 45 } }; } });
  const input = made.find((e) => e.tag === 'input');
  input.value = text;
  input.fire('input');
  input.fire('keydown', { key: 'Enter', preventDefault() {} });
  return { it: await p, asked };
}
let r = await enter('#45');
if (r.asked.join() !== '#45' || r.it.n !== 45) fail('#45: asked ' + JSON.stringify(r));
r = await enter('lease');
if (r.asked.length || r.it.n !== 7) fail('lease: ' + JSON.stringify(r));
for (const [q, want] of [['#45', true], [' #45 ', true], ['https://github.com/acme/web/pull/5/files', true], ['45', false], ['#45 x', false], ['fix #45', false], ['#', false]]) {
  if (directPR(q) !== want) fail('directPR(' + JSON.stringify(q) + ') = ' + !want);
}
const free = storyBranches([{ name: 'main', checkedOut: true }, { name: 'develop' }, { name: 'feat/s1', checkedOut: true }]);
if (free.map((b) => b.name).join() !== 'develop') fail('storyBranches: ' + JSON.stringify(free));
`

func TestPRPickerLooksANumberUp(t *testing.T) {
	runJS(t, prPickScript)
}

// shellScript loads the shell, session and project screens on the same fake DOM and checks the
// shell helpers: routes ('_' = Chats), plain shells, the next free number, the row label and
// finding a shell in a snapshot (missing = gone, which ends a failed stream at once).
const shellScript = `
class Node {}
class El extends Node {
  constructor(tag) { super(); this.tag = tag; this.children = []; this.on = {}; }
  addEventListener(k, f) { (this.on[k] ||= []).push(f); }
  setAttribute(k, v) { this[k] = v; }
  append(...c) { this.children.push(...c); }
}
globalThis.Node = Node;
globalThis.document = { createElement: (t) => new El(t), body: new El('body') };
globalThis.location = { hash: '', protocol: 'http:', host: 'x' };

const { shellHref, plainShell, nextShellK, shellLabel, groupName, findShell } = await import(process.argv[2] + '/shell.js');
const { showSession, showShell } = await import(process.argv[2] + '/session.js');
const { showProject } = await import(process.argv[2] + '/project.js');
const { api } = await import(process.argv[2] + '/api.js');
const fail = (m) => { console.error(m); process.exit(1); };

if (typeof showSession !== 'function' || typeof showShell !== 'function' || typeof showProject !== 'function') fail('screens not exported');
if (shellHref('p', '', 2) !== '#/p/p/sh/_/2') fail('shellHref chats: ' + shellHref('p', '', 2));
if (shellHref('p', 's1', 1) !== '#/p/p/sh/s1/1') fail('shellHref s1: ' + shellHref('p', 's1', 1));
if (!(plainShell('zsh') && plainShell('-bash') && plainShell('fish') && !plainShell('npm') && !plainShell(''))) fail('plainShell');
const k = nextShellK({ shells: [{ story: 's1', k: 1 }, { story: 's1', k: 3 }, { story: '', k: 2 }] }, 's1');
if (k !== 2) fail('nextShellK: ' + k);
if (nextShellK({ shells: [{ story: 's1', k: 1 }] }, '') !== 1) fail('nextShellK chats');
if (shellLabel({ title: '', command: 'zsh' }) !== 'zsh') fail('shellLabel command');
if (shellLabel({ title: 'dev', command: 'npm' }) !== 'dev') fail('shellLabel title');
if (shellLabel({}) !== 'shell') fail('shellLabel empty');
if (groupName('') !== 'Chats' || groupName('s1') !== 's1') fail('groupName');
const snap = { shells: [{ story: 's1', k: 1, command: 'zsh' }, { story: '', k: 2 }] };
if (findShell(snap, 's1', 1)?.command !== 'zsh' || !findShell(snap, '', 2)) fail('findShell present');
if (findShell(snap, 's1', 2) || findShell(snap, '', 1) || findShell({}, 's1', 1)) fail('findShell gone');
const url = api.shellStreamURL('home network', 'fix/login', 2, 80, 24);
if (url !== 'ws://x/api/projects/home%20network/shells/stream?story=fix%2Flogin&k=2&cols=80&rows=24') fail('shellStreamURL: ' + url);
`

func TestShellHelpers(t *testing.T) {
	runJS(t, shellScript)
}

// slashCommandsScript checks matchSlashCommands: only "/" and a name with no space yet matches, case-insensitive,
// with names that start with the text before names that only contain it.
const slashCommandsScript = `
class Node {}
globalThis.Node = Node;
globalThis.document = { createElement: () => ({}), body: {} };
globalThis.location = { hash: '', protocol: 'http:', host: 'x' };
const { matchSlashCommands } = await import(process.argv[2] + '/session.js');
const fail = (m) => { console.error(m); process.exit(1); };
const list = ['bx:artifact', 'clear', 'compact', 'context', 'cost', 'plan', 'review'].map((name) => ({ name }));
const names = (t) => matchSlashCommands(list, t).map((c) => c.name).join();
if (names('/co') !== 'compact,context,cost') fail('/co: ' + names('/co'));
if (names('/CO') !== 'compact,context,cost') fail('/CO: ' + names('/CO'));
if (names('/art') !== 'bx:artifact') fail('/art: ' + names('/art'));
if (names('/a') !== 'bx:artifact,clear,compact,plan') fail('/a: ' + names('/a'));
if (names('/') !== list.map((c) => c.name).join()) fail('/: ' + names('/'));
for (const t of ['', 'co', '/compact ', '/compact x', ' /co', '/co\nx', '/zzz']) {
  if (names(t) !== '') fail(JSON.stringify(t) + ': ' + names(t));
}
const many = Array.from({ length: 50 }, (_, i) => ({ name: 'c' + i }));
if (matchSlashCommands(many, '/c').length !== 30) fail('limit');
`

func TestMatchSlashCommands(t *testing.T) {
	runJS(t, slashCommandsScript)
}

// sheetScript opens a sheet on a fake DOM: danger rows go in their own group after the others,
// a setter fills in a row's hint and badge, a tap runs the item and a tap on the backdrop
// (the dialog itself) only closes it.
const sheetScript = `
class Node {}
class El extends Node {
  constructor(tag) { super(); this.tag = tag; this.children = []; this.on = {}; this.hidden = false; this.open = false; this.textContent = ''; }
  addEventListener(k, f) { (this.on[k] ||= []).push(f); }
  fire(k, e = {}) { for (const f of this.on[k] || []) f(e); }
  setAttribute(k, v) { this[k] = v; }
  append(...c) { this.children.push(...c); }
  showModal() { this.open = true; }
  close() { this.open = false; this.fire('close'); }
  remove() {}
}
const made = [];
globalThis.Node = Node;
globalThis.document = { createElement: (t) => { const e = new El(t); made.push(e); return e; },
  createElementNS: (_, t) => new El(t), body: new El('body') };

const { sheet } = await import(process.argv[2] + '/ui.js');
const fail = (m) => { console.error(m); process.exit(1); };
const ran = [];
const [setA] = sheet('s', [{ label: 'Delete', danger: true, icon: 'trash', run: () => ran.push('d') },
  { label: 'Artifacts', icon: 'doc', hint: 'Loading…', chevron: true, run: () => ran.push('a') }], { meta: 'idle · #1' });
const groups = made.filter((e) => e.className === 'sheet-group');
const label = (g) => g.children[0].children.find((c) => c.className === 'sheet-label').children[0].children[0];
if (groups.length !== 2 || label(groups[0]) !== 'Artifacts' || label(groups[1]) !== 'Delete') fail('groups: ' + groups.map(label));
setA({ hint: 'a.html · 2m ago', badge: 2 });
const hint = made.find((e) => e.className === 'sheet-hint');
const badge = made.find((e) => e.className === 'sheet-badge');
if (hint.textContent !== 'a.html · 2m ago' || hint.hidden || badge.textContent !== 2 || badge.hidden) fail('setter');
const dlg = made.find((e) => e.tag === 'dialog');
dlg.fire('click', { target: groups[0] });
if (!dlg.open) fail('a tap inside closed the sheet');
groups[0].children[0].fire('click');
if (dlg.open || ran.join() !== 'a') fail('row tap: ' + ran);
sheet('t', [{ label: 'x', run: () => ran.push('x') }]);
const dlg2 = made.filter((e) => e.tag === 'dialog').pop();
dlg2.fire('click', { target: dlg2 });
if (dlg2.open || ran.includes('x')) fail('backdrop tap');
`

func TestSheetGroupsAndFillsRows(t *testing.T) {
	runJS(t, sheetScript)
}

// textSizeScript checks the ⋯ sheet's Text size row: its rows sit in their own group between
// the actions and the danger rows, A−/A+ step the size within 10..16 and leave the sheet open,
// and the saved size falls back to 12 when storage is off or holds junk.
const textSizeScript = `
class Node {}
class El extends Node {
  constructor(tag) { super(); this.tag = tag; this.children = []; this.on = {}; this.hidden = false; this.open = false; this.textContent = ''; }
  addEventListener(k, f) { (this.on[k] ||= []).push(f); }
  fire(k, e = {}) { for (const f of this.on[k] || []) f(e); }
  setAttribute(k, v) { this[k] = v; }
  append(...c) { this.children.push(...c); }
  showModal() { this.open = true; }
  close() { this.open = false; this.fire('close'); }
  remove() {}
}
const made = [];
globalThis.Node = Node;
globalThis.document = { createElement: (t) => { const e = new El(t); made.push(e); return e; },
  createElementNS: (_, t) => new El(t), body: new El('body') };
globalThis.location = { hash: '', protocol: 'http:', host: 'x' };
let store = {};
globalThis.localStorage = { getItem: (k) => store[k] ?? null, setItem: (k, v) => { store[k] = v; } };

const { sheet } = await import(process.argv[2] + '/ui.js');
const { textSizeItem, savedFontSize } = await import(process.argv[2] + '/session.js');
const fail = (m) => { console.error(m); process.exit(1); };

if (savedFontSize() !== 12) fail('default: ' + savedFontSize());
store['bx.termFontSize'] = '14';
if (savedFontSize() !== 14) fail('saved: ' + savedFontSize());
store['bx.termFontSize'] = '40';
if (savedFontSize() !== 12) fail('out of range: ' + savedFontSize());
globalThis.localStorage = { getItem: () => { throw new Error('off'); } };
if (savedFontSize() !== 12) fail('storage off: ' + savedFontSize());

let size = 15;
const sets = [];
const item = textSizeItem(() => size, (n) => { size = n; sets.push(n); });
sheet('s', [{ label: 'Rename', run() {} }, { label: 'Delete', danger: true, run() {} }, item]);
const groups = made.filter((e) => e.className === 'sheet-group');
const label = (g) => g.children[0].children.find((c) => c.className === 'sheet-label').children[0].children[0];
if (groups.map(label).join() !== 'Rename,Text size,Delete') fail('groups: ' + groups.map(label));
const row = groups[1].children[0];
if (row.tag !== 'div' || row.className !== 'sheet-row is-static') fail('control row: ' + row.tag + ' ' + row.className);
const [less, out, more] = item.control.children;
more.fire('click');
more.fire('click');
if (sets.join() !== '16' || out.textContent !== '16' || !more.disabled || less.disabled) fail('A+: ' + sets + ' ' + out.textContent);
for (let i = 0; i < 8; i++) less.fire('click');
if (size !== 10 || !less.disabled || more.disabled || out.textContent !== '10') fail('A−: ' + size);
const dlg = made.find((e) => e.tag === 'dialog');
if (!dlg.open) fail('a size tap closed the sheet');
`

func TestTextSizeRow(t *testing.T) {
	runJS(t, textSizeScript)
}

// listScreensScript renders the projects and project screens on a fake DOM: waiting projects
// get a "Needs you" card, rows carry tinted tiles and state dots, no projects shows the empty
// state, and an empty story offers a New session row.
const listScreensScript = `
class Node {}
class El extends Node {
  constructor(tag) { super(); this.tag = tag; this.children = []; this.on = {}; this.hidden = false; this.className = ''; }
  addEventListener(k, f) { (this.on[k] ||= []).push(f); }
  removeEventListener() {}
  setAttribute(k, v) { this[k] = v; }
  append(...c) { this.children.push(...c); }
  replaceChildren(...c) { this.children = c; }
}
globalThis.Node = Node;
globalThis.document = { createElement: (t) => new El(t), createElementNS: (_, t) => new El(t),
  addEventListener() {}, removeEventListener() {}, body: new El('body') };
const { showProjects } = await import(process.argv[2] + '/projects.js');
const { showProject } = await import(process.argv[2] + '/project.js');
const { api } = await import(process.argv[2] + '/api.js');
const fail = (m) => { console.error(m); process.exit(1); };
const all = (e, out = []) => { if (e instanceof El) { out.push(e); e.children.forEach((c) => all(c, out)); } return out; };
const text = (e) => typeof e === 'string' ? e : e.children.map(text).join('');
const byClass = (root, c) => all(root).filter((e) => e.className.split(' ').includes(c));
const tick = () => new Promise((r) => setTimeout(r, 0));

api.projects = async () => [{ name: 'zeta', running: 0, waiting: 0 }, { name: 'bx', running: 2, waiting: 1 }, { name: 'home', running: 1, waiting: 0 },
  { name: 'lab', running: 0, waiting: 0, unread: 1 }];
let root = new El('main');
let s = showProjects(root);
await tick(); s.stop();
const secs = byClass(root, 'sec');
if (secs.length !== 2) fail('projects sections: ' + secs.length);
if (!text(secs[0]).startsWith('Needs you') || !byClass(secs[0], 'attn').length) fail('needs you: ' + text(secs[0]));
const lab = byClass(secs[0], 'item-link')[1];
if (lab?.href !== '#/p/lab' || !byClass(lab, 'tile-unread').length || text(byClass(lab, 'item-meta')[0]) !== '1 unread') fail('lab row: ' + (lab && text(lab)));
const rows = byClass(secs[1], 'item-link').map((r) => byClass(r, 'item-title').map(text)[0]);
if (rows.join() !== 'home,zeta') fail('all projects: ' + rows);
const bx = byClass(secs[0], 'item-link')[0];
if (bx.href !== '#/p/bx' || !byClass(bx, 'tile-wait').length) fail('bx row tile');
if (text(byClass(bx, 'item-meta')[0]) !== '1 waiting2 running') fail('bx meta: ' + text(byClass(bx, 'item-meta')[0]));
if (text(byClass(secs[1], 'item-meta')[1]) !== 'Nothing running') fail('idle meta');

api.projects = async () => [];
root = new El('main');
s = showProjects(root);
await tick(); s.stop();
if (!byClass(root, 'empty-state').length || byClass(root, 'sec').length) fail('empty state');

api.snapshot = async () => ({
  project: { kind: '', stories: { 'fix/login': {}, idle: {} },
    sessions: { 1: { title: 'explain policy', done: false }, 2: { title: 'old', done: true }, 3: { title: 'login', story: 'fix/login', done: false } } },
  live: { branch: 'master', dirty_files: 2, diff: { 'fix/login': { add: 5, del: 1, dirty: 0 } } },
  session_states: { 1: { state: 'waiting', text: 'waiting for you' }, 3: { state: 'idle', text: 'idle · unread', unread: true } },
  shells: [{ story: '', k: 1, command: 'npm', title: 'dev' }],
});
root = new El('main');
s = showProject(root, 'bx');
await tick(); s.stop();
if (text(byClass(root, 'ttl-meta')[0]) !== 'master · 2 uncommitted') fail('meta: ' + text(byClass(root, 'ttl-meta')[0]));
const ps = byClass(root, 'sec');
if (ps.map((x) => text(byClass(x, 'sec-label')[0])).join() !== 'Chats,fix/login,idle') fail('groups: ' + ps.map((x) => text(byClass(x, 'sec-label')[0])));
const items = byClass(ps[0], 'item');
if (items.length !== 3 || !items[1].className.includes('done')) fail('chats rows: ' + items.map((i) => i.className));
if (!byClass(items[0], 'tile-wait').length || text(byClass(items[0], 'item-meta')[0]) !== 'waiting for you') fail('waiting row: ' + text(byClass(items[0], 'item-meta')[0]));
if (!byClass(items[2], 'tile-shell').length || !byClass(items[2], 'busy').length || text(byClass(items[2], 'item-meta')[0]) !== 'npm') fail('shell row');
if (text(byClass(ps[1], 'diff')[0]) !== '+5−1' || !byClass(ps[1], 'tile-unread').length) fail('story diff/tile');
if (text(byClass(ps[1], 'item-meta')[0]) !== 'idle · unread') fail('unread row: ' + text(byClass(ps[1], 'item-meta')[0]));
if (byClass(ps[2], 'card').length || byClass(ps[2], 'item').length) fail('empty story shows a card');
`

func TestListScreens(t *testing.T) {
	runJS(t, listScreensScript)
}

// closeStoryScript checks that Close story is a red row in its own group, asks with a red
// button, and asks again, red, before discarding uncommitted files.
const closeStoryScript = `
class Node {}
class El extends Node {
  constructor(tag) { super(); this.tag = tag; this.children = []; this.on = {}; this.hidden = false; this.className = ''; }
  addEventListener(k, f) { (this.on[k] ||= []).push(f); }
  removeEventListener() {}
  setAttribute(k, v) { this[k] = v; }
  append(...c) { this.children.push(...c); }
  replaceChildren(...c) { this.children = c; }
  showModal() { this.open = true; }
  close(v) { if (v !== undefined) this.returnValue = v; this.open = false; (this.on.close || []).forEach((f) => f()); }
  remove() { this.removed = true; }
  click() { (this.on.click || []).forEach((f) => f({ target: this })); }
}
globalThis.Node = Node;
globalThis.document = { createElement: (t) => new El(t), createElementNS: (_, t) => new El(t),
  addEventListener() {}, removeEventListener() {}, querySelector: () => null, body: new El('body') };
const { showProject } = await import(process.argv[2] + '/project.js');
const { api } = await import(process.argv[2] + '/api.js');
const fail = (m) => { console.error(m); process.exit(1); };
const all = (e, out = []) => { if (e instanceof El) { out.push(e); e.children.forEach((c) => all(c, out)); } return out; };
const text = (e) => typeof e === 'string' ? e : e.children.map(text).join('');
const byClass = (root, c) => all(root).filter((e) => e.className.split(' ').includes(c));
const tick = () => new Promise((r) => setTimeout(r, 0));
const top = () => document.body.children.filter((d) => d.open).at(-1);

api.snapshot = async () => ({
  project: { kind: '', stories: { 'fix/login': {} }, sessions: { 3: { title: 'login', story: 'fix/login', done: false } } },
  live: { branch: 'master', dirty_files: 0, diff: {} }, session_states: {}, shells: [],
});
const calls = [];
api.action = async (p, a, body) => {
  calls.push(a + (body.force ? ' force' : ''));
  if (!body.force) { const e = new Error('dirty'); e.status = 409; e.result = { dirty: 2, detail: 'a.go, b.go' }; throw e; }
  return {};
};
const root = new El('main');
const s = showProject(root, 'bx');
await tick(); s.stop();
all(root).find((e) => e['aria-label'] === 'Story actions').click();
const sheet = top();
const groups = byClass(sheet, 'sheet-group');
const close = byClass(sheet, 'sheet-row').find((r) => text(r).startsWith('Close story'));
if (!close || !close.className.includes('is-danger')) fail('close story is not red: ' + (close && close.className));
if (!byClass(groups.at(-1), 'sheet-row').includes(close) || byClass(groups.at(-1), 'sheet-row').length !== 1) fail('close story is not alone in the last group');
close.click();
await tick();
let dlg = top();
let ok = all(dlg).find((e) => e.value === 'ok');
if (text(all(dlg).find((e) => e.tag === 'h2')) !== 'Close fix/login?' || ok.className !== 'danger' || text(ok) !== 'Close story') fail('first ask: ' + text(dlg) + ' ok=' + ok.className);
if (!text(dlg).includes('Its sessions stop, its worktree is removed and its branch is deleted here and on origin.')) fail('first ask text: ' + text(dlg));
if (calls.length) fail('closed before the answer: ' + calls);
dlg.close('ok');
await tick(); await tick();
dlg = top();
ok = all(dlg).find((e) => e.value === 'ok');
if (!text(dlg).includes('fix/login has 2 uncommitted files (a.go, b.go). They are lost with its worktree.') || ok.className !== 'danger' || text(ok) !== 'Discard and close') fail('second ask: ' + text(dlg) + ' ok=' + ok.className);
dlg.close('ok');
await tick(); await tick();
if (calls.join() !== 'stories/close,stories/close force') fail('calls: ' + calls);
`

func TestCloseStoryIsRedAndConfirmed(t *testing.T) {
	runJS(t, closeStoryScript)
}

// runJS runs an ES-module script with node; the script gets the file:// URL of static/js as
// its first argument and fails by exiting non-zero.
func runJS(t *testing.T, src string) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	js, err := filepath.Abs(filepath.Join("static", "js"))
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(t.TempDir(), "test.mjs")
	if err := os.WriteFile(script, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(node, script, "file://"+filepath.ToSlash(js)).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}

// reviewScript draws the review screen from a stubbed api and checks Send counts only unsent
// comments, a sent bubble is dimmed, and ask has a textarea field.
const reviewScript = `
class Node {}
class El extends Node {
  constructor(tag) { super(); this.tag = tag; this.children = []; this.on = {}; this.value = ''; this.hidden = false; this.open = false; }
  addEventListener(k, f) { (this.on[k] ||= []).push(f); }
  fire(k, e = {}) { for (const f of this.on[k] || []) f(e); }
  setAttribute(k, v) { this[k] = v; }
  append(...c) { this.children.push(...c); }
  // like the DOM: anything but a node or string (null, an array) would be printed as text
  replaceChildren(...c) { if (c.some((x) => !(x instanceof Node) && typeof x !== 'string')) fail('replaceChildren got ' + c.map(String)); this.children = c; }
  showModal() { this.open = true; }
  close() { this.open = false; this.fire('close'); }
  remove() {}
  set textContent(v) { this.children = []; this._t = v; }
  get textContent() { return this._t; }
  get classList() {
    const e = this;
    return { toggle(c, on) { const s = new Set((e.className || '').split(' ').filter(Boolean)); if (on) s.add(c); else s.delete(c); e.className = [...s].join(' '); } };
  }
}
const made = [];
globalThis.Node = Node;
globalThis.document = { createElement: (t) => { const e = new El(t); made.push(e); return e; }, createElementNS: (_, t) => new El(t), body: new El('body'), querySelector: () => null };
globalThis.location = { hash: '' };
globalThis.window = globalThis;
// the poll (2 s) and the card clock (1 s) must not keep node alive; short waits stay real
const realTimeout = globalThis.setTimeout;
globalThis.setTimeout = (f, ms, ...a) => (ms >= 1000 ? 0 : realTimeout(f, ms, ...a));
globalThis.setInterval = () => 0;
globalThis.clearTimeout = () => {};
globalThis.clearInterval = () => {};
const fail = (m) => { console.error(m); process.exit(1); };
const text = (e) => typeof e === 'string' ? e : (e.textContent ?? '') + (e.children || []).map(text).join('');

const { api } = await import(process.argv[2] + '/api.js');
const { showReview, unsentOf, sideOf, lineOf, quoteOf, rangeOf, linesOf } = await import(process.argv[2] + '/review.js');
const { ask } = await import(process.argv[2] + '/ui.js');

if (sideOf({ kind: 'del', old: 4 }) !== 'old' || lineOf({ kind: 'del', old: 4 }) !== 4 || lineOf({ kind: 'add', new: 7 }) !== 7) fail('sideOf/lineOf');
if (unsentOf([{ id: 'a' }, { id: 'b', sent_at: 5 }]).length !== 1) fail('unsentOf');
if (quoteOf('\tif a {\n\n\t\tb()\n\t}\nx', 4).join('|') !== 'if a {||\tb()|}|… 1 more lines' || quoteOf('  ', 4).length) fail('quoteOf');
const rg = rangeOf([{ kind: 'ctx', new: 4, text: 'a' }, { kind: 'del', old: 5, text: 'gone' }, { kind: 'add', new: 5, text: 'b' }]);
if (rg.side !== 'new' || rg.start_line !== 4 || rg.line !== 5 || rg.code !== 'a\nb') fail('rangeOf mixed: ' + JSON.stringify(rg));
const rd = rangeOf([{ kind: 'del', old: 5, text: 'x' }, { kind: 'del', old: 6, text: 'y' }]);
if (rd.side !== 'old' || rd.start_line !== 5 || rd.line !== 6) fail('rangeOf removed: ' + JSON.stringify(rd));
const r1 = rangeOf([{ kind: 'del', old: 5, text: 'x' }, { kind: 'add', new: 5, text: 'y' }]);
if (r1.start_line !== 0 || r1.line !== 5 || r1.side !== 'new') fail('rangeOf one line: ' + JSON.stringify(r1));
if (linesOf({ line: 3 }) !== 'line 3' || linesOf({ start_line: 1, line: 3 }) !== 'lines 1–3') fail('linesOf');

api.review = async () => ({ base: 'main', total: 1, version: 2, files: [{ path: 'a.go', status: 'modified', add: 1, del: 0,
  hunks: [{ header: '@@ -1 +1,2 @@', rows: [{ kind: 'ctx', old: 1, new: 1, text: 'x' }, { kind: 'add', old: 0, new: 2, text: 'y' }] }] }],
  comments: [{ id: 'a', file: 'a.go', side: 'new', line: 2, body: 'why y' }, { id: 'b', file: 'a.go', side: 'new', line: 1, body: 'old', sent_to: 3, sent_at: 9 }] });
const root = new El('div');
showReview(root, 'home-network', 's1');
await new Promise((r) => setTimeout(r, 0));
const send = made.find((e) => e.tag === 'button' && e.className === 'primary');
if (!send || text(send) !== 'Send 1' || send.disabled) fail('send button: ' + (send && text(send)));
const bubbles = made.filter((e) => typeof e.className === 'string' && e.className.startsWith('rv-bubble'));
if (bubbles.length !== 2 || !bubbles.some((b) => b.className === 'rv-bubble sent' && text(b).includes('✓ sent → [3]'))) fail('bubbles: ' + bubbles.map(text).join(' | '));

// Select more lines: tap line 1, type, pick a range to line 2 (the other file's rows ignore
// taps), comment on it with the draft.
const saved = [];
api.saveReview = async (n, s, body) => { saved.push(body); return api.review(); };
const rowEl = (n) => made.find((e) => e.className && e.className.startsWith('rv-row') && text(e).startsWith(n));
const dialog = () => made.filter((e) => e.tag === 'dialog').pop();
rowEl('1').fire('click');
const d1 = dialog();
const more = made.find((e) => e.tag === 'button' && e.className === 'ghost');
if (!more || text(more) !== '⇕ Select more lines') fail('no Select more lines');
if (!made.some((e) => e.tag === 'button' && e.className === 'vio' && text(e) === '✦ Explain')) fail('no Explain in the comment form');
made.find((e) => e.tag === 'textarea').value = 'pull out';
d1.returnValue = 'extra:select';
d1.close();
await new Promise((r) => setTimeout(r, 0));
if (!rowEl('1').className.includes('rv-sel') || rowEl('2').className.includes('rv-sel')) fail('select starts on line 1: ' + rowEl('1').className);
rowEl('2').fire('click');
const dockEl = made.find((e) => e.className === 'rv-dock');
if (dockEl.hidden || !text(dockEl).includes('Lines 1–2') || !rowEl('2').className.includes('rv-sel')) fail('dock: ' + text(dockEl));
made.find((e) => e.tag === 'button' && text(e) === 'Comment').fire('click');
await new Promise((r) => setTimeout(r, 0));
const d2 = dialog();
const ta2 = made.filter((e) => e.tag === 'textarea').pop();
if (ta2.value !== 'pull out' || !text(d2).includes('Comment · a.go:1–2') || made.some((e) => e.className === 'ghost' && made.indexOf(e) > made.indexOf(d2))) fail('range form: ' + text(d2));
d2.returnValue = 'ok';
d2.close();
await new Promise((r) => setTimeout(r, 0));
const c = saved.length && saved[0].comments.pop();
if (!c || c.start_line !== 1 || c.line !== 2 || c.side !== 'new' || c.code !== 'x\ny' || c.body !== 'pull out') fail('saved: ' + JSON.stringify(c));
if (!dockEl.hidden) fail('the dock stays after saving');

// Explain: tap line 2, pick the Explain extra, answer the second form with code only.
const asked = [];
api.snapshot = async () => ({ project: { sessions: { 1: { story: 's1', title: 'Add lease time', uuid: 'u1' } } }, live: { agents: {} } });
api.explain = async (n, s, body) => {
  asked.push(body);
  return { ...(await api.review()), explains: [{ id: 'e1', file: 'a.go', side: 'new', line: 2, state: 'running', started: 1 }] };
};
rowEl('2').fire('click');
const d3 = dialog();
d3.returnValue = 'extra:explain';
d3.close();
await new Promise((r) => setTimeout(r, 0));
await new Promise((r) => setTimeout(r, 0));
const d4 = dialog();
if (d4 === d3 || !text(d4).includes('Explain · a.go:2') || !text(d4).includes('[1] Add lease time · done') || !text(d4).includes('Code only (no conversation)')) fail('explain form: ' + text(d4));
made.filter((e) => e.tag === 'select').pop().value = '-1';
made.filter((e) => e.tag === 'textarea').pop().value = 'why?';
d4.returnValue = 'ok';
d4.close();
await new Promise((r) => setTimeout(r, 0));
await new Promise((r) => setTimeout(r, 0));
if (asked.length !== 1 || JSON.stringify(asked[0]) !== JSON.stringify({ file: 'a.go', side: 'new', line: 2, code: 'y', question: 'why?', session: -1 })) fail('asked: ' + JSON.stringify(asked));
const card = made.filter((e) => e.className === 'rv-ex').pop();
if (!card || !text(card).includes('Explaining…') || !text(card).includes('code only')) fail('card: ' + (card && text(card)));

made.length = 0;
ask('Comment', [{ name: 'body', label: 'Comment', type: 'textarea', value: 'hi' }], 'Save');
const ta = made.find((e) => e.tag === 'textarea');
if (!ta || ta.value !== 'hi') fail('textarea');
`

func TestReviewScreen(t *testing.T) {
	runJS(t, reviewScript)
}

// pullRequestScript checks the pure helpers of the PR page, then draws it from a stubbed api:
// the open unsent thread is ticked and Address counts it, and a failed check shows Fix CI.
const pullRequestScript = `
class Node {}
class El extends Node {
  constructor(tag) { super(); this.tag = tag; this.children = []; this.on = {}; this.hidden = false; }
  addEventListener(k, f) { (this.on[k] ||= []).push(f); }
  setAttribute(k, v) { this[k] = v; }
  append(...c) { this.children.push(...c); }
  replaceChildren(...c) { this.children = c; }
  set textContent(v) { this.children = []; this._t = v; }
  get textContent() { return this._t; }
}
const made = [];
globalThis.Node = Node;
globalThis.document = { createElement: (t) => { const e = new El(t); made.push(e); return e; }, createElementNS: (_, t) => new El(t), body: new El('body'), querySelector: () => null };
globalThis.location = { hash: '' };
globalThis.window = globalThis;
const fail = (m) => { console.error(m); process.exit(1); };
const text = (e) => typeof e === 'string' ? e : (e.textContent ?? '') + (e.children || []).map(text).join('');

const { api } = await import(process.argv[2] + '/api.js');
const { prBadgeText, firstFocus, initialPicks, openThread, showPR } = await import(process.argv[2] + '/pr.js');

for (const [b, want] of [
  [{ count: 1, number: 142, fail: true, threads: 3 }, '#142 ✗ · 💬3'],
  [{ count: 2, fail: true }, '2 PRs ✗'],
  [{ count: 1, number: 131, merged: 1 }, 'merged #131'],
  [{ count: 1, number: 120, closed: 1 }, 'closed #120'],
  [{ count: 2, closed: 2 }, 'closed 2/2'],
  [{ count: 2, merged: 1, closed: 1 }, 'merged 1/2'],
  [{ count: 1, number: 137, pass: true, ready: true }, '#137 ✓ ready'],
  [{ count: 1, number: 9, pending: true, changes: true, conflict: true }, '#9 ◌ · changes · conflict'],
  [{ count: 0 }, ''],
  [undefined, ''],
]) {
  if (prBadgeText(b) !== want) fail('prBadgeText(' + JSON.stringify(b) + ') = ' + JSON.stringify(prBadgeText(b)));
}

const data = {
  me: 'huy', checked_at: 1,
  prs: [
    { repo: 'web', number: 88, title: 'Web', base: 'develop', state: 'OPEN', checks: [{ name: 'build', state: 'pass' }], threads: [] },
    { repo: 'api', number: 142, title: 'Shorter DHCP lease', base: 'develop', state: 'OPEN', url: 'https://x/142',
      checks: [{ name: 'lint', state: 'pass' }, { name: 'test', state: 'fail', url: 'https://x/run' }],
      threads: [
        { id: 'T1', path: 'a.go', line: 3, comments: [{ author: 'mai', body: 'Fix this' }] },
        { id: 'T2', path: 'b.go', line: 4, sent_at: 5, comments: [{ author: 'mai', body: 'Sent' }] },
        { id: 'T3', path: 'c.go', line: 5, comments: [{ author: 'mai', body: 'a' }, { author: 'huy', body: 'b' }] },
        { id: 'T4', path: 'd.go', line: 6, resolved: true, comments: [{ author: 'kofi', body: 'ok' }] }] },
    { repo: 'infra', missing: 'not pushed' }],
};
if (firstFocus(data, '') !== 'api' || firstFocus(data, 'web') !== 'web' || firstFocus(data, 'infra') !== 'api' || firstFocus({ prs: [{ missing: 'no PR' }] }, '') !== '') fail('firstFocus');
if (initialPicks(data.prs[1], 'huy').join() !== 'T1') fail('initialPicks: ' + initialPicks(data.prs[1], 'huy'));
if (!openThread(data.prs[1].threads[1], 'huy') || openThread(data.prs[1].threads[2], 'huy') || openThread(data.prs[1].threads[3], 'huy')) fail('openThread');

api.pr = async () => data;
const root = new El('div');
showPR(root, 'home-network', 's1', '');
await new Promise((r) => setTimeout(r, 0));
const btn = (label) => made.filter((e) => e.tag === 'button' && text(e).startsWith(label)).pop();
if (!btn('Address 1') || btn('Address 1').disabled) fail('Address 1: ' + made.filter((e) => e.tag === 'button').map(text));
const fix = btn('Fix CI');
if (!fix || fix.hidden) fail('Fix CI must show with a failed check');
const page = text(root);
if (!page.includes('Shorter DHCP lease') || !page.includes('Fix this') || !page.includes('api #142') || !page.includes('web #88')) fail('page: ' + page);
if (page.includes('infra #')) fail('a repo without a PR must not be in the switcher');
`

func TestPullRequestPage(t *testing.T) {
	runJS(t, pullRequestScript)
}
