package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func sampleReview() ReviewData {
	return ReviewData{
		Base: "develop", Total: 2,
		Files: []DiffFile{
			{Path: "internal/git/git.go", Status: "modified", Add: 2, Del: 1, Hunks: []Hunk{{
				Header: "@@ -86,4 +86,5 @@ func (g *Git) diff(dir, base string) error {",
				Rows: []DiffRow{
					{Kind: "ctx", Old: 86, New: 86, Text: "\tif base == \"\" {"},
					{Kind: "ctx", Old: 87, New: 87, Text: "\t\treturn nil"},
					{Kind: "del", Old: 88, Text: "\tmb := g.base(dir)"},
					{Kind: "add", New: 88, Text: "\tmb, err := g.mergeBase(dir, base)"},
					{Kind: "add", New: 89, Text: "\tif err != nil { return err }"},
				},
			}}},
			{Path: "cmd/bx/main.go", Status: "added", Add: 1, Hunks: []Hunk{{Header: "@@ -0,0 +1 @@",
				Rows: []DiffRow{{Kind: "add", New: 1, Text: "package main"}}}}},
		},
		ReviewComments: ReviewComments{Version: 3, Comments: []Comment{
			{ID: "a", File: "internal/git/git.go", Side: "new", Line: 88, Code: "\tmb, err := g.mergeBase(dir, base)", Body: "Wrap this error"},
		}},
	}
}

func TestReviewKeys(t *testing.T) {
	r := NewReview("login-timeout")
	r.SetData(sampleReview())
	l, ok := r.Selected()
	if !ok || l.File != "internal/git/git.go" || l.Line != 86 || l.Side != "new" {
		t.Fatalf("start: %+v", l)
	}
	r.Key("j", false)
	r.Key("j", false)
	if l, _ := r.Selected(); l.Side != "old" || l.Line != 88 || l.Code != "\tmb := g.base(dir)" {
		t.Fatalf("removed line: %+v", l)
	}
	r.Key("j", false)
	if l, _ := r.Selected(); l.Edit == nil || l.Edit.ID != "a" {
		t.Fatalf("commented line: %+v", l)
	}
	if r.Key("x", false) != ReviewDelete || r.Key("c", false) != ReviewComment {
		t.Fatal("x and c on a commented line")
	}
	if r.Key("S", false) != ReviewSend || r.Key("C", false) != ReviewKeep {
		t.Fatal("S sends unsent comments; C needs sent ones")
	}
	r.Key("J", false)
	if l, _ := r.Selected(); l.File != "cmd/bx/main.go" || l.Line != 1 {
		t.Fatalf("J: %+v", l)
	}
	r.Key("N", false)
	if l, _ := r.Selected(); l.Edit == nil {
		t.Fatalf("N goes back to the comment: %+v", l)
	}
	if r.Key("Esc", false) != ReviewBack || r.Key("R", false) != ReviewRefresh {
		t.Fatal("Esc and R")
	}
}

func TestReviewNarrowPanes(t *testing.T) {
	r := NewReview("s1")
	r.SetData(sampleReview())
	r.Key("j", true) // files pane: next file
	r.Key("Enter", true)
	if l, _ := r.Selected(); l.File != "cmd/bx/main.go" {
		t.Fatalf("%+v", l)
	}
	if r.Key("Esc", true) != ReviewKeep || r.Key("Esc", true) != ReviewBack {
		t.Fatal("Esc goes to the files, then back")
	}
}

func TestReviewKeepsTheFileOnReload(t *testing.T) {
	r := NewReview("s1")
	r.SetData(sampleReview())
	r.Key("J", false)
	d := sampleReview()
	d.Files[0], d.Files[1] = d.Files[1], d.Files[0]
	r.SetData(d)
	if l, _ := r.Selected(); l.File != "cmd/bx/main.go" {
		t.Fatalf("%+v", l)
	}
}

func TestReviewScreens(t *testing.T) {
	r := NewReview("login-timeout")
	r.SetData(sampleReview())
	r.Key("j", false)
	r.Key("j", false)
	r.Key("j", false)
	golden(t, "review-120", ansi.Strip(r.Render(120, 14, true, false)+"\n"+r.Bar(false)), 120)

	round2 := sampleReview()
	round2.Comments = []Comment{
		{ID: "a", File: "internal/git/git.go", Side: "new", Line: 88, Body: "Wrap this error", SentTo: 3, SentAt: 100},
		{ID: "b", File: "internal/git/git.go", Side: "new", Line: 89, Body: "Say which base"},
		{ID: "c", File: "internal/git/git.go", Side: "new", Line: 40, Body: "Old note", Outdated: true},
	}
	r.SetData(round2)
	golden(t, "review-round2-120", ansi.Strip(r.Render(120, 16, true, false)+"\n"+r.Bar(false)), 120)
	if r.Key("C", false) != ReviewClearSent {
		t.Fatal("C with a sent comment")
	}

	n := NewReview("login-timeout")
	n.SetData(sampleReview())
	golden(t, "review-narrow-files-40", ansi.Strip(n.Render(40, 10, true, true)+"\n"+n.Bar(true)), 40)
	n.Key("Enter", true)
	golden(t, "review-narrow-diff-40", ansi.Strip(n.Render(40, 12, true, true)+"\n"+n.Bar(true)), 40)

	var p Prompt
	l, _ := r.Selected()
	p.OpenComment("Comment · "+l.File+":89", "\tif err != nil { return err }", "")
	golden(t, "review-comment-dialog-120", ansi.Strip(Overlay(r.Render(120, 16, true, false), p.Dialog().Render(120, 16, true), 120, 16)), 120)

	e := NewReview("s1")
	e.SetData(ReviewData{Base: "main", Files: []DiffFile{}})
	golden(t, "review-empty-120", ansi.Strip(e.Render(120, 6, true, false)+"\n"+e.Bar(false)), 120)
	if e.Key("S", false) != ReviewKeep || e.Key("c", false) != ReviewKeep {
		t.Fatal("nothing to send or comment on")
	}
}

func twoHunks() ReviewData {
	d := sampleReview()
	d.Files[0].Hunks = append(d.Files[0].Hunks, Hunk{Header: "@@ -120,2 +121,3 @@ func (g *Git) run(",
		Rows: []DiffRow{{Kind: "ctx", Old: 120, New: 121, Text: "\tcmd := exec.Command(\"git\")"}, {Kind: "add", New: 122, Text: "\tcmd.Dir = dir"}}})
	return d
}

func TestReviewSelectsARange(t *testing.T) {
	r := NewReview("login-timeout")
	r.SetData(twoHunks())
	r.Key("v", false) // on 86
	for range 4 {
		r.Key("j", false)
	}
	l, ok := r.Selected()
	want := ReviewLine{File: "internal/git/git.go", Side: "new", StartLine: 86, Line: 89,
		Code: "\tif base == \"\" {\n\t\treturn nil\n\tmb, err := g.mergeBase(dir, base)\n\tif err != nil { return err }"}
	if !ok || l != want {
		t.Fatalf("range without the removed row:\n%+v\nwant\n%+v", l, want)
	}
	if r.Bar(false) != "SELECT 86–89 · 4 lines · j/k move end · o other end · c comment · e explain · esc cancel" {
		t.Fatalf("bar: %s", r.Bar(false))
	}
	r.Key("j", false) // the next row is in the next hunk
	if l, _ := r.Selected(); l.Line != 89 || !strings.HasSuffix(r.Bar(false), " · a range stays inside one hunk") {
		t.Fatalf("hunk edge: %+v · %s", l, r.Bar(false))
	}
	if r.Key("S", false) != ReviewKeep || r.Key("J", false) != ReviewKeep {
		t.Fatal("only the selection keys work while selecting")
	}
	if r.Key("c", false) != ReviewComment {
		t.Fatal("c comments on the range")
	}
	r.Key("o", false) // move the start now
	r.Key("j", false)
	r.Key("j", false) // 88 old to 89: the removed row drops out
	if l, _ := r.Selected(); l.StartLine != 88 || l.Line != 89 || l.Side != "new" {
		t.Fatalf("moved start: %+v", l)
	}
	r.Key("j", false)
	r.Key("j", false) // the start reaches the end: one line
	if l, _ := r.Selected(); l.StartLine != 0 || l.Line != 89 || l.Edit != nil {
		t.Fatalf("one line: %+v", l)
	}
	r.Key("Esc", false)
	if r.selecting || r.Key("Esc", false) != ReviewBack {
		t.Fatal("Esc ends the selection, then leaves")
	}

	o := NewReview("s")
	o.SetData(sampleReview())
	o.Key("j", false)
	o.Key("j", false) // 88 old
	o.Key("v", false)
	if l, _ := o.Selected(); l.Side != "old" || l.Line != 88 || l.StartLine != 0 {
		t.Fatalf("a removed line: %+v", l)
	}
	o.Key("j", false) // 88 old and 88 new: 88 new alone, with its comment
	if l, _ := o.Selected(); l.Side != "new" || l.Line != 88 || l.StartLine != 0 || l.Edit == nil || l.Edit.ID != "a" {
		t.Fatalf("one line with its comment: %+v", l)
	}
}

func TestReviewRangeComments(t *testing.T) {
	r := NewReview("login-timeout")
	d := twoHunks()
	d.Comments = []Comment{
		{ID: "r", File: "internal/git/git.go", Side: "new", StartLine: 86, Line: 88, Code: "a\nb\nc", Body: "Pull these into a helper"},
		{ID: "o", File: "internal/git/git.go", Side: "new", StartLine: 30, Line: 32, Body: "Old range", Outdated: true},
	}
	r.SetData(d)
	r.Key("j", false) // from the pinned comment to 86
	r.Key("j", false)
	r.Key("j", false)
	r.Key("j", false) // 88 new: the range's last line
	if l, _ := r.Selected(); l.Edit == nil || l.Edit.ID != "r" || l.StartLine != 86 || l.Code != "a\nb\nc" {
		t.Fatalf("the range's last line edits it: %+v", l)
	}
	golden(t, "review-range-120", ansi.Strip(r.Render(120, 16, true, false)+"\n"+r.Bar(false)), 120)

	s := NewReview("login-timeout")
	s.SetData(twoHunks())
	s.Key("v", false)
	s.Key("j", false)
	s.Key("j", false)
	golden(t, "review-select-120", ansi.Strip(s.Render(120, 16, true, false)+"\n"+s.Bar(false)), 120)

	var p Prompt
	p.OpenComment("Comment · x.go:1–6", "\tone\n\t\ttwo\n\tthree\n\tfour\n\tfive\n\tsix", "")
	if strings.Join(p.Body, "|") != "> one|>     two|> three|> four|> … 2 more lines" {
		t.Fatalf("quote: %q", p.Body)
	}
}

func TestQuoteCode(t *testing.T) {
	for _, c := range []struct {
		code string
		max  int
		want []string
	}{
		{"", 4, nil},
		{"  \t ", 4, nil},
		{"\tx := 1  ", 4, []string{"x := 1"}},
		{"\tif a {\n\n\t\tb()\n\t}", 4, []string{"if a {", "", "\tb()", "}"}},
		{"    a\n  b\n      c", 4, []string{"  a", "b", "    c"}},
		{"a\nb\nc", 2, []string{"a", "b", "… 1 more lines"}},
	} {
		if got := QuoteCode(c.code, c.max); strings.Join(got, "|") != strings.Join(c.want, "|") || len(got) != len(c.want) {
			t.Errorf("QuoteCode(%q, %d) = %q, want %q", c.code, c.max, got, c.want)
		}
	}
}

func TestReviewTextDropsControls(t *testing.T) {
	if got := ReviewText("a\tb\x1b[31mc\r"); got != "a    b[31mc" || strings.ContainsRune(got, 0x1b) {
		t.Fatalf("%q", got)
	}
}

func TestExplainAsComment(t *testing.T) {
	e := Explain{ID: "e1", File: "a.go", Side: "new", StartLine: 3, Line: 5, Code: "x\ny\nz"}
	c := e.AsComment()
	if c.ID != "e1" || c.File != "a.go" || c.First() != 3 || c.Line != 5 || c.Code != "x\ny\nz" || c.Lines() != "lines 3–5" {
		t.Fatalf("%+v", c)
	}
	if !(Explain{State: ExplainRunning}).Running() || (Explain{State: ExplainDone}).Running() {
		t.Fatal("Running")
	}
}

func explainReview() ReviewData {
	d := twoHunks()
	d.Explains = []Explain{
		{ID: "e1", File: "internal/git/git.go", Side: "new", StartLine: 86, Line: 88, State: ExplainDone, Session: 3,
			SessionTitle: "Add lease time", TookMS: 21000, Answer: "One.\nTwo.\n\nThree.\nFour."},
		{ID: "e2", File: "internal/git/git.go", Side: "new", Line: 89, State: ExplainRunning, Started: 100},
	}
	return d
}

func TestReviewExplainKeys(t *testing.T) {
	r := NewReview("login-timeout")
	r.Now = func() time.Time { return time.Unix(114, 0) }
	r.SetData(explainReview())
	if r.Key("e", false) != ReviewExplain { // on 86
		t.Fatal("e on a row")
	}
	for range 4 { // 87, 88 old, 88 new, the card of e1
		r.Key("j", false)
	}
	if e := r.SelectedExplain(); e == nil || e.ID != "e1" {
		t.Fatalf("on the card: %+v", e)
	}
	if r.Key("x", false) != ReviewExplainDelete {
		t.Fatal("x dismisses")
	}
	r.Key("Enter", false)
	golden(t, "review-explain-read-120", ansi.Strip(r.Render(120, 12, true, false)+"\n"+r.Bar(false)), 120)
	r.Key("Esc", false)
	golden(t, "review-explain-120", ansi.Strip(r.Render(120, 18, true, false)+"\n"+r.Bar(false)), 120)
	r.Key("v", false) // not on a row: nothing
	r.Key("k", false)
	r.Key("v", false)
	r.Key("k", false) // 88 old
	r.Key("k", false) // 87: 87 and 88 new, without the removed row
	if r.Key("e", false) != ReviewExplain {
		t.Fatal("e on a selection")
	}
	if l, _ := r.Selected(); l.StartLine == 0 {
		t.Fatalf("the selection: %+v", l)
	}
}

func TestReviewKeepsTheCursorWhenACardAppears(t *testing.T) {
	r := NewReview("s")
	d := twoHunks()
	r.SetData(d)
	r.Key("j", false)
	r.Key("j", false)
	r.Key("j", false)
	r.Key("v", false) // 88 new
	r.Key("j", false) // 89
	d.Explains = []Explain{{ID: "e", File: "internal/git/git.go", Side: "new", Line: 86, State: ExplainRunning}}
	r.SetData(d) // a card under 86, above the cursor
	if l, _ := r.Selected(); l.StartLine != 88 || l.Line != 89 {
		t.Fatalf("the selection moved: %+v", l)
	}
}
