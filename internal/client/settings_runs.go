package client

import (
	"errors"
	"os"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/dinhhuy258/bx/internal/api"
	"github.com/dinhhuy258/bx/internal/ui"
)

type (
	runsProjectsMsg struct {
		list []api.RunConfigs
		err  error
	}
	runsPageMsg struct {
		project string
		rc      api.RunConfig
		err     error
	}
	runsSavedMsg struct {
		project string
		rc      api.RunConfig
		err     error
	}
	runsTextMsg struct {
		project, text string
		err           error
	}
	runsEditedMsg struct {
		project, path, orig string
		err                 error
	}
	runsTextSavedMsg struct {
		project, text, orig string
		res                 api.Result
		err                 error
	}
)

// openRunsSettings opens settings on the runs page of project.
func (m *Model) openRunsSettings(project string) tea.Cmd {
	cmd := m.openSettings()
	m.runsOpen = project // gotSettings opens the section once it exists
	return tea.Batch(cmd, m.loadRunsProjects(), m.loadRunsPage(project))
}

func (m *Model) loadRunsProjects() tea.Cmd {
	be, ctx := m.be, m.ctx
	return func() tea.Msg {
		list, err := be.RunConfigs(ctx)
		return runsProjectsMsg{list, err}
	}
}

func (m *Model) loadRunsPage(project string) tea.Cmd {
	be, ctx := m.be, m.ctx
	return func() tea.Msg {
		rc, err := be.RunConfig(ctx, project)
		return runsPageMsg{project, rc, err}
	}
}

func (m *Model) gotRunsProjects(msg runsProjectsMsg) {
	if m.settings == nil {
		return
	}
	if msg.err != nil {
		m.setFlash("runs: " + msg.err.Error())
		return
	}
	ps := make([]ui.RunsProject, len(msg.list))
	for i, p := range msg.list {
		ps[i] = ui.RunsProject{Project: p.Project, Runs: p.Runs}
	}
	m.settings.SetRunsProjects(ps)
	if m.runsOpen != "" && m.settings.Loaded() {
		m.settings.ShowRuns(m.runsOpen)
	}
}

func (m *Model) gotRunsPage(msg runsPageMsg) {
	if m.settings == nil {
		return
	}
	if msg.err != nil {
		m.setFlash("runs: " + msg.err.Error())
		return
	}
	if m.runsOpen != "" { // opened from M-b: wait for the sections, then show the page
		if !m.settings.Loaded() {
			m.runsPending = &msg
			return
		}
		m.settings.ShowRuns(m.runsOpen)
		m.runsOpen = ""
	}
	m.settings.SetRunsPage(msg.project, runsConfig(msg.rc))
}

func runsConfig(rc api.RunConfig) ui.RunsConfig {
	return ui.RunsConfig{Runs: rc.Runs, Hide: rc.Hide, Show: rc.Show, Repos: rc.Repos, Compose: rc.Compose, HasComments: rc.HasComments, Error: rc.Error, Errors: rc.Errors}
}

// saveRuns sends the page's config.
func (m *Model) saveRuns() tea.Cmd {
	project, c := m.settings.RunsProject(), m.settings.RunsSave()
	be, ctx := m.be, m.ctx
	return func() tea.Msg {
		rc, err := be.SaveRunConfig(ctx, project, api.RunConfig{Runs: c.Runs, Hide: c.Hide, Show: c.Show})
		return runsSavedMsg{project, rc, err}
	}
}

func (m *Model) gotRunsSaved(msg runsSavedMsg) tea.Cmd {
	if m.settings == nil {
		return nil
	}
	if msg.err != nil {
		cfg := m.settings.RunsSave()
		cfg.Errors = msg.rc.Errors
		m.settings.SetRunsPage(msg.project, cfg)
		m.settings.SetRunFormErrors(msg.rc.Errors) // Task 19: the form stays open with them
		m.setFlash(msg.project + ": run config not saved · " + msg.err.Error())
		return nil
	}
	m.settings.CloseRunForm()
	m.settings.SetRunsPage(msg.project, runsConfig(msg.rc))
	m.setFlash(msg.project + ": run config saved · the next run uses it")
	return m.loadRunsProjects()
}

// editRunsText opens the whole file in $EDITOR: its draft if a save was refused.
func (m *Model) editRunsText(project string) tea.Cmd {
	if d, ok := m.runsDrafts[project]; ok {
		return m.openRunsEditor(project, d.text, d.orig, d.line)
	}
	be, ctx := m.be, m.ctx
	return func() tea.Msg {
		text, err := be.RunConfigText(ctx, project)
		return runsTextMsg{project, text, err}
	}
}

func (m *Model) gotRunsText(msg runsTextMsg) tea.Cmd {
	if msg.err != nil {
		m.setFlash("runs: " + msg.err.Error())
		return nil
	}
	return m.openRunsEditor(msg.project, msg.text, msg.text, 0)
}

func (m *Model) openRunsEditor(project, text, orig string, line int) tea.Cmd {
	path, err := writeTemp("bx-run-*.toml", text)
	if err != nil {
		m.setFlash("runs: " + err.Error())
		return nil
	}
	return m.edit(path, line, func(err error) tea.Msg { return runsEditedMsg{project, path, orig, err} })
}

func (m *Model) gotRunsEdited(msg runsEditedMsg) tea.Cmd {
	data, rerr := os.ReadFile(msg.path)
	_ = os.Remove(msg.path)
	switch {
	case msg.err != nil:
		m.setFlash("editor: " + msg.err.Error())
		return nil
	case rerr != nil:
		m.setFlash("runs: " + rerr.Error())
		return nil
	case string(data) == msg.orig:
		delete(m.runsDrafts, msg.project)
		m.setFlash(msg.project + ": no changes")
		return nil
	}
	text, orig, project := string(data), msg.orig, msg.project
	be, ctx := m.be, m.ctx
	return func() tea.Msg {
		res, err := be.SaveRunConfigText(ctx, project, text)
		return runsTextSavedMsg{project: project, text: text, orig: orig, res: res, err: err}
	}
}

// gotRunsTextSaved keeps a refused text as the project's draft; a save reloads the page.
func (m *Model) gotRunsTextSaved(msg runsTextSavedMsg) tea.Cmd {
	if msg.err != nil {
		m.runsDrafts[msg.project] = mcpDraft{text: msg.text, orig: msg.orig, line: msg.res.Line}
		m.setFlash(msg.project + ": run config not saved · " + msg.err.Error() + " · e reopens your draft")
		return nil
	}
	delete(m.runsDrafts, msg.project)
	m.setFlash(msg.project + ": run config saved · the next run uses it")
	return tea.Batch(m.loadRunsPage(msg.project), m.loadRunsProjects())
}

// writeTemp writes text to a new temp file only this user can read.
func writeTemp(pattern, text string) (string, error) {
	f, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", err
	}
	_, err = f.WriteString(text)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// runsResult handles what a key in the runs section asked for.
func (m *Model) runsResult(r ui.SettingsResult) tea.Cmd {
	s := m.settings
	switch r {
	case ui.SettingsRunsOpen:
		return m.loadRunsPage(s.RunsProject())
	case ui.SettingsRunsEditText:
		return m.editRunsText(s.RunsProject())
	case ui.SettingsRunsRemove:
		if i, ok := s.PageRun(); ok {
			m.ask(ui.PromptRemoveRun, strconv.Itoa(i), "Remove run "+s.RunsSave().Runs[i].Name+"?", "The run config is saved without it.")
		}
	case ui.SettingsRunsPorts:
		which, ps := s.RunsPortsRow()
		title := "Hidden ports (empty = the defaults)"
		if which == "show" {
			title = "Ports to list even though hidden"
		}
		m.openPrompt(ui.PromptRunsPorts, which, title, portText(ps))
	case ui.SettingsRunsSave:
		return m.saveRuns()
	case ui.SettingsRunsText, ui.SettingsRunsBuild, ui.SettingsRunsForm, ui.SettingsRunsDiscard:
		return m.runFormResult(r) // Task 19
	}
	return nil
}

func portText(ps []int) string {
	var out []string
	for _, p := range ps {
		out = append(out, strconv.Itoa(p))
	}
	return strings.Join(out, " ")
}

// errBadPorts is the flash of a port list that does not read.
var errBadPorts = errors.New("ports are numbers from 1 to 65535, space separated")

type runsBuildMsg struct {
	path, orig string
	err        error
}

// runFormResult handles the run form's requests: a text field, the Dockerfile, discard.
func (m *Model) runFormResult(r ui.SettingsResult) tea.Cmd {
	s := m.settings
	switch r {
	case ui.SettingsRunsText:
		if k, label, v, ok := s.RunsFormText(); ok {
			m.openPrompt(ui.PromptRunText, strconv.Itoa(k), label, v)
		}
	case ui.SettingsRunsBuild:
		text := s.RunsBuild()
		if text == "" {
			text = "FROM debian:bookworm-slim\n"
		}
		path, err := writeTemp("Dockerfile-*", text)
		if err != nil {
			m.setFlash("runs: " + err.Error())
			return nil
		}
		orig := s.RunsBuild()
		return m.edit(path, 0, func(err error) tea.Msg { return runsBuildMsg{path, orig, err} })
	case ui.SettingsRunsDiscard:
		m.ask(ui.PromptDiscardRun, "", "Drop the changes to this run?")
	}
	return nil
}

func (m *Model) gotRunsBuild(msg runsBuildMsg) {
	data, rerr := os.ReadFile(msg.path)
	_ = os.Remove(msg.path)
	switch {
	case msg.err != nil:
		m.setFlash("editor: " + msg.err.Error())
	case rerr != nil:
		m.setFlash("runs: " + rerr.Error())
	case m.settings != nil:
		m.settings.SetRunsBuild(string(data))
	}
}
