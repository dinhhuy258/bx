package ui

import (
	"fmt"
	"strings"

	"github.com/dinhhuy258/bx/internal/config"
)

// BarContext tells the key bar which pane has focus and whether a key sequence is pending.
type BarContext struct {
	ClaudeFocused bool
	FocusMode     bool
	Session       int    // the session in the Claude pane; 0 = none
	Seq           string // pending first key of a sequence, "" when none
	Flash         string // message shown instead of the keys
	Narrow        bool   // window too small for two panes: one pane at a time
}

var labels = map[string]string{
	"focus": "focus mode", "waiting": "jump to waiting", "projects": "projects",
	"new_chat": "new chat", "new_session": "new session", "new_story": "new story", "keymap": "keymap",
	"fold_all": "fold all", "yes": "yes", "lazygit": "lazygit", "nvim": "nvim", "shell": "shell", "prs": "PRs",
	"rename": "rename", "resume": "resume", "close_story": "close story",
}

// KeyBar builds the bottom line of the client.
func KeyBar(m *Model, cfg *config.Config, ctx BarContext) string {
	if ctx.Flash != "" {
		return ctx.Flash
	}
	if ctx.Seq != "" {
		return fmt.Sprintf("%s  waiting for the next key · any other key cancels", config.Pretty(ctx.Seq))
	}
	k := func(key, label string) string { return config.Pretty(key) + " " + label }
	lk := func(action string) string {
		if key := cfg.Keys.First(cfg.Keys.List, action); key != "" {
			return k(key, labels[action])
		}
		return ""
	}
	var parts []string
	toggle := config.Pretty(cfg.Client.Toggle)
	if ctx.ClaudeFocused {
		back := toggle + " to list"
		if ctx.FocusMode {
			back = toggle + " exit focus, to list"
		}
		if n := ctx.Session; n != 0 && strings.HasPrefix(m.StateText(n), "asks:") {
			parts = []string{"CLAUDE", "1-3 choose", "↑↓ move", "⏎ confirm", "esc no", back}
		} else {
			parts = []string{"CLAUDE", "type to write · Claude queues it while it works", "⏎ send", "esc interrupt", back}
		}
		return join(parts)
	}
	r := m.Selected()
	parts = append(parts, "j/k move")
	num, _ := m.SelectedSession()
	if r.Kind == RowStory && r.Story != "" {
		if m.IsOpen(r.Story) {
			parts = append(parts, "⏎ collapse")
		} else {
			parts = append(parts, "⏎ expand")
		}
	} else if num != 0 {
		if ctx.Narrow {
			parts = append(parts, "⏎ Claude")
		} else {
			parts = append(parts, "⏎ type in Claude")
		}
	}
	parts = append(parts, lk("fold_all"))
	if num != 0 {
		parts = append(parts, lk("focus"))
		if strings.HasPrefix(m.StateText(num), "asks:") {
			parts = append(parts, lk("yes"))
		}
		if m.Gone(num) {
			parts = append(parts, lk("resume"))
		}
	}
	parts = append(parts, lk("new_chat"), lk("new_session"), lk("new_story"))
	parts = append(parts, lk("keymap"), lk("waiting"), lk("projects"))
	if ctx.Narrow {
		// one pane at a time: say how to come back
		parts = append(parts, toggle+" back")
	}
	return join(parts)
}

// WaitingElsewhere is a waiting session in another project, for the focus line.
type WaitingElsewhere struct {
	Project string
	Num     int
}

// FocusLine is the single status line shown in focus mode.
func FocusLine(m *Model, waitingKey, toggleKey string, runningElsewhere int, waiting []WaitingElsewhere) string {
	num, s := m.SelectedSession()
	left := m.P.Name
	if s != nil {
		left = fmt.Sprintf("%s · [%d] %s · %s", m.P.Name, num, s.Title, m.StateText(num))
	}
	var right []string
	if len(waiting) > 0 {
		w := fmt.Sprintf("◆ %s [%d] needs you", waiting[0].Project, waiting[0].Num)
		if len(waiting) > 1 {
			w += fmt.Sprintf(" +%d", len(waiting)-1)
		}
		right = append(right, w+" · "+config.Pretty(waitingKey)+" jump")
	}
	right = append(right, fmt.Sprintf("● %d running", runningElsewhere), config.Pretty(toggleKey)+" exit")
	return left + "    " + strings.Join(right, " · ")
}

func join(parts []string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, "  ")
}
