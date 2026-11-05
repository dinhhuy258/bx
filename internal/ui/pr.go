package ui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
)

// PRAction is what a key on the PR screen asks the client to do.
type PRAction int

const (
	PRNone PRAction = iota
	PRBack
	PRRefresh
	PRFix
	PRAddress
	PRSync
	PROpenWeb
)

// PRListWidth is the width of the checks and threads list on a wide screen.
const PRListWidth = 38

// PRScreen is the PR screen of a story: one PR at a time (Tab / S-Tab move between the found
// ones), its checks then its threads, and the threads picked for Address. The client loads;
// PRScreen only draws and moves.
type PRScreen struct {
	Story string
	Now   func() time.Time // nil = time.Now; tests set it

	data     PRData
	loaded   bool
	focus    string          // the repo of the PR on screen
	focusSet bool            // focus names a PR
	want     string          // the repo to open on, if it has a PR
	sel      int             // row cursor over checks then threads
	detail   bool            // narrow: the detail of the selected row is open
	picks    map[string]bool // thread id → picked
	seen     map[string]bool // threads whose pick was set once: SetData keeps the user's choice
}

// NewPRScreen is the screen of story, opening on the PR of repo when it has one.
func NewPRScreen(story, repo string) *PRScreen {
	return &PRScreen{Story: story, want: repo, picks: map[string]bool{}, seen: map[string]bool{}}
}

func (s *PRScreen) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Data is what the screen shows.
func (s *PRScreen) Data() PRData { return s.data }

// SetData shows d. The first time, the PR on screen is the wanted repo's if it has a PR, else
// the first PR that needs you (failed check, then changes requested or open threads, then a
// conflict), else the first found. Threads not seen before are picked when open and never sent.
func (s *PRScreen) SetData(d PRData) {
	first := !s.loaded
	s.data, s.loaded = d, true
	found := d.Found()
	keep := false
	for _, p := range found {
		if s.focusSet && p.Repo == s.focus {
			keep = true
		}
	}
	if !keep {
		s.focusSet = false
		if len(found) > 0 {
			s.focus, s.focusSet = s.firstFocus(found, first), true
		}
	}
	for _, p := range d.PRs {
		for _, t := range p.Threads {
			if s.seen[t.ID] {
				continue
			}
			s.seen[t.ID] = true
			if t.Open(d.Me) && t.SentAt == 0 {
				s.picks[t.ID] = true
			}
		}
	}
	s.clamp()
}

func (s *PRScreen) firstFocus(found []PRInfo, first bool) string {
	if first && s.want != "" {
		for _, p := range found {
			if p.Repo == s.want {
				return p.Repo
			}
		}
	}
	best, rank := found[0], 3
	for _, p := range found {
		r := 3
		switch {
		case p.Failed():
			r = 0
		case p.Decision == "CHANGES_REQUESTED" || p.OpenThreads(s.data.Me) > 0:
			r = 1
		case p.Merge == "DIRTY":
			r = 2
		}
		if r < rank {
			best, rank = p, r
		}
	}
	return best.Repo
}

// Current is the PR on screen.
func (s *PRScreen) Current() (PRInfo, bool) {
	if !s.focusSet {
		return PRInfo{}, false
	}
	for _, p := range s.data.PRs {
		if p.Has() && p.Repo == s.focus {
			return p, true
		}
	}
	return PRInfo{}, false
}

// prRow is a row of the list: a check or a thread.
type prRow struct {
	check  *PRCheck
	thread *PRThread
}

// rows are the checks (failed, pending, passed, skipped), then the threads (open, sent,
// resolved) of the PR on screen.
func (s *PRScreen) rows() []prRow {
	pr, ok := s.Current()
	if !ok {
		return nil
	}
	var out []prRow
	for _, state := range []string{CheckFail, CheckPending, CheckPass, CheckSkip} {
		for i := range pr.Checks {
			if pr.Checks[i].State == state {
				out = append(out, prRow{check: &pr.Checks[i]})
			}
		}
	}
	rank := func(t PRThread) int {
		switch {
		case t.Resolved:
			return 2
		case t.SentAt > 0:
			return 1
		}
		return 0
	}
	var ts []int
	for i := range pr.Threads {
		ts = append(ts, i)
	}
	sort.SliceStable(ts, func(a, b int) bool { return rank(pr.Threads[ts[a]]) < rank(pr.Threads[ts[b]]) })
	for _, i := range ts {
		out = append(out, prRow{thread: &pr.Threads[i]})
	}
	return out
}

func (s *PRScreen) clamp() {
	s.sel = min(max(s.sel, 0), max(len(s.rows())-1, 0))
}

// Picked are the picked thread ids of the PR on screen, in list order.
func (s *PRScreen) Picked() []string {
	var out []string
	for _, r := range s.rows() {
		if r.thread != nil && s.picks[r.thread.ID] && !r.thread.Resolved {
			out = append(out, r.thread.ID)
		}
	}
	return out
}

// Selected is the check or the thread under the cursor.
func (s *PRScreen) Selected() (*PRCheck, *PRThread) {
	rows := s.rows()
	if s.sel < 0 || s.sel >= len(rows) {
		return nil, nil
	}
	return rows[s.sel].check, rows[s.sel].thread
}

// step moves to the next (d = 1) or previous found PR, wrapping.
func (s *PRScreen) step(d int) {
	found := s.data.Found()
	if len(found) < 2 {
		return
	}
	at := 0
	for i, p := range found {
		if p.Repo == s.focus {
			at = i
		}
	}
	s.focus = found[(at+d+len(found))%len(found)].Repo
	s.sel, s.detail = 0, false
}

// Key handles one key.
func (s *PRScreen) Key(key string) PRAction {
	switch key {
	case "j", "Down":
		s.sel++
		s.clamp()
	case "k", "Up":
		s.sel--
		s.clamp()
	case "Tab":
		s.step(1)
	case "S-Tab":
		s.step(-1)
	case "Space":
		if _, t := s.Selected(); t != nil && !t.Resolved {
			s.picks[t.ID] = !s.picks[t.ID]
		}
	case "a":
		for _, r := range s.rows() {
			if r.thread != nil && !r.thread.Resolved {
				s.picks[r.thread.ID] = true
			}
		}
	case "n":
		for _, r := range s.rows() {
			if r.thread != nil {
				delete(s.picks, r.thread.ID)
			}
		}
	case "Enter":
		if len(s.rows()) > 0 {
			s.detail = true
		}
	case "F":
		return PRFix
	case "A":
		return PRAddress
	case "U":
		return PRSync
	case "R":
		return PRRefresh
	case "o":
		return PROpenWeb
	case "Esc":
		if s.detail {
			s.detail = false
			return PRNone
		}
		return PRBack
	}
	return PRNone
}

// Bar is the key bar of the screen.
func (s *PRScreen) Bar(narrow bool) string {
	pr, ok := s.Current()
	if !ok {
		return "R refresh · esc back"
	}
	address := "A address"
	if n := len(s.Picked()); n > 0 {
		address = fmt.Sprintf("A address %d", n)
	}
	fix := "F fix CI · "
	if !pr.Failed() {
		fix = ""
	}
	if narrow {
		if s.detail {
			return fix + address + " · esc list"
		}
		return "⏎ open · space pick · " + fix + address + " · esc back"
	}
	tab := ""
	if len(s.data.Found()) > 1 {
		tab = "Tab/S-Tab PR · "
	}
	return "j/k move · ⏎ details · " + tab + "space pick · a all · n none · " + fix + address + " · U sync · o GitHub · R refresh · esc back"
}

// agoText is "just now", "20s ago", "6m ago", "2h ago".
func agoText(d time.Duration) string {
	switch {
	case d < 10*time.Second:
		return "just now"
	case d < time.Minute:
		return strconv.Itoa(int(d.Seconds())) + "s ago"
	}
	return Ago(d) + " ago"
}

func (s *PRScreen) ago(unix int64) string {
	if unix == 0 {
		return "earlier"
	}
	return agoText(s.now().Sub(time.Unix(unix, 0)))
}

// Render draws the screen in w x h.
func (s *PRScreen) Render(w, h int, plain, narrow bool) string {
	pal := newPalette(plain)
	pr, ok := s.Current()
	lines := s.head(pr, ok, w, pal)
	lines = append(lines, pal.dim.Render(strings.Repeat("─", w)))
	bh := max(h-len(lines), 1)
	switch {
	case !s.loaded:
		lines = append(lines, "", pal.dim.Render("Loading…"))
	case !ok:
		lines = append(lines, "", pal.dim.Render(s.empty()))
	case narrow && !s.detail:
		lines = append(lines, s.listLines(w, bh, pal, pr)...)
	case narrow:
		lines = append(lines, s.detailLines(w, bh, pal)...)
	default:
		left, right := s.listLines(PRListWidth, bh, pal, pr), s.detailLines(max(w-PRListWidth-1, 10), bh, pal)
		for i := 0; i < bh; i++ {
			l, r := "", ""
			if i < len(left) {
				l = left[i]
			}
			if i < len(right) {
				r = right[i]
			}
			lines = append(lines, padCells(l, PRListWidth)+pal.dim.Render("│")+r)
		}
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines[:h], "\n")
}

// empty is the body when no PR was found.
func (s *PRScreen) empty() string {
	notPushed := len(s.data.PRs) > 0
	for _, p := range s.data.PRs {
		if p.Missing != "not pushed" {
			notPushed = false
		}
	}
	if notPushed {
		return "Not pushed. Push the branch and open a PR."
	}
	return "No pull request for this branch yet."
}

// head is the title, the PR bar when the story has several repositories, and the chips.
func (s *PRScreen) head(pr PRInfo, ok bool, w int, pal palette) []string {
	checked := ""
	if s.data.CheckedAt > 0 {
		checked = " · checked " + s.ago(s.data.CheckedAt)
	}
	title := pal.bold.Render("pr") + " " + s.Story
	if !ok {
		return []string{truncate(title+pal.dim.Render(checked), w)}
	}
	many := len(s.data.PRs) > 1
	if !many {
		t := pal.bold.Render("pr") + " " + pal.blue.Render("#"+strconv.Itoa(pr.Number)) + " " + ReviewText(pr.Title) +
			pal.dim.Render(" · "+s.Story+" → "+pr.Base+checked)
		return []string{truncate(t, w), truncate(s.chips(pr, pal), w)}
	}
	found := s.data.Found()
	n := 1
	for i, p := range found {
		if p.Repo == pr.Repo {
			n = i + 1
		}
	}
	title += pal.dim.Render(fmt.Sprintf(" · PR %d of %d%s", n, len(found), checked))
	second := pal.blue.Render("#"+strconv.Itoa(pr.Number)) + " " + ReviewText(pr.Title) + pal.dim.Render(" · "+pr.Repo+" → "+pr.Base)
	return []string{truncate(title, w), s.prBar(pr, w, pal), truncate(second, w), truncate(s.chips(pr, pal), w)}
}

// prBar lists every repository of the story, the one on screen marked.
func (s *PRScreen) prBar(cur PRInfo, w int, pal palette) string {
	var parts []string
	for _, p := range s.data.PRs {
		name := p.Repo
		if name == "" {
			name = "PR"
		}
		if !p.Has() {
			parts = append(parts, pal.dim.Render(name+" "+p.Missing))
			continue
		}
		t := fmt.Sprintf("%s #%d", name, p.Number)
		switch {
		case p.Failed():
			t += " " + pal.red.Render("✗")
		case p.Pending():
			t += " " + pal.amber.Render("◌")
		case p.Passed():
			t += " " + pal.green.Render("✓")
		}
		if n := p.OpenThreads(s.data.Me); n > 0 {
			t += " " + pal.amber.Render(fmt.Sprintf("💬%d", n))
		}
		if p.Repo == cur.Repo {
			t = pal.bold.Render("▸") + t
		} else {
			t = " " + t
		}
		parts = append(parts, t)
	}
	left := " " + strings.Join(parts, pal.dim.Render(" │ "))
	right := ""
	if len(s.data.Found()) > 1 {
		right = pal.dim.Render("Tab › ")
	}
	return fit(left, right, w)
}

// chips sum up the PR on screen.
func (s *PRScreen) chips(pr PRInfo, pal palette) string {
	var c []string
	switch pr.State {
	case PRMerged:
		c = append(c, pal.green.Render("merged"))
	case PRClosed:
		c = append(c, pal.dim.Render("closed"))
	}
	if pr.Draft {
		c = append(c, pal.dim.Render("draft"))
	}
	if n := len(pr.FailedChecks()); n > 0 {
		c = append(c, pal.red.Render("✗ "+pluralWord(n, "check", "checks")+" failed"))
	} else if pr.Pending() {
		c = append(c, pal.amber.Render("◌ checks running"))
	}
	if n := pr.OpenThreads(s.data.Me); n > 0 {
		c = append(c, pal.amber.Render("💬 "+pluralWord(n, "open thread", "open threads")))
	}
	switch pr.Decision {
	case "CHANGES_REQUESTED":
		c = append(c, pal.red.Render("changes requested"))
	case "APPROVED":
		c = append(c, pal.green.Render("approved"))
	case "REVIEW_REQUIRED":
		c = append(c, pal.dim.Render("review required"))
	}
	switch pr.Merge {
	case "DIRTY":
		c = append(c, pal.red.Render("⇄ conflicts with "+pr.Base))
	case "BEHIND":
		c = append(c, pal.dim.Render("⇄ behind "+pr.Base))
	case "CLEAN":
		if pr.State == PROpen && pr.Passed() {
			c = append(c, pal.green.Render("✓ ready"))
		}
	}
	return strings.Join(c, "  ")
}

func checkGlyph(state string, pal palette) string {
	switch state {
	case CheckFail:
		return pal.red.Render("✗")
	case CheckPending:
		return pal.amber.Render("◌")
	case CheckPass:
		return pal.green.Render("✓")
	}
	return pal.dim.Render("–")
}

// listLines are the checks and the threads, the selected row in a band, scrolled to keep it
// in view.
func (s *PRScreen) listLines(w, h int, pal palette, pr PRInfo) []string {
	rows := s.rows()
	var all []string
	at := 0
	nChecks := 0
	for _, r := range rows {
		if r.check != nil {
			nChecks++
		}
	}
	if nChecks > 0 {
		all = append(all, pal.dim.Render("CHECKS"))
	}
	picked := len(s.Picked())
	for i, r := range rows {
		if i == nChecks && len(rows) > nChecks {
			head := "THREADS"
			right := ""
			if picked > 0 {
				right = fmt.Sprintf("%d picked", picked)
			}
			all = append(all, fit(pal.dim.Render(head), pal.dim.Render(right), w))
		}
		var line string
		if c := r.check; c != nil {
			right := ""
			if c.State == CheckSkip {
				right = pal.dim.Render("skipped")
			}
			line = fit(" "+checkGlyph(c.State, pal)+" "+ReviewText(c.Name), right+" ", w)
		} else {
			t := r.thread
			who := ""
			if len(t.Comments) > 0 {
				who = pal.dim.Render(ReviewText(t.Comments[0].Author))
			}
			switch {
			case t.Resolved:
				line = fit(" "+pal.green.Render("✓")+"   "+pal.dim.Render(t.Where()), pal.dim.Render("resolved")+" ", w)
			case t.SentAt > 0 && !s.picks[t.ID]:
				line = fit(" "+pal.amber.Render("⇢")+"   "+t.Where(), pal.dim.Render("sent "+s.ago(t.SentAt))+" ", w)
			default:
				box := "[ ]"
				if s.picks[t.ID] {
					box = pal.amber.Render("[x]")
				}
				line = fit(" "+box+" "+t.Where(), who+" ", w)
			}
		}
		if i == s.sel {
			at = len(all)
			line = band(line, w, pal.selBG)
			if pal.selBG == "" {
				line = ">" + strings.TrimPrefix(line, " ")
			}
		}
		all = append(all, line)
	}
	if len(rows) == 0 {
		all = append(all, pal.dim.Render("No checks and no review threads."))
	}
	first := min(max(at-h+1, 0), max(len(all)-h, 0))
	return all[first:min(first+h, len(all))]
}

// detailLines are the selected check or thread.
func (s *PRScreen) detailLines(w, h int, pal palette) []string {
	c, t := s.Selected()
	var out []string
	switch {
	case c != nil:
		out = append(out, checkGlyph(c.State, pal)+" "+pal.bold.Render(ReviewText(c.Name))+"  "+pal.dim.Render(checkWord(c.State)))
		if c.RunID > 0 {
			out = append(out, "", pal.dim.Render("run "+strconv.FormatInt(c.RunID, 10)))
		}
		if c.URL != "" {
			out = append(out, pal.dim.Render(ReviewText(c.URL)))
		}
		if c.State == CheckFail {
			out = append(out, "", pal.dim.Render("F sends the last lines of the failed step to Claude"))
		}
		out = append(out, "", pal.dim.Render("o opens it on GitHub"))
	case t != nil:
		out = append(out, pal.bold.Render(ReviewText(t.Where())))
		for _, l := range strings.Split(t.Code, "\n") {
			if l == "" {
				continue
			}
			style := pal.dim
			switch l[0] {
			case '+':
				style = pal.green
			case '-':
				style = pal.red
			}
			out = append(out, wrapText("  "+l, w, style)...)
		}
		for _, cm := range t.Comments {
			out = append(out, "", pal.dim.Render(ReviewText(cm.Author)+" · "+s.ago(cm.At)))
			out = append(out, wrapText(cm.Body, w, lipgloss.NewStyle())...)
		}
	}
	if len(out) > h {
		out = out[:h]
	}
	return out
}

func checkWord(state string) string {
	switch state {
	case CheckFail:
		return "failed"
	case CheckPending:
		return "running"
	case CheckPass:
		return "passed"
	}
	return "skipped"
}
