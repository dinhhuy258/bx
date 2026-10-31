package client

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func TestTermRendersBytesAndEncodesKeys(t *testing.T) {
	tm := NewTerm(40, 5)
	tm.Feed([]byte("hello \x1b[31mred\x1b[0m\r\nline2"))
	view := tm.View()
	if !strings.Contains(view, "hello") || !strings.Contains(view, "line2") || strings.Count(view, "\n") != 4 {
		t.Fatalf("view = %q", view)
	}
	if x, y := tm.Cursor(); x != 5 || y != 1 {
		t.Fatalf("cursor = %d,%d", x, y)
	}
	tm.SendKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	tm.SendKey(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	tm.SendKey(tea.KeyPressMsg{Code: tea.KeyUp})
	tm.SendKey(tea.KeyPressMsg{Code: 'a', Text: "a"})
	var got []byte
	deadline := time.After(2 * time.Second)
	for len(got) < 6 {
		select {
		case b := <-tm.Input():
			got = append(got, b...)
		case <-deadline:
			t.Fatalf("input so far %q", got)
		}
	}
	if string(got) != "\r\x03\x1b[Aa" {
		t.Fatalf("encoded = %q", got)
	}
	tm.Resize(20, 3)
	if tm.Cols() != 20 || tm.Rows() != 3 || strings.Count(tm.View(), "\n") != 2 {
		t.Fatal(tm.View())
	}
}
