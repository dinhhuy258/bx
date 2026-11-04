package ui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// ReviewAction is what a key did on the review screen.
type ReviewAction int

const (
	ReviewKeep      ReviewAction = iota
	ReviewBack                   // Esc: leave the screen
	ReviewRefresh                // R: load the diff again
	ReviewComment                // c: open the comment form on Selected
	ReviewDelete                 // x: delete Selected's unsent comment
	ReviewSend                   // S: pick a session for the unsent comments
	ReviewClearSent              // C: ask to drop the sent comments
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

// rangeBG is the band of the selected lines, bluer than the cursor's.
const rangeBG = "\x1b[48;2;35;52;84m" // #233454

// ReviewFilesWidth is the file list's width on a wide screen.
const ReviewFilesWidth = 38

type itemKind int

const (
	itemHunk    itemKind = iota
	itemRow              // a diff line: the cursor stops here
	itemPinned           // a comment that has no line in the diff (outdated, folded file): the cursor stops here
	itemComment          // one line of a comment under its diff line
	itemNote             // a folded file's note
)

type reviewItem struct {
	kind  itemKind
	text  string
	row   DiffRow
	hunk  int // a row's hunk
	c     *Comment
	first bool // a comment's first line
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
}

// NewReview starts an empty screen for story, until SetData.
func NewReview(story string) *Review { return &Review{Story: story} }

// Data is the review last set.
func (r *Review) Data() ReviewData { return r.data }

// SetData shows a new load of the review, staying on the same file when it is still there.
func (r *Review) SetData(d ReviewData) {
	path := ""
	if r.file < len(r.data.Files) {
		path = r.data.Files[r.file].Path
	}
	r.data, r.file = d, 0
	r.selecting, r.hint = false, ""
	for i, f := range d.Files {
		if f.Path == path {
			r.file = i
		}
	}
	r.snap(1)
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
	placed := map[*Comment]bool{}
	var body []reviewItem
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
		}
	}
	var pinned []reviewItem
	for _, c := range mine {
		if !placed[c] {
			pinned = append(pinned, commentItems(itemPinned, c)...)
		}
	}
	return append(pinned, body...)
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

func stops(it reviewItem) bool { return it.kind == itemRow || it.kind == itemPinned }

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

func (r *Review) moveFile(d int) {
	if n := len(r.data.Files); n > 0 {
		r.file = min(max(r.file+d, 0), n-1)
		r.line, r.top = 0, 0
		r.selecting, r.hint = false, ""
		r.snap(1)
	}
}

// nextComment goes to the next (d 1) or previous (d -1) line with a comment, across files.
func (r *Review) nextComment(d int) {
	type pos struct{ file, line int }
	var all []pos
	for f := range r.data.Files {
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
				return
			}
		}
		return
	}
	for i := len(all) - 1; i >= 0; i-- {
		if less(all[i], cur) {
			r.file, r.line = all[i].file, all[i].line
			return
		}
	}
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
	if r.line >= len(its) || !stops(its[r.line]) {
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
	case "o":
		r.anchor, r.line = r.line, r.anchor
	case "c":
		return ReviewComment
	case "v", "Esc":
		r.selecting = false
	}
	return ReviewKeep
}

// moveEnd moves the cursor end of the range to the next row of the anchor's hunk.
func (r *Review) moveEnd(d int) {
	its := r.items(r.file)
	for i := r.line + d; i >= 0 && i < len(its); i += d {
		switch {
		case its[i].kind == itemRow && its[i].hunk == its[r.anchor].hunk:
			r.line = i
			return
		case its[i].kind == itemHunk || its[i].kind == itemRow:
			r.hint = "a range stays inside one hunk"
			return
		}
	}
}

// Key handles a key; narrow has two panes, the files and the diff.
func (r *Review) Key(key string, narrow bool) ReviewAction {
	if r.selecting {
		return r.selectKey(key)
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
	if narrow && r.pane == 0 {
		switch key {
		case "j", "Down":
			r.moveFile(1)
		case "k", "Up":
			r.moveFile(-1)
		case "Enter", "l", "Right":
			if len(r.data.Files) > 0 {
				r.pane = 1
			}
		case "Esc":
			return ReviewBack
		}
		return ReviewKeep
	}
	switch key {
	case "j", "Down":
		r.moveLine(1)
	case "k", "Up":
		r.moveLine(-1)
	case "J":
		r.moveFile(1)
	case "K":
		r.moveFile(-1)
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
	case "x":
		if l, ok := r.Selected(); ok && l.Edit != nil {
			return ReviewDelete
		}
	case "Esc":
		if narrow {
			r.pane = 0
			return ReviewKeep
		}
		return ReviewBack
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
	head := fmt.Sprintf("%s · %s vs %s · %s · +%d −%d", pal.bold.Render("review"), r.Story, base,
		pluralWord(r.data.Total, "file", "files"), add, del)
	if n := r.Unsent(); n > 0 {
		head += " · " + pal.amber.Render(fmt.Sprintf("%d to send", n))
	}
	if n := r.SentCount(); n > 0 {
		head += " · " + pal.dim.Render(fmt.Sprintf("%d sent", n))
	}
	second := pal.dim.Render(strings.Repeat("─", w))
	if r.data.Note != "" {
		second = pal.amber.Render(truncate(ReviewText(r.data.Note), w))
	}
	lines := []string{truncate(head, w), second}
	bh := max(h-2, 1)
	switch {
	case len(r.data.Files) == 0:
		lines = append(lines, "", pal.dim.Render("No changes since "+base+"."))
	case narrow && r.pane == 0:
		lines = append(lines, r.fileLines(w, bh, pal)...)
	case narrow:
		lines = append(lines, r.diffLines(w, bh, pal, true)...)
	default:
		left, right := r.fileLines(ReviewFilesWidth, bh, pal), r.diffLines(max(w-ReviewFilesWidth-1, 10), bh, pal, false)
		for i := 0; i < bh; i++ {
			l, rt := "", ""
			if i < len(left) {
				l = left[i]
			}
			if i < len(right) {
				rt = right[i]
			}
			lines = append(lines, padCells(l, ReviewFilesWidth)+pal.dim.Render("│")+rt)
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
	first := max(r.file-h+1, 0)
	var out []string
	for i := first; i < len(r.data.Files) && len(out) < h; i++ {
		f := r.data.Files[i]
		letter := strings.ToUpper(f.Status[:1])
		right := fmt.Sprintf("+%d −%d", f.Add, f.Del)
		if n := counts[f.Path]; n > 0 {
			right += fmt.Sprintf(" ●%d", n)
		}
		line := fit(" "+letter+" "+ReviewText(f.Path), right+" ", w)
		if i == r.file {
			line = band(line, w, pal.selBG)
			if pal.selBG == "" {
				line = ">" + strings.TrimPrefix(line, " ")
			}
		}
		out = append(out, line)
	}
	return out
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
		}
		switch {
		case i >= lo && i <= hi && pal.selBG != "":
			line = band(line, w, rangeBG)
		case i == r.line:
			line = band(line, w, pal.selBG)
		}
		switch {
		case pal.selBG != "":
		case i == r.line:
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
	if r.selecting {
		l, _ := r.Selected()
		n := 1
		if l.StartLine > 0 {
			n = l.Line - l.StartLine + 1
		}
		bar := fmt.Sprintf("SELECT %s · %s · j/k move end · o other end · c comment · esc cancel", l.Lines(), pluralWord(n, "line", "lines"))
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
		clear = " · C clear sent"
	}
	switch {
	case narrow && r.pane == 0:
		return "⏎ open · j/k file" + send + " · esc back"
	case narrow:
		return "c comment · x del" + send + " · esc files"
	}
	return "j/k line · J/K file · c comment · v select · x delete · n/N comments" + send + clear + " · R refresh · esc back"
}
