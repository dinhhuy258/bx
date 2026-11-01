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
	"github.com/dinhhuy258/bx/internal/settings"
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
	values    map[string]string // the server's settings
	saves     []map[string]string
	saveErr   error
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

func (f *fakeBackend) Settings(context.Context) (api.Settings, error) {
	return api.Settings{Sections: settings.Sections, Values: f.values}, nil
}

func (f *fakeBackend) SaveSettings(_ context.Context, values map[string]string) (api.Settings, error) {
	f.saves = append(f.saves, values)
	if f.saveErr != nil {
		return api.Settings{}, f.saveErr
	}
	if f.values == nil {
		f.values = map[string]string{}
	}
	for k, v := range values {
		f.values[k] = v
	}
	return api.Settings{Sections: settings.Sections, Values: f.values}, nil
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
	tmux   []string // tmux pane moves: "L" or "R"
}

// start builds a client over projects (each with session 1 in story s1), opening the first one
// as `bx <project>` does, and runs Init.
func start(t *testing.T, w, h int, statuses ...string) *harness {
	t.Helper()
	first := ""
	if len(statuses) > 0 {
		first = "home-network"
	}
	return startIn(t, t.TempDir(), first, w, h, statuses...)
}

// startIn is start with the client root (where the opened projects are saved) and the project
// asked for on the command line ("" = none).
func startIn(t *testing.T, root, project string, w, h int, statuses ...string) *harness {
	t.Helper()
	be := &fakeBackend{snaps: map[string]api.Snapshot{}}
	names := []string{"home-network", "lab"}
	for i, st := range statuses {
		be.snaps[names[i]] = snapshot(names[i], st)
		sum := api.ProjectSummary{Name: names[i]}
		switch st {
		case "waiting":
			sum.Waiting, sum.WaitingSessions = 1, []int{1}
		case "running":
			sum.Running = 1
		}
		be.projects = append(be.projects, sum)
	}
	hs := &harness{be: be}
	hs.m = NewModel(config.Default(root), be, project)
	hs.m.tick = func() tea.Cmd { return nil }
	hs.m.tmux = func(dir string) tea.Cmd { hs.tmux = append(hs.tmux, dir); return nil }
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
		case "C-h", "C-l":
			msg = tea.KeyPressMsg{Code: rune(k[2]), Mod: tea.ModCtrl}
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

func TestStartupWithNothingOpenedIsEmpty(t *testing.T) {
	h := startIn(t, t.TempDir(), "", 120, 30, "running", "running")
	v := h.m.View().Content
	if h.m.picker != nil || h.m.project != "" || !strings.Contains(v, "No project open.") || !strings.Contains(v, "o open a project") {
		t.Fatalf("picker=%v project=%q\n%s", h.m.picker != nil, h.m.project, v)
	}
	h.press("o", "Down", "Enter")
	if h.m.project != "lab" || h.m.picker != nil || !strings.Contains(h.m.View().Content, "Add lease time") {
		t.Fatalf("project=%q\n%s", h.m.project, h.m.View().Content)
	}
}

func TestOpenedProjectsAreSavedAndRestored(t *testing.T) {
	root := t.TempDir()
	h := startIn(t, root, "", 120, 30, "running", "running")
	h.press("o", "Enter") // home-network
	h.press("o")
	if items := ansi.Strip(h.m.View().Content); strings.Contains(items, "home-network") || !strings.Contains(items, "lab") {
		t.Fatalf("the picker must leave out opened projects:\n%s", items)
	}
	h.press("Enter") // lab, the only one left
	h.press("[")     // back to home-network: shown last
	h = startIn(t, root, "", 120, 30, "running", "running")
	if h.m.project != "home-network" || strings.Join(h.m.opened.Projects, ",") != "home-network,lab" {
		t.Fatalf("restored project=%q opened=%v", h.m.project, h.m.opened.Projects)
	}
	if v := ansi.Strip(h.m.View().Content); !strings.Contains(v, "◉ home-network  ● lab") {
		t.Fatalf("strip:\n%s", v)
	}
}

func TestCloseProject(t *testing.T) {
	root := t.TempDir()
	h := startIn(t, root, "", 120, 30, "running", "running")
	h.press("o", "Enter", "o", "Enter") // home-network, then lab
	h.press("x")
	if h.m.project != "home-network" || strings.Join(h.m.opened.Projects, ",") != "home-network" {
		t.Fatalf("project=%q opened=%v", h.m.project, h.m.opened.Projects)
	}
	h.press("o")
	if v := ansi.Strip(h.m.View().Content); !strings.Contains(v, "lab") {
		t.Fatalf("a closed project goes back to the picker:\n%s", v)
	}
	h.press("Esc", "x")
	if v := h.m.View().Content; h.m.project != "" || h.m.picker != nil || !strings.Contains(v, "No project open.") {
		t.Fatalf("project=%q\n%s", h.m.project, v)
	}
	if h = startIn(t, root, "", 120, 30, "running", "running"); h.m.project != "" {
		t.Fatalf("nothing opened must stay empty after a restart: %q", h.m.project)
	}
}

func TestFocusedPaneIsLit(t *testing.T) {
	h := start(t, 120, 30, "waiting")
	v := h.m.View().Content
	// the joint points at the focused pane; only the list goes faint
	if !strings.Contains(v, "┫") || strings.Contains(v, "┣") || strings.Contains(v, "\x1b[2m") {
		t.Fatalf("list focused:\n%q", v)
	}
	h.press("Enter")
	v = h.m.View().Content
	if !strings.Contains(v, "┣") || strings.Contains(v, "┫") || !strings.Contains(v, "\x1b[2m") {
		t.Fatalf("Claude focused:\n%q", v)
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
	h.press("C-h")
	if h.m.claudeFocused {
		t.Fatal("C-h must return to the list")
	}
	h.press("C-l")
	if !h.m.claudeFocused || len(h.opened) != 1 {
		t.Fatalf("C-l on the open session must not reconnect: %q", h.opened)
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
	h.press("C-h", "PgUp")
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
	h.press("C-h")
	h.update(tea.PasteMsg{Content: "list"})
	h.update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModAlt}) // M-c: copy-mode
	h.update(tea.PasteMsg{Content: "copy-mode"})
	if got := input(); got != "" {
		t.Fatalf("a paste on the list or in copy-mode must not reach Claude, got %q", got)
	}
}

func TestCopyModeSendsKeysToTmuxAndCopies(t *testing.T) {
	h := start(t, 120, 30, "idle")
	h.press("Enter", "C-h") // a session open, keys back on the list
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

func TestPaneRightReturnsToTheOpenSessionOnly(t *testing.T) {
	h := start(t, 160, 30, "waiting")
	h.press("C-l")
	if h.m.claudeFocused || len(h.opened) != 0 || strings.Join(h.tmux, "") != "R" {
		t.Fatalf("with no session open C-l goes on to tmux: focused=%v opened=%q tmux=%q", h.m.claudeFocused, h.opened, h.tmux)
	}
	h.press("Enter", "C-h") // open session 1, back to the list
	h.press("k")            // cursor to the chats row, away from session 1
	h.press("C-l")
	if !h.m.claudeFocused || h.m.streamNum != 1 || len(h.opened) != 1 {
		t.Fatalf("C-l should go back to session 1 without opening another: focused=%v num=%d opened=%q", h.m.claudeFocused, h.m.streamNum, h.opened)
	}
}

func TestPaneKeysPastTheEdgeGoToTmux(t *testing.T) {
	h := start(t, 160, 30, "waiting")
	h.press("C-h") // already in the list: the tmux pane on the left
	h.press("Enter", "C-l", "C-l")
	if !h.m.claudeFocused || strings.Join(h.tmux, "") != "LRR" {
		t.Fatalf("focused=%v tmux=%q", h.m.claudeFocused, h.tmux)
	}
	select {
	case b := <-h.m.term.Input():
		t.Fatalf("C-l reached Claude: %q", b)
	default:
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
	h.press("C-h", "y") // back to the list; y answers the waiting session 1
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
	h.update(tea.PasteMsg{Content: "/srv/lab\r\n"}) // ⌘V, with the newline a copied path often has
	h.press("Enter")
	if len(h.be.created) != 1 || h.be.created[0] != (api.NewProject{Kind: "folder", Path: "/srv/lab"}) {
		t.Fatalf("%+v", h.be.created)
	}
	if h.m.project != "lab" {
		t.Fatalf("should switch to the new project: %q", h.m.project)
	}
}

func TestAddFirstFolderWithNoProjects(t *testing.T) {
	h := start(t, 120, 30) // no projects: the empty screen
	h.be.snaps["lab"] = snapshot("lab", "running")
	h.press("o")
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

func TestCancelledOrFailedFirstAddStaysEmpty(t *testing.T) {
	h := start(t, 120, 30)
	h.press("o")
	h.typeText("existing")
	h.press("Enter", "Esc")
	if v := h.m.View().Content; h.m.picker != nil || !strings.Contains(v, "No project open.") {
		t.Fatalf("Esc should go back to the empty screen:\n%s", v)
	}
	h.be.createErr = errors.New("not a git repository")
	h.press("o", "Enter") // + Add existing folder
	h.typeText("/srv/nope")
	h.press("Enter")
	if v := h.m.View().Content; h.m.picker != nil || !strings.Contains(v, "No project open.") || !strings.Contains(v, "not a git repository") {
		t.Fatalf("a failed add should show the error on the empty screen:\n%s", v)
	}
}

func TestCloneFromThePicker(t *testing.T) {
	h := start(t, 120, 30, "running")
	h.press("o")
	h.update(tea.PasteMsg{Content: "clone"}) // a paste filters the picker
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

func TestProjectStripAndSwitchKeys(t *testing.T) {
	h := start(t, 200, 30, "waiting", "running")
	h.press("o", "Enter", "[") // open lab, then back to home-network
	v := ansi.Strip(h.m.View().Content)
	if !strings.Contains(v, "◉ home-network  ● lab") || !strings.Contains(v, "[ ] switch project") || !strings.Contains(v, "A activity") {
		t.Fatalf("strip and key bar:\n%s", v)
	}
	h.press("]")
	if h.m.project != "lab" {
		t.Fatalf("] opened %q", h.m.project)
	}
	h.press("]") // wraps
	if h.m.project != "home-network" {
		t.Fatalf("] wrapped to %q", h.m.project)
	}
	h.press("[")
	if h.m.project != "lab" {
		t.Fatalf("[ opened %q", h.m.project)
	}
}

func TestActivityListsEveryProject(t *testing.T) {
	h := start(t, 160, 30, "running", "waiting")
	h.press("A") // the activity list
	v := ansi.Strip(h.m.View().Content)
	// lab waits, so it comes first
	lab, home := strings.Index(v, "  lab "), strings.Index(v, "  home-network ")
	if !strings.Contains(v, "bx activity") || lab < 0 || home < 0 || lab > home {
		t.Fatalf("activity:\n%s", v)
	}
	if !strings.Contains(v, "s1 · asks: needs you") || !strings.Contains(v, "s1 · running") || !strings.Contains(v, "A back") || !strings.Contains(v, "y yes") {
		t.Fatalf("rows and key bar:\n%s", v)
	}
	h.press("y") // the cursor starts on lab's waiting session
	if got := strings.Join(h.be.actions, "|"); got != "lab yes 1" {
		t.Fatalf("actions %q", got)
	}
	h.press("Enter")
	if h.m.project != "lab" || !h.m.claudeFocused || strings.Join(h.opened, "|") != "lab 1" || !h.m.activity {
		t.Fatalf("project=%q focused=%v opened=%q activity=%v", h.m.project, h.m.claudeFocused, h.opened, h.m.activity)
	}
	h.press("C-h", "k", "Enter") // back to the list, up to lab's header: its list
	if h.m.activity || h.m.project != "lab" {
		t.Fatalf("activity=%v project=%q", h.m.activity, h.m.project)
	}
}

func TestActivityEmpty(t *testing.T) {
	h := start(t, 160, 30, "idle")
	h.press("A")
	v := ansi.Strip(h.m.View().Content)
	if !strings.Contains(v, "Nothing is running or waiting.") || !strings.Contains(v, "A back to home-network · o projects") {
		t.Fatalf("empty activity:\n%s", v)
	}
	h.press("A")
	if h.m.activity {
		t.Fatal("A leaves the activity list")
	}
}

func TestSettingsScreenEditsAndSaves(t *testing.T) {
	h := start(t, 120, 30, "running")
	h.press("S")
	v := ansi.Strip(h.m.View().Content)
	if h.m.settings == nil || !strings.Contains(v, "bx settings") || !strings.Contains(v, "Permission mode") {
		t.Fatalf("S should open the settings:\n%s", v)
	}
	h.press("l", "j", "j", "l")
	v = ansi.Strip(h.m.View().Content)
	if !strings.Contains(v, "◂ opus") || !strings.Contains(v, "• 2 unsaved") {
		t.Fatalf("two changes should be marked:\n%s", v)
	}
	h.press("Enter")
	if len(h.be.saves) != 1 || h.be.saves[0]["claude.model"] != "opus" || h.be.saves[0]["claude.permission_mode"] != "acceptEdits" {
		t.Fatalf("saves = %v", h.be.saves)
	}
	if v = ansi.Strip(h.m.View().Content); strings.Contains(v, "unsaved") || !strings.Contains(v, "settings saved") {
		t.Fatalf("after the save:\n%s", v)
	}
	h.press("Esc")
	if h.m.settings != nil {
		t.Fatal("Esc with nothing unsaved closes")
	}
}

func TestSettingsEscAsksBeforeDiscarding(t *testing.T) {
	h := start(t, 120, 30, "running")
	h.press("S", "l", "Esc")
	if h.m.settings == nil || !strings.Contains(ansi.Strip(h.m.View().Content), "discard 1 change? y/n") {
		t.Fatalf("Esc with a change should ask:\n%s", ansi.Strip(h.m.View().Content))
	}
	h.press("n")
	if h.m.settings == nil || len(h.m.settings.Changes()) != 1 {
		t.Fatal("n keeps the screen and the change")
	}
	h.press("Esc", "y")
	if h.m.settings != nil || len(h.be.saves) != 0 {
		t.Fatalf("y discards: open=%v saves=%v", h.m.settings != nil, h.be.saves)
	}
}

func TestSettingsSaveErrorKeepsTheEdits(t *testing.T) {
	h := start(t, 120, 30, "running")
	h.be.saveErr = errors.New("bx serve unreachable")
	h.press("S", "l", "Enter")
	v := ansi.Strip(h.m.View().Content)
	if h.m.settings == nil || len(h.m.settings.Changes()) != 1 || !strings.Contains(v, "bx serve unreachable") {
		t.Fatalf("a failed save keeps the screen and its edits:\n%s", v)
	}
}
