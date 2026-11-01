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
	all := Folders
	for _, tc := range []struct {
		p       string
		folders []string
		ok      bool
	}{
		{"skills/a/SKILL.md", all, true},
		{"rules/x.md", all, true},
		{"", all, false},
		{"../x", all, false},
		{"/abs", all, false},
		{"/skills/a", all, false},
		{"skills/../agents/x", all, false},
		{"skills/../x", all, false},
		{"skills//a", all, false},
		{"skills/./a", all, false},
		{"skills/.git/x", all, false},
		{"hidden/.x", all, false},
		{"skills/a/", all, false},
		{`skills/a\b`, all, false},
		{"skills/a\x00", all, false},
		{"settings.json", all, false},
		{"other/a", all, false},
		{"skills", all, false},
		{"agents/a.md", []string{"skills"}, false},
		{"other/a", []string{"other"}, false},
	} {
		err := ValidPath(tc.p, tc.folders)
		if (err == nil) != tc.ok {
			t.Errorf("ValidPath(%q, %v) = %v, want ok=%v", tc.p, tc.folders, err, tc.ok)
		}
		if err != nil && !errors.Is(err, ErrInvalid) {
			t.Errorf("ValidPath(%q): %v is not ErrInvalid", tc.p, err)
		}
	}
}

func TestValidFolders(t *testing.T) {
	if err := ValidFolders([]string{"skills", "rules"}); err != nil {
		t.Fatal(err)
	}
	for _, f := range [][]string{nil, {"other"}, {"skills", "../x"}} {
		if err := ValidFolders(f); !errors.Is(err, ErrInvalid) {
			t.Errorf("ValidFolders(%q) = %v", f, err)
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
	link(t, filepath.Join(outside, "real.md"), filepath.Join(mac, "skills/linked.md"))
	link(t, filepath.Join(outside, "dir"), filepath.Join(mac, "skills/linkdir"))
	link(t, "..", filepath.Join(mac, "skills/a/loop"))              // a loop back to skills/
	link(t, "/nowhere/at/all", filepath.Join(mac, "skills/broken")) // a broken link

	files, err := Scan(mac, []string{"skills", "commands"}) // commands is missing
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"skills/a/SKILL.md", "skills/a/run.sh", "skills/linkdir/c.md", "skills/linked.md"}
	if got := paths(files); !reflect.DeepEqual(got, want) {
		t.Fatalf("paths = %q, want %q", got, want)
	}
	by := map[string]File{}
	for _, f := range files {
		by[f.Path] = f
	}
	if !by["skills/a/run.sh"].Exec || by["skills/a/SKILL.md"].Exec {
		t.Errorf("exec bits: %+v", files)
	}
	if f := by["skills/linked.md"]; f.Size != 4 || f.SHA256 == "" || f.Src != filepath.Join(mac, "skills/linked.md") {
		t.Errorf("linked file: %+v", f)
	}
}

func TestScanSymlinkedTopFolderAndBadName(t *testing.T) {
	mac, outside := t.TempDir(), t.TempDir()
	put(t, outside, "x/SKILL.md", "x", 0o644)
	link(t, outside, filepath.Join(mac, "skills"))
	files, err := Scan(mac, []string{"skills"})
	if err != nil || !reflect.DeepEqual(paths(files), []string{"skills/x/SKILL.md"}) {
		t.Fatalf("%q %v", paths(files), err)
	}
	put(t, mac, `agents/a\b.md`, "x", 0o644)
	if _, err := Scan(mac, []string{"agents"}); err == nil || !strings.Contains(err.Error(), `a\b.md`) {
		t.Fatalf("want an error naming the file, got %v", err)
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
	got, err := ScanServer(srv, []string{"skills", "agents", "rules"})
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
