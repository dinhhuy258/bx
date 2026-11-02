package ui

import (
	"fmt"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// PickResult is what a key did to the picker.
type PickResult int

const (
	PickKeep    PickResult = iota // still choosing
	PickChoose                    // Enter: read Choice
	PickCancel                    // Esc or C-c
	PickNoMatch                   // Enter with nothing visible: read Filter (the pull request search)
)

// Picker is a filterable single-choice list (projects, GitHub repositories). Typed text
// filters; arrows, C-j and C-k move. As a checklist Space ticks the highlighted item.
type Picker struct {
	Title   string
	items   []string
	filter  string
	cursor  int
	checked map[string]bool // nil unless a checklist
}

// NewPicker builds a picker over items.
func NewPicker(title string, items []string) *Picker { return &Picker{Title: title, items: items} }

// NewChecklist builds a picker that chooses several items (workspace repositories).
func NewChecklist(title string, items []string) *Picker {
	return &Picker{Title: title, items: items, checked: map[string]bool{}}
}

// Checked are the ticked items of a checklist in list order. With none ticked, Enter takes the
// highlighted one.
func (p *Picker) Checked() []string {
	var out []string
	for _, it := range p.items {
		if p.checked[it] {
			out = append(out, it)
		}
	}
	if len(out) == 0 && p.Choice() != "" {
		out = []string{p.Choice()}
	}
	return out
}

// Tick ticks an item of a checklist. It does nothing on a single-choice picker.
func (p *Picker) Tick(item string) {
	if p.checked != nil {
		p.checked[item] = true
	}
}

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
		return PickNoMatch
	case "Up", "C-k":
		p.cursor--
	case "Down", "C-j":
		p.cursor++
	case "Space":
		if p.checked == nil {
			p.filter += " "
			p.cursor = 0
		} else if c := p.Choice(); c != "" {
			p.checked[c] = !p.checked[c]
		}
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

// Select highlights item (the last used branch type) when it is visible.
func (p *Picker) Select(item string) {
	if i := slices.Index(p.visible(), item); i >= 0 {
		p.cursor = i
	}
}

// Filter is the typed text.
func (p *Picker) Filter() string { return p.filter }

// Cursor is the highlighted row of the visible items, 0 the first.
func (p *Picker) Cursor() int { return p.cursor }

// SetItems replaces the items (the results of a search), clears the filter and highlights the
// first one.
func (p *Picker) SetItems(items []string) {
	p.items, p.filter, p.cursor = items, "", 0
}

// Paste adds pasted text to the filter.
func (p *Picker) Paste(text string) {
	p.filter += oneLine(text)
	p.cursor = 0
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
		item := v[i]
		if p.checked != nil {
			box := "[ ] "
			if p.checked[item] {
				box = "[x] "
			}
			item = box + item
		}
		line := ansi.Truncate("  "+item, w, "…")
		if i == p.cursor {
			line = sel.Width(w).Render(ansi.Truncate("▸ "+item, w, "…"))
		}
		lines = append(lines, line)
	}
	if len(v) == 0 {
		lines = append(lines, "  no match")
	}
	help := "type to filter · ↑↓ move · ⏎ choose · esc cancel"
	if p.checked != nil {
		help = "type to filter · ↑↓ move · space tick · ⏎ done · esc cancel"
	}
	lines = append(lines, "", help)
	return strings.Join(lines, "\n")
}
