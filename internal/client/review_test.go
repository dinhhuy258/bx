package client

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/dinhhuy258/bx/internal/api"
)

func reviewFixture() api.Review {
	return api.Review{Base: "main", Total: 1, Files: []api.DiffFile{{Path: "a.go", Status: "modified", Add: 1,
		Hunks: []api.Hunk{{Header: "@@ -1 +1,2 @@", Rows: []api.DiffRow{{Kind: "ctx", Old: 1, New: 1, Text: "x"}, {Kind: "add", New: 2, Text: "y"}}}}}}}
}

func TestReviewCommentSendFlow(t *testing.T) {
	h := start(t, 120, 30, "idle")
	h.be.review = reviewFixture()
	h.be.sendRes = api.Result{Num: 1, Message: "Pasted 1 comment into [1]"}
	h.press("k", "G") // the story row s1, then review
	if h.m.review == nil || !strings.Contains(ansi.Strip(h.m.View().Content), "review · s1 vs main") {
		t.Fatalf("review not open:\n%s", ansi.Strip(h.m.View().Content))
	}
	h.press("j", "c")
	if !strings.Contains(ansi.Strip(h.m.View().Content), "Comment · a.go:2") {
		t.Fatalf("no comment form:\n%s", ansi.Strip(h.m.View().Content))
	}
	h.typeText("why y")
	h.press("Enter")
	if len(h.be.saveRev) != 1 || h.be.saveRev[0].Comments[0].Body != "why y" || h.be.saveRev[0].Comments[0].Line != 2 {
		t.Fatalf("saved %+v", h.be.saveRev)
	}
	if !strings.Contains(ansi.Strip(h.m.bar()), "S send 1") {
		t.Fatalf("bar %q", ansi.Strip(h.m.bar()))
	}
	h.press("S")
	v := ansi.Strip(h.m.View().Content)
	if !strings.Contains(v, "send 1 comment · pasted, not submitted") || !strings.Contains(v, "[1] Add lease time · running") || !strings.Contains(v, "+ New session") {
		t.Fatalf("picker:\n%s", v)
	}
	h.press("Enter")
	if len(h.be.sends) != 1 || h.be.sends[0].Session != 1 || h.be.sends[0].New {
		t.Fatalf("sends %+v", h.be.sends)
	}
	if !strings.Contains(ansi.Strip(h.m.bar()), "Pasted 1 comment into [1]") {
		t.Fatalf("bar %q", ansi.Strip(h.m.bar()))
	}
	h.press("Esc")
	if h.m.review != nil {
		t.Fatal("Esc must leave the review")
	}
}

func TestReviewEmptyCommentDeletes(t *testing.T) {
	h := start(t, 120, 30, "idle")
	h.be.review = reviewFixture()
	h.be.review.Comments = []api.Comment{{ID: "c1", File: "a.go", Side: "new", Line: 2, Body: "old"}}
	h.press("k", "G", "j", "c")
	for range 3 {
		h.press("BSpace")
	}
	h.press("Enter")
	if n := len(h.be.saveRev); n != 1 || len(h.be.saveRev[0].Comments) != 0 {
		t.Fatalf("saved %+v", h.be.saveRev)
	}
}

func TestReviewConflictReloads(t *testing.T) {
	h := start(t, 120, 30, "idle")
	h.be.review = reviewFixture()
	h.be.reviewErr = &StatusError{Code: 409, Text: "comments changed on another device"}
	h.press("k", "G", "j", "c")
	h.typeText("x")
	h.press("Enter")
	if !strings.Contains(ansi.Strip(h.m.bar()), "comments changed on another device · reloaded") {
		t.Fatalf("bar %q", ansi.Strip(h.m.bar()))
	}
}

func TestStoryRowShowsReviewKey(t *testing.T) {
	h := start(t, 120, 30, "idle")
	h.press("k")
	if !strings.Contains(ansi.Strip(h.m.bar()), "G review") {
		t.Fatalf("bar %q", ansi.Strip(h.m.bar()))
	}
}

func TestReviewRangeComment(t *testing.T) {
	h := start(t, 120, 30, "idle")
	h.be.review = reviewFixture()
	h.press("k", "G", "v", "j")
	if !strings.Contains(ansi.Strip(h.m.bar()), "SELECT 1–2 · 2 lines") {
		t.Fatalf("bar %q", ansi.Strip(h.m.bar()))
	}
	h.press("c")
	v := ansi.Strip(h.m.View().Content)
	if !strings.Contains(v, "Comment · a.go:1–2") || !strings.Contains(v, "> x") || !strings.Contains(v, "> y") {
		t.Fatalf("range form:\n%s", v)
	}
	h.typeText("pull out")
	h.press("Enter")
	if len(h.be.saveRev) != 1 {
		t.Fatalf("saved %+v", h.be.saveRev)
	}
	if c := h.be.saveRev[0].Comments[0]; c.StartLine != 1 || c.Line != 2 || c.Code != "x\ny" || c.Body != "pull out" {
		t.Fatalf("saved %+v", c)
	}
}

func TestReviewExplainFlow(t *testing.T) {
	h := start(t, 120, 30, "idle")
	h.m.reviewTick = func(string) tea.Cmd { return nil } // the real wait would never end here
	h.be.review = reviewFixture()
	h.press("k", "G", "v", "j", "e")
	v := ansi.Strip(h.m.View().Content)
	if !strings.Contains(v, "Explain · a.go:1–2") || !strings.Contains(v, "Conversation") || !strings.Contains(v, "[1] Add lease time") {
		t.Fatalf("form:\n%s", v)
	}
	h.press("Tab", "Right") // Code only
	h.press("Enter")
	if len(h.be.explains) != 1 {
		t.Fatalf("explains %+v", h.be.explains)
	}
	if q := h.be.explains[0]; q.StartLine != 1 || q.Line != 2 || q.Code != "x\ny" || q.Session != api.ExplainCodeOnly {
		t.Fatalf("request %+v", q)
	}
	if !h.m.reviewPolling {
		t.Fatal("a running explanation polls")
	}
	// the card is under line 2; x stops it
	h.press("Esc") // the selection
	h.press("j")
	h.m.flash = "" // "explaining…" fades by itself
	if !strings.Contains(ansi.Strip(h.m.bar()), "x stop") {
		t.Fatalf("bar %q", ansi.Strip(h.m.bar()))
	}
	h.press("x")
	if len(h.be.dismissed) != 1 || h.be.dismissed[0] != "e1" || len(h.m.review.Data().Explains) != 0 {
		t.Fatalf("dismissed %v", h.be.dismissed)
	}
	// the poll reloads the review once
	h.m.reviewPolling = false
	n := len(h.be.reads)
	h.update(reviewPollMsg{"s1"})
	if len(h.be.reads) != n+1 {
		t.Fatalf("reads %v", h.be.reads)
	}
}
