package ui

import (
	"fmt"
	"strings"
)

// SwitcherRow is one project of the switcher.
type SwitcherRow struct {
	Name                     string
	Open                     bool // in this client's tabs
	Running, Waiting, Unread int
}

// The actions of the switcher's buttons.
const (
	SwitchAddFolder = iota
	SwitchClone
	SwitchNewEmpty
)

var switchActions = []string{"Existing folder", "Clone from GitHub", "New empty"}

// SwitchResult is what a key did to the switcher.
type SwitchResult int

const (
	SwitchKeep   SwitchResult = iota
	SwitchOpen                // Enter on a project: read Choice
	SwitchAction              // Enter on a button: read Action
	SwitchClose               // ⌫ with no filter on an open project: read Choice
	SwitchCancel              // Esc
)

// Switcher is the project dialog: every project, those open in this client first, filtered by
// what is typed, and the ways to add one as buttons that Tab moves to.
type Switcher struct {
	rows      []SwitcherRow
	filter    string
	cursor    int
	onButtons bool
	action    int
}

// NewSwitcher builds the switcher. The cursor starts on the first project not open here, the
// likely one to open, else on the first open one that is not current.
func NewSwitcher(rows []SwitcherRow, current string) *Switcher {
	var open, other []SwitcherRow
	for _, r := range rows {
		if r.Open {
			open = append(open, r)
		} else {
			other = append(other, r)
		}
	}
	s := &Switcher{rows: append(open, other...)}
	switch {
	case len(other) > 0:
		s.cursor = len(open)
	case len(open) > 1 && open[0].Name == current:
		s.cursor = 1
	}
	return s
}

func (s *Switcher) visible() []SwitcherRow {
	if s.filter == "" {
		return s.rows
	}
	f := strings.ToLower(s.filter)
	var out []SwitcherRow
	for _, r := range s.rows {
		if strings.Contains(strings.ToLower(r.Name), f) {
			out = append(out, r)
		}
	}
	return out
}

// Choice is the project under the cursor, "" when nothing matches.
func (s *Switcher) Choice() string {
	v := s.visible()
	if len(v) == 0 {
		return ""
	}
	return v[max(0, min(s.cursor, len(v)-1))].Name
}

// Action is the button under the cursor: SwitchAddFolder, SwitchClone or SwitchNewEmpty.
func (s *Switcher) Action() int { return s.action }

// Key feeds one key (bx key name plus the typed text).
func (s *Switcher) Key(key, text string) SwitchResult {
	n := len(s.visible())
	switch {
	case key == "Esc" || key == "C-c":
		return SwitchCancel
	case key == "Tab" || key == "S-Tab":
		s.onButtons = !s.onButtons || n == 0 && key == "Tab"
	case key == "Enter" && s.onButtons:
		return SwitchAction
	case key == "Enter" && n > 0:
		return SwitchOpen
	case key == "Enter":
		// no project matches: the action the filter names ("clone"), else the buttons
		for i, a := range switchActions {
			if strings.Contains(strings.ToLower(a), strings.ToLower(strings.TrimSpace(s.filter))) {
				s.action = i
				return SwitchAction
			}
		}
		s.onButtons = true
	case s.onButtons && (key == "Left" || key == "Right"):
		d := 1
		if key == "Left" {
			d = len(switchActions) - 1
		}
		s.action = (s.action + d) % len(switchActions)
	case (key == "Up" || key == "C-k") && s.onButtons: // back to the project it left from
		s.onButtons = false
	case key == "Up" || key == "C-k":
		s.cursor--
	case (key == "Down" || key == "C-j") && !s.onButtons && s.cursor >= n-1: // past the last: the buttons
		s.onButtons = true
	case key == "Down" || key == "C-j":
		s.cursor++
	case key == "BSpace" && s.filter == "":
		for _, r := range s.visible() {
			if r.Name == s.Choice() && r.Open && !s.onButtons {
				return SwitchClose
			}
		}
	case key == "BSpace":
		r := []rune(s.filter)
		s.filter, s.cursor, s.onButtons = string(r[:len(r)-1]), 0, false
	case key == "Space":
		s.filter, s.cursor, s.onButtons = s.filter+" ", 0, false
	case text != "":
		s.filter, s.cursor, s.onButtons = s.filter+text, 0, false
	}
	s.cursor = max(0, min(s.cursor, len(s.visible())-1))
	return SwitchKeep
}

// Paste adds pasted text to the filter.
func (s *Switcher) Paste(text string) { s.filter, s.cursor = s.filter+oneLine(text), 0 }

// Dialog is the switcher as a dialog for a screen h rows high: the filter, the projects around
// the cursor under "OPEN HERE" and "ON THE SERVER", and the buttons.
func (s *Switcher) Dialog(h int, plain bool) Dialog {
	pal := newPalette(plain)
	muted := dlgMuted
	if plain {
		muted = pal.dim
	}
	v := s.visible()
	nameW := 12
	for _, r := range v {
		nameW = max(nameW, len([]rune(r.Name)))
	}
	nameW = min(nameW, 28)
	line := func(i int, r SwitcherRow) string {
		dot, ds := "○", pal.dim
		if r.Open {
			dot, ds = "◉", pal.dim
		}
		switch {
		case r.Waiting > 0:
			ds = pal.amber
		case r.Running > 0:
			ds = pal.blue
		}
		name := truncate(r.Name, nameW)
		name += strings.Repeat(" ", nameW-runeWidth(name))
		var counts []string
		if r.Running > 0 {
			counts = append(counts, pal.blue.Render(fmt.Sprintf("● %d", r.Running)))
		}
		if r.Waiting > 0 {
			counts = append(counts, pal.amber.Render(fmt.Sprintf("◆ %d", r.Waiting)))
		}
		if r.Unread > 0 {
			counts = append(counts, pal.green.Render(fmt.Sprintf("◎ %d", r.Unread)))
		}
		sel := i == s.cursor && !s.onButtons
		if sel {
			name = pal.bold.Render(name)
		}
		text := " " + ds.Render(dot) + " " + name + "  " + strings.Join(counts, "  ")
		if sel {
			text = pal.blue.Render("▌") + text[1:]
		}
		return text
	}
	var rows []string
	mark := 0
	room := max(h-18, 3)
	start := max(0, min(s.cursor-room/2, len(v)-room))
	for i := start; i < len(v) && i < start+room; i++ {
		if i == start || v[i].Open != v[i-1].Open {
			if len(rows) > 0 {
				rows = append(rows, "")
			}
			head := "ON THE SERVER"
			if v[i].Open {
				head = "OPEN HERE"
			}
			rows = append(rows, muted.Bold(true).Render(head))
		}
		rows = append(rows, line(i, v[i]))
		if i == s.cursor && !s.onButtons {
			mark = len(rows)
		}
	}
	if len(v) == 0 {
		rows = append(rows, pal.dim.Render("no project matches · ↓ to add one"))
	}
	hint := "↓ past the last project to add one · ⌫ closes an open one"
	if s.onButtons {
		hint = "←→ choose · ⏎ add · ↑ back to the projects"
	}
	rows = append(rows, "", pal.dim.Render(hint))
	var buttons []Button
	for i, a := range switchActions {
		buttons = append(buttons, Button{Key: "+", Label: a, Default: s.onButtons && i == s.action})
	}
	focus := 0
	if s.onButtons {
		focus = -1
	}
	return Dialog{
		Title: "Projects", Tone: ToneForm, Focus: focus,
		Fields:  []Field{{Label: "Find", Value: s.filter}},
		After:   rows,
		Mark:    mark,
		Hint:    "",
		Buttons: buttons,
	}
}

// SwitcherLine is the key bar while the switcher is open.
func (s *Switcher) Line() string {
	if s.onButtons {
		return "←→ choose · ⏎ add · ↑ or tab back to the projects · esc cancel"
	}
	return "type to filter · ↑↓ move, past the last to the buttons · ⏎ open · ⌫ close an open one · esc cancel"
}
