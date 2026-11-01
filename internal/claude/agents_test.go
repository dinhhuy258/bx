package claude

import (
	"os"
	"testing"
)

func TestDecodeAgents(t *testing.T) {
	data, err := os.ReadFile("testdata/agents.json")
	if err != nil {
		t.Fatal(err)
	}
	agents, err := DecodeAgents(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 4 {
		t.Fatalf("got %d agents", len(agents))
	}
	byID := IndexByUUID(agents)
	a := byID["11111111-1111-4111-8111-111111111111"]
	if a.State() != Running || a.Name != "Fix guest DHCP lease time" {
		t.Errorf("a = %+v state=%v", a, a.State())
	}
	b := byID["22222222-2222-4222-8222-222222222222"]
	if b.State() != Waiting || b.WaitingFor != "permission prompt" {
		t.Errorf("b = %+v", b)
	}
	if byID["33333333-3333-4333-8333-333333333333"].State() != Idle {
		t.Error("c should be idle")
	}
	if d := byID["44444444-4444-4444-8444-444444444444"]; d.State() != Running {
		t.Errorf("unknown status must map to running, got %v", d.State())
	}
}

func TestDecodeAgentsRejectsGarbage(t *testing.T) {
	if _, err := DecodeAgents([]byte("not json")); err == nil {
		t.Fatal("expected error")
	}
}

func TestStartArgs(t *testing.T) {
	got := StartArgs("abc-uuid", false, Options{})
	want := []string{"claude", "--session-id", "abc-uuid"}
	if !equal(got, want) {
		t.Errorf("start = %v", got)
	}
	got = StartArgs("abc-uuid", true, Options{})
	want = []string{"claude", "--resume", "abc-uuid"}
	if !equal(got, want) {
		t.Errorf("resume = %v", got)
	}
	o := Options{Model: "opus", Effort: "high", PermissionMode: "acceptEdits"}
	flags := []string{"--permission-mode", "acceptEdits", "--model", "opus", "--effort", "high"}
	if got = StartArgs("abc-uuid", false, o); !equal(got, append([]string{"claude", "--session-id", "abc-uuid"}, flags...)) {
		t.Errorf("start with options = %v", got)
	}
	if got = StartArgs("abc-uuid", true, o); !equal(got, append([]string{"claude", "--resume", "abc-uuid"}, flags...)) {
		t.Errorf("resume with options = %v", got)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
