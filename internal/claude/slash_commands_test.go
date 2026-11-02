package claude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSlashCommands(t *testing.T) {
	cfg, work, plugin := t.TempDir(), t.TempDir(), t.TempDir()
	write(t, filepath.Join(cfg, "skills", "plan", "SKILL.md"), "---\nname: plan\ndescription: \"Make a plan\"\n---\n# Plan\n")
	write(t, filepath.Join(cfg, "skills", "nofm", "SKILL.md"), "# no frontmatter\n")
	write(t, filepath.Join(cfg, "skills", "multi", "SKILL.md"), "---\ndescription: >\n  folded text\nname: multi\n---\n")
	write(t, filepath.Join(cfg, "skills", "empty", "README.md"), "not a skill")
	write(t, filepath.Join(cfg, "commands", "ship.md"), "---\ndescription: Ship it\n---\nbody")
	write(t, filepath.Join(cfg, "commands", "notes.txt"), "skip")
	write(t, filepath.Join(work, ".claude", "commands", "ship.md"), "---\ndescription: Project ship\n---\n")
	write(t, filepath.Join(work, ".claude", "skills", "clear", "SKILL.md"), "---\ndescription: overrides the built-in\n---\n")
	write(t, filepath.Join(plugin, ".claude-plugin", "plugin.json"), `{"name":"bx"}`)
	write(t, filepath.Join(plugin, "skills", "artifact", "SKILL.md"), "---\nname: artifact\ndescription: "+strings.Repeat("x", 300)+"\n---\n")

	got := map[string]SlashCommand{}
	var names []string
	for _, c := range SlashCommands(cfg, work, plugin) {
		got[c.Name] = c
		names = append(names, c.Name)
	}
	for i := 1; i < len(names); i++ {
		if names[i-1] >= names[i] {
			t.Fatalf("not sorted: %v", names)
		}
	}
	want := map[string]SlashCommand{
		"plan":  {"plan", "Make a plan", "skill"},
		"nofm":  {"nofm", "", "skill"},
		"multi": {"multi", "", "skill"},
		"ship":  {"ship", "Project ship", "command"},
		"clear": {"clear", "overrides the built-in", "skill"},
		"model": {"model", "Set the AI model", "builtin"},
	}
	for n, w := range want {
		if got[n] != w {
			t.Errorf("%s: got %+v, want %+v", n, got[n], w)
		}
	}
	if _, ok := got["empty"]; ok {
		t.Error("a folder without SKILL.md is listed")
	}
	if _, ok := got["notes"]; ok {
		t.Error("a non-.md command is listed")
	}
	a := got["bx:artifact"]
	if a.Source != "plugin" || len([]rune(a.Description)) != maxDescription || !strings.HasSuffix(a.Description, "…") {
		t.Errorf("plugin skill: %+v", a)
	}
}

func TestSlashCommandsNoFolders(t *testing.T) {
	got := SlashCommands("", "", "")
	if len(got) != len(builtins) {
		t.Fatalf("got %d, want the %d built-ins", len(got), len(builtins))
	}
	missing := filepath.Join(t.TempDir(), "nope")
	if n := len(SlashCommands(missing, missing, missing)); n != len(builtins) {
		t.Fatalf("missing folders: %d", n)
	}
}
