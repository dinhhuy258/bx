package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func switcherSample() *Switcher {
	return NewSwitcher([]SwitcherRow{
		{Name: "bx", Open: true, Unread: 1},
		{Name: "dotfiles"},
		{Name: "home-network", Open: true, Running: 2, Waiting: 1},
		{Name: "lab"},
	}, "bx")
}

func TestSwitcherListsOpenFirstAndStartsOnAnotherProject(t *testing.T) {
	s := switcherSample()
	if s.Choice() != "dotfiles" {
		t.Fatalf("starts on %q, want the first one not open", s.Choice())
	}
	out := ansi.Strip(s.Dialog(30, true).Render(120, 30, true))
	open, server := strings.Index(out, "OPEN HERE"), strings.Index(out, "ON THE SERVER")
	if open < 0 || server < open || strings.Index(out, "home-network") > server || strings.Index(out, "lab") < server {
		t.Fatalf("open projects first:\n%s", out)
	}
	for _, want := range []string{"● 2  ◆ 1", "◎ 1", "+  Existing folder", "+  Clone from GitHub", "+  New empty"} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in\n%s", want, out)
		}
	}
	// all open: start on the one after the current
	if s := NewSwitcher([]SwitcherRow{{Name: "bx", Open: true}, {Name: "lab", Open: true}}, "bx"); s.Choice() != "lab" {
		t.Fatal(s.Choice())
	}
}

func TestSwitcherKeys(t *testing.T) {
	s := switcherSample()
	for _, k := range []string{"h", "o", "m"} {
		s.Key(k, k)
	}
	if s.Choice() != "home-network" || s.Key("Enter", "") != SwitchOpen {
		t.Fatalf("filter and open: %q", s.Choice())
	}
	// ⌫ with no filter closes an open project, not one that is only on the server
	s = switcherSample()
	if r := s.Key("BSpace", ""); r != SwitchKeep {
		t.Fatalf("dotfiles is not open: %v", r)
	}
	s.Key("Up", "")
	if s.Choice() != "home-network" || s.Key("BSpace", "") != SwitchClose {
		t.Fatalf("close %q", s.Choice())
	}
	// Tab moves to the buttons, ←→ choose, Enter adds
	s = switcherSample()
	s.Key("Tab", "")
	s.Key("Right", "")
	if s.Key("Enter", "") != SwitchAction || s.Action() != SwitchClone {
		t.Fatal(s.Action())
	}
	// nothing matches: Enter takes the action the filter names
	s = switcherSample()
	for _, k := range "empty" {
		s.Key(string(k), string(k))
	}
	if s.Key("Enter", "") != SwitchAction || s.Action() != SwitchNewEmpty {
		t.Fatal(s.Action())
	}
	if s.Key("Esc", "") != SwitchCancel {
		t.Fatal("esc")
	}
	// the Workspace button
	s = switcherSample()
	for _, k := range "workspace" { // "work" alone also matches home-network
		s.Key(string(k), string(k))
	}
	if s.Key("Enter", "") != SwitchAction || s.Action() != SwitchWorkspace {
		t.Fatal(s.Action())
	}
}

func TestSwitcherArrowsReachTheButtons(t *testing.T) {
	s := switcherSample() // on dotfiles, then lab is the last
	s.Key("Down", "")
	s.Key("Down", "") // past lab: the first button
	if !s.onButtons || s.Action() != SwitchAddFolder {
		t.Fatalf("on buttons %v action %d", s.onButtons, s.Action())
	}
	s.Key("Right", "")
	if s.Key("Enter", "") != SwitchAction || s.Action() != SwitchClone {
		t.Fatal("→ ⏎ clones:", s.Action())
	}
	s.Key("Up", "") // back to the project it left from
	if s.onButtons || s.Choice() != "lab" {
		t.Fatalf("↑ left the buttons for %q", s.Choice())
	}
	// the selected project is drawn across the box, the hint says how to reach the buttons
	d := s.Dialog(30, true)
	if d.Mark == 0 || !strings.Contains(ansi.Strip(d.After[d.Mark-1]), "lab") {
		t.Fatalf("mark %d in %q", d.Mark, d.After)
	}
	if out := ansi.Strip(d.Render(120, 30, true)); !strings.Contains(out, "↓ past the last project to add one") {
		t.Fatal(out)
	}
}
