package ui

import (
	"fmt"
	"slices"
	"strings"
)

// gitSection is the ID of the settings section of the base branch overrides. The server does
// not send it: Load adds it after the server's sections, so the phone never shows it.
const gitSection = "git"

// gitAdd is the last row of the git section.
const gitAdd = "+ add override"

// gitEmpty is shown above the add row when no project has an override.
const gitEmpty = "No overrides. New stories start from each repo's default branch."

// GitOverride is a project whose new stories start from Base instead of the remote default.
type GitOverride struct {
	Project, Base string
}

// SetGitOverrides shows the projects with a base override, sorted by name. The cursor stays on
// its project, or on the add row.
func (s *Settings) SetGitOverrides(list []GitOverride) {
	project, add := s.GitChoice()
	s.git = slices.Clone(list)
	slices.SortFunc(s.git, func(a, b GitOverride) int { return strings.Compare(a.Project, b.Project) })
	if !s.GitShown() {
		return
	}
	switch i := slices.IndexFunc(s.git, func(o GitOverride) bool { return o.Project == project }); {
	case add:
		s.fld = len(s.git)
	case i >= 0:
		s.fld = i
	default:
		s.fld = min(s.fld, len(s.git))
	}
}

// GitShown reports whether the git section is open.
func (s *Settings) GitShown() bool {
	return s.sec < len(s.sections) && s.sections[s.sec].ID == gitSection
}

// GitChoice is the row under the cursor of the git section: an override's project, or add on
// the add row. Both are empty when the section is not open.
func (s *Settings) GitChoice() (project string, add bool) {
	switch {
	case !s.GitShown():
		return "", false
	case s.fld < len(s.git):
		return s.git[s.fld].Project, false
	}
	return "", true
}

// gitKey handles a key in the git section: j/k move, Enter chooses, Backspace removes the
// override under the cursor, Tab goes to the next section and Esc closes.
func (s *Settings) gitKey(key string) SettingsResult {
	switch key {
	case "Esc", "C-c":
		return SettingsClose
	case "Tab":
		s.sec, s.fld = (s.sec+1)%len(s.sections), 0
	case "j", "Down", "C-j":
		s.fld = min(s.fld+1, len(s.git))
	case "k", "Up", "C-k":
		s.fld = max(s.fld-1, 0)
	case "Enter":
		return SettingsGitChoose
	case "BSpace":
		if _, add := s.GitChoice(); !add {
			return SettingsGitRemove
		}
	}
	return SettingsKeep
}

// gitLines is the git section fitted to w and to at most h lines: the header, then the
// overrides "› api-server  develop" windowed around the cursor, and the add row.
func (s *Settings) gitLines(w, h int, pal palette) []string {
	out := []string{truncate(pal.bold.Render("git")+pal.dim.Render(" · base branch overrides"), w), ""}
	if len(s.git) == 0 {
		for _, l := range wrap(gitEmpty, w-3, pal.dim) {
			out = append(out, "   "+l)
		}
	}
	nameW := 0
	for _, o := range s.git {
		nameW = max(nameW, runeWidth(o.Project))
	}
	nameW = max(min(nameW, w-3-2-10), 1)
	var rows []string
	for i, o := range s.git {
		cursor, name, base := " ", truncate(o.Project, nameW), o.Base
		name += strings.Repeat(" ", max(nameW-runeWidth(name), 0))
		if i == s.fld {
			cursor, name, base = pal.blue.Render("›"), pal.bold.Render(name), pal.blue.Render(base)
		}
		rows = append(rows, truncate(" "+cursor+" "+name+"  "+base, w))
	}
	add := "   " + gitAdd
	if s.fld >= len(s.git) {
		add = " " + pal.blue.Render("›") + " " + pal.bold.Render(gitAdd)
	}
	rows = append(rows, truncate(add, w))
	if avail := max(h-len(out), 3); len(rows) > avail {
		start := min(max(s.fld-(avail-1)/2, 0), len(rows)-avail)
		end := start + avail
		shown := slices.Clone(rows[start:end])
		more := func(n int) string { return "     " + pal.dim.Render(fmt.Sprintf("… %d more", n)) }
		if start > 0 {
			shown[0] = more(start + 1)
		}
		if end < len(rows) {
			shown[len(shown)-1] = more(len(rows) - end + 1)
		}
		rows = shown
	}
	return append(out, rows...)
}

// SettingsGitBar is the key bar of the settings screen while the git section is open.
func SettingsGitBar(narrow bool) string {
	if narrow {
		return "⏎ choose · ⌫ remove · esc"
	}
	return "j/k move · ⏎ choose · ⌫ remove · esc back"
}
