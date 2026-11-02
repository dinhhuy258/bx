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
