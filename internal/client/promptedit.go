package client

import (
	"os"

	tea "charm.land/bubbletea/v2"

	"github.com/dinhhuy258/bx/internal/ui"
)

// promptEditedMsg is $EDITOR closing on a prompt's text, written to path.
type promptEditedMsg struct {
	kind ui.PromptKind
	path string
	err  error
}

// editPrompt opens the prompt's text in $EDITOR (C-g); the prompt stays open and takes the
// edited text back when the editor closes.
func (m *Model) editPrompt() tea.Cmd {
	path, err := writeTemp("bx-prompt-*.txt", m.prompt.Input)
	if err != nil {
		m.setFlash("editor: " + err.Error())
		return nil
	}
	kind := m.prompt.Kind
	return m.edit(path, 0, func(err error) tea.Msg { return promptEditedMsg{kind, path, err} })
}

func (m *Model) gotPromptEdited(msg promptEditedMsg) {
	data, rerr := os.ReadFile(msg.path)
	_ = os.Remove(msg.path)
	switch {
	case msg.err != nil:
		m.setFlash("editor: " + msg.err.Error())
	case rerr != nil:
		m.setFlash("editor: " + rerr.Error())
	case m.prompt.Active() && m.prompt.Kind == msg.kind:
		m.prompt.SetInput(string(data))
	}
}
