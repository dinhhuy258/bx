package client

import (
	"fmt"
	"strconv"

	tea "charm.land/bubbletea/v2"

	"github.com/dinhhuy258/bx/internal/api"
	"github.com/dinhhuy258/bx/internal/ui"
)

// openPR opens the PR screen of a story, on the PR of repo when it has one, and loads it.
func (m *Model) openPR(story, repo string) tea.Cmd {
	m.pr = ui.NewPRScreen(story, repo)
	m.flash = ""
	return m.loadPR(story, false)
}

// loadPR reads the PRs of a story; refresh asks the server to look at GitHub first.
func (m *Model) loadPR(story string, refresh bool) tea.Cmd {
	be, ctx, project := m.be, m.ctx, m.project
	return func() tea.Msg {
		var d api.PRData
		var err error
		if refresh {
			d, err = be.RefreshPR(ctx, project, story)
		} else {
			d, err = be.PR(ctx, project, story)
		}
		return prMsg{story: story, d: d, err: err}
	}
}

func (m *Model) gotPR(msg prMsg) tea.Cmd {
	if m.pr == nil || m.pr.Story != msg.story {
		return nil
	}
	if msg.err != nil {
		m.setFlash("error: " + msg.err.Error())
		return nil
	}
	m.pr.SetData(msg.d)
	if m.flash == "refreshing…" {
		m.flash = ""
	}
	return nil
}

func (m *Model) prKey(key string) tea.Cmd {
	s := m.pr
	switch s.Key(key) {
	case ui.PRBack:
		m.pr, m.flash = nil, ""
	case ui.PRRefresh:
		m.setFlash("refreshing…")
		return m.loadPR(s.Story, true)
	case ui.PRSync:
		return m.syncStory(s.Story, false) // the server looks at the PR again after a sync
	case ui.PROpenWeb:
		if c, _ := s.Selected(); c != nil && c.URL != "" {
			return m.browse(c.URL)
		}
		if pr, ok := s.Current(); ok && pr.URL != "" {
			return m.browse(pr.URL)
		}
	case ui.PRFix:
		pr, ok := s.Current()
		if !ok || !pr.Failed() {
			m.setFlash("no failed check")
			return nil
		}
		m.prSendReq = api.PRSend{Kind: "fix", Repo: pr.Repo}
		m.openPRSendPicker("Fix CI of #" + strconv.Itoa(pr.Number) + " in…")
	case ui.PRAddress:
		pr, ok := s.Current()
		ids := s.Picked()
		if !ok || len(ids) == 0 {
			m.setFlash("pick a comment first · space")
			return nil
		}
		m.prSendReq = api.PRSend{Kind: "address", Repo: pr.Repo, Threads: ids}
		m.openPRSendPicker(fmt.Sprintf("Address %s of #%d in…", plural(len(ids), "comment", "comments"), pr.Number))
	}
	return nil
}

// openPRSendPicker asks which session gets the prompt; it is pasted and submitted.
func (m *Model) openPRSendPicker(title string) {
	m.picker, m.prSend = m.sessionPicker(m.pr.Story, title)
	m.pickKind = pickPRSend
}

// sendPR asks the server to paste and submit the prompt in m.prSendReq into session num
// (0 = a new one).
func (m *Model) sendPR(num int) tea.Cmd {
	if m.pr == nil {
		return nil
	}
	be, ctx, project, story := m.be, m.ctx, m.project, m.pr.Story
	req := m.prSendReq
	req.Session, req.New = num, num == 0
	if num == 0 {
		m.setFlash("starting a session…")
	} else {
		m.setFlash(fmt.Sprintf("sending to [%d]…", num))
	}
	return func() tea.Msg {
		res, err := be.SendPR(ctx, project, story, req)
		return prSentMsg{story: story, res: res, err: err}
	}
}

// gotPRSent shows the outcome and reloads: sent threads are stamped, and a failed send may
// still have started a session.
func (m *Model) gotPRSent(msg prSentMsg) tea.Cmd {
	switch {
	case msg.err != nil:
		m.setFlash("error: " + msg.err.Error())
	default:
		m.setFlash(msg.res.Message)
	}
	if m.pr == nil || m.pr.Story != msg.story {
		return nil
	}
	return m.loadPR(msg.story, false)
}
