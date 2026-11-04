package client

import (
	"errors"
	"fmt"
	"slices"
	"time"

	tea "charm.land/bubbletea/v2" // use the same import path model.go uses for Bubble Tea

	"github.com/dinhhuy258/bx/internal/api"
	"github.com/dinhhuy258/bx/internal/ui"
)

// openReview opens the review screen of a story and loads it.
func (m *Model) openReview(story string) tea.Cmd {
	m.review = ui.NewReview(story)
	m.flash = ""
	return m.loadReview(story)
}

func (m *Model) loadReview(story string) tea.Cmd {
	be, ctx, project := m.be, m.ctx, m.project
	return func() tea.Msg {
		r, err := be.Review(ctx, project, story)
		return reviewMsg{story: story, r: r, err: err}
	}
}

// gotReview shows a load or a save. A save refused because another device saved first
// reloads the review.
func (m *Model) gotReview(msg reviewMsg) tea.Cmd {
	if m.review == nil || m.review.Story != msg.story {
		return nil
	}
	var se *StatusError
	switch {
	case errors.As(msg.err, &se) && se.Code == 409 && !msg.explain:
		m.setFlash("comments changed on another device · reloaded")
		return m.loadReview(msg.story)
	case msg.err != nil:
		m.setFlash("error: " + msg.err.Error())
		return nil
	}
	m.review.SetData(msg.r)
	if msg.done != "" {
		m.setFlash(msg.done)
	} else if m.flash == "refreshing…" {
		m.flash = ""
	}
	return m.pollReview()
}

// pollReview reloads the review in 2 s while an explanation runs, once at a time.
func (m *Model) pollReview() tea.Cmd {
	if m.review == nil || m.reviewPolling || !slices.ContainsFunc(m.review.Data().Explains, api.Explain.Running) {
		return nil
	}
	m.reviewPolling = true
	story := m.review.Story
	if m.reviewTick != nil {
		return m.reviewTick(story)
	}
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return reviewPollMsg{story} })
}

func (m *Model) reviewKey(key string) tea.Cmd {
	r := m.review
	switch r.Key(key, m.narrow) {
	case ui.ReviewBack:
		m.review, m.flash = nil, ""
	case ui.ReviewRefresh:
		m.setFlash("refreshing…")
		return m.loadReview(r.Story)
	case ui.ReviewComment:
		l, _ := r.Selected()
		m.reviewLine = l
		title := fmt.Sprintf("Comment · %s:%s", l.File, l.Lines())
		switch {
		case l.Side == "old" && l.StartLine > 0:
			title += " (removed lines)"
		case l.Side == "old":
			title += " (removed line)"
		}
		initial := ""
		if l.Edit != nil {
			initial = l.Edit.Body
		}
		m.prompt.OpenComment(title, l.Code, initial)
		m.flash = ""
	case ui.ReviewExplain:
		l, _ := r.Selected()
		m.reviewLine = l
		title := fmt.Sprintf("Explain · %s:%s", l.File, l.Lines())
		switch {
		case l.Side == "old" && l.StartLine > 0:
			title += " (removed lines)"
		case l.Side == "old":
			title += " (removed line)"
		}
		labels, idx := m.explainConvs()
		m.prompt.OpenExplain(title, l.Code, labels, idx)
		m.flash = ""
	case ui.ReviewExplainDelete:
		if e := r.SelectedExplain(); e != nil {
			return m.deleteExplain(e.ID)
		}
	case ui.ReviewDelete:
		l, _ := r.Selected()
		return m.saveComments(without(r.Data().Comments, l.Edit.ID), "")
	case ui.ReviewSend:
		m.openSendPicker()
	case ui.ReviewClearSent:
		m.ask(ui.PromptClearSent, r.Story, fmt.Sprintf("Clear %s?", plural(r.SentCount(), "sent comment", "sent comments")),
			"They leave the review for good.", "Unsent comments stay.")
	}
	return nil
}

func without(cs []api.Comment, id string) []api.Comment {
	return slices.DeleteFunc(slices.Clone(cs), func(c api.Comment) bool { return c.ID == id })
}

// commentDone saves the comment form: a new comment, an edit, or a delete when it is empty.
func (m *Model) commentDone(text string) tea.Cmd {
	if m.review == nil {
		return nil
	}
	l, cs := m.reviewLine, slices.Clone(m.review.Data().Comments)
	switch {
	case l.Edit != nil && text == "":
		return m.saveComments(without(cs, l.Edit.ID), "")
	case l.Edit != nil:
		for i := range cs {
			if cs[i].ID == l.Edit.ID {
				cs[i].Body = text
			}
		}
		return m.saveComments(cs, "")
	case text == "":
		return nil
	}
	cs = append(cs, api.Comment{File: l.File, Side: l.Side, StartLine: l.StartLine, Line: l.Line, Code: l.Code, Body: text})
	return m.saveComments(cs, "")
}

// saveComments sends the whole list at the version it was read at.
func (m *Model) saveComments(cs []api.Comment, done string) tea.Cmd {
	be, ctx, project, story := m.be, m.ctx, m.project, m.review.Story
	body := api.ReviewComments{Version: m.review.Data().Version, Comments: cs}
	return func() tea.Msg {
		r, err := be.SaveReview(ctx, project, story, body)
		return reviewMsg{story: story, r: r, err: err, done: done}
	}
}

// explainConvs lists the conversations an explanation can use: the story's sessions, newest
// first, then none. It fills reviewConvs and returns their labels and the one the story's last
// ask used (the first when that is gone).
func (m *Model) explainConvs() ([]string, int) {
	nums := m.m.P.SessionsOf(m.review.Story)
	m.reviewConvs = nil
	var labels []string
	for i := len(nums) - 1; i >= 0; i-- {
		n, s := nums[i], m.m.P.Sessions[nums[i]]
		title := s.Title
		if title == "" {
			title = "untitled"
		}
		state := "running"
		if s.Done || m.m.Gone(n) {
			state = "done"
		}
		labels = append(labels, fmt.Sprintf("[%d] %s · %s", n, title, state))
		m.reviewConvs = append(m.reviewConvs, n)
	}
	labels = append(labels, "Code only (no conversation)")
	m.reviewConvs = append(m.reviewConvs, api.ExplainCodeOnly)
	return labels, max(slices.Index(m.reviewConvs, m.review.Data().ExplainWith), 0)
}

// explainDone asks Claude about the lines the form was opened on, in conversation number conv
// of reviewConvs.
func (m *Model) explainDone(question string, conv int) tea.Cmd {
	if m.review == nil || conv < 0 || conv >= len(m.reviewConvs) {
		return nil
	}
	l := m.reviewLine
	req := api.ExplainRequest{File: l.File, Side: l.Side, StartLine: l.StartLine, Line: l.Line, Code: l.Code,
		Question: question, Session: m.reviewConvs[conv]}
	be, ctx, project, story := m.be, m.ctx, m.project, m.review.Story
	return func() tea.Msg {
		r, err := be.Explain(ctx, project, story, req)
		return reviewMsg{story: story, r: r, err: err, done: "explaining…", explain: true}
	}
}

// deleteExplain stops a running explanation or dismisses an answer.
func (m *Model) deleteExplain(id string) tea.Cmd {
	be, ctx, project, story := m.be, m.ctx, m.project, m.review.Story
	return func() tea.Msg {
		r, err := be.DeleteExplain(ctx, project, story, id)
		return reviewMsg{story: story, r: r, err: err, explain: true}
	}
}

func (m *Model) clearSent(story string) tea.Cmd {
	be, ctx, project := m.be, m.ctx, m.project
	return func() tea.Msg {
		r, err := be.ClearSent(ctx, project, story)
		return reviewMsg{story: story, r: r, err: err, done: "sent comments cleared"}
	}
}

// openSendPicker lists the story's sessions, newest first, then a new session. A done or gone
// session is resumed by the server.
func (m *Model) openSendPicker() {
	r := m.review
	nums := m.m.P.SessionsOf(r.Story)
	m.reviewSend = map[string]int{}
	var items []string
	for i := len(nums) - 1; i >= 0; i-- {
		n, s := nums[i], m.m.P.Sessions[nums[i]]
		title := s.Title
		if title == "" {
			title = "untitled"
		}
		state := "running"
		if s.Done || m.m.Gone(n) {
			state = "done · resumes"
		}
		label := fmt.Sprintf("[%d] %s · %s", n, title, state)
		items = append(items, label)
		m.reviewSend[label] = n
	}
	items = append(items, "+ New session")
	m.reviewSend["+ New session"] = 0
	title := "send " + plural(r.Unsent(), "comment", "comments") + " · pasted, not submitted"
	if n := r.SentCount(); n > 0 {
		title += fmt.Sprintf(" · %d already sent, not included", n)
	}
	m.picker, m.pickKind = ui.NewPicker(title, items), pickReviewSend
}

// sendReview asks the server to paste the unsent comments into session num (0 = a new one).
func (m *Model) sendReview(num int) tea.Cmd {
	if m.review == nil {
		return nil
	}
	be, ctx, project, story := m.be, m.ctx, m.project, m.review.Story
	req := api.ReviewSend{Session: num, New: num == 0}
	if num == 0 {
		m.setFlash("starting a session for the comments…")
	} else {
		m.setFlash(fmt.Sprintf("sending to [%d]…", num))
	}
	return func() tea.Msg {
		res, err := be.SendReview(ctx, project, story, req)
		return reviewSentMsg{story: story, res: res, err: err}
	}
}

// gotReviewSent shows the outcome and reloads: sent comments are stamped, and a failed send
// may still have started a session.
func (m *Model) gotReviewSent(msg reviewSentMsg) tea.Cmd {
	if msg.err != nil {
		m.setFlash("error: " + msg.err.Error())
	} else {
		m.setFlash(msg.res.Message)
	}
	if m.review == nil || m.review.Story != msg.story {
		return nil
	}
	return m.loadReview(msg.story)
}
