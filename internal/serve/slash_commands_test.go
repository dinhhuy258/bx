package serve

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dinhhuy258/bx/internal/api"
)

func TestListSlashCommands(t *testing.T) {
	s, _, ts := newServer(t)
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	put := func(p, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	put(filepath.Join(cfg, "skills", "plan", "SKILL.md"), "---\nname: plan\ndescription: Make a plan\n---\n")
	// session 1 runs in story s1's worktree: its .claude folder counts, the project's doesn't
	p, err := s.App.LoadProject("home-network")
	if err != nil {
		t.Fatal(err)
	}
	put(filepath.Join(s.App.Dir(p, "s1"), ".claude", "commands", "ship.md"), "---\ndescription: Ship it\n---\n")

	got, code := get[api.SlashCommandList](t, ts, "/api/projects/home-network/sessions/1/slash-commands")
	if code != 200 {
		t.Fatalf("code %d", code)
	}
	by := map[string]string{}
	for _, c := range got.Commands {
		by[c.Name] = c.Source + ":" + c.Description
	}
	for n, want := range map[string]string{"plan": "skill:Make a plan", "ship": "command:Ship it", "clear": "builtin:Clear conversation history and free up context"} {
		if by[n] != want {
			t.Errorf("%s: %q, want %q", n, by[n], want)
		}
	}
	if _, code = get[api.SlashCommandList](t, ts, "/api/projects/home-network/sessions/9/slash-commands"); code != 404 {
		t.Fatalf("unknown session: %d", code)
	}
}
