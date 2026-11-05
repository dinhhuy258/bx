package prwatch

import (
	"testing"

	"github.com/dinhhuy258/bx/internal/ui"
)

func pr(n int, state, slug, oid string) PR {
	return PR{PRInfo: ui.PRInfo{Number: n, State: state, HeadOID: oid}, Slug: slug}
}

func TestChoose(t *testing.T) {
	never := func(string) bool { return false }
	always := func(string) bool { return true }
	cases := []struct {
		name string
		f    Found
		in   func(string) bool
		want int
		more int
	}{
		{"open wins over last", Found{Open: []PR{pr(150, ui.PROpen, "a/r", "x")}, Last: &PR{PRInfo: ui.PRInfo{Number: 120, State: ui.PRClosed}, Slug: "a/r"}}, always, 150, 0},
		{"two open: newest, one more", Found{Open: []PR{pr(151, ui.PROpen, "a/r", "x"), pr(150, ui.PROpen, "a/r", "x")}}, never, 151, 1},
		{"fork with the same branch name is left out", Found{Open: []PR{pr(160, ui.PROpen, "them/r", "x")}}, always, 0, 0},
		{"merged in the branch counts", Found{Last: ptr(pr(131, ui.PRMerged, "a/r", "m"))}, always, 131, 0},
		{"merged not in the branch: reused name", Found{Last: ptr(pr(99, ui.PRMerged, "a/r", "old"))}, never, 0, 0},
		{"nothing", Found{}, always, 0, 0},
	}
	for _, c := range cases {
		got := Choose(c.f, "a/r", c.in)
		if got.Number != c.want || got.MoreOpen != c.more {
			t.Errorf("%s: got #%d more %d, want #%d more %d", c.name, got.Number, got.MoreOpen, c.want, c.more)
		}
	}
}

func ptr(p PR) *PR { return &p }

func TestBadge(t *testing.T) {
	prs := []ui.PRInfo{
		{Number: 142, State: ui.PROpen, Decision: "CHANGES_REQUESTED", Merge: "BEHIND",
			Checks:  []ui.PRCheck{{State: ui.CheckFail}},
			Threads: []ui.PRThread{{Comments: []ui.PRComment{{Author: "mai"}}}, {Comments: []ui.PRComment{{Author: "huy"}}}}},
		{Number: 88, State: ui.PROpen, Merge: "CLEAN", Checks: []ui.PRCheck{{State: ui.CheckPass}},
			Threads: []ui.PRThread{{Comments: []ui.PRComment{{Author: "kofi"}}}}},
		{Missing: "not pushed"},
	}
	b := Badge(prs, "huy")
	want := ui.PRBadge{Count: 2, Fail: true, Threads: 2, Changes: true, Behind: true}
	if b != want {
		t.Fatalf("Badge = %+v, want %+v", b, want)
	}
	one := Badge(prs[1:2], "huy")
	if one.Number != 88 || !one.Ready || !one.Pass {
		t.Fatalf("one = %+v", one)
	}
	merged := Badge([]ui.PRInfo{{Number: 131, State: ui.PRMerged}}, "huy")
	if merged.Merged != 1 || merged.Count != 1 {
		t.Fatalf("merged = %+v", merged)
	}
}

func TestCarry(t *testing.T) {
	fail := ui.PRInfo{Number: 1, State: ui.PROpen, Merge: "CLEAN", Checks: []ui.PRCheck{{State: ui.CheckFail}}}
	pending := ui.PRInfo{Number: 1, State: ui.PROpen, Merge: "UNKNOWN", Checks: []ui.PRCheck{{State: ui.CheckPending}}}
	pass := ui.PRInfo{Number: 1, State: ui.PROpen, Merge: "CLEAN", Checks: []ui.PRCheck{{State: ui.CheckPass}}}
	a := Carry(ui.PRInfo{}, fail)
	b := Carry(a, pending)
	if b.LastCI != ui.CheckFail || b.Merge != "CLEAN" {
		t.Fatalf("pending look keeps the failure and the merge state: %+v", b)
	}
	if got := titles(Events(b, Carry(b, pass), "me", Where{Story: "s"})); got != "✓ CI fixed · s #1" {
		t.Fatalf("fail → pending → pass: %q", got)
	}
	if c := Carry(ui.PRInfo{Number: 2, Merge: "CLEAN"}, pending); c.Merge != "UNKNOWN" || c.LastCI != "" {
		t.Fatalf("another PR carries nothing: %+v", c)
	}
}

func TestReadyOnceOverUnknown(t *testing.T) {
	ready := ui.PRInfo{Number: 1, State: ui.PROpen, Merge: "CLEAN", Checks: []ui.PRCheck{{State: ui.CheckPass}}}
	unknown := ready
	unknown.Merge = "UNKNOWN"
	a := Carry(ui.PRInfo{}, ready)
	b := Carry(a, unknown)
	if got := titles(Events(b, Carry(b, ready), "me", Where{Story: "s"})); got != "" {
		t.Fatalf("CLEAN → UNKNOWN → CLEAN announced %q", got)
	}
}
