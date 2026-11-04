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

func TestPlaceRanges(t *testing.T) {
	files := []api.DiffFile{{Path: "a.go", Hunks: []api.Hunk{
		{Rows: []api.DiffRow{
			{Kind: "ctx", Old: 1, New: 1, Text: "a"},
			{Kind: "del", Old: 2, Text: "gone 1"},
			{Kind: "del", Old: 3, Text: "gone 2"},
			{Kind: "add", New: 2, Text: "b"},
			{Kind: "add", New: 3, Text: "c"},
		}},
		{Rows: []api.DiffRow{
			{Kind: "ctx", Old: 20, New: 20, Text: "a"},
			{Kind: "add", New: 21, Text: "b"},
			{Kind: "del", Old: 21, Text: "gone 1"},
			{Kind: "ctx", Old: 22, New: 22, Text: "keep"},
			{Kind: "del", Old: 23, Text: "gone 2"},
		}},
	}}}
	cs := []api.Comment{
		{ID: "same", File: "a.go", Side: "new", StartLine: 1, Line: 3, Code: "?"},                // the lines are there
		{ID: "moved", File: "a.go", Side: "new", StartLine: 18, Line: 19, Code: "a\nb"},          // nearest copy: 20-21
		{ID: "split", File: "a.go", Side: "new", StartLine: 3, Line: 20, Code: "c\n\n\na"},       // two hunks: outdated
		{ID: "old", File: "a.go", Side: "old", StartLine: 2, Line: 3, Code: "gone 1\ngone 2"},    // removed run kept
		{ID: "gap", File: "a.go", Side: "old", StartLine: 40, Line: 41, Code: "gone 1\ngone 2"},  // 21 and 23 aren't a run: 2-3
		{ID: "lost", File: "a.go", Side: "new", StartLine: 50, Line: 51, Code: "nowhere\nfound"}, // outdated, lines kept
	}
	_, got := Place(files, cs)
	want := map[string][3]int{"same": {1, 3, 0}, "moved": {20, 21, 0}, "split": {3, 20, 1}, "old": {2, 3, 0}, "gap": {2, 3, 0}, "lost": {50, 51, 1}}
	for _, c := range got {
		out := 0
		if c.Outdated {
			out = 1
		}
		if w := want[c.ID]; [3]int{c.StartLine, c.Line, out} != w {
			t.Errorf("%s: %d-%d outdated %v, want %v", c.ID, c.StartLine, c.Line, c.Outdated, w)
		}
	}
}
