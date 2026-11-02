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
	Projects      int    // how many projects the client has open; the switch keys show from two
	Shell         bool   // the pane shows a live shell rather than Claude
}

var labels = map[string]string{
	"focus": "focus mode", "waiting": "jump to waiting", "projects": "projects",
	"new_chat": "new chat", "new_session": "new session", "new_story": "new story", "keymap": "keymap",
	"fold_all": "fold all", "scroll_up": "scroll up", "scroll_down": "scroll down", "lazygit": "lazygit", "nvim": "nvim", "shell": "new shell", "prs": "PRs",
	"rename": "rename", "resume": "resume", "close_story": "close story", "activity": "activity", "close_project": "close project",
	"settings": "settings", "add_repo": "add repo", "stop": "stop", "delete": "delete",
	"cleanup": "cleanup", "artifacts": "artifacts",
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
	if ctx.ClaudeFocused && ctx.Shell {
		parts = []string{"SHELL", "type to run · exit closes it", k(cfg.Client.PaneLeft, "to list")}
		if cfg.Client.CopyMode != "" {
			parts = append(parts, k(cfg.Client.CopyMode, "copy"))
		}
		return join(parts)
	}
	if ctx.ClaudeFocused {
		back := k(cfg.Client.PaneLeft, "to list")
		if ctx.FocusMode {
			back = k(cfg.Client.PaneLeft, "exit focus, to list")
		}
		if n := ctx.Session; n != 0 && strings.HasPrefix(m.StateText(n), "asks:") {
			parts = []string{"CLAUDE", "↑↓ move", "⏎ confirm", "esc no", back}
		} else {
			parts = []string{"CLAUDE", "type to write · Claude queues it while it works", "⏎ send", "esc interrupt", back}
		}
		return join(parts)
	}
	r := m.Selected()
	parts = append(parts, "j/k move")
	num, _ := m.SelectedSession()
	if r.Kind == RowStory {
		if m.IsOpen(r.Story) {
			parts = append(parts, "⏎ collapse")
		} else {
			parts = append(parts, "⏎ expand")
		}
	} else if r.Kind == RowShell {
		parts = append(parts, "⏎ open shell")
	} else if num != 0 {
		if ctx.Narrow {
			parts = append(parts, "⏎ Claude")
		} else {
			parts = append(parts, "⏎ type in Claude")
		}
	}
	if num != 0 {
		if sess := m.P.Sessions[num]; sess.Done {
			parts = append(parts, lk("resume"))
		} else {
			parts = append(parts, lk("focus"))
			if m.Gone(num) {
				parts = append(parts, lk("resume"))
			}
			parts = append(parts, lk("stop"))
		}
		parts = append(parts, lk("rename"), lk("delete"), lk("artifacts"))
	} else if r.Kind == RowShell {
		parts = append(parts, lk("rename"))
		if key := cfg.Keys.First(cfg.Keys.List, "delete"); key != "" {
			parts = append(parts, k(key, "close shell"))
		}
	} else if r.Kind == RowStory {
		if r.Story != "" {
			parts = append(parts, lk("close_story"))
		}
		parts = append(parts, lk("cleanup"))
		if r.Story != "" && m.P.IsWorkspace() {
			parts = append(parts, lk("add_repo"))
		}
	}
	parts = append(parts, lk("shell"), lk("fold_all"), lk("new_chat"), lk("new_session"), lk("new_story"))
	parts = append(parts, lk("keymap"), lk("waiting"), lk("projects"), lk("activity"), lk("settings"))
	if ctx.Projects > 1 {
		parts = append(parts, switchKeys(cfg))
	}
	parts = append(parts, lk("close_project"))
	if ctx.Narrow {
		// one pane at a time: say how to come back
		parts = append(parts, k(cfg.Client.PaneRight, "back"))
	}
	return join(parts)
}

// switchKeys is "[ ] switch project", or "" when either key is unbound.
func switchKeys(cfg *config.Config) string {
	prev, next := cfg.Keys.First(cfg.Keys.List, "prev_project"), cfg.Keys.First(cfg.Keys.List, "next_project")
	if prev == "" || next == "" {
		return ""
	}
	return config.Pretty(prev) + " " + config.Pretty(next) + " switch project"
}

// ActivityBar is the key bar of the activity list. header tells that the cursor is on a
// project's header.
func ActivityBar(cfg *config.Config, header bool, ctx BarContext) string {
	if ctx.Flash != "" {
		return ctx.Flash
	}
	lk := func(action, label string) string {
		if key := cfg.Keys.First(cfg.Keys.List, action); key != "" {
			return config.Pretty(key) + " " + label
		}
		return ""
	}
	parts := []string{"j/k move"}
	switch {
	case header:
		parts = append(parts, lk("open", "open project"))
	case ctx.Narrow:
		parts = append(parts, lk("open", "Claude"))
	default:
		parts = append(parts, lk("open", "type in Claude"))
	}
	if !header {
		parts = append(parts, lk("focus", "focus"))
	}
	parts = append(parts, lk("activity", "back"), switchKeys(cfg))
	if ctx.Narrow {
		parts = append(parts, config.Pretty(cfg.Client.PaneRight)+" back")
	}
	return join(parts)
}

// WaitingElsewhere is a waiting session in another project, for the focus line.
type WaitingElsewhere struct {
	Project string
	Num     int
}

// FocusLine is the single status line shown in focus mode.
func FocusLine(m *Model, waitingKey, exitKey string, runningElsewhere int, waiting []WaitingElsewhere) string {
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
	right = append(right, fmt.Sprintf("● %d running", runningElsewhere), config.Pretty(exitKey)+" exit")
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
