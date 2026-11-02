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
	SettingsKeep  SettingsResult = iota // still editing
	SettingsSave                        // Enter: send Changes
	SettingsClose                       // Esc or C-c: the caller asks first when Changes is not empty
	SettingsSelect                      // Space changed the sync selection: save SyncExclude
	SettingsRescan                      // r: scan and plan again
	SettingsUpToDate                    // s with nothing to sync
	SettingsSyncApply                   // y in the confirmation: sync SyncScope
)

// Settings is the settings screen. It knows no setting by name: it draws the sections and
// fields the server sent. Edits stay here until they are saved. The Claude section also shows
// the sync list below its fields; fld is the cursor over the fields, then the list's rows.
type Settings struct {
	Server   string // the server's host, for the header
	sections []settings.Section
	saved    map[string]string // what the server holds; a missing key is the default
	edits    map[string]string // changed keys only
	sec, fld int
	sync     syncList
}

// NewSettings starts an empty screen; Load fills it.
func NewSettings(server string) *Settings {
	return &Settings{Server: server, saved: map[string]string{}, edits: map[string]string{}}
}

// Loaded reports whether the server's sections have arrived.
func (s *Settings) Loaded() bool { return len(s.sections) > 0 }

// Load takes what the server holds, after a GET or a save. Edits that now match it are dropped.
func (s *Settings) Load(sections []settings.Section, values map[string]string) {
	if len(sections) > 0 {
		s.sections = sections
	}
	s.saved = maps.Clone(values)
	if s.saved == nil {
		s.saved = map[string]string{}
	}
	maps.DeleteFunc(s.edits, func(k, v string) bool { return s.saved[k] == v })
	s.sec = min(s.sec, max(len(s.sections)-1, 0))
	s.fld = min(s.fld, max(len(s.fields())+len(s.syncRows())-1, 0))
}

// Changes are the edited keys and their new values.
func (s *Settings) Changes() map[string]string { return maps.Clone(s.edits) }

func (s *Settings) fields() []settings.Field {
	if s.sec >= len(s.sections) {
		return nil
	}
	return s.sections[s.sec].Fields
}

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

// Key feeds one key: Tab next section, j/k field or sync row, h/l the value or a folder's
// fold, Space a sync checkbox, s the sync confirmation, r a rescan, Enter save, Esc close.
// While the confirmation is open only y, n and Esc act.
func (s *Settings) Key(key string) SettingsResult {
	if s.sync.confirm {
		return s.confirmKey(key)
	}
	switch key {
	case "Esc", "C-c":
		return SettingsClose
	case "Enter":
		return SettingsSave
	}
	if !s.Loaded() {
		return SettingsKeep
	}
	_, onField := s.field()
	switch key {
	case "Tab":
		s.sec, s.fld = (s.sec+1)%len(s.sections), 0
	case "j", "Down", "C-j":
		s.fld = min(s.fld+1, max(len(s.fields())+len(s.syncRows())-1, 0))
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

// step moves the selected choice field to the previous or next allowed value, wrapping.
func (s *Settings) step(delta int) {
	f, ok := s.field()
	if !ok {
		return
	}
	if f.Kind != settings.Choice || len(f.Choices) == 0 {
		return
	}
	i := max(slices.Index(f.Choices, s.value(f.Key)), 0)
	v := f.Choices[(i+delta+len(f.Choices))%len(f.Choices)]
	if v == s.saved[f.Key] {
		delete(s.edits, f.Key)
	} else {
		s.edits[f.Key] = v
	}
}

func shown(v string) string {
	if v == "" {
		return "default"
	}
	return v
}

// Render draws the screen at w x h: sections on the left and fields on the right, or, when
// narrow, the sections as tabs on the top line. note replaces the fields until they load.
func (s *Settings) Render(w, h int, plain bool, note string) string {
	pal := newPalette(plain)
	head := fit(pal.brand.Render("bx")+" "+pal.bold.Render("settings"), pal.dim.Render(s.Server), w)
	lines := []string{head, rule(w, RenderOptions{Plain: plain, Focused: true})}
	if !s.Loaded() {
		return pad(append(lines, "", "  "+note), h)
	}
	if IsNarrow(w) {
		tabs := make([]string, len(s.sections))
		for i, sec := range s.sections {
			tabs[i] = pal.dim.Render(sec.Title)
			if i == s.sec {
				tabs[i] = pal.bold.Inherit(pal.blue).Render(sec.Title)
			}
		}
		lines = append(lines, truncate(" "+strings.Join(tabs, pal.dim.Render(" · ")), w), "")
		if s.sync.confirm {
			return pad(append(lines, s.confirmLines(w, h-len(lines), pal)...), h)
		}
		lines = append(lines, s.fieldLines(w, pal)...)
		help := s.helpLines(w, pal)
		if s.syncShown() {
			lines = append(lines, s.syncLines(w, h-len(lines)-len(help), pal)...)
		}
		return pad(append(lines, help...), h)
	}
	const left = 20
	right := w - left - 3
	var l []string
	for i, sec := range s.sections {
		cursor, title := " ", sec.Title
		if i == s.sec {
			cursor, title = "›", pal.bold.Inherit(pal.blue).Render(title)
		}
		l = append(l, truncate(" "+cursor+" "+title, left))
	}
	r := []string{pal.bold.Render(s.sections[s.sec].Title)}
	if help := s.sections[s.sec].Help; help != "" {
		r = append(r, wrap(help, right, pal.dim)...)
	}
	r = append(r, "")
	switch {
	case s.sync.confirm:
		r = append(r, s.confirmLines(right, h-len(lines)-len(r), pal)...)
	default:
		r = append(r, s.fieldLines(right, pal)...)
		var help []string
		if text := s.cursorHelp(); text != "" {
			help = append([]string{""}, wrap(text, right, pal.dim)...)
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
// column gives way first, so the value always shows.
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
		v := shown(s.value(f.Key))
		v += strings.Repeat(" ", max(valueW-runeWidth(v), 0))
		value := pal.dim.Render("◂ ") + v + pal.dim.Render(" ▸")
		if i == s.fld {
			cursor, label = "›", pal.bold.Render(label)
			value = pal.blue.Render("◂ " + v + " ▸")
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

// cursorHelp is the help of the field under the cursor, or the keys of the sync list.
func (s *Settings) cursorHelp() string {
	if f, ok := s.field(); ok {
		return f.Help
	}
	if _, ok := s.syncRow(); ok {
		return syncHint
	}
	return ""
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

// SettingsBar is the key bar of the settings screen, with the count of unsaved changes and of
// the items a sync would change.
func SettingsBar(changes, toSync int, narrow bool) string {
	parts := []string{"tab section", "j/k move", "h/l change", "space select", "s sync…", "r refresh", "⏎ save", "esc close"}
	if narrow {
		parts = []string{"⏎ save", "s sync", "esc"}
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
