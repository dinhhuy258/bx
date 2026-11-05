package ui

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// ReviewAction is what a key did on the review screen.
type ReviewAction int

const (
	ReviewKeep          ReviewAction = iota
	ReviewBack                       // Esc: leave the screen
	ReviewRefresh                    // R: load the diff again
	ReviewComment                    // c: open the comment form on Selected
	ReviewDelete                     // x: delete Selected's unsent comment
	ReviewSend                       // S: pick a session for the unsent comments
	ReviewClearSent                  // C: ask to drop the sent comments
	ReviewExplain                    // e: open the explain form on Selected
	ReviewExplainDelete              // x on an explanation: stop it or dismiss it
	ReviewToggleViewed               // Space: mark the file viewed or not, with ToggleViewed
)

// ReviewLine is the line under the cursor and its unsent comment, or the selected lines.
type ReviewLine struct {
	File, Side string
	StartLine  int // a range's first line; 0 for one line
	Line       int
	Code       string   // the lines' text, one per line
	Edit       *Comment // its unsent comment, which c edits and x deletes
}

// Lines names the line or the range: "89" or "89–92".
func (l ReviewLine) Lines() string {
	if l.StartLine > 0 && l.StartLine < l.Line {
		return fmt.Sprintf("%d–%d", l.StartLine, l.Line)
	}
	return fmt.Sprint(l.Line)
}

// rangeBG is the band of the selected lines, bluer than the cursor's. restBG is the selected
// file's band while the focus is in the code: dimmer than the cursor's.
const (
	rangeBG = "\x1b[48;2;35;52;84m" // #233454
	restBG  = "\x1b[48;2;31;34;46m" // #1f222e
)

// ReviewFilesWidth is the file list's width on a wide screen.
const ReviewFilesWidth = 38

type itemKind int

const (
	itemHunk        itemKind = iota
	itemRow                  // a diff line: the cursor stops here
	itemPinned               // a comment that has no line in the diff (outdated, folded file): the cursor stops here
	itemComment              // one line of a comment under its diff line
	itemNote                 // a folded file's note
	itemExplain              // the header of an explanation's card: the cursor stops here
	itemExplainLine          // one line of its answer
	itemViewed               // a viewed file's note: folded, or changed since
)

type reviewItem struct {
	kind  itemKind
	text  string
	row   DiffRow
	hunk  int // a row's hunk
	c     *Comment
	e     *Explain // an explanation's card
	first bool     // a comment's first line
}

// Review is the review screen of a story: its files, the diff of the selected one and the
// comments on it. The client loads and saves; Review only draws and moves.
type Review struct {
	Story string
	data  ReviewData
	file  int
	line  int // index into items(file); always on a row or a pinned comment when one exists
	top   int
	pane  int // narrow: 0 the files, 1 the diff

	// selecting: v started a range at the row anchor (an index into items, like line); the
	// cursor moves its other end, inside the anchor's hunk. hint says why it stopped.
	selecting bool
	anchor    int
	hint      string

	// reading is the id of the explanation whose answer fills the diff pane, scrolled to
	// readTop. Now is the clock of the running seconds; nil is time.Now.
	reading string
	readTop int
	Now     func() time.Time

	// filter narrows the file list to the paths that hold each of its words (a !word leaves
	// them out); typing is set while / edits it. hideViewed (H) leaves viewed files out.
	filter     string
	typing     bool
	hideViewed bool

	// paneH and listH are the heights of the diff pane and the file list at the last Render:
	// what a page is.
	paneH, listH int

	// help (?) shows the keys in the diff pane, scrolled to helpTop.
	help    bool
	helpTop int
}

// reviewKeys is what ? shows.
var reviewKeys = [][2]string{
	{"⏎", "open the file's code (from the list), or read an explanation"},
	{"j/k ↑/↓", "next or previous file, or line in the code"},
	{"^d/^u", "half a page down or up, in the list or the code"},
	{"^f/^b PgDn/PgUp", "a page down or up"},
	{"g/G", "first or last file, or line of the file"},
	{"n/N", "next or previous comment, across files"},
	{"/", "filter the files by name (every word matches; !word leaves out)"},
	{"space", "mark the file viewed (or not) and go to the next one"},
	{"H", "hide or show viewed files"},
	{"c", "comment on the line, or edit its comment"},
	{"v", "select lines (o other end)"},
	{"e", "ask Claude to explain the line or the selection"},
	{"x", "delete the unsent comment, or stop / dismiss an explanation"},
	{"S", "send the unsent comments to a session"},
	{"C", "clear the sent comments"},
	{"R", "refresh the diff"},
	{"esc", "back to the list; on the list, clear the filter, then leave"},
}

// NewReview starts an empty screen for story, until SetData.
func NewReview(story string) *Review { return &Review{Story: story} }

// Data is the review last set.
func (r *Review) Data() ReviewData { return r.data }

// SetData shows a new load of the review, staying on the same file when it is still there. The
// cursor, and the selection when both its ends are still there, stay on the same rows: a
// reload that adds a card above them moves their index.
func (r *Review) SetData(d ReviewData) {
	path, cur, anc := "", "", ""
	if r.file < len(r.data.Files) {
		path = r.data.Files[r.file].Path
	}
	its := r.items(r.file)
	if r.line < len(its) {
		cur = itemKey(its[r.line])
	}
	if r.selecting && r.anchor < len(its) {
		anc = itemKey(its[r.anchor])
	}
	wasSelecting := r.selecting
	r.data, r.file = d, 0
	same := false
	for i, f := range d.Files {
		if f.Path == path {
			r.file, same = i, true
		}
	}
	r.selecting, r.hint = false, ""
	if r.reading != "" && r.explainByID(r.reading) == nil {
		r.reading = ""
	}
	its = r.items(r.file)
	if same && cur != "" {
		if ci := findKey(its, cur); ci >= 0 {
			r.line = ci
			if ai := findKey(its, anc); wasSelecting && anc != "" && ai >= 0 {
				r.anchor, r.selecting = ai, true
			}
			return
		}
	}
	r.snap(1)
}

// itemKey names a stop so it can be found again after a reload: a row by its place in the
// hunk, a comment or an explanation by its id.
func itemKey(it reviewItem) string {
	switch {
	case it.kind == itemRow:
		return fmt.Sprintf("r%d/%d/%d/%s", it.hunk, it.row.Old, it.row.New, it.row.Kind)
	case it.e != nil:
		return "c" + it.e.ID
	case it.c != nil:
		return "c" + it.c.ID
	}
	return ""
}

func findKey(its []reviewItem, key string) int {
	for i, it := range its {
		if stops(it) && itemKey(it) == key {
			return i
		}
	}
	return -1
}

// Unsent and SentCount count the comments.
func (r *Review) Unsent() int {
	n := 0
	for _, c := range r.data.Comments {
		if c.SentAt == 0 {
			n++
		}
	}
	return n
}

func (r *Review) SentCount() int { return len(r.data.Comments) - r.Unsent() }

func sideOf(row DiffRow) string {
	if row.Kind == "del" {
		return "old"
	}
	return "new"
}

func lineOf(row DiffRow) int {
	if row.Kind == "del" {
		return row.Old
	}
	return row.New
}

// items is the diff pane of file i, one item per screen line: its pinned comments, then each
// hunk with the comments under their lines.
func (r *Review) items(i int) []reviewItem {
	if i >= len(r.data.Files) {
		return nil
	}
	f := r.data.Files[i]
	var mine []*Comment
	for j := range r.data.Comments {
		if r.data.Comments[j].File == f.Path {
			mine = append(mine, &r.data.Comments[j])
		}
	}
	var mineE []*Explain
	for j := range r.data.Explains {
		if r.data.Explains[j].File == f.Path {
			mineE = append(mineE, &r.data.Explains[j])
		}
	}
	if f.Viewed == FileViewed {
		out := []reviewItem{{kind: itemViewed, text: fmt.Sprintf("✓ viewed · %s · +%d −%d · space to unmark", ReviewText(f.Path), f.Add, f.Del)}}
		for _, c := range mine {
			out = append(out, commentItems(itemPinned, c)...)
		}
		for _, e := range mineE {
			out = append(out, explainItems(e)...)
		}
		return out
	}
	placed := map[*Comment]bool{}
	placedE := map[*Explain]bool{}
	var body []reviewItem
	if f.Viewed == FileChanged {
		body = append(body, reviewItem{kind: itemViewed, text: "◐ changed since you viewed it · space to mark it viewed again"})
	}
	if f.Folded != "" {
		body = append(body, reviewItem{kind: itemNote, text: f.Folded})
	}
	for hi, h := range f.Hunks {
		body = append(body, reviewItem{kind: itemHunk, text: h.Header})
		for _, row := range h.Rows {
			body = append(body, reviewItem{kind: itemRow, row: row, hunk: hi})
			for _, c := range mine {
				if !c.Outdated && f.Folded == "" && c.Side == sideOf(row) && c.Line == lineOf(row) {
					placed[c] = true
					body = append(body, commentItems(itemComment, c)...)
				}
			}
			for _, e := range mineE {
				if !e.Outdated && f.Folded == "" && e.Side == sideOf(row) && e.Line == lineOf(row) {
					placedE[e] = true
					body = append(body, explainItems(e)...)
				}
			}
		}
	}
	var pinned []reviewItem
	for _, c := range mine {
		if !placed[c] {
			pinned = append(pinned, commentItems(itemPinned, c)...)
		}
	}
	for _, e := range mineE {
		if !placedE[e] {
			pinned = append(pinned, explainItems(e)...)
		}
	}
	return append(pinned, body...)
}

// maxCardLines is how many lines of an answer a card shows; the rest is read with Enter.
const maxCardLines = 3

// explainItems is an explanation's card: its header, then when it is done the first lines of
// its answer and how many more there are.
func explainItems(e *Explain) []reviewItem {
	out := []reviewItem{{kind: itemExplain, e: e, first: true}}
	if e.State != ExplainDone {
		return out
	}
	var lines []string
	for _, l := range strings.Split(e.Answer, "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	for i, l := range lines {
		if i == maxCardLines {
			out = append(out, reviewItem{kind: itemExplainLine, e: e, text: fmt.Sprintf("… %d more lines · ⏎ read", len(lines)-maxCardLines)})
			break
		}
		out = append(out, reviewItem{kind: itemExplainLine, e: e, text: strings.TrimSpace(l)})
	}
	return out
}

// explainByID is the explanation with id, or nil.
func (r *Review) explainByID(id string) *Explain {
	for i := range r.data.Explains {
		if r.data.Explains[i].ID == id {
			return &r.data.Explains[i]
		}
	}
	return nil
}

// cardAtCursor is the explanation whose card header the cursor is on, or nil.
func (r *Review) cardAtCursor() *Explain {
	its := r.items(r.file)
	if r.line < len(its) && its[r.line].kind == itemExplain {
		return its[r.line].e
	}
	return nil
}

// SelectedExplain is the explanation being read, or else the one under the cursor.
func (r *Review) SelectedExplain() *Explain {
	if r.reading != "" {
		return r.explainByID(r.reading)
	}
	return r.cardAtCursor()
}

func (r *Review) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// commentItems is a comment's lines; the first is of kind first, the others itemComment.
func commentItems(first itemKind, c *Comment) []reviewItem {
	var out []reviewItem
	for i, l := range strings.Split(c.Body, "\n") {
		k := first
		if i > 0 {
			k = itemComment
		}
		out = append(out, reviewItem{kind: k, text: l, c: c, first: i == 0})
	}
	return out
}

func stops(it reviewItem) bool {
	return it.kind == itemRow || it.kind == itemPinned || it.kind == itemExplain
}

// snap moves the cursor to the nearest stop in direction d (1 down, -1 up), then the other way.
func (r *Review) snap(d int) {
	its := r.items(r.file)
	r.line = min(max(r.line, 0), max(len(its)-1, 0))
	for _, dir := range []int{d, -d} {
		for i := r.line; i >= 0 && i < len(its); i += dir {
			if stops(its[i]) {
				r.line = i
				return
			}
		}
	}
}

func (r *Review) moveLine(d int) {
	its := r.items(r.file)
	for i := r.line + d; i >= 0 && i < len(its); i += d {
		if stops(its[i]) {
			r.line = i
			return
		}
	}
}

// moveFile moves d files down (up when negative) the list as it is shown, stopping at its ends.
// From a file the list doesn't show, 1 is the next one shown.
func (r *Review) moveFile(d int) {
	list := r.shown()
	if len(list) == 0 {
		return
	}
	pos := r.listPos(list)
	if d > 0 && (pos >= len(list) || list[pos] != r.file) {
		d-- // pos is already past the file
	}
	r.setFile(list[min(max(pos+d, 0), len(list)-1)])
}

// listPos is where the selected file is in list, or where it would be.
func (r *Review) listPos(list []int) int {
	for i, f := range list {
		if f >= r.file {
			return i
		}
	}
	return len(list)
}

// setFile opens file i at its top.
func (r *Review) setFile(i int) {
	if i == r.file {
		return
	}
	r.file, r.line, r.top = i, 0, 0
	r.selecting, r.hint = false, ""
	r.snap(1)
}

// shows says whether the file list shows file i: it matches the filter and isn't a viewed
// file that H hides.
func (r *Review) shows(i int) bool {
	f := r.data.Files[i]
	return (!r.hideViewed || f.Viewed != FileViewed) && MatchFilter(f.Path, r.filter)
}

// shown is the files the list shows, by index.
func (r *Review) shown() []int {
	var out []int
	for i := range r.data.Files {
		if r.shows(i) {
			out = append(out, i)
		}
	}
	return out
}

// keepShown moves off a file the list stopped showing, to the next one it shows.
func (r *Review) keepShown() {
	if len(r.data.Files) > 0 && !r.shows(r.file) {
		r.moveFile(1)
	}
}

// MatchFilter says whether path holds every word of filter, ignoring case; a word that starts
// with ! must not be in it.
func MatchFilter(path, filter string) bool {
	path = strings.ToLower(path)
	for _, w := range strings.Fields(strings.ToLower(filter)) {
		if not, ok := strings.CutPrefix(w, "!"); ok {
			if not != "" && strings.Contains(path, not) {
				return false
			}
		} else if !strings.Contains(path, w) {
			return false
		}
	}
	return true
}

// page is how far Ctrl-d and Ctrl-u go in a pane of h lines: half of it, at least a line.
func page(h int) int {
	if h <= 0 {
		h = 20 // before the first Render
	}
	return max((h-1)/2, 1)
}

// moveBy moves the cursor n screen lines (up when negative) and scrolls the view as far, then
// onto the nearest line it can stop on.
func (r *Review) moveBy(n int) {
	its := r.items(r.file)
	if len(its) == 0 {
		return
	}
	r.top = min(max(r.top+n, 0), max(len(its)-max(r.paneH, 1), 0))
	r.line = min(max(r.line+n, 0), len(its)-1)
	if n < 0 {
		r.snap(-1)
	} else {
		r.snap(1)
	}
}

// moveEdge goes to the first (d -1) or last (d 1) line of the file.
func (r *Review) moveEdge(d int) {
	its := r.items(r.file)
	if d < 0 {
		r.line, r.top = 0, 0
		r.snap(1)
		return
	}
	r.line = max(len(its)-1, 0)
	r.snap(-1)
}

// fileEdge goes to the first (d -1) or last (d 1) file of the list.
func (r *Review) fileEdge(d int) {
	if list := r.shown(); len(list) > 0 {
		if d < 0 {
			r.setFile(list[0])
		} else {
			r.setFile(list[len(list)-1])
		}
	}
}

// ToggleViewed marks the selected file viewed, or unmarks it, and returns what to send. Marking
// goes on to the next file of the list that isn't viewed. False when the file can't be marked
// (a gone file, which has no diff).
func (r *Review) ToggleViewed() (ReviewViewed, bool) {
	if r.file >= len(r.data.Files) || r.data.Files[r.file].Hash == "" {
		return ReviewViewed{}, false
	}
	f := &r.data.Files[r.file]
	req := ReviewViewed{Path: f.Path, Hash: f.Hash, Viewed: f.Viewed != FileViewed}
	f.Viewed = ""
	if req.Viewed {
		f.Viewed = FileViewed
	}
	r.selecting, r.hint, r.reading = false, "", ""
	r.line, r.top = 0, 0
	r.snap(1)
	if req.Viewed {
		for _, i := range r.shown() {
			if i > r.file && r.data.Files[i].Viewed != FileViewed {
				r.setFile(i)
				break
			}
		}
	}
	return req, true
}

// Viewed counts the viewed files.
func (r *Review) Viewed() int {
	n := 0
	for _, f := range r.data.Files {
		if f.Viewed == FileViewed {
			n++
		}
	}
	return n
}

// Filtering says whether / is editing the filter.
func (r *Review) Filtering() bool { return r.typing }

// nextComment goes to the next (d 1) or previous (d -1) line with a comment, across files;
// false when there is none.
func (r *Review) nextComment(d int) bool {
	type pos struct{ file, line int }
	var all []pos
	for f := range r.data.Files {
		if !r.shows(f) && f != r.file {
			continue
		}
		for i, it := range r.items(f) {
			if it.kind == itemPinned || (it.kind == itemRow && r.commentsOn(f, it.row) != nil) {
				all = append(all, pos{f, i})
			}
		}
	}
	less := func(a, b pos) bool { return a.file < b.file || a.file == b.file && a.line < b.line }
	cur := pos{r.file, r.line}
	if d > 0 {
		for _, p := range all {
			if less(cur, p) {
				r.file, r.line = p.file, p.line
				return true
			}
		}
		return false
	}
	for i := len(all) - 1; i >= 0; i-- {
		if less(all[i], cur) {
			r.file, r.line = all[i].file, all[i].line
			return true
		}
	}
	return false
}

func (r *Review) commentsOn(file int, row DiffRow) []*Comment {
	f := r.data.Files[file]
	var out []*Comment
	for j := range r.data.Comments {
		c := &r.data.Comments[j]
		if c.File == f.Path && !c.Outdated && c.Side == sideOf(row) && c.Line == lineOf(row) {
			out = append(out, c)
		}
	}
	return out
}

// Selected is the line under the cursor, or the selected lines; false when the file has none
// (folded, empty).
func (r *Review) Selected() (ReviewLine, bool) {
	its := r.items(r.file)
	if r.line >= len(its) || (its[r.line].kind != itemRow && its[r.line].kind != itemPinned) {
		return ReviewLine{}, false
	}
	it := its[r.line]
	if it.kind == itemPinned {
		c := it.c
		l := ReviewLine{File: c.File, Side: c.Side, StartLine: c.StartLine, Line: c.Line, Code: c.Code}
		if c.SentAt == 0 {
			l.Edit = c
		}
		return l, true
	}
	if r.selecting {
		if l, ok := r.selectedRange(its); ok {
			return l, true
		}
	}
	return r.rowLine(it.row), true
}

// rowLine is a diff row as a line, with its unsent comment; a range comment brings its lines.
func (r *Review) rowLine(row DiffRow) ReviewLine {
	l := ReviewLine{File: r.data.Files[r.file].Path, Side: sideOf(row), Line: lineOf(row), Code: row.Text}
	for _, c := range r.commentsOn(r.file, row) {
		if c.SentAt == 0 {
			l.Edit, l.StartLine, l.Code = c, c.StartLine, c.Code
			if c.StartLine == 0 {
				l.Code = row.Text
			}
			break
		}
	}
	return l
}

// selectedRange is the rows from the anchor to the cursor as one range: on the old side when
// they are all removed rows, else on the new side without the removed rows. False when that
// is one line: it is then that row's line.
func (r *Review) selectedRange(its []reviewItem) (ReviewLine, bool) {
	lo, hi := min(r.anchor, r.line), max(r.anchor, r.line)
	var rows []DiffRow
	old := true
	for _, it := range its[lo : hi+1] {
		if it.kind == itemRow {
			rows = append(rows, it.row)
			old = old && it.row.Kind == "del"
		}
	}
	var lines []int
	var texts []string
	var last DiffRow
	for _, row := range rows {
		if old == (row.Kind == "del") {
			lines, texts, last = append(lines, lineOf(row)), append(texts, row.Text), row
		}
	}
	if len(lines) < 2 {
		if len(lines) == 1 {
			return r.rowLine(last), true
		}
		return ReviewLine{}, false
	}
	side := "new"
	if old {
		side = "old"
	}
	return ReviewLine{File: r.data.Files[r.file].Path, Side: side, StartLine: lines[0], Line: lines[len(lines)-1],
		Code: strings.Join(texts, "\n")}, true
}

// selectKey handles a key while selecting.
func (r *Review) selectKey(key string) ReviewAction {
	r.hint = ""
	switch key {
	case "j", "Down":
		r.moveEnd(1)
	case "k", "Up":
		r.moveEnd(-1)
	case "C-d", "C-u", "C-f", "C-b", "PgDn", "PgUp":
		n, d := r.pageKey(key)
		for range n {
			if r.hint != "" || !r.moveEnd(d) {
				break
			}
		}
	case "o":
		r.anchor, r.line = r.line, r.anchor
	case "c":
		return ReviewComment
	case "e":
		return ReviewExplain
	case "v", "Esc":
		r.selecting = false
	}
	return ReviewKeep
}

// moveEnd moves the cursor end of the range to the next row of the anchor's hunk; false when
// there is none.
func (r *Review) moveEnd(d int) bool {
	its := r.items(r.file)
	for i := r.line + d; i >= 0 && i < len(its); i += d {
		switch {
		case its[i].kind == itemRow && its[i].hunk == its[r.anchor].hunk:
			r.line = i
			return true
		case its[i].kind == itemHunk || its[i].kind == itemRow:
			r.hint = "a range stays inside one hunk"
			return false
		}
	}
	return false
}

// pageKey is how many lines a paging key goes in the diff pane, and which way.
func (r *Review) pageKey(key string) (n, d int) {
	n = page(r.paneH)
	if key == "C-f" || key == "C-b" || key == "PgDn" || key == "PgUp" {
		n = max(r.paneH-1, 1)
	}
	if key == "C-u" || key == "C-b" || key == "PgUp" {
		return n, -1
	}
	return n, 1
}

// helpKey handles a key while ? shows the keys: they scroll, anything else closes it.
func (r *Review) helpKey(key string) ReviewAction {
	switch key {
	case "j", "Down":
		r.helpTop++
	case "k", "Up":
		r.helpTop = max(r.helpTop-1, 0)
	case "C-d", "C-u", "C-f", "C-b", "PgDn", "PgUp":
		n, d := r.pageKey(key)
		r.helpTop = max(r.helpTop+n*d, 0)
	default:
		r.help = false
	}
	return ReviewKeep
}

// helpLines is the key list, scrolled by helpTop.
func (r *Review) helpLines(w, h int, pal palette) []string {
	all := []string{pal.bold.Render("Keys on the review screen"), ""}
	for _, k := range reviewKeys {
		all = append(all, truncate(pal.amber.Render(padCells(k[0], 17))+" "+k[1], w))
	}
	r.helpTop = min(max(r.helpTop, 0), max(len(all)-h, 0))
	return all[r.helpTop:min(r.helpTop+h, len(all))]
}

// filterKey handles a key while / edits the filter.
func (r *Review) filterKey(key string) ReviewAction {
	switch key {
	case "Enter":
		r.typing = false
	case "Esc":
		r.typing, r.filter = false, ""
	case "BSpace":
		if _, n := utf8.DecodeLastRuneInString(r.filter); n > 0 {
			r.filter = r.filter[:len(r.filter)-n]
		}
	case "C-u":
		r.filter = ""
	case "Down", "C-n":
		r.moveFile(1)
		return ReviewKeep
	case "Up", "C-p":
		r.moveFile(-1)
		return ReviewKeep
	case "Space":
		r.filter += " "
	default:
		if utf8.RuneCountInString(key) == 1 {
			r.filter += key
		}
	}
	r.keepShown()
	return ReviewKeep
}

// listKey handles the keys that act on the file list on both screens; false when key isn't one.
func (r *Review) listKey(key string) (ReviewAction, bool) {
	switch key {
	case "/":
		r.typing = true
		return ReviewKeep, true
	case "?":
		r.help, r.helpTop = true, 0
		return ReviewKeep, true
	case "H":
		r.hideViewed = !r.hideViewed
		r.keepShown()
		return ReviewKeep, true
	case "Space":
		if r.file < len(r.data.Files) && r.data.Files[r.file].Hash != "" {
			return ReviewToggleViewed, true
		}
		return ReviewKeep, true
	}
	return ReviewKeep, false
}

// Key handles a key; narrow has two panes, the files and the diff.
func (r *Review) Key(key string, narrow bool) ReviewAction {
	if r.help {
		return r.helpKey(key)
	}
	if r.typing {
		return r.filterKey(key)
	}
	if r.selecting {
		return r.selectKey(key)
	}
	if r.reading != "" {
		return r.readKey(key)
	}
	if a, ok := r.listKey(key); ok {
		if r.typing {
			r.pane = 0 // the filter is on the list
		}
		return a
	}
	switch key {
	case "R":
		return ReviewRefresh
	case "S":
		if r.Unsent() > 0 {
			return ReviewSend
		}
		return ReviewKeep
	case "C":
		if r.SentCount() > 0 {
			return ReviewClearSent
		}
		return ReviewKeep
	}
	if r.pane == 0 {
		switch key {
		case "j", "Down":
			r.moveFile(1)
		case "k", "Up":
			r.moveFile(-1)
		case "C-d", "PgDn", "C-f":
			r.moveFile(page(r.listH))
		case "C-u", "PgUp", "C-b":
			r.moveFile(-page(r.listH))
		case "g":
			r.fileEdge(-1)
		case "G":
			r.fileEdge(1)
		case "Enter":
			if len(r.data.Files) > 0 {
				r.pane = 1
			}
		case "n", "N":
			if r.nextComment(map[string]int{"n": 1, "N": -1}[key]) {
				r.pane = 1
			}
		case "Esc":
			if r.filter != "" {
				r.filter = ""
				return ReviewKeep
			}
			return ReviewBack
		}
		return ReviewKeep
	}
	switch key {
	case "j", "Down":
		r.moveLine(1)
	case "k", "Up":
		r.moveLine(-1)
	case "C-d", "C-u", "C-f", "C-b", "PgDn", "PgUp":
		n, d := r.pageKey(key)
		r.moveBy(n * d)
	case "g":
		r.moveEdge(-1)
	case "G":
		r.moveEdge(1)
	case "n":
		r.nextComment(1)
	case "N":
		r.nextComment(-1)
	case "c":
		if _, ok := r.Selected(); ok {
			return ReviewComment
		}
	case "v":
		if its := r.items(r.file); r.line < len(its) && its[r.line].kind == itemRow {
			r.selecting, r.anchor, r.hint = true, r.line, ""
		}
	case "e":
		if its := r.items(r.file); r.line < len(its) && its[r.line].kind == itemRow {
			return ReviewExplain
		}
	case "Enter":
		if e := r.cardAtCursor(); e != nil {
			r.reading, r.readTop = e.ID, 0
		}
	case "x":
		if r.cardAtCursor() != nil {
			return ReviewExplainDelete
		}
		if l, ok := r.Selected(); ok && l.Edit != nil {
			return ReviewDelete
		}
	case "Esc":
		r.pane = 0
	}
	return ReviewKeep
}

// readKey handles a key while an answer is open in the diff pane.
func (r *Review) readKey(key string) ReviewAction {
	switch key {
	case "j", "Down":
		r.readTop++
	case "k", "Up":
		r.readTop = max(r.readTop-1, 0)
	case "C-d", "C-u", "C-f", "C-b", "PgDn", "PgUp":
		n, d := r.pageKey(key)
		r.readTop = max(r.readTop+n*d, 0)
	case "g":
		r.readTop = 0
	case "G":
		r.readTop = 1 << 30 // readLines brings it back to the last page
	case "x":
		return ReviewExplainDelete
	case "Esc":
		r.reading = ""
	}
	return ReviewKeep
}

// ReviewText makes text safe to draw: tabs become 4 spaces, control characters go.
func ReviewText(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return -1
		}
		return r
	}, strings.ReplaceAll(s, "\t", "    "))
}

func (r *Review) totals() (add, del int) {
	for _, f := range r.data.Files {
		add, del = add+f.Add, del+f.Del
	}
	return add, del
}

// Render draws the screen in w x h.
func (r *Review) Render(w, h int, plain, narrow bool) string {
	pal := newPalette(plain)
	add, del := r.totals()
	base := r.data.Base
	if base == "" {
		base = "HEAD"
	}
	files := pluralWord(r.data.Total, "file", "files")
	if n := len(r.shown()); n < len(r.data.Files) {
		files = pal.amber.Render(fmt.Sprintf("%d of %s", n, files))
	}
	head := fmt.Sprintf("%s · %s vs %s · %s · +%d −%d", pal.bold.Render("review"), r.Story, base, files, add, del)
	if n := r.Viewed(); n > 0 {
		head += " · " + pal.green.Render(fmt.Sprintf("%d/%d viewed", n, len(r.data.Files)))
	}
	if n := r.Unsent(); n > 0 {
		head += " · " + pal.amber.Render(fmt.Sprintf("%d to send", n))
	}
	if n := r.SentCount(); n > 0 {
		head += " · " + pal.dim.Render(fmt.Sprintf("%d sent", n))
	}
	second := pal.dim.Render(strings.Repeat("─", w))
	if !narrow && w > ReviewFilesWidth+1 {
		// the corner over the divider opens toward the pane that has the focus, drawn bright
		left, right := pal.dim.Render(strings.Repeat("─", ReviewFilesWidth)), pal.cyan.Render(strings.Repeat("─", w-ReviewFilesWidth-1))
		corner := "┌"
		if r.pane == 0 {
			left, right, corner = pal.cyan.Render(strings.Repeat("─", ReviewFilesWidth)), pal.dim.Render(strings.Repeat("─", w-ReviewFilesWidth-1)), "┐"
		}
		second = left + pal.cyan.Render(corner) + right
	}
	if r.data.Note != "" {
		second = pal.amber.Render(truncate(ReviewText(r.data.Note), w))
	}
	lines := []string{truncate(head, w), second}
	bh := max(h-2, 1)
	r.paneH, r.listH = bh, bh
	if r.typing || r.filter != "" {
		r.listH = max(bh-1, 1)
	}
	switch {
	case len(r.data.Files) == 0:
		lines = append(lines, "", pal.dim.Render("No changes since "+base+"."))
	case narrow && r.pane == 0 && !r.help:
		lines = append(lines, r.fileLines(w, bh, pal)...)
	case narrow:
		lines = append(lines, r.paneLines(w, bh, pal, true)...)
	default:
		left, right := r.fileLines(ReviewFilesWidth, bh, pal), r.paneLines(max(w-ReviewFilesWidth-1, 10), bh, pal, false)
		for i := 0; i < bh; i++ {
			l, rt := "", ""
			if i < len(left) {
				l = left[i]
			}
			if i < len(right) {
				rt = right[i]
			}
			lines = append(lines, padCells(l, ReviewFilesWidth)+pal.cyan.Render("│")+rt)
		}
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines[:h], "\n")
}

func pluralWord(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func padCells(s string, w int) string {
	if lw := lipgloss.Width(s); lw < w {
		return s + strings.Repeat(" ", w-lw)
	}
	return s
}

// fileLines lists the files, the selected one in a band, scrolled to keep it in view.
func (r *Review) fileLines(w, h int, pal palette) []string {
	counts := map[string]int{}
	for _, c := range r.data.Comments {
		counts[c.File]++
	}
	var out []string
	rows := h // the list's lines, under the filter's when it has one
	if r.typing || r.filter != "" {
		bar := pal.amber.Render("/") + " " + ReviewText(r.filter)
		if r.typing {
			bar += pal.amber.Render("▏")
		}
		out = append(out, truncate(bar, w))
		rows = max(h-1, 1)
	}
	list := r.shown()
	if len(list) == 0 {
		return append(out, pal.dim.Render(truncate(" no file matches", w)))
	}
	first := max(min(r.listPos(list), len(list)-1)-rows+1, 0)
	for _, i := range list[first:] {
		if len(out) >= h {
			break
		}
		f := r.data.Files[i]
		letter := strings.ToUpper(f.Status[:1])
		right := fmt.Sprintf("+%d −%d", f.Add, f.Del)
		if n := counts[f.Path]; n > 0 {
			right += fmt.Sprintf(" ●%d", n)
		}
		switch f.Viewed {
		case FileViewed:
			right += " ✓"
		case FileChanged:
			right += " ◐"
		}
		line := fit(" "+letter+" "+ReviewText(f.Path), right+" ", w)
		if f.Viewed == FileViewed {
			line = pal.dim.Render(line)
		}
		if i == r.file {
			bg := pal.selBG
			if r.pane == 1 && bg != "" {
				bg = restBG
			}
			line = band(line, w, bg)
			if pal.selBG == "" {
				line = ">" + strings.TrimPrefix(line, " ")
			}
		}
		out = append(out, line)
	}
	return out
}

// paneLines is the diff pane: the open answer, or the selected file's diff.
func (r *Review) paneLines(w, h int, pal palette, narrow bool) []string {
	if r.help {
		return r.helpLines(w, h, pal)
	}
	if r.reading != "" {
		return r.readLines(w, h, pal)
	}
	return r.diffLines(w, h, pal, narrow)
}

// readLines is the open explanation: its header, the question, then the answer wrapped to w,
// scrolled by readTop.
func (r *Review) readLines(w, h int, pal palette) []string {
	e := r.explainByID(r.reading)
	if e == nil {
		return nil
	}
	all := []string{r.explainHeader(e, pal, w), ""}
	if q := strings.TrimSpace(e.Question); q != "" {
		all = append(all, wrapText("Q: "+q, w, pal.dim)...)
		all = append(all, "")
	}
	switch {
	case e.Running():
		all = append(all, pal.dim.Render("Claude is still answering…"))
	case e.State == ExplainDone:
		all = append(all, wrapText(e.Answer, w, lipgloss.NewStyle())...)
	default:
		all = append(all, wrapText(e.Error, w, pal.red)...)
	}
	r.readTop = min(max(r.readTop, 0), max(len(all)-h, 0))
	return all[r.readTop:min(r.readTop+h, len(all))]
}

// wrapText wraps each line of text to w cells, keeping blank lines and indents, drawn in style.
func wrapText(text string, w int, style lipgloss.Style) []string {
	var out []string
	for _, l := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		l = strings.TrimRight(ReviewText(l), " ")
		if l == "" {
			out = append(out, "")
			continue
		}
		for _, part := range strings.Split(ansi.Wrap(l, w, ""), "\n") {
			out = append(out, style.Render(part))
		}
	}
	return out
}

// explainHeader is the one line that names an explanation and tells how it is going.
func (r *Review) explainHeader(e *Explain, pal palette, w int) string {
	who := "code only"
	if e.Session > 0 {
		who = fmt.Sprintf("[%d] %s", e.Session, ReviewText(e.SessionTitle))
	}
	switch e.State {
	case ExplainRunning:
		secs := max(r.now().Unix()-e.Started, 0)
		return pal.brand.Render(truncate(fmt.Sprintf("✦ explaining with %s… %ds · x stop", who, secs), w))
	case ExplainFailed:
		return pal.red.Render(truncate("✦ explain failed · "+ReviewText(strings.ReplaceAll(e.Error, "\n", " ")), w))
	}
	parts := []string{"✦ explain"}
	if e.Outdated {
		parts = append(parts, "outdated")
	}
	parts = append(parts, ReviewLine{StartLine: e.StartLine, Line: e.Line}.Lines(), who)
	if e.TookMS > 0 {
		parts = append(parts, fmt.Sprintf("%ds", (e.TookMS+500)/1000))
	}
	return pal.brand.Render(truncate(strings.Join(parts, " · "), w))
}

// diffLines is the selected file's diff, scrolled to keep the cursor in view.
func (r *Review) diffLines(w, h int, pal palette, narrow bool) []string {
	its := r.items(r.file)
	if r.line < r.top {
		r.top = r.line
	}
	if r.line >= r.top+h {
		r.top = r.line - h + 1
	}
	lo, hi := -1, -1
	if r.selecting {
		lo, hi = min(r.anchor, r.line), max(r.anchor, r.line)
	}
	var out []string
	code := 11 // "%4s %4s " + sign
	if narrow {
		code = 6
	}
	for i := r.top; i < len(its) && len(out) < h; i++ {
		it := its[i]
		var line string
		switch it.kind {
		case itemHunk:
			line = pal.cyan.Render(truncate(ReviewText(it.text), w))
		case itemNote:
			line = pal.dim.Render(truncate("  "+it.text, w))
		case itemViewed:
			style := pal.green
			if r.data.Files[r.file].Viewed == FileChanged {
				style = pal.amber
			}
			line = style.Render(truncate("  "+it.text, w))
		case itemRow:
			sign, style := " ", pal.dim
			switch it.row.Kind {
			case "add":
				sign, style = "+", pal.green
			case "del":
				sign, style = "-", pal.red
			}
			num := func(n int) string {
				if n == 0 {
					return ""
				}
				return fmt.Sprint(n)
			}
			nums := fmt.Sprintf("%4s %4s ", num(it.row.Old), num(it.row.New))
			if narrow {
				nums = fmt.Sprintf("%4s ", num(lineOf(it.row)))
			}
			text := truncate(sign+ReviewText(it.row.Text), w-len(nums))
			numStyle := pal.dim
			if r.inRange(it.row) {
				numStyle = pal.amber
			}
			if it.row.Kind == "ctx" {
				line = numStyle.Render(nums) + text
			} else {
				line = numStyle.Render(nums) + style.Render(text)
			}
		case itemPinned, itemComment:
			line = r.commentLine(it, w, code, pal)
		case itemExplain:
			line = truncate(strings.Repeat(" ", code)+r.explainHeader(it.e, pal, w-code), w)
		case itemExplainLine:
			line = pal.arrow.Render(truncate(strings.Repeat(" ", code)+"┊ "+ReviewText(it.text), w))
		}
		cursor := i == r.line && r.pane == 1 // the list has the focus: no cursor in the code
		switch {
		case i >= lo && i <= hi && pal.selBG != "":
			line = band(line, w, rangeBG)
		case cursor:
			line = band(line, w, pal.selBG)
		}
		switch {
		case pal.selBG != "":
		case cursor:
			line = ">" + strings.TrimPrefix(line, " ")
		case i >= lo && i <= hi:
			line = "┃" + strings.TrimPrefix(line, " ")
		}
		out = append(out, line)
	}
	return out
}

// inRange says whether row is inside an unsent range comment of the file, so its number is
// marked.
func (r *Review) inRange(row DiffRow) bool {
	path := r.data.Files[r.file].Path
	for _, c := range r.data.Comments {
		if c.File == path && !c.Outdated && c.SentAt == 0 && c.First() < c.Line && c.Covers(sideOf(row), lineOf(row)) {
			return true
		}
	}
	return false
}

func (r *Review) commentLine(it reviewItem, w, indent int, pal palette) string {
	c := it.c
	text := ReviewText(it.text)
	switch {
	case it.kind == itemPinned:
		where := c.Lines()
		if c.Outdated {
			where = "outdated · " + where
		}
		text = where + " · " + text
	case it.first && c.First() < c.Line:
		text = strings.TrimPrefix(c.Lines(), "lines ") + " · " + text
	}
	if c.SentAt != 0 && it.first {
		text = fmt.Sprintf("✓ sent → [%d] · %s", c.SentTo, text)
	}
	s := truncate(strings.Repeat(" ", indent)+"┃ "+text, w)
	if c.SentAt != 0 {
		return pal.dim.Render(s)
	}
	return pal.amber.Render(s)
}

// Bar is the key bar of the screen.
func (r *Review) Bar(narrow bool) string {
	if r.help {
		return "j/k scroll · any other key closes"
	}
	if r.typing {
		if narrow {
			return "⏎ keep · esc clear · ↑/↓ file"
		}
		return "type to filter · !word leaves out · ⏎ keep · esc clear · ↑/↓ file"
	}
	if r.reading != "" {
		return "j/k scroll · ^d/^u page · x dismiss · esc close"
	}
	if r.selecting {
		l, _ := r.Selected()
		n := 1
		if l.StartLine > 0 {
			n = l.Line - l.StartLine + 1
		}
		bar := fmt.Sprintf("SELECT %s · %s · j/k move end · o other end · c comment · e explain · esc cancel", l.Lines(), pluralWord(n, "line", "lines"))
		if narrow {
			bar = fmt.Sprintf("SELECT %s · c comment · esc cancel", l.Lines())
		}
		if r.hint != "" {
			bar += " · " + r.hint
		}
		return bar
	}
	send := ""
	if n := r.Unsent(); n > 0 {
		send = fmt.Sprintf(" · S send %d", n)
	}
	clear := ""
	if r.SentCount() > 0 {
		clear = " · C clear"
	}
	if e := r.cardAtCursor(); e != nil && r.pane == 1 {
		stop := "x dismiss"
		if e.Running() {
			stop = "x stop"
		}
		return "⏎ read · " + stop + " · j/k line · esc list"
	}
	switch {
	case narrow && r.pane == 0:
		return "⏎ open · ? keys" + send + " · " + r.escWord()
	case narrow:
		return "c comment · x del" + send + " · esc list"
	case r.pane == 0:
		return "j/k file · ^d/^u page · ⏎ open · space viewed · / filter · H hide viewed" + send + clear + " · ? keys · " + r.escWord()
	}
	return "j/k line · ^d/^u page · c comment · v select · e explain · n/N next" + send + clear + " · ? keys · esc list"
}

// escWord says what Esc does on the screen: clear the filter first, then leave.
func (r *Review) escWord() string {
	if r.filter != "" {
		return "esc clear"
	}
	return "esc back"
}
