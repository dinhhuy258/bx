package ui

import "testing"

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
	if p.Line() != "⏎ create · esc cancel" {
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
	p.Ask(PromptCloseStory, "s1", "Close story s1?")
	if p.Line() != "y close · n cancel · ⏎ cancel · esc cancel" {
		t.Fatal(p.Line())
	}
	if r := p.Key("x", "x"); r != PromptKeep || !p.Active() {
		t.Fatal("a stray key does not answer a question:", r)
	}
	if r := p.Key("n", "n"); r != PromptCancel {
		t.Fatal(r)
	}
	p.Ask(PromptCloseStory, "s1", "Close story s1?")
	if r := p.Key("y", "y"); r != PromptSubmit || p.Target != "s1" {
		t.Fatal(r)
	}
}

func TestQuestionsEnterPicksTheDefault(t *testing.T) {
	// Enter takes the action, unless the question loses something for good
	for k, enter := range map[PromptKind]PromptResult{
		PromptStop: PromptSubmit, PromptResume: PromptSubmit, PromptRemoveOverride: PromptSubmit,
		PromptNewShell: PromptSubmit, PromptNewSession: PromptSubmit, PromptCloseProject: PromptSubmit,
		PromptDelete: PromptCancel, PromptDeleteForce: PromptCancel, PromptCloseStory: PromptCancel,
		PromptCloseShell: PromptCancel, PromptDiscardSettings: PromptCancel,
	} {
		var p Prompt
		p.Ask(k, "1", "Sure?")
		if !p.YesNo() {
			t.Fatalf("kind %d is not y/n", k)
		}
		if r := p.Key("Enter", ""); r != enter {
			t.Errorf("kind %d: Enter gave %v, want %v", k, r, enter)
		}
		p.Ask(k, "1", "Sure?")
		if r := p.Key("y", "y"); r != PromptSubmit || p.Target != "1" || p.Active() {
			t.Errorf("kind %d: y gave %v", k, r)
		}
		for _, key := range []string{"n", "Esc"} {
			p.Ask(k, "1", "Sure?")
			if r := p.Key(key, key); r != PromptCancel || p.Active() {
				t.Errorf("kind %d: %s gave %v, want cancel", k, key, r)
			}
		}
	}
	var p Prompt
	p.Ask(PromptResume, "6", "Resume [6]?", "old")
	if p.Line() != "y resume · n cancel · ⏎ resume · esc cancel" {
		t.Fatal(p.Line())
	}
	p.Open(PromptRenameShell, "s1/1", "Name", "dev")
	if p.YesNo() || p.Input != "dev" {
		t.Fatal("rename shell is a text prompt with the old title")
	}
}
