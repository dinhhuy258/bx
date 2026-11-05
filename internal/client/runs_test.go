package client

import (
	"strings"
	"testing"
	"time"

	"github.com/dinhhuy258/bx/internal/api"
	"github.com/dinhhuy258/bx/internal/runcfg"
	"github.com/dinhhuy258/bx/internal/ui"

	tea "charm.land/bubbletea/v2"
)

// runConfig gives home-network two runs.
func runConfig(h *harness) {
	h.be.runCfgs = map[string]api.RunConfig{"home-network": {Runs: []runcfg.Run{{Name: "web", Cmd: "npm run dev"}, {Name: "api", Cmd: "php artisan serve"}}}}
}

func TestRunMenuRunsTheStory(t *testing.T) {
	h := start(t, 120, 30, "running")
	runConfig(h)
	h.press("k", "M-b") // on the s1 story row
	if v := view(h); !strings.Contains(v, "Run all") || !strings.Contains(v, "Run one…") {
		t.Fatal(v)
	}
	h.press("Enter") // Run all
	if got := strings.Join(h.be.runActs, "|"); got != "home-network run s1" {
		t.Fatalf("acts %q", got)
	}
	if !strings.Contains(h.m.flash, "s1: starting web, api") {
		t.Errorf("flash %q", h.m.flash)
	}
}

func TestRunMenuWhileRunning(t *testing.T) {
	h := start(t, 120, 30, "running")
	runConfig(h)
	setRunning(h, "s1", ui.Port{Port: 3000, URL: "http://dev:3000/login", Shell: 1, Run: "web"})
	var browsed []string
	h.m.browse = func(u string) tea.Cmd { browsed = append(browsed, u); return nil }
	h.press("k", "M-b")
	v := view(h)
	for _, w := range []string{"Open http://dev:3000/login", "Restart", "Stop", "Setup again…"} {
		if !strings.Contains(v, w) {
			t.Fatalf("missing %q:\n%s", w, v)
		}
	}
	h.press("Enter") // the link is first
	if len(browsed) != 1 || browsed[0] != "http://dev:3000/login" {
		t.Errorf("browsed %q", browsed)
	}
	h.press("M-b", "s", "t", "o", "p", "Enter") // the filter picks Stop
	if got := h.be.runActs[len(h.be.runActs)-1]; got != "home-network stop s1" {
		t.Errorf("stop: %q", got)
	}
	h.press("M-b", "s", "e", "t", "u", "p", "Enter", "a", "p", "i", "Enter")
	if got := h.be.runActs[len(h.be.runActs)-1]; got != "home-network setup s1 [api]" {
		t.Errorf("setup again: %q", got)
	}
}

func TestRunConflictAsks(t *testing.T) {
	h := start(t, 120, 30, "running")
	runConfig(h)
	h.be.runRes = api.Result{RunStory: "s2", RunNames: []string{"web"}}
	h.be.runErr = &StatusError{Code: 409, Text: "s2 is running web"}
	h.press("k", "M-b", "Enter")
	if v := view(h); !strings.Contains(v, "Stop s2 and run s1?") {
		t.Fatal(v)
	}
	h.be.runRes, h.be.runErr = api.Result{}, nil
	h.press("y")
	if got := strings.Join(h.be.runActs, "|"); got != "home-network run s1|home-network run s1 replace" {
		t.Errorf("acts %q", got)
	}
}

func TestRunIsUpFlash(t *testing.T) {
	h := start(t, 120, 30, "running")
	runConfig(h)
	h.press("k", "M-b", "Enter")
	setRunning(h, "s1", ui.Port{Port: 3000, URL: "http://dev:3000", Shell: 1, Run: "web"})
	if h.m.flash != "s1 is up · http://dev:3000" {
		t.Errorf("flash %q", h.m.flash)
	}
}

// setRunning makes story the running one in the fake snapshot, with its ports, and polls.
func setRunning(h *harness, story string, ports ...ui.Port) {
	snap := h.be.snaps["home-network"]
	snap.Live.Running = story
	snap.Live.Ports = map[string][]ui.Port{story: ports}
	for _, p := range ports {
		if p.Run != "" {
			snap.Shells = append(snap.Shells, api.Shell{Story: story, K: p.Shell, Command: "node", Run: p.Run})
		}
	}
	h.be.snaps["home-network"] = snap
	h.update(tickMsg(time.Now()))
}
