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
	if !strings.Contains(out, "No stories yet.") || !strings.Contains(out, "└ no chats\n") {
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
	for _, want := range []string{"j/k move", "⏎ type in Claude", "Z fold all", "F focus", "N new session", "A inbox", "O projects"} {
		if !strings.Contains(kb, want) {
			t.Errorf("list bar missing %q: %s", want, kb)
		}
	}
	m.SelectSession(2) // waiting: answered only in the Claude pane
	if kb = KeyBar(m, cfg, BarContext{}); strings.Contains(kb, "y yes") {
		t.Error("no quick answer on a waiting session:", kb)
	}
	if got := KeyBar(m, cfg, BarContext{ClaudeFocused: true, Session: 2}); got != "CLAUDE  ↑↓ move  ⏎ confirm  esc no  ^h to list" || strings.Contains(got, "1-3") {
		t.Errorf("Claude bar while asking: %q", got)
	}
	for _, want := range []string{"S stop", "R rename", "D delete"} {
		if !strings.Contains(kb, want) {
			t.Errorf("session bar missing %q: %s", want, kb)
		}
	}
	if strings.Contains(kb, "D close story") {
		t.Error("close story only on story rows:", kb)
	}
	if i, j := strings.Index(kb, "S stop"), strings.Index(kb, "N new session"); i < 0 || j < 0 || i > j {
		t.Error("lifecycle keys come before the global keys:", kb)
	}
	if !strings.HasPrefix(kb, "[2] Write the test") || !strings.Contains(kb, " › ⏎ type in Claude") || strings.Index(kb, "│") > strings.Index(kb, "j/k move") {
		t.Error("the bar names the session, then its keys, then the global ones:", kb)
	}
	m.SelectStory("guest-vlan-fix")
	if kb = KeyBar(m, cfg, BarContext{}); !strings.HasPrefix(kb, "story guest-vlan-fix › ⏎ collapse  D close  │") || strings.Contains(kb, "D delete") {
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
	if !strings.Contains(kb, "⏎ resume") || strings.Contains(kb, "type in Claude") || strings.Contains(kb, "S stop") || strings.Contains(kb, "F focus") {
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

	m.SelectStory("guest-vlan-fix")
	if kb = KeyBar(m, cfg, BarContext{}); !strings.Contains(kb, "T new shell") {
		t.Error("a story row offers a new shell:", kb)
	}
	m.SetShells([]Shell{{Story: "guest-vlan-fix", K: 1, Command: "zsh"}}, nil)
	m.SelectShell("guest-vlan-fix", 1)
	kb = KeyBar(m, cfg, BarContext{})
	for _, want := range []string{"$1 zsh › ⏎ open  R rename  D close  │", "T new shell"} {
		if !strings.Contains(kb, want) {
			t.Errorf("shell bar missing %q: %s", want, kb)
		}
	}
	if strings.Contains(kb, "D delete") {
		t.Error("D closes a shell, it deletes nothing:", kb)
	}
	if got, want := KeyBar(m, cfg, BarContext{ClaudeFocused: true, Shell: true}), "SHELL  type to run · exit closes it  ^h to list  M-c copy"; got != want {
		t.Errorf("shell pane bar:\n got %q\nwant %q", got, want)
	}
}

func TestRenderShellRows(t *testing.T) {
	m, _ := sampleModel()
	m.SetShells([]Shell{{Story: "guest-vlan-fix", K: 1, Command: "zsh"}, {Story: "guest-vlan-fix", K: 2, Title: "dev server", Command: "npm"}}, nil)
	out := Render(m, 46, 40, RenderOptions{Plain: true})
	lines := strings.Split(out, "\n")
	var one, two string
	for i, line := range lines {
		if w := runeWidth(line); w > 46 {
			t.Errorf("line %d is %d cells wide: %q", i, w, line)
		}
		switch {
		case strings.Contains(line, "$1 zsh"):
			one = line
		case strings.Contains(line, "$2 dev server"):
			two = line
		}
	}
	if one == "" || strings.TrimRight(one, " ") != " ├ $1 zsh" {
		t.Errorf("no $1 row with only its command:\n%s", out)
	}
	if !strings.HasSuffix(two, "npm") {
		t.Errorf("the $2 row shows its title and the command on the right, got %q:\n%s", two, out)
	}
	if i, j := strings.Index(out, "Shorter lease for the IoT VLAN"), strings.Index(out, "$1 zsh"); i < 0 || j < i {
		t.Errorf("shells come after the story's sessions:\n%s", out)
	}
}

func TestFocusLine(t *testing.T) {
	m, _ := sampleModel()
	m.SelectSession(1)
	line := FocusLine(m, "A", "C-h", 1, []WaitingElsewhere{{Project: "ha-hermes-assist", Num: 1}})
	want := "home-network · [1] Fix guest DHCP lease time · running    ◆ ha-hermes-assist [1] needs you · A inbox · ● 1 running · ^h exit"
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
	tabs := []ProjectTab{{Name: "bx", Waiting: 1}, {Name: "home-network", Waiting: 1, Running: 2}, {Name: "nas"}}
	head := func(tabs []ProjectTab) string {
		return strings.Split(Render(m, 46, 30, RenderOptions{Plain: true, Projects: tabs}), "\n")[0]
	}
	// numbered tabs, the others with what waits or runs, the open one's counts on the right
	if got := head(tabs); got != "bx  1 bx ◆1  2 home-network  3 nas    ● 2  ◆ 1" {
		t.Fatalf("tabs %q", got)
	}
	// too narrow for all: the open one stays, the cut ones are counted with what waits in them
	tabs = append([]ProjectTab{{Name: "a-very-long-project", Waiting: 2}, {Name: "another-long-one"}}, tabs...)
	got := head(tabs)
	if !strings.Contains(got, "home-network") || !strings.Contains(got, "+") || !strings.Contains(got, "◆2") || runeWidth(got) > 46 {
		t.Fatalf("cut tabs %q", got)
	}
	// no opened projects: the project's name instead of tabs (the golden test)
	if Render(m, 46, 30, RenderOptions{Plain: true, Projects: tabs[:0]}) != Render(m, 46, 30, RenderOptions{Plain: true}) {
		t.Fatal("no projects must not add tabs")
	}
}

func TestActivityRowsAndRender(t *testing.T) {
	home, _ := sampleModel()
	home.Live.Unread = map[string]bool{"u1": true, "u3": true, "u6": true} // u1 runs again: not unread
	p, live := sampleHomeNetwork()
	p.Name = "bx"
	live.Agents["u2"] = claude.Agent{SessionID: "u2", Status: "idle"} // bx has nothing waiting
	live.Unread = map[string]bool{"u7": true}
	bx := NewModel(p)
	bx.SetLive(live)
	rows := ActivityRows([]*Model{bx, home})
	// running and read idle sessions are left out; waiting comes before unread
	want := []ActivityRow{{"home-network", 0}, {"home-network", 2}, {"home-network", 3}, {"home-network", 6}, {"bx", 0}, {"bx", 7}}
	if fmt.Sprint(rows) != fmt.Sprint(want) {
		t.Fatalf("rows %v\nwant %v", rows, want)
	}
	var a Activity
	out := RenderActivity([]*Model{bx, home}, &a, 46, 30, RenderOptions{Plain: true}, "")
	for _, s := range []string{"bx inbox", "2 projects", "◆ 1 needs you", "◎ 3 unread", "  home-network", "◆ 1  ◎ 2", "›  ◆ [2] Write the test checklist",
		"guest-vlan-fix · asks: permission prompt", "◎ [3] Shorter lease for the IoT VLAN", "guest-vlan-fix · idle · unread", "chats · idle · unread"} {
		if !strings.Contains(out, s) {
			t.Errorf("missing %q in\n%s", s, out)
		}
	}
	if strings.Contains(out, "running") || strings.Contains(out, "[1] Fix guest") {
		t.Errorf("running sessions in the inbox:\n%s", out)
	}
	// the cursor stays on its session when the rows move
	a.Move(rows, 2) // from home-network [2] to [6]
	if a.Selected(rows) != (ActivityRow{"home-network", 6}) {
		t.Fatal(a.Selected(rows))
	}
	home.Live.Agents["u2"] = claude.Agent{SessionID: "u2", Status: "idle"} // answered: it leaves
	if r := a.Selected(ActivityRows([]*Model{bx, home})); r != (ActivityRow{"home-network", 6}) {
		t.Fatalf("cursor moved to %v", r)
	}
	if out := RenderActivity(nil, &a, 46, 10, RenderOptions{Plain: true}, "A back"); !strings.Contains(out, "Nothing needs you.") {
		t.Fatal(out)
	}
}
