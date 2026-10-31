package ui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// PickResult is what a key did to the picker.
type PickResult int

const (
	PickKeep   PickResult = iota // still choosing
	PickChoose                   // Enter: read Choice
	PickCancel                   // Esc or C-c
)

// Picker is a filterable single-choice list (projects, GitHub repositories). Typed text
// filters; arrows, C-j and C-k move.
type Picker struct {
	Title  string
	items  []string
	filter string
	cursor int
}

// NewPicker builds a picker over items.
func NewPicker(title string, items []string) *Picker { return &Picker{Title: title, items: items} }

func (p *Picker) visible() []string {
	if p.filter == "" {
		return p.items
	}
	f := strings.ToLower(p.filter)
	var out []string
	for _, it := range p.items {
		if strings.Contains(strings.ToLower(it), f) {
			out = append(out, it)
		}
	}
	return out
}

// Key feeds one key (bx key name plus the typed text).
func (p *Picker) Key(key, text string) PickResult {
	switch key {
	case "Esc", "C-c":
		return PickCancel
	case "Enter":
		if len(p.visible()) > 0 {
			return PickChoose
		}
	case "Up", "C-k":
		p.cursor--
	case "Down", "C-j":
		p.cursor++
	case "BSpace":
		if r := []rune(p.filter); len(r) > 0 {
			p.filter, p.cursor = string(r[:len(r)-1]), 0
		}
	default:
		if text != "" {
			p.filter += text
			p.cursor = 0
		}
	}
	p.cursor = max(0, min(p.cursor, len(p.visible())-1))
	return PickKeep
}

// Choice is the highlighted item, or "" when nothing matches.
func (p *Picker) Choice() string {
	v := p.visible()
	if len(v) == 0 {
		return ""
	}
	return v[max(0, min(p.cursor, len(v)-1))]
}

// Render draws the picker in w x h: title, filter line, then the items around the cursor.
func (p *Picker) Render(w, h int) string {
	sel := lipgloss.NewStyle().Foreground(lipgloss.Color("#1a1b26")).Background(lipgloss.Color("#7aa2f7"))
	lines := []string{
		lipgloss.NewStyle().Bold(true).Render(p.Title),
		fmt.Sprintf("> %s▏", p.filter),
	}
	v := p.visible()
	rows := max(h-len(lines)-1, 1)
	start := max(0, min(p.cursor-rows/2, len(v)-rows))
	for i := start; i < len(v) && i < start+rows; i++ {
		line := ansi.Truncate("  "+v[i], w, "…")
		if i == p.cursor {
			line = sel.Width(w).Render(ansi.Truncate("▸ "+v[i], w, "…"))
		}
		lines = append(lines, line)
	}
	if len(v) == 0 {
		lines = append(lines, "  no match")
	}
	lines = append(lines, "", "type to filter · ↑↓ move · ⏎ choose · esc cancel")
	return strings.Join(lines, "\n")
}
