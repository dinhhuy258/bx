package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/dinhhuy258/bx/internal/config"
)

func TestKeymapGroupsByWhatTheKeysActOn(t *testing.T) {
	cfg := config.Default("/x")
	cfg.Keys.List["my_macro"] = []string{"z M"}
	delete(cfg.Keys.List, "artifacts") // unbound: not listed
	v := ansi.Strip(Keymap(cfg, 80))
	session, shell, story := strings.Index(v, "SESSION"), strings.Index(v, "SHELL"), strings.Index(v, "STORY")
	if session < 0 || shell < session || story < shell {
		t.Fatal(v)
	}
	for _, want := range []string{
		"D      delete · removes uploads and artifacts; transcript stays in ~/.claude",
		"D      close · kills what runs in it",
		"X      close · off this client, keeps running",
		"zM     my macro",
		"1…9    open project by its number in the tabs",
	} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "the pages it made") || strings.Contains(v, "delete_session") || strings.Contains(v, "project 2") {
		t.Error(v)
	}
	if strings.Contains(v[:shell], "close · kills") {
		t.Error("closing a shell is listed under SESSION:", v)
	}
	// wide enough: two columns, so it fits a screen of the usual height
	if wide := ansi.Strip(Keymap(cfg, 160)); strings.Count(wide, "\n") >= strings.Count(v, "\n")*2/3 {
		t.Errorf("not in two columns:\n%s", wide)
	}
}
