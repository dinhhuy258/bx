package claudesync

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestMakePlan(t *testing.T) {
	f := func(p, sum string, exec bool) File { return File{Path: p, SHA256: sum, Exec: exec} }
	srv := func(fs ...File) map[string]File {
		m := map[string]File{}
		for _, x := range fs {
			m[x.Path] = x
		}
		return m
	}
	for _, tc := range []struct {
		name   string
		mac    []File
		server map[string]File
		record []string
		want   []Item
	}{
		{"new", []File{f("skills/a/SKILL.md", "1", false), f("skills/a/x/y.md", "1", false)}, srv(), nil,
			[]Item{{Name: "skills/a", Status: StatusNew, Add: []string{"skills/a/SKILL.md", "skills/a/x/y.md"}}}},
		{"changed by content", []File{f("agents/a.md", "2", false)}, srv(f("agents/a.md", "1", false)), []string{"agents/a.md"},
			[]Item{{Name: "agents/a.md", Status: StatusChanged, Update: []string{"agents/a.md"}}}},
		{"changed by exec bit", []File{f("agents/a.md", "1", true)}, srv(f("agents/a.md", "1", false)), []string{"agents/a.md"},
			[]Item{{Name: "agents/a.md", Status: StatusChanged, Update: []string{"agents/a.md"}}}},
		{"changed by an add", []File{f("skills/a/SKILL.md", "1", false), f("skills/a/new.md", "1", false)},
			srv(f("skills/a/SKILL.md", "1", false)), []string{"skills/a/SKILL.md"},
			[]Item{{Name: "skills/a", Status: StatusChanged, Add: []string{"skills/a/new.md"}}}},
		{"changed by a delete", []File{f("skills/a/SKILL.md", "1", false)},
			srv(f("skills/a/SKILL.md", "1", false), f("skills/a/old.md", "1", false), f("skills/a/mine.md", "1", false)),
			[]string{"skills/a/SKILL.md", "skills/a/old.md"},
			[]Item{{Name: "skills/a", Status: StatusChanged, Delete: []string{"skills/a/old.md"}}}},
		{"same", []File{f("agents/sub/a.md", "1", false)}, srv(f("agents/sub/a.md", "1", false)), nil,
			[]Item{{Name: "agents/sub", Status: StatusSame}}},
		{"removed", nil, srv(f("skills/a/SKILL.md", "1", false), f("skills/a/mine.md", "1", false)),
			[]string{"skills/a/SKILL.md", "skills/a/gone.md"},
			[]Item{{Name: "skills/a", Status: StatusRemoved, Delete: []string{"skills/a/SKILL.md"}}}},
		{"recorded, gone from both", nil, srv(), []string{"skills/a/SKILL.md"}, []Item{}},
		{"server-only", nil, srv(f("rules/mine.md", "1", false)), nil,
			[]Item{{Name: "rules/mine.md", Status: StatusServerOnly}}},
		{"several, sorted", []File{f("skills/b/SKILL.md", "1", false), f("agents/a.md", "1", false)},
			srv(f("agents/a.md", "1", false), f("commands/mine.md", "1", false)), nil,
			[]Item{
				{Name: "agents/a.md", Status: StatusSame},
				{Name: "commands/mine.md", Status: StatusServerOnly},
				{Name: "skills/b", Status: StatusNew, Add: []string{"skills/b/SKILL.md"}},
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := MakePlan(t.TempDir(), tc.mac, tc.server, tc.record)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestMakePlanReportsAServerSymlinkPerItem(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	link(t, outside, filepath.Join(dir, "skills/a"))
	mac := []File{{Path: "skills/a/SKILL.md", SHA256: "1"}, {Path: "skills/b/SKILL.md", SHA256: "1"}}
	got := MakePlan(dir, mac, map[string]File{}, nil)
	want := []Item{
		{Name: "skills/a", Status: StatusNew, Add: []string{"skills/a/SKILL.md"}, Error: "skills/a: refusing to write through a symlink"},
		{Name: "skills/b", Status: StatusNew, Add: []string{"skills/b/SKILL.md"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

// fixture is a server Claude dir and a record path in a separate bx root.
func fixture(t *testing.T) (dir, record string) {
	t.Helper()
	return filepath.Join(t.TempDir(), "claude"), filepath.Join(t.TempDir(), "claude-sync.json")
}

func mode(t *testing.T, p string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

func record(t *testing.T, p string) []string {
	t.Helper()
	r, err := LoadRecord(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func save(t *testing.T, p string, paths []string) {
	t.Helper()
	if err := SaveRecord(p, paths); err != nil {
		t.Fatal(err)
	}
}

func TestApplyWritesWithModesAndCreatesDir(t *testing.T) {
	dir, rec := fixture(t)
	files := []Write{{Path: "skills/a/SKILL.md", Content: []byte("a")}, {Path: "skills/a/run.sh", Exec: true, Content: []byte("x")}}
	res, err := Apply(dir, rec, []string{"skills/a"}, []string{"skills/a/SKILL.md", "skills/a/run.sh"}, files, nil)
	if err != nil || res != (Result{Added: 2}) {
		t.Fatalf("%+v %v", res, err)
	}
	if m := mode(t, dir); m != 0o700 {
		t.Errorf("dir mode %o", m)
	}
	if m := mode(t, filepath.Join(dir, "skills/a")); m != 0o755 {
		t.Errorf("parent mode %o", m)
	}
	if m := mode(t, filepath.Join(dir, "skills/a/SKILL.md")); m != 0o644 {
		t.Errorf("file mode %o", m)
	}
	if m := mode(t, filepath.Join(dir, "skills/a/run.sh")); m != 0o755 {
		t.Errorf("exec mode %o", m)
	}
	if got := record(t, rec); !reflect.DeepEqual(got, []string{"skills/a/SKILL.md", "skills/a/run.sh"}) {
		t.Errorf("record %q", got)
	}
	// update overwrites, and the mode follows the Mac
	res, err = Apply(dir, rec, []string{"skills/a"}, []string{"skills/a/SKILL.md", "skills/a/run.sh"}, []Write{{Path: "skills/a/run.sh", Content: []byte("y")}}, nil)
	if err != nil || res != (Result{Updated: 1}) {
		t.Fatalf("%+v %v", res, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "skills/a/run.sh")); string(b) != "y" || mode(t, filepath.Join(dir, "skills/a/run.sh")) != 0o644 {
		t.Errorf("update: %q", b)
	}
}

func TestApplyDeletePrunesEmptyFolders(t *testing.T) {
	dir, rec := fixture(t)
	put(t, dir, "skills/a/b/x.md", "x", 0o644)
	put(t, dir, "skills/k/y.md", "y", 0o644)
	put(t, dir, "skills/k/mine.md", "m", 0o644)
	save(t, rec, []string{"skills/a/b/x.md", "skills/k/y.md"})
	res, err := Apply(dir, rec, []string{"skills/a", "skills/k"}, nil, nil, []string{"skills/a/b/x.md", "skills/k/y.md"})
	if err != nil || res != (Result{Deleted: 2}) {
		t.Fatalf("%+v %v", res, err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "skills/a")); !os.IsNotExist(err) {
		t.Errorf("skills/a should be gone: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "skills")); err != nil {
		t.Errorf("skills/ must stay: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "skills/k/mine.md")); err != nil {
		t.Errorf("a non-empty parent must stay: %v", err)
	}
	if got := record(t, rec); len(got) != 0 {
		t.Errorf("record %q", got)
	}
	// the only file of the top folder: skills/ itself stays
	put(t, dir, "rules/r.md", "r", 0o644)
	save(t, rec, []string{"rules/r.md"})
	if _, err := Apply(dir, rec, []string{"rules/r.md"}, nil, nil, []string{"rules/r.md"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "rules")); err != nil {
		t.Errorf("rules/ must stay: %v", err)
	}
}

func TestApplyRefusesBeforeTouchingDisk(t *testing.T) {
	big := make([]byte, MaxFile+1)
	mb := make([]byte, MaxFile)
	var many []Write
	var manyItems []string
	for i := range 21 {
		p := "skills/f" + string(rune('a'+i))
		many = append(many, Write{Path: p, Content: mb})
		manyItems = append(manyItems, p)
	}
	for _, tc := range []struct {
		name    string
		items   []string
		paths   []string
		files   []Write
		deletes []string
		want    error
	}{
		{"no items", nil, nil, nil, nil, ErrInvalid},
		{"bad item", []string{"other/a"}, nil, nil, nil, ErrInvalid},
		{"a path, not an item", []string{"skills/a/SKILL.md"}, nil, nil, nil, ErrInvalid},
		{"dotdot", []string{"skills/a"}, nil, []Write{{Path: "../x"}}, nil, ErrInvalid},
		{"hidden", []string{"skills/a"}, nil, []Write{{Path: "skills/a/.hidden"}}, nil, ErrInvalid},
		{"unknown folder", []string{"skills/a"}, nil, []Write{{Path: "other/a.md"}}, nil, ErrInvalid},
		{"write outside the items", []string{"skills/a"}, nil, []Write{{Path: "skills/b/SKILL.md"}}, nil, ErrInvalid},
		{"path outside the items", []string{"skills/a"}, []string{"agents/x.md"}, nil, nil, ErrInvalid},
		{"bad path in paths", []string{"skills/a"}, []string{"/abs"}, nil, nil, ErrInvalid},
		{"delete outside the items", []string{"skills/a"}, nil, nil, []string{"skills/b/SKILL.md"}, ErrInvalid},
		{"delete not in record", []string{"skills/a"}, nil, nil, []string{"skills/a/mine.md"}, ErrInvalid},
		{"delete the Mac has", []string{"skills/a"}, []string{"skills/a/x.md"}, nil, []string{"skills/a/x.md"}, ErrInvalid},
		{"file over 1 MB", []string{"skills/big"}, nil, []Write{{Path: "skills/big", Content: big}}, nil, ErrTooLarge},
		{"total over 20 MB", manyItems, nil, many, nil, ErrTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, rec := fixture(t)
			_, err := Apply(dir, rec, tc.items, tc.paths, tc.files, tc.deletes)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if _, err := os.Lstat(dir); !os.IsNotExist(err) {
				t.Errorf("dir was touched: %v", err)
			}
			if _, err := os.Lstat(rec); !os.IsNotExist(err) {
				t.Errorf("record was written: %v", err)
			}
		})
	}
}

func TestApplyRefusesSymlinks(t *testing.T) {
	dir, rec := fixture(t)
	outside := t.TempDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	link(t, outside, filepath.Join(dir, "skills/x"))
	put(t, outside, "t.md", "theirs", 0o644)
	link(t, filepath.Join(outside, "t.md"), filepath.Join(dir, "agents/t.md"))
	for _, p := range []string{"skills/x/SKILL.md", "agents/t.md"} {
		_, err := Apply(dir, rec, []string{ItemOf(p)}, []string{p}, []Write{{Path: p, Content: []byte("mine")}}, nil)
		if err == nil || !strings.Contains(err.Error(), "symlink") {
			t.Errorf("%s: %v", p, err)
		}
	}
	if _, err := os.Stat(filepath.Join(outside, "SKILL.md")); !os.IsNotExist(err) {
		t.Errorf("wrote through the symlink: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(outside, "t.md")); string(b) != "theirs" {
		t.Errorf("overwrote the link target: %q", b)
	}
	if got := record(t, rec); len(got) != 0 {
		t.Errorf("record %q", got)
	}
	// a delete through a symlinked folder is refused too
	put(t, outside, "SKILL.md", "theirs", 0o644)
	save(t, rec, []string{"skills/x/SKILL.md"})
	if _, err := Apply(dir, rec, []string{"skills/x"}, nil, nil, []string{"skills/x/SKILL.md"}); err == nil {
		t.Error("delete through a symlink must fail")
	}
	if _, err := os.Stat(filepath.Join(outside, "SKILL.md")); err != nil {
		t.Errorf("deleted through the symlink: %v", err)
	}
}

func TestApplyRecord(t *testing.T) {
	dir, rec := fixture(t)
	put(t, dir, "skills/a/same.md", "s", 0o644)
	put(t, dir, "skills/b/SKILL.md", "b", 0o644)
	put(t, dir, "agents/a.md", "a", 0o644)
	save(t, rec, []string{"agents/a.md", "agents/gone.md", "skills/b/SKILL.md"})
	paths := []string{"skills/a/same.md", "skills/a/new.md"}
	if _, err := Apply(dir, rec, []string{"skills/a"}, paths, []Write{{Path: "skills/a/new.md", Content: []byte("n")}}, nil); err != nil {
		t.Fatal(err)
	}
	// other items' entries stay as they were; the unchanged file is adopted
	want := []string{"agents/a.md", "agents/gone.md", "skills/a/new.md", "skills/a/same.md", "skills/b/SKILL.md"}
	if got := record(t, rec); !reflect.DeepEqual(got, want) {
		t.Fatalf("record %q, want %q", got, want)
	}
	for p, c := range map[string]string{"agents/a.md": "a", "skills/b/SKILL.md": "b"} {
		if b, _ := os.ReadFile(filepath.Join(dir, p)); string(b) != c {
			t.Errorf("%s touched: %q", p, b)
		}
	}
}

// An unchecked item is out of the scope: never written, never deleted, its record kept.
func TestApplyLeavesAnUncheckedItemAlone(t *testing.T) {
	dir, rec := fixture(t)
	put(t, dir, "skills/a/SKILL.md", "old", 0o644)
	put(t, dir, "skills/gone/SKILL.md", "g", 0o644)
	save(t, rec, []string{"skills/a/SKILL.md", "skills/gone/SKILL.md"})
	// the Mac changed skills/a and removed skills/gone; only skills/new is checked
	mac := []File{{Path: "skills/a/SKILL.md", SHA256: "new"}, {Path: "skills/new/SKILL.md", SHA256: "n"}}
	server, err := ScanServer(dir)
	if err != nil {
		t.Fatal(err)
	}
	plan := MakePlan(dir, mac, server, record(t, rec))
	st := map[string]string{}
	for _, it := range plan {
		st[it.Name] = it.Status
	}
	if want := map[string]string{"skills/a": StatusChanged, "skills/gone": StatusRemoved, "skills/new": StatusNew}; !reflect.DeepEqual(st, want) {
		t.Fatalf("plan %v, want %v", st, want)
	}
	res, err := Apply(dir, rec, []string{"skills/new"}, []string{"skills/new/SKILL.md"}, []Write{{Path: "skills/new/SKILL.md", Content: []byte("n")}}, nil)
	if err != nil || res != (Result{Added: 1}) {
		t.Fatalf("%+v %v", res, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "skills/a/SKILL.md")); string(b) != "old" {
		t.Errorf("skills/a was written: %q", b)
	}
	if _, err := os.Stat(filepath.Join(dir, "skills/gone/SKILL.md")); err != nil {
		t.Errorf("skills/gone was deleted: %v", err)
	}
	want := []string{"skills/a/SKILL.md", "skills/gone/SKILL.md", "skills/new/SKILL.md"}
	if got := record(t, rec); !reflect.DeepEqual(got, want) {
		t.Fatalf("record %q, want %q", got, want)
	}
	// now in scope, the removed item is deleted
	res, err = Apply(dir, rec, []string{"skills/gone"}, nil, nil, []string{"skills/gone/SKILL.md"})
	if err != nil || res != (Result{Deleted: 1}) {
		t.Fatalf("%+v %v", res, err)
	}
	if want := []string{"skills/a/SKILL.md", "skills/new/SKILL.md"}; !reflect.DeepEqual(record(t, rec), want) {
		t.Fatalf("record %q, want %q", record(t, rec), want)
	}
}

func TestApplyPartialFailureKeepsRecordConsistent(t *testing.T) {
	dir, rec := fixture(t)
	outside := t.TempDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	link(t, outside, filepath.Join(dir, "skills/blocked"))
	put(t, dir, "skills/old/SKILL.md", "o", 0o644)
	save(t, rec, []string{"skills/old/SKILL.md"})
	items := []string{"skills/a.md", "skills/blocked", "skills/c.md", "skills/old"}
	paths := []string{"skills/a.md", "skills/blocked/b.md", "skills/c.md"}
	files := []Write{{Path: "skills/a.md", Content: []byte("a")}, {Path: "skills/blocked/b.md", Content: []byte("b")}, {Path: "skills/c.md", Content: []byte("c")}}
	res, err := Apply(dir, rec, items, paths, files, []string{"skills/old/SKILL.md"})
	if err == nil || !strings.Contains(err.Error(), "skills/blocked/b.md") {
		t.Fatalf("want an error naming the failed path, got %v", err)
	}
	if res != (Result{Added: 1}) {
		t.Errorf("res %+v", res)
	}
	// on disk: a.md and old/SKILL.md (its delete never ran); the record says exactly that
	if got := record(t, rec); !reflect.DeepEqual(got, []string{"skills/a.md", "skills/old/SKILL.md"}) {
		t.Fatalf("record %q", got)
	}
}
