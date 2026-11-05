package ui

import (
	"slices"
	"strconv"
	"strings"

	"github.com/dinhhuy258/bx/internal/ports"
	"github.com/dinhhuy258/bx/internal/runcfg"
	"github.com/dinhhuy258/bx/internal/settings"
)

const appsSection = "apps"

// AppsProject is a row of the apps section: a project and its run names.
type AppsProject struct {
	Project string
	Runs    []string
}

// AppsConfig is a project's page: what GET runconfig sent.
type AppsConfig struct {
	Runs        []runcfg.Run
	Hide, Show  []int
	Repos       []string
	HasComments bool
	Error       string              // the file does not parse
	Errors      []runcfg.FieldError // why the last save was refused
}

type appsState struct {
	projects []AppsProject
	page     string // the open project; "" shows the projects
	cfg      AppsConfig
	form     *runForm // Task 19
}

// The rows of a page after its runs.
const (
	appsAdd  = -1
	appsHide = -2
	appsShow = -3
)

// AppsShown reports whether the apps section is open.
func (s *Settings) AppsShown() bool {
	return s.sec < len(s.sections) && s.sections[s.sec].ID == appsSection
}

// SetAppsProjects fills the project rows.
func (s *Settings) SetAppsProjects(ps []AppsProject) {
	s.apps.projects = ps
	if s.AppsShown() && s.apps.page == "" {
		s.fld = min(s.fld, max(len(ps)-1, 0))
	}
}

// ShowApps opens the apps section, on project's row.
func (s *Settings) ShowApps(project string) {
	if i := slices.IndexFunc(s.sections, func(sec settings.Section) bool { return sec.ID == appsSection }); i >= 0 {
		s.sec = i
	}
	s.fld = max(slices.IndexFunc(s.apps.projects, func(p AppsProject) bool { return p.Project == project }), 0)
}

// AppsProject is the open page's project, else the project under the cursor; "" on a page
// that is not loaded yet and with no projects.
func (s *Settings) AppsProject() string {
	if s.apps.page != "" {
		return s.apps.page
	}
	if s.AppsShown() && s.fld < len(s.apps.projects) {
		return s.apps.projects[s.fld].Project
	}
	return ""
}

// SetAppsPage opens project's page on cfg, keeping the cursor when it is the same page.
func (s *Settings) SetAppsPage(project string, cfg AppsConfig) {
	if s.apps.page != project {
		s.fld = 0
	}
	s.apps.page, s.apps.cfg = project, cfg
	s.fld = min(s.fld, max(len(s.appsRows())-1, 0))
}

// appsRows are the cursor's stops: project indexes, or on a page run indexes then
// appsAdd, appsHide, appsShow. A broken file has none.
func (s *Settings) appsRows() []int {
	var rows []int
	if s.apps.page == "" {
		for i := range s.apps.projects {
			rows = append(rows, i)
		}
		return rows
	}
	if s.apps.cfg.Error != "" {
		return nil
	}
	for i := range s.apps.cfg.Runs {
		rows = append(rows, i)
	}
	return append(rows, appsAdd, appsHide, appsShow)
}

func (s *Settings) appsRow() (int, bool) {
	rows := s.appsRows()
	if s.fld >= len(rows) {
		return 0, false
	}
	return rows[s.fld], true
}

// AppsRun is the run under the cursor of a page.
func (s *Settings) AppsRun() (int, bool) {
	r, ok := s.appsRow()
	return r, ok && s.apps.page != "" && r >= 0
}

// AppsPortsRow is "hide" or "show" when the cursor is on that port row, with its list.
func (s *Settings) AppsPortsRow() (string, []int) {
	r, ok := s.appsRow()
	switch {
	case !ok || s.apps.page == "":
	case r == appsHide:
		return "hide", s.apps.cfg.Hide
	case r == appsShow:
		return "show", s.apps.cfg.Show
	}
	return "", nil
}

// SetAppsPorts sets the hide or show list of the page.
func (s *Settings) SetAppsPorts(which string, ports []int) {
	if which == "hide" {
		s.apps.cfg.Hide = ports
	} else {
		s.apps.cfg.Show = ports
	}
}

// RemoveRun drops run i from the page; AppsSave then holds the config without it.
func (s *Settings) RemoveRun(i int) {
	if i >= 0 && i < len(s.apps.cfg.Runs) {
		s.apps.cfg.Runs = slices.Delete(slices.Clone(s.apps.cfg.Runs), i, i+1)
	}
	s.fld = min(s.fld, max(len(s.appsRows())-1, 0))
}

// AppsSave is the page's config to send.
func (s *Settings) AppsSave() AppsConfig { return s.apps.cfg }

// appsKey handles a key in the apps section. The run form has its own keys (Task 19).
func (s *Settings) appsKey(key string) SettingsResult {
	if s.apps.form != nil {
		return s.runFormKey(key)
	}
	rows := s.appsRows()
	switch key {
	case "C-c":
		return SettingsClose
	case "Esc":
		if s.apps.page == "" {
			return SettingsClose
		}
		back := s.apps.page
		s.apps.page, s.apps.cfg = "", AppsConfig{}
		s.fld = max(slices.IndexFunc(s.apps.projects, func(p AppsProject) bool { return p.Project == back }), 0)
		return SettingsKeep
	case "Tab":
		if s.apps.page == "" {
			s.sec, s.fld = (s.sec+1)%len(s.sections), 0
		}
	case "j", "Down", "C-j":
		s.fld = min(s.fld+1, max(len(rows)-1, 0))
	case "k", "Up", "C-k":
		s.fld = max(s.fld-1, 0)
	case "e":
		if s.apps.page != "" {
			return SettingsAppsEditText
		}
	case "BSpace":
		if _, ok := s.AppsRun(); ok {
			return SettingsAppsRemove
		}
	case "Enter":
		if s.apps.page == "" {
			if len(s.apps.projects) > 0 {
				return SettingsAppsOpen
			}
			return SettingsKeep
		}
		if s.apps.cfg.Error != "" {
			return SettingsAppsEditText
		}
		r, ok := s.appsRow()
		switch {
		case !ok:
		case r == appsHide || r == appsShow:
			return SettingsAppsPorts
		case r == appsAdd:
			s.openRunForm(runcfg.Run{}, -1)
			return SettingsAppsForm
		default:
			s.openRunForm(s.apps.cfg.Runs[r], r)
			return SettingsAppsForm
		}
	}
	return SettingsKeep
}

// where names where a run runs: "host", "image php:8.3" or "build".
func where(r runcfg.Run) string {
	switch {
	case r.Build != "":
		return "build"
	case r.Image != "":
		return "image " + r.Image
	}
	return "host"
}

// portList is "5432 3306" for a port list, "—" for none.
func portList(ps []int) string {
	if len(ps) == 0 {
		return "—"
	}
	parts := make([]string, len(ps))
	for i, p := range ps {
		parts[i] = strconv.Itoa(p)
	}
	return strings.Join(parts, " ")
}

// ParsePorts reads "5432 3306" or "5432, 3306"; ok is false on anything else.
func ParsePorts(text string) ([]int, bool) {
	var out []int
	for _, f := range strings.FieldsFunc(text, func(r rune) bool { return r == ' ' || r == ',' }) {
		n, err := strconv.Atoi(f)
		if err != nil || n < 1 || n > 65535 {
			return nil, false
		}
		out = append(out, n)
	}
	return out, true
}

// appsLines is the apps section fitted to w and h: the project rows, or a page.
func (s *Settings) appsLines(w, h int, narrow bool, pal palette) []string {
	if s.apps.form != nil {
		return s.runFormLines(w, h, pal)
	}
	var lines []string
	row := func(i int, left, right string) {
		mark := "  "
		if i == s.fld {
			mark = pal.arrow.Render("▸ ")
			left = pal.bold.Render(left)
		}
		lines = append(lines, fit(" "+mark+left, right, w))
	}
	if s.apps.page == "" {
		if !narrow {
			lines = append(lines, " "+pal.group.Render("Apps"), " "+pal.dim.Render("What M-b runs in a story, and which ports it lists. ⏎ opens a project."), "")
		}
		for i, p := range s.apps.projects {
			runs := pal.dim.Render("no runs")
			if len(p.Runs) > 0 {
				runs = strings.Join(p.Runs, ", ")
			}
			row(i, p.Project, runs)
		}
		return lines
	}
	cfg := s.apps.cfg
	lines = append(lines, " "+pal.group.Render("Apps › "+s.apps.page), "")
	if cfg.Error != "" {
		return append(lines, " "+pal.red.Render(cfg.Error), "", " "+pal.dim.Render("⏎ or e edit the text"))
	}
	i := 0
	for _, r := range cfg.Runs {
		row(i, pal.green.Render("▶ ")+r.Name+"  "+pal.dim.Render(truncate(r.Cmd, max(w/2, 10))), pal.dim.Render(where(r)))
		i++
	}
	row(i, "+ add run", "")
	i++
	hide := portList(cfg.Hide)
	if len(cfg.Hide) == 0 {
		hide = portList(ports.Hidden) + pal.dim.Render("  (defaults)")
	}
	row(i, "hidden ports", hide)
	i++
	row(i, "shown ports", portList(cfg.Show))
	for _, e := range cfg.Errors {
		lines = append(lines, " "+pal.red.Render(e.Error()))
	}
	if cfg.HasComments {
		lines = append(lines, "", " "+pal.amber.Render("⚠ the file has comments: a save here drops them · e edits the text"))
	}
	return lines
}

// SettingsAppsBar is the key bar of the apps section.
func (s *Settings) SettingsAppsBar(bool) string {
	switch {
	case s.apps.form != nil:
		return s.runFormBar()
	case s.apps.page == "":
		return "⏎ open · tab section · esc close"
	case s.apps.cfg.Error != "":
		return "⏎ edit the text · esc back"
	}
	return "⏎ edit · ⌫ remove · e edit the text · esc back"
}

type runForm struct{}

func (s *Settings) openRunForm(runcfg.Run, int)             {}
func (s *Settings) runFormKey(string) SettingsResult        { s.apps.form = nil; return SettingsKeep }
func (s *Settings) runFormLines(int, int, palette) []string { return nil }
func (s *Settings) runFormBar() string                      { return "" }
func (s *Settings) SetRunFormErrors([]runcfg.FieldError)    {}
func (s *Settings) CloseRunForm()                           { s.apps.form = nil }
