package review

import (
	"errors"
	"strings"
	"testing"

	"github.com/dinhhuy258/bx/internal/api"
)

func TestApply(t *testing.T) {
	sent := api.Comment{ID: "s1", File: "a.go", Side: "new", Line: 3, Code: "x", Body: "done", SentTo: 2, SentAt: 100}
	open := api.Comment{ID: "u1", File: "a.go", Side: "old", Line: 4, Body: "keep"}
	old := []api.Comment{sent, open}

	// a new comment gets an id; an edit and a sent comment's moved line go through; Outdated is dropped
	moved := sent
	moved.Line, moved.Outdated = 5, true
	edited := open
	edited.Body = "  keep it  "
	got, err := Apply(old, []api.Comment{moved, edited, {File: "b.go", Side: "new", Line: 1, Body: "new"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Line != 5 || got[0].Outdated || got[1].Body != "keep it" || len(got[2].ID) != 16 {
		t.Fatalf("%+v", got)
	}

	bad := func(name string, next []api.Comment, want error) {
		t.Helper()
		_, err := Apply(old, next)
		if err == nil || (want != nil && !errors.Is(err, want)) {
			t.Errorf("%s: err = %v, want %v", name, err, want)
		}
	}
	changed := sent
	changed.Body = "other"
	bad("sent body", []api.Comment{changed}, ErrSentChanged)
	unsend := sent
	unsend.SentAt, unsend.SentTo = 0, 0
	bad("unmark sent", []api.Comment{unsend}, ErrSentChanged)
	marked := open
	marked.SentTo, marked.SentAt = 1, 1
	bad("mark sent", []api.Comment{marked}, ErrSentChanged)
	bad("new marked sent", []api.Comment{{File: "a", Side: "new", Line: 1, Body: "x", SentAt: 1}}, ErrSentChanged)
	bad("unknown id", []api.Comment{{ID: "zz", File: "a", Side: "new", Line: 1, Body: "x"}}, ErrUnknown)
	bad("twice", []api.Comment{open, open}, nil)
	bad("no body", []api.Comment{{File: "a", Side: "new", Line: 1, Body: " "}}, nil)
	bad("bad side", []api.Comment{{File: "a", Side: "left", Line: 1, Body: "x"}}, nil)
	bad("line 0", []api.Comment{{File: "a", Side: "new", Body: "x"}}, nil)
	bad("too long", []api.Comment{{File: "a", Side: "new", Line: 1, Body: strings.Repeat("x", MaxBody+1)}}, ErrTooLong)
	many := make([]api.Comment, MaxComments+1)
	for i := range many {
		many[i] = api.Comment{File: "a", Side: "new", Line: 1, Body: "x"}
	}
	bad("too many", many, ErrTooMany)
}

func TestApplyRanges(t *testing.T) {
	sent := api.Comment{ID: "s", File: "a.go", Side: "new", StartLine: 3, Line: 5, Code: "x\ny\nz", Body: "b", SentTo: 1, SentAt: 9}
	got, err := Apply([]api.Comment{sent}, []api.Comment{
		{ID: "s", File: "a.go", Side: "new", StartLine: 7, Line: 9, Code: "x\ny\nz", Body: "b", SentTo: 1, SentAt: 9},
		{File: "a.go", Side: "new", StartLine: 4, Line: 4, Body: "one line"},
		{File: "a.go", Side: "old", StartLine: 1, Line: 2, Body: "removed lines"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].StartLine != 7 || got[0].Line != 9 || got[1].StartLine != 0 || got[2].StartLine != 1 {
		t.Fatalf("a sent range follows the code; a one-line range is one line: %+v", got)
	}
	for _, c := range []api.Comment{
		{File: "a.go", Side: "new", StartLine: 5, Line: 4, Body: "x"},
		{File: "a.go", Side: "new", StartLine: -1, Line: 4, Body: "x"},
	} {
		if _, err := Apply(nil, []api.Comment{c}); err == nil {
			t.Errorf("%d-%d: want an error", c.StartLine, c.Line)
		}
	}
}

func TestSentHelpers(t *testing.T) {
	cs := []api.Comment{{ID: "a", Body: "1"}, {ID: "b", Body: "2", SentTo: 1, SentAt: 5}, {ID: "c", Body: "3"}}
	if u := Unsent(cs); len(u) != 2 || u[0].ID != "a" || u[1].ID != "c" {
		t.Fatalf("unsent %+v", u)
	}
	marked := MarkSent(cs, []string{"a"}, 3, 9)
	if marked[0].SentTo != 3 || marked[0].SentAt != 9 || cs[0].SentAt != 0 {
		t.Fatalf("MarkSent must copy: %+v / %+v", marked, cs)
	}
	if u, s := Counts(marked); u != 1 || s != 2 {
		t.Fatalf("counts %d %d", u, s)
	}
	if left := ClearSent(marked); len(left) != 1 || left[0].ID != "c" {
		t.Fatalf("ClearSent %+v", left)
	}
}
