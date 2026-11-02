package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dinhhuy258/bx/internal/claudesync"
	"github.com/dinhhuy258/bx/internal/serve/servetest"
)

// syncEnv points bx at a temp root and a temp "Mac" Claude dir, and starts a server whose
// Claude dir is another temp dir. It never touches the real ~/.claude or ~/.bx.
func syncEnv(t *testing.T) (mac, srv, url, record string) {
	t.Helper()
	t.Setenv("BX_ROOT", t.TempDir())
	mac = t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", mac)
	s, _, ts := servetest.New(t)
	s.ClaudeDir = t.TempDir()
	return mac, s.ClaudeDir, ts.URL, filepath.Join(s.Cfg.Root, "claude-sync.json")
}

func write(t *testing.T, root, rel, content string, mode os.FileMode) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return "<missing>"
	}
	return string(b)
}

// run runs bx claude sync with args against url; tty and in stand for the terminal.
func run(t *testing.T, url string, tty bool, in string, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	if url != "" {
		args = append(args, "--server", url)
	}
	code := claudeSync(args, syncIO{Out: &out, Err: &errOut, In: strings.NewReader(in), TTY: tty})
	return code, out.String(), errOut.String()
}

func empty(t *testing.T, dir string) bool {
	t.Helper()
	entries, err := os.ReadDir(dir)
	return err == nil && len(entries) == 0
}

func TestClaudeUsage(t *testing.T) {
	for _, args := range [][]string{nil, {"nope"}} {
		var errOut bytes.Buffer
		if code := runClaude(args, &bytes.Buffer{}, &errOut); code != 2 || !strings.Contains(errOut.String(), "usage: bx claude sync") {
			t.Errorf("%q: %d %q", args, code, errOut.String())
		}
	}
}

func TestClaudeSyncArgs(t *testing.T) {
	_, _, url, _ := syncEnv(t)
	if code, _, errOut := run(t, url, true, "", "skillz"); code != 2 || !strings.Contains(errOut, `unknown argument "skillz"`) {
		t.Errorf("unknown folder: %d %q", code, errOut)
	}
	if code, _, errOut := run(t, "", true, ""); code != 2 || !strings.Contains(errOut, "no server") {
		t.Errorf("no server: %d %q", code, errOut)
	}
}

func TestClaudeSyncDryRunAndNoTerminal(t *testing.T) {
	mac, srv, url, _ := syncEnv(t)
	write(t, mac, "skills/a/SKILL.md", "a", 0o644)
	code, out, _ := run(t, url, false, "", "--dry-run")
	if code != 0 || !strings.Contains(out, "  + skills/a/SKILL.md") || !strings.Contains(out, "(server: "+srv+")") {
		t.Fatalf("dry run: %d %q", code, out)
	}
	code, out, errOut := run(t, url, false, "")
	if code != 1 || !strings.Contains(errOut, "not a terminal; pass --yes to apply") || !strings.Contains(out, "+ skills/a/SKILL.md") {
		t.Fatalf("no terminal: %d %q %q", code, out, errOut)
	}
	if !empty(t, srv) {
		t.Fatal("nothing may be applied")
	}
}

func TestClaudeSyncAsks(t *testing.T) {
	mac, srv, url, _ := syncEnv(t)
	write(t, mac, "skills/a/SKILL.md", "a", 0o644)
	if code, _, errOut := run(t, url, true, "n\n"); code != 1 || !strings.Contains(errOut, "Cancelled") || !empty(t, srv) {
		t.Fatalf("no: %d %q", code, errOut)
	}
	if code, _, _ := run(t, url, true, ""); code != 1 || !empty(t, srv) {
		t.Fatalf("EOF: %d", code)
	}
	code, out, _ := run(t, url, true, "y\n")
	if code != 0 || !strings.Contains(out, "Apply 1 changes? [y/N] ") || read(t, srv, "skills/a/SKILL.md") != "a" {
		t.Fatalf("yes: %d %q", code, out)
	}
}

func TestClaudeSyncCycle(t *testing.T) {
	mac, srv, url, record := syncEnv(t)
	write(t, mac, "skills/a/SKILL.md", "a", 0o644)
	write(t, mac, "skills/a/run.sh", "#!/bin/sh", 0o755)
	write(t, mac, "skills/.DS_Store", "x", 0o644)
	write(t, mac, "agents/b.md", "b", 0o644)
	write(t, srv, "skills/mine/SKILL.md", "mine", 0o644)

	code, out, errOut := run(t, url, false, "", "--yes")
	if code != 0 || !strings.Contains(out, "Synced: 3 added, 0 updated, 0 removed.") || !strings.Contains(out, "1 server-only kept") {
		t.Fatalf("first: %d %q %q", code, out, errOut)
	}
	if fi, err := os.Stat(filepath.Join(srv, "skills/a/run.sh")); err != nil || fi.Mode().Perm() != 0o755 {
		t.Fatalf("exec bit: %v %v", fi, err)
	}
	if read(t, srv, "skills/.DS_Store") != "<missing>" {
		t.Fatal("dot-names must not sync")
	}

	write(t, mac, "skills/a/SKILL.md", "a2", 0o644)
	if err := os.Remove(filepath.Join(mac, "agents/b.md")); err != nil {
		t.Fatal(err)
	}
	code, out, errOut = run(t, url, false, "", "--yes")
	if code != 0 || !strings.Contains(out, "  ~ skills/a/SKILL.md") || !strings.Contains(out, "  - agents/b.md") || !strings.Contains(out, "Synced: 0 added, 1 updated, 1 removed.") {
		t.Fatalf("second: %d %q %q", code, out, errOut)
	}
	if read(t, srv, "skills/a/SKILL.md") != "a2" || read(t, srv, "agents/b.md") != "<missing>" || read(t, srv, "skills/mine/SKILL.md") != "mine" {
		t.Fatal("server files after the second sync")
	}

	if code, out, _ = run(t, url, false, "", "--yes"); code != 0 || !strings.Contains(out, "Up to date.") {
		t.Fatalf("third: %d %q", code, out)
	}
	rec, err := claudesync.LoadRecord(record)
	if err != nil || strings.Join(rec, ",") != "skills/a/SKILL.md,skills/a/run.sh" {
		t.Fatalf("record %q %v", rec, err)
	}
}

func TestClaudeSyncOneFolder(t *testing.T) {
	mac, srv, url, record := syncEnv(t)
	write(t, mac, "skills/a/SKILL.md", "a", 0o644)
	write(t, mac, "agents/b.md", "b", 0o644)
	if code, _, errOut := run(t, url, false, "", "--yes"); code != 0 {
		t.Fatalf("%d %q", code, errOut)
	}
	// agents/b.md leaves the Mac, but only skills is synced: agents stays as it is
	if err := os.Remove(filepath.Join(mac, "agents/b.md")); err != nil {
		t.Fatal(err)
	}
	write(t, mac, "skills/c.md", "c", 0o644)
	code, out, errOut := run(t, url, false, "", "skills", "--yes")
	if code != 0 || strings.Contains(out, "agents") || !strings.Contains(out, "Synced: 1 added, 0 updated, 0 removed.") {
		t.Fatalf("%d %q %q", code, out, errOut)
	}
	if read(t, srv, "agents/b.md") != "b" {
		t.Fatal("agents/b.md must stay")
	}
	rec, err := claudesync.LoadRecord(record)
	if err != nil || strings.Join(rec, ",") != "agents/b.md,skills/a/SKILL.md,skills/c.md" {
		t.Fatalf("record %q %v", rec, err)
	}
}

func TestClaudeSyncFileOverLimit(t *testing.T) {
	mac, srv, url, _ := syncEnv(t)
	write(t, mac, "skills/big/data.bin", strings.Repeat("x", claudesync.MaxFile+1), 0o644)
	code, _, errOut := run(t, url, false, "", "--yes")
	if code != 1 || !strings.Contains(errOut, "data.bin") || !strings.Contains(errOut, "over 1 MB") || !empty(t, srv) {
		t.Fatalf("%d %q", code, errOut)
	}
}
