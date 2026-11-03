package ui

import (
	"fmt"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/dinhhuy258/bx/internal/claude"
)

// ProjectTab is one project in the tabs of the list header.
type ProjectTab struct {
	Name             string
	Running, Waiting int
}

// strip is the project tabs of the header: "1 home-network  2 bx ◆1  3 dotfiles ●2". The first
// nine are numbered for the project_1..9 keys. The open project is bold blue on the selection
// band and has no count, the header's right side shows its own; the others show what waits,
// else what runs. When they do not fit, the open tab stays: the tabs before it are cut first,
// then those after, and the cut ones become "+2 ◆1", with what waits among them.
func strip(tabs []ProjectTab, current string, width int, pal palette) string {
	items := make([]string, len(tabs))
	cur := 0
	for i, t := range tabs {
		num := ""
		if i < 9 {
			num = pal.dim.Render(fmt.Sprint(i+1)) + " "
		}
		if t.Name == current {
			cur = i
			items[i] = band(num+pal.bold.Inherit(pal.blue).Render(t.Name), 0, pal.selBG)
			continue
		}
		badge := ""
		switch {
		case t.Waiting > 0:
			badge = " " + pal.amber.Render(fmt.Sprintf("◆%d", t.Waiting))
		case t.Running > 0:
			badge = " " + pal.blue.Render(fmt.Sprintf("●%d", t.Running))
		}
		items[i] = num + t.Name + badge
	}
	// rest is "+N ◆W" for the tabs left out of [from, to)
	rest := func(from, to int) string {
		n, w := 0, 0
		for i, t := range tabs {
			if i < from || i >= to {
				n++
				w += t.Waiting
			}
		}
		if n == 0 {
			return ""
		}
		s := pal.dim.Render(fmt.Sprintf("+%d", n))
		if w > 0 {
			s += " " + pal.amber.Render(fmt.Sprintf("◆%d", w))
		}
		return s
	}
	line := func(from, to int) string {
		parts := append([]string(nil), items[from:to]...)
		if r := rest(from, to); r != "" {
			parts = append(parts, r)
		}
		return strings.Join(parts, "  ")
	}
	from, to := 0, len(tabs)
	for to > cur+1 && runeWidth(line(from, to)) > width {
		to--
	}
	for from < cur && runeWidth(line(from, to)) > width {
		from++
	}
	return truncate(line(from, to), width)
}

// ActivityRow is one row of the inbox: a project's header (Num 0) or one of its sessions.
type ActivityRow struct {
	Project string
	Num     int
}

// ActivityRows is the inbox: every session of models that needs you, waiting or unread, grouped
// by project: projects with a waiting session first, then by name; in a project, waiting before
// unread, then by number. Running, read idle, done and gone sessions are left out, and so are
// projects with none left.
func ActivityRows(models []*Model) []ActivityRow {
	type group struct {
		name string
		wait bool
		nums []int
	}
	var groups []group
	for _, m := range models {
		g := group{name: m.P.Name}
		for n := range m.P.Sessions {
			switch {
			case m.State(n) == claude.Waiting:
				g.wait = true
				g.nums = append(g.nums, n)
			case m.Unread(n):
				g.nums = append(g.nums, n)
			}
		}
		if len(g.nums) == 0 {
			continue
		}
		sort.Slice(g.nums, func(i, j int) bool {
			wi, wj := m.State(g.nums[i]) == claude.Waiting, m.State(g.nums[j]) == claude.Waiting
			if wi != wj {
				return wi
			}
			return g.nums[i] < g.nums[j]
		})
		groups = append(groups, g)
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].wait != groups[j].wait {
			return groups[i].wait
		}
		return groups[i].name < groups[j].name
	})
	var rows []ActivityRow
	for _, g := range groups {
		rows = append(rows, ActivityRow{Project: g.name})
		for _, n := range g.nums {
			rows = append(rows, ActivityRow{Project: g.name, Num: n})
		}
	}
	return rows
}

// Activity is the cursor of the inbox. It stays on its row while rows move; when the row leaves
// (the session was answered or read) it stays at the same place in the list.
type Activity struct {
	sel ActivityRow
	idx int
}

// Index finds the selected row in rows and remembers it. A fresh cursor starts on the first session.
func (a *Activity) Index(rows []ActivityRow) int {
	if len(rows) == 0 {
		return 0
	}
	if a.sel == (ActivityRow{}) {
		a.idx = min(1, len(rows)-1)
	}
	for i, r := range rows {
		if r == a.sel {
			a.idx = i
			return i
		}
	}
	a.idx = max(min(a.idx, len(rows)-1), 0)
	a.sel = rows[a.idx]
	return a.idx
}

// Selected returns the selected row; the zero row when rows is empty.
func (a *Activity) Selected(rows []ActivityRow) ActivityRow {
	if len(rows) == 0 {
		return ActivityRow{}
	}
	return rows[a.Index(rows)]
}

// Move moves the cursor by delta, clamped.
func (a *Activity) Move(rows []ActivityRow, delta int) {
	if len(rows) == 0 {
		return
	}
	a.idx = max(min(a.Index(rows)+delta, len(rows)-1), 0)
	a.sel = rows[a.idx]
}

// RenderActivity draws the inbox at width x height in place of the list pane. empty is the hint
// under "Nothing needs you..
func RenderActivity(models []*Model, a *Activity, width, height int, o RenderOptions, empty string) string {
	if width < 30 {
		return "bx: terminal too narrow"
	}
	pal := newPalette(o.Plain)
	byName := map[string]*Model{}
	unread, wait := 0, 0
	for _, m := range models {
		byName[m.P.Name] = m
	}
	rows := ActivityRows(models)
	for _, r := range rows {
		if r.Num == 0 {
			continue
		}
		if byName[r.Project].State(r.Num) == claude.Waiting {
			wait++
		} else {
			unread++
		}
	}
	nProjects := 0
	for _, r := range rows {
		if r.Num == 0 {
			nProjects++
		}
	}
	head := fit(pal.brand.Render("bx")+" "+pal.bold.Render("inbox"), pal.dim.Render(plural(nProjects, "project", "projects")), width)
	waitText := pal.dim.Render(fmt.Sprintf("◆ %d needs you", wait))
	if wait > 0 {
		waitText = pal.amber.Render(fmt.Sprintf("◆ %d needs you", wait))
	}
	unreadText := pal.dim.Render(fmt.Sprintf("◎ %d unread", unread))
	if unread > 0 {
		unreadText = pal.green.Render(fmt.Sprintf("◎ %d unread", unread))
	}
	top := []string{head, rule(width, o), fit(waitText, unreadText, width)}
	if len(rows) == 0 {
		top = append(top, "", "  Nothing needs you.", truncate("  "+pal.dim.Render(empty), width))
		return pad(top, height)
	}

	selIdx := a.Index(rows)
	var body []string
	selLine := 0
	for i, r := range rows {
		cursor := " "
		if i == selIdx {
			cursor = "›"
			selLine = len(body)
		}
		m := byName[r.Project]
		if r.Num == 0 {
			pw, pu := 0, 0
			for _, x := range rows {
				if x.Project == r.Project && x.Num != 0 {
					if m.State(x.Num) == claude.Waiting {
						pw++
					} else {
						pu++
					}
				}
			}
			var counts []string
			if pw > 0 {
				counts = append(counts, pal.amber.Render(fmt.Sprintf("◆ %d", pw)))
			}
			if pu > 0 {
				counts = append(counts, pal.green.Render(fmt.Sprintf("◎ %d", pu)))
			}
			body = append(body, fit(cursor+" "+pal.bold.Render(r.Project), strings.Join(counts, "  "), width))
			continue
		}
		s := m.P.Sessions[r.Num]
		dot, st := pal.green.Render(m.Dot(r.Num)), pal.green.Render(m.StateText(r.Num))
		if m.State(r.Num) == claude.Waiting {
			dot, st = pal.amber.Render(m.Dot(r.Num)), pal.amber.Render(st)
		}
		title := fmt.Sprintf("[%d] %s", r.Num, s.Title)
		if i == selIdx {
			title = pal.bold.Render(title)
		}
		right := ""
		if width >= 46 {
			right = pal.dim.Render(m.Age(r.Num))
		}
		group := s.Story
		if group == "" {
			group = "chats"
		}
		body = append(body, fit(cursor+"  "+dot+" "+title, right, width),
			truncate("      "+pal.dim.Render(group+" · ")+st, width))
	}
	// scroll the rows under the header so the cursor and the line under it stay in view
	room := max(height-len(top), 1)
	off := max(min(selLine+2-room, len(body)-room), 0)
	return pad(append(top, body[off:]...), height)
}

// rule is the line under the header: heavy and blue when the list has the focus.
func rule(width int, o RenderOptions) string {
	ch, color := "─", "#3b4261"
	if o.Focused {
		ch, color = "━", "#7aa2f7"
	}
	r := strings.Repeat(ch, width)
	if !o.Plain {
		r = lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(r)
	}
	return r
}

// pad fills or cuts lines to height and joins them.
func pad(lines []string, height int) string {
	for len(lines) < height {
		lines = append(lines, "")
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	return strings.Join(lines, "\n")
}
