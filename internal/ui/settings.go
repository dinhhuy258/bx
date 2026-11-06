package ui

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/dinhhuy258/bx/internal/settings"
)

// SettingsResult is what a key did to the settings screen.
type SettingsResult int

const (
	SettingsKeep            SettingsResult = iota // still editing
	SettingsSave                                  // Enter: send Changes
	SettingsClose                                 // Esc on the section list: the caller asks first when Changes is not empty
	SettingsSelect                                // Space changed the sync selection: save SyncInclude
	SettingsRescan                                // r: scan and plan again
	SettingsUpToDate                              // s with nothing to sync
	SettingsNothingSelected                       // s with no item selected
	SettingsSyncApply                             // y in the confirmation: sync SyncScope
	SettingsGitChoose                             // Enter in the git section: read GitRow
	SettingsGitRemove                             // Backspace on a git row: remove GitRow's
	SettingsEdit                                  // Enter on a text field: edit TextField in a prompt
	SettingsMCPEdit                               // Enter on an MCP row: edit MCPChoice's servers (or add a project)
	SettingsMCPRemove                             // Backspace on an MCP row: remove its servers, or drop its draft
	SettingsMCPReload                             // r on an MCP row: read the servers again
	SettingsRuleUp                                // K on an account rule: move it up
	SettingsRuleDown                              // J on an account rule: move it down
	SettingsAccountsReload                        // r in the git section: read the accounts and gh's logins again
	SettingsAccountText                           // Enter on a text field of the account form: edit AccountFormText in a prompt
	SettingsAccountSave                           // C-s in the account form: save AccountForm
	SettingsAccountBack                           // Esc in the account form: the caller asks first when it has edits
	SettingsRunsOpen                              // Enter on an runs project row: load RunsProject's page
	SettingsRunsEditText                          // e on an runs page (Enter on a broken one): edit the file in $EDITOR
	SettingsRunsRemove                            // Backspace on a run: ask, then RemoveRun and save RunsSave
	SettingsRunsPorts                             // Enter on a port row: edit RunsPortsRow in a prompt
	SettingsRunsForm                              // Enter on a run or + add run: the run form is open (Task 19)
	SettingsRunsSave                              // the run form saved (C-s): save RunsSave
	SettingsRunsText                              // Enter on a text field of the run form: edit RunsFormText in a prompt
	SettingsRunsBuild                             // Enter on the Dockerfile field: edit it in $EDITOR
	SettingsRunsDiscard                           // Esc in the run form with edits: ask first
)

// Settings is the settings screen. It knows no setting by name: it draws the sections and
// fields the server sent. Edits stay here until they are saved. The Claude section also shows
// the sync list below its fields; fld is the cursor over the fields, then the list's rows.
// The Git section is the client's own (see settings_git.go); there fld is the cursor over its rows.
// The focus starts on the section list; Enter moves it into the section, Esc back.
type Settings struct {
	Server   string // the server's host, for the header
	sections []settings.Section
	saved    map[string]string // what the server holds; a missing key is the default
	edits    map[string]string // changed keys only
	sec, fld int
	open     bool // the focus is in the section, not on the section list
	runs     runsState // the runs section (settings_runs.go)
	sync     syncList
	git      []GitOverride     // the git section's base overrides, by project
	acc      GitAccounts       // the git section's GitHub accounts
	accErr   string            // why the accounts could not be read or saved
	form     *accountForm      // the account being edited in the git section; nil shows the rows
	mcp      MCP               // the MCP section's rows (see mcp.go)
	mcpTotal int               // every project, for "3 of 34"
	mcpErrs  map[string]string // by scope ("" = global): why the last save was refused
}

// NewSettings starts an empty screen; Load fills it.
func NewSettings(server string) *Settings {
	return &Settings{Server: server, saved: map[string]string{}, edits: map[string]string{}, mcpErrs: map[string]string{}}
}

// Loaded reports whether the server's sections have arrived.
func (s *Settings) Loaded() bool { return len(s.sections) > 0 }

// Load takes what the server holds, after a GET or a save, and adds the git section after the
// claude section (else after the server's sections). Edits that now match it are dropped.
func (s *Settings) Load(sections []settings.Section, values map[string]string) {
	if len(sections) > 0 {
		at := len(sections)
		if i := slices.IndexFunc(sections, func(sec settings.Section) bool { return sec.ID == syncSection }); i >= 0 {
			at = i + 1
		}
		s.sections = slices.Insert(slices.Clone(sections), at, settings.Section{ID: gitSection, Title: "Git"}, settings.Section{ID: runsSection, Title: "Runs"})
	}
	s.saved = maps.Clone(values)
	if s.saved == nil {
		s.saved = map[string]string{}
	}
	maps.DeleteFunc(s.edits, func(k, v string) bool { return s.saved[k] == v })
	s.sec = min(s.sec, max(len(s.sections)-1, 0))
	s.fld = min(s.fld, max(s.rowCount()-1, 0))
}

// rowCount is the number of rows the cursor moves through in the open section.
func (s *Settings) rowCount() int {
	if s.GitShown() {
		return len(s.gitRows())
	}
	if s.RunsShown() {
		return len(s.runsRows())
	}
	return len(s.fields()) + len(s.syncRows()) + s.mcpRowCount()
}

// Changes are the edited keys and their new values.
func (s *Settings) Changes() map[string]string { return maps.Clone(s.edits) }

func (s *Settings) fields() []settings.Field {
	if s.sec >= len(s.sections) {
		return nil
	}
	return s.sections[s.sec].Fields
}

// Value is the edited value of key, else the saved one.
func (s *Settings) Value(key string) string { return s.value(key) }

func (s *Settings) value(key string) string {
	if v, ok := s.edits[key]; ok {
		return v
	}
	return s.saved[key]
}

// field is the field under the cursor, if it is on one.
func (s *Settings) field() (settings.Field, bool) {
	fs := s.fields()
	if s.fld >= len(fs) {
		return settings.Field{}, false
	}
	return fs[s.fld], true
}

// TextField is the field under the cursor if it is edited by typing (a URL or a line).
func (s *Settings) TextField() (settings.Field, bool) {
	f, ok := s.field()
	if !ok || !f.Kind.Typed() {
		return settings.Field{}, false
	}
	return f, true
}

// Key feeds one key. On the section list: j/k pick a section, Enter opens it, Esc closes.
// In a section: j/k field or sync row, h/l the value or a folder's fold, Space a sync
// checkbox, s the sync confirmation, r a rescan, Enter save (on a text field: edit it), Esc
// back to the list. While the confirmation is open only y, n and Esc act. The git and runs
// sections have their own keys (gitKey, runsKey).
func (s *Settings) Key(key string) SettingsResult {
	if !s.open {
		return s.listKey(key)
	}
	if s.sync.confirm {
		return s.confirmKey(key)
	}
	if s.GitShown() {
		return s.gitKey(key)
	}
	if s.RunsShown() {
		return s.runsKey(key)
	}
	if _, ok := s.MCPChoice(); ok && key != "Esc" && !isMove(key) {
		return s.mcpKey(key) // moves and Esc work as on any row
	}
	switch key {
	case "Esc":
		s.open = false
		return SettingsKeep
	case "Enter":
		if _, ok := s.TextField(); ok {
			return SettingsEdit
		}
		return SettingsSave
	}
	_, onField := s.field()
	switch key {
	case "j", "Down", "C-j":
		s.fld = min(s.fld+1, max(s.rowCount()-1, 0))
	case "k", "Up", "C-k":
		s.fld = max(s.fld-1, 0)
	case "h", "Left":
		if onField {
			s.step(-1)
		} else {
			s.fold(true)
		}
	case "l", "Right":
		if onField {
			s.step(1)
		} else {
			s.fold(false)
		}
	case "Space":
		if s.toggle() {
			return SettingsSelect
		}
	case "s":
		switch {
		case !s.syncShown() || s.sync.state != syncReady:
		case len(s.SyncScope()) == 0:
			return SettingsNothingSelected
		case s.SyncChanges() == 0:
			return SettingsUpToDate
		default:
			s.sync.confirm, s.sync.top = true, 0
		}
	case "r":
		if s.syncShown() && s.sync.state != syncLoading {
			return SettingsRescan
		}
	}
	return SettingsKeep
}

// listKey handles a key on the section list. Before the sections load, Enter asks to load
// them again.
func (s *Settings) listKey(key string) SettingsResult {
	switch {
	case key == "Esc":
		return SettingsClose
	case !s.Loaded():
		if key == "Enter" {
			return SettingsSave
		}
	case key == "Enter":
		s.open = true
	case key == "j" || key == "Down" || key == "C-j":
		s.sec, s.fld = min(s.sec+1, len(s.sections)-1), 0
	case key == "k" || key == "Up" || key == "C-k":
		s.sec, s.fld = max(s.sec-1, 0), 0
	}
	return SettingsKeep
}

// InSection reports whether the focus is in the section rather than on the section list.
func (s *Settings) InSection() bool { return s.open }

// at reports whether the row cursor is on row i and drawn: not while the list has the focus.
func (s *Settings) at(i int) bool { return s.open && i == s.fld }

// step moves the selected choice field to the previous or next allowed value, wrapping. No
// value is the field's Default.
func (s *Settings) step(delta int) {
	f, ok := s.field()
	if !ok {
		return
	}
	if f.Kind != settings.Choice || len(f.Choices) == 0 {
		return
	}
	i := max(slices.Index(f.Choices, orDefault(f, s.value(f.Key))), 0)
	v := f.Choices[(i+delta+len(f.Choices))%len(f.Choices)]
	if v == orDefault(f, s.saved[f.Key]) {
		delete(s.edits, f.Key)
	} else {
		s.edits[f.Key] = v
	}
}

// orDefault is v, or the field's Default when v is "".
func orDefault(f settings.Field, v string) string {
	if v == "" {
		return f.Default
	}
	return v
}

func shown(v string) string {
	if v == "" {
		return "default"
	}
	return v
}

// Render draws the screen at w x h: sections on the left and fields on the right, or, when
// narrow, the sections as tabs on the top line. note replaces the fields until they load.
// The section under the cursor is drawn blue while the list has the focus; on a wide screen
// the rule's corner over the divider also opens toward the side that has it.
func (s *Settings) Render(w, h int, plain bool, note string) string {
	const left = 20
	pal := newPalette(plain)
	head := fit(pal.brand.Render("bx")+" "+pal.bold.Render("settings"), pal.dim.Render(s.Server), w)
	var second string
	if IsNarrow(w) {
		second = rule(w, RenderOptions{Plain: plain, Focused: true})
	} else {
		l, r, corner := pal.cyan.Render(strings.Repeat("─", left+1)), pal.dim.Render(strings.Repeat("─", max(w-left-2, 0))), "┐"
		if s.open {
			l, r, corner = pal.dim.Render(strings.Repeat("─", left+1)), pal.cyan.Render(strings.Repeat("─", max(w-left-2, 0))), "┌"
		}
		second = l + pal.cyan.Render(corner) + r
	}
	lines := []string{head, second}
	if !s.Loaded() {
		return pad(append(lines, "", "  "+note), h)
	}
	current := pal.bold.Inherit(pal.blue)
	if s.open {
		current = pal.bold
	}
	if IsNarrow(w) {
		tabs := make([]string, len(s.sections))
		for i, sec := range s.sections {
			tabs[i] = pal.dim.Render(sec.Title)
			if i == s.sec {
				tabs[i] = current.Render(sec.Title)
			}
		}
		lines = append(lines, truncate(" "+strings.Join(tabs, pal.dim.Render(" · ")), w), "")
		if s.GitShown() {
			git := s.gitLines(w, h-len(lines), true, pal)
			if s.form != nil {
				git[0] = truncate(" "+git[0], w) // under the tabs
			}
			return pad(append(lines, git...), h)
		}
		if s.RunsShown() {
			return pad(append(lines, s.runsLines(w, h-len(lines), true, pal)...), h)
		}
		if s.sync.confirm {
			return pad(append(lines, s.confirmLines(w, h-len(lines), pal)...), h)
		}
		lines = append(lines, s.fieldLines(w, pal)...)
		if s.MCPShown() {
			lines = append(lines, s.mcpLines(w, h-len(lines)-len(s.helpLines(w, pal)), pal)...)
		}
		help := s.helpLines(w, pal)
		if s.syncShown() {
			lines = append(lines, s.syncLines(w, h-len(lines)-len(help), pal)...)
		}
		return pad(append(lines, help...), h)
	}
	right := w - left - 3
	var l []string
	for i, sec := range s.sections {
		cursor, title := " ", sec.Title
		if i == s.sec {
			cursor, title = "›", current.Render(title)
		}
		l = append(l, truncate(" "+cursor+" "+title, left))
	}
	var r []string
	if !s.GitShown() && !s.RunsShown() {
		r = append(r, pal.bold.Render(s.sections[s.sec].Title))
		if help := s.sections[s.sec].Help; help != "" {
			r = append(r, wrap(help, right, pal.dim)...)
		}
		r = append(r, "")
	}
	switch {
	case s.GitShown():
		r = s.gitLines(right, h-len(lines), false, pal)
	case s.RunsShown():
		r = s.runsLines(right, h-len(lines), false, pal)
	case s.sync.confirm:
		r = append(r, s.confirmLines(right, h-len(lines)-len(r), pal)...)
	default:
		r = append(r, s.fieldLines(right, pal)...)
		var help []string
		if text := s.cursorHelp(); text != "" {
			help = append([]string{""}, wrap(text, right, pal.dim)...)
		}
		if s.MCPShown() {
			r = append(r, s.mcpLines(right, h-len(lines)-len(r)-len(help), pal)...)
		}
		if s.syncShown() {
			r = append(r, s.syncLines(right, h-len(lines)-len(r)-len(help), pal)...)
		}
		r = append(r, help...)
	}
	sep := pal.dim.Render("│")
	for i := 0; i < max(len(l), len(r)); i++ {
		var a, b string
		if i < len(l) {
			a = l[i]
		}
		if i < len(r) {
			b = r[i]
		}
		line := a + strings.Repeat(" ", max(left-runeWidth(a), 0)) + " " + sep
		if b != "" {
			line += " " + b
		}
		lines = append(lines, line)
	}
	return pad(lines, h)
}

// fieldLines are the rows "› Model  ◂ opus  ▸ •" of the open section, fitted to w. The label
// column gives way first, so the value always shows. A text field shows its value as is, or
// "not set", truncated to w.
func (s *Settings) fieldLines(w int, pal palette) []string {
	fs := s.fields()
	labelW, valueW := 0, 0
	for _, f := range fs {
		labelW = max(labelW, runeWidth(f.Label))
		for _, c := range f.Choices {
			valueW = max(valueW, runeWidth(shown(c)))
		}
	}
	labelW = max(min(labelW, w-valueW-11), 4) // " › " label "  ◂ " value " ▸ •"
	var out []string
	for i, f := range fs {
		cursor, label := " ", truncate(f.Label, labelW)
		label += strings.Repeat(" ", max(labelW-runeWidth(label), 0))
		var value string
		if f.Kind.Typed() {
			v := s.value(f.Key)
			if v == "" {
				v = "not set"
			}
			v = truncate(v, max(w-labelW-5, 1))
			value = v
			if s.at(i) {
				cursor, label = "›", pal.bold.Render(label)
				value = pal.blue.Render(v)
			}
		} else {
			v := shown(orDefault(f, s.value(f.Key)))
			v += strings.Repeat(" ", max(valueW-runeWidth(v), 0))
			value = pal.dim.Render("◂ ") + v + pal.dim.Render(" ▸")
			if s.at(i) {
				cursor, label = "›", pal.bold.Render(label)
				value = pal.blue.Render("◂ " + v + " ▸")
			}
		}
		line := " " + cursor + " " + label + "  " + value
		if _, ok := s.edits[f.Key]; ok {
			line += " " + pal.amber.Render("•")
		}
		out = append(out, truncate(line, w))
	}
	return out
}

// helpLines are the section's help and the selected field's, under the fields on a narrow screen.
func (s *Settings) helpLines(w int, pal palette) []string {
	out := []string{""}
	if help := s.sections[s.sec].Help; help != "" {
		out = append(out, wrap(help, w-1, pal.dim)...)
	}
	if text := s.cursorHelp(); text != "" {
		out = append(out, wrap(text, w-1, pal.dim)...)
	}
	for i := 1; i < len(out); i++ {
		out[i] = " " + out[i]
	}
	return out
}

// cursorHelp is the help of the field under the cursor, or the keys of the sync list; none
// while the list has the focus.
func (s *Settings) cursorHelp() string {
	if _, ok := s.MCPChoice(); ok || !s.open {
		return "" // the key bar has the row's keys; the rows show the rest
	}
	if f, ok := s.field(); ok {
		return f.Help
	}
	if _, ok := s.syncRow(); ok {
		return syncHint
	}
	return ""
}

// isMove reports whether key moves the cursor up or down.
func isMove(key string) bool {
	switch key {
	case "j", "Down", "C-j", "k", "Up", "C-k":
		return true
	}
	return false
}

// wrap breaks text into lines of at most w cells, each drawn in style.
func wrap(text string, w int, style lipgloss.Style) []string {
	var out []string
	line := ""
	for _, word := range strings.Fields(text) {
		if line != "" && runeWidth(line+" "+word) > w {
			out = append(out, style.Render(truncate(line, w)))
			line = ""
		}
		if line != "" {
			line += " "
		}
		line += word
	}
	if line != "" {
		out = append(out, style.Render(truncate(line, w)))
	}
	return out
}

// SettingsListBar is the key bar while the section list has the focus.
func SettingsListBar(narrow bool) string {
	if narrow {
		return "j/k section · ⏎ open · esc"
	}
	return "j/k section · ⏎ open · esc close"
}

// SettingsBar is the key bar of the settings screen, with the count of unsaved changes and of
// the items a sync would change. onText (the cursor is on a text field) reads ⏎ edit.
func SettingsBar(changes, toSync int, narrow, onText bool) string {
	enter := "⏎ save"
	if onText {
		enter = "⏎ edit"
	}
	parts := []string{"j/k move", "h/l change", "space select", "s sync…", "r refresh", enter, "esc back"}
	if narrow {
		parts = []string{enter, "s sync", "esc"}
	}
	text := strings.Join(parts, " · ")
	if changes > 0 {
		text += fmt.Sprintf("    • %d unsaved", changes)
	}
	if toSync > 0 {
		text += fmt.Sprintf("  • %d to sync", toSync)
	}
	return text
}
