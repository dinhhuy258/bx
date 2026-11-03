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
	dim, blue, amber, green, red, bold, sel, brand, cyan, group, arrow lipgloss.Style
	selBG, groupBG                                                     string // SGR of the selection and group-row bands, "" when plain
}

func newPalette(plain bool) palette {
	if plain {
		s := lipgloss.NewStyle()
		return palette{s, s, s, s, s, s, s, s, s, s, s, "", ""}
	}
	return palette{
		cyan:    lipgloss.NewStyle().Foreground(lipgloss.Color("#7dcfff")),
		dim:     lipgloss.NewStyle().Foreground(lipgloss.Color("#565f89")),
		blue:    lipgloss.NewStyle().Foreground(lipgloss.Color("#7aa2f7")),
		amber:   lipgloss.NewStyle().Foreground(lipgloss.Color("#e0af68")),
		green:   lipgloss.NewStyle().Foreground(lipgloss.Color("#9ece6a")),
		red:     lipgloss.NewStyle().Foreground(lipgloss.Color("#f7768e")),
		bold:    lipgloss.NewStyle().Bold(true),
		sel:     lipgloss.NewStyle().Background(lipgloss.Color("#292e42")),
		brand:   lipgloss.NewStyle().Foreground(lipgloss.Color("#bb9af7")).Bold(true),
		group:   lipgloss.NewStyle().Foreground(lipgloss.Color("#bb9af7")).Bold(true),
		arrow:   lipgloss.NewStyle().Foreground(lipgloss.Color("#9d7cd8")),
		selBG:   "\x1b[48;2;41;46;66m", // #292e42
		groupBG: "\x1b[48;2;31;35;53m", // #1f2335
	}
}

// band paints line, padded to width, with the background sgr. It renews sgr after every reset
// inside the line so the band holds across the line's own styling. "" leaves line as it is.
func band(line string, width int, sgr string) string {
	if sgr == "" {
		return line
	}
	if w := lipgloss.Width(line); w < width {
		line += strings.Repeat(" ", width-w)
	}
	return sgr + strings.ReplaceAll(line, "\x1b[m", "\x1b[m"+sgr) + "\x1b[m"
}

// Render draws the list pane at width x height. Lines never exceed width cells.
//
// The header holds the brand, the project (or the strip of opened projects) and what runs or
// waits; the rule stays on the second line, level with the Claude pane's. Under it each story is
// a band with its name, and its sessions and shells hang off it on tree guides, one line each.
// A session takes a second line only when it says something to act on: a question, or gone.
func Render(m *Model, width, height int, o RenderOptions) string {
	if width < 30 {
		return "bx: terminal too narrow"
	}
	pal := newPalette(o.Plain)
	showAge := width >= 46

	run, wait, _ := m.Counts()
	var counts []string
	if run > 0 {
		counts = append(counts, pal.blue.Render(fmt.Sprintf("● %d", run)))
	}
	if wait > 0 {
		counts = append(counts, pal.amber.Render(fmt.Sprintf("◆ %d", wait)))
	}
	right := strings.Join(counts, "  ")
	title := pal.bold.Render(m.P.Name)
	if len(o.Projects) > 0 {
		title = strip(o.Projects, m.P.Name, width-4-lipgloss.Width(right)-2, pal)
	}
	top := []string{fit(pal.brand.Render("bx")+"  "+title, right, width), rule(width, o)}

	rows := m.Rows()
	selIdx := m.SelIndex()
	numW := len(fmt.Sprint(m.maxNum()))
	var body []string
	selLine := 0
	for i, r := range rows {
		sel := i == selIdx
		if sel {
			selLine = len(body)
		}
		mark := func(line string, bg string) string {
			if sel {
				return band(pal.blue.Render("▌")+line[1:], width, pal.selBG)
			}
			return band(line, width, bg)
		}
		last := i+1 >= len(rows) || rows[i+1].Kind == RowStory
		guide, under := "├ ", "│ "
		if last {
			guide, under = "└ ", "  "
		}
		switch r.Kind {
		case RowStory:
			body = append(body, mark(m.storyLine(r, width, pal), pal.groupBG))
			if len(m.P.SessionsOf(r.Story)) == 0 && len(m.ShellsOf(r.Story)) == 0 && m.IsOpen(r.Story) {
				what := "no sessions"
				if r.Story == "" {
					what = "no chats"
				}
				body = append(body, truncate("   "+pal.dim.Render("└ "+what), width))
			}
		case RowShell:
			body = append(body, mark(m.shellLine(r, pal.dim.Render(guide), width, pal, sel), ""))
		default:
			line, note := m.sessionLine(r.Num, numW, pal.dim.Render(guide), width, showAge, pal, sel)
			body = append(body, mark(line, ""))
			if note != "" {
				body = append(body, truncate("   "+pal.dim.Render(under)+strings.Repeat(" ", numW+4)+note, width))
			}
		}
	}
	if len(m.P.Stories) == 0 {
		body = append(body, "", " No stories yet.", " A story is a branch with its own worktree.", " Press w to create one.")
	}
	// scroll the rows under the header so the cursor and the line under it stay in view
	room := max(height-len(top), 1)
	off := max(min(selLine+2-room, len(body)-room), 0)
	return pad(append(top, body[off:]...), height)
}

// maxNum is the highest session number, to right-align the numbers in one column.
func (m *Model) maxNum() int {
	n := 1
	for k := range m.P.Sessions {
		n = max(n, k)
	}
	return n
}

// sessionLine is a session's row, "▌  ├ ●  1  Fix guest DHCP lease time   6m", and its note
// for the line under it: the question it asks, or that it is gone; "" when there is nothing
// to act on.
func (m *Model) sessionLine(num, numW int, guide string, width int, showAge bool, pal palette, selected bool) (string, string) {
	s := m.P.Sessions[num]
	dot := m.Dot(num)
	switch m.State(num) {
	case claude.Waiting:
		dot = pal.amber.Render(dot)
	case claude.Running:
		dot = pal.blue.Render(dot)
	default:
		if s.Done || m.Unread(num) {
			dot = pal.green.Render(dot)
		} else {
			dot = pal.dim.Render(dot)
		}
	}
	title := s.Title
	if selected {
		title = pal.bold.Render(title)
	}
	right := ""
	if showAge {
		right = pal.dim.Render(m.Age(num))
	}
	line := fit("   "+guide+dot+" "+pal.dim.Render(fmt.Sprintf("%*d", numW, num))+"  "+title, right, width)
	note := ""
	switch {
	case m.State(num) == claude.Waiting:
		note = pal.amber.Render(m.StateText(num))
	case m.Gone(num):
		note = pal.red.Render(m.StateText(num))
	}
	return line, note
}

// storyLine is a group row: the chats of the project folder, or a story and its worktree.
func (m *Model) storyLine(r Row, width int, pal palette) string {
	arrow := "▸"
	if m.IsOpen(r.Story) {
		arrow = "▾"
	}
	name := "chats"
	// Each group row is one folder: chats run in the project folder, a story in its worktree.
	// A workspace is no repository: its folder has no branch nor changes of its own.
	right := pal.dim.Render(m.branch()) + changed(m.Live.DirtyFiles, pal, "", " · ")
	if m.P.IsWorkspace() {
		right = pal.dim.Render("workspace")
	}
	if r.Story != "" {
		name = r.Story
		d := m.Live.Diff[r.Story]
		var parts []string
		if d.Dirty > 0 {
			parts = append(parts, changed(d.Dirty, pal, "", ""))
		}
		if d.Add > 0 {
			parts = append(parts, pal.green.Render(fmt.Sprintf("+%d", d.Add)))
		}
		if d.Del > 0 {
			parts = append(parts, pal.red.Render(fmt.Sprintf("-%d", d.Del)))
		}
		right = strings.Join(parts, " ")
		if st := m.P.Stories[r.Story]; st != nil && len(st.Repos) > 0 {
			repos := pal.dim.Render(strings.Join(st.RepoNames(), ", "))
			if right != "" {
				repos += pal.dim.Render(" · ")
			}
			right = repos + right
		}
		if !m.IsOpen(r.Story) {
			if sum := m.Summary(r.Story); sum != "" {
				right = sum + "  " + right
			}
		}
	}
	return fit(" "+pal.arrow.Render(arrow)+" "+pal.group.Render(name), right, width)
}

// shellLine is the one line of a shell row: "$1  zsh", or "$2  dev server … npm" when the
// shell has a title, with the command dim on the right.
func (m *Model) shellLine(r Row, guide string, width int, pal palette, selected bool) string {
	sh, _ := m.Shell(r.Story, r.K)
	label := sh.Label()
	if selected {
		label = pal.bold.Render(label)
	}
	right := ""
	if sh.Title != "" {
		right = pal.dim.Render(sh.Command)
	}
	return fit("   "+guide+pal.cyan.Render(fmt.Sprintf("$%d", r.K))+"  "+label, right, width)
}

// changed is the amber "N changed" for uncommitted files in a folder, between before and after,
// or "" when the folder is clean.
func changed(n int, pal palette, after, before string) string {
	if n == 0 {
		return ""
	}
	return pal.dim.Render(before) + pal.amber.Render(fmt.Sprintf("%d changed", n)) + pal.dim.Render(after)
}

// ShortPath replaces the home directory with ~, as the list shows folders.
func ShortPath(p string) string { return shortPath(p) }

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
