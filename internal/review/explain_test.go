package review

import (
	"path/filepath"
	"testing"

	"github.com/dinhhuy258/bx/internal/api"
)

func TestExplainFileRoundTripAndTrim(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s1.explain.json")
	if f, err := LoadExplains(path); err != nil || len(f.Explains) != 0 {
		t.Fatalf("missing file: %+v %v", f, err)
	}
	var f ExplainFile
	for i := range MaxExplains + 2 {
		f.Explains = append(f.Explains, api.Explain{ID: string(rune('a' + i)), State: api.ExplainDone})
	}
	f.Explains[0].State = api.ExplainRunning // a running one is never dropped
	f.Explains = TrimExplains(f.Explains)
	if len(f.Explains) != MaxExplains || f.Explains[0].ID != "a" || f.Explains[1].ID != "d" {
		t.Fatalf("trim: %d %s %s", len(f.Explains), f.Explains[0].ID, f.Explains[1].ID)
	}
	f.With = 3
	if err := SaveExplains(path, f); err != nil {
		t.Fatal(err)
	}
	if g, err := LoadExplains(path); err != nil || len(g.Explains) != MaxExplains || g.With != 3 {
		t.Fatalf("load: %+v %v", g, err)
	}
}

func TestPlaceExplains(t *testing.T) {
	files := []api.DiffFile{{Path: "a.go", Hunks: []api.Hunk{{Rows: []api.DiffRow{
		{Kind: "ctx", Old: 1, New: 1, Text: "a"}, {Kind: "add", New: 2, Text: "b"}}}}}}
	cs := []api.Comment{{ID: "c", File: "a.go", Side: "new", Line: 2, Code: "b"}}
	es := []api.Explain{
		{ID: "moved", File: "a.go", Side: "new", StartLine: 5, Line: 6, Code: "a\nb"},
		{ID: "gone", File: "b.go", Side: "new", Line: 1, Code: "z"},
	}
	fs, gotC, gotE := PlaceAll(files, cs, es)
	if len(gotC) != 1 || gotC[0].Line != 2 || gotC[0].Outdated {
		t.Fatalf("comments %+v", gotC)
	}
	if gotE[0].StartLine != 1 || gotE[0].Line != 2 || gotE[0].Outdated || !gotE[1].Outdated {
		t.Fatalf("explains %+v", gotE)
	}
	if len(fs) != 2 || fs[1].Path != "b.go" || fs[1].Status != "gone" {
		t.Fatalf("files %+v", fs)
	}
}
