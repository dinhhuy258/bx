package ui

import (
	"flag"
	"os"
	"strings"
	"testing"

	"github.com/dinhhuy258/bx/internal/config"
	"github.com/dinhhuy258/bx/internal/state"
)

var update = flag.Bool("update", false, "rewrite golden files")

func TestRenderGolden46x30(t *testing.T) {
	m, _ := sampleModel(Options{AutoExpandWaiting: true})
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
	m := NewModel(p, Options{})
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
	m := NewModel(p, Options{})
	m.SetLive(live)
	out := Render(m, 46, 20, RenderOptions{Plain: true})
	if !strings.Contains(out, "first chat") || !strings.Contains(out, "second chat") || !strings.Contains(out, "No stories yet.") {
		t.Fatalf("chats should be listed with no stories:\n%s", out)
	}
}

func TestRenderTooNarrow(t *testing.T) {
	m, _ := sampleModel(Options{})
	if out := Render(m, 24, 10, RenderOptions{Plain: true}); !strings.Contains(out, "bx: terminal too narrow") {
		t.Fatal(out)
	}
}

func TestKeyBar(t *testing.T) {
	m, _ := sampleModel(Options{AutoExpandWaiting: true})
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
	m.SelectStory("zigbee-map-docs")
	if kb = KeyBar(m, cfg, BarContext{}); !strings.Contains(kb, "⏎ expand") {
		t.Error(kb)
	}
	kb = KeyBar(m, cfg, BarContext{ClaudeFocused: true})
	if !strings.Contains(kb, "esc interrupt") || !strings.Contains(kb, "^/ to list") {
		t.Error(kb)
	}
	kb = KeyBar(m, cfg, BarContext{Seq: "z"})
	if !strings.Contains(kb, "waiting for the next key") {
		t.Error(kb)
	}
}

func TestFocusLine(t *testing.T) {
	m, _ := sampleModel(Options{AutoExpandWaiting: true})
	m.SelectSession(1)
	line := FocusLine(m, "a", "C-/", 1, []WaitingElsewhere{{Project: "ha-hermes-assist", Num: 1}})
	want := "home-network · [1] Fix guest DHCP lease time · running    ◆ ha-hermes-assist [1] needs you · a jump · ● 1 running · ^/ exit"
	if line != want {
		t.Fatalf("\n got %q\nwant %q", line, want)
	}
}

func TestRenderLongProjectPathNeverPanicsOrOverflows(t *testing.T) {
	p, live := sampleHomeNetwork()
	p.Path = "/Users/someone-with-a-long-name/Workspace/projects/clients/home-network-infrastructure"
	m := NewModel(p, Options{})
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
