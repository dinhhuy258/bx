package ui

import (
	"strings"
	"testing"

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

func TestReviewTextDropsControls(t *testing.T) {
	if got := ReviewText("a\tb\x1b[31mc\r"); got != "a    b[31mc" || strings.ContainsRune(got, 0x1b) {
		t.Fatalf("%q", got)
	}
}
