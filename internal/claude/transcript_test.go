package claude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestPromptsLast(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.jsonl")
	write := func(s string) {
		t.Helper()
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if _, err := f.WriteString(s); err != nil {
			t.Fatal(err)
		}
	}
	var p Prompts
	if p.Last(path) != "" {
		t.Fatal("a missing transcript has no prompt")
	}
	write(`{"type":"user","message":{"content":"first"}}` + "\n" +
		`{"type":"user","message":{"content":"  fix the\n  login  "}}` + "\n" +
		`{"type":"user","message":{"content":[{"type":"tool_result","content":"x"}]}}` + "\n" +
		`{"type":"assistant","message":{"content":"done"}}` + "\n")
	if got := p.Last(path); got != "fix the\n  login" {
		t.Fatalf("got %q: lines kept, ends trimmed, tool results skipped", got)
	}
	// a line still being written doesn't count until it is whole
	write(`{"type":"user","message":{"content":"/build`)
	if got := p.Last(path); got != "fix the\n  login" {
		t.Fatalf("half a line: %q", got)
	}
	write(`"}}` + "\n")
	if got := p.Last(path); got != "/build" {
		t.Fatalf("appended: %q", got)
	}
	write(`{"type":"user","message":{"content":"<command-name>/review</command-name>\n<command-args> 12 </command-args>"}}` + "\n")
	if got := p.Last(path); got != "/review 12" {
		t.Fatalf("command: %q", got)
	}
	// rewritten shorter: read again from the end
	os.WriteFile(path, []byte(`{"type":"user","message":{"content":"new"}}`+"\n"), 0o644)
	if got := p.Last(path); got != "new" {
		t.Fatalf("rewritten: %q", got)
	}

	// a prompt further back than the first chunk, and one split by the chunk's start
	far := filepath.Join(t.TempDir(), "far.jsonl")
	prompt := `{"type":"user","message":{"content":"far back"}}` + "\n"
	filler := strings.Repeat(`{"type":"assistant","message":{"content":"`+strings.Repeat("x", 1000)+`"}}`+"\n", tailChunk/1000)
	os.WriteFile(far, []byte(prompt+filler), 0o644)
	if got := new(Prompts).Last(far); got != "far back" {
		t.Fatalf("far: %q", got)
	}
	split := filepath.Join(t.TempDir(), "split.jsonl")
	os.WriteFile(split, []byte(prompt+strings.Repeat("y", tailChunk-len(prompt)/2-1)+"\n"), 0o644)
	if got := new(Prompts).Last(split); got != "far back" {
		t.Fatalf("split: %q", got)
	}
}

func TestPromptsLastCapsAndFallsBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.jsonl")
	long := strings.Repeat("é", promptMax+50)
	os.WriteFile(path, []byte(`{"type":"user","message":{"content":"`+long+`"}}`+"\n"), 0o644)
	var p Prompts
	if got := p.Last(path); got != strings.Repeat("é", promptMax) {
		t.Fatalf("cut to %d runes, want %d", len([]rune(got)), promptMax)
	}
	// more than tailMax added since the last call: read from the end, not all of it
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString(`{"type":"user","message":{"content":"buried"}}` + "\n")
	f.WriteString(strings.Repeat(`{"type":"assistant"}`+"\n", tailMax/20+1))
	f.WriteString(`{"type":"user","message":{"content":"latest"}}` + "\n")
	f.Close()
	if got := p.Last(path); got != "latest" {
		t.Fatalf("after a big append: %q", got)
	}
}

func TestPromptsForgetIdleTranscripts(t *testing.T) {
	var p Prompts
	t0 := time.Now()
	p.remember("a", promptSeen{text: "a"}, t0)
	p.remember("b", promptSeen{text: "b"}, t0.Add(promptIdle/2))
	if len(p.seen) != 2 {
		t.Fatalf("seen %v", p.seen)
	}
	p.remember("b", promptSeen{text: "b"}, t0.Add(promptIdle+time.Minute)) // a is idle, b was asked about
	if _, ok := p.seen["a"]; ok || len(p.seen) != 1 {
		t.Fatalf("idle a kept: %v", p.seen)
	}
}
