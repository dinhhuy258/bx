package ui

import (
	"strings"
	"testing"
)

func TestPromptTypesSubmitsAndCancels(t *testing.T) {
	var p Prompt
	if p.Active() {
		t.Fatal("inactive at start")
	}
	p.Open(PromptNewStory, "", "story name", "")
	for _, k := range []string{"g", "u", "BSpace", "e", "s", "t"} {
		text := k
		if k == "BSpace" {
			text = ""
		}
		if r := p.Key(k, text); r != PromptKeep {
			t.Fatalf("%s: %v", k, r)
		}
	}
	if p.Line() != "story name: gest▏  ⏎ ok · esc cancel" {
		t.Fatal(p.Line())
	}
	if r := p.Key("Enter", ""); r != PromptSubmit || p.Active() || p.Input != "gest" {
		t.Fatalf("r=%v active=%v input=%q", r, p.Active(), p.Input)
	}
	p.Open(PromptRename, "", "new title", "old")
	if p.Input != "old" {
		t.Fatal(p.Input)
	}
	if r := p.Key("Esc", ""); r != PromptCancel || p.Active() {
		t.Fatal(r)
	}
	p.Open(PromptCloseStory, "s1", "close s1? y/n", "")
	if p.Line() != "close s1? y/n: ▏  y close · any key cancels" {
		t.Fatal(p.Line())
	}
	if r := p.Key("n", "n"); r != PromptCancel {
		t.Fatal(r)
	}
	p.Open(PromptCloseStory, "s1", "close s1? y/n", "")
	if r := p.Key("y", "y"); r != PromptSubmit || p.Target != "s1" {
		t.Fatal(r)
	}
}

func TestLifecyclePromptsAreYesNo(t *testing.T) {
	for _, k := range []PromptKind{PromptStop, PromptDelete, PromptDeleteForce, PromptUseBranch, PromptRemoveOverride} {
		var p Prompt
		p.Open(k, "1", "sure? y/n", "")
		if !p.YesNo() {
			t.Fatalf("kind %d is not y/n", k)
		}
		if r := p.Key("Enter", ""); r != PromptCancel {
			t.Fatalf("kind %d: Enter gave %v, want cancel", k, r)
		}
		p.Open(k, "1", "sure? y/n", "")
		if r := p.Key("y", "y"); r != PromptSubmit || p.Target != "1" {
			t.Fatalf("kind %d: y gave %v", k, r)
		}
	}
	var p Prompt
	p.Open(PromptUseBranch, "", "branch fix/x exists on origin · use it? y/n", "")
	if p.Line() != "branch fix/x exists on origin · use it? y/n: ▏  y use · any key cancels" {
		t.Fatal(p.Line())
	}
	p.Open(PromptRemoveOverride, "lab", "remove base override of lab (develop)? y/n", "")
	if !strings.HasSuffix(p.Line(), "y remove · any key cancels") {
		t.Fatal(p.Line())
	}
}

func TestShellPrompts(t *testing.T) {
	var p Prompt
	p.Open(PromptNewShell, "s1/1", "New shell $1 in s1  /w", "")
	if got, want := p.Line(), "New shell $1 in s1  /w   y open  n cancel"; got != want {
		t.Fatalf("\n got %q\nwant %q", got, want)
	}
	for _, key := range []string{"Enter", "y"} {
		p.Open(PromptNewShell, "s1/1", "New shell $1 in s1  /w", "")
		if r := p.Key(key, key); r != PromptSubmit || p.Target != "s1/1" || p.Active() {
			t.Fatalf("%s gave %v, want submit", key, r)
		}
	}
	for _, key := range []string{"n", "Esc"} {
		p.Open(PromptNewShell, "s1/1", "New shell $1 in s1  /w", "")
		if r := p.Key(key, key); r != PromptCancel || p.Active() {
			t.Fatalf("%s gave %v, want cancel", key, r)
		}
	}
	p.Open(PromptCloseShell, "s1/1", "Close shell $1 zsh?", "")
	if got, want := p.Line(), "Close shell $1 zsh?   y close  n cancel"; got != want {
		t.Fatalf("\n got %q\nwant %q", got, want)
	}
	if r := p.Key("Enter", ""); r != PromptCancel {
		t.Fatalf("Enter on close shell gave %v, want cancel", r)
	}
	p.Open(PromptCloseShell, "s1/1", "Close shell $1 zsh?", "")
	if r := p.Key("y", "y"); r != PromptSubmit {
		t.Fatalf("y gave %v", r)
	}
	p.Open(PromptRenameShell, "s1/1", "shell name (empty = command)", "dev")
	if p.YesNo() || p.Input != "dev" {
		t.Fatal("rename shell is a text prompt with the old title")
	}
}
