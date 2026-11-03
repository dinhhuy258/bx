package review

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

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
