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
	for range 20 {
		s.Key("j")
	}
	if f, ok := s.field(); !ok || f.Key != "claude.address" {
		t.Fatalf("j stops on the last field, not %v", f)
	}
	for range 6 {
		s.Key("k")
	}
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

func TestSettingsTextField(t *testing.T) {
	s := NewSettings("dev")
	s.Load(settings.Sections, map[string]string{"notifications.ntfy_url": "https://ntfy.sh/bx"})
	if s.Key("Enter") != SettingsSave {
		t.Fatal("Enter on a choice saves")
	}
	if _, ok := s.TextField(); ok {
		t.Fatal("a choice is not a text field")
	}
	s.Key("Tab") // git
	s.Key("Tab") // runs
	s.Key("Tab") // mcp
	s.Key("Tab") // notifications
	s.Key("j")   // past Enabled
	s.Key("j")   // past Pull requests
	f, ok := s.TextField()
	if !ok || f.Key != "notifications.ntfy_url" {
		t.Fatalf("text field = %v %v", f, ok)
	}
	if s.Key("Enter") != SettingsEdit {
		t.Fatal("Enter on a text field edits it")
	}
	s.Key("l")
	s.Key("h")
	if len(s.Changes()) != 0 {
		t.Fatalf("h/l changed a text field: %v", s.Changes())
	}
	if s.Value("notifications.ntfy_url") != "https://ntfy.sh/bx" || s.Value("notifications.open_url") != "" {
		t.Fatal("Value reads the saved values")
	}
	s.Key("Tab") // back to claude
	s.Key("l")
	if s.Value("claude.model") != "opus" {
		t.Fatal("Value reads the edits first")
	}
}

func TestSettingsNotificationsEnabled(t *testing.T) {
	s := NewSettings("dev")
	s.Load(settings.Sections, nil)
	s.Key("Tab") // git
	s.Key("Tab") // runs
	s.Key("Tab") // mcp
	s.Key("Tab") // notifications
	if f, ok := s.field(); !ok || f.Key != "notifications.enabled" {
		t.Fatalf("first field = %v %v", f, ok)
	}
	if out := s.Render(120, 14, true, ""); !strings.Contains(out, "Enabled        ◂ on  ▸") {
		t.Fatalf("no value is on:\n%s", out)
	}
	s.Key("l")
	if got := s.Changes(); len(got) != 1 || got["notifications.enabled"] != "off" {
		t.Fatalf("changes = %v", got)
	}
	s.Key("l") // back to on, the default: no change
	if len(s.Changes()) != 0 {
		t.Fatalf("changes = %v", s.Changes())
	}
	s.Key("h")
	if s.Changes()["notifications.enabled"] != "off" || s.Key("Enter") != SettingsSave {
		t.Fatalf("h: %v", s.Changes())
	}
	s.Load(nil, map[string]string{"notifications.enabled": "off"}) // the save's reply
	if len(s.Changes()) != 0 || !strings.Contains(s.Render(120, 14, true, ""), "Enabled        ◂ off ▸") {
		t.Fatalf("after save: %v", s.Changes())
	}
	s.Key("l")
	if s.Changes()["notifications.enabled"] != "on" {
		t.Fatalf("off → on: %v", s.Changes())
	}
}

func TestSettingsNotificationsGolden(t *testing.T) {
	const w = 120
	s := NewSettings("dev")
	s.Load(settings.Sections, map[string]string{"notifications.ntfy_url": "https://ntfy.sh/bx-topic"})
	s.Key("Tab") // git
	s.Key("Tab") // runs
	s.Key("Tab") // mcp
	s.Key("Tab") // notifications
	got := s.Render(w, 14, true, "")
	golden := "testdata/settings-notifications-120.txt"
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
		t.Fatalf("differs:\n%s\n---- want ----\n%s", got, want)
	}
	for i, line := range strings.Split(got, "\n") {
		if lw := runeWidth(line); lw > w {
			t.Errorf("line %d is %d cells wide: %q", i, lw, line)
		}
	}
	if !strings.Contains(got, "not set") || !strings.Contains(got, "https://ntfy.sh/bx-topic") {
		t.Fatal(got)
	}
}

func TestSettingsLineField(t *testing.T) {
	s := NewSettings("dev")
	s.Load(settings.Sections, map[string]string{"claude.commit": "custom", "claude.commit_text": "Co-Authored-By: Huy"})
	for range 3 { // model, effort, permission mode
		s.Key("j")
	}
	if f, ok := s.field(); !ok || f.Key != "claude.commit" || s.Key("Enter") != SettingsSave {
		t.Fatalf("the commit trailer is a choice: %v", f)
	}
	s.Key("j")
	f, ok := s.TextField()
	if !ok || f.Key != "claude.commit_text" || s.Key("Enter") != SettingsEdit {
		t.Fatalf("the commit text is typed: %v %v", f, ok)
	}
	v := s.Render(120, 40, true, "")
	for _, want := range []string{"Commit trailer", "custom", "Co-Authored-By: Huy", "PR text", "not set"} {
		if !strings.Contains(v, want) {
			t.Errorf("no %q in\n%s", want, v)
		}
	}
}
