package ui

import "strings"

// PromptKind says what the one-line input in the key bar is for.
type PromptKind int

const (
	PromptNone PromptKind = iota
	PromptNewStory
	PromptRename
	PromptCloseStory
	PromptCloseStoryForce
	PromptAddFolder  // an absolute folder path on the server
	PromptNewProject // a project name
	PromptDiscardSettings
	PromptAddRepoForce // y/n: add repositories whose originals have uncommitted files
	PromptStop         // y/n: stop a live session (Target is its number)
	PromptDelete       // y/n: delete a session's record (Target is its number)
	PromptDeleteForce  // y/n: the session is still open; kill it and delete
)

// PromptResult is what a key did to the prompt.
type PromptResult int

const (
	PromptKeep   PromptResult = iota // still typing
	PromptSubmit                     // Enter, or y on a yes/no prompt: read Input and Target
	PromptCancel                     // Esc, or anything but y on a yes/no prompt
)

// Prompt is the one-line input shown in the key bar (new story, rename, close story).
// Used by the client.
type Prompt struct {
	Kind   PromptKind
	Target string // the story a new session or a close applies to
	Label  string
	Input  string
}

// Open starts a prompt with an initial text (the old title for rename).
func (p *Prompt) Open(kind PromptKind, target, label, initial string) {
	p.Kind, p.Target, p.Label, p.Input = kind, target, label, initial
}

// Active reports whether a prompt is open.
func (p *Prompt) Active() bool { return p.Kind != PromptNone }

// YesNo reports whether the prompt is a y/n question.
func (p *Prompt) YesNo() bool {
	switch p.Kind {
	case PromptCloseStory, PromptCloseStoryForce, PromptDiscardSettings, PromptAddRepoForce,
		PromptStop, PromptDelete, PromptDeleteForce:
		return true
	}
	return false
}

// Key feeds one key (bx key name plus the typed text). On Submit the caller reads Input and
// Target, which stay set until the next Open; Kind is reset to PromptNone, so the caller
// saves it before calling Key.
func (p *Prompt) Key(key, text string) PromptResult {
	switch {
	case key == "Esc":
		p.Kind = PromptNone
		return PromptCancel
	case p.YesNo():
		p.Kind = PromptNone
		if key == "y" {
			return PromptSubmit
		}
		return PromptCancel
	case key == "Enter":
		p.Kind = PromptNone
		return PromptSubmit
	case key == "BSpace":
		if r := []rune(p.Input); len(r) > 0 {
			p.Input = string(r[:len(r)-1])
		}
	case text != "":
		p.Input += text
	case key == "Space":
		p.Input += " "
	}
	return PromptKeep
}

// Paste adds pasted text to the input, on one line.
func (p *Prompt) Paste(text string) { p.Input += oneLine(text) }

// oneLine drops the line breaks of pasted text, so a copied path with a trailing newline fits.
func oneLine(s string) string { return strings.NewReplacer("\r", "", "\n", "").Replace(s) }

// Line is the key bar text while the prompt is open.
func (p *Prompt) Line() string { return p.Label + ": " + p.Input + "▏  ⏎ ok · esc cancel" }
