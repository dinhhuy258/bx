package client

import (
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/dinhhuy258/bx/internal/api"
)

func prFixture() api.PRData {
	return api.PRData{Me: "huy", PRs: []api.PRInfo{{
		Repo: "api", Number: 142, Title: "Shorter DHCP lease", State: "OPEN", Base: "develop", URL: "https://github.com/acme/netcfg/pull/142",
		Checks: []api.PRCheck{{Name: "test / go 1.24", State: "fail", URL: "https://github.com/acme/netcfg/actions/runs/9", RunID: 9}},
		Threads: []api.PRThread{
			{ID: "T1", Path: "dhcp/lease.go", Line: 24, Comments: []api.PRComment{{Author: "mai", Body: "Read this from config."}}},
		}}}}
}

func TestMmOpensPRScreenOnStory(t *testing.T) {
	h := start(t, 120, 30, "idle")
	h.be.pr = prFixture()
	h.press("k", "M-m") // the story row s1, then the PR screen
	if h.m.pr == nil || h.m.pr.Story != "s1" {
		t.Fatalf("pr screen not open: %+v", h.m.pr)
	}
	if !slices.Contains(h.be.reads, "pr home-network s1") {
		t.Fatalf("reads = %v", h.be.reads)
	}
	if v := ansi.Strip(h.m.View().Content); !strings.Contains(v, "pr #142 Shorter DHCP lease") {
		t.Fatalf("screen:\n%s", v)
	}
	h.press("Esc")
	if h.m.pr != nil {
		t.Fatal("Esc must leave the PR screen")
	}
}

func TestMmFromClaudePane(t *testing.T) {
	h := start(t, 120, 30, "idle")
	h.be.pr = prFixture()
	h.press("Enter") // open session 1: the keys go to Claude
	if !h.m.claudeFocused {
		t.Fatal("Claude pane not focused")
	}
	h.press("M-m")
	if h.m.pr == nil || h.m.pr.Story != "s1" {
		t.Fatalf("pr screen not open on the story of the session: %+v", h.m.pr)
	}
}

func TestPRAddressSendsPickedThreadsOfCurrentPR(t *testing.T) {
	h := start(t, 120, 30, "idle")
	h.be.pr = prFixture()
	h.be.sendRes = api.Result{Num: 1, Message: "Sent 1 comment to [1]"}
	h.press("k", "M-m", "A") // T1 is open and unsent: picked from the start
	v := ansi.Strip(h.m.View().Content)
	if !strings.Contains(v, "Address 1 comment of #142 in…") || !strings.Contains(v, "[1] Add lease time") {
		t.Fatalf("picker:\n%s", v)
	}
	h.press("Enter")
	want := api.PRSend{Kind: "address", Repo: "api", Threads: []string{"T1"}, Session: 1}
	if len(h.be.prSends) != 1 || !slices.Equal(h.be.prSends[0].Threads, want.Threads) || h.be.prSends[0].Kind != "address" || h.be.prSends[0].Repo != "api" || h.be.prSends[0].Session != 1 || h.be.prSends[0].New {
		t.Fatalf("sends = %+v", h.be.prSends)
	}
	if !strings.Contains(ansi.Strip(h.m.bar()), "Sent 1 comment to [1]") {
		t.Fatalf("bar %q", ansi.Strip(h.m.bar()))
	}
	h.press("n", "A") // nothing picked
	if len(h.be.prSends) != 1 || !strings.Contains(ansi.Strip(h.m.bar()), "pick a comment first") {
		t.Fatalf("sends %+v bar %q", h.be.prSends, ansi.Strip(h.m.bar()))
	}
}

func TestPRFixSendsKindFix(t *testing.T) {
	h := start(t, 120, 30, "idle")
	h.be.pr = prFixture()
	h.press("k", "M-m", "F", "Enter")
	if len(h.be.prSends) != 1 || h.be.prSends[0].Kind != "fix" || h.be.prSends[0].Repo != "api" || h.be.prSends[0].Session != 1 {
		t.Fatalf("sends = %+v", h.be.prSends)
	}
	h.be.pr.PRs[0].Checks[0].State = "pass"
	h.press("R")
	h.press("F")
	if len(h.be.prSends) != 1 || !strings.Contains(ansi.Strip(h.m.bar()), "no failed check") {
		t.Fatalf("sends %+v bar %q", h.be.prSends, ansi.Strip(h.m.bar()))
	}
}

func TestPRRefresh(t *testing.T) {
	h := start(t, 120, 30, "idle")
	h.be.pr = prFixture()
	h.press("k", "M-m")
	h.be.pr.PRs[0].Title = "Newer title"
	h.press("R")
	if h.be.prRefreshes != 1 || !strings.Contains(ansi.Strip(h.m.View().Content), "Newer title") {
		t.Fatalf("refreshes %d:\n%s", h.be.prRefreshes, ansi.Strip(h.m.View().Content))
	}
}

func TestPROpenWeb(t *testing.T) {
	h := start(t, 120, 30, "idle")
	var opened []string
	h.m.browse = func(u string) tea.Cmd { opened = append(opened, u); return nil }
	h.be.pr = prFixture()
	h.press("k", "M-m", "o") // the failed check is selected: its run
	h.press("j", "j", "o")   // a thread: the PR
	if len(opened) != 2 || opened[0] != "https://github.com/acme/netcfg/actions/runs/9" || opened[1] != "https://github.com/acme/netcfg/pull/142" {
		t.Fatalf("opened = %v", opened)
	}
}

func TestStoryRowShowsPRKeyWithABadge(t *testing.T) {
	h := start(t, 120, 30, "idle")
	h.press("k")
	if strings.Contains(ansi.Strip(h.m.bar()), "M-m pr") {
		t.Fatalf("no PR, no key: %q", ansi.Strip(h.m.bar()))
	}
	snap := h.be.snaps["home-network"]
	snap.Live.PR = map[string]api.PRBadge{"s1": {Count: 1, Number: 142, Fail: true}}
	h.be.snaps["home-network"] = snap
	h.update(tickMsg(time.Now()))
	if !strings.Contains(ansi.Strip(h.m.bar()), "M-m pr") {
		t.Fatalf("bar %q", ansi.Strip(h.m.bar()))
	}
}
