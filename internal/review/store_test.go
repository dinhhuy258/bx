package review

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dinhhuy258/bx/internal/api"
	"github.com/dinhhuy258/bx/internal/state"
)

func TestStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "review", "p", "s1.json")
	f, err := Load(path)
	if err != nil || f.Version != 0 || f.Comments != nil {
		t.Fatalf("missing file: %+v %v", f, err)
	}
	want := File{Version: 3, Comments: []api.Comment{{ID: "c1", File: "a.go", Side: "new", Line: 2, Body: "x"}}}
	if err := Save(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil || got.Version != 3 || len(got.Comments) != 1 || got.Comments[0] != want.Comments[0] {
		t.Fatalf("%+v %v", got, err)
	}
	os.WriteFile(path, []byte("{nope"), 0o644)
	_, err = Load(path)
	var ce *state.CorruptError
	if !errors.As(err, &ce) {
		t.Fatalf("corrupt: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("the corrupt file must be moved aside")
	}
}

func TestHashAndViewed(t *testing.T) {
	a := api.DiffFile{Path: "a.go", Status: "added", Hunks: []api.Hunk{{Header: "@@", Rows: []api.DiffRow{{Kind: "add", New: 1, Text: "x"}}}}}
	b := a
	b.Hunks = []api.Hunk{{Header: "@@", Rows: []api.DiffRow{{Kind: "add", New: 1, Text: "y"}}}}
	if Hash(a) == "" || Hash(a) == Hash(b) || Hash(api.DiffFile{Path: "g", Status: "gone"}) != "" {
		t.Fatal("hash")
	}
	gone := api.DiffFile{Path: "old.go", Status: "gone"}
	marks := map[string]Mark{"a.go": {Hash: Hash(b)}, "left.go": {Hash: "z"}}
	files := []api.DiffFile{a, gone}
	MarkViewed(files, marks)
	if files[0].Viewed != api.FileChanged || files[1].Viewed != "" || files[1].Hash != "" {
		t.Fatalf("%+v", files)
	}
	got, err := SetViewed(marks, files, api.ReviewViewed{Path: "a.go", Viewed: true}, time.Unix(5, 0))
	if err != nil || len(got) != 1 || got["a.go"] != (Mark{Hash: Hash(a), At: 5}) {
		t.Fatalf("%+v %v: marked at the current hash, left.go pruned", got, err)
	}
	if got, _ = SetViewed(got, files, api.ReviewViewed{Path: "a.go"}, time.Now()); got != nil {
		t.Fatalf("unmarked: %+v", got)
	}
	if _, err = SetViewed(nil, files, api.ReviewViewed{Path: "old.go", Viewed: true}, time.Now()); err == nil {
		t.Fatal("a gone file can't be marked")
	}
}
