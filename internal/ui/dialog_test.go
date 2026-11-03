package ui

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestDialogQuestion(t *testing.T) {
	var p Prompt
	p.Ask(PromptStop, "1", "Stop [1]?", "Fix guest DHCP lease time", "", "Claude stops and its window closes.")
	out := p.Dialog().Render(100, 30, true)
	for _, want := range []string{"╭─ Stop [1]? ─", "Fix guest DHCP lease time", "Claude stops and its window closes.", "n  Cancel", "[y  Stop]"} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in\n%s", want, out)
		}
	}
	lines := strings.Split(out, "\n")
	for _, l := range lines {
		if w := lipgloss.Width(l); w != lipgloss.Width(lines[0]) {
			t.Fatalf("ragged box: %d vs %d in\n%s", w, lipgloss.Width(lines[0]), out)
		}
	}
	// danger: Enter picks Cancel
	p.Ask(PromptDelete, "1", "Delete [1]?", "old")
	if out := p.Dialog().Render(100, 30, true); !strings.Contains(out, "[n  Cancel]") || strings.Contains(out, "[y") {
		t.Fatalf("a danger dialog defaults to Cancel:\n%s", out)
	}
	if p.Dialog().Tone != ToneDanger {
		t.Fatal("delete is a danger dialog")
	}
}

func TestDialogForm(t *testing.T) {
	var p Prompt
	p.Open(PromptRename, "", "Title of [2]", "old title")
	out := p.Dialog().Render(100, 30, true)
	for _, want := range []string{"╭─ Rename session ─", "Title of [2]", " old title▏", "esc  Cancel", "[⏎  Rename]"} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in\n%s", want, out)
		}
	}
}

func TestDialogFitsAndCutsWithMore(t *testing.T) {
	var items []string
	for i := range 10 {
		items = append(items, fmt.Sprintf("item %d", i))
	}
	d := Dialog{Title: "Remove 10 items?", Tone: ToneDanger, Body: items, Buttons: []Button{{Key: "n", Label: "Cancel", Default: true}, {Key: "y", Label: "Remove", Danger: true}}}
	out := d.Render(40, 12, false) // 4 rows for the items
	plain := ansi.Strip(out)
	if !strings.Contains(plain, "item 2") || strings.Contains(plain, "item 3") || !strings.Contains(plain, "+7 more") {
		t.Fatal(plain)
	}
	if len(items) != 10 || items[3] != "item 3" {
		t.Fatalf("the caller's lines changed: %v", items)
	}
	for _, l := range strings.Split(out, "\n") {
		if lipgloss.Width(l) > 38 {
			t.Fatalf("wider than the screen allows: %q", ansi.Strip(l))
		}
	}
}

func TestOverlayCentresAndFades(t *testing.T) {
	screen := strings.Repeat(strings.Repeat("x", 20)+"\n", 9) + "keybar"
	box := "+--+\n|ab|\n+--+"
	out := strings.Split(Overlay(screen, box, 20, 9), "\n")
	if len(out) != 10 || out[9] != "keybar" {
		t.Fatalf("the key bar stays as it is: %q", out)
	}
	if got := ansi.Strip(out[4]); got != "xxxxxxxx|ab|xxxxxxxx" {
		t.Fatalf("centred row %q", got)
	}
	if !strings.HasPrefix(out[0], "\x1b[2m") {
		t.Fatalf("the screen behind is faint: %q", out[0])
	}
}
