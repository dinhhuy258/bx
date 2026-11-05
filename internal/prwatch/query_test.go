package prwatch

import (
	"os"
	"strings"
	"testing"

	"github.com/dinhhuy258/bx/internal/ui"
)

func TestQueryAliasesEveryBranch(t *testing.T) {
	q := Query("acme", "netcfg", []string{"guest-vlan-fix", `we"ird`})
	for _, want := range []string{`repository(owner: "acme", name: "netcfg")`, `b0open: pullRequests(headRefName: "guest-vlan-fix", states: OPEN`,
		`b1last: pullRequests(headRefName: "we\"ird", states: [MERGED, CLOSED]`, "viewer { login }"} {
		if !strings.Contains(q, want) {
			t.Errorf("query lacks %q", want)
		}
	}
}

func TestParse(t *testing.T) {
	raw, err := os.ReadFile("testdata/graphql.json")
	if err != nil {
		t.Fatal(err)
	}
	me, got, err := Parse(raw, 2)
	if err != nil {
		t.Fatal(err)
	}
	if me != "huy" {
		t.Fatalf("me = %q", me)
	}
	b0 := got[0]
	if len(b0.Open) != 1 || b0.Last != nil {
		t.Fatalf("b0 = %+v", b0)
	}
	pr := b0.Open[0]
	if pr.Number != 142 || pr.Base != "develop" || pr.HeadOID != "abc123" || pr.Decision != "CHANGES_REQUESTED" || pr.Merge != "BEHIND" || pr.Slug != "acme/netcfg" {
		t.Fatalf("pr = %+v", pr)
	}
	states := map[string]string{}
	for _, c := range pr.Checks {
		states[c.Name] = c.State
	}
	want := map[string]string{"test / go 1.24": ui.CheckFail, "lint": ui.CheckPass, "deploy-preview": ui.CheckSkip, "e2e": ui.CheckPending, "ci/legacy": ui.CheckPass}
	for k, v := range want {
		if states[k] != v {
			t.Errorf("%s = %q, want %q", k, states[k], v)
		}
	}
	if pr.Checks[0].RunID != 9182736 {
		t.Errorf("RunID = %d", pr.Checks[0].RunID)
	}
	th := pr.Threads[0]
	if th.ID != "T1" || th.Where() != "dhcp/lease.go:24" || th.Comments[0].Author != "mai" || !strings.Contains(th.Code, "24 * time.Hour") || th.Comments[0].At == 0 {
		t.Fatalf("thread = %+v", th)
	}
	if got[1].Last == nil || got[1].Last.Number != 120 || got[1].Last.State != ui.PRMerged {
		t.Fatalf("b1.Last = %+v", got[1].Last)
	}
}

func TestParseGraphQLError(t *testing.T) {
	if _, _, err := Parse([]byte(`{"errors":[{"message":"Bad credentials"}]}`), 1); err == nil || !strings.Contains(err.Error(), "Bad credentials") {
		t.Fatalf("err = %v", err)
	}
}
