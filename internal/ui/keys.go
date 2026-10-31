package ui

import (
	tea "charm.land/bubbletea/v2"
)

// KeyName converts a Bubble Tea key press to the bx key names used in config.toml:
// printable text as typed ("j", "Z", "?"), "Enter", "Esc", "Space", "Up", "Down",
// "Left", "Right", "Tab", "PgUp", "PgDn", "C-x" for control keys and "M-x" for Alt (Option
// sends Alt only when the terminal treats it as Meta).
func KeyName(k tea.KeyPressMsg) string {
	if k.Mod.Contains(tea.ModAlt) {
		k.Mod &^= tea.ModAlt
		k.Text = ""
		return "M-" + KeyName(k)
	}
	switch k.Code {
	case tea.KeyEnter:
		return "Enter"
	case tea.KeyEscape:
		return "Esc"
	case tea.KeySpace:
		return "Space"
	case tea.KeyUp:
		return "Up"
	case tea.KeyDown:
		return "Down"
	case tea.KeyLeft:
		return "Left"
	case tea.KeyRight:
		return "Right"
	case tea.KeyTab:
		return "Tab"
	case tea.KeyBackspace:
		return "BSpace"
	case tea.KeyPgUp:
		return "PgUp"
	case tea.KeyPgDown:
		return "PgDn"
	}
	if k.Mod.Contains(tea.ModCtrl) {
		return "C-" + string(k.Code)
	}
	if k.Text != "" {
		return k.Text
	}
	return k.String()
}
