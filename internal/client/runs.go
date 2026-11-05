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
	// runConfigMsg is a project's run names, read when the run menu opens.
	runConfigMsg struct {
		project, story string
		runs           []string
		err            error
	}
	// runMsg is what a run action did.
	runMsg struct {
		project, story, action string
		runs                   []string
		res                    api.Result
		err                    error
	}
)

// openRunMenu reads the project's run names, then shows the run menu of story.
func (m *Model) openRunMenu(story string) tea.Cmd {
	if story == "" {
		m.setFlash("runs start from a story · select one")
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
		return runConfigMsg{project, story, runs, err}
	}
}

// gotRunConfig builds the menu: links and stop items while the story runs, run items when not.
func (m *Model) gotRunConfig(msg runConfigMsg) tea.Cmd {
	switch {
	case msg.project != m.project:
		return nil
	case msg.err != nil:
		m.setFlash("error: " + msg.err.Error())
		return nil
	}
	story := msg.story
	m.runStory, m.runNames = story, msg.runs
	items := map[string]func() tea.Cmd{}
	var labels []string
	add := func(label string, run func() tea.Cmd) {
		labels = append(labels, label)
		items[label] = run
	}
	edit := func() tea.Cmd { return m.openRunsSettings(m.project) }
	if m.m.Live.Running == story {
		for _, p := range m.m.Live.Ports[story] {
			if u := p.URL; u != "" {
				add("Open "+u, func() tea.Cmd { m.setFlash(u); return m.browse(u) })
			}
		}
		add("Restart", func() tea.Cmd { return m.restartRuns(story) })
		add("Stop", func() tea.Cmd { return m.runAction(story, "stop", nil, false, false) })
		if len(msg.runs) > 0 {
			add("Setup again…", func() tea.Cmd { return m.pickOneRun(story, "setup", msg.runs) })
		}
	} else if len(msg.runs) > 0 {
		add("Run all", func() tea.Cmd { return m.runAction(story, "run", nil, false, false) })
		if len(msg.runs) > 1 {
			add("Run one…", func() tea.Cmd { return m.pickOneRun(story, "run", msg.runs) })
		}
	}
	add("Edit run config", edit)
	m.runItems = items
	m.picker, m.pickKind = ui.NewPicker("runs · "+story, labels), pickRun
	return nil
}

// pickOneRun opens the picker of one run, for Run one… or Setup again….
func (m *Model) pickOneRun(story, what string, runs []string) tea.Cmd {
	m.runStory, m.runPickFor = story, what
	title := "run one · " + story
	if what == "setup" {
		title = "setup again · " + story
	}
	m.picker, m.pickKind = ui.NewPicker(title, runs), pickOneRun
	return nil
}

// runAction sends a run action in the background: run, stop or setup of story's runs.
func (m *Model) runAction(story, action string, runs []string, replace, setup bool) tea.Cmd {
	if story == "" {
		m.setFlash("runs start from a story · select one")
		return nil
	}
	if m.busy != "" {
		return nil
	}
	be, project := m.be, m.project
	verb := map[string]string{"run": "starting", "stop": "stopping", "setup": "setting up"}[action]
	ctx := m.startBusy(fmt.Sprintf("%s runs of %s…", verb, story))
	return func() tea.Msg {
		res, err := be.RunAction(ctx, project, action, api.Action{Story: story, Runs: runs, Replace: replace, Setup: setup})
		return runMsg{project, story, action, runs, res, err}
	}
}

// restartRuns stops the story's runs and starts them again.
func (m *Model) restartRuns(story string) tea.Cmd {
	be, project := m.be, m.project
	ctx := m.startBusy("restarting runs of " + story + "…")
	return func() tea.Msg {
		if res, err := be.RunAction(ctx, project, "stop", api.Action{Story: story}); err != nil {
			return runMsg{project, story, "stop", nil, res, err}
		}
		res, err := be.RunAction(ctx, project, "run", api.Action{Story: story})
		return runMsg{project, story, "run", nil, res, err}
	}
}

// gotRun flashes what an action did, or asks to stop the story that runs.
func (m *Model) gotRun(msg runMsg) tea.Cmd {
	m.stopBusy()
	var se *StatusError
	switch {
	case errors.Is(msg.err, context.Canceled):
		m.setFlash("cancelled")
		return nil
	case errors.As(msg.err, &se) && se.Code == 409 && msg.res.RunStory != "" && msg.project == m.project:
		m.replaceRuns = msg.runs
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
			runs = m.runNames
		}
		if len(runs) == 0 { // run_story: no menu read the names
			m.setFlash(msg.story + ": starting its runs")
		} else {
			m.setFlash(fmt.Sprintf("%s: starting %s", msg.story, strings.Join(runs, ", ")))
		}
		m.runUp = msg.story
	case "stop":
		m.setFlash(msg.story + ": runs stopped")
	}
	if msg.project != m.project {
		return nil
	}
	return m.poll()
}

// checkRunUp flashes "s1 is up · <link>" once a story just started shows its first port.
func (m *Model) checkRunUp() {
	if m.runUp == "" {
		return
	}
	for _, p := range m.m.Live.Ports[m.runUp] {
		if p.URL != "" {
			m.setFlash(fmt.Sprintf("%s is up · %s", m.runUp, p.URL))
			m.runUp = ""
			return
		}
	}
}
