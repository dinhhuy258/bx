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
	"focus": "focus mode", "inbox": "inbox", "projects": "projects",
	"new_chat": "new chat", "new_session": "new session", "new_story": "new story", "keymap": "keymap",
	"fold_all": "fold all", "scroll_up": "scroll up", "scroll_down": "scroll down", "nvim": "nvim", "shell": "new shell", "prs": "PRs",
	"close_project": "close project", "settings": "settings", "cleanup": "cleanup",
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
	row, rowKeys := rowPart(m, cfg, ctx)
	global := []string{"j/k move", lk("shell"), lk("fold_all"), lk("new_chat"), lk("new_session"), lk("new_story")}
	if r := m.Selected(); r.Kind == RowStory {
		global = append(global, lk("cleanup"))
	}
	global = append(global, lk("keymap"), lk("inbox"), lk("projects"), lk("settings"))
	if ctx.Projects > 1 {
		global = append(global, switchKeys(cfg))
	}
	global = append(global, lk("close_project"))
	if ctx.Narrow {
		// one pane at a time: say how to come back
		global = append(global, k(cfg.Client.PaneRight, "back"))
	}
	if keys := join(rowKeys); keys != "" {
		return row + " › " + keys + "  │  " + join(global)
	}
	return join(global)
}

// rowPart names the selected row and lists the keys that act on it, so "d delete" reads as
// deleting that session and "D close" as closing that story.
func rowPart(m *Model, cfg *config.Config, ctx BarContext) (string, []string) {
	lk := func(action, label string) string {
		if key := cfg.Keys.First(cfg.Keys.List, action); key != "" {
			return config.Pretty(key) + " " + label
		}
		return ""
	}
	r := m.Selected()
	if num, sess := m.SelectedSession(); sess != nil {
		open := lk("open", "type in Claude")
		if ctx.Narrow {
			open = lk("open", "Claude")
		}
		parts := []string{open}
		if sess.Done {
			parts = append(parts, lk("resume", "resume"))
		} else {
			parts = append(parts, lk("focus", "focus mode"))
			if m.Gone(num) {
				parts = append(parts, lk("resume", "resume"))
			}
			parts = append(parts, lk("stop_session", "stop"))
		}
		parts = append(parts, lk("rename", "rename"), lk("delete_session", "delete"), lk("artifacts", "artifacts"))
		return truncate(fmt.Sprintf("[%d] %s", num, sess.Title), rowNameWidth), parts
	}
	if sh, ok := m.SelectedShell(); ok {
		return truncate(fmt.Sprintf("$%d %s", sh.K, sh.Label()), rowNameWidth),
			[]string{lk("open", "open"), lk("rename", "rename"), lk("close_shell", "close")}
	}
	if r.Kind != RowStory {
		return "", nil
	}
	fold := lk("open", "expand")
	if m.IsOpen(r.Story) {
		fold = lk("open", "collapse")
	}
	if r.Story == "" {
		return "chats", []string{fold}
	}
	parts := []string{fold, lk("close_story", "close")}
	if m.P.IsWorkspace() {
		parts = append(parts, lk("add_repo", "add repo"))
	}
	return truncate("story "+r.Story, rowNameWidth), parts
}

// rowNameWidth caps the row name in the key bar, so a long title leaves room for the keys.
const rowNameWidth = 24

// switchKeys is "[ ] switch project", or "" when either key is unbound.
func switchKeys(cfg *config.Config) string {
	prev, next := cfg.Keys.First(cfg.Keys.List, "prev_project"), cfg.Keys.First(cfg.Keys.List, "next_project")
	if prev == "" || next == "" {
		return ""
	}
	return config.Pretty(prev) + " " + config.Pretty(next) + " switch project"
}

// ActivityBar is the key bar of the inbox. header tells that the cursor is on a
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
	parts = append(parts, lk("inbox", "back"), switchKeys(cfg))
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
func FocusLine(m *Model, inboxKey, exitKey string, runningElsewhere int, waiting []WaitingElsewhere) string {
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
		right = append(right, w+" · "+config.Pretty(inboxKey)+" inbox")
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
