package client

import (
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/dinhhuy258/bx/internal/api"
	"github.com/dinhhuy258/bx/internal/runcfg"
)

func TestSettingsRunsEditTextAndRemove(t *testing.T) {
	h := start(t, 120, 30, "running")
	h.be.runCfgs = map[string]api.RunConfig{"home-network": {Runs: []runcfg.Run{{Name: "web", Cmd: "npm run dev"}, {Name: "api", Cmd: "php artisan serve"}}}}
	h.be.runTexts = map[string]string{"home-network": "# mine\n[[run]]\nname = \"web\"\ncmd = \"npm run dev\"\n"}
	drain(h.m, h.m.openRunsSettings("home-network"))
	if v := view(h); !strings.Contains(v, "Runs › home-network") || !strings.Contains(v, "npm run dev") {
		t.Fatal(v)
	}
	// e: the text in $EDITOR; the edit is saved as text
	h.m.edit = func(path string, _ int, done func(error) tea.Msg) tea.Cmd {
		data, _ := os.ReadFile(path)
		os.WriteFile(path, append(data, "# more\n"...), 0o600)
		return func() tea.Msg { return done(nil) }
	}
	h.press("e")
	if len(h.be.runTextSaves) != 1 || !strings.HasSuffix(h.be.runTextSaves[0], "# more\n") {
		t.Fatalf("text saves %q", h.be.runTextSaves)
	}
	// ⌫ on api, y: saved without it
	h.press("j", "BSpace")
	if v := view(h); !strings.Contains(v, "Remove run api?") {
		t.Fatal(v)
	}
	h.press("y")
	if n := len(h.be.runSaves); n != 1 || len(h.be.runSaves[0].Runs) != 1 {
		t.Fatalf("saves %+v", h.be.runSaves)
	}
}

func TestRunFormSaves(t *testing.T) {
	h := start(t, 120, 30, "running")
	h.be.runCfgs = map[string]api.RunConfig{"home-network": {}}
	drain(h.m, h.m.openRunsSettings("home-network"))
	h.press("Enter")                         // + add run
	h.press("Enter", "w", "e", "b", "Enter") // Name
	h.press("j", "j", "Enter")               // Command
	for _, r := range "npm run dev" {
		h.press(string(r))
	}
	h.press("Enter", "C-s")
	if n := len(h.be.runSaves); n != 1 || h.be.runSaves[0].Runs[0].Cmd != "npm run dev" {
		t.Fatalf("saves %+v", h.be.runSaves)
	}
	if !strings.Contains(h.m.flash, "run config saved") {
		t.Errorf("flash %q", h.m.flash)
	}
}

func TestRunFormCommandEditsInEditor(t *testing.T) {
	h := start(t, 120, 30, "running")
	h.be.runCfgs = map[string]api.RunConfig{"home-network": {Runs: []runcfg.Run{{Name: "web", Cmd: "npm run dev"}}}}
	drain(h.m, h.m.openRunsSettings("home-network"))
	h.press("Enter")           // web
	h.press("j", "j", "Enter") // Command
	if h.m.prompt.Input != "npm run dev" {
		t.Fatalf("input %q", h.m.prompt.Input)
	}
	// C-w drops the word before the cursor; C-a then typing inserts at the start
	h.press("C-w", "C-a")
	h.typeText("pnpm ")
	if h.m.prompt.Input != "pnpm npm run " {
		t.Fatalf("input %q", h.m.prompt.Input)
	}
	// C-g: the text in $EDITOR; the edited lines come back as one line, the prompt still open
	var got string
	h.m.edit = func(path string, _ int, done func(error) tea.Msg) tea.Cmd {
		data, _ := os.ReadFile(path)
		got = string(data)
		os.WriteFile(path, []byte("go run ./cmd/bx\nserve\n"), 0o600)
		return func() tea.Msg { return done(nil) }
	}
	h.press("C-g")
	if got != "pnpm npm run " {
		t.Errorf("editor got %q", got)
	}
	if !h.m.prompt.Active() || h.m.prompt.Input != "go run ./cmd/bx serve" {
		t.Fatalf("active %v input %q", h.m.prompt.Active(), h.m.prompt.Input)
	}
	h.press("Enter", "C-s")
	if n := len(h.be.runSaves); n != 1 || h.be.runSaves[0].Runs[0].Cmd != "go run ./cmd/bx serve" {
		t.Fatalf("saves %+v", h.be.runSaves)
	}
}
