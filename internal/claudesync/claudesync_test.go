package claudesync

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// put writes rel (slash path) under root with mode, creating its folders.
func put(t *testing.T, root, rel, content string, mode os.FileMode) {
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

func link(t *testing.T, target, at string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(at), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, at); err != nil {
		t.Fatal(err)
	}
}

func paths(files []File) []string {
	out := []string{}
	for _, f := range files {
		out = append(out, f.Path)
	}
	return out
}

func TestValidPath(t *testing.T) {
	for _, tc := range []struct {
		p  string
		ok bool
	}{
		{"skills/a/SKILL.md", true},
		{"rules/x.md", true},
		{"", false},
		{"../x", false},
		{"/abs", false},
		{"/skills/a", false},
		{"skills/../agents/x", false},
		{"skills/../x", false},
		{"skills//a", false},
		{"skills/./a", false},
		{"skills/.git/x", false},
		{"hidden/.x", false},
		{"skills/a/", false},
		{`skills/a\b`, false},
		{"skills/a\x00", false},
		{"settings.json", false},
		{"other/a", false},
		{"skills", false},
	} {
		err := ValidPath(tc.p)
		if (err == nil) != tc.ok {
			t.Errorf("ValidPath(%q) = %v, want ok=%v", tc.p, err, tc.ok)
		}
		if err != nil && !errors.Is(err, ErrInvalid) {
			t.Errorf("ValidPath(%q): %v is not ErrInvalid", tc.p, err)
		}
	}
}

func TestItemOf(t *testing.T) {
	for p, want := range map[string]string{
		"skills/a/b/c.md": "skills/a",
		"skills/a":        "skills/a",
		"agents/x.md":     "agents/x.md",
		"agents/sub/y.md": "agents/sub",
		"commands/c.md":   "commands/c.md",
		"rules/r/s/t.md":  "rules/r",
	} {
		if got := ItemOf(p); got != want {
			t.Errorf("ItemOf(%q) = %q, want %q", p, got, want)
		}
	}
}

func TestValidItem(t *testing.T) {
	for _, it := range []string{"skills/a", "agents/x.md", "rules/sub"} {
		if err := ValidItem(it); err != nil {
			t.Errorf("ValidItem(%q) = %v", it, err)
		}
	}
	for _, it := range []string{"", "skills", "skills/a/b", "other/a", "skills/.git", "skills/..", `agents/a\b`} {
		if err := ValidItem(it); !errors.Is(err, ErrInvalid) {
			t.Errorf("ValidItem(%q) = %v, want ErrInvalid", it, err)
		}
	}
}

func TestScan(t *testing.T) {
	mac, outside := t.TempDir(), t.TempDir()
	put(t, mac, "skills/a/SKILL.md", "a", 0o644)
	put(t, mac, "skills/a/run.sh", "#!/bin/sh", 0o755)
	put(t, mac, "skills/.DS_Store", "x", 0o644)
	put(t, mac, "skills/a/.DS_Store", "x", 0o644)
	put(t, mac, "skills/.git/config", "x", 0o644)
	put(t, mac, "skills/a/.hidden/x.md", "x", 0o644)
	put(t, mac, "agents/b.md", "b", 0o644)
	put(t, mac, "settings.json", "{}", 0o644)
	put(t, outside, "real.md", "real", 0o644)
	put(t, outside, "dir/c.md", "c", 0o644)
	link(t, filepath.Join(outside, "real.md"), filepath.Join(mac, "agents/linked.md"))
	link(t, filepath.Join(outside, "dir"), filepath.Join(mac, "skills/linkdir"))
	link(t, "..", filepath.Join(mac, "skills/a/loop"))              // a loop back to skills/
	link(t, "/nowhere/at/all", filepath.Join(mac, "skills/broken")) // a broken link

	sc, err := Scan(mac) // commands and rules are missing
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"agents/b.md", "agents/linked.md", "skills/a/SKILL.md", "skills/a/run.sh", "skills/linkdir/c.md"}
	if got := paths(sc.Files); !reflect.DeepEqual(got, want) {
		t.Fatalf("paths = %q, want %q", got, want)
	}
	by := map[string]File{}
	for _, f := range sc.Files {
		by[f.Path] = f
	}
	if !by["skills/a/run.sh"].Exec || by["skills/a/SKILL.md"].Exec {
		t.Errorf("exec bits: %+v", sc.Files)
	}
	if f := by["agents/linked.md"]; f.Size != 4 || f.SHA256 == "" || f.Src != filepath.Join(mac, "agents/linked.md") {
		t.Errorf("linked file: %+v", f)
	}
	if want := map[string]bool{"agents/linked.md": true, "skills/linkdir": true}; !reflect.DeepEqual(sc.Links, want) {
		t.Errorf("links = %v, want %v", sc.Links, want)
	}
	if len(sc.Warnings) != 0 {
		t.Errorf("warnings = %v", sc.Warnings)
	}
}

func TestScanSymlinkedTopFolderAndBadName(t *testing.T) {
	mac, outside := t.TempDir(), t.TempDir()
	put(t, outside, "x/SKILL.md", "x", 0o644)
	link(t, outside, filepath.Join(mac, "skills"))
	put(t, mac, `agents/a\b.md`, "x", 0o644)
	put(t, mac, `agents/sub/c\d.md`, "x", 0o644)
	put(t, mac, `agents/sub/e\f.md`, "x", 0o644)
	put(t, mac, "agents/sub/ok.md", "x", 0o644)
	sc, err := Scan(mac)
	if err != nil {
		t.Fatal(err)
	}
	// the bad names are skipped, the rest of the scan still returns
	if want := []string{"agents/sub/ok.md", "skills/x/SKILL.md"}; !reflect.DeepEqual(paths(sc.Files), want) {
		t.Fatalf("paths = %q, want %q", paths(sc.Files), want)
	}
	// a symlinked top folder doesn't mark its items
	if len(sc.Links) != 0 {
		t.Errorf("links = %v", sc.Links)
	}
	want := map[string]string{
		`agents/a\b.md`: `skipped agents/a\b.md: bad name`,
		"agents/sub":    `skipped agents/sub/c\d.md: bad name (+1 more)`,
	}
	if !reflect.DeepEqual(sc.Warnings, want) {
		t.Errorf("warnings = %q, want %q", sc.Warnings, want)
	}
}

func TestScanServerSkipsSymlinks(t *testing.T) {
	srv, outside := t.TempDir(), t.TempDir()
	put(t, srv, "skills/a/SKILL.md", "a", 0o755)
	put(t, srv, "skills/.git/x", "x", 0o644)
	put(t, outside, "f.md", "f", 0o644)
	put(t, outside, "d/g.md", "g", 0o644)
	link(t, filepath.Join(outside, "f.md"), filepath.Join(srv, "skills/f.md"))
	link(t, filepath.Join(outside, "d"), filepath.Join(srv, "skills/d"))
	link(t, outside, filepath.Join(srv, "agents"))
	got, err := ScanServer(srv)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got["skills/a/SKILL.md"].Exec || got["skills/a/SKILL.md"].Size != 1 {
		t.Fatalf("%+v", got)
	}
}

func TestRecordRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "claude-sync.json")
	r, err := LoadRecord(p)
	if err != nil || len(r) != 0 || r == nil {
		t.Fatalf("missing record: %q %v", r, err)
	}
	if err := SaveRecord(p, []string{"skills/b", "agents/a"}); err != nil {
		t.Fatal(err)
	}
	r, err = LoadRecord(p)
	if err != nil || !reflect.DeepEqual(r, []string{"agents/a", "skills/b"}) {
		t.Fatalf("%q %v", r, err)
	}
	if err := os.WriteFile(p, []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRecord(p); err == nil || !strings.Contains(err.Error(), p) {
		t.Fatalf("bad JSON: %v", err)
	}
}
