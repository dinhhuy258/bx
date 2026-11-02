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
