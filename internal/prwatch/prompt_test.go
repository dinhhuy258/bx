package prwatch

import (
	"strings"
	"testing"

	"github.com/dinhhuy258/bx/internal/ui"
)

func TestFixPrompt(t *testing.T) {
	p := ui.PRInfo{Number: 142, Base: "develop", Checks: []ui.PRCheck{
		{Name: "test / go 1.24", State: ui.CheckFail, URL: "https://x/runs/9", RunID: 9}, {Name: "lint", State: ui.CheckPass}}}
	got := FixPrompt("guest-vlan-fix", "guest-vlan-fix", p, map[int64]string{9: "--- FAIL\x1b[31m: TestLease"}, "")
	for _, want := range []string{
		"CI failed on PR #142 (guest-vlan-fix → develop).",
		"Failed check: test / go 1.24",
		"Run: https://x/runs/9",
		"  --- FAIL: TestLease",
		"gh run view 9 --log-failed",
		DefaultFix,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "\x1b") || strings.Contains(got, "lint") || strings.HasSuffix(got, "\n") {
		t.Errorf("bad prompt:\n%q", got)
	}
}

func TestAddressPromptWorkspace(t *testing.T) {
	p := ui.PRInfo{Repo: "api", Number: 142, Threads: []ui.PRThread{
		{ID: "T2", Path: "b.go", Line: 9, Comments: []ui.PRComment{{Author: "kofi", Body: "Second"}}},
		{ID: "T1", Path: "a.go", Line: 3, StartLine: 1, Code: "+x := 1\n+y := 2", Comments: []ui.PRComment{{Author: "mai", Body: "First"}, {Author: "huy", Body: "Why?"}}},
		{ID: "T3", Path: "c.go", Line: 1, Comments: []ui.PRComment{{Author: "x", Body: "not picked"}}},
	}}
	got := AddressPrompt("guest-vlan-fix", p, []string{"T1", "T2"}, "Be brief.")
	for _, want := range []string{
		"Review comments on PR #142 (api · guest-vlan-fix):",
		"1. api/a.go:1-3 · mai\n   > +x := 1\n   > +y := 2\n   First\n   huy: Why?",
		"2. api/b.go:9 · kofi\n   Second",
		"Be brief.",
		"Work in api/ only, and push that repo.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "not picked") || strings.Contains(got, DefaultAddress) {
		t.Errorf("bad prompt:\n%s", got)
	}
}

func TestAddressPromptLeavesOutResolved(t *testing.T) {
	p := ui.PRInfo{Number: 1, Threads: []ui.PRThread{{ID: "T1", Path: "a.go", Line: 1, Resolved: true, Comments: []ui.PRComment{{Author: "x", Body: "done already"}}}}}
	if got := AddressPrompt("s", p, []string{"T1"}, ""); strings.Contains(got, "done already") {
		t.Fatalf("resolved thread sent:\n%s", got)
	}
}
