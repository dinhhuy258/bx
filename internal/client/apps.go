package client

import (
	"context"
	"errors"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/dinhhuy258/bx/internal/api"
	"github.com/dinhhuy258/bx/internal/ui"
)

type (
	// appConfigMsg is a project's run names, read when the app menu opens.
	appConfigMsg struct {
		project, story string
		runs           []string
		err            error
	}
	// appMsg is what an app action did.
	appMsg struct {
		project, story, action string
		runs                   []string
		res                    api.Result
		err                    error
	}
)

// openAppMenu reads the project's run names, then shows the app menu of story.
func (m *Model) openAppMenu(story string) tea.Cmd {
	if story == "" {
		m.setFlash("apps run in a story · select one")
		return nil
	}
	be, ctx, project := m.be, m.ctx, m.project
	return func() tea.Msg {
		rc, err := be.RunConfig(ctx, project)
		var runs []string
		for _, r := range rc.Runs {
			runs = append(runs, r.Name)
		}
		if err == nil && rc.Error != "" {
			err = errors.New("run config: " + rc.Error)
		}
		return appConfigMsg{project, story, runs, err}
	}
}

// gotAppConfig builds the menu: links and stop items while the story runs, run items when not.
func (m *Model) gotAppConfig(msg appConfigMsg) tea.Cmd {
	switch {
	case msg.project != m.project:
		return nil
	case msg.err != nil:
		m.setFlash("error: " + msg.err.Error())
		return nil
	}
	story := msg.story
	m.appStory, m.appRunNames = story, msg.runs
	items := map[string]func() tea.Cmd{}
	var labels []string
	add := func(label string, run func() tea.Cmd) {
		labels = append(labels, label)
		items[label] = run
	}
	edit := func() tea.Cmd { return m.openAppsSettings(m.project) }
	if m.m.Live.Running == story {
		for _, p := range m.m.Live.Ports[story] {
			if u := p.URL; u != "" {
				add("Open "+u, func() tea.Cmd { m.setFlash(u); return m.browse(u) })
			}
		}
		add("Restart", func() tea.Cmd { return m.restartApps(story) })
		add("Stop", func() tea.Cmd { return m.appAction(story, "stop", nil, false, false) })
		if len(msg.runs) > 0 {
			add("Setup again…", func() tea.Cmd { return m.pickAppRun(story, "setup", msg.runs) })
		}
	} else if len(msg.runs) > 0 {
		add("Run all", func() tea.Cmd { return m.appAction(story, "run", nil, false, false) })
		if len(msg.runs) > 1 {
			add("Run one…", func() tea.Cmd { return m.pickAppRun(story, "run", msg.runs) })
		}
	}
	add("Edit run config", edit)
	m.appItems = items
	m.picker, m.pickKind = ui.NewPicker("apps · "+story, labels), pickApp
	return nil
}

// pickAppRun opens the picker of one run, for Run one… or Setup again….
func (m *Model) pickAppRun(story, what string, runs []string) tea.Cmd {
	m.appStory, m.appRunFor = story, what
	title := "run one · " + story
	if what == "setup" {
		title = "setup again · " + story
	}
	m.picker, m.pickKind = ui.NewPicker(title, runs), pickAppRun
	return nil
}

// appAction sends an app action in the background: run, stop or setup of story's runs.
func (m *Model) appAction(story, action string, runs []string, replace, setup bool) tea.Cmd {
	if story == "" {
		m.setFlash("apps run in a story · select one")
		return nil
	}
	if m.busy != "" {
		return nil
	}
	be, project := m.be, m.project
	verb := map[string]string{"run": "starting", "stop": "stopping", "setup": "setting up"}[action]
	ctx := m.startBusy(fmt.Sprintf("%s apps of %s…", verb, story))
	return func() tea.Msg {
		res, err := be.AppAction(ctx, project, action, api.Action{Story: story, Runs: runs, Replace: replace, Setup: setup})
		return appMsg{project, story, action, runs, res, err}
	}
}

// restartApps stops the story's runs and starts them again.
func (m *Model) restartApps(story string) tea.Cmd {
	be, project := m.be, m.project
	ctx := m.startBusy("restarting apps of " + story + "…")
	return func() tea.Msg {
		if res, err := be.AppAction(ctx, project, "stop", api.Action{Story: story}); err != nil {
			return appMsg{project, story, "stop", nil, res, err}
		}
		res, err := be.AppAction(ctx, project, "run", api.Action{Story: story})
		return appMsg{project, story, "run", nil, res, err}
	}
}

// gotApp flashes what an action did, or asks to stop the story that runs.
func (m *Model) gotApp(msg appMsg) tea.Cmd {
	m.stopBusy()
	var se *StatusError
	switch {
	case errors.Is(msg.err, context.Canceled):
		m.setFlash("cancelled")
		return nil
	case errors.As(msg.err, &se) && se.Code == 409 && msg.res.RunStory != "" && msg.project == m.project:
		m.appRuns = msg.runs
		m.ask(ui.PromptRunReplace, msg.story, fmt.Sprintf("Stop %s and run %s?", msg.res.RunStory, msg.story),
			fmt.Sprintf("%s runs %s.", msg.res.RunStory, strings.Join(msg.res.RunNames, ", ")), "One story of a project runs at a time.")
		return nil
	case msg.err != nil:
		m.setFlash("error: " + msg.err.Error())
		return nil
	}
	switch msg.action {
	case "run", "setup":
		runs := msg.runs
		if len(runs) == 0 {
			runs = m.appNames()
		}
		if len(runs) == 0 { // run_story: no menu read the names
			m.setFlash(msg.story + ": starting its apps")
		} else {
			m.setFlash(fmt.Sprintf("%s: starting %s", msg.story, strings.Join(runs, ", ")))
		}
		m.appUp = msg.story
	case "stop":
		m.setFlash(msg.story + ": apps stopped")
	}
	if msg.project != m.project {
		return nil
	}
	return m.poll()
}

// appNames are the run names read when the menu opened, for the flash of Run all.
func (m *Model) appNames() []string { return m.appRunNames }

// checkAppUp flashes "s1 is up · <link>" once a story just started shows its first port.
func (m *Model) checkAppUp() {
	if m.appUp == "" {
		return
	}
	for _, p := range m.m.Live.Ports[m.appUp] {
		if p.URL != "" {
			m.setFlash(fmt.Sprintf("%s is up · %s", m.appUp, p.URL))
			m.appUp = ""
			return
		}
	}
}

// openAppsSettings is replaced by Task 18.
func (m *Model) openAppsSettings(string) tea.Cmd { return m.openSettings() }
