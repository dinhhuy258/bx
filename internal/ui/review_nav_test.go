package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// navReview is four files; the first has 40 added lines, for paging.
func navReview() ReviewData {
	var rows []DiffRow
	for i := 1; i <= 40; i++ {
		rows = append(rows, DiffRow{Kind: "add", New: i, Text: fmt.Sprintf("line %d", i)})
	}
	file := func(path string) DiffFile {
		return DiffFile{Path: path, Status: "added", Add: 1, Hash: "h-" + path,
			Hunks: []Hunk{{Header: "@@ -0,0 +1 @@", Rows: []DiffRow{{Kind: "add", New: 1, Text: "package x"}}}}}
	}
	long := DiffFile{Path: "internal/api/api.go", Status: "modified", Add: 40, Hash: "h-api",
		Hunks: []Hunk{{Header: "@@ -0,0 +1,40 @@", Rows: rows}}}
	return ReviewData{Base: "master", Total: 4, Files: []DiffFile{long,
		file("internal/prwatch/query.go"), file("internal/prwatch/query_test.go"), file("internal/serve/server.go")}}
}

func TestReviewPagesHalfAScreen(t *testing.T) {
	r := NewReview("s")
	r.SetData(navReview())
	r.Key("Enter", false)
	r.Render(120, 22, true, false) // a diff pane of 20 lines: a half page is 9
	r.Key("C-d", false)
	if l, _ := r.Selected(); l.Line != 10 {
		t.Fatalf("C-d: line %d", l.Line)
	}
	if !strings.Contains(ansi.Strip(r.Render(120, 22, true, false)), "line 10") || r.top != 9 {
		t.Fatalf("the view scrolls with the cursor: top %d", r.top)
	}
	r.Key("C-u", false)
	if l, _ := r.Selected(); l.Line != 1 || r.top != 0 {
		t.Fatalf("C-u: line %d top %d", l.Line, r.top)
	}
	for range 10 {
		r.Key("C-d", false)
	}
	if l, _ := r.Selected(); l.Line != 40 || l.File != "internal/api/api.go" {
		t.Fatalf("C-d stops at the end of the file: %+v", l)
	}
	r.Key("g", false)
	if l, _ := r.Selected(); l.Line != 1 {
		t.Fatalf("g: %d", l.Line)
	}
	r.Key("G", false)
	if l, _ := r.Selected(); l.Line != 40 {
		t.Fatalf("G: %d", l.Line)
	}
	r.Key("C-b", false)
	if l, _ := r.Selected(); l.Line != 21 {
		t.Fatalf("C-b is a page less a line: %d", l.Line)
	}
}

func TestReviewPagesTheNarrowFileList(t *testing.T) {
	r := NewReview("s")
	r.SetData(navReview())
	r.Render(40, 6, true, true) // a list of 4 lines: a half page is 1
	r.Key("C-d", true)
	r.Key("C-d", true)
	if r.file != 2 {
		t.Fatalf("file %d", r.file)
	}
	r.Key("G", true)
	if r.file != 3 {
		t.Fatalf("G: file %d", r.file)
	}
}

func TestReviewPagingKeepsARangeInItsHunk(t *testing.T) {
	r := NewReview("s")
	r.SetData(navReview())
	r.Render(120, 22, true, false)
	r.Key("Enter", false)
	r.Key("v", false)
	r.Key("C-d", false)
	if l, _ := r.Selected(); l.StartLine != 1 || l.Line != 10 {
		t.Fatalf("%+v", l)
	}
}

func TestMatchFilter(t *testing.T) {
	for _, c := range []struct {
		path, filter string
		want         bool
	}{
		{"internal/prwatch/query_test.go", "prwatch test", true},
		{"internal/prwatch/query_test.go", "PRWATCH", true},
		{"internal/prwatch/query.go", "prwatch test", false},
		{"internal/serve/server.go", "serve !_test", true},
		{"internal/serve/server_test.go", "serve !_test", false},
		{"a.go", "", true},
		{"a.go", "!", true},
	} {
		if got := MatchFilter(c.path, c.filter); got != c.want {
			t.Errorf("MatchFilter(%q, %q) = %v", c.path, c.filter, got)
		}
	}
}

func TestReviewFiltersFiles(t *testing.T) {
	r := NewReview("s")
	r.SetData(navReview())
	r.Key("/", false)
	if !r.Filtering() {
		t.Fatal("/ edits the filter")
	}
	for _, k := range []string{"p", "r", "w", "Space", "q", "u", "x", "BSpace"} {
		r.Key(k, false)
	}
	if r.filter != "prw qu" || r.file != 1 {
		t.Fatalf("filter %q, file %d: leaves a file the filter hides", r.filter, r.file)
	}
	v := ansi.Strip(r.Render(120, 10, true, false))
	if !strings.Contains(v, "2 of 4 files") || !strings.Contains(v, "/ prw qu▏") || strings.Contains(v, "server.go") {
		t.Fatalf("\n%s", v)
	}
	r.Key("Enter", false)
	r.Key("j", false)
	if r.file != 2 {
		t.Fatalf("j: file %d", r.file)
	}
	r.Key("j", false)
	if r.file != 2 {
		t.Fatalf("j stays on the last match: file %d", r.file)
	}
	if r.Key("Esc", false) != ReviewKeep || r.filter != "" || r.Key("Esc", false) != ReviewBack {
		t.Fatal("Esc clears the filter, then leaves")
	}
	r.Key("/", false)
	r.Key("z", false)
	v = ansi.Strip(r.Render(120, 10, true, false))
	if !strings.Contains(v, "0 of 4 files") || !strings.Contains(v, "no file matches") {
		t.Fatalf("\n%s", v)
	}
	r.Key("Esc", false)
	if r.filter != "" || r.Filtering() {
		t.Fatal("Esc while typing clears it")
	}
}

func TestReviewMarksFilesViewed(t *testing.T) {
	r := NewReview("s")
	d := navReview()
	d.Comments = []Comment{{ID: "c", File: "internal/prwatch/query.go", Side: "new", Line: 1, Body: "why"}}
	r.SetData(d)
	if r.Key("Space", false) != ReviewToggleViewed {
		t.Fatal("Space")
	}
	v, ok := r.ToggleViewed()
	if !ok || v != (ReviewViewed{Path: "internal/api/api.go", Hash: "h-api", Viewed: true}) || r.file != 1 {
		t.Fatalf("%+v %v, file %d: marks and goes on", v, ok, r.file)
	}
	r.ToggleViewed() // query.go, with its comment
	r.Key("k", false)
	view := ansi.Strip(r.Render(120, 10, true, false))
	for _, want := range []string{"2/4 viewed", "✓ viewed · internal/prwatch/query.go", "1 · why"} {
		if !strings.Contains(view, want) {
			t.Fatalf("no %q:\n%s", want, view)
		}
	}
	if l, ok := r.Selected(); !ok || l.Edit == nil {
		t.Fatalf("the comment of a viewed file stays at hand: %+v", l)
	}
	r.Key("H", false)
	view = ansi.Strip(r.Render(120, 10, true, false))
	if !strings.Contains(view, "2 of 4 files") || strings.Contains(view, "M internal/api/api.go") || r.file != 2 {
		t.Fatalf("H hides viewed files, file %d:\n%s", r.file, view)
	}
	r.Key("H", false)
	r.Key("g", false)
	r.setFile(0)
	if v, _ := r.ToggleViewed(); v.Viewed || r.file != 0 {
		t.Fatalf("Space again unmarks: %+v, file %d", v, r.file)
	}

	d = r.Data()
	d.Files[1].Viewed = FileChanged
	r.SetData(d)
	r.setFile(1)
	if view := ansi.Strip(r.Render(120, 10, true, false)); !strings.Contains(view, "◐ changed since you viewed it") {
		t.Fatalf("\n%s", view)
	}
	d.Files[1].Hash = "" // a gone file
	r.SetData(d)
	if r.Key("Space", false) != ReviewKeep {
		t.Fatal("a file without a diff can't be marked")
	}
}

func TestReviewNavScreens(t *testing.T) {
	r := NewReview("pr-review")
	d := navReview()
	d.Files[2].Viewed = FileChanged
	r.SetData(d)
	r.ToggleViewed()
	r.Key("k", false)
	golden(t, "review-viewed-120", ansi.Strip(r.Render(120, 10, true, false)+"\n"+r.Bar(false)), 120)
	r.Key("/", false)
	for _, k := range []string{"q", "u", "e", "r", "y"} {
		r.Key(k, false)
	}
	golden(t, "review-filter-120", ansi.Strip(r.Render(120, 10, true, false)+"\n"+r.Bar(false)), 120)
	r.Key("Esc", false)
	r.Key("?", false)
	golden(t, "review-keys-120", ansi.Strip(r.Render(120, 24, true, false)+"\n"+r.Bar(false)), 120)
	n := NewReview("pr-review")
	n.SetData(d)
	n.Key("/", true)
	n.Key("q", true)
	golden(t, "review-narrow-filter-40", ansi.Strip(n.Render(40, 8, true, true)+"\n"+n.Bar(true)), 40)
}

func TestReviewFocus(t *testing.T) {
	r := NewReview("s")
	d := navReview()
	d.Comments = []Comment{{ID: "c", File: "internal/prwatch/query_test.go", Side: "new", Line: 1, Body: "why"}}
	r.SetData(d)
	golden(t, "review-list-focus-120", ansi.Strip(r.Render(120, 10, true, false)+"\n"+r.Bar(false)), 120)
	if r.Key("c", false) != ReviewKeep || r.Key("v", false) != ReviewKeep || r.selecting {
		t.Fatal("the list has no line to comment on or select")
	}
	r.Render(120, 6, true, false) // a list of 4 lines: a half page is 1
	r.Key("C-d", false)
	r.Key("G", false)
	if r.file != 3 {
		t.Fatalf("the list pages and ends: file %d", r.file)
	}
	r.Key("g", false)
	r.Key("Enter", false)
	r.Key("j", false)
	r.Key("j", false) // line 3 of api.go
	if r.Key("Esc", false) != ReviewKeep || r.pane != 0 {
		t.Fatal("Esc goes to the list")
	}
	r.Key("Enter", false)
	if l, _ := r.Selected(); l.Line != 3 {
		t.Fatalf("back on the line it left: %+v", l)
	}
	r.Key("Esc", false)
	r.Key("n", false)
	if l, _ := r.Selected(); r.pane != 1 || l.File != "internal/prwatch/query_test.go" || l.Edit == nil {
		t.Fatalf("n from the list opens the code on the comment: pane %d %+v", r.pane, l)
	}
	r.Key("Esc", false)
	r.Key("k", false)
	if r.Key("n", false) != ReviewKeep || r.pane != 1 {
		t.Fatal("n finds it again from the file above")
	}
	r.Key("Esc", false)
	r.Key("n", false) // no comment below the cursor
	if r.pane != 0 {
		t.Fatal("n with no comment after stays in the list")
	}
	for _, k := range []string{"l", "Right"} {
		if r.Key(k, false); r.pane != 0 {
			t.Fatalf("%s doesn't open the code", k)
		}
	}
	r.Key("Enter", false)
	for _, k := range []string{"h", "Left"} {
		if r.Key(k, false); r.pane != 1 {
			t.Fatalf("%s doesn't leave the code", k)
		}
	}
}
