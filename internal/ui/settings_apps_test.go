package ui

import (
	"strings"
	"testing"

	"github.com/dinhhuy258/bx/internal/runcfg"
	"github.com/dinhhuy258/bx/internal/settings"
)

func appsSettings(t *testing.T) *Settings {
	t.Helper()
	s := NewSettings("server dev")
	s.Load([]settings.Section{{ID: "claude", Title: "Claude"}}, nil)
	for !s.AppsShown() {
		s.Key("Tab")
	}
	s.SetAppsProjects([]AppsProject{{Project: "home-network", Runs: []string{"web", "api"}}, {Project: "unem"}})
	return s
}

func TestAppsProjectsAndPage(t *testing.T) {
	s := appsSettings(t)
	out := strings.Join(s.appsLines(80, 30, false, newPalette(true)), "\n")
	if !strings.Contains(out, "home-network") || !strings.Contains(out, "web, api") || !strings.Contains(out, "no runs") {
		t.Fatal(out)
	}
	if r := s.Key("Enter"); r != SettingsAppsOpen || s.AppsProject() != "home-network" {
		t.Fatalf("enter: %v %q", r, s.AppsProject())
	}
	s.SetAppsPage("home-network", AppsConfig{Runs: []runcfg.Run{{Name: "web", Cmd: "npm run dev"}, {Name: "api", Cmd: "php artisan serve", Image: "php:8.3"}}, HasComments: true})
	out = strings.Join(s.appsLines(80, 30, false, newPalette(true)), "\n")
	for _, w := range []string{"Apps › home-network", "web", "npm run dev", "image php:8.3", "+ add run", "hidden ports", "(defaults)", "comments"} {
		if !strings.Contains(out, w) {
			t.Errorf("missing %q:\n%s", w, out)
		}
	}
	s.Key("j") // api
	if r := s.Key("BSpace"); r != SettingsAppsRemove {
		t.Errorf("backspace: %v", r)
	}
	if i, ok := s.AppsRun(); !ok || i != 1 {
		t.Errorf("run under cursor: %d %v", i, ok)
	}
	s.RemoveRun(1)
	if got := s.AppsSave(); len(got.Runs) != 1 || got.Runs[0].Name != "web" {
		t.Errorf("save after remove: %+v", got)
	}
	if r := s.Key("e"); r != SettingsAppsEditText {
		t.Errorf("e: %v", r)
	}
	s.Key("j") // + add run was under the cursor after the remove: hidden ports
	if r := s.Key("Enter"); r != SettingsAppsPorts {
		t.Errorf("enter on ports: %v", r)
	}
	if which, _ := s.AppsPortsRow(); which != "hide" {
		t.Errorf("ports row %q", which)
	}
	if r := s.Key("Esc"); r != SettingsKeep || s.apps.page != "" {
		t.Errorf("esc goes back: %v %q", r, s.apps.page)
	}
}

func TestAppsBrokenFile(t *testing.T) {
	s := appsSettings(t)
	s.Key("Enter")
	s.SetAppsPage("home-network", AppsConfig{Error: "line 3: expected value"})
	out := strings.Join(s.appsLines(80, 30, false, newPalette(true)), "\n")
	if !strings.Contains(out, "line 3: expected value") || strings.Contains(out, "+ add run") {
		t.Fatal(out)
	}
	if r := s.Key("Enter"); r != SettingsAppsEditText {
		t.Errorf("enter on a broken file edits the text: %v", r)
	}
}

func TestRunForm(t *testing.T) {
	s := appsSettings(t)
	s.Key("Enter")
	s.SetAppsPage("home-network", AppsConfig{})
	if r := s.Key("Enter"); r != SettingsAppsForm { // + add run
		t.Fatalf("add: %v", r)
	}
	if r := s.Key("Enter"); r != SettingsAppsText {
		t.Fatalf("enter on Name: %v", r)
	}
	f, label, _, _ := s.AppsFormText()
	if label != "Name" {
		t.Errorf("label %q", label)
	}
	s.SetAppsFormText(f, "web")
	s.Key("j") // Runs on
	s.Key("l") // image
	out := strings.Join(s.appsLines(80, 40, false, newPalette(true)), "\n")
	if !strings.Contains(out, "◂ image ▸") || !strings.Contains(out, "Image") || strings.Contains(out, "Dockerfile") {
		t.Fatalf("image rows:\n%s", out)
	}
	s.Key("l") // build
	out = strings.Join(s.appsLines(80, 40, false, newPalette(true)), "\n")
	if !strings.Contains(out, "Dockerfile") || !strings.Contains(out, "Cache") {
		t.Fatalf("build rows:\n%s", out)
	}
	s.Key("j") // Dockerfile
	if r := s.Key("Enter"); r != SettingsAppsBuild {
		t.Errorf("enter on Dockerfile: %v", r)
	}
	s.Key("h")
	s.Key("k")
	s.Key("h") // back to host
	if r := s.Key("C-s"); r != SettingsAppsSave {
		t.Fatalf("save: %v", r)
	}
	got := s.AppsSave()
	if len(got.Runs) != 1 || got.Runs[0].Name != "web" || got.Runs[0].Image != "" || got.Runs[0].Build != "" {
		t.Errorf("saved %+v", got.Runs)
	}
	s.SetRunFormErrors([]runcfg.FieldError{{Run: 0, Field: "cmd", Message: "required"}})
	out = strings.Join(s.appsLines(80, 40, false, newPalette(true)), "\n")
	if !strings.Contains(out, "Command: required") {
		t.Errorf("errors:\n%s", out)
	}
	if r := s.Key("Esc"); r != SettingsAppsDiscard {
		t.Errorf("esc with edits: %v", r)
	}
}
