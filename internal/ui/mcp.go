package ui

import (
	"fmt"
	"slices"
	"strings"
)

// MCPServer is one MCP server on the settings screen: Type stdio, http or sse; Target its
// command line or URL.
type MCPServer struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Target string `json:"target"`
}

// MCPSet is the servers of one scope. Error says why they are skipped at launch: a hand edit
// that fails the check.
type MCPSet struct {
	Servers []MCPServer `json:"servers"`
	Error   string      `json:"error,omitempty"`
}

// MCPProject is a project that has its own servers.
type MCPProject struct {
	Project string `json:"project"`
	MCPSet
}

// MCP is what the settings screen shows: the global servers and every project with its own,
// by name.
type MCP struct {
	Global   MCPSet       `json:"global"`
	Projects []MCPProject `json:"projects"`
}

// mcpSection is the ID of the server's MCP section; the client adds its rows under the Mode
// field.
const mcpSection = "mcp"

const mcpAdd = "+ add project servers"

// MCPRow is an MCP row: the Global row, a project's, or the add row.
type MCPRow struct {
	Project     string
	Global, Add bool
}

// SetMCP shows the servers from GET /api/mcp. The cursor stays on its row number.
func (s *Settings) SetMCP(m MCP) {
	s.mcp = m
	s.fld = min(s.fld, max(s.rowCount()-1, 0))
}

// SetMCPTotal is the number of projects, for the section's header.
func (s *Settings) SetMCPTotal(n int) { s.mcpTotal = n }

// SetMCPError marks scope ("" = global) as refused with err; "" clears it.
func (s *Settings) SetMCPError(scope, err string) {
	if err == "" {
		delete(s.mcpErrs, scope)
		return
	}
	s.mcpErrs[scope] = err
}

// MCPShown reports whether the MCP section is open.
func (s *Settings) MCPShown() bool {
	return s.sec < len(s.sections) && s.sections[s.sec].ID == mcpSection
}

// MCPProjects are the projects that have their own servers.
func (s *Settings) MCPProjects() []string {
	out := make([]string, len(s.mcp.Projects))
	for i, p := range s.mcp.Projects {
		out[i] = p.Project
	}
	return out
}

// MCPCount is the number of servers of scope ("" = global).
func (s *Settings) MCPCount(scope string) int {
	if scope == "" {
		return len(s.mcp.Global.Servers)
	}
	for _, p := range s.mcp.Projects {
		if p.Project == scope {
			return len(p.Servers)
		}
	}
	return 0
}

func (s *Settings) mcpRowCount() int {
	if !s.MCPShown() {
		return 0
	}
	return len(s.mcp.Projects) + 2
}

// MCPChoice is the MCP row under the cursor; false on the Mode field or outside the section.
func (s *Settings) MCPChoice() (MCPRow, bool) {
	i := s.fld - len(s.fields())
	switch {
	case !s.MCPShown() || i < 0:
		return MCPRow{}, false
	case i == 0:
		return MCPRow{Global: true}, true
	case i <= len(s.mcp.Projects):
		return MCPRow{Project: s.mcp.Projects[i-1].Project}, true
	}
	return MCPRow{Add: true}, true
}

// mcpKey handles a key on an MCP row other than a move, Tab or Esc, which Key handles.
func (s *Settings) mcpKey(key string) SettingsResult {
	row, _ := s.MCPChoice()
	switch key {
	case "Enter":
		return SettingsMCPEdit
	case "BSpace":
		if !row.Add {
			return SettingsMCPRemove
		}
	case "r":
		return SettingsMCPReload
	}
	return SettingsKeep
}

// mcpLines are the rows under the Mode field: Global, the header, the projects windowed
// around the cursor, the add row, then what a new session of the row under the cursor gets.
func (s *Settings) mcpLines(w, h int, pal palette) []string {
	row, onRow := s.MCPChoice()
	wide := w >= 60
	nameW := runeWidth("Global")
	for _, p := range s.mcp.Projects {
		nameW = max(nameW, runeWidth(p.Project))
	}
	nameW = max(min(nameW, w/3), 6)
	first := len(s.fields())
	line := func(i int, name, state string) string {
		cursor, label := " ", truncate(name, nameW)
		label += strings.Repeat(" ", max(nameW-runeWidth(label), 0))
		if s.fld == first+i {
			cursor, label = pal.blue.Render("›"), pal.bold.Render(label)
		}
		return truncate(" "+cursor+" "+label+"  "+state, w)
	}
	head := pal.bold.Render("project servers") + pal.dim.Render(fmt.Sprintf(" · %d of %d projects have their own", len(s.mcp.Projects), s.mcpTotal))
	if !wide {
		head = pal.bold.Render("project servers") + pal.dim.Render(fmt.Sprintf(" · %d of %d", len(s.mcp.Projects), s.mcpTotal))
	}
	out := []string{"", line(0, "Global", s.mcpState("", s.mcp.Global, wide, pal)), "", truncate(" "+head, w), ""}
	var rows []string
	for i, p := range s.mcp.Projects {
		rows = append(rows, line(i+1, p.Project, s.mcpState(p.Project, p.MCPSet, wide, pal)))
	}
	add := "   " + mcpAdd
	if onRow && row.Add {
		add = " " + pal.blue.Render("›") + " " + pal.bold.Render(mcpAdd)
	}
	rows = append(rows, add)
	preview := s.mcpPreview(row, onRow, w, wide, pal)
	rows = window(rows, max(s.fld-first-1, 0), max(h-len(out)-len(preview), 3), pal)
	return append(append(out, rows...), preview...)
}

// mcpState is a row's state: a refused save, a set skipped at launch, or its servers.
func (s *Settings) mcpState(scope string, set MCPSet, wide bool, pal palette) string {
	if e := s.mcpErrs[scope]; e != "" {
		return pal.red.Render("✗ not saved") + "  " + pal.dim.Render(e)
	}
	if set.Error != "" {
		return pal.red.Render("✗ skipped") + "  " + pal.dim.Render(set.Error)
	}
	if len(set.Servers) == 0 {
		return pal.dim.Render("none")
	}
	n := fmt.Sprintf("%d servers", len(set.Servers))
	if len(set.Servers) == 1 {
		n = "1 server"
	}
	if !wide {
		return pal.green.Render(n)
	}
	names := make([]string, len(set.Servers))
	for i, sv := range set.Servers {
		names[i] = sv.Name
	}
	return pal.green.Render(n) + "  " + pal.dim.Render(strings.Join(names, " · "))
}

// mcpPreview is what a new session of the row under the cursor starts with, and where each
// server comes from; nil off the rows and on the add row.
func (s *Settings) mcpPreview(row MCPRow, onRow bool, w int, wide bool, pal palette) []string {
	if !onRow || row.Add {
		return nil
	}
	type entry struct {
		MCPServer
		from     string
		replaces bool
	}
	var list []entry
	own := map[string]bool{}
	var proj MCPSet
	for _, p := range s.mcp.Projects {
		if p.Project == row.Project {
			proj = p.MCPSet
		}
	}
	for _, sv := range proj.Servers {
		own[sv.Name] = true
	}
	for _, sv := range s.mcp.Global.Servers {
		if !own[sv.Name] {
			list = append(list, entry{sv, "global", false})
		}
	}
	globalNames := map[string]bool{}
	for _, sv := range s.mcp.Global.Servers {
		globalNames[sv.Name] = true
	}
	for _, sv := range proj.Servers {
		list = append(list, entry{sv, row.Project, globalNames[sv.Name]})
	}
	title := "Every new session starts with"
	if !row.Global {
		title = "A new session in " + row.Project + " starts with"
	}
	out := []string{"", truncate(" "+pal.dim.Render(title), w)}
	if len(list) == 0 {
		return append(out, "   "+pal.dim.Render("no MCP servers from bx"))
	}
	if !wide {
		byFrom := map[string][]string{}
		var order []string
		for _, e := range list {
			if _, ok := byFrom[e.from]; !ok {
				order = append(order, e.from)
			}
			byFrom[e.from] = append(byFrom[e.from], e.Name)
		}
		for _, f := range order {
			out = append(out, truncate(" "+pal.dim.Render(strings.Join(byFrom[f], ", ")+" · "+f), w))
		}
		return out
	}
	nameW := 0
	for _, e := range list {
		nameW = max(nameW, runeWidth(e.Name))
	}
	targetW := max(w-3-nameW-2-7-2-len(row.Project)-len("  replaces global"), 10)
	for _, e := range list {
		target := truncate(e.Target, targetW)
		l := "   " + e.Name + strings.Repeat(" ", nameW-runeWidth(e.Name)+2) +
			e.Type + strings.Repeat(" ", max(7-runeWidth(e.Type), 1)) +
			pal.dim.Render(target+strings.Repeat(" ", max(targetW-runeWidth(target), 0))) + "  " + e.from
		if e.replaces {
			l += "  " + pal.amber.Render("replaces global")
		}
		out = append(out, truncate(l, w))
	}
	return out
}

// window keeps at most avail rows around cursor, with "… n more" in place of the first or last
// shown row when rows are hidden that way.
func window(rows []string, cursor, avail int, pal palette) []string {
	if len(rows) <= avail {
		return rows
	}
	start := min(max(cursor-(avail-1)/2, 0), len(rows)-avail)
	end := start + avail
	shown := slices.Clone(rows[start:end])
	more := func(n int) string { return "     " + pal.dim.Render(fmt.Sprintf("… %d more", n)) }
	if start > 0 {
		shown[0] = more(start + 1)
	}
	if end < len(rows) {
		shown[len(shown)-1] = more(len(rows) - end + 1)
	}
	return shown
}

// SettingsMCPBar is the key bar while the cursor is on an MCP row.
func SettingsMCPBar(narrow bool) string {
	if narrow {
		return "⏎ edit · ⌫ remove · esc"
	}
	return "j/k move · ⏎ edit in $EDITOR · ⌫ remove · r reload · tab section · esc close"
}
