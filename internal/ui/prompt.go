package ui

import (
	"strings"

	"github.com/dinhhuy258/bx/internal/naming"
)

// PromptKind says what a prompt is for.
type PromptKind int

const (
	PromptNone PromptKind = iota
	PromptNewStory
	PromptRename
	PromptCloseStory
	PromptCloseStoryForce
	PromptAddFolder    // an absolute folder path on the server
	PromptNewProject   // a project name
	PromptNewWorkspace // a workspace name
	PromptDiscardSettings
	PromptAddRepoForce          // y/n: add repositories whose originals have uncommitted files
	PromptStop                  // y/n: stop a live session (Target is its number)
	PromptDelete                // y/n: delete a session's record (Target is its number)
	PromptDeleteForce           // y/n: the session is still open; kill it and delete
	PromptRemoveOverride        // y/n: remove a project's base override (Target is the project)
	PromptSettingText           // a text setting's new value (Target is its key)
	PromptNewShell              // y/n: open a shell (Target is "story/k")
	PromptCloseShell            // y/n: close a shell, killing what runs in it (Target is "story/k")
	PromptRenameShell           // a shell's new title, empty = its command (Target is "story/k")
	PromptRemoveMCP             // y/n: remove the MCP servers of a scope (Target is the project, "" = global)
	PromptNewSession            // y/n: start a session (Target is the story, "" = a chat)
	PromptCloseProject          // y/n: take the open project off this client's list (Target is the project)
	PromptResume                // y/n: start Claude again in a done or gone session (Target is its number)
	PromptAccountText           // a text field of the account form (Target is its field number)
	PromptRuleOwner             // the owner of an account rule (Target is its index, -1 = new)
	PromptRemoveAccount         // y/n: remove a GitHub account and its rules and overrides (Target is its id)
	PromptRemoveRule            // y/n: remove an account rule (Target is its index)
	PromptRemoveAccountOverride // y/n: remove a project's account override (Target is the project)
	PromptDiscardAccount        // y/n: drop the edits of the account form
	PromptLogin                 // y/n: open a shell running gh auth login for github.com
	PromptComment               // a review comment on a diff line, empty = delete it
	PromptClearSent             // y/n: drop the sent comments of a review (Target is the story)
	PromptSyncBusy              // y/n: sync a story Claude works in (Target is the story, "" = chats)
	PromptRunReplace            // y/n: stop the story that runs and run this one (Target is the story)
	PromptExplain               // a question about diff lines; Types are the conversations
)

// PromptResult is what a key did to the prompt.
type PromptResult int

const (
	PromptKeep   PromptResult = iota // still typing
	PromptSubmit                     // Enter, or y on a yes/no prompt: read Input and Target
	PromptCancel                     // Esc, or anything but y on a yes/no prompt
)

// Prompt is a question or a one-field form, drawn as a dialog over the screen (new story,
// rename, close story…). Used by the client.
type Prompt struct {
	Kind   PromptKind
	Target string // the story a new session or a close applies to
	Label  string // a form's field label; a question's title
	Body   []string
	Title  string // a form's title when it is not the kind's (a comment names its line)
	Input  string

	// the new story form: its branch types, the chosen one, the field with the cursor (0 the
	// name, 1 the type), the base it starts from and a note under the preview
	Types []string
	Type  int
	Focus int
	Base  string
	Note  string
}

// OpenStory starts the new story form: a name, the branch type (typ chosen) and a preview of
// the branch and its base.
func (p *Prompt) OpenStory(base string, types []string, typ string) {
	p.Open(PromptNewStory, "", "Name", "")
	p.Types, p.Type, p.Base = types, max(indexOf(types, typ), 0), base
}

func indexOf(list []string, s string) int {
	for i, x := range list {
		if x == s {
			return i
		}
	}
	return -1
}

// BranchType is the chosen branch type of the new story form, "" when it has none.
func (p *Prompt) BranchType() string {
	if p.Type < len(p.Types) {
		return p.Types[p.Type]
	}
	return ""
}

// StoryBranch is the branch the new story form makes: a typed full branch ("fix/x") as it is,
// else the chosen type and the name's slug; "<name>" stands in for an empty name.
func (p *Prompt) StoryBranch() string {
	name := strings.TrimSpace(p.Input)
	if b := naming.CleanBranch(name); strings.Contains(b, "/") {
		return b
	}
	slug := naming.Slug(name)
	if slug == "" {
		slug = "<name>"
	}
	if t := p.BranchType(); t != "" {
		return naming.Branch(t, slug)
	}
	return slug
}

// Open starts a one-field form with an initial text (the old title for rename).
func (p *Prompt) Open(kind PromptKind, target, label, initial string) {
	*p = Prompt{Kind: kind, Target: target, Label: label, Input: initial}
}

// Ask starts a y/n question: a title and the lines that say what will happen.
func (p *Prompt) Ask(kind PromptKind, target, title string, body ...string) {
	*p = Prompt{Kind: kind, Target: target, Label: title, Body: body}
}

// CommentQuote is how many lines of code the comment form quotes.
const CommentQuote = 4

// OpenComment starts the comment form on diff lines: title names them, the body quotes their
// code. The field is one line, so a comment written on the phone is joined with spaces.
func (p *Prompt) OpenComment(title, code, initial string) {
	p.Open(PromptComment, "", "Comment (empty deletes it)", strings.ReplaceAll(initial, "\n", " "))
	p.Title, p.Body = title, nil
	for _, l := range QuoteCode(code, CommentQuote) {
		p.Body = append(p.Body, "> "+ReviewText(l))
	}
	if len(p.Body) == 0 {
		p.Body = []string{">"}
	}
}

// OpenExplain starts the explain form on diff lines: title names them, the body quotes their
// code, the field takes an optional question and Types are the conversations to use.
func (p *Prompt) OpenExplain(title, code string, convs []string, conv int) {
	p.OpenComment(title, code, "")
	p.Kind, p.Label, p.Types, p.Type = PromptExplain, "Question (empty: what it does and why)", convs, conv
}

// Active reports whether a prompt is open.
func (p *Prompt) Active() bool { return p.Kind != PromptNone }

// YesNo reports whether the prompt is a y/n question.
func (p *Prompt) YesNo() bool {
	switch p.Kind {
	case PromptCloseStory, PromptCloseStoryForce, PromptDiscardSettings, PromptAddRepoForce,
		PromptStop, PromptDelete, PromptDeleteForce, PromptRemoveOverride,
		PromptNewShell, PromptCloseShell, PromptRemoveMCP, PromptNewSession, PromptCloseProject, PromptResume,
		PromptRemoveAccount, PromptRemoveRule, PromptRemoveAccountOverride, PromptDiscardAccount, PromptLogin, PromptClearSent,
		PromptSyncBusy, PromptRunReplace:
		return true
	}
	return false
}

// Danger reports a question whose yes loses something for good: a record, a worktree, files,
// edits, a running program. Its dialog is red and Enter picks Cancel.
func (p *Prompt) Danger() bool {
	switch p.Kind {
	case PromptDelete, PromptDeleteForce, PromptCloseStory, PromptCloseStoryForce,
		PromptDiscardSettings, PromptCloseShell, PromptRemoveMCP, PromptRemoveAccount, PromptDiscardAccount:
		return true
	}
	return false
}

// Key feeds one key (bx key name plus the typed text). On Submit the caller reads Input and
// Target, which stay set until the next Open; Kind is reset to PromptNone, so the caller
// saves it before calling Key.
//
// A question takes y, n and Esc; Enter picks the default button, the action unless Danger.
// Other keys do nothing, so a stray key does not answer it.
func (p *Prompt) Key(key, text string) PromptResult {
	switch {
	case key == "Esc":
		p.Kind = PromptNone
		return PromptCancel
	case p.YesNo():
		switch {
		case key == "y" || key == "Enter" && !p.Danger():
			p.Kind = PromptNone
			return PromptSubmit
		case key == "n" || key == "Enter":
			p.Kind = PromptNone
			return PromptCancel
		}
	case key == "Enter":
		p.Kind = PromptNone
		return PromptSubmit
	case len(p.Types) > 0 && (key == "Tab" || key == "S-Tab" || key == "Up" || key == "Down"):
		p.Focus = 1 - p.Focus
	case len(p.Types) > 0 && p.Focus == 1 && (key == "Left" || key == "Right"):
		d := 1
		if key == "Left" {
			d = len(p.Types) - 1
		}
		p.Type = (p.Type + d) % len(p.Types)
		p.Note = ""
	case key == "BSpace":
		p.Note = ""
		if r := []rune(p.Input); len(r) > 0 {
			p.Input = string(r[:len(r)-1])
		}
	case text != "":
		p.Input, p.Note = p.Input+text, ""
	case key == "Space":
		p.Input, p.Note = p.Input+" ", ""
	}
	return PromptKeep
}

// Paste adds pasted text to the input, on one line.
func (p *Prompt) Paste(text string) { p.Input += oneLine(text) }

// oneLine drops the line breaks of pasted text, so a copied path with a trailing newline fits.
func oneLine(s string) string { return strings.NewReplacer("\r", "", "\n", "").Replace(s) }

// Line is the key bar text while the prompt is open: its keys.
func (p *Prompt) Line() string {
	if p.YesNo() {
		enter := p.verb()
		if p.Danger() {
			enter = "cancel"
		}
		return "y " + p.verb() + " · n cancel · ⏎ " + enter + " · esc cancel"
	}
	if p.Kind == PromptExplain {
		return "⏎ " + p.verb() + " · tab next field · ←→ conversation · esc cancel"
	}
	if len(p.Types) > 0 {
		return "⏎ " + p.verb() + " · tab next field · ←→ change type · esc cancel"
	}
	return "⏎ " + p.verb() + " · esc cancel"
}

// titles name the dialog of a form; a question's title is its Label.
var titles = map[PromptKind]string{
	PromptNewStory: "New story", PromptRename: "Rename session", PromptRenameShell: "Rename shell",
	PromptAddFolder: "Add existing folder", PromptNewProject: "New empty project", PromptNewWorkspace: "New workspace", PromptSettingText: "Edit setting",
	PromptAccountText: "Edit account", PromptRuleOwner: "Account rule · owner/*, host/* or host/owner/*",
}

// Dialog is the prompt's dialog: a form with its one field, or a question with its buttons.
func (p *Prompt) Dialog() Dialog {
	verb := strings.ToUpper(p.verb()[:1]) + p.verb()[1:]
	if p.Kind == PromptExplain {
		return Dialog{
			Title: p.Title, Tone: ToneForm, Body: p.Body, Focus: p.Focus,
			Fields:  []Field{{Label: p.Label, Value: p.Input}, {Label: "Conversation", Choices: p.Types, Choice: p.Type}},
			Hint:    "tab next  ←→ conversation",
			Buttons: []Button{{Key: "esc", Label: "Cancel"}, {Key: "⏎", Label: "Explain", Default: true}},
		}
	}
	if len(p.Types) > 0 {
		after := []string{"Branch  " + p.StoryBranch()}
		if p.Base != "" {
			after = append([]string{"Base    " + p.Base}, after...)
		}
		return Dialog{
			Title: titles[p.Kind], Tone: ToneForm, Focus: p.Focus, After: after, Note: p.Note,
			Fields:  []Field{{Label: p.Label, Value: p.Input}, {Label: "Type", Choices: p.Types, Choice: p.Type}},
			Hint:    "tab next  ←→ type",
			Buttons: []Button{{Key: "esc", Label: "Cancel"}, {Key: "⏎", Label: verb, Default: true}},
		}
	}
	if !p.YesNo() {
		title := titles[p.Kind]
		if p.Title != "" {
			title = p.Title
		}
		return Dialog{
			Title: title, Tone: ToneForm, Body: p.Body,
			Fields:  []Field{{Label: p.Label, Value: p.Input}},
			Buttons: []Button{{Key: "esc", Label: "Cancel"}, {Key: "⏎", Label: verb, Default: true}},
		}
	}
	d := Dialog{Title: p.Label, Tone: ToneConfirm, Body: p.Body}
	if p.Danger() {
		d.Tone = ToneDanger
		d.Buttons = []Button{{Key: "n", Label: "Cancel", Default: true}, {Key: "y", Label: verb, Danger: true}}
	} else {
		d.Buttons = []Button{{Key: "n", Label: "Cancel"}, {Key: "y", Label: verb, Default: true}}
	}
	return d
}

// verb is what y, or Enter on a form, does.
func (p *Prompt) verb() string {
	switch p.Kind {
	case PromptCloseStory:
		return "close"
	case PromptCloseStoryForce, PromptDiscardSettings, PromptDiscardAccount:
		return "discard"
	case PromptAddRepoForce, PromptAddFolder:
		return "add"
	case PromptStop:
		return "stop"
	case PromptDelete:
		return "delete"
	case PromptDeleteForce:
		return "kill and delete"
	case PromptRemoveOverride, PromptRemoveMCP, PromptRemoveAccount, PromptRemoveRule, PromptRemoveAccountOverride:
		return "remove"
	case PromptNewWorkspace:
		return "next"
	case PromptNewShell:
		return "open"
	case PromptNewSession:
		return "start"
	case PromptResume:
		return "resume"
	case PromptCloseShell, PromptCloseProject:
		return "close"
	case PromptNewStory, PromptNewProject:
		return "create"
	case PromptRename, PromptRenameShell:
		return "rename"
	case PromptSettingText, PromptAccountText, PromptComment:
		return "save"
	case PromptRuleOwner:
		return "next"
	case PromptLogin:
		return "log in"
	case PromptClearSent:
		return "clear"
	case PromptSyncBusy:
		return "sync"
	case PromptExplain:
		return "explain"
	}
	return "yes"
}
