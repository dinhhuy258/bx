package client

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/coder/websocket"

	"github.com/dinhhuy258/bx/internal/api"
	"github.com/dinhhuy258/bx/internal/claude"
	"github.com/dinhhuy258/bx/internal/config"
	"github.com/dinhhuy258/bx/internal/state"
	"github.com/dinhhuy258/bx/internal/ui"
)

// fakeBackend serves fixed projects and snapshots and records every action.
type fakeBackend struct {
	projects  []api.ProjectSummary
	snaps     map[string]api.Snapshot
	actions   []string
	dirty     int // stories/close without force is refused with this many files
	created   []api.NewProject
	createErr error
}

func (f *fakeBackend) Projects(context.Context) ([]api.ProjectSummary, error) { return f.projects, nil }

func (f *fakeBackend) Snapshot(_ context.Context, p string) (api.Snapshot, error) {
	s, ok := f.snaps[p]
	if !ok {
		return s, errors.New("project not found")
	}
	return s, nil
}

func (f *fakeBackend) Do(_ context.Context, project, action string, a api.Action) (api.Result, error) {
	parts := []string{project, action}
	if a.Num != 0 {
		parts = append(parts, fmt.Sprint(a.Num))
	}
	for _, s := range []string{a.Story, a.Title, a.Name} {
		if s != "" {
			parts = append(parts, s)
		}
	}
	if a.Force {
		parts = append(parts, "force")
	}
	f.actions = append(f.actions, strings.Join(parts, " "))
	switch {
	case action == "sessions":
		return api.Result{Num: 9}, nil
	case action == "stories/close" && !a.Force && f.dirty > 0:
		return api.Result{Error: "dirty", Dirty: f.dirty}, errors.New("dirty")
	}
	return api.Result{}, nil
}

func (f *fakeBackend) CreateProject(_ context.Context, p api.NewProject) (api.Result, error) {
	f.created = append(f.created, p)
	if f.createErr != nil {
		return api.Result{}, f.createErr
	}
	return api.Result{Name: "lab"}, nil
}

func (f *fakeBackend) Repos(context.Context) ([]string, error) {
	return []string{"me/lab", "me/other"}, nil
}

type fakeConn struct {
	resized  [][2]int
	scrolled []int
	copies   int
	closed   bool
}

func (c *fakeConn) Resize(_ context.Context, cols, rows int) error {
	c.resized = append(c.resized, [2]int{cols, rows})
	return nil
}
func (c *fakeConn) Scroll(_ context.Context, lines int) error {
	c.scrolled = append(c.scrolled, lines)
	return nil
}
func (c *fakeConn) CopyMode(context.Context) error {
	c.copies++
	return nil
}
func (c *fakeConn) Close() { c.closed = true }

func snapshot(name, status string) api.Snapshot {
	p := state.New(name, "/p/"+name)
	p.Stories["s1"] = &state.Story{Branch: "feat/s1"}
	p.AddSession(&state.Session{Story: "s1", UUID: name + "-u1", Title: "Add lease time", Created: time.Now()})
	return api.Snapshot{Project: p, Live: ui.Live{Now: time.Now(), Agents: map[string]claude.Agent{name + "-u1": {SessionID: name + "-u1", Status: status}}}}
}

type harness struct {
	m      *Model
	be     *fakeBackend
	opened []string // "project num"
	conns  []*fakeConn
}

// start builds a client over projects (each with session 1 in story s1) and runs Init.
func start(t *testing.T, w, h int, statuses ...string) *harness {
	t.Helper()
	be := &fakeBackend{snaps: map[string]api.Snapshot{}}
	names := []string{"home-network", "lab"}
	for i, st := range statuses {
		be.snaps[names[i]] = snapshot(names[i], st)
		sum := api.ProjectSummary{Name: names[i]}
		if st == "waiting" {
			sum.Waiting, sum.WaitingSessions = 1, []int{1}
		}
		be.projects = append(be.projects, sum)
	}
	hs := &harness{be: be}
	hs.m = NewModel(config.Default(t.TempDir()), be, "")
	hs.m.tick = func() tea.Cmd { return nil }
	hs.m.SetStreamer(func(_ context.Context, project string, num, cols, rows int, term *Term, _ func(tea.Msg)) (Conn, error) {
		hs.opened = append(hs.opened, fmt.Sprintf("%s %d", project, num))
		term.Feed([]byte("CLAUDE SAYS HI"))
		c := &fakeConn{}
		hs.conns = append(hs.conns, c)
		return c, nil
	})
	hs.update(tea.WindowSizeMsg{Width: w, Height: h})
	drain(hs.m, hs.m.Init())
	return hs
}

// drain runs a command and feeds every message back into the model, recursively.
func drain(m *Model, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case nil:
	case tea.BatchMsg:
		for _, c := range msg {
			drain(m, c)
		}
	default:
		_, next := m.Update(msg)
		drain(m, next)
	}
}

func (h *harness) update(msg tea.Msg) {
	_, cmd := h.m.Update(msg)
	drain(h.m, cmd)
}

func (h *harness) press(keys ...string) {
	for _, k := range keys {
		var msg tea.KeyPressMsg
		switch k {
		case "Enter":
			msg = tea.KeyPressMsg{Code: tea.KeyEnter}
		case "Esc":
			msg = tea.KeyPressMsg{Code: tea.KeyEscape}
		case "Down":
			msg = tea.KeyPressMsg{Code: tea.KeyDown}
		case "PgUp":
			msg = tea.KeyPressMsg{Code: tea.KeyPgUp}
		case "C-/":
			msg = tea.KeyPressMsg{Code: '/', Mod: tea.ModCtrl}
		case "C-_": // what a legacy terminal sends for Ctrl-/
			msg = tea.KeyPressMsg{Code: '_', Mod: tea.ModCtrl}
		default:
			msg = tea.KeyPressMsg{Code: []rune(k)[0], Text: k}
		}
		h.update(msg)
	}
}

func (h *harness) typeText(s string) {
	for _, r := range s {
		h.press(string(r))
	}
}

func TestStartupOpensTheOnlyProject(t *testing.T) {
	h := start(t, 160, 30, "waiting") // the full key bar is about 150 columns wide
	v := h.m.View().Content
	lines := strings.Split(v, "\n")
	if len(lines) != 30 || !strings.Contains(lines[0], "home-network") || !strings.Contains(v, "Add lease time") {
		t.Fatalf("view:\n%s", v)
	}
	if !strings.Contains(lines[29], "j/k move") || !strings.Contains(lines[29], "o projects") {
		t.Fatalf("key bar: %q", lines[29])
	}
	if !strings.Contains(v, "No session open") || len(h.opened) != 0 {
		t.Fatalf("no stream before a session is chosen: %q\n%s", h.opened, v)
	}
}

func TestStartupWithManyProjectsShowsThePicker(t *testing.T) {
	h := start(t, 120, 30, "running", "running")
	v := h.m.View().Content
	if !strings.Contains(v, "home-network") || !strings.Contains(v, "lab") || !strings.Contains(v, "+ Add existing folder") {
		t.Fatalf("picker:\n%s", v)
	}
	h.press("Down", "Enter")
	if h.m.project != "lab" || h.m.picker != nil || !strings.Contains(h.m.View().Content, "Add lease time") {
		t.Fatalf("project=%q\n%s", h.m.project, h.m.View().Content)
	}
}

func TestEnterStreamsTheSessionAndFocusesClaude(t *testing.T) {
	h := start(t, 120, 30, "waiting")
	h.press("Enter") // the cursor starts on session 1
	if strings.Join(h.opened, "|") != "home-network 1" || !h.m.claudeFocused {
		t.Fatalf("opened=%q focused=%v", h.opened, h.m.claudeFocused)
	}
	if v := ansi.Strip(h.m.View().Content); !strings.Contains(v, "CLAUDE SAYS HI") || !strings.Contains(v, " home-network › s1 › [1] Add lease time") || !strings.Contains(v, "◆ asks") {
		t.Fatal(v)
	}
	if c := h.m.View().Cursor; c == nil || c.Position.X < ui.ListWidth {
		t.Fatalf("cursor should be in the Claude pane: %+v", c)
	}
	h.press("x") // goes to Claude, not to the list
	select {
	case b := <-h.m.term.Input():
		if string(b) != "x" {
			t.Fatalf("forwarded %q", b)
		}
	case <-time.After(time.Second):
		t.Fatal("key was not forwarded")
	}
	h.press("C-/")
	if h.m.claudeFocused {
		t.Fatal("C-/ must return to the list")
	}
	h.press("C-_")
	if !h.m.claudeFocused || len(h.opened) != 1 {
		t.Fatalf("C-/ (sent as C-_) on the open session must not reconnect: %q", h.opened)
	}
	h.update(tea.WindowSizeMsg{Width: 140, Height: 40})
	if got := h.conns[0].resized; len(got) == 0 || got[len(got)-1] != [2]int{140 - ui.ListWidth - 1, 37} {
		t.Fatalf("resize not sent: %v", got)
	}
}

func TestWheelAndPgUpScrollTheClaudePane(t *testing.T) {
	h := start(t, 120, 30, "idle")
	if h.m.View().MouseMode != tea.MouseModeNone {
		t.Fatal("no session open: the mouse stays with the terminal")
	}
	h.press("Enter")
	if h.m.View().MouseMode != tea.MouseModeCellMotion {
		t.Fatal("a session is open: the wheel should be captured")
	}
	c := h.conns[0]
	h.update(tea.MouseWheelMsg{X: 5, Y: 5, Button: tea.MouseWheelUp}) // over the list
	h.update(tea.MouseWheelMsg{X: ui.ListWidth + 5, Y: 5, Button: tea.MouseWheelUp})
	h.update(tea.MouseWheelMsg{X: ui.ListWidth + 5, Y: 5, Button: tea.MouseWheelDown})
	h.press("x") // back to live first, then the key
	select {
	case b := <-h.m.term.Input():
		if string(b) != "x" {
			t.Fatalf("forwarded %q", b)
		}
	case <-time.After(time.Second):
		t.Fatal("key was not forwarded")
	}
	h.press("y") // already live: no extra frame
	<-h.m.term.Input()
	h.press("C-/", "PgUp")
	if got, want := fmt.Sprint(c.scrolled), fmt.Sprint([]int{-wheelLines, wheelLines, 0, -h.m.term.Rows() / 2}); got != want {
		t.Fatalf("scroll frames %s, want %s", got, want)
	}
}

func TestPasteGoesToClaude(t *testing.T) {
	h := start(t, 120, 30, "idle")
	h.press("Enter")
	input := func() string {
		var got []byte
		for {
			select {
			case b := <-h.m.term.Input():
				got = append(got, b...)
			case <-time.After(100 * time.Millisecond):
				return string(got)
			}
		}
	}
	h.update(tea.PasteMsg{Content: "one\ntwo"})
	if got := input(); got != "one\ntwo" {
		t.Fatalf("paste forwarded as %q", got)
	}
	h.m.term.Feed([]byte("\x1b[?2004h")) // Claude asks for bracketed paste
	h.update(tea.PasteMsg{Content: "hi"})
	if got := input(); got != "\x1b[200~hi\x1b[201~" {
		t.Fatalf("bracketed paste forwarded as %q", got)
	}
	h.press("C-/")
	h.update(tea.PasteMsg{Content: "list"})
	h.update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModAlt}) // M-c: copy-mode
	h.update(tea.PasteMsg{Content: "copy-mode"})
	if got := input(); got != "" {
		t.Fatalf("a paste on the list or in copy-mode must not reach Claude, got %q", got)
	}
}

func TestCopyModeSendsKeysToTmuxAndCopies(t *testing.T) {
	h := start(t, 120, 30, "idle")
	h.press("Enter", "C-/") // a session open, keys back on the list
	c := h.conns[0]
	h.update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModAlt})
	if c.copies != 1 || !h.m.copying || !h.m.claudeFocused {
		t.Fatalf("copies=%d copying=%v focused=%v", c.copies, h.m.copying, h.m.claudeFocused)
	}
	if !strings.Contains(h.m.View().Content, "COPY") {
		t.Fatal("the pane title should say COPY")
	}
	h.update(tea.MouseWheelMsg{X: ui.ListWidth + 5, Y: 5, Button: tea.MouseWheelUp})
	h.press("k") // to tmux, without leaving copy-mode first
	if b := <-h.m.term.Input(); string(b) != "k" {
		t.Fatalf("forwarded %q", b)
	}
	if fmt.Sprint(c.scrolled) != fmt.Sprint([]int{-wheelLines}) {
		t.Fatalf("copy-mode must not snap back to live: %v", c.scrolled)
	}
	h.update(copyEndMsg{conn: &fakeConn{}}) // from an old stream
	if !h.m.copying {
		t.Fatal("a stale mode frame must be ignored")
	}
	h.update(copyEndMsg{conn: c})
	if h.m.copying || strings.Contains(h.m.View().Content, "COPY") {
		t.Fatal("tmux left copy-mode")
	}
	_, cmd := h.m.Update(clipboardMsg("héllo"))
	if cmd == nil || !strings.Contains(h.m.flash, "copied 5 chars") {
		t.Fatalf("flash=%q cmd=%v", h.m.flash, cmd)
	}
}

func TestToggleReturnsToTheOpenSessionOnly(t *testing.T) {
	h := start(t, 160, 30, "waiting")
	h.press("C-/")
	if h.m.claudeFocused || len(h.opened) != 0 {
		t.Fatalf("with no session open the toggle does nothing: focused=%v opened=%q", h.m.claudeFocused, h.opened)
	}
	h.press("Enter", "C-/") // open session 1, back to the list
	h.press("k")            // cursor to the chats row, away from session 1
	h.press("C-/")
	if !h.m.claudeFocused || h.m.streamNum != 1 || len(h.opened) != 1 {
		t.Fatalf("toggle should go back to session 1 without opening another: focused=%v num=%d opened=%q", h.m.claudeFocused, h.m.streamNum, h.opened)
	}
}

func TestPromptsCallTheBackend(t *testing.T) {
	h := start(t, 120, 30, "waiting")
	h.press("w")
	h.typeText("guest")
	h.press("Enter", "c")
	h.typeText("hi")
	h.press("Enter")
	if !h.m.claudeFocused || strings.Join(h.opened, "|") != "home-network 9" {
		t.Fatalf("a new chat should open and focus: %q", h.opened)
	}
	h.press("C-/", "y") // back to the list; y answers the waiting session 1
	if got := strings.Join(h.be.actions, "|"); got != "home-network stories guest|home-network sessions hi|home-network yes 1" {
		t.Fatalf("actions = %q", got)
	}
}

func TestCloseStoryAsksAgainWhenDirty(t *testing.T) {
	h := start(t, 120, 30, "running")
	h.be.dirty = 2
	h.press("D")
	if !strings.Contains(h.m.View().Content, "close story s1") {
		t.Fatal(h.m.View().Content)
	}
	h.press("y")
	if !strings.Contains(h.m.View().Content, "2 uncommitted") {
		t.Fatal(h.m.View().Content)
	}
	h.press("y")
	if got := strings.Join(h.be.actions, "|"); got != "home-network stories/close s1|home-network stories/close s1 force" {
		t.Fatalf("actions = %q", got)
	}
}

func TestWaitingJumpsAcrossProjects(t *testing.T) {
	h := start(t, 120, 30, "running", "waiting")
	h.press("Enter") // picker: home-network
	h.press("a")
	if h.m.project != "lab" || strings.Join(h.opened, "|") != "lab 1" || !h.m.claudeFocused {
		t.Fatalf("project=%q opened=%q", h.m.project, h.opened)
	}
	if n, _ := h.m.m.SelectedSession(); n != 1 {
		t.Fatalf("selected %d", n)
	}
}

func TestSessionEndedClosesThePane(t *testing.T) {
	h := start(t, 120, 30, "waiting")
	h.press("Enter")
	h.update(streamClosedMsg{conn: h.conns[0], err: websocket.CloseError{Code: websocket.StatusNormalClosure, Reason: "session ended"}})
	if h.m.claudeFocused || h.m.streamNum != 0 || !strings.Contains(h.m.View().Content, "ended") {
		t.Fatalf("focused=%v num=%d\n%s", h.m.claudeFocused, h.m.streamNum, h.m.View().Content)
	}
	h.update(tickMsg(time.Now()))
	if len(h.opened) != 1 {
		t.Fatalf("an ended session must not reconnect: %q", h.opened)
	}
}

func TestDroppedStreamReconnectsOnTick(t *testing.T) {
	h := start(t, 120, 30, "waiting")
	h.press("Enter")
	h.update(streamClosedMsg{conn: h.conns[0], err: errors.New("EOF")})
	h.update(tickMsg(time.Now()))
	if strings.Join(h.opened, "|") != "home-network 1|home-network 1" {
		t.Fatalf("opened=%q", h.opened)
	}
}

func TestStaleConnectIsClosed(t *testing.T) {
	h := start(t, 120, 30, "waiting")
	c := &fakeConn{}
	h.update(connectMsg{gen: h.m.gen - 1, conn: c})
	if !c.closed || h.m.conn == Conn(c) {
		t.Fatal("a connect from an older switch must be closed and ignored")
	}
}

func TestAddFolderFromThePicker(t *testing.T) {
	h := start(t, 120, 30, "running")
	h.press("o")
	h.typeText("existing")
	h.press("Enter")
	h.typeText("/srv/lab")
	h.press("Enter")
	if len(h.be.created) != 1 || h.be.created[0] != (api.NewProject{Kind: "folder", Path: "/srv/lab"}) {
		t.Fatalf("%+v", h.be.created)
	}
	if h.m.project != "lab" {
		t.Fatalf("should switch to the new project: %q", h.m.project)
	}
}

func TestAddFirstFolderWithNoProjects(t *testing.T) {
	h := start(t, 120, 30) // no projects: the picker opens
	h.be.snaps["lab"] = snapshot("lab", "running")
	h.typeText("existing")
	h.press("Enter")
	h.typeText("/srv/lab")
	if v := h.m.View().Content; !strings.Contains(v, "/srv/lab") {
		t.Fatalf("the prompt should show what is typed:\n%s", v)
	}
	h.press("Enter")
	if len(h.be.created) != 1 || h.be.created[0] != (api.NewProject{Kind: "folder", Path: "/srv/lab"}) {
		t.Fatalf("%+v", h.be.created)
	}
	if h.m.project != "lab" || !strings.Contains(h.m.View().Content, "Add lease time") {
		t.Fatalf("project=%q\n%s", h.m.project, h.m.View().Content)
	}
}

func TestCancelledOrFailedFirstAddReopensThePicker(t *testing.T) {
	h := start(t, 120, 30)
	h.typeText("existing")
	h.press("Enter", "Esc")
	if h.m.picker == nil {
		t.Fatalf("Esc should reopen the picker:\n%s", h.m.View().Content)
	}
	h.be.createErr = errors.New("not a git repository")
	h.press("Enter") // the cursor is back on the first entry, + Add existing folder
	h.typeText("/srv/nope")
	h.press("Enter")
	if v := h.m.View().Content; h.m.picker == nil || !strings.Contains(v, "not a git repository") {
		t.Fatalf("a failed add should reopen the picker with the error:\n%s", v)
	}
}

func TestCloneFromThePicker(t *testing.T) {
	h := start(t, 120, 30, "running")
	h.press("o")
	h.typeText("clone")
	h.press("Enter")
	if !strings.Contains(h.m.View().Content, "me/other") {
		t.Fatal(h.m.View().Content)
	}
	h.press("Down", "Enter")
	if len(h.be.created) != 1 || h.be.created[0] != (api.NewProject{Kind: "clone", Repo: "me/other"}) {
		t.Fatalf("%+v", h.be.created)
	}
}

func TestNarrowShowsOnePaneAtATime(t *testing.T) {
	h := start(t, 80, 24, "waiting")
	v := h.m.View().Content
	if strings.Contains(v, "No session open") || !strings.Contains(v, "Add lease time") {
		t.Fatalf("narrow list view:\n%s", v)
	}
	h.press("Enter")
	v = ansi.Strip(h.m.View().Content)
	// the list (its "◆ 1 needs you" header) is hidden; the pane's title line names the session
	if !strings.Contains(v, "CLAUDE SAYS HI") || strings.Contains(v, "◆ 1 needs you") || !strings.Contains(v, "[1] Add lease time") || h.m.term.Cols() != 80 {
		t.Fatalf("narrow Claude view (%d cols):\n%s", h.m.term.Cols(), v)
	}
}

func TestToolKeysExplainThemselves(t *testing.T) {
	h := start(t, 120, 30, "waiting")
	h.press("d")
	if len(h.be.actions) != 0 || !strings.Contains(h.m.View().Content, "not available yet") {
		t.Fatalf("actions=%q\n%s", h.be.actions, h.m.View().Content)
	}
}
