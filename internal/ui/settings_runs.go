package ui

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/dinhhuy258/bx/internal/ports"
	"github.com/dinhhuy258/bx/internal/runcfg"
	"github.com/dinhhuy258/bx/internal/settings"
)

const runsSection = "runs"

// RunsProject is a row of the runs section: a project and its run names.
type RunsProject struct {
	Project string
	Runs    []string
}

// RunsConfig is a project's page: what GET runconfig sent.
type RunsConfig struct {
	Runs        []runcfg.Run
	Hide, Show  []int
	Repos       []string
	HasComments bool
	Error       string              // the file does not parse
	Errors      []runcfg.FieldError // why the last save was refused
}

type runsState struct {
	projects []RunsProject
	page     string // the open project; "" shows the projects
	cfg      RunsConfig
	form     *runForm // Task 19
}

// The rows of a page after its runs.
const (
	runsAdd  = -1
	runsHide = -2
	runsShow = -3
)

// RunsShown reports whether the runs section is open.
func (s *Settings) RunsShown() bool {
	return s.sec < len(s.sections) && s.sections[s.sec].ID == runsSection
}

// SetRunsProjects fills the project rows.
func (s *Settings) SetRunsProjects(ps []RunsProject) {
	s.runs.projects = ps
	if s.RunsShown() && s.runs.page == "" {
		s.fld = min(s.fld, max(len(ps)-1, 0))
	}
}

// ShowRuns opens the runs section, on project's row.
func (s *Settings) ShowRuns(project string) {
	if i := slices.IndexFunc(s.sections, func(sec settings.Section) bool { return sec.ID == runsSection }); i >= 0 {
		s.sec = i
	}
	s.fld = max(slices.IndexFunc(s.runs.projects, func(p RunsProject) bool { return p.Project == project }), 0)
}

// RunsProject is the open page's project, else the project under the cursor; "" on a page
// that is not loaded yet and with no projects.
func (s *Settings) RunsProject() string {
	if s.runs.page != "" {
		return s.runs.page
	}
	if s.RunsShown() && s.fld < len(s.runs.projects) {
		return s.runs.projects[s.fld].Project
	}
	return ""
}

// SetRunsPage opens project's page on cfg, keeping the cursor when it is the same page.
func (s *Settings) SetRunsPage(project string, cfg RunsConfig) {
	if s.runs.page != project {
		s.fld = 0
	}
	s.runs.page, s.runs.cfg = project, cfg
	s.fld = min(s.fld, max(len(s.runsRows())-1, 0))
}

// runsRows are the cursor's stops: project indexes, or on a page run indexes then
// runsAdd, runsHide, runsShow. A broken file has none.
func (s *Settings) runsRows() []int {
	var rows []int
	if s.runs.page == "" {
		for i := range s.runs.projects {
			rows = append(rows, i)
		}
		return rows
	}
	if s.runs.cfg.Error != "" {
		return nil
	}
	for i := range s.runs.cfg.Runs {
		rows = append(rows, i)
	}
	return append(rows, runsAdd, runsHide, runsShow)
}

func (s *Settings) runsRow() (int, bool) {
	rows := s.runsRows()
	if s.fld >= len(rows) {
		return 0, false
	}
	return rows[s.fld], true
}

// PageRun is the run under the cursor of a page.
func (s *Settings) PageRun() (int, bool) {
	r, ok := s.runsRow()
	return r, ok && s.runs.page != "" && r >= 0
}

// RunsPortsRow is "hide" or "show" when the cursor is on that port row, with its list.
func (s *Settings) RunsPortsRow() (string, []int) {
	r, ok := s.runsRow()
	switch {
	case !ok || s.runs.page == "":
	case r == runsHide:
		return "hide", s.runs.cfg.Hide
	case r == runsShow:
		return "show", s.runs.cfg.Show
	}
	return "", nil
}

// SetRunsPorts sets the hide or show list of the page.
func (s *Settings) SetRunsPorts(which string, ports []int) {
	if which == "hide" {
		s.runs.cfg.Hide = ports
	} else {
		s.runs.cfg.Show = ports
	}
}

// RemoveRun drops run i from the page; RunsSave then holds the config without it.
func (s *Settings) RemoveRun(i int) {
	if i >= 0 && i < len(s.runs.cfg.Runs) {
		s.runs.cfg.Runs = slices.Delete(slices.Clone(s.runs.cfg.Runs), i, i+1)
	}
	s.fld = min(s.fld, max(len(s.runsRows())-1, 0))
}

// RunsSave is the page's config to send.
func (s *Settings) RunsSave() RunsConfig { return s.runs.cfg }

// runsKey handles a key in the runs section. The run form has its own keys (Task 19).
func (s *Settings) runsKey(key string) SettingsResult {
	if s.runs.form != nil {
		return s.runFormKey(key)
	}
	rows := s.runsRows()
	switch key {
	case "C-c":
		return SettingsClose
	case "Esc":
		if s.runs.page == "" {
			return SettingsClose
		}
		back := s.runs.page
		s.runs.page, s.runs.cfg = "", RunsConfig{}
		s.fld = max(slices.IndexFunc(s.runs.projects, func(p RunsProject) bool { return p.Project == back }), 0)
		return SettingsKeep
	case "Tab":
		if s.runs.page == "" {
			s.sec, s.fld = (s.sec+1)%len(s.sections), 0
		}
	case "j", "Down", "C-j":
		s.fld = min(s.fld+1, max(len(rows)-1, 0))
	case "k", "Up", "C-k":
		s.fld = max(s.fld-1, 0)
	case "e":
		if s.runs.page != "" {
			return SettingsRunsEditText
		}
	case "BSpace":
		if _, ok := s.PageRun(); ok {
			return SettingsRunsRemove
		}
	case "Enter":
		if s.runs.page == "" {
			if len(s.runs.projects) > 0 {
				return SettingsRunsOpen
			}
			return SettingsKeep
		}
		if s.runs.cfg.Error != "" {
			return SettingsRunsEditText
		}
		r, ok := s.runsRow()
		switch {
		case !ok:
		case r == runsHide || r == runsShow:
			return SettingsRunsPorts
		case r == runsAdd:
			s.openRunForm(runcfg.Run{}, -1)
			return SettingsRunsForm
		default:
			s.openRunForm(s.runs.cfg.Runs[r], r)
			return SettingsRunsForm
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

// runsLines is the runs section fitted to w and h: the project rows, or a page.
func (s *Settings) runsLines(w, h int, narrow bool, pal palette) []string {
	if s.runs.form != nil {
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
	if s.runs.page == "" {
		if !narrow {
			lines = append(lines, " "+pal.group.Render("Runs"), " "+pal.dim.Render("What M-b runs in a story, and which ports it lists. ⏎ opens a project."), "")
		}
		for i, p := range s.runs.projects {
			runs := pal.dim.Render("no runs")
			if len(p.Runs) > 0 {
				runs = strings.Join(p.Runs, ", ")
			}
			row(i, p.Project, runs)
		}
		return lines
	}
	cfg := s.runs.cfg
	lines = append(lines, " "+pal.group.Render("Runs › "+s.runs.page), "")
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

// SettingsRunsBar is the key bar of the runs section.
func (s *Settings) SettingsRunsBar(bool) string {
	switch {
	case s.runs.form != nil:
		return s.runFormBar()
	case s.runs.page == "":
		return "⏎ open · tab section · esc close"
	case s.runs.cfg.Error != "":
		return "⏎ edit the text · esc back"
	}
	return "⏎ edit · ⌫ remove · e edit the text · esc back"
}

// runForm is the run being edited on an runs page.
type runForm struct {
	run, orig runcfg.Run
	index     int // in the page's runs; -1 = new
	where     int // whereHost, whereImage, whereBuild
	fld       int // in visible()
	errs      []runcfg.FieldError
}

const (
	whereHost = iota
	whereImage
	whereBuild
)

var whereNames = []string{"host", "image", "build"}

// The fields of the run form.
const (
	rfName = iota
	rfWhere
	rfImage
	rfBuild
	rfCmd
	rfDir
	rfEnv
	rfSetup
	rfSetupImage
	rfCreates
	rfStop
	rfOpen
	rfPort
	rfCache
	rfFields
)

var rfLabels = [rfFields]string{"Name", "Runs on", "Image", "Dockerfile", "Command", "Folder", "Env", "Setup", "Setup image", "Creates", "Stop", "Opens", "Ports", "Cache"}

// rfKeys are the run config keys of the fields, to match field errors.
var rfKeys = [rfFields]string{"name", "", "image", "build", "cmd", "dir", "env", "setup", "setup", "creates", "stop", "open", "port", "cache"}

func (s *Settings) openRunForm(r runcfg.Run, index int) {
	f := &runForm{run: r, orig: r, index: index}
	switch {
	case r.Build != "":
		f.where = whereBuild
	case r.Image != "":
		f.where = whereImage
	}
	s.runs.form = f
}

// CloseRunForm goes back to the page.
func (s *Settings) CloseRunForm() { s.runs.form = nil }

// SetRunFormErrors shows why a save was refused, under the form.
func (s *Settings) SetRunFormErrors(errs []runcfg.FieldError) {
	if s.runs.form != nil {
		s.runs.form.errs = errs
	}
}

// visible are the form's fields for where the run runs.
func (f *runForm) visible() []int {
	out := []int{rfName, rfWhere}
	switch f.where {
	case whereImage:
		out = append(out, rfImage)
	case whereBuild:
		out = append(out, rfBuild)
	}
	out = append(out, rfCmd, rfDir, rfEnv, rfSetup)
	if f.where == whereHost {
		out = append(out, rfSetupImage) // a host run may set up in a container
	}
	out = append(out, rfCreates, rfStop, rfOpen, rfPort)
	if f.where != whereHost {
		out = append(out, rfCache)
	}
	return out
}

func (f *runForm) field() int { v := f.visible(); return v[min(f.fld, len(v)-1)] }

func (f *runForm) value(k int) string {
	r := f.run
	switch k {
	case rfName:
		return r.Name
	case rfWhere:
		return whereNames[f.where]
	case rfImage:
		return r.Image
	case rfBuild:
		if r.Build == "" {
			return ""
		}
		return fmt.Sprintf("%d lines", strings.Count(strings.TrimRight(r.Build, "\n"), "\n")+1)
	case rfCmd:
		return r.Cmd
	case rfDir:
		return r.Dir
	case rfEnv:
		return envText(r.Env)
	case rfSetup:
		return r.Setup.Cmd
	case rfSetupImage:
		return r.Setup.Image
	case rfCreates:
		return r.Creates
	case rfStop:
		return r.Stop
	case rfOpen:
		return r.Open
	case rfPort:
		return portList(r.Port)
	case rfCache:
		return strings.Join(r.Cache, ", ")
	}
	return ""
}

// envText is "A=1 B=2", sorted.
func envText(env map[string]string) string {
	keys := slices.Sorted(maps.Keys(env))
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + "=" + env[k]
	}
	return strings.Join(parts, " ")
}

// RunsFormText is the text field under the cursor of the run form: its key, label and value
// ("" for an empty list); ok is false on a choice or the Dockerfile.
func (s *Settings) RunsFormText() (key int, label, value string, ok bool) {
	f := s.runs.form
	if f == nil {
		return 0, "", "", false
	}
	k := f.field()
	if k == rfWhere || k == rfBuild || k == rfDir && len(s.runs.cfg.Repos) > 0 {
		return 0, "", "", false
	}
	v := f.value(k)
	if v == "—" {
		v = ""
	}
	return k, rfLabels[k], v, true
}

// SetRunsFormText sets text field key of the run form. A port or env list that does not read
// returns false and changes nothing.
func (s *Settings) SetRunsFormText(key int, v string) bool {
	f := s.runs.form
	if f == nil {
		return false
	}
	r := &f.run
	switch key {
	case rfName:
		r.Name = v
	case rfImage:
		r.Image = v
	case rfCmd:
		r.Cmd = v
	case rfDir:
		r.Dir = v
	case rfEnv:
		env := map[string]string{}
		for _, kv := range strings.Fields(v) {
			k, val, ok := strings.Cut(kv, "=")
			if !ok || k == "" {
				return false
			}
			env[k] = val
		}
		r.Env = env
		if len(env) == 0 {
			r.Env = nil
		}
	case rfSetup:
		r.Setup.Cmd = v
	case rfSetupImage:
		r.Setup.Image = v
	case rfCreates:
		r.Creates = v
	case rfStop:
		r.Stop = v
	case rfOpen:
		r.Open = v
	case rfPort:
		ps, ok := ParsePorts(v)
		if !ok {
			return false
		}
		r.Port = runcfg.Ports(ps)
	case rfCache:
		r.Cache = nil
		for _, c := range strings.Split(v, ",") {
			if c = strings.TrimSpace(c); c != "" {
				r.Cache = append(r.Cache, c)
			}
		}
	}
	return true
}

// RunsBuild is the run form's Dockerfile; SetRunsBuild replaces it.
func (s *Settings) RunsBuild() string {
	if s.runs.form == nil {
		return ""
	}
	return s.runs.form.run.Build
}

func (s *Settings) SetRunsBuild(text string) {
	if s.runs.form != nil {
		s.runs.form.run.Build = text
	}
}

func (f *runForm) changed() bool {
	return f.index < 0 || !reflect.DeepEqual(f.run, f.orig)
}

// finish is the run as the form leaves it: the fields of other places cleared.
func (f *runForm) finish() runcfg.Run {
	r := f.run
	switch f.where {
	case whereHost:
		r.Image, r.Build, r.Cache = "", "", nil
	case whereImage:
		r.Build, r.Setup.Image = "", ""
	case whereBuild:
		r.Image, r.Setup.Image = "", ""
	}
	return r
}

// runFormKey: j/k move, h/l step a choice, Enter edits, C-s saves into the page's config,
// Esc goes back (SettingsRunsDiscard first when there are edits).
func (s *Settings) runFormKey(key string) SettingsResult {
	f := s.runs.form
	v := f.visible()
	switch key {
	case "C-c":
		return SettingsClose
	case "Esc":
		if f.changed() {
			return SettingsRunsDiscard
		}
		s.runs.form = nil
	case "j", "Down", "C-j":
		f.fld = min(f.fld+1, len(v)-1)
	case "k", "Up", "C-k":
		f.fld = max(f.fld-1, 0)
	case "h", "Left", "l", "Right":
		d := 1
		if key == "h" || key == "Left" {
			d = -1
		}
		switch f.field() {
		case rfWhere:
			f.where = (f.where + d + len(whereNames)) % len(whereNames)
		case rfDir:
			if repos := s.runs.cfg.Repos; len(repos) > 0 {
				opts := append([]string{""}, repos...)
				i := max(slices.Index(opts, f.run.Dir), 0)
				f.run.Dir = opts[(i+d+len(opts))%len(opts)]
			}
		}
	case "Enter":
		switch k := f.field(); {
		case k == rfBuild:
			return SettingsRunsBuild
		case k == rfWhere:
		default:
			if _, _, _, ok := s.RunsFormText(); ok {
				return SettingsRunsText
			}
		}
	case "C-s":
		runs := slices.Clone(s.runs.cfg.Runs)
		if f.index < 0 {
			runs = append(runs, f.finish())
		} else {
			runs[f.index] = f.finish()
		}
		s.runs.cfg.Runs = runs
		if f.index < 0 {
			f.index = len(runs) - 1 // a refused save keeps editing this one
		}
		return SettingsRunsSave
	}
	return SettingsKeep
}

// runFormLines draws the form, the command it runs, and the refused fields.
func (s *Settings) runFormLines(w, h int, pal palette) []string {
	f := s.runs.form
	title := f.run.Name
	if title == "" {
		title = "new run"
	}
	lines := []string{" " + pal.group.Render("Runs › "+s.runs.page+" › "+title), ""}
	for i, k := range f.visible() {
		val := f.value(k)
		switch {
		case k == rfWhere || k == rfDir && len(s.runs.cfg.Repos) > 0:
			if val == "" {
				val = "."
			}
			val = "◂ " + val + " ▸"
		case val == "":
			val = pal.dim.Render("—")
		}
		label := fmt.Sprintf("%-12s", rfLabels[k])
		mark := "  "
		if i == f.fld {
			mark, label = pal.arrow.Render("▸ "), pal.bold.Render(label)
		}
		lines = append(lines, truncate(" "+mark+label+val, w))
	}
	lines = append(lines, " "+pal.dim.Render(strings.Repeat("─", min(w-2, 40))))
	lines = append(lines, truncate(" "+pal.dim.Render("$ "+preview(f.finish())), w))
	for _, e := range f.errs {
		label := e.Field
		if i := slices.Index(rfKeys[:], e.Field); i >= 0 {
			label = rfLabels[i]
		}
		lines = append(lines, " "+pal.red.Render("✗ "+label+": "+e.Message))
	}
	return lines[:min(len(lines), h)]
}

// preview is the command line a run types, for the form: env, then the command, in the dir.
func preview(r runcfg.Run) string {
	cmd := r.Cmd
	if env := envText(r.Env); env != "" {
		cmd = env + " " + cmd
	}
	if r.Dir != "" && r.Dir != "." {
		cmd = "cd " + r.Dir + " && " + cmd
	}
	switch {
	case r.Build != "":
		return "docker run (built image) " + cmd
	case r.Image != "":
		return "docker run " + r.Image + " " + cmd
	}
	return cmd
}

func (s *Settings) runFormBar() string {
	return "⏎ edit · ◂ ▸ choose · ^s save · esc back"
}
