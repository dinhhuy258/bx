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
	all := Folders
	for _, tc := range []struct {
		name    string
		mac     []File
		server  map[string]File
		record  []string
		folders []string
		want    Plan
	}{
		{"add", []File{f("skills/a", "1", false)}, srv(), nil, all,
			Plan{Add: []string{"skills/a"}}},
		{"update by content", []File{f("skills/a", "2", false)}, srv(f("skills/a", "1", false)), []string{"skills/a"}, all,
			Plan{Update: []string{"skills/a"}}},
		{"update by exec bit", []File{f("skills/a", "1", true)}, srv(f("skills/a", "1", false)), []string{"skills/a"}, all,
			Plan{Update: []string{"skills/a"}}},
		{"unchanged", []File{f("skills/a", "1", false)}, srv(f("skills/a", "1", false)), []string{"skills/a"}, all,
			Plan{Unchanged: 1}},
		{"delete", nil, srv(f("skills/a", "1", false)), []string{"skills/a"}, all,
			Plan{Delete: []string{"skills/a"}}},
		{"recorded, gone from both", nil, srv(), []string{"skills/a"}, all,
			Plan{}},
		{"kept", nil, srv(f("skills/mine", "1", false)), nil, all,
			Plan{Kept: 1}},
		{"scope", []File{f("skills/a", "1", false), f("agents/new", "1", false)},
			srv(f("skills/a", "1", false), f("agents/old", "1", false), f("agents/mine", "1", false)),
			[]string{"skills/a", "agents/old"}, []string{"skills"},
			Plan{Unchanged: 1}},
		{"first sync", []File{f("skills/same", "1", false), f("skills/diff", "2", false)},
			srv(f("skills/same", "1", false), f("skills/diff", "1", false)), nil, all,
			Plan{Update: []string{"skills/diff"}, Unchanged: 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := MakePlan(tc.mac, tc.server, tc.record, tc.folders)
			for _, s := range []*[]string{&tc.want.Add, &tc.want.Update, &tc.want.Delete} {
				if *s == nil {
					*s = []string{}
				}
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
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
	res, err := Apply(dir, rec, []string{"skills"}, []string{"skills/a/SKILL.md", "skills/a/run.sh"}, files, nil)
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
	res, err = Apply(dir, rec, []string{"skills"}, []string{"skills/a/SKILL.md", "skills/a/run.sh"}, []Write{{Path: "skills/a/run.sh", Content: []byte("y")}}, nil)
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
	res, err := Apply(dir, rec, []string{"skills"}, nil, nil, []string{"skills/a/b/x.md", "skills/k/y.md"})
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
	if _, err := Apply(dir, rec, []string{"rules"}, nil, nil, []string{"rules/r.md"}); err != nil {
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
	for i := range 21 {
		many = append(many, Write{Path: "skills/f" + string(rune('a'+i)), Content: mb})
	}
	for _, tc := range []struct {
		name    string
		folders []string
		paths   []string
		files   []Write
		deletes []string
		want    error
	}{
		{"bad folder", []string{"other"}, nil, nil, nil, ErrInvalid},
		{"dotdot", []string{"skills"}, nil, []Write{{Path: "../x"}}, nil, ErrInvalid},
		{"hidden", []string{"skills"}, nil, []Write{{Path: "skills/.hidden"}}, nil, ErrInvalid},
		{"outside the selected folders", []string{"skills"}, nil, []Write{{Path: "agents/a.md"}}, nil, ErrInvalid},
		{"bad path in paths", []string{"skills"}, []string{"/abs"}, nil, nil, ErrInvalid},
		{"delete not in record", []string{"skills"}, nil, nil, []string{"skills/mine.md"}, ErrInvalid},
		{"file over 1 MB", []string{"skills"}, nil, []Write{{Path: "skills/big", Content: big}}, nil, ErrTooLarge},
		{"total over 20 MB", []string{"skills"}, nil, many, nil, ErrTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, rec := fixture(t)
			_, err := Apply(dir, rec, tc.folders, tc.paths, tc.files, tc.deletes)
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
	link(t, filepath.Join(outside, "t.md"), filepath.Join(dir, "skills/t.md"))
	for _, p := range []string{"skills/x/SKILL.md", "skills/t.md"} {
		_, err := Apply(dir, rec, []string{"skills"}, []string{p}, []Write{{Path: p, Content: []byte("mine")}}, nil)
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
	if _, err := Apply(dir, rec, []string{"skills"}, nil, nil, []string{"skills/x/SKILL.md"}); err == nil {
		t.Error("delete through a symlink must fail")
	}
	if _, err := os.Stat(filepath.Join(outside, "SKILL.md")); err != nil {
		t.Errorf("deleted through the symlink: %v", err)
	}
}

func TestApplyRecord(t *testing.T) {
	dir, rec := fixture(t)
	put(t, dir, "skills/same.md", "s", 0o644)
	put(t, dir, "agents/a.md", "a", 0o644)
	save(t, rec, []string{"agents/a.md", "agents/gone.md"})
	paths := []string{"skills/same.md", "skills/new.md"}
	if _, err := Apply(dir, rec, []string{"skills"}, paths, []Write{{Path: "skills/new.md", Content: []byte("n")}}, nil); err != nil {
		t.Fatal(err)
	}
	// other folders' entries stay as they were; the unchanged file is adopted
	want := []string{"agents/a.md", "agents/gone.md", "skills/new.md", "skills/same.md"}
	if got := record(t, rec); !reflect.DeepEqual(got, want) {
		t.Fatalf("record %q, want %q", got, want)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "agents/a.md")); string(b) != "a" {
		t.Errorf("agents touched: %q", b)
	}
}

func TestApplyPartialFailureKeepsRecordConsistent(t *testing.T) {
	dir, rec := fixture(t)
	outside := t.TempDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	link(t, outside, filepath.Join(dir, "skills/blocked"))
	put(t, dir, "skills/old.md", "o", 0o644)
	save(t, rec, []string{"skills/old.md"})
	paths := []string{"skills/a.md", "skills/blocked/b.md", "skills/c.md"}
	files := []Write{{Path: "skills/a.md", Content: []byte("a")}, {Path: "skills/blocked/b.md", Content: []byte("b")}, {Path: "skills/c.md", Content: []byte("c")}}
	res, err := Apply(dir, rec, []string{"skills"}, paths, files, []string{"skills/old.md"})
	if err == nil || !strings.Contains(err.Error(), "skills/blocked/b.md") {
		t.Fatalf("want an error naming the failed path, got %v", err)
	}
	if res != (Result{Added: 1}) {
		t.Errorf("res %+v", res)
	}
	// on disk: a.md and old.md (its delete never ran); the record says exactly that
	if got := record(t, rec); !reflect.DeepEqual(got, []string{"skills/a.md", "skills/old.md"}) {
		t.Fatalf("record %q", got)
	}
}
