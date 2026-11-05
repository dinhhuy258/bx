package prwatch

import (
	"strings"
	"testing"

	"github.com/dinhhuy258/bx/internal/ui"
)

func open(mod func(*ui.PRInfo)) ui.PRInfo {
	p := ui.PRInfo{Number: 142, State: ui.PROpen, HeadOID: "h1", Base: "develop", Merge: "BLOCKED",
		Checks: []ui.PRCheck{{Name: "test", State: ui.CheckPending}}}
	if mod != nil {
		mod(&p)
	}
	return p
}

func titles(evs []Event) string {
	var s []string
	for _, e := range evs {
		s = append(s, e.Title)
	}
	return strings.Join(s, " | ")
}

func TestEvents(t *testing.T) {
	where := Where{Story: "guest-vlan-fix"}
	cases := []struct {
		name     string
		old, new ui.PRInfo
		want     string
	}{
		{"first look is silent", ui.PRInfo{}, open(nil), ""},
		{"ci failed", open(nil), open(func(p *ui.PRInfo) { p.Checks[0].State = ui.CheckFail }), "✗ CI failed · guest-vlan-fix #142"},
		{"still failing is silent", open(func(p *ui.PRInfo) { p.Checks[0].State = ui.CheckFail }), open(func(p *ui.PRInfo) { p.Checks[0].State = ui.CheckFail }), ""},
		{"ci fixed", open(func(p *ui.PRInfo) { p.Checks[0].State = ui.CheckFail }), open(func(p *ui.PRInfo) { p.HeadOID = "h2"; p.Checks[0].State = ui.CheckPass }), "✓ CI fixed · guest-vlan-fix #142"},
		{"ready", open(nil), open(func(p *ui.PRInfo) { p.Merge = "CLEAN"; p.Checks[0].State = ui.CheckPass }), "✓ Ready to merge · guest-vlan-fix #142"},
		{"conflict", open(nil), open(func(p *ui.PRInfo) { p.Merge = "DIRTY" }), "⇄ Conflicts with develop · guest-vlan-fix #142"},
		{"changes", open(nil), open(func(p *ui.PRInfo) { p.Decision = "CHANGES_REQUESTED" }), "✎ Changes requested · guest-vlan-fix #142"},
		{"approved", open(nil), open(func(p *ui.PRInfo) { p.Decision = "APPROVED" }), "✓ Approved · guest-vlan-fix #142"},
		{"merged", open(nil), open(func(p *ui.PRInfo) { p.State = ui.PRMerged }), "Merged · guest-vlan-fix #142"},
		{"closed", open(nil), open(func(p *ui.PRInfo) { p.State = ui.PRClosed }), "Closed · guest-vlan-fix #142"},
		{"new comment", open(nil), open(func(p *ui.PRInfo) {
			p.Threads = []ui.PRThread{{ID: "T1", Path: "a.go", Line: 3, Comments: []ui.PRComment{{Author: "mai", Body: "Fix\nthis", At: 10}}}}
		}), "💬 mai commented · guest-vlan-fix #142"},
		{"my comment is silent", open(nil), open(func(p *ui.PRInfo) {
			p.Threads = []ui.PRThread{{ID: "T1", Comments: []ui.PRComment{{Author: "huy", At: 10}}}}
		}), ""},
	}
	for _, c := range cases {
		if got := titles(Events(c.old, c.new, "huy", where)); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestEventsNameTheRepoInAWorkspace(t *testing.T) {
	evs := Events(open(nil), open(func(p *ui.PRInfo) { p.State = ui.PRMerged }), "huy", Where{Story: "s", Repo: "api"})
	if len(evs) != 1 || evs[0].Title != "Merged · s · api #142" {
		t.Fatalf("%+v", evs)
	}
}

func TestEventsSeveralComments(t *testing.T) {
	n := open(func(p *ui.PRInfo) {
		p.Threads = []ui.PRThread{{ID: "T1", Comments: []ui.PRComment{{Author: "mai", At: 1}}}, {ID: "T2", Comments: []ui.PRComment{{Author: "kofi", At: 2}}}}
	})
	evs := Events(open(nil), n, "huy", Where{Story: "s"})
	if len(evs) != 1 || evs[0].Title != "💬 2 new comments · s #142" {
		t.Fatalf("%+v", evs)
	}
}
