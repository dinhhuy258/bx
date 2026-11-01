package ui

import (
	"os"
	"strings"
	"testing"

	"github.com/dinhhuy258/bx/internal/settings"
)

func sampleSettings() *Settings {
	s := NewSettings("dev")
	s.Load(settings.Sections, map[string]string{"claude.permission_mode": "acceptEdits"})
	return s
}

func TestSettingsStepMarksAndUnmarksChanges(t *testing.T) {
	s := sampleSettings()
	s.Key("l") // model: default → opus
	if got := s.Changes(); len(got) != 1 || got["claude.model"] != "opus" {
		t.Fatalf("changes = %v", got)
	}
	s.Key("h") // back to the saved value: no change
	if len(s.Changes()) != 0 {
		t.Fatalf("changes = %v", s.Changes())
	}
	s.Key("h") // wraps to the last choice
	if s.Changes()["claude.model"] != "fable" {
		t.Fatalf("changes = %v", s.Changes())
	}
	s.Key("j")
	s.Key("j")
	s.Key("j") // stays on the last field
	s.Key("l")
	if s.Changes()["claude.permission_mode"] != "auto" {
		t.Fatalf("changes = %v", s.Changes())
	}
	if s.Key("Enter") != SettingsSave || s.Key("Esc") != SettingsClose {
		t.Fatal("Enter saves, Esc closes")
	}
	s.Load(nil, map[string]string{"claude.model": "fable", "claude.permission_mode": "auto"}) // the save's reply
	if len(s.Changes()) != 0 || !s.Loaded() {
		t.Fatalf("changes after save = %v", s.Changes())
	}
}

func TestSettingsGolden(t *testing.T) {
	for _, w := range []int{120, 40} {
		s := sampleSettings()
		s.Key("l") // model → opus, marked
		got := s.Render(w, 14, true, "")
		golden := "testdata/settings-" + map[int]string{120: "120", 40: "40"}[w] + ".txt"
		if *update {
			if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		want, err := os.ReadFile(golden)
		if err != nil {
			t.Fatalf("%v (run with -update once to create it)", err)
		}
		if got != string(want) {
			t.Fatalf("width %d differs:\n%s\n---- want ----\n%s", w, got, want)
		}
		for i, line := range strings.Split(got, "\n") {
			if lw := runeWidth(line); lw > w {
				t.Errorf("width %d: line %d is %d cells wide: %q", w, i, lw, line)
			}
		}
	}
}

func TestSettingsBeforeLoad(t *testing.T) {
	s := NewSettings("dev")
	if s.Key("l") != SettingsKeep || len(s.Changes()) != 0 {
		t.Fatal("keys before the load do nothing")
	}
	if out := s.Render(120, 10, true, "loading settings…"); !strings.Contains(out, "loading settings…") {
		t.Fatal(out)
	}
}
