package ui

import (
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func sampleSync() SyncData {
	return SyncData{
		MacDir: "/Users/me/.claude", ServerDir: "/home/bx/.claude", At: time.Date(2026, 11, 1, 22, 41, 0, 0, time.UTC), ServerOnly: 3,
		Items: []SyncItem{
			{Name: "agents/reviewer.md", Status: "same"},
			{Name: "agents/sub", Status: "same"},
			{Name: "commands/c.md", Status: "same"},
			{Name: "rules/a.md", Status: "new"},
			{Name: "rules/b.md", Status: "same"},
			{Name: "skills/brainstorm", Status: "new"},
			{Name: "skills/linked", Status: "new", Error: "skills/linked: refusing to write through a symlink"},
			{Name: "skills/old-skill", Status: "removed"},
			{Name: "skills/plan", Status: "changed"},
			{Name: "skills/quick-build", Status: "changed", Link: true},
			{Name: "skills/rails-console", Status: "changed"},
			{Name: "skills/systematic-debugging", Status: "same", Warning: `skipped a\b.md: bad name`},
		},
	}
}

// allBut is the names of d's selectable items, minus names.
func allBut(d SyncData, names ...string) []string {
	var out []string
	for _, it := range d.Items {
		if selectable(it) && !slices.Contains(names, it.Name) {
			out = append(out, it.Name)
		}
	}
	return out
}

// syncSettings is the sample screen with the sample list; all selected but rules and rails-console.
func syncSettings() *Settings {
	s := sampleSettings()
	s.SetSync(sampleSync(), allBut(sampleSync(), "rules/a.md", "rules/b.md", "skills/rails-console"))
	return s
}

// to moves the cursor down n rows from the first field.
func to(s *Settings, n int) {
	for range 10 {
		s.Key("k")
	}
	for range n {
		s.Key("j")
	}
}

func golden(t *testing.T, name, got string, w int) {
	t.Helper()
	path := "testdata/" + name + ".txt"
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update once to create it)", err)
	}
	if got != string(want) {
		t.Fatalf("%s differs:\n%s\n---- want ----\n%s", name, got, want)
	}
	for i, line := range strings.Split(got, "\n") {
		if lw := runeWidth(line); lw > w {
			t.Errorf("%s: line %d is %d cells wide: %q", name, i, lw, line)
		}
	}
}

func TestSettingsSyncGolden(t *testing.T) {
	s := syncSettings()
	to(s, 4) // skills/brainstorm
	golden(t, "settings-sync-120", s.Render(120, 30, true, ""), 120)
	golden(t, "settings-sync-40", s.Render(40, 30, true, ""), 40)
	s.Key("s")
	golden(t, "settings-confirm-120", s.Render(120, 20, true, ""), 120)
	s2 := sampleSettings() // fresh: nothing selected
	s2.SetSync(sampleSync(), nil)
	to(s2, 4)
	golden(t, "settings-sync-default-120", s2.Render(120, 30, true, ""), 120)
}

func TestSettingsSyncFoldsAndCursor(t *testing.T) {
	s := sampleSettings()
	s.Key("j")
	s.Key("j")
	s.Key("j")
	if _, ok := s.syncRow(); ok {
		t.Fatal("no list before the first scan: the cursor stays on the last field")
	}
	s.SetSync(sampleSync(), allBut(sampleSync()))
	// skills has changes: open; the others start folded
	if got := s.Render(120, 40, true, ""); !strings.Contains(got, "▾ [x] skills") || !strings.Contains(got, "▸ [x] agents") || strings.Contains(got, "reviewer.md") {
		t.Fatalf("folds:\n%s", got)
	}
	to(s, 3)
	if r, ok := s.syncRow(); !ok || r.folder != "skills" || r.item >= 0 {
		t.Fatalf("j past the last field: %+v %v", r, ok)
	}
	s.Key("h") // fold skills
	if got := s.Render(120, 40, true, ""); !strings.Contains(got, "▸ [x] skills") || strings.Contains(got, "brainstorm") {
		t.Fatalf("h folds:\n%s", got)
	}
	s.Key("j")
	s.Key("l") // unfold agents
	if got := s.Render(120, 40, true, ""); !strings.Contains(got, "▾ [x] agents") || !strings.Contains(got, "reviewer.md") {
		t.Fatalf("l unfolds:\n%s", got)
	}
	if len(s.Changes()) != 0 {
		t.Fatalf("h/l on folders changed fields: %v", s.Changes())
	}
	// a rescan keeps the folds and the cursor's row
	s.Key("j") // agents/reviewer.md
	s.SetSync(sampleSync(), allBut(sampleSync()))
	if r, _ := s.syncRow(); s.sync.data.Items[r.item].Name != "agents/reviewer.md" || s.sync.folded["agents"] || !s.sync.folded["skills"] {
		t.Fatalf("after a rescan: %+v %v", r, s.sync.folded)
	}
	if got := s.Render(120, 40, true, ""); !strings.Contains(got, syncHint) {
		t.Fatalf("help on a sync row:\n%s", got)
	}
}

func TestSettingsSyncSelect(t *testing.T) {
	s := sampleSettings()
	s.SetSync(sampleSync(), allBut(sampleSync()))
	if got, want := s.SyncChanges(), 6; got != want { // rules/a, brainstorm, old-skill, plan, quick-build, rails-console
		t.Fatalf("changes = %d, want %d", got, want)
	}
	to(s, 3) // the skills folder
	if s.Key("Space") != SettingsSelect {
		t.Fatal("space on a folder selects")
	}
	// all selectable skills cleared; linked (an error) never counts
	want := []string{"agents/reviewer.md", "agents/sub", "commands/c.md", "rules/a.md", "rules/b.md"}
	if got := s.SyncInclude(); !reflect.DeepEqual(got, want) {
		t.Fatalf("include = %q", got)
	}
	if got := s.Render(120, 40, true, ""); !strings.Contains(got, "[ ] skills") || !strings.Contains(got, "not synced (7)") {
		t.Fatalf("cleared:\n%s", got)
	}
	s.Key("j") // brainstorm
	s.Key("Space")
	if got := s.Render(120, 40, true, ""); !strings.Contains(got, "[-] skills") || !strings.Contains(got, "+1") {
		t.Fatalf("partial:\n%s", got)
	}
	s.Key("k")
	s.Key("Space") // partial → all
	all := allBut(sampleSync())
	if got := s.Render(120, 40, true, ""); !strings.Contains(got, "[x] skills") || !reflect.DeepEqual(s.SyncInclude(), all) || len(all) != 11 {
		t.Fatalf("all %q:\n%s", s.SyncInclude(), got)
	}
	// the error item can't be checked
	to(s, 5) // skills/linked
	if r, _ := s.syncRow(); s.sync.data.Items[r.item].Name != "skills/linked" {
		t.Fatalf("row %+v", r)
	}
	if s.Key("Space") != SettingsKeep || !reflect.DeepEqual(s.SyncInclude(), all) {
		t.Fatal("space on an error item does nothing")
	}
	// the scope: checked items on the Mac or removed, no error
	s.SetSync(sampleSync(), allBut(sampleSync(), "rules/a.md", "skills/old-skill"))
	want = []string{"agents/reviewer.md", "agents/sub", "commands/c.md", "rules/b.md", "skills/brainstorm", "skills/plan", "skills/quick-build", "skills/rails-console", "skills/systematic-debugging"}
	if got := s.SyncScope(); !reflect.DeepEqual(got, want) {
		t.Fatalf("scope = %q", got)
	}
	if s.SyncChanges() != 4 {
		t.Fatalf("changes = %d", s.SyncChanges())
	}
}

// A fresh list selects nothing: every box is clear, and only the include list checks items.
func TestSettingsSyncOptIn(t *testing.T) {
	s := sampleSettings()
	s.SetSync(sampleSync(), nil)
	if s.SyncChanges() != 0 || len(s.SyncScope()) != 0 {
		t.Fatalf("changes %d, scope %q", s.SyncChanges(), s.SyncScope())
	}
	got := s.Render(120, 40, true, "")
	for _, want := range []string{"▾ [ ] skills", "not synced (7)", "▸ [ ] agents", "not synced (2)", "▸ [ ] commands", "▸ [ ] rules"} {
		if !strings.Contains(got, want) {
			t.Fatalf("no %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "[x]") {
		t.Fatalf("a box is checked:\n%s", got)
	}
	to(s, 3)
	if s.Key("s") != SettingsNothingSelected || s.sync.confirm {
		t.Fatal("s with nothing selected")
	}
	s.SetSync(sampleSync(), []string{"skills/plan", "skills/old-skill"})
	got = s.Render(120, 40, true, "")
	for _, want := range []string{"[-] skills", "~1 −1", "[x] old-skill", "− removed on the Mac"} {
		if !strings.Contains(got, want) {
			t.Fatalf("no %q:\n%s", want, got)
		}
	}
	if s.SyncChanges() != 2 {
		t.Fatalf("changes = %d", s.SyncChanges())
	}
}

func TestSettingsSyncKeys(t *testing.T) {
	s := syncSettings()
	if s.Key("Enter") != SettingsSave {
		t.Fatal("⏎ still saves the fields")
	}
	if s.Key("r") != SettingsRescan {
		t.Fatal("r rescans")
	}
	if s.Key("s") != SettingsKeep || !s.sync.confirm {
		t.Fatal("s opens the confirmation")
	}
	for _, k := range []string{"Enter", "s", "Space", "r", "l", "x"} {
		if got := s.Key(k); got != SettingsKeep || !s.sync.confirm {
			t.Fatalf("%s in the box: %v", k, got)
		}
	}
	if s.Key("Esc") != SettingsKeep || s.sync.confirm {
		t.Fatal("Esc closes the box, not the screen")
	}
	s.Key("s")
	if s.Key("n") != SettingsKeep || s.sync.confirm {
		t.Fatal("n cancels")
	}
	s.Key("s")
	if s.Key("y") != SettingsSyncApply || s.sync.confirm {
		t.Fatal("y syncs")
	}
	// nothing to sync: s says so and opens nothing
	s.SetSync(SyncData{Items: []SyncItem{{Name: "skills/a", Status: "same"}}}, []string{"skills/a"})
	if s.Key("s") != SettingsUpToDate || s.sync.confirm {
		t.Fatal("s with no changes")
	}
	s.SetSync(SyncData{Items: []SyncItem{{Name: "skills/a", Status: "new"}}}, nil)
	if s.Key("s") != SettingsNothingSelected || s.sync.confirm {
		t.Fatal("s with nothing selected")
	}
	// while loading or failed: s does nothing, r only once the scan is done
	s.SyncScanning("/Users/me/.claude")
	if s.Key("s") != SettingsKeep || s.Key("r") != SettingsKeep {
		t.Fatal("keys while loading")
	}
	s.SyncFailed("bx serve doesn't support sync yet. Update the server: make deploy")
	if s.Key("s") != SettingsKeep || s.Key("r") != SettingsRescan {
		t.Fatal("keys after a failure")
	}
}

func TestSettingsSyncConfirmScrolls(t *testing.T) {
	s := sampleSettings()
	var items []SyncItem
	for _, n := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		items = append(items, SyncItem{Name: "skills/" + n, Status: "new"})
	}
	s.SetSync(SyncData{Items: items}, allBut(SyncData{Items: items}))
	s.Key("s")
	got := s.Render(120, 16, true, "")
	if !strings.Contains(got, "Sync 8 changes") || !strings.Contains(got, "+ skills/a") || strings.Contains(got, "+ skills/h") {
		t.Fatalf("box:\n%s", got)
	}
	for range 10 {
		s.Key("j")
	}
	got = s.Render(120, 16, true, "")
	if strings.Contains(got, "+ skills/a") || !strings.Contains(got, "+ skills/h") {
		t.Fatalf("scrolled:\n%s", got)
	}
}

func TestSettingsSyncStates(t *testing.T) {
	s := sampleSettings()
	s.SyncScanning(os.Getenv("HOME") + "/.claude")
	if got := s.Render(120, 30, true, ""); !strings.Contains(got, "⠋ scanning ~/.claude and asking bx serve…") {
		t.Fatalf("loading:\n%s", got)
	}
	s.SyncFailed("skills/big/x.md: 2 MB, over the 1 MB limit")
	got := s.Render(120, 30, true, "")
	if !strings.Contains(got, "skills/big/x.md: 2 MB, over the 1 MB limit") || !strings.Contains(got, "r to try again") {
		t.Fatalf("failed:\n%s", got)
	}
	d := sampleSync()
	d.Done = true
	s.SetSync(d, nil)
	if got := s.Render(120, 40, true, ""); !strings.Contains(got, "New Claude sessions see the changes. Resume running ones to pick them up.") || !strings.Contains(got, "3 server-only items kept") {
		t.Fatalf("done:\n%s", got)
	}
}

// A long list scrolls with the cursor, in both layouts; fields and the header stay.
func TestSettingsSyncWindow(t *testing.T) {
	for _, w := range []int{120, 40} {
		s := sampleSettings()
		var items []SyncItem
		for i := range 30 {
			items = append(items, SyncItem{Name: "skills/s" + string(rune('a'+i)), Status: "new"})
		}
		s.SetSync(SyncData{Items: items}, allBut(SyncData{Items: items}))
		to(s, 3+1+20) // skills/su
		got := s.Render(w, 24, true, "")
		lines := strings.Split(got, "\n")
		if len(lines) != 24 {
			t.Fatalf("width %d: %d lines", w, len(lines))
		}
		for _, want := range []string{"Model", "Permission", "Sync to bx serve", "› ", "su", "more"} {
			if !strings.Contains(got, want) {
				t.Fatalf("width %d: no %q:\n%s", w, want, got)
			}
		}
		if !strings.Contains(got, "[x] su") {
			t.Fatalf("width %d: the cursor's row is hidden:\n%s", w, got)
		}
	}
}

func TestSettingsBar(t *testing.T) {
	if got := SettingsBar(1, 4, false); got != "tab section · j/k move · h/l change · space select · s sync… · r refresh · ⏎ save · esc close    • 1 unsaved  • 4 to sync" {
		t.Fatal(got)
	}
	if got := SettingsBar(0, 2, true); got != "⏎ save · s sync · esc  • 2 to sync" {
		t.Fatal(got)
	}
	if got := SettingsBar(0, 0, true); got != "⏎ save · s sync · esc" {
		t.Fatal(got)
	}
}
