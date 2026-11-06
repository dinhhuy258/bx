package ui

import (
	"strings"
	"testing"

	"github.com/dinhhuy258/bx/internal/runcfg"
	"github.com/dinhhuy258/bx/internal/settings"
)

func runsSettings(t *testing.T) *Settings {
	t.Helper()
	s := NewSettings("server dev")
	s.Load([]settings.Section{{ID: "claude", Title: "Claude"}}, nil)
	openSection(s, runsSection)
	s.SetRunsProjects([]RunsProject{{Project: "home-network", Runs: []string{"web", "api"}}, {Project: "unem"}})
	return s
}

func TestRunsProjectsAndPage(t *testing.T) {
	s := runsSettings(t)
	out := strings.Join(s.runsLines(80, 30, false, newPalette(true)), "\n")
	if !strings.Contains(out, "home-network") || !strings.Contains(out, "web, api") || !strings.Contains(out, "no runs") {
		t.Fatal(out)
	}
	if r := s.Key("Enter"); r != SettingsRunsOpen || s.RunsProject() != "home-network" {
		t.Fatalf("enter: %v %q", r, s.RunsProject())
	}
	s.SetRunsPage("home-network", RunsConfig{Runs: []runcfg.Run{{Name: "web", Cmd: "npm run dev"}, {Name: "api", Cmd: "php artisan serve", Image: "php:8.3"}}, HasComments: true})
	out = strings.Join(s.runsLines(80, 30, false, newPalette(true)), "\n")
	for _, w := range []string{"Runs › home-network", "web", "npm run dev", "image php:8.3", "+ add run", "hidden ports", "(defaults)", "comments"} {
		if !strings.Contains(out, w) {
			t.Errorf("missing %q:\n%s", w, out)
		}
	}
	s.Key("j") // api
	if r := s.Key("BSpace"); r != SettingsRunsRemove {
		t.Errorf("backspace: %v", r)
	}
	if i, ok := s.PageRun(); !ok || i != 1 {
		t.Errorf("run under cursor: %d %v", i, ok)
	}
	s.RemoveRun(1)
	if got := s.RunsSave(); len(got.Runs) != 1 || got.Runs[0].Name != "web" {
		t.Errorf("save after remove: %+v", got)
	}
	if r := s.Key("e"); r != SettingsRunsEditText {
		t.Errorf("e: %v", r)
	}
	s.Key("j") // + add run was under the cursor after the remove: hidden ports
	if r := s.Key("Enter"); r != SettingsRunsPorts {
		t.Errorf("enter on ports: %v", r)
	}
	if which, _ := s.RunsPortsRow(); which != "hide" {
		t.Errorf("ports row %q", which)
	}
	if r := s.Key("Esc"); r != SettingsKeep || s.runs.page != "" {
		t.Errorf("esc goes back: %v %q", r, s.runs.page)
	}
}

func TestRunsBrokenFile(t *testing.T) {
	s := runsSettings(t)
	s.Key("Enter")
	s.SetRunsPage("home-network", RunsConfig{Error: "line 3: expected value"})
	out := strings.Join(s.runsLines(80, 30, false, newPalette(true)), "\n")
	if !strings.Contains(out, "line 3: expected value") || strings.Contains(out, "+ add run") {
		t.Fatal(out)
	}
	if r := s.Key("Enter"); r != SettingsRunsEditText {
		t.Errorf("enter on a broken file edits the text: %v", r)
	}
}

func TestRunForm(t *testing.T) {
	s := runsSettings(t)
	s.Key("Enter")
	s.SetRunsPage("home-network", RunsConfig{})
	if r := s.Key("Enter"); r != SettingsRunsForm { // + add run
		t.Fatalf("add: %v", r)
	}
	if r := s.Key("Enter"); r != SettingsRunsText {
		t.Fatalf("enter on Name: %v", r)
	}
	f, label, _, _ := s.RunsFormText()
	if label != "Name" {
		t.Errorf("label %q", label)
	}
	s.SetRunsFormText(f, "web")
	s.Key("j") // Runs on
	s.Key("l") // image
	out := strings.Join(s.runsLines(80, 40, false, newPalette(true)), "\n")
	if !strings.Contains(out, "◂ image ▸") || !strings.Contains(out, "Image") || strings.Contains(out, "Dockerfile") {
		t.Fatalf("image rows:\n%s", out)
	}
	s.Key("l") // build
	out = strings.Join(s.runsLines(80, 40, false, newPalette(true)), "\n")
	if !strings.Contains(out, "Dockerfile") || !strings.Contains(out, "Cache") {
		t.Fatalf("build rows:\n%s", out)
	}
	s.Key("j") // Dockerfile
	if r := s.Key("Enter"); r != SettingsRunsBuild {
		t.Errorf("enter on Dockerfile: %v", r)
	}
	s.Key("h")
	s.Key("k")
	s.Key("h") // back to host
	if r := s.Key("C-s"); r != SettingsRunsSave {
		t.Fatalf("save: %v", r)
	}
	got := s.RunsSave()
	if len(got.Runs) != 1 || got.Runs[0].Name != "web" || got.Runs[0].Image != "" || got.Runs[0].Build != "" {
		t.Errorf("saved %+v", got.Runs)
	}
	s.SetRunFormErrors([]runcfg.FieldError{{Run: 0, Field: "cmd", Message: "required"}})
	out = strings.Join(s.runsLines(80, 40, false, newPalette(true)), "\n")
	if !strings.Contains(out, "Command: required") {
		t.Errorf("errors:\n%s", out)
	}
	if r := s.Key("Esc"); r != SettingsRunsDiscard {
		t.Errorf("esc with edits: %v", r)
	}
}

func TestRunFormHints(t *testing.T) {
	s := runsSettings(t)
	s.Key("Enter")
	s.SetRunsPage("unem", RunsConfig{
		Runs: []runcfg.Run{
			{Name: "api", Cmd: "./vendor/bin/sail up", Dir: "unem-be"},
			{Name: "queue", Cmd: "./vendor/bin/sail artisan queue:work", Dir: "unem-be/"},
			{Name: "web", Cmd: "npm run dev", Dir: "unem-fe"},
		},
		Repos:   []string{"unem-be", "unem-fe"},
		Compose: []string{"unem-be"},
	})
	lines := func() string { return strings.Join(s.runsLines(100, 40, false, newPalette(true)), "\n") }

	s.openRunForm(s.runs.cfg.Runs[0], 0)
	out := lines()
	for _, want := range []string{"queue also runs in unem-be. Set the port api serves", "unem-be has a compose file. Set a stop command"} {
		if !strings.Contains(out, want) {
			t.Errorf("api: missing %q in\n%s", want, out)
		}
	}
	// a port and a stop settle both
	s.runs.form.run.Port, s.runs.form.run.Stop = runcfg.Ports{8000}, "./vendor/bin/sail down"
	if out := lines(); strings.Contains(out, "also runs in") || strings.Contains(out, "compose file") {
		t.Errorf("api settled: hints in\n%s", out)
	}
	// web has its folder to itself, and no compose file there
	s.openRunForm(s.runs.cfg.Runs[2], 2)
	if out := lines(); strings.Contains(out, "also runs in") || strings.Contains(out, "compose file") {
		t.Errorf("web: hints in\n%s", out)
	}
}
