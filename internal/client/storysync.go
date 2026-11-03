package client

import (
	"context"
	"errors"
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/dinhhuy258/bx/internal/api"
	"github.com/dinhhuy258/bx/internal/ui"
)

// storySyncMsg is the reply of a sync of story ("" = chats) in project.
type storySyncMsg struct {
	project, story string
	res            api.Result
	err            error
}

// syncStory fast-forwards story ("" = chats) to origin in the background; the key bar says so
// meanwhile and Esc cancels it. force syncs a story Claude works in. One fetch at a time:
// a story being made or another sync keeps the key bar.
func (m *Model) syncStory(story string, force bool) tea.Cmd {
	if m.busy != "" {
		return nil
	}
	if story == "" && m.m.P.IsWorkspace() {
		m.setFlash("a workspace folder is no repository · sync a story instead")
		return nil
	}
	be, project := m.be, m.project
	ctx := m.startBusy(fmt.Sprintf("syncing %s with origin… · esc to cancel", syncName(m, story)))
	return func() tea.Msg {
		res, err := be.Sync(ctx, project, api.Action{Story: story, Force: force})
		return storySyncMsg{project, story, res, err}
	}
}

// gotStorySync flashes what the sync did, or asks before syncing a story Claude works in.
func (m *Model) gotStorySync(msg storySyncMsg) tea.Cmd {
	m.stopBusy()
	switch {
	case errors.Is(msg.err, context.Canceled):
		m.setFlash("cancelled")
		return nil
	case msg.err != nil && msg.res.Running && msg.project == m.project:
		name := groupName(msg.story)
		body := []string{"Files can change under it."}
		if s := m.m.P.Sessions[msg.res.Num]; s != nil {
			body = []string{fmt.Sprintf("[%d] %s is running.", msg.res.Num, s.Title), "Files can change under it."}
		}
		m.ask(ui.PromptSyncBusy, msg.story, "Sync "+name+" while Claude works in it?", body...)
		return nil
	case msg.err != nil:
		m.setFlash("error: " + msg.err.Error())
		return nil
	}
	m.setFlash(msg.res.Message)
	if msg.project != m.project {
		return nil
	}
	return m.poll()
}

// syncName names what a sync of story updates: the story, or the project folder's branch.
func syncName(m *Model, story string) string {
	if story == "" {
		if b := m.m.Live.Branch; b != "" {
			return b
		}
		return "chats"
	}
	return story
}
