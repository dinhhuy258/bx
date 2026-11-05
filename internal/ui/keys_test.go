package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestKeyName(t *testing.T) {
	cases := []struct {
		msg  tea.KeyPressMsg
		want string
	}{
		{tea.KeyPressMsg{Code: 'j', Text: "j"}, "j"},
		{tea.KeyPressMsg{Code: 'Z', Text: "Z", Mod: tea.ModShift}, "Z"},
		{tea.KeyPressMsg{Code: '?', Text: "?"}, "?"},
		{tea.KeyPressMsg{Code: tea.KeyEnter}, "Enter"},
		{tea.KeyPressMsg{Code: tea.KeyDown}, "Down"},
		{tea.KeyPressMsg{Code: tea.KeyUp}, "Up"},
		{tea.KeyPressMsg{Code: tea.KeyEscape}, "Esc"},
		{tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}, "Space"},
		{tea.KeyPressMsg{Code: 'h', Mod: tea.ModCtrl}, "C-h"},
		{tea.KeyPressMsg{Code: tea.KeyPgUp}, "PgUp"},
		{tea.KeyPressMsg{Code: tea.KeyPgDown}, "PgDn"},
		{tea.KeyPressMsg{Code: tea.KeyDelete}, "Delete"},
		{tea.KeyPressMsg{Code: tea.KeyHome}, "Home"},
		{tea.KeyPressMsg{Code: tea.KeyEnd}, "End"},
		{tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModAlt}, "M-Left"},
		{tea.KeyPressMsg{Code: tea.KeyBackspace, Mod: tea.ModAlt}, "M-BSpace"},
		{tea.KeyPressMsg{Code: 'u', Mod: tea.ModAlt}, "M-u"},
		{tea.KeyPressMsg{Code: 'd', Mod: tea.ModAlt | tea.ModCtrl}, "M-C-d"},
	}
	for _, c := range cases {
		if got := KeyName(c.msg); got != c.want {
			t.Errorf("%+v -> %q want %q", c.msg, got, c.want)
		}
	}
}
