package ui

import (
	"fmt"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/dinhhuy258/bx/internal/config"
)

// keymapItem is one line of the keymap: a list action and what it does.
type keymapItem struct{ action, does string }

// keymapGroup is a block of the keymap: the thing its keys act on.
type keymapGroup struct {
	title, about string
	items        []keymapItem
}

// keymapGroups sort the list actions by what they act on. An action bound in config.toml but
// in no group is listed under "other" by its name.
var keymapGroups = []keymapGroup{
	{"SESSION", "the selected [n] row", []keymapItem{
		{"open", "type in Claude"},
		{"focus", "focus mode · Claude full screen"},
		{"stop_session", "stop · Claude exits; resume picks it up"},
		{"resume", "resume a stopped or gone session"},
		{"delete_session", "delete · removes uploads and artifacts; transcript stays in ~/.claude"},
		{"rename", "rename"},
		{"artifacts", "artifacts · the pages it made"},
	}},
	{"SHELL", "the selected $n row", []keymapItem{
		{"open", "open the shell"},
		{"rename", "rename"},
		{"close_shell", "close · kills what runs in it"},
	}},
	{"STORY", "a branch and its worktree", []keymapItem{
		{"open", "fold or unfold"},
		{"new_session", "new session in it · in chats, a new chat"},
		{"add_repo", "add repositories (workspace)"},
		{"close_story", "close · stop its sessions, remove worktree"},
	}},
	{"PROJECT", "the one on screen", []keymapItem{
		{"new_story", "new story · a branch and worktree"},
		{"shell", "new shell"},
		{"projects", "open another project"},
		{"prev_project", "previous open project"},
		{"next_project", "next open project"},
		{"close_project", "close · off this client, keeps running"},
	}},
	{"GENERAL", "", []keymapItem{
		{"down", "move down"},
		{"up", "move up"},
		{"fold_all", "fold all stories"},
		{"inbox", "inbox · sessions that need you"},
		{"scroll_up", "scroll the Claude pane up"},
		{"scroll_down", "scroll the Claude pane down"},
		{"cleanup", "clean up leftovers, all projects"},
		{"settings", "settings"},
		{"keymap", "this keymap"},
	}},
}

// keyGap separates the two columns of the keymap.
const keyGap = 4

// Keymap renders the keymap overlay for a screen width: the pane keys, then the list keys
// grouped by what they act on, in two columns when they fit.
func Keymap(cfg *config.Config, width int) string {
	pal := newPalette(false)
	keys := func(binds ...string) string {
		out := make([]string, 0, len(binds))
		for _, b := range binds {
			if b != "" {
				out = append(out, config.Pretty(b))
			}
		}
		return strings.Join(out, " / ")
	}
	type line struct{ keys, does string }
	type block struct {
		head  string
		lines []line
	}
	var blocks []block
	panes := block{head: pal.bold.Render("PANES") + pal.dim.Render(" · from the list and from Claude")}
	for _, l := range []line{
		{keys(cfg.Client.PaneLeft), "to the list; past it, the tmux pane on the left"},
		{keys(cfg.Client.PaneRight), "to Claude; past it, the tmux pane on the right"},
		{keys(cfg.Client.CopyMode), "copy mode · vi keys, v select, y copy, q quit"},
	} {
		if l.keys != "" {
			panes.lines = append(panes.lines, l)
		}
	}
	blocks = append(blocks, panes)
	grouped := map[string]bool{}
	for _, g := range keymapGroups {
		b := block{head: pal.bold.Render(g.title)}
		if g.about != "" {
			b.head += pal.dim.Render(" · " + g.about)
		}
		for _, it := range g.items {
			grouped[it.action] = true
			if k := keys(cfg.Keys.List[it.action]...); k != "" {
				b.lines = append(b.lines, line{k, it.does})
			}
		}
		if len(b.lines) > 0 {
			blocks = append(blocks, b)
		}
	}
	var other []string
	for a, binds := range cfg.Keys.List {
		if !grouped[a] && len(binds) > 0 {
			other = append(other, a)
		}
	}
	sort.Strings(other)
	if len(other) > 0 {
		b := block{head: pal.bold.Render("OTHER")}
		for _, a := range other {
			b.lines = append(b.lines, line{keys(cfg.Keys.List[a]...), strings.ReplaceAll(a, "_", " ")})
		}
		blocks = append(blocks, b)
	}

	keyW := 0
	for _, b := range blocks {
		for _, l := range b.lines {
			keyW = max(keyW, lipgloss.Width(l.keys))
		}
	}
	render := func(bs []block) string {
		var out []string
		for i, b := range bs {
			if i > 0 {
				out = append(out, "")
			}
			out = append(out, b.head)
			for _, l := range b.lines {
				pad := strings.Repeat(" ", keyW-lipgloss.Width(l.keys))
				out = append(out, fmt.Sprintf("  %s%s  %s", pal.blue.Render(l.keys), pad, l.does))
			}
		}
		return strings.Join(out, "\n")
	}
	// two columns split where the line counts come closest to even
	lines := func(bs []block) int {
		n := 0
		for _, b := range bs {
			n += len(b.lines) + 2
		}
		return n
	}
	split, best := len(blocks), lines(blocks)
	for i := 1; i < len(blocks); i++ {
		if d := max(lines(blocks[:i]), lines(blocks[i:])); d < best {
			split, best = i, d
		}
	}
	left, right := render(blocks[:split]), render(blocks[split:])
	if split == len(blocks) || lipgloss.Width(left)+keyGap+lipgloss.Width(right) > width {
		return render(blocks)
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, lipgloss.NewStyle().PaddingRight(keyGap).Render(left), right)
}
