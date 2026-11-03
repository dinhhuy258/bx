// Package ui holds the list pane: a pure model (this file), the renderer (render.go)
// and the Bubble Tea program (program.go). The model knows nothing about tmux.
package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/dinhhuy258/bx/internal/claude"
	"github.com/dinhhuy258/bx/internal/state"
)

// Diff is +added -deleted lines of a story against main, and its uncommitted files.
type Diff struct {
	Add   int `json:"add"`
	Del   int `json:"del"`
	Dirty int `json:"dirty,omitempty"` // git status --porcelain in the story worktree
}

// Plus adds two diffs, for a story of several repositories.
func (d Diff) Plus(o Diff) Diff { return Diff{d.Add + o.Add, d.Del + o.Del, d.Dirty + o.Dirty} }

// Live is everything derived on each tick and never stored.
type Live struct {
	Now        time.Time               `json:"now"`
	Agents     map[string]claude.Agent `json:"agents"`      // by session uuid
	Dups       map[string]bool         `json:"dups"`        // uuids seen twice
	Diff       map[string]Diff         `json:"diff"`        // by story
	DirtyFiles int                     `json:"dirty_files"` // git status --porcelain in the project folder
	Branch     string                  `json:"branch"`      // current branch of the project folder
	AgentsErr  bool                    `json:"agents_err"`  // claude agents --json failed; keep the last state
	Unread     map[string]bool         `json:"unread"`      // by session uuid: finished a turn nobody has opened since (serve/unread.go)
}

// RowKind tells what a row is.
type RowKind int

const (
	RowStory RowKind = iota // includes the chats group (Story == "")
	RowSession
	RowShell
)

// Row is one selectable item.
type Row struct {
	Kind  RowKind
	Story string // story name, "" = chats
	Num   int    // session number, when Kind == RowSession
	K     int    // shell number, when Kind == RowShell
}

// Shell is a running shell: shell K of Story ("" = chats, the project folder).
type Shell struct {
	Story   string `json:"story"`
	K       int    `json:"k"`
	Title   string `json:"title,omitempty"`   // the rename; "" = use Command
	Command string `json:"command,omitempty"` // what the pane runs now
}

// Label is what the shell is called: its title, else its command, else "shell".
func (s Shell) Label() string {
	switch {
	case s.Title != "":
		return s.Title
	case s.Command != "":
		return s.Command
	}
	return "shell"
}

// PlainShell reports whether cmd is a shell waiting for input (zsh, bash, sh or fish; a login
// shell's leading "-" is ignored) rather than a program running in it.
func PlainShell(cmd string) bool {
	switch strings.TrimPrefix(cmd, "-") {
	case "zsh", "bash", "sh", "fish":
		return true
	}
	return false
}

// Model is the list state for one project.
type Model struct {
	P    *state.Project
	Live Live
	sel  int
	last Live

	folded map[string]bool // story → folded; client memory only
	states map[int]string  // session number → the server's derived state name; nil = unknown

	shells []Shell           // sorted by Story, then K
	dirs   map[string]string // group ("" = chats) → its folder
}

// NewModel builds a model and selects the first session of the first story.
func NewModel(p *state.Project) *Model {
	m := &Model{P: p, folded: map[string]bool{}}
	m.sel = -1
	return m
}

// Folded reports whether the user folded a story.
func (m *Model) Folded(story string) bool { return m.folded[story] }

// SetFolded folds or unfolds a story. Before the first SetLive the cursor is not placed yet.
func (m *Model) SetFolded(story string, on bool) {
	if on {
		m.folded[story] = true
	} else {
		delete(m.folded, story)
	}
	if m.sel >= 0 {
		m.clamp()
	}
}

// SetProject swaps in a new snapshot of the project and keeps the cursor on the same row
// (story or session) when it still exists.
func (m *Model) SetProject(p *state.Project) {
	if m.sel < 0 {
		m.P = p
		return
	}
	cur := m.Selected()
	m.P = p
	for i, r := range m.Rows() {
		if r == cur {
			m.sel = i
			return
		}
	}
	m.clamp()
}

// SetLive stores a new snapshot. On AgentsErr the previous agents are kept.
func (m *Model) SetLive(l Live) {
	if l.AgentsErr && m.last.Agents != nil {
		l.Agents = m.last.Agents
	}
	m.Live = l
	m.last = l
	if m.sel < 0 {
		m.selectInitial()
	}
	m.clamp()
}

func (m *Model) selectInitial() {
	rows := m.Rows()
	m.sel = 0
	for i, r := range rows {
		if r.Kind == RowSession && r.Story != "" {
			m.sel = i
			return
		}
	}
	for i, r := range rows {
		if r.Kind == RowSession {
			m.sel = i
			return
		}
	}
}

// StoryNames returns story names sorted, chats first.
func (m *Model) StoryNames() []string {
	names := make([]string, 0, len(m.P.Stories))
	for n := range m.P.Stories {
		names = append(names, n)
	}
	sort.Strings(names)
	return append([]string{""}, names...)
}

// IsOpen reports whether a group shows its sessions.
func (m *Model) IsOpen(story string) bool {
	if story != "" && m.P.Stories[story] == nil {
		return false
	}
	if !m.folded[story] {
		return true
	}
	return m.HasWaiting(story) // a folded story opens while a session in it waits
}

// HasWaiting reports whether any session of the story waits.
func (m *Model) HasWaiting(story string) bool {
	for _, n := range m.P.SessionsOf(story) {
		if m.State(n) == claude.Waiting {
			return true
		}
	}
	return false
}

// Rows flattens groups and sessions in display order.
func (m *Model) Rows() []Row {
	var rows []Row
	for _, name := range m.StoryNames() {
		rows = append(rows, Row{Kind: RowStory, Story: name})
		if m.IsOpen(name) {
			for _, n := range m.P.SessionsOf(name) {
				rows = append(rows, Row{Kind: RowSession, Story: name, Num: n})
			}
			for _, sh := range m.ShellsOf(name) {
				rows = append(rows, Row{Kind: RowShell, Story: name, K: sh.K})
			}
		}
	}
	return rows
}

// SetShells stores the project's shells and group folders and keeps the cursor on the same
// row when it still exists.
func (m *Model) SetShells(sh []Shell, dirs map[string]string) {
	cp := append([]Shell(nil), sh...)
	sort.Slice(cp, func(i, j int) bool {
		if cp[i].Story != cp[j].Story {
			return cp[i].Story < cp[j].Story
		}
		return cp[i].K < cp[j].K
	})
	if m.sel < 0 {
		m.shells, m.dirs = cp, dirs
		return
	}
	cur := m.Selected()
	m.shells, m.dirs = cp, dirs
	for i, r := range m.Rows() {
		if r == cur {
			m.sel = i
			return
		}
	}
	m.clamp()
}

// ShellsOf returns the shells of a group ("" = chats), by K.
func (m *Model) ShellsOf(story string) []Shell {
	var out []Shell
	for _, sh := range m.shells {
		if sh.Story == story {
			out = append(out, sh)
		}
	}
	return out
}

// Shell returns shell k of a group.
func (m *Model) Shell(story string, k int) (Shell, bool) {
	for _, sh := range m.shells {
		if sh.Story == story && sh.K == k {
			return sh, true
		}
	}
	return Shell{}, false
}

// NextShell is the lowest shell number not in use in a group, the one a new shell gets.
func (m *Model) NextShell(story string) int {
	k := 1
	for {
		if _, ok := m.Shell(story, k); !ok {
			return k
		}
		k++
	}
}

// SelectedShell returns the selected shell, or false when the cursor is not on a shell row.
func (m *Model) SelectedShell() (Shell, bool) {
	r := m.Selected()
	if r.Kind != RowShell {
		return Shell{}, false
	}
	return m.Shell(r.Story, r.K)
}

// SelectShell unfolds the story and puts the selection on the shell row.
func (m *Model) SelectShell(story string, k int) {
	delete(m.folded, story)
	for i, r := range m.Rows() {
		if r.Kind == RowShell && r.Story == story && r.K == k {
			m.sel = i
			return
		}
	}
}

// SelectAfterShell moves the selection off shell k of a group that went away: to the next
// shell with a higher number, else the highest one left, else the group header.
func (m *Model) SelectAfterShell(story string, k int) {
	var next, last int
	for _, sh := range m.ShellsOf(story) {
		if sh.K > k && next == 0 {
			next = sh.K
		}
		if sh.K != k {
			last = sh.K
		}
	}
	switch {
	case next != 0:
		m.SelectShell(story, next)
	case last != 0:
		m.SelectShell(story, last)
	default:
		m.SelectStory(story)
	}
}

// Dir is the folder of a group's shells: the story worktree, or the project folder for chats.
func (m *Model) Dir(story string) string {
	if d, ok := m.dirs[story]; ok {
		return d
	}
	if story == "" && m.P != nil {
		return m.P.Path
	}
	return ""
}

func (m *Model) clamp() {
	n := len(m.Rows())
	if m.sel >= n {
		m.sel = n - 1
	}
	if m.sel < 0 {
		m.sel = 0
	}
}

// Selected returns the current row.
func (m *Model) Selected() Row {
	rows := m.Rows()
	if len(rows) == 0 {
		return Row{Kind: RowStory}
	}
	m.clamp()
	return rows[m.sel]
}

// SelIndex is the index of the selected row.
func (m *Model) SelIndex() int { m.clamp(); return m.sel }

// SelectedSession returns the selected session, or nil on a story row.
func (m *Model) SelectedSession() (int, *state.Session) {
	r := m.Selected()
	if r.Kind != RowSession {
		return 0, nil
	}
	return r.Num, m.P.Sessions[r.Num]
}

// Move moves the selection by delta, clamped.
func (m *Model) Move(delta int) { m.sel += delta; m.clamp() }

// SelectStory puts the selection on a story row.
func (m *Model) SelectStory(story string) {
	for i, r := range m.Rows() {
		if r.Kind == RowStory && r.Story == story {
			m.sel = i
			return
		}
	}
}

// SelectSession unfolds its story and puts the selection on the session row.
func (m *Model) SelectSession(num int) {
	s := m.P.Sessions[num]
	if s == nil {
		return
	}
	delete(m.folded, s.Story)
	for i, r := range m.Rows() {
		if r.Kind == RowSession && r.Num == num {
			m.sel = i
			return
		}
	}
}

// ToggleFold folds or unfolds the selected group: a story or chats (or the group of the
// selected session). A group with a waiting session stays open. Selection stays on the
// group row.
func (m *Model) ToggleFold() {
	r := m.Selected()
	if r.Story != "" && m.P.Stories[r.Story] == nil {
		return
	}
	if m.IsOpen(r.Story) {
		if m.HasWaiting(r.Story) {
			return
		}
		m.folded[r.Story] = true
	} else {
		delete(m.folded, r.Story)
	}
	m.SelectStory(r.Story)
}

// FoldAll unfolds every group (stories and chats) when all are folded, otherwise folds all.
func (m *Model) FoldAll() {
	names := m.StoryNames()
	allFolded := true
	for _, name := range names {
		if !m.folded[name] {
			allFolded = false
		}
	}
	for _, name := range names {
		m.SetFolded(name, !allFolded)
	}
	m.clamp()
}

// State returns the live state of a session. Done sessions are Idle.
func (m *Model) State(num int) claude.State {
	s := m.P.Sessions[num]
	if s == nil || s.Done {
		return claude.Idle
	}
	a, ok := m.Live.Agents[s.UUID]
	if !ok {
		return claude.Idle
	}
	return a.State()
}

// Unread reports an idle session that finished a turn nobody has opened since.
func (m *Model) Unread(num int) bool {
	s := m.P.Sessions[num]
	return s != nil && !s.Done && m.Live.Unread[s.UUID] && !m.Starting(num) && !m.Gone(num) && m.State(num) == claude.Idle
}

// Gone reports a session whose process is not in the agents list and is not done.
func (m *Model) Gone(num int) bool {
	s := m.P.Sessions[num]
	if s == nil || s.Done || m.Live.Agents == nil || m.Starting(num) {
		return false
	}
	_, ok := m.Live.Agents[s.UUID]
	return !ok
}

// Starting reports a session the server says has a tmux session but no Claude in the agents
// list yet. It is never Gone.
func (m *Model) Starting(num int) bool {
	s := m.P.Sessions[num]
	return s != nil && !s.Done && m.states[num] == "starting"
}

// SetStates stores the server's derived state names by session number (Snapshot.SessionStates),
// which know about tmux. nil means unknown.
func (m *Model) SetStates(st map[int]string) { m.states = st }

// Alive reports whether a session still has a tmux session, so stopping it kills a process.
// Without states from the server it falls back to the agents list.
func (m *Model) Alive(num int) bool {
	s := m.P.Sessions[num]
	if s == nil || s.Done {
		return false
	}
	if m.states != nil {
		switch m.states[num] {
		case "starting", "running", "waiting", "idle":
			return true
		}
		return false
	}
	return !m.Gone(num)
}

// StateText is the second line of a session row.
func (m *Model) StateText(num int) string {
	s := m.P.Sessions[num]
	if s == nil {
		return ""
	}
	if s.Done {
		return "done · r to resume"
	}
	if m.Starting(num) {
		return "starting"
	}
	if m.Gone(num) {
		return "gone · r to resume"
	}
	a := m.Live.Agents[s.UUID]
	var text string
	switch a.State() {
	case claude.Waiting:
		w := a.WaitingFor
		if w == "" {
			w = "needs you"
		}
		text = "asks: " + w
	case claude.Running:
		text = "running"
	default:
		text = "idle"
		if m.Unread(num) {
			text = "idle · unread"
		}
	}
	if m.Live.Dups[s.UUID] {
		text += " · duplicate"
	}
	if m.Live.AgentsErr {
		text += " ?"
	}
	return text
}

func (m *Model) branch() string {
	if m.Live.Branch == "" {
		return "main"
	}
	return m.Live.Branch
}

// Counts returns the running, waiting and unread totals for the project.
func (m *Model) Counts() (running, waiting, unread int) {
	for n, s := range m.P.Sessions {
		if s.Done {
			continue
		}
		switch m.State(n) {
		case claude.Running:
			running++
		case claude.Waiting:
			waiting++
		default:
			if m.Unread(n) {
				unread++
			}
		}
	}
	return
}

// Summary is the folded-story marker string: "◆1 ●2 ◎1 ○1 ✓1" (only non-zero parts).
func (m *Model) Summary(story string) string {
	var wait, run, unread, idle, done int
	for _, n := range m.P.SessionsOf(story) {
		if m.P.Sessions[n].Done {
			done++
			continue
		}
		switch m.State(n) {
		case claude.Waiting:
			wait++
		case claude.Running:
			run++
		default:
			if m.Unread(n) {
				unread++
			} else {
				idle++
			}
		}
	}
	var parts []string
	for _, p := range []struct {
		dot string
		n   int
	}{{"◆", wait}, {"●", run}, {"◎", unread}, {"○", idle}, {"✓", done}} {
		if p.n > 0 {
			parts = append(parts, fmt.Sprintf("%s%d", p.dot, p.n))
		}
	}
	return strings.Join(parts, " ")
}

// Dot is the state marker of a session row.
func (m *Model) Dot(num int) string {
	s := m.P.Sessions[num]
	if s != nil && s.Done {
		return "✓"
	}
	if m.Starting(num) {
		return "◌"
	}
	switch m.State(num) {
	case claude.Waiting:
		return "◆"
	case claude.Running:
		return "●"
	}
	if m.Unread(num) {
		return "◎"
	}
	return "○"
}

// Age renders a session's age like the prototype: 6m, 1h, 1d.
func (m *Model) Age(num int) string {
	s := m.P.Sessions[num]
	if s == nil || s.Created.IsZero() {
		return ""
	}
	return Ago(m.Live.Now.Sub(s.Created))
}

// Ago renders a duration the same short way: now, 6m, 1h, 1d.
func Ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// NarrowWidth is the window width below which list and Claude cannot share the screen.
const NarrowWidth = 100

// IsNarrow reports whether the whole tmux window is too small for two panes.
func IsNarrow(windowWidth int) bool { return windowWidth < NarrowWidth }
