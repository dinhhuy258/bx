package ui

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// syncSection is the ID of the settings section the sync list is drawn in.
const syncSection = "claude"

// syncFolders are the folders of the sync list, in screen order.
var syncFolders = []string{"skills", "agents", "commands", "rules"}

// syncHint is the help line while the cursor is on the sync list.
const syncHint = "space select · h/l fold · s sync… · r refresh"

// SyncItem is one row of the sync list: "skills/<name>" or "<agents|commands|rules>/<entry>".
// Status is "new", "changed", "same" or "removed" (removed on the Mac, still on the server);
// "" is an item known only from Warning, or one the Mac couldn't read. Error says why it can't sync; such an item and one
// with no Status can't be checked. Link marks an item that is a symlink on the Mac.
type SyncItem struct {
	Name, Status, Error, Warning string
	Link                         bool
}

// SyncData is what a scan and a plan found: the two Claude dirs, when, the items, and the
// number of items only the server has (kept). Warnings are the Mac folders that couldn't be
// read. Done is set after a sync.
type SyncData struct {
	MacDir, ServerDir string
	At                time.Time
	Items             []SyncItem
	Warnings          []string
	ServerOnly        int
	Done              bool
}

type syncState int

const (
	syncNone    syncState = iota // not asked for yet
	syncLoading                  // scanning, planning or applying
	syncReady                    // data is shown
	syncFailed                   // note is the error, until r
)

// syncList is the sync part of the settings screen.
type syncList struct {
	state    syncState
	note     string
	data     SyncData
	include  map[string]bool // selected items
	folded   map[string]bool
	foldSet  bool // folds were set from the first data
	confirm  bool // the confirmation box is open
	top      int  // its first shown line
	boxLines int  // how many change lines it showed last time
}

// syncRow is a row of the list: a folder (item < 0) or an item of it.
type syncRow struct {
	folder string
	item   int
}

// SyncScanning shows the list as loading while the Mac's dir is scanned and the server asked.
func (s *Settings) SyncScanning(macDir string) {
	s.sync.state, s.sync.note = syncLoading, "⠋ scanning "+shortPath(macDir)+" and asking bx serve…"
	s.sync.confirm = false
}

// SyncApplying shows the list as loading while a sync runs.
func (s *Settings) SyncApplying() {
	s.sync.state, s.sync.note, s.sync.confirm = syncLoading, "⠋ syncing…", false
}

// SyncFailed shows msg in place of the list until the next scan.
func (s *Settings) SyncFailed(msg string) {
	key := s.rowKey()
	s.sync.state, s.sync.note, s.sync.confirm = syncFailed, msg, false
	s.restoreCursor(key)
}

// SetSync shows d, with the items of include checked; every other item starts unchecked.
// Folds and the cursor's row are kept from the last data; on the first data, folders with
// nothing checked to do start folded.
func (s *Settings) SetSync(d SyncData, include []string) {
	key := s.rowKey()
	s.sync.state, s.sync.note, s.sync.data = syncReady, "", d
	s.sync.include = map[string]bool{}
	for _, e := range include {
		s.sync.include[e] = true
	}
	if !s.sync.foldSet {
		s.sync.folded, s.sync.foldSet = map[string]bool{}, true
		for _, f := range syncFolders {
			s.sync.folded[f] = !slices.ContainsFunc(s.folderItems(f), func(it SyncItem) bool {
				return it.Error != "" || it.Warning != "" || s.changed(it)
			})
		}
	}
	s.restoreCursor(key)
}

// SyncInclude is the selected items, sorted.
func (s *Settings) SyncInclude() []string {
	out := make([]string, 0, len(s.sync.include))
	for e := range s.sync.include {
		out = append(out, e)
	}
	slices.Sort(out)
	return out
}

// SyncScope is the checked items a sync sends.
func (s *Settings) SyncScope() []string {
	var out []string
	for _, it := range s.syncItems() {
		if s.checked(it) {
			out = append(out, it.Name)
		}
	}
	return out
}

// SyncChanges is the number of checked items a sync would change on the server.
func (s *Settings) SyncChanges() int {
	n := 0
	for _, it := range s.syncItems() {
		if s.changed(it) {
			n++
		}
	}
	return n
}

func (s *Settings) syncShown() bool {
	return s.sec < len(s.sections) && s.sections[s.sec].ID == syncSection
}

func (s *Settings) syncItems() []SyncItem {
	if s.sync.state != syncReady {
		return nil
	}
	return s.sync.data.Items
}

func selectable(it SyncItem) bool { return it.Error == "" && it.Status != "" }

func (s *Settings) checked(it SyncItem) bool { return selectable(it) && s.sync.include[it.Name] }

func (s *Settings) changed(it SyncItem) bool {
	return s.checked(it) && it.Status != "same"
}

func folderOf(name string) string {
	f, _, _ := strings.Cut(name, "/")
	return f
}

func (s *Settings) folderItems(folder string) []SyncItem {
	var out []SyncItem
	for _, it := range s.syncItems() {
		if folderOf(it.Name) == folder {
			out = append(out, it)
		}
	}
	return out
}

// syncRows are the rows the cursor moves through: each folder with items, and its items when
// unfolded. Items are indexed into data.Items.
func (s *Settings) syncRows() []syncRow {
	if !s.syncShown() {
		return nil
	}
	var out []syncRow
	for _, f := range syncFolders {
		var items []int
		for i, it := range s.syncItems() {
			if folderOf(it.Name) == f {
				items = append(items, i)
			}
		}
		if len(items) == 0 {
			continue
		}
		out = append(out, syncRow{folder: f, item: -1})
		if !s.sync.folded[f] {
			for _, i := range items {
				out = append(out, syncRow{folder: f, item: i})
			}
		}
	}
	return out
}

// syncRow is the row under the cursor, if it is on the list.
func (s *Settings) syncRow() (syncRow, bool) {
	rows := s.syncRows()
	i := s.fld - len(s.fields())
	if i < 0 || i >= len(rows) {
		return syncRow{}, false
	}
	return rows[i], true
}

// rowKey names the cursor's row, so it can be found again after the rows change.
func (s *Settings) rowKey() string {
	r, ok := s.syncRow()
	switch {
	case !ok:
		return ""
	case r.item < 0:
		return r.folder
	}
	return s.sync.data.Items[r.item].Name
}

func (s *Settings) restoreCursor(key string) {
	n := len(s.fields())
	if key != "" {
		for i, r := range s.syncRows() {
			if r.item < 0 && r.folder == key || r.item >= 0 && s.sync.data.Items[r.item].Name == key {
				s.fld = n + i
				return
			}
		}
		if r := s.syncRows(); len(r) > 0 { // the item is gone: its folder, else the last row
			s.fld = n + len(r) - 1
			for i, row := range r {
				if row.item < 0 && row.folder == folderOf(key) {
					s.fld = n + i
				}
			}
			return
		}
	}
	s.fld = min(s.fld, max(n+len(s.syncRows())-1, 0))
}

// toggle checks or clears the item, or all items of the folder, under the cursor. It reports
// whether anything changed.
func (s *Settings) toggle() bool {
	r, ok := s.syncRow()
	if !ok {
		return false
	}
	var items []SyncItem
	if r.item < 0 {
		for _, it := range s.folderItems(r.folder) {
			if selectable(it) {
				items = append(items, it)
			}
		}
	} else if it := s.sync.data.Items[r.item]; selectable(it) {
		items = []SyncItem{it}
	}
	if len(items) == 0 {
		return false
	}
	all := !slices.ContainsFunc(items, func(it SyncItem) bool { return !s.checked(it) })
	for _, it := range items {
		if all {
			delete(s.sync.include, it.Name)
		} else {
			s.sync.include[it.Name] = true
		}
	}
	return true
}

// fold folds (h) or unfolds (l) the folder under the cursor.
func (s *Settings) fold(folded bool) {
	if r, ok := s.syncRow(); ok && r.item < 0 {
		s.sync.folded[r.folder] = folded
	}
}

// confirmKey handles a key while the confirmation box is open: only y syncs.
func (s *Settings) confirmKey(key string) SettingsResult {
	switch key {
	case "y":
		s.sync.confirm = false
		return SettingsSyncApply
	case "n", "Esc", "C-c":
		s.sync.confirm = false
	case "j", "Down", "C-j":
		s.sync.top = min(s.sync.top+1, max(len(s.syncChangeItems())-s.sync.boxLines, 0))
	case "k", "Up", "C-k":
		s.sync.top = max(s.sync.top-1, 0)
	}
	return SettingsKeep
}

// syncChangeItems are the checked items with a change, in screen order.
func (s *Settings) syncChangeItems() []SyncItem {
	var out []SyncItem
	for _, f := range syncFolders {
		for _, it := range s.folderItems(f) {
			if s.changed(it) {
				out = append(out, it)
			}
		}
	}
	return out
}

// syncHead is the list's header, and the loading note or the error, fitted to w.
func (s *Settings) syncHead(w int, pal palette) []string {
	head := pal.bold.Render("Sync to bx serve")
	if d := s.sync.data; s.sync.state == syncReady {
		head += "  " + pal.dim.Render("Mac "+shortPath(d.MacDir)+" → "+d.ServerDir+" · checked "+d.At.Format("15:04"))
	}
	out := []string{"", truncate(" "+head, w)}
	switch s.sync.state {
	case syncLoading:
		out = append(out, truncate("   "+pal.dim.Render(s.sync.note), w))
	case syncFailed:
		out = append(out, wrap(s.sync.note, w-3, pal.red)...)
		for i := 2; i < len(out); i++ {
			out[i] = "   " + out[i]
		}
		out = append(out, "   "+pal.dim.Render("r to try again"))
	case syncReady:
		for _, warn := range s.sync.data.Warnings {
			out = append(out, truncate("   "+pal.amber.Render(warn), w))
		}
		if len(s.syncRows()) == 0 {
			out = append(out, "   "+pal.dim.Render("nothing to sync: no skills, agents, commands or rules"))
		}
	}
	return out
}

// syncFoot is the dim lines under the list.
func (s *Settings) syncFoot(w int, pal palette) []string {
	if s.sync.state != syncReady {
		return nil
	}
	var out []string
	if n := s.sync.data.ServerOnly; n > 0 {
		out = append(out, truncate(" "+pal.dim.Render(plural(n, "server-only item", "server-only items")+" kept"), w))
	}
	if s.sync.data.Done {
		for _, l := range wrap("New Claude sessions see the changes. Resume running ones to pick them up.", w-1, pal.dim) {
			out = append(out, " "+l)
		}
	}
	if len(out) > 0 {
		out = append([]string{""}, out...)
	}
	return out
}

// syncRowLines draws the rows "▾ [-] skills  +1 ~2 −1" and "    [x] plan  ~ changed".
func (s *Settings) syncRowLines(w int, pal palette) []string {
	rows := s.syncRows()
	nameW := 0
	for _, r := range rows {
		if r.item < 0 {
			nameW = max(nameW, runeWidth(r.folder)+6)
		} else {
			nameW = max(nameW, runeWidth(s.itemName(r))+8)
		}
	}
	nameW = max(min(nameW, w-3-18), 10)
	cur := s.fld - len(s.fields())
	out := make([]string, 0, len(rows))
	for i, r := range rows {
		var left, right string
		if r.item < 0 {
			arrow := "▾"
			if s.sync.folded[r.folder] {
				arrow = "▸"
			}
			left = pal.dim.Render(arrow) + " " + s.folderBox(r.folder, pal) + " " + r.folder
			right = s.folderSummary(r.folder, pal)
		} else {
			it := s.sync.data.Items[r.item]
			box := "[ ]"
			if s.checked(it) {
				box = "[x]"
			}
			name := s.itemName(r)
			if it.Status == "removed" && s.checked(it) {
				name = pal.red.Render(name)
			}
			left = "    " + box + " " + name
			right = s.itemStatus(it, pal)
		}
		left = truncate(left, nameW)
		cursor := " "
		if i == cur {
			cursor = pal.blue.Render("›")
			left = pal.bold.Render(left)
		}
		line := " " + cursor + " " + left + strings.Repeat(" ", max(nameW-runeWidth(left), 0)) + "  " + right
		out = append(out, truncate(line, w))
	}
	return out
}

func (s *Settings) itemName(r syncRow) string {
	return strings.TrimPrefix(s.sync.data.Items[r.item].Name, r.folder+"/")
}

// folderBox is [x] when all of the folder's selectable items are checked, [ ] when none is,
// [-] otherwise.
func (s *Settings) folderBox(folder string, pal palette) string {
	on, n := 0, 0
	for _, it := range s.folderItems(folder) {
		if selectable(it) {
			n++
			if s.checked(it) {
				on++
			}
		}
	}
	switch {
	case n > 0 && on == n:
		return "[x]"
	case on > 0:
		return "[" + pal.dim.Render("-") + "]"
	}
	return "[ ]"
}

func (s *Settings) folderSummary(folder string, pal palette) string {
	items := s.folderItems(folder)
	var add, upd, del, on int
	for _, it := range items {
		if !s.checked(it) {
			continue
		}
		on++
		switch it.Status {
		case "new":
			add++
		case "changed":
			upd++
		case "removed":
			del++
		}
	}
	var parts []string
	if add > 0 {
		parts = append(parts, pal.green.Render(fmt.Sprintf("+%d", add)))
	}
	if upd > 0 {
		parts = append(parts, pal.amber.Render(fmt.Sprintf("~%d", upd)))
	}
	if del > 0 {
		parts = append(parts, pal.red.Render(fmt.Sprintf("−%d", del)))
	}
	switch {
	case len(parts) > 0:
		return strings.Join(parts, " ")
	case on > 0:
		return pal.dim.Render(fmt.Sprintf("up to date (%d)", len(items)))
	}
	return pal.dim.Render(fmt.Sprintf("not synced (%d)", len(items)))
}

func (s *Settings) itemStatus(it SyncItem, pal palette) string {
	var st string
	switch {
	case it.Error != "":
		st = pal.red.Render(it.Error)
	case it.Status == "":
	case !s.checked(it):
		st = pal.dim.Render("not synced")
	case it.Status == "new":
		st = pal.green.Render("+ new")
	case it.Status == "changed":
		st = pal.amber.Render("~ changed")
	case it.Status == "same":
		st = pal.dim.Render("= same")
	case it.Status == "removed":
		st = pal.red.Render("− removed on the Mac")
	}
	if it.Warning != "" {
		st = strings.TrimSpace(st + "  " + pal.amber.Render(it.Warning))
	}
	if it.Link {
		st = strings.TrimSpace(st + "  " + pal.dim.Render("↪"))
	}
	return st
}

// syncLines is the whole list fitted to w and to at most h lines: the header, the rows
// windowed around the cursor with "… N more" for the hidden ones, and the footer. Before the
// first scan it is empty.
func (s *Settings) syncLines(w, h int, pal palette) []string {
	if s.sync.state == syncNone {
		return nil
	}
	head, foot := s.syncHead(w, pal), s.syncFoot(w, pal)
	rows := s.syncRowLines(w, pal)
	avail := max(h-len(head)-len(foot), 3)
	if len(rows) > avail {
		cur := max(s.fld-len(s.fields()), 0)
		start := min(max(cur-(avail-1)/2, 0), len(rows)-avail)
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
	return slices.Concat(head, rows, foot)
}

// confirmLines is the confirmation box, at most h lines: one line per item change.
func (s *Settings) confirmLines(w, h int, pal palette) []string {
	changes := s.syncChangeItems()
	inner := min(max(w-6, 20), 60)
	var lines []string
	for _, it := range changes {
		switch it.Status {
		case "new":
			lines = append(lines, pal.green.Render("+ "+it.Name))
		case "changed":
			lines = append(lines, pal.amber.Render("~ "+it.Name))
		case "removed":
			lines = append(lines, pal.red.Render("− "+it.Name)+"  "+pal.dim.Render("deleted on the server"))
		}
	}
	avail := max(h-6, 1)
	s.sync.boxLines = avail
	top := min(s.sync.top, max(len(lines)-avail, 0))
	if len(lines) > avail {
		lines = lines[top : top+avail]
	}
	row := func(text string) string {
		text = truncate(text, inner-2)
		return pal.dim.Render("│") + " " + text + strings.Repeat(" ", max(inner-2-runeWidth(text), 0)) + " " + pal.dim.Render("│")
	}
	out := []string{pal.dim.Render("╭" + strings.Repeat("─", inner) + "╮")}
	out = append(out, row(pal.bold.Render(fmt.Sprintf("Sync %s to bx serve?", plural(len(changes), "change", "changes")))), row(""))
	for _, l := range lines {
		out = append(out, row("  "+l))
	}
	out = append(out, row(""), row("  "+pal.sel.Render(" y sync ")+"   "+pal.dim.Render("n / esc cancel")))
	out = append(out, pal.dim.Render("╰"+strings.Repeat("─", inner)+"╯"))
	for i := range out {
		out[i] = truncate("  "+out[i], w)
	}
	return out
}
