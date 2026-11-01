package ui

import (
	"strings"
	"testing"
)

func TestPickerFiltersMovesAndChooses(t *testing.T) {
	p := NewPicker("projects", []string{"home-network", "lab", "+ Add existing folder"})
	if p.Key("Down", "") != PickKeep || p.Choice() != "lab" {
		t.Fatal(p.Choice())
	}
	p.Key("C-k", "")
	if p.Choice() != "home-network" {
		t.Fatal(p.Choice())
	}
	for _, r := range "ad" {
		p.Key(string(r), string(r))
	}
	if p.Choice() != "+ Add existing folder" { // "ad" matches only this one
		t.Fatal(p.Choice())
	}
	p.Key("BSpace", "")
	p.Key("BSpace", "")
	if p.Key("Enter", "") != PickChoose || p.Choice() != "home-network" {
		t.Fatal(p.Choice())
	}
	if p.Key("Esc", "") != PickCancel {
		t.Fatal("Esc must cancel")
	}
	out := p.Render(40, 10)
	if !strings.Contains(out, "projects") || !strings.Contains(out, "lab") {
		t.Fatal(out)
	}
	empty := NewPicker("x", nil)
	if empty.Key("Enter", "") != PickKeep || empty.Choice() != "" {
		t.Fatal("Enter on an empty list must do nothing")
	}
}

func TestChecklistTicks(t *testing.T) {
	p := NewChecklist("repos", []string{"api", "libs/auth", "web"})
	if got := p.Checked(); len(got) != 1 || got[0] != "api" {
		t.Fatalf("nothing ticked takes the highlighted one: %v", got)
	}
	p.Key("Space", " ")
	p.Key("Down", "")
	p.Key("Down", "")
	p.Key("Space", " ")
	p.Key("w", "w") // the filter hides api, which stays ticked
	if got := strings.Join(p.Checked(), " "); got != "api web" {
		t.Fatalf("checked = %q", got)
	}
	p.Key("Space", " ") // untick web
	if got := strings.Join(p.Checked(), " "); got != "api" {
		t.Fatalf("checked = %q", got)
	}
	if out := p.Render(40, 10); !strings.Contains(out, "[ ] web") || !strings.Contains(out, "space tick") {
		t.Fatal(out)
	}
}
