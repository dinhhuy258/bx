package web

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// prPickScript runs pickFrom on a tiny fake DOM: "#45" with #456 listed must search for #45
// on Enter, "lease" must pick its row, and directPR must name only #<n> and PR URLs.
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
const { directPR } = await import(process.argv[2] + '/newstory.js');
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
`

func TestPRPickerLooksANumberUp(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	js, err := filepath.Abs(filepath.Join("static", "js"))
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(t.TempDir(), "pick.mjs")
	if err := os.WriteFile(script, []byte(prPickScript), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(node, script, "file://"+filepath.ToSlash(js)).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}
