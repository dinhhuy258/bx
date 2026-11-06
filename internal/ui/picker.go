package ui

import (
	"cmp"
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
	Title string
	// Tabs, when set, are drawn as a strip under the title with Tab highlighted (the owners of
	// the clone picker). The picker does not switch them: its owner reads Tab and S-Tab.
	Tabs []string
	Tab  int
	// Details are dim text drawn after an item, in a column (a repository's description).
	Details map[string]string
	// Note is a dim line under the items ("searching…"); it replaces "no match" when set.
	Note string
	// Help replaces the key help line when set.
	Help    string
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

// Ticked returns the ticked items in list order. Unlike Checked, nothing ticked is nothing.
func (p *Picker) Ticked() []string {
	var out []string
	for _, it := range p.items {
		if p.checked[it] {
			out = append(out, it)
		}
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

// Replace replaces the items and keeps the filter (the results of a search as you type). The
// highlighted item stays highlighted when it is still visible.
func (p *Picker) Replace(items []string) {
	cur := p.Choice()
	p.items, p.cursor = items, 0
	p.Select(cur)
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

// Render draws the picker in w x h: title, tabs, filter line, then the items around the cursor.
func (p *Picker) Render(w, h int) string {
	sel := lipgloss.NewStyle().Foreground(lipgloss.Color("#1a1b26")).Background(lipgloss.Color("#7aa2f7"))
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("#565f89"))
	lines := []string{lipgloss.NewStyle().Bold(true).Render(p.Title)}
	if len(p.Tabs) > 0 {
		lines = append(lines, p.renderTabs(w, sel))
	}
	lines = append(lines, fmt.Sprintf("> %s▏", p.filter))
	v := p.visible()
	rows := max(h-len(lines)-1, 1)
	if p.Note != "" {
		rows = max(rows-1, 1)
	}
	start := max(0, min(p.cursor-rows/2, len(v)-rows))
	col := 0 // the width of the item column when there are details
	if len(p.Details) > 0 {
		for _, it := range v {
			col = max(col, ansi.StringWidth(it))
		}
		col = min(col, w*3/5)
	}
	for i := start; i < len(v) && i < start+rows; i++ {
		item := v[i]
		if p.checked != nil {
			box := "[ ] "
			if p.checked[item] {
				box = "[x] "
			}
			item = box + item
		}
		detail := ""
		if d := p.Details[v[i]]; d != "" {
			item = ansi.Truncate(item, col, "…")
			item += strings.Repeat(" ", col-ansi.StringWidth(item))
			detail = "  " + d
		}
		if i == p.cursor {
			lines = append(lines, sel.Width(w).Render(ansi.Truncate("▸ "+item+detail, w, "…")))
			continue
		}
		line := ansi.Truncate("  "+item, w, "…")
		if detail != "" {
			if room := w - ansi.StringWidth(line); room > 3 {
				line += dim.Render(ansi.Truncate(detail, room, "…"))
			}
		}
		lines = append(lines, line)
	}
	switch {
	case p.Note != "":
		lines = append(lines, dim.Render(ansi.Truncate("  "+p.Note, w, "…")))
	case len(v) == 0:
		lines = append(lines, "  no match")
	}
	help := "type to filter · ↑↓ move · ⏎ choose · esc cancel"
	if p.checked != nil {
		help = "type to filter · ↑↓ move · space tick · ⏎ done · esc cancel"
	}
	lines = append(lines, "", cmp.Or(p.Help, help))
	return strings.Join(lines, "\n")
}

// renderTabs draws the tab strip, scrolled so the selected tab shows.
func (p *Picker) renderTabs(w int, sel lipgloss.Style) string {
	parts := make([]string, len(p.Tabs))
	for i, t := range p.Tabs {
		if i == p.Tab {
			parts[i] = sel.Render(" " + t + " ")
		} else {
			parts[i] = " " + t + " "
		}
	}
	// drop tabs from the left until the selected one fits
	from := 0
	for from < p.Tab && ansi.StringWidth(strings.Join(parts[from:p.Tab+1], " ")) > w-2 {
		from++
	}
	line := strings.Join(parts[from:], " ")
	if from > 0 {
		line = "‹" + line
	}
	return ansi.Truncate(line, w, "›")
}
