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

api.projects = async () => [{ name: 'zeta', running: 0, waiting: 0 }, { name: 'bx', running: 2, waiting: 1 }, { name: 'home', running: 1, waiting: 0 }];
let root = new El('main');
let s = showProjects(root);
await tick(); s.stop();
const secs = byClass(root, 'sec');
if (secs.length !== 2) fail('projects sections: ' + secs.length);
if (!text(secs[0]).startsWith('Needs you') || !byClass(secs[0], 'attn').length) fail('needs you: ' + text(secs[0]));
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
  session_states: { 1: { state: 'waiting', text: 'waiting for you' }, 3: { state: 'running', text: 'running' } },
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
if (!byClass(items[0], 'tile-wait').length || text(byClass(items[0], 'item-meta')[0]) !== 'waiting for you · #1') fail('waiting row: ' + text(byClass(items[0], 'item-meta')[0]));
if (!byClass(items[2], 'tile-shell').length || !byClass(items[2], 'busy').length || text(byClass(items[2], 'item-meta')[0]) !== 'npm') fail('shell row');
if (text(byClass(ps[1], 'diff')[0]) !== '+5−1' || !byClass(ps[1], 'tile-run').length) fail('story diff/tile');
const add = byClass(ps[2], 'card-add');
if (add.length !== 1 || text(add[0]) !== 'New session') fail('empty story add row');
`

func TestListScreens(t *testing.T) {
	runJS(t, listScreensScript)
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
