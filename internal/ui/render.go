package ui

import (
	"fmt"
	"os"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/dinhhuy258/bx/internal/claude"
)

// ListWidth is the width of the list pane next to the Claude pane. app.ListWidth is the same number.
const ListWidth = 46

// RenderOptions controls colours. Plain disables ANSI for golden tests. Focused draws the rule
// under the header heavy and blue, like the Claude pane's when it has the focus. Projects, the
// projects the client has open, adds the project strip under the rule.
type RenderOptions struct {
	Plain, Focused bool
	Projects       []ProjectTab
}

type palette struct {
	dim, blue, amber, green, red, bold, sel, brand lipgloss.Style
}

func newPalette(plain bool) palette {
	if plain {
		s := lipgloss.NewStyle()
		return palette{s, s, s, s, s, s, s, s}
	}
	return palette{
		dim:   lipgloss.NewStyle().Foreground(lipgloss.Color("#565f89")),
		blue:  lipgloss.NewStyle().Foreground(lipgloss.Color("#7aa2f7")),
		amber: lipgloss.NewStyle().Foreground(lipgloss.Color("#e0af68")),
		green: lipgloss.NewStyle().Foreground(lipgloss.Color("#9ece6a")),
		red:   lipgloss.NewStyle().Foreground(lipgloss.Color("#f7768e")),
		bold:  lipgloss.NewStyle().Bold(true),
		sel:   lipgloss.NewStyle().Background(lipgloss.Color("#292e42")),
		brand: lipgloss.NewStyle().Foreground(lipgloss.Color("#bb9af7")).Bold(true),
	}
}

// Render draws the list pane at width x height. Lines never exceed width cells.
func Render(m *Model, width, height int, o RenderOptions) string {
	if width < 30 {
		return "bx: terminal too narrow"
	}
	pal := newPalette(o.Plain)
	showAge := width >= 46
	var lines []string

	run, wait := m.Counts()
	nStories := len(m.P.Stories)
	head := fit(pal.brand.Render("bx")+" "+pal.bold.Render(m.P.Name), pal.dim.Render(plural(nStories, "story", "stories")), width)
	// the rule stays on the second line, level with the Claude pane's
	lines = append(lines, head, rule(width, o))
	if len(o.Projects) > 0 {
		lines = append(lines, strip(o.Projects, m.P.Name, width, pal))
	}
	waitText := pal.dim.Render(fmt.Sprintf("◆ %d needs you", wait))
	if wait > 0 {
		waitText = pal.amber.Render(fmt.Sprintf("◆ %d needs you", wait))
	}
	lines = append(lines, fit(pal.blue.Render(fmt.Sprintf("● %d running", run)), waitText, width))

	rows := m.Rows()
	selIdx := m.SelIndex()
	for i, r := range rows {
		cursor := " "
		if i == selIdx {
			cursor = "›"
		}
		if r.Kind == RowStory {
			lines = append(lines, m.storyLine(r, cursor, width, pal, i == selIdx))
			if len(m.P.SessionsOf(r.Story)) == 0 && m.IsOpen(r.Story) {
				what, key := "no sessions", "n"
				if r.Story == "" {
					what, key = "no chats", "c"
				}
				lines = append(lines, truncate("      "+pal.dim.Render(what+" · "+key+" to start one"), width))
			}
			continue
		}
		s := m.P.Sessions[r.Num]
		dot := m.Dot(r.Num)
		switch m.State(r.Num) {
		case claude.Waiting:
			dot = pal.amber.Render(dot)
		case claude.Running:
			dot = pal.blue.Render(dot)
		default:
			if s.Done {
				dot = pal.green.Render(dot)
			} else {
				dot = pal.dim.Render(dot)
			}
		}
		title := fmt.Sprintf("[%d] %s", r.Num, s.Title)
		if i == selIdx {
			title = pal.bold.Render(title)
		}
		right := ""
		if showAge {
			right = pal.dim.Render(m.Age(r.Num))
		}
		lines = append(lines, fit(cursor+"  "+dot+" "+title, right, width))
		st := m.StateText(r.Num)
		if m.State(r.Num) == claude.Waiting {
			st = pal.amber.Render(st)
		}
		lines = append(lines, truncate("      "+st, width))
	}
	if nStories == 0 {
		lines = append(lines, "", "No stories yet.", "A story is a branch with its own worktree.", "Press w to create one.")
	}
	return pad(lines, height)
}

func (m *Model) storyLine(r Row, cursor string, width int, pal palette, selected bool) string {
	arrow := "▸"
	if m.IsOpen(r.Story) {
		arrow = "▾"
	}
	name := "chats"
	// Each group row is one folder: chats run in the project folder, a story in its worktree.
	right := pal.dim.Render(m.branch()+" · ") + changed(m.Live.DirtyFiles, pal, " · ") + pal.dim.Render(shortPath(m.P.Path))
	if r.Story != "" {
		name = r.Story
		d := m.Live.Diff[r.Story]
		var parts []string
		if d.Dirty > 0 {
			parts = append(parts, changed(d.Dirty, pal, ""))
		}
		if d.Add > 0 {
			parts = append(parts, pal.green.Render(fmt.Sprintf("+%d", d.Add)))
		}
		if d.Del > 0 {
			parts = append(parts, pal.red.Render(fmt.Sprintf("-%d", d.Del)))
		}
		right = strings.Join(parts, " ")
		if !m.IsOpen(r.Story) {
			if sum := m.Summary(r.Story); sum != "" {
				right = sum + "  " + right
			}
		}
	}
	left := cursor + " " + pal.dim.Render(arrow) + " " + pal.bold.Render(name)
	return fit(left, right, width)
}

// changed is the amber "N changed" for uncommitted files in a folder, empty when clean.
func changed(n int, pal palette, sep string) string {
	if n == 0 {
		return ""
	}
	return pal.amber.Render(fmt.Sprintf("%d changed", n)) + pal.dim.Render(sep)
}

// shortPath replaces the home directory with ~.
func shortPath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if p == home {
			return "~"
		}
		if strings.HasPrefix(p, home+"/") {
			return "~" + p[len(home):]
		}
	}
	if i := strings.Index(p, "/.bx/"); i >= 0 {
		return "~" + p[i:]
	}
	return p
}

// fit places left and right on one line of width cells, truncating left first.
func fit(left, right string, width int) string {
	lw, rw := lipgloss.Width(left), lipgloss.Width(right)
	if rw == 0 {
		return truncate(left, width)
	}
	if rw > width/2 && lw+1+rw > width { // keep at least half the line for the name
		right = truncate(right, max(width-lw-1, width/2))
		rw = lipgloss.Width(right)
	}
	if lw+1+rw > width {
		left = truncate(left, width-rw-1)
		lw = lipgloss.Width(left)
	}
	return left + strings.Repeat(" ", max(width-lw-rw, 1)) + right
}

// truncate cuts s to width cells, ending with … when cut.
func truncate(s string, width int) string {
	if lipgloss.Width(s) <= width {
		return s
	}
	if width <= 1 {
		return "…"
	}
	return lipgloss.NewStyle().MaxWidth(width-1).Render(s) + "…"
}

func runeWidth(s string) int { return lipgloss.Width(s) }
