package ui

import (
	"strings"
	"testing"
	"time"
)

var prNow = time.Unix(1_000_000, 0)

func prData() PRData {
	return PRData{Me: "huy", CheckedAt: prNow.Unix() - 20, PRs: []PRInfo{
		{Repo: "api", Number: 142, Title: "Shorter DHCP lease", State: PROpen, Base: "develop", Decision: "CHANGES_REQUESTED", Merge: "BEHIND",
			Checks: []PRCheck{{Name: "lint", State: CheckPass}, {Name: "test / go 1.24", State: CheckFail, RunID: 9}},
			Threads: []PRThread{
				{ID: "T1", Path: "dhcp/lease.go", Line: 24, Comments: []PRComment{{Author: "mai", Body: "Read this from config."}}},
				{ID: "T2", Path: "README.md", Line: 12, SentAt: prNow.Unix() - 7200, Comments: []PRComment{{Author: "mai", Body: "Mention it."}}},
				{ID: "T3", Path: "vlan/guest.go", Line: 8, Resolved: true, Comments: []PRComment{{Author: "kofi", Body: "ok"}}},
			}},
		{Repo: "web", Number: 88, State: PROpen, Merge: "CLEAN", Checks: []PRCheck{{Name: "build", State: CheckPass}}},
		{Repo: "infra", Missing: "not pushed"},
	}}
}

func TestPRScreenPicksOpenUnsentThreads(t *testing.T) {
	s := prScreen("guest-vlan-fix", "")
	s.SetData(prData())
	if got := s.Picked(); len(got) != 1 || got[0] != "T1" {
		t.Fatalf("Picked = %v", got)
	}
	if pr, _ := s.Current(); pr.Repo != "api" {
		t.Fatalf("opens on the PR that needs you, got %q", pr.Repo)
	}
}

func TestPRScreenTabCyclesFoundPRs(t *testing.T) {
	s := prScreen("guest-vlan-fix", "")
	s.SetData(prData())
	s.Key("Tab")
	if pr, _ := s.Current(); pr.Repo != "web" {
		t.Fatalf("Tab → %q", pr.Repo)
	}
	s.Key("Tab") // infra has no PR: skipped, wraps to api
	if pr, _ := s.Current(); pr.Repo != "api" {
		t.Fatalf("Tab wraps → %q", pr.Repo)
	}
	s.Key("S-Tab")
	if pr, _ := s.Current(); pr.Repo != "web" {
		t.Fatalf("S-Tab → %q", pr.Repo)
	}
}

func TestPRScreenKeys(t *testing.T) {
	s := prScreen("guest-vlan-fix", "api")
	s.SetData(prData())
	if s.Key("n"); len(s.Picked()) != 0 {
		t.Fatal("n clears")
	}
	if s.Key("a"); len(s.Picked()) != 2 { // T1 and T2 (sent but open); T3 resolved
		t.Fatalf("a picks open threads: %v", s.Picked())
	}
	for _, c := range []struct {
		key  string
		want PRAction
	}{{"F", PRFix}, {"A", PRAddress}, {"R", PRRefresh}, {"U", PRSync}, {"o", PROpenWeb}, {"Esc", PRBack}} {
		if got := s.Key(c.key); got != c.want {
			t.Errorf("%s = %v, want %v", c.key, got, c.want)
		}
	}
}

func TestPRScreenFocusRepo(t *testing.T) {
	s := prScreen("guest-vlan-fix", "web")
	s.SetData(prData())
	if pr, _ := s.Current(); pr.Repo != "web" {
		t.Fatalf("focus = %q", pr.Repo)
	}
}

func prScreen(story, repo string) *PRScreen {
	s := NewPRScreen(story, repo)
	s.Now = func() time.Time { return prNow }
	return s
}

func TestPRScreenRender(t *testing.T) {
	s := prScreen("guest-vlan-fix", "")
	s.SetData(prData())
	golden(t, "pr-workspace-120", s.Render(120, 20, true, false), 120)
	one := prData()
	one.PRs = one.PRs[:1]
	one.PRs[0].Repo = ""
	s = prScreen("guest-vlan-fix", "")
	s.SetData(one)
	golden(t, "pr-120", s.Render(120, 20, true, false), 120)
	golden(t, "pr-narrow-40", s.Render(40, 20, true, true), 40)
}

func TestPRScreenThreadDetail(t *testing.T) {
	s := prScreen("guest-vlan-fix", "")
	d := prData()
	d.PRs[0].Threads[0].Code = "-lease := time.Hour\n+lease := 24 * time.Hour"
	s.SetData(d)
	s.Key("j")
	s.Key("j") // lint, then the first thread
	if _, th := s.Selected(); th == nil || th.ID != "T1" {
		t.Fatalf("selected thread = %+v", th)
	}
	out := s.Render(120, 20, true, false)
	for _, want := range []string{"dhcp/lease.go:24", "+lease := 24 * time.Hour", "Read this from config."} {
		if !strings.Contains(out, want) {
			t.Errorf("detail lacks %q:\n%s", want, out)
		}
	}
	// narrow: Enter opens the detail, Esc closes it before leaving
	n := prScreen("guest-vlan-fix", "")
	n.SetData(d)
	n.Key("Enter")
	if out := n.Render(40, 20, true, true); strings.Contains(out, "THREADS") {
		t.Fatalf("detail should replace the list:\n%s", out)
	}
	if n.Key("Esc") != PRNone || n.Key("Esc") != PRBack {
		t.Fatal("Esc closes the detail, then leaves")
	}
}

func TestPRScreenNoPR(t *testing.T) {
	s := prScreen("s", "")
	s.SetData(PRData{PRs: []PRInfo{{Missing: "not pushed"}}})
	if out := s.Render(60, 6, true, false); !strings.Contains(out, "Not pushed. Push the branch and open a PR.") {
		t.Fatal(out)
	}
	if s.Bar(false) != "R refresh · esc back" {
		t.Fatal(s.Bar(false))
	}
	s.SetData(PRData{PRs: []PRInfo{{Missing: "no PR"}}})
	if out := s.Render(60, 6, true, false); !strings.Contains(out, "No pull request for this branch yet.") {
		t.Fatal(out)
	}
}
