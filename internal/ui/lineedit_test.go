package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// show is text with the cursor as | (back runes from the end).
func show(text string, back int) string {
	r := []rune(text)
	at := len(r) - back
	return string(r[:at]) + "|" + string(r[at:])
}

func TestEditLine(t *testing.T) {
	type step struct{ key, typed, want string }
	for _, tc := range []struct {
		name, start string
		steps       []step
	}{
		{"move and insert", "hello world", []step{
			{"Left", "", "hello worl|d"},
			{"C-a", "", "|hello world"},
			{"x", "x", "x|hello world"},
			{"End", "", "xhello world|"},
			{"Home", "", "|xhello world"},
			{"Right", "", "x|hello world"},
			{"C-e", "", "xhello world|"},
			{"Space", "", "xhello world |"},
		}},
		{"words", "cd /tmp/bx-dev && go run", []step{
			{"M-b", "", "cd /tmp/bx-dev && go |run"},
			{"M-Left", "", "cd /tmp/bx-dev && |go run"},
			{"M-b", "", "cd /tmp/bx-|dev && go run"},
			{"M-f", "", "cd /tmp/bx-dev| && go run"},
			{"M-Right", "", "cd /tmp/bx-dev && go| run"},
		}},
		{"delete", "mkdir -p /tmp/bx-dev && go run", []step{
			{"BSpace", "", "mkdir -p /tmp/bx-dev && go ru|"},
			{"C-w", "", "mkdir -p /tmp/bx-dev && go |"},
			{"M-BSpace", "", "mkdir -p /tmp/bx-dev && |"},
			{"M-BSpace", "", "mkdir -p /tmp/bx-|"},
			{"C-a", "", "|mkdir -p /tmp/bx-"},
			{"Delete", "", "|kdir -p /tmp/bx-"},
			{"C-d", "", "|dir -p /tmp/bx-"},
			{"M-f", "", "dir| -p /tmp/bx-"},
			{"C-k", "", "dir|"},
			{"C-u", "", "|"},
			{"BSpace", "", "|"},
			{"Delete", "", "|"},
		}},
		{"unicode", "héllo", []step{
			{"Left", "", "héll|o"},
			{"C-a", "", "|héllo"},
			{"Right", "", "h|éllo"},
			{"Delete", "", "h|llo"},
		}},
		{"edges", "ab", []step{
			{"Right", "", "ab|"},
			{"C-a", "", "|ab"},
			{"Left", "", "|ab"},
			{"M-b", "", "|ab"},
		}},
	} {
		text, back := tc.start, 0
		for _, s := range tc.steps {
			var ok bool
			text, back, ok = editLine(text, back, s.key, s.typed)
			if !ok {
				t.Fatalf("%s: %s not handled", tc.name, s.key)
			}
			if got := show(text, back); got != s.want {
				t.Fatalf("%s: after %s got %q, want %q", tc.name, s.key, got, s.want)
			}
		}
	}
	if _, _, ok := editLine("x", 0, "Enter", ""); ok {
		t.Error("Enter is not an edit")
	}
}

func TestPromptEditsAtTheCursor(t *testing.T) {
	var p Prompt
	p.Open(PromptRunText, "4", "Command", "npm run dev")
	for _, k := range []string{"M-b", "M-b", "BSpace"} {
		p.Key(k, "")
	}
	p.Key("x", "x")
	p.Paste("-y\n")
	if got := show(p.Input, p.Back); got != "npmx-y|run dev" {
		t.Fatalf("got %q", got)
	}
	if r := p.Key("C-g", ""); r != PromptEdit || !p.Active() {
		t.Fatalf("C-g: %v active=%v", r, p.Active())
	}
	p.SetInput("first line\nsecond\n\n")
	if got := show(p.Input, p.Back); got != "first line second|" {
		t.Fatalf("from the editor: %q", got)
	}
	// the story form: ←→ change the type on the type field, and edit on the name
	p.OpenStory("main", []string{"feat", "fix"}, "feat")
	p.Key("a", "a")
	p.Key("b", "b")
	p.Key("Left", "")
	if p.Back != 1 || p.Type != 0 {
		t.Fatalf("left on the name moves the cursor: back=%d type=%d", p.Back, p.Type)
	}
	p.Key("Tab", "")
	p.Key("Right", "")
	if p.Type != 1 || p.Back != 1 {
		t.Fatalf("right on the type changes it: back=%d type=%d", p.Back, p.Type)
	}
	if r := p.Key("C-g", ""); r != PromptKeep {
		t.Errorf("C-g on the type field: %v", r)
	}
	// a question has no editor
	p.Ask(PromptStop, "1", "Stop?")
	if r := p.Key("C-g", ""); r != PromptKeep {
		t.Errorf("C-g on a question: %v", r)
	}
}

func TestDialogWrapsALongValueWithTheCursor(t *testing.T) {
	var p Prompt
	long := "mkdir -p /tmp/bx-dev-${story} && printf '[serve]\\nlisten = \"127.0.0.1:7457\"\\nfiles_listen = \"off\"\\n' > /tmp/bx-dev-${story}/config.toml && go run ./cmd/bx serve"
	p.Open(PromptRunText, "4", "Command", long)
	out := p.Dialog().Render(100, 30, true)
	for _, want := range []string{"mkdir -p", "serve▏"} {
		if !contains(out, want) {
			t.Errorf("no %q in\n%s", want, out)
		}
	}
	if contains(out, "…") {
		t.Errorf("a long value wraps instead of being cut:\n%s", out)
	}
	p.Key("C-a", "")
	if out = p.Dialog().Render(100, 30, true); !contains(out, "▏mkdir") {
		t.Errorf("the cursor at the start:\n%s", out)
	}
	// past 4 lines, the window follows the cursor
	p.Open(PromptRunText, "4", "Command", repeat("abcdefghij ", 40)+"END")
	out = p.Dialog().Render(100, 30, true)
	if !contains(out, "END▏") || !contains(out, "↑") {
		t.Errorf("the end of a very long value, marked as scrolled:\n%s", out)
	}
	assertSquare(t, out)
}

func contains(s, sub string) bool   { return strings.Contains(s, sub) }
func repeat(s string, n int) string { return strings.Repeat(s, n) }

func assertSquare(t *testing.T, out string) {
	t.Helper()
	lines := strings.Split(out, "\n")
	for _, l := range lines {
		if lipgloss.Width(l) != lipgloss.Width(lines[0]) {
			t.Fatalf("ragged box:\n%s", out)
		}
	}
}
