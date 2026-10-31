package ui

import (
	tea "charm.land/bubbletea/v2"
)

// KeyName converts a Bubble Tea key press to the bx key names used in config.toml:
// printable text as typed ("j", "Z", "?"), "Enter", "Esc", "Space", "Up", "Down",
// "Left", "Right", "Tab", and "C-x" for control keys.
func KeyName(k tea.KeyPressMsg) string {
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
	}
	if k.Mod.Contains(tea.ModCtrl) {
		return "C-" + string(k.Code)
	}
	if k.Text != "" {
		return k.Text
	}
	return k.String()
}
