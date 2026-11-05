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
	appsProjectsMsg struct {
		list []api.RunConfigs
		err  error
	}
	appsPageMsg struct {
		project string
		rc      api.RunConfig
		err     error
	}
	appsSavedMsg struct {
		project string
		rc      api.RunConfig
		err     error
	}
	appsTextMsg struct {
		project, text string
		err           error
	}
	appsEditedMsg struct {
		project, path, orig string
		err                 error
	}
	appsTextSavedMsg struct {
		project, text, orig string
		res                 api.Result
		err                 error
	}
)

// openAppsSettings opens settings on the apps page of project.
func (m *Model) openAppsSettings(project string) tea.Cmd {
	cmd := m.openSettings()
	m.appsOpen = project // gotSettings opens the section once it exists
	return tea.Batch(cmd, m.loadAppsProjects(), m.loadAppsPage(project))
}

func (m *Model) loadAppsProjects() tea.Cmd {
	be, ctx := m.be, m.ctx
	return func() tea.Msg {
		list, err := be.RunConfigs(ctx)
		return appsProjectsMsg{list, err}
	}
}

func (m *Model) loadAppsPage(project string) tea.Cmd {
	be, ctx := m.be, m.ctx
	return func() tea.Msg {
		rc, err := be.RunConfig(ctx, project)
		return appsPageMsg{project, rc, err}
	}
}

func (m *Model) gotAppsProjects(msg appsProjectsMsg) {
	if m.settings == nil {
		return
	}
	if msg.err != nil {
		m.setFlash("apps: " + msg.err.Error())
		return
	}
	ps := make([]ui.AppsProject, len(msg.list))
	for i, p := range msg.list {
		ps[i] = ui.AppsProject{Project: p.Project, Runs: p.Runs}
	}
	m.settings.SetAppsProjects(ps)
	if m.appsOpen != "" && m.settings.Loaded() {
		m.settings.ShowApps(m.appsOpen)
	}
}

func (m *Model) gotAppsPage(msg appsPageMsg) {
	if m.settings == nil {
		return
	}
	if msg.err != nil {
		m.setFlash("apps: " + msg.err.Error())
		return
	}
	if m.appsOpen != "" { // opened from M-b: wait for the sections, then show the page
		if !m.settings.Loaded() {
			m.appsPending = &msg
			return
		}
		m.settings.ShowApps(m.appsOpen)
		m.appsOpen = ""
	}
	m.settings.SetAppsPage(msg.project, appsConfig(msg.rc))
}

func appsConfig(rc api.RunConfig) ui.AppsConfig {
	return ui.AppsConfig{Runs: rc.Runs, Hide: rc.Hide, Show: rc.Show, Repos: rc.Repos, HasComments: rc.HasComments, Error: rc.Error, Errors: rc.Errors}
}

// saveApps sends the page's config.
func (m *Model) saveApps() tea.Cmd {
	project, c := m.settings.AppsProject(), m.settings.AppsSave()
	be, ctx := m.be, m.ctx
	return func() tea.Msg {
		rc, err := be.SaveRunConfig(ctx, project, api.RunConfig{Runs: c.Runs, Hide: c.Hide, Show: c.Show})
		return appsSavedMsg{project, rc, err}
	}
}

func (m *Model) gotAppsSaved(msg appsSavedMsg) tea.Cmd {
	if m.settings == nil {
		return nil
	}
	if msg.err != nil {
		cfg := m.settings.AppsSave()
		cfg.Errors = msg.rc.Errors
		m.settings.SetAppsPage(msg.project, cfg)
		m.settings.SetRunFormErrors(msg.rc.Errors) // Task 19: the form stays open with them
		m.setFlash(msg.project + ": run config not saved · " + msg.err.Error())
		return nil
	}
	m.settings.CloseRunForm()
	m.settings.SetAppsPage(msg.project, appsConfig(msg.rc))
	m.setFlash(msg.project + ": run config saved · the next run uses it")
	return m.loadAppsProjects()
}

// editAppsText opens the whole file in $EDITOR: its draft if a save was refused.
func (m *Model) editAppsText(project string) tea.Cmd {
	if d, ok := m.appsDrafts[project]; ok {
		return m.openAppsEditor(project, d.text, d.orig, d.line)
	}
	be, ctx := m.be, m.ctx
	return func() tea.Msg {
		text, err := be.RunConfigText(ctx, project)
		return appsTextMsg{project, text, err}
	}
}

func (m *Model) gotAppsText(msg appsTextMsg) tea.Cmd {
	if msg.err != nil {
		m.setFlash("apps: " + msg.err.Error())
		return nil
	}
	return m.openAppsEditor(msg.project, msg.text, msg.text, 0)
}

func (m *Model) openAppsEditor(project, text, orig string, line int) tea.Cmd {
	path, err := writeTemp("bx-run-*.toml", text)
	if err != nil {
		m.setFlash("apps: " + err.Error())
		return nil
	}
	return m.edit(path, line, func(err error) tea.Msg { return appsEditedMsg{project, path, orig, err} })
}

func (m *Model) gotAppsEdited(msg appsEditedMsg) tea.Cmd {
	data, rerr := os.ReadFile(msg.path)
	_ = os.Remove(msg.path)
	switch {
	case msg.err != nil:
		m.setFlash("editor: " + msg.err.Error())
		return nil
	case rerr != nil:
		m.setFlash("apps: " + rerr.Error())
		return nil
	case string(data) == msg.orig:
		delete(m.appsDrafts, msg.project)
		m.setFlash(msg.project + ": no changes")
		return nil
	}
	text, orig, project := string(data), msg.orig, msg.project
	be, ctx := m.be, m.ctx
	return func() tea.Msg {
		res, err := be.SaveRunConfigText(ctx, project, text)
		return appsTextSavedMsg{project: project, text: text, orig: orig, res: res, err: err}
	}
}

// gotAppsTextSaved keeps a refused text as the project's draft; a save reloads the page.
func (m *Model) gotAppsTextSaved(msg appsTextSavedMsg) tea.Cmd {
	if msg.err != nil {
		m.appsDrafts[msg.project] = mcpDraft{text: msg.text, orig: msg.orig, line: msg.res.Line}
		m.setFlash(msg.project + ": run config not saved · " + msg.err.Error() + " · e reopens your draft")
		return nil
	}
	delete(m.appsDrafts, msg.project)
	m.setFlash(msg.project + ": run config saved · the next run uses it")
	return tea.Batch(m.loadAppsPage(msg.project), m.loadAppsProjects())
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

// appsResult handles what a key in the apps section asked for.
func (m *Model) appsResult(r ui.SettingsResult) tea.Cmd {
	s := m.settings
	switch r {
	case ui.SettingsAppsOpen:
		return m.loadAppsPage(s.AppsProject())
	case ui.SettingsAppsEditText:
		return m.editAppsText(s.AppsProject())
	case ui.SettingsAppsRemove:
		if i, ok := s.AppsRun(); ok {
			m.ask(ui.PromptRemoveRun, strconv.Itoa(i), "Remove run "+s.AppsSave().Runs[i].Name+"?", "The run config is saved without it.")
		}
	case ui.SettingsAppsPorts:
		which, ps := s.AppsPortsRow()
		title := "Hidden ports (empty = the defaults)"
		if which == "show" {
			title = "Ports to list even though hidden"
		}
		m.openPrompt(ui.PromptAppsPorts, which, title, portText(ps))
	case ui.SettingsAppsSave:
		return m.saveApps()
	case ui.SettingsAppsText, ui.SettingsAppsBuild, ui.SettingsAppsForm:
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

func (m *Model) runFormResult(ui.SettingsResult) tea.Cmd { return nil }
