package client

import (
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/dinhhuy258/bx/internal/api"
	"github.com/dinhhuy258/bx/internal/runcfg"
)

func TestSettingsAppsEditTextAndRemove(t *testing.T) {
	h := start(t, 120, 30, "running")
	h.be.runCfgs = map[string]api.RunConfig{"home-network": {Runs: []runcfg.Run{{Name: "web", Cmd: "npm run dev"}, {Name: "api", Cmd: "php artisan serve"}}}}
	h.be.runTexts = map[string]string{"home-network": "# mine\n[[run]]\nname = \"web\"\ncmd = \"npm run dev\"\n"}
	drain(h.m, h.m.openAppsSettings("home-network"))
	if v := view(h); !strings.Contains(v, "Apps › home-network") || !strings.Contains(v, "npm run dev") {
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
