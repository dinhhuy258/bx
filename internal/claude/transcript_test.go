package claude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTranscriptPath(t *testing.T) {
	got := TranscriptPath("/h/.claude/projects", "/Users/me/.bx/worktrees/home_net/s1", "u1")
	if got != "/h/.claude/projects/-Users-me--bx-worktrees-home-net-s1/u1.jsonl" {
		t.Fatal(got)
	}
}

func TestFirstPrompt(t *testing.T) {
	for name, c := range map[string]struct{ lines, want string }{
		"plain": {`{"type":"queue-operation"}
{"type":"user","message":{"content":"fix the  login\nredirect"}}
{"type":"user","message":{"content":"second"}}`, "fix the login redirect"},
		"local commands and meta skipped": {`{"type":"user","isMeta":true,"message":{"content":"<local-command-caveat>x</local-command-caveat>"}}
{"type":"user","message":{"content":"<command-name>/model</command-name>\n<command-message>model</command-message>\n<command-args></command-args>"}}
{"type":"user","message":{"content":"<local-command-stdout>Set model</local-command-stdout>"}}
{"type":"user","message":{"content":[{"type":"text","text":"from blocks"}]}}`, "from blocks"},
		"command with arguments": {`{"type":"user","message":{"content":"<command-message>build</command-message>\n<command-name>/build</command-name>\n<command-args>add a\nsettings screen</command-args>"}}`, "/build add a settings screen"},
		"tool results only":      {`{"type":"user","message":{"content":[{"type":"tool_result","content":"x"}]}}`, ""},
		"no prompt yet":          {`{"type":"queue-operation"}`, ""},
	} {
		path := filepath.Join(t.TempDir(), "t.jsonl")
		if err := os.WriteFile(path, []byte(c.lines+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := FirstPrompt(path); got != c.want {
			t.Errorf("%s: got %q, want %q", name, got, c.want)
		}
	}
	if FirstPrompt(filepath.Join(t.TempDir(), "missing.jsonl")) != "" {
		t.Error("a missing transcript has no prompt")
	}
	long := filepath.Join(t.TempDir(), "long.jsonl")
	os.WriteFile(long, []byte(strings.Repeat(`{"type":"attachment"}`+"\n", promptLines)+`{"type":"user","message":{"content":"late"}}`+"\n"), 0o644)
	if FirstPrompt(long) != "" {
		t.Error("looked past promptLines")
	}
}

func TestAITitle(t *testing.T) {
	for name, c := range map[string]struct{ lines, want string }{
		"after the prompt": {`{"type":"user","message":{"content":"the ai-title is not the prompt"}}
{"type":"ai-title","aiTitle":"Guest  network\nDHCP lease","sessionId":"u1"}
{"type":"ai-title","aiTitle":"later","sessionId":"u1"}`, "Guest network DHCP lease"},
		"none yet":    {`{"type":"user","message":{"content":"mentions \"ai-title\" only"}}`, ""},
		"broken line": {`{"type":"ai-title","aiTitle":`, ""},
		"empty title": {`{"type":"ai-title","aiTitle":"  "}`, ""},
	} {
		path := filepath.Join(t.TempDir(), "t.jsonl")
		if err := os.WriteFile(path, []byte(c.lines+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := AITitle(path); got != c.want {
			t.Errorf("%s: got %q, want %q", name, got, c.want)
		}
	}
	if AITitle(filepath.Join(t.TempDir(), "missing.jsonl")) != "" {
		t.Error("a missing transcript has no title")
	}
}
