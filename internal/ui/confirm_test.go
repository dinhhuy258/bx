package ui

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func TestConfirmBox(t *testing.T) {
	out := ConfirmBox("Remove 2 items?", []string{"bx-home-network-7 · no record", "home-network · chats · [2] old · done"}, "remove", 50, 20)
	for _, want := range []string{"Remove 2 items?", "bx-home-network-7 · no record", "[2] old · done", "y remove", "n / esc cancel"} {
		if !strings.Contains(out, want) {
			t.Fatalf("no %q in\n%s", want, out)
		}
	}
	lines := strings.Split(out, "\n")
	if len(lines) != 20 {
		t.Fatalf("%d lines", len(lines))
	}
	for _, l := range lines {
		if lipgloss.Width(l) > 50 {
			t.Fatalf("line wider than 50: %q", l)
		}
	}
}

func TestConfirmBoxCutsWithMore(t *testing.T) {
	var items []string
	for i := range 10 {
		items = append(items, fmt.Sprintf("item %d", i))
	}
	out := ConfirmBox("Remove 10 items?", items, "remove", 40, 10) // 4 rows for items
	if !strings.Contains(out, "item 2") || strings.Contains(out, "item 3") || !strings.Contains(out, "+7 more") {
		t.Fatal(out)
	}
	if len(items) != 10 || items[3] != "item 3" {
		t.Fatalf("the caller's lines changed: %v", items)
	}
}
