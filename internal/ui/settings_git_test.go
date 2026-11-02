package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/dinhhuy258/bx/internal/settings"
)

// gitSettings is the sample screen on its git section, with two overrides.
func gitSettings() *Settings {
	s := sampleSettings()
	s.SetGitOverrides([]GitOverride{{Project: "infra", Base: "release/1.4"}, {Project: "api-server", Base: "develop"}})
	s.Key("Tab")
	return s
}

func TestSettingsGitSectionIsTheClients(t *testing.T) {
	s := sampleSettings()
	var ids []string
	for _, sec := range s.sections {
		ids = append(ids, sec.ID+" "+sec.Title)
	}
	if got := strings.Join(ids, ", "); got != "claude Claude, git Git" {
		t.Fatalf("sections = %s", got)
	}
	if len(settings.Sections) != 1 || settings.Sections[0].ID != "claude" {
		t.Fatalf("the server's registry must stay claude-only: %v", settings.Sections)
	}
	s.Load(settings.Sections, nil) // a second load adds it once
	if len(s.sections) != 2 {
		t.Fatalf("%d sections after a reload", len(s.sections))
	}
	if s.GitShown() {
		t.Fatal("the Claude section opens first")
	}
	if p, add := s.GitChoice(); p != "" || add {
		t.Fatalf("GitChoice outside the section = %q %v", p, add)
	}
}

func TestSettingsGitRowsAndKeys(t *testing.T) {
	s := gitSettings()
	if !s.GitShown() {
		t.Fatal("Tab opens the git section")
	}
	if p, add := s.GitChoice(); p != "api-server" || add {
		t.Fatalf("first row = %q %v, want api-server (sorted)", p, add)
	}
	if s.Key("Enter") != SettingsGitChoose {
		t.Fatal("Enter on an override chooses, it does not save")
	}
	if s.Key("BSpace") != SettingsGitRemove {
		t.Fatal("Backspace on an override removes it")
	}
	for _, k := range []string{"l", "h", "Space", "s", "r"} {
		if got := s.Key(k); got != SettingsKeep {
			t.Fatalf("%s in the git section = %v", k, got)
		}
	}
	if len(s.Changes()) != 0 {
		t.Fatalf("h/l must not edit the hidden fields: %v", s.Changes())
	}
	s.Key("j")
	if p, _ := s.GitChoice(); p != "infra" {
		t.Fatalf("j → %q", p)
	}
	s.Key("Down")
	s.Key("j") // stays on the last row
	if p, add := s.GitChoice(); p != "" || !add {
		t.Fatalf("last row = %q %v, want the add row", p, add)
	}
	if s.Key("BSpace") != SettingsKeep {
		t.Fatal("Backspace on the add row does nothing")
	}
	if s.Key("Enter") != SettingsGitChoose {
		t.Fatal("Enter on the add row chooses")
	}
	s.Key("k")
	s.Key("Up")
	s.Key("k")
	if p, _ := s.GitChoice(); p != "api-server" {
		t.Fatalf("k → %q", p)
	}
	if s.Key("Esc") != SettingsClose {
		t.Fatal("Esc closes")
	}
	s.Key("Tab")
	if s.GitShown() || s.fld != 0 {
		t.Fatal("Tab goes back to Claude, on its first field")
	}
}

func TestSettingsGitCursorFollowsItsRow(t *testing.T) {
	s := gitSettings()
	s.Key("j") // infra
	s.SetGitOverrides([]GitOverride{{Project: "web", Base: "main"}, {Project: "infra", Base: "release/1.4"}, {Project: "api-server", Base: "develop"}})
	if p, _ := s.GitChoice(); p != "infra" {
		t.Fatalf("cursor on %q after an update", p)
	}
	s.Key("j")
	s.Key("j") // the add row
	s.SetGitOverrides([]GitOverride{{Project: "a", Base: "main"}, {Project: "web", Base: "main"}, {Project: "infra", Base: "release/1.4"}, {Project: "api-server", Base: "develop"}})
	if _, add := s.GitChoice(); !add {
		t.Fatal("the cursor stays on the add row")
	}
	s.Key("k") // web
	s.SetGitOverrides([]GitOverride{{Project: "a", Base: "main"}})
	if _, add := s.GitChoice(); !add {
		t.Fatal("its row is gone: the cursor is kept in range")
	}
	s.SyncFailed("offline") // a sync reply does not move the git cursor
	s.Load(nil, nil)
	if _, add := s.GitChoice(); !add {
		t.Fatal("a sync reply or a load moved the git cursor")
	}
}

func TestSettingsGitEmpty(t *testing.T) {
	s := sampleSettings()
	s.Key("Tab")
	got := s.Render(120, 14, true, "")
	for _, want := range []string{"git · base branch overrides", gitEmpty, "› + add override"} {
		if !strings.Contains(got, want) {
			t.Fatalf("no %q:\n%s", want, got)
		}
	}
	if s.Key("BSpace") != SettingsKeep || s.Key("Enter") != SettingsGitChoose {
		t.Fatal("with no override only the add row is there")
	}
}

func TestSettingsGitGolden(t *testing.T) {
	s := gitSettings()
	s.Key("j") // infra
	golden(t, "settings-git-120", s.Render(120, 14, true, ""), 120)
	e := sampleSettings()
	e.Key("Tab")
	golden(t, "settings-git-empty-40", e.Render(40, 14, true, ""), 40)
}

// Thirty overrides scroll with the cursor; the header stays.
func TestSettingsGitWindow(t *testing.T) {
	s := sampleSettings()
	var list []GitOverride
	for i := range 30 {
		list = append(list, GitOverride{Project: fmt.Sprintf("p%02d", i), Base: "develop"})
	}
	s.SetGitOverrides(list)
	s.Key("Tab")
	for range 20 {
		s.Key("j")
	}
	for _, w := range []int{120, 40} {
		got := s.Render(w, 16, true, "")
		if lines := strings.Split(got, "\n"); len(lines) != 16 {
			t.Fatalf("width %d: %d lines", w, len(lines))
		}
		for _, want := range []string{"git · base branch overrides", "› p20", "more"} {
			if !strings.Contains(got, want) {
				t.Fatalf("width %d: no %q:\n%s", w, want, got)
			}
		}
	}
}

func TestSettingsGitBar(t *testing.T) {
	if got := SettingsGitBar(false); got != "j/k move · ⏎ choose · ⌫ remove · esc back" {
		t.Fatal(got)
	}
	if got := SettingsGitBar(true); got != "⏎ choose · ⌫ remove · esc" {
		t.Fatal(got)
	}
}
