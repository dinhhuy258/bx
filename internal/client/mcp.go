package client

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/dinhhuy258/bx/internal/api"
	"github.com/dinhhuy258/bx/internal/ui"
)

// mcpDraft is a save the server refused: the text as typed, reopened at line by ⏎ until it
// saves or is discarded (⌫, or closing the settings screen). orig is the server's text.
type mcpDraft struct {
	text, orig string
	line       int
}

type (
	mcpMsg struct {
		m   api.MCP
		err error
	}
	mcpDocMsg struct {
		scope, text string
		err         error
	}
	mcpEditedMsg struct {
		scope, path, orig string
		err               error
	}
	mcpSavedMsg struct {
		scope, text string
		res         api.Result
		err         error
		orig        string
	}
)

// lineEditors take +N to open at line N.
var lineEditors = []string{"vi", "vim", "nvim", "nano"}

// openEditor runs $EDITOR (vi when unset) on path in this terminal, the TUI paused.
func openEditor(path string, line int, done func(error) tea.Msg) tea.Cmd {
	args := strings.Fields(os.Getenv("EDITOR"))
	if len(args) == 0 {
		args = []string{"vi"}
	}
	if line > 0 && slices.Contains(lineEditors, filepath.Base(args[0])) {
		args = append(args, fmt.Sprintf("+%d", line))
	}
	args = append(args, path)
	return tea.ExecProcess(exec.Command(args[0], args[1:]...), done)
}

// scopeName names a scope in messages.
func scopeName(scope string) string {
	if scope == "" {
		return "global"
	}
	return scope
}

func (m *Model) loadMCP() tea.Cmd {
	be, ctx := m.be, m.ctx
	return func() tea.Msg {
		x, err := be.MCP(ctx)
		return mcpMsg{m: x, err: err}
	}
}

func (m *Model) gotMCP(msg mcpMsg) {
	if m.settings == nil {
		return
	}
	if msg.err != nil {
		m.setFlash("mcp: " + msg.err.Error())
		return
	}
	m.settings.SetMCP(msg.m)
}

// settingsOnMCPRow reports whether the cursor is on an MCP row, for the key bar.
func (m *Model) settingsOnMCPRow() bool {
	_, ok := m.settings.MCPChoice()
	return ok
}

// editMCP opens scope's servers in the editor: its draft if a save was refused, else the
// server's text.
func (m *Model) editMCP(scope string) tea.Cmd {
	if d, ok := m.mcpDrafts[scope]; ok {
		return m.openMCPEditor(scope, d.text, d.orig, d.line)
	}
	be, ctx := m.be, m.ctx
	return func() tea.Msg {
		d, err := be.MCPDoc(ctx, scope)
		return mcpDocMsg{scope: scope, text: d.Text, err: err}
	}
}

func (m *Model) gotMCPDoc(msg mcpDocMsg) tea.Cmd {
	if msg.err != nil {
		m.setFlash("mcp: " + msg.err.Error())
		return nil
	}
	return m.openMCPEditor(msg.scope, msg.text, msg.text, 0)
}

// openMCPEditor writes text to a temp file only this user can read (os.CreateTemp makes it
// 0600) and hands the terminal to the editor.
func (m *Model) openMCPEditor(scope, text, orig string, line int) tea.Cmd {
	f, err := os.CreateTemp("", "bx-mcp-*.json")
	if err != nil {
		m.setFlash("mcp: " + err.Error())
		return nil
	}
	path := f.Name()
	_, err = f.WriteString(text)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(path)
		m.setFlash("mcp: " + err.Error())
		return nil
	}
	return m.edit(path, line, func(err error) tea.Msg {
		return mcpEditedMsg{scope: scope, path: path, orig: orig, err: err}
	})
}

// gotMCPEdited reads and removes the temp file, then saves what changed.
func (m *Model) gotMCPEdited(msg mcpEditedMsg) tea.Cmd {
	data, rerr := os.ReadFile(msg.path)
	_ = os.Remove(msg.path)
	switch {
	case msg.err != nil:
		m.setFlash("editor: " + msg.err.Error())
		return nil
	case rerr != nil:
		m.setFlash("mcp: " + rerr.Error())
		return nil
	case string(data) == msg.orig:
		m.dropMCPDraft(msg.scope)
		m.setFlash(scopeName(msg.scope) + ": no changes")
		return nil
	}
	return m.saveMCP(msg.scope, string(data), msg.orig)
}

func (m *Model) saveMCP(scope, text, orig string) tea.Cmd {
	be, ctx := m.be, m.ctx
	return func() tea.Msg {
		res, err := be.SaveMCPDoc(ctx, scope, text)
		return mcpSavedMsg{scope: scope, text: text, orig: orig, res: res, err: err}
	}
}

// gotMCPSaved keeps a refused text as the scope's draft; a save reloads the rows.
func (m *Model) gotMCPSaved(msg mcpSavedMsg) tea.Cmd {
	if msg.err != nil {
		if msg.text == "" { // a remove: nothing to keep
			m.setFlash("error: " + msg.err.Error())
			return nil
		}
		m.mcpDrafts[msg.scope] = mcpDraft{text: msg.text, orig: msg.orig, line: msg.res.Line}
		if m.settings != nil {
			m.settings.SetMCPError(msg.scope, msg.err.Error())
		}
		m.setFlash(scopeName(msg.scope) + ": MCP servers not saved · ⏎ reopens your draft")
		return nil
	}
	m.dropMCPDraft(msg.scope)
	if msg.text == "" {
		m.setFlash(scopeName(msg.scope) + ": MCP servers removed")
	} else {
		m.setFlash(scopeName(msg.scope) + ": MCP servers saved · new sessions use them")
	}
	return m.loadMCP()
}

func (m *Model) dropMCPDraft(scope string) {
	delete(m.mcpDrafts, scope)
	if m.settings != nil {
		m.settings.SetMCPError(scope, "")
	}
}

// openMCPProjects opens the picker of the projects without their own servers.
func (m *Model) openMCPProjects() {
	have := m.settings.MCPProjects()
	var items []string
	for _, p := range m.projects {
		if !slices.Contains(have, p.Name) {
			items = append(items, p.Name)
		}
	}
	if len(items) == 0 {
		m.setFlash("every project has MCP servers")
		return
	}
	m.picker, m.pickKind = ui.NewPicker("MCP servers for…", items), pickMCPProject
}

// removeMCPKey is ⌫ on an MCP row: it drops a draft, or asks before removing the servers.
func (m *Model) removeMCPKey() {
	row, _ := m.settings.MCPChoice()
	scope := row.Project
	if _, ok := m.mcpDrafts[scope]; ok {
		m.dropMCPDraft(scope)
		m.setFlash(scopeName(scope) + ": draft discarded")
		return
	}
	n := m.settings.MCPCount(scope)
	if n == 0 {
		return
	}
	what := fmt.Sprintf("%s of %s", plural(n, "server", "servers"), scope)
	if scope == "" {
		what = plural(n, "global server", "global servers") + ", used by every project"
	}
	m.ask(ui.PromptRemoveMCP, scope, "Remove MCP servers?", what, "New sessions start without them.")
}
