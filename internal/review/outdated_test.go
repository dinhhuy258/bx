package review

import (
	"testing"

	"github.com/dinhhuy258/bx/internal/api"
)

func TestPlace(t *testing.T) {
	files := []api.DiffFile{
		{Path: "a.go", Hunks: []api.Hunk{{Rows: []api.DiffRow{
			{Kind: "ctx", Old: 9, New: 10, Text: "x"},
			{Kind: "del", Old: 10, Text: "old"},
			{Kind: "add", New: 11, Text: "moved here"},
		}}}},
		{Path: "big.go", Folded: "3000 lines changed · too long to show"},
	}
	cs := []api.Comment{
		{ID: "1", File: "a.go", Side: "new", Line: 11, Code: "anything"},  // the line still exists
		{ID: "2", File: "a.go", Side: "new", Line: 5, Code: "moved here"}, // its text moved to 11
		{ID: "3", File: "a.go", Side: "old", Line: 10, Code: "old"},       // removed line still there
		{ID: "4", File: "a.go", Side: "old", Line: 3, Code: "gone"},       // nowhere: outdated
		{ID: "5", File: "big.go", Side: "new", Line: 7},                   // folded: unchecked
		{ID: "6", File: "c.go", Side: "new", Line: 1},                     // file no longer changed
		{ID: "7", File: "c.go", Side: "new", Line: 2},
	}
	fs, got := Place(files, cs)
	want := map[string]struct {
		line     int
		outdated bool
	}{"1": {11, false}, "2": {11, false}, "3": {10, false}, "4": {3, true}, "5": {7, false}, "6": {1, true}, "7": {2, true}}
	for _, c := range got {
		if w := want[c.ID]; c.Line != w.line || c.Outdated != w.outdated {
			t.Errorf("%s: line %d outdated %v, want %+v", c.ID, c.Line, c.Outdated, w)
		}
	}
	if len(fs) != 3 || fs[2].Path != "c.go" || fs[2].Status != "gone" || fs[2].Folded != "no longer changed" {
		t.Fatalf("files %+v", fs)
	}
	if cs[1].Line != 5 {
		t.Fatal("Place must not change the caller's comments")
	}
}
