package ui

import "testing"

func TestPRInfoStates(t *testing.T) {
	pr := PRInfo{Number: 1, State: "OPEN", Checks: []PRCheck{
		{Name: "lint", State: CheckPass}, {Name: "test", State: CheckFail}, {Name: "e2e", State: CheckPending}, {Name: "deploy", State: CheckSkip},
	}}
	if !pr.Has() || !pr.Failed() || !pr.Pending() {
		t.Fatalf("Has/Failed/Pending = %v %v %v", pr.Has(), pr.Failed(), pr.Pending())
	}
	if got := pr.FailedChecks(); len(got) != 1 || got[0].Name != "test" {
		t.Fatalf("FailedChecks = %+v", got)
	}
	ok := PRInfo{Number: 2, State: "OPEN", Checks: []PRCheck{{Name: "lint", State: CheckPass}, {Name: "d", State: CheckSkip}}}
	if ok.Failed() || ok.Pending() || !ok.Passed() {
		t.Fatal("skipped counts as passed")
	}
	if (PRInfo{}).Has() {
		t.Fatal("zero PRInfo has no PR")
	}
}

func TestPRThreadOpen(t *testing.T) {
	th := PRThread{Comments: []PRComment{{Author: "mai"}, {Author: "me"}}}
	if th.Open("me") {
		t.Fatal("last word is mine: not open")
	}
	th.Comments = append(th.Comments, PRComment{Author: "mai"})
	if !th.Open("me") {
		t.Fatal("reviewer answered: open")
	}
	th.Resolved = true
	if th.Open("me") {
		t.Fatal("resolved is never open")
	}
}
