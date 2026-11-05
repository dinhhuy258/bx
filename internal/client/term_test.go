package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
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

func TestTermEncodesModifiedCursorKeysLikeXterm(t *testing.T) {
	tm := NewTerm(40, 5)
	cases := []struct {
		key  tea.KeyPressMsg
		want string
	}{
		{tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModAlt}, "\x1b[1;3D"},
		{tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModAlt}, "\x1b[1;3C"},
		{tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModShift}, "\x1b[1;2D"},
		{tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModCtrl}, "\x1b[1;5C"},
		{tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModShift | tea.ModAlt}, "\x1b[1;4A"},
		{tea.KeyPressMsg{Code: tea.KeyHome, Mod: tea.ModShift}, "\x1b[1;2H"},
		{tea.KeyPressMsg{Code: tea.KeyEnd, Mod: tea.ModShift}, "\x1b[1;2F"},
		{tea.KeyPressMsg{Code: tea.KeyDelete, Mod: tea.ModAlt}, "\x1b[3;3~"},
		{tea.KeyPressMsg{Code: tea.KeyPgUp, Mod: tea.ModCtrl}, "\x1b[5;5~"},
		{tea.KeyPressMsg{Code: tea.KeyLeft}, "\x1b[D"},                     // plain keys stay the emulator's
		{tea.KeyPressMsg{Code: 'b', Mod: tea.ModAlt, Text: "b"}, "\x1bb"}, // Alt letters too
	}
	for _, c := range cases {
		tm.SendKey(c.key)
		select {
		case b := <-tm.Input():
			if string(b) != c.want {
				t.Errorf("%s = %q, want %q", c.key, b, c.want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%s sent nothing", c.key)
		}
	}
}

func TestTermTakesOSC52Copies(t *testing.T) {
	term := NewTerm(20, 5)
	defer term.Close()
	term.Feed([]byte("a\x1b]52;c;aGVsbG8gYng=\x07b\x1b]52;c;?\x07\x1b]52;;d29ybGQ=\x1b\\"))
	if got := term.TakeCopied(); fmt.Sprint(got) != "[hello bx world]" {
		t.Fatalf("copied %q", got)
	}
	if got := term.TakeCopied(); got != nil {
		t.Fatalf("taken twice: %q", got)
	}
	if !strings.Contains(term.View(), "ab") {
		t.Fatalf("OSC 52 must not print: %q", term.View())
	}
}

// A refused upgrade carries the status code, so the model can tell a gone shell from a drop.
func TestConnectRefusedCarriesTheCode(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not running", http.StatusGone)
	}))
	defer ts.Close()
	_, err := Connect(context.Background(), "ws"+strings.TrimPrefix(ts.URL, "http"), NewTerm(80, 24), func(tea.Msg) {})
	var se *StatusError
	if !errors.As(err, &se) || se.Code != http.StatusGone || !strings.Contains(err.Error(), "410") {
		t.Fatalf("%#v", err)
	}
}
