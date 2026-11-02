package ui

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/dinhhuy258/bx/internal/claude"
	"github.com/dinhhuy258/bx/internal/config"
	"github.com/dinhhuy258/bx/internal/state"
)

var update = flag.Bool("update", false, "rewrite golden files")

func TestRenderGolden46x30(t *testing.T) {
	m, _ := sampleModel()
	got := Render(m, 46, 30, RenderOptions{Plain: true})
	golden := "testdata/home-network-46x30.txt"
	if *update {
		os.WriteFile(golden, []byte(got), 0o644)
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (run with -update once to create it)", err)
	}
	if got != string(want) {
		t.Fatalf("screen differs:\n%s\n---- want ----\n%s", got, want)
	}
	for i, line := range strings.Split(got, "\n") {
		if w := runeWidth(line); w > 46 {
			t.Errorf("line %d is %d cells wide: %q", i, w, line)
		}
	}
}

func TestRenderEmptyProject(t *testing.T) {
	p, live := sampleHomeNetwork()
	for k := range p.Stories {
		delete(p.Stories, k)
	}
	for k := range p.Sessions {
		delete(p.Sessions, k)
	}
	m := NewModel(p)
	m.SetLive(live)
	out := Render(m, 46, 20, RenderOptions{Plain: true})
	if !strings.Contains(out, "No stories yet.") || !strings.Contains(out, "no chats · c to start one") {
		t.Fatalf("empty render:\n%s", out)
	}
}

func TestRenderChatsWithoutStories(t *testing.T) {
	p, live := sampleHomeNetwork()
	for k := range p.Stories {
		delete(p.Stories, k)
	}
	for k := range p.Sessions {
		delete(p.Sessions, k)
	}
	p.AddSession(&state.Session{UUID: "c1", Title: "first chat", Created: live.Now})
	p.AddSession(&state.Session{UUID: "c2", Title: "second chat", Created: live.Now})
	m := NewModel(p)
	m.SetLive(live)
	out := Render(m, 46, 20, RenderOptions{Plain: true})
	if !strings.Contains(out, "first chat") || !strings.Contains(out, "second chat") || !strings.Contains(out, "No stories yet.") {
		t.Fatalf("chats should be listed with no stories:\n%s", out)
	}
}

func TestRenderTooNarrow(t *testing.T) {
	m, _ := sampleModel()
	if out := Render(m, 24, 10, RenderOptions{Plain: true}); !strings.Contains(out, "bx: terminal too narrow") {
		t.Fatal(out)
	}
}

func TestKeyBar(t *testing.T) {
	m, _ := sampleModel()
	cfg := config.Default("/x")
	kb := KeyBar(m, cfg, BarContext{})
	for _, want := range []string{"j/k move", "⏎ type in Claude", "Z fold all", "F focus", "c new chat", "a jump to waiting", "o projects"} {
		if !strings.Contains(kb, want) {
			t.Errorf("list bar missing %q: %s", want, kb)
		}
	}
	if strings.Contains(kb, "y yes") {
		t.Error("y yes only on a waiting session")
	}
	m.SelectSession(2)
	if kb = KeyBar(m, cfg, BarContext{}); !strings.Contains(kb, "y yes") {
		t.Error(kb)
	}
	for _, want := range []string{"s stop", "R rename", "X delete"} {
		if !strings.Contains(kb, want) {
			t.Errorf("session bar missing %q: %s", want, kb)
		}
	}
	if strings.Contains(kb, "D close story") {
		t.Error("close story only on story rows:", kb)
	}
	if i, j := strings.Index(kb, "s stop"), strings.Index(kb, "n new session"); i < 0 || j < 0 || i > j {
		t.Error("lifecycle keys come before the global keys:", kb)
	}
	m.SelectStory("guest-vlan-fix")
	if kb = KeyBar(m, cfg, BarContext{}); !strings.Contains(kb, "D close story") || strings.Contains(kb, "X delete") {
		t.Error(kb)
	}
	m.SelectStory("zigbee-map-docs")
	if kb = KeyBar(m, cfg, BarContext{}); !strings.Contains(kb, "⏎ expand") {
		t.Error(kb)
	}
	m.SetFolded("zigbee-map-docs", false)
	m.SelectSession(5) // done
	if n, _ := m.SelectedSession(); n != 5 {
		t.Fatal("selected", n)
	}
	kb = KeyBar(m, cfg, BarContext{})
	if !strings.Contains(kb, "r resume") || strings.Contains(kb, "s stop") || strings.Contains(kb, "F focus") {
		t.Error(kb)
	}
	kb = KeyBar(m, cfg, BarContext{ClaudeFocused: true})
	if !strings.Contains(kb, "esc interrupt") || !strings.Contains(kb, "^h to list") {
		t.Error(kb)
	}
	kb = KeyBar(m, cfg, BarContext{Seq: "z"})
	if !strings.Contains(kb, "waiting for the next key") {
		t.Error(kb)
	}
}

func TestFocusLine(t *testing.T) {
	m, _ := sampleModel()
	m.SelectSession(1)
	line := FocusLine(m, "a", "C-h", 1, []WaitingElsewhere{{Project: "ha-hermes-assist", Num: 1}})
	want := "home-network · [1] Fix guest DHCP lease time · running    ◆ ha-hermes-assist [1] needs you · a jump · ● 1 running · ^h exit"
	if line != want {
		t.Fatalf("\n got %q\nwant %q", line, want)
	}
}

func TestRenderLongProjectPathNeverPanicsOrOverflows(t *testing.T) {
	p, live := sampleHomeNetwork()
	p.Path = "/Users/someone-with-a-long-name/Workspace/projects/clients/home-network-infrastructure"
	m := NewModel(p)
	m.SetLive(live)
	for w := 25; w <= 60; w++ {
		out := Render(m, w, 20, RenderOptions{Plain: true})
		for i, line := range strings.Split(out, "\n") {
			if got := runeWidth(line); got > w {
				t.Fatalf("width %d: line %d is %d cells: %q", w, i, got, line)
			}
		}
	}
}

func TestShortPathUsesHome(t *testing.T) {
	home, _ := os.UserHomeDir()
	if got := shortPath(home + "/Workspace/bx"); got != "~/Workspace/bx" {
		t.Fatal(got)
	}
	if got := shortPath("/srv/repo"); got != "/srv/repo" {
		t.Fatal(got)
	}
}

func TestRenderProjectStrip(t *testing.T) {
	m, _ := sampleModel()
	tabs := []ProjectTab{{Name: "bx", Waiting: 1}, {Name: "home-network", Waiting: 1, Running: 2}, {Name: "dotfiles"}, {Name: "lab"}}
	lines := strings.Split(Render(m, 46, 30, RenderOptions{Plain: true, Projects: tabs}), "\n")
	if lines[2] != "◆ bx  ◉ home-network  ○ dotfiles  ○ lab" {
		t.Fatalf("strip %q", lines[2])
	}
	// too narrow for all: the projects before the open one go first
	tabs = append([]ProjectTab{{Name: "a-very-long-project"}, {Name: "another-long-one"}}, tabs...)
	line := strings.Split(Render(m, 46, 30, RenderOptions{Plain: true, Projects: tabs}), "\n")[2]
	if !strings.HasPrefix(line, "… ") || !strings.Contains(line, "home-network") || runeWidth(line) > 46 {
		t.Fatalf("cut strip %q", line)
	}
	// no opened projects: no strip, the list is as before (the golden test)
	if Render(m, 46, 30, RenderOptions{Plain: true, Projects: tabs[:0]}) != Render(m, 46, 30, RenderOptions{Plain: true}) {
		t.Fatal("no projects must not add a strip")
	}
}

func TestActivityRowsAndRender(t *testing.T) {
	home, _ := sampleModel()
	p, live := sampleHomeNetwork()
	p.Name = "bx"
	live.Agents["u2"] = claude.Agent{SessionID: "u2", Status: "idle"} // bx has nothing waiting
	bx := NewModel(p)
	bx.SetLive(live)
	rows := ActivityRows([]*Model{bx, home})
	want := []ActivityRow{{"home-network", 0}, {"home-network", 2}, {"home-network", 1}, {"home-network", 4}, {"bx", 0}, {"bx", 1}, {"bx", 4}}
	if fmt.Sprint(rows) != fmt.Sprint(want) {
		t.Fatalf("rows %v\nwant %v", rows, want)
	}
	var a Activity
	out := RenderActivity([]*Model{bx, home}, &a, 46, 30, RenderOptions{Plain: true}, "")
	for _, s := range []string{"bx activity", "2 projects", "● 4 running", "◆ 1 needs you", "  home-network", "◆ 1  ● 2", "›  ◆ [2] Write the test checklist", "guest-vlan-fix · asks: permission prompt"} {
		if !strings.Contains(out, s) {
			t.Errorf("missing %q in\n%s", s, out)
		}
	}
	// the cursor stays on its session when the rows move
	a.Move(rows, 2) // from home-network [2] to [4]
	if a.Selected(rows) != (ActivityRow{"home-network", 4}) {
		t.Fatal(a.Selected(rows))
	}
	home.Live.Agents["u1"] = claude.Agent{SessionID: "u1", Status: "idle"}
	if r := a.Selected(ActivityRows([]*Model{bx, home})); r != (ActivityRow{"home-network", 4}) {
		t.Fatalf("cursor moved to %v", r)
	}
	if out := RenderActivity(nil, &a, 46, 10, RenderOptions{Plain: true}, "A back"); !strings.Contains(out, "Nothing is running or waiting.") {
		t.Fatal(out)
	}
}
