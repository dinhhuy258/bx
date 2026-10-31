package client

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/coder/websocket"

	"github.com/dinhhuy258/bx/internal/api"
	"github.com/dinhhuy258/bx/internal/claude"
	"github.com/dinhhuy258/bx/internal/config"
	"github.com/dinhhuy258/bx/internal/ui"
)

// Backend is what the model needs from the server; *API implements it.
type Backend interface {
	Projects(ctx context.Context) ([]api.ProjectSummary, error)
	Snapshot(ctx context.Context, project string) (api.Snapshot, error)
	Do(ctx context.Context, project, action string, a api.Action) (api.Result, error)
	CreateProject(ctx context.Context, p api.NewProject) (api.Result, error)
	Repos(ctx context.Context) ([]string, error)
}

// Conn is one open session stream.
type Conn interface {
	Resize(ctx context.Context, cols, rows int) error
	Scroll(ctx context.Context, lines int) error
	CopyMode(ctx context.Context) error
	Close()
}

// Streamer opens the stream of one session into term; `bx` passes the WebSocket dialer.
type Streamer func(ctx context.Context, project string, num, cols, rows int, term *Term, send func(tea.Msg)) (Conn, error)

type (
	tickMsg     time.Time
	projectsMsg struct {
		list []api.ProjectSummary
		err  error
	}
	snapshotMsg struct {
		project string
		snap    api.Snapshot
		err     error
	}
	connectMsg struct {
		gen  int
		conn Conn
		err  error
	}
	reposMsg struct {
		repos []string
		err   error
	}
	createdMsg struct {
		res api.Result
		err error
	}
)

// Picker entries after the project names.
const (
	addFolder  = "+ Add existing folder"
	cloneRepo  = "+ Clone from GitHub"
	newProject = "+ New empty project"
)

type pickKind int

const (
	pickProjects pickKind = iota
	pickRepos
)

// Model is the client: the list of the open project (ui.Model, drawn by ui.Render), the Claude
// pane of the open session (Term) and the key bar, in one Bubble Tea program. Folds and the
// cursor live here, per project; the server only knows projects, stories and sessions.
type Model struct {
	cfg  *config.Config
	be   Backend
	open Streamer
	send func(tea.Msg)
	tick func() tea.Cmd

	projects []api.ProjectSummary
	models   map[string]*ui.Model // per project, so folds and cursor survive a switch
	project  string               // the open project; "" until one is chosen
	m        *ui.Model            // models[project]; nil until its first snapshot
	loaded   bool                 // the first projects reply arrived

	term       *Term
	conn       Conn
	streamNum  int // the session shown in the Claude pane; 0 = none
	gen        int // bumped on every stream switch; older connects are dropped
	connecting bool
	pendingSel int // select this session when the project's first snapshot arrives

	picker   *ui.Picker
	pickKind pickKind

	width, height int
	narrow        bool
	claudeFocused bool
	focusMode     bool // Claude pane fills the width
	scrolled      bool // the Claude pane may be back in history; the next key returns to live first
	copying       bool // the Claude pane is in tmux copy-mode: keys go to tmux until it leaves
	prompt        ui.Prompt
	closeDirty    int // uncommitted files of the story in a force prompt
	seq           string
	flash         string
	flashAt       time.Time
	showKeys      bool
	ctx           context.Context
	cancel        context.CancelFunc
}

// NewModel builds the client. project is the one to open first ("" = the only one, or ask).
func NewModel(cfg *config.Config, be Backend, project string) *Model {
	ctx, cancel := context.WithCancel(context.Background())
	return &Model{cfg: cfg, be: be, project: project, models: map[string]*ui.Model{}, term: NewTerm(80, 24), tick: tick, ctx: ctx, cancel: cancel}
}

// SetStreamer sets how a session stream is opened.
func (m *Model) SetStreamer(s Streamer) { m.open = s }

// SetSender gives the model the program's Send so stream goroutines can post messages.
func (m *Model) SetSender(send func(tea.Msg)) { m.send = send }

func (m *Model) Init() tea.Cmd { return tea.Batch(m.loadProjects(), m.tick()) }

func tick() tea.Cmd {
	return tea.Tick(1500*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m *Model) loadProjects() tea.Cmd {
	be, ctx := m.be, m.ctx
	return func() tea.Msg {
		list, err := be.Projects(ctx)
		return projectsMsg{list, err}
	}
}

func (m *Model) poll() tea.Cmd {
	if m.project == "" {
		return nil
	}
	be, ctx, project := m.be, m.ctx, m.project
	return func() tea.Msg {
		snap, err := be.Snapshot(ctx, project)
		return snapshotMsg{project, snap, err}
	}
}

// connect opens the stream of streamNum when it is not open yet.
func (m *Model) connect() tea.Cmd {
	if m.open == nil || m.streamNum == 0 || m.conn != nil || m.connecting {
		return nil
	}
	m.connecting = true
	open, ctx, project, num, gen := m.open, m.ctx, m.project, m.streamNum, m.gen
	cols, rows, term, send := m.term.Cols(), m.term.Rows(), m.term, m.send
	return func() tea.Msg {
		c, err := open(ctx, project, num, cols, rows, term, send)
		return connectMsg{gen, c, err}
	}
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.narrow = ui.IsNarrow(msg.Width)
		m.resizeTerm()
		return m, nil
	case tickMsg:
		if m.flash != "" && time.Since(m.flashAt) > 4*time.Second {
			m.flash = ""
		}
		return m, tea.Batch(m.loadProjects(), m.poll(), m.connect(), m.tick())
	case projectsMsg:
		return m, m.gotProjects(msg)
	case snapshotMsg:
		m.gotSnapshot(msg)
		return m, nil
	case connectMsg:
		if msg.gen != m.gen {
			if msg.conn != nil {
				msg.conn.Close()
			}
			return m, nil
		}
		m.connecting = false
		if msg.err != nil {
			m.setFlash("stream: " + msg.err.Error() + " · retrying")
			return m, nil
		}
		m.conn = msg.conn
		// the window size may have changed while the dial was in flight
		_ = m.conn.Resize(m.ctx, m.term.Cols(), m.term.Rows())
		return m, nil
	case streamOutputMsg:
		return m, nil // the screen changed; View reads the emulator
	case streamClosedMsg:
		if msg.conn != m.conn {
			return m, nil
		}
		m.conn = nil
		if websocket.CloseStatus(msg.err) == websocket.StatusNormalClosure && strings.Contains(msg.err.Error(), "session ended") {
			m.setFlash(fmt.Sprintf("session [%d] ended", m.streamNum))
			m.streamNum, m.claudeFocused, m.focusMode = 0, false, false
			m.resizeTerm()
			return m, nil
		}
		m.setFlash("stream closed · reconnecting")
		return m, nil
	case reposMsg:
		if msg.err != nil {
			m.setFlash("github: " + msg.err.Error())
			return m, nil
		}
		m.picker, m.pickKind = ui.NewPicker("clone from GitHub", msg.repos), pickRepos
		return m, nil
	case createdMsg:
		if msg.err != nil {
			m.setFlash("error: " + msg.err.Error())
			m.pickerIfNoProject()
			return m, nil
		}
		m.setFlash("added project " + msg.res.Name)
		return m, tea.Batch(m.switchProject(msg.res.Name), m.loadProjects())
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	case copyEndMsg:
		if msg.conn == m.conn {
			m.copying = false
		}
		return m, nil
	case clipboardMsg:
		m.setFlash(fmt.Sprintf("copied %d chars", utf8.RuneCountInString(string(msg))))
		return m, tea.SetClipboard(string(msg))
	case tea.MouseWheelMsg:
		m.wheel(msg)
		return m, nil
	case tea.PasteMsg: // ⌘V: the terminal sends the clipboard as a paste, not as keys
		if m.claudeFocused && m.conn != nil && !m.copying {
			m.live()
			m.term.Paste(msg.Content)
		}
		return m, nil
	}
	return m, nil
}

// gotProjects stores the project list. The first reply opens the project to start with:
// the one asked for, the only one, or the picker.
func (m *Model) gotProjects(msg projectsMsg) tea.Cmd {
	if msg.err != nil {
		m.setFlash("error: " + msg.err.Error())
		return nil
	}
	m.projects = msg.list
	if m.loaded {
		return nil
	}
	m.loaded = true
	switch {
	case m.project != "":
		name := m.project
		m.project = ""
		return m.switchProject(name)
	case len(msg.list) == 1:
		return m.switchProject(msg.list[0].Name)
	}
	m.openProjectPicker()
	return nil
}

func (m *Model) gotSnapshot(msg snapshotMsg) {
	if msg.err != nil {
		if msg.project == m.project {
			m.setFlash("error: " + msg.err.Error())
		}
		return
	}
	um := m.models[msg.project]
	if um == nil {
		um = ui.NewModel(msg.snap.Project, ui.Options{AutoExpandWaiting: m.cfg.List.AutoExpandWaiting})
		m.models[msg.project] = um
	} else {
		um.SetProject(msg.snap.Project)
	}
	um.SetLive(msg.snap.Live)
	if msg.project == m.project {
		m.m = um
		if m.pendingSel != 0 {
			um.SelectSession(m.pendingSel)
			m.pendingSel = 0
		}
	}
}

// switchProject shows another project: its list (kept from last time, if any) and an empty
// Claude pane. The stream of the previous project is closed.
func (m *Model) switchProject(name string) tea.Cmd {
	if name == m.project {
		return nil
	}
	m.closeStream()
	m.project, m.m = name, m.models[name]
	m.claudeFocused, m.focusMode, m.prompt = false, false, ui.Prompt{}
	m.resizeTerm()
	return m.poll()
}

// showSession puts a session in the Claude pane. A session already shown keeps its stream.
func (m *Model) showSession(num int) tea.Cmd {
	if num == m.streamNum && (m.conn != nil || m.connecting) {
		return nil
	}
	m.closeStream()
	m.streamNum = num
	return m.connect()
}

// closeStream ends the current stream and starts a fresh screen for the next one.
func (m *Model) closeStream() {
	m.gen++
	if m.conn != nil {
		m.conn.Close()
	}
	m.conn, m.connecting, m.streamNum, m.scrolled, m.copying = nil, false, 0, false, false
	cols, rows := m.term.Cols(), m.term.Rows()
	m.term.Close()
	m.term = NewTerm(cols, rows)
}

// resizeTerm sizes the Claude pane to the space next to the list (or the whole width in focus
// mode and narrow mode), below its title line and rule and above the key bar, and tells the server.
func (m *Model) resizeTerm() {
	cols, rows := m.width-ui.ListWidth-1, m.height-1-paneHeader
	if m.focusMode || m.narrow {
		cols = m.width
	}
	cols, rows = max(cols, 10), max(rows, 3)
	if cols == m.term.Cols() && rows == m.term.Rows() {
		return
	}
	m.term.Resize(cols, rows)
	if m.conn != nil {
		_ = m.conn.Resize(m.ctx, cols, rows)
	}
}

func (m *Model) handleKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := ui.KeyName(k)
	if m.picker != nil {
		return m, m.pickerKey(key, k.Text)
	}
	// before the m == nil check: adding the first project prompts with no project open
	if m.prompt.Active() {
		kind := m.prompt.Kind
		switch m.prompt.Key(key, k.Text) {
		case ui.PromptSubmit:
			return m, m.submit(kind, strings.TrimSpace(m.prompt.Input), m.prompt.Target)
		case ui.PromptCancel:
			m.pickerIfNoProject()
		}
		return m, nil
	}
	if m.m == nil {
		if key == "C-c" {
			return m, m.quit()
		}
		return m, nil
	}
	if key == m.cfg.Client.CopyMode && key != "" { // from both panes
		m.copyMode()
		return m, nil
	}
	if m.claudeFocused {
		if m.cfg.Client.IsToggle(key) {
			m.claudeFocused, m.focusMode = false, false
			m.resizeTerm()
			return m, nil
		}
		if m.conn != nil {
			m.live()
			m.term.SendKey(k)
		}
		return m, nil
	}
	if m.showKeys {
		m.showKeys = false
		return m, nil
	}
	if m.seq != "" {
		key = m.seq + " " + key
		m.seq = ""
	}
	if key == "C-c" {
		return m, m.quit()
	}
	if m.cfg.Client.IsToggle(key) {
		return m, m.backToClaude()
	}
	action := m.cfg.Keys.ListAction(key)
	if action == "" && m.cfg.Keys.IsPrefixOfSequence(key) {
		m.seq = key
		return m, nil
	}
	num, sess := m.m.SelectedSession()
	row := m.m.Selected()
	switch action {
	case "down":
		m.m.Move(1)
	case "up":
		m.m.Move(-1)
	case "open":
		if row.Kind == ui.RowStory {
			m.m.ToggleFold()
		} else if sess != nil {
			return m, m.enterClaude()
		}
	case "fold_all":
		m.m.FoldAll()
	case "focus":
		if sess != nil && !m.m.Gone(num) {
			m.focusMode = true
			return m, m.enterClaude()
		}
	case "yes":
		if sess != nil && strings.HasPrefix(m.m.StateText(num), "asks:") {
			_ = m.do("yes", api.Action{Num: num})
		}
	case "new_chat":
		m.openPrompt(ui.PromptNewChat, "", "chat title", "")
	case "new_session":
		if row.Story == "" {
			m.openPrompt(ui.PromptNewChat, "", "chat title", "")
		} else {
			m.openPrompt(ui.PromptNewSession, row.Story, "session title", "")
		}
	case "new_story":
		m.openPrompt(ui.PromptNewStory, "", "story name (branch feat/<name>)", "")
	case "rename":
		if sess != nil {
			m.openPrompt(ui.PromptRename, "", "new title", sess.Title)
		}
	case "resume":
		if sess != nil && m.m.Gone(num) {
			_ = m.do("resume", api.Action{Num: num})
		}
	case "close_story":
		if row.Story != "" {
			m.openPrompt(ui.PromptCloseStory, row.Story, fmt.Sprintf("close story %s, stop its sessions and remove its worktree? y/n", row.Story), "")
		}
	case "waiting":
		return m, m.jumpWaiting()
	case "projects":
		m.openProjectPicker()
	case "lazygit", "nvim", "shell", "prs":
		m.setFlash(action + " is not available yet")
	case "keymap":
		m.showKeys = true
	case "scroll_up":
		m.scroll(-max(m.term.Rows()/2, 1))
	case "scroll_down":
		m.scroll(max(m.term.Rows()/2, 1))
	}
	return m, nil
}

// copyMode puts the open session in tmux copy-mode and gives the Claude pane the keys, which
// then go to tmux (vi keys, v selects, y copies) until the server reports that it left.
func (m *Model) copyMode() {
	if m.conn == nil {
		return
	}
	if err := m.conn.CopyMode(m.ctx); err != nil {
		m.setFlash("copy-mode: " + err.Error())
		return
	}
	m.copying, m.scrolled = true, false
	if !m.claudeFocused {
		m.claudeFocused = true
		m.resizeTerm()
	}
}

// wheelLines is how far one wheel notch scrolls the Claude pane.
const wheelLines = 3

// wheel scrolls the Claude pane when the pointer is over it.
func (m *Model) wheel(msg tea.MouseWheelMsg) {
	if m.m == nil || m.picker != nil || m.showKeys || msg.Y < paneHeader {
		return
	}
	switch {
	case m.focusMode || (m.narrow && m.claudeFocused):
	case m.narrow || msg.X <= ui.ListWidth:
		return // over the list
	}
	switch msg.Button {
	case tea.MouseWheelUp:
		m.scroll(-wheelLines)
	case tea.MouseWheelDown:
		m.scroll(wheelLines)
	}
}

// scroll moves the open session's view through its history: tmux copy-mode on the server
// draws it into the stream. lines < 0 goes back, > 0 toward live.
func (m *Model) scroll(lines int) {
	if m.conn == nil {
		return
	}
	if lines < 0 && !m.copying {
		m.scrolled = true
	}
	_ = m.conn.Scroll(m.ctx, lines)
}

// live returns the Claude pane to the live screen before a key goes to Claude, so copy-mode
// never eats what is typed. scrolled is never set in copy-mode, where the keys are for tmux.
func (m *Model) live() {
	if m.scrolled {
		m.scrolled = false
		_ = m.conn.Scroll(m.ctx, 0)
	}
}

// enterClaude shows the selected session in the Claude pane and gives it the keys.
func (m *Model) enterClaude() tea.Cmd {
	num, sess := m.m.SelectedSession()
	if sess == nil {
		return nil
	}
	if m.m.Gone(num) {
		m.setFlash("session is gone · r to resume")
		return nil
	}
	m.claudeFocused = true
	m.resizeTerm()
	return m.showSession(num)
}

// backToClaude gives the keys back to the session already in the Claude pane. With no session
// open it does nothing: the toggle never opens the session under the cursor (Enter does).
func (m *Model) backToClaude() tea.Cmd {
	if m.streamNum == 0 {
		return nil
	}
	m.claudeFocused = true
	m.resizeTerm()
	return m.connect() // nil unless the stream dropped
}

// jumpWaiting goes to the next session that needs an answer: in this project first, then in
// the others in name order. It opens the session and gives it the keys.
func (m *Model) jumpWaiting() tea.Cmd {
	if n := m.m.NextWaiting(); n != 0 {
		m.m.SelectSession(n)
		return m.enterClaude()
	}
	list := append([]api.ProjectSummary(nil), m.projects...)
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	for _, p := range list {
		if p.Name == m.project || len(p.WaitingSessions) == 0 {
			continue
		}
		num := p.WaitingSessions[0]
		cmd := m.switchProject(p.Name)
		if m.m != nil {
			m.m.SelectSession(num)
		} else {
			m.pendingSel = num
		}
		m.claudeFocused = true
		m.resizeTerm()
		return tea.Batch(cmd, m.showSession(num))
	}
	m.setFlash("nothing is waiting")
	return nil
}

func (m *Model) openProjectPicker() {
	items := make([]string, 0, len(m.projects)+3)
	for _, p := range m.projects {
		items = append(items, p.Name)
	}
	m.picker, m.pickKind = ui.NewPicker("projects", append(items, addFolder, cloneRepo, newProject)), pickProjects
}

func (m *Model) pickerKey(key, text string) tea.Cmd {
	switch m.picker.Key(key, text) {
	case ui.PickCancel:
		m.picker = nil
		if m.project == "" {
			return m.quit() // nothing to go back to
		}
	case ui.PickChoose:
		choice, kind := m.picker.Choice(), m.pickKind
		m.picker = nil
		if kind == pickRepos {
			m.setFlash("cloning " + choice + "…")
			return m.create(api.NewProject{Kind: "clone", Repo: choice})
		}
		switch choice {
		case addFolder:
			m.openPrompt(ui.PromptAddFolder, "", "folder on the server (absolute path)", "")
		case newProject:
			m.openPrompt(ui.PromptNewProject, "", "project name", "")
		case cloneRepo:
			m.setFlash("loading repositories…")
			be, ctx := m.be, m.ctx
			return func() tea.Msg {
				repos, err := be.Repos(ctx)
				return reposMsg{repos, err}
			}
		default:
			return m.switchProject(choice)
		}
	}
	return nil
}

func (m *Model) create(p api.NewProject) tea.Cmd {
	be, ctx := m.be, m.ctx
	return func() tea.Msg {
		res, err := be.CreateProject(ctx, p)
		return createdMsg{res, err}
	}
}

func (m *Model) submit(kind ui.PromptKind, text, target string) tea.Cmd {
	switch kind {
	case ui.PromptNewStory:
		if text != "" && m.do("stories", api.Action{Name: text}) == nil {
			m.setFlash("Created story " + text + " on branch feat/" + text)
		}
	case ui.PromptNewSession, ui.PromptNewChat:
		res, err := m.be.Do(m.ctx, m.project, "sessions", api.Action{Story: target, Title: text})
		if err != nil {
			m.setFlash("error: " + err.Error())
			return nil
		}
		m.pendingSel = res.Num
		if kind == ui.PromptNewChat {
			m.claudeFocused = true
			m.resizeTerm()
		}
		return tea.Batch(m.showSession(res.Num), m.poll())
	case ui.PromptRename:
		if num, _ := m.m.SelectedSession(); num != 0 && text != "" {
			_ = m.do("rename", api.Action{Num: num, Title: text})
		}
	case ui.PromptCloseStory, ui.PromptCloseStoryForce:
		force := kind == ui.PromptCloseStoryForce
		res, err := m.be.Do(m.ctx, m.project, "stories/close", api.Action{Name: target, Force: force})
		switch {
		case err != nil && res.Dirty > 0 && !force:
			m.openPrompt(ui.PromptCloseStoryForce, target, fmt.Sprintf("%s has %d uncommitted files · discard them? y/n", target, res.Dirty), "")
		case err != nil:
			m.setFlash("error: " + err.Error())
		default:
			m.setFlash("closed story " + target)
			return m.poll()
		}
	case ui.PromptAddFolder:
		if text == "" {
			m.pickerIfNoProject()
			return nil
		}
		m.setFlash("adding " + text + "…")
		return m.create(api.NewProject{Kind: "folder", Path: text})
	case ui.PromptNewProject:
		if text == "" {
			m.pickerIfNoProject()
			return nil
		}
		return m.create(api.NewProject{Kind: "new", Name: text})
	}
	return nil
}

// pickerIfNoProject reopens the project picker when no project is open, so a cancelled or
// failed add does not leave an empty screen.
func (m *Model) pickerIfNoProject() {
	if m.project == "" {
		m.openProjectPicker()
	}
}

func (m *Model) openPrompt(kind ui.PromptKind, target, label, initial string) {
	m.prompt.Open(kind, target, label, initial)
	m.flash = ""
}

// do runs an action and shows its error in the key bar. The next poll shows the result.
func (m *Model) do(action string, a api.Action) error {
	_, err := m.be.Do(m.ctx, m.project, action, a)
	if err != nil {
		m.setFlash("error: " + err.Error())
	}
	return err
}

func (m *Model) setFlash(s string) { m.flash, m.flashAt = s, time.Now() }

func (m *Model) quit() tea.Cmd {
	m.cancel()
	if m.conn != nil {
		m.conn.Close()
	}
	m.term.Close()
	return tea.Quit
}

func (m *Model) View() tea.View {
	v := tea.NewView(m.body())
	v.AltScreen = true
	if m.streamNum != 0 {
		v.MouseMode = tea.MouseModeCellMotion // the wheel scrolls the Claude pane
	}
	if m.claudeFocused && m.picker == nil && m.m != nil && !m.showKeys {
		if x, y := m.term.Cursor(); x >= 0 {
			off := 0
			if !m.focusMode && !m.narrow {
				off = ui.ListWidth + 1
			}
			v.Cursor = tea.NewCursor(off+x, y+paneHeader)
		}
	}
	return v
}

func (m *Model) body() string {
	switch {
	case m.picker != nil:
		return m.picker.Render(m.width, m.height-1) + "\n" + m.flash
	case m.m == nil && m.prompt.Active():
		return "bx · " + m.prompt.Line() + "\n" + m.flash
	case m.m == nil && m.project == "":
		return "bx · connecting…\n" + m.flash
	case m.m == nil:
		return "bx · loading " + m.project + "…\n" + m.flash
	case m.showKeys:
		return "bx keymap · press any key to close\n\n" + keymapText(m.cfg)
	}
	var body string
	switch {
	case m.focusMode || (m.narrow && m.claudeFocused):
		body = m.pane()
	case m.narrow:
		body = ui.Render(m.m, m.width, m.height-1, ui.RenderOptions{Focused: true})
	default:
		// the focused pane is lit: its rule is heavy and blue and the separator's joint points at it;
		// in Claude the list is also faint
		list := ui.Render(m.m, ui.ListWidth, m.height-1, ui.RenderOptions{Focused: !m.claudeFocused})
		join := "┫"
		if m.claudeFocused {
			join = "┣"
			list = restyle(list, "\x1b[2m")
		}
		// both rules join the separator on the rule's row
		sep := strings.TrimRight("┃\n"+join+"\n"+strings.Repeat("┃\n", max(m.height-3, 0)), "\n")
		body = lipgloss.JoinHorizontal(lipgloss.Top, list, lipgloss.NewStyle().Foreground(lipgloss.Color(focusColor)).Render(sep), m.pane())
	}
	return body + "\n" + m.bar()
}

// paneHeader is the rows above the Claude pane: the title line and the rule under it.
const paneHeader = 2

// ruleColor draws the rules of the pane without the focus; focusColor the focused one and the separator.
const ruleColor, focusColor = "#3b4261", "#7aa2f7"

// restyle starts every line of s with the SGR sequence sgr and renews it after each reset inside,
// so it holds across the line's own styling.
func restyle(s, sgr string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = sgr + strings.ReplaceAll(l, "\x1b[m", "\x1b[m"+sgr) + "\x1b[m"
	}
	return strings.Join(lines, "\n")
}

// pane is the Claude pane: a title line naming the open session and a rule, then the session's
// screen, or a hint when no session is open.
func (m *Model) pane() string {
	ch, color := "─", ruleColor
	if m.claudeFocused {
		ch, color = "━", focusColor
	}
	rule := lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(strings.Repeat(ch, m.term.Cols()))
	if m.streamNum != 0 {
		return m.paneTitle() + "\n" + rule + "\n" + m.term.View()
	}
	hint := "\n  No session open.\n  Enter on a session shows it here; c starts a chat."
	return strings.Repeat(" ", m.term.Cols()) + "\n" + rule + "\n" + lipgloss.NewStyle().Width(m.term.Cols()).Height(m.term.Rows()).Foreground(lipgloss.Color("#565f89")).Render(hint)
}

// paneTitle is the line above the Claude pane: " project › group › [2] title ··· ○ idle ". It names
// the session the pane shows, since the list cursor may be elsewhere. It keeps the terminal's
// background, like Claude; the rule under it (see pane) sets it apart.
func (m *Model) paneTitle() string {
	cols, num := m.term.Cols(), m.streamNum
	s := m.m.P.Sessions[num]
	if s == nil {
		return strings.Repeat(" ", cols)
	}
	group := s.Story
	if group == "" {
		group = "chats"
	}
	state, color := "○ idle", "#565f89"
	switch {
	case s.Done:
		state, color = "✓ done", "#9ece6a"
	case m.m.Gone(num):
		state = "gone"
	case m.m.State(num) == claude.Waiting:
		state, color = "◆ asks", "#e0af68"
	case m.m.State(num) == claude.Running:
		state, color = "● running", "#7aa2f7"
	}
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("#565f89"))
	path := lipgloss.NewStyle().Foreground(lipgloss.Color("#a9b1d6"))
	left := " " + path.Render(m.project) + dim.Render(" › ") + path.Render(group) + dim.Render(" › ") +
		lipgloss.NewStyle().Foreground(lipgloss.Color("#c0caf5")).Bold(true).Render(fmt.Sprintf("[%d] %s", num, s.Title))
	right := lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(state) + " "
	if m.copying {
		right = lipgloss.NewStyle().Foreground(lipgloss.Color("#1a1b26")).Background(lipgloss.Color("#e0af68")).Render(" COPY ") + " " + right
	}
	// a narrow pane cuts the left side and keeps the state
	left = ansi.Truncate(left, max(cols-ansi.StringWidth(right)-1, 0), "…")
	return left + strings.Repeat(" ", max(cols-ansi.StringWidth(left)-ansi.StringWidth(right), 0)) + right
}

// bar is the bottom line: prompt, flash, focus line or key bar.
func (m *Model) bar() string {
	var text string
	switch {
	case m.prompt.Active():
		text = m.prompt.Line()
	case m.focusMode:
		run, waiting := 0, []ui.WaitingElsewhere(nil)
		for _, p := range m.projects {
			if p.Name == m.project {
				continue
			}
			run += p.Running
			for _, n := range p.WaitingSessions {
				waiting = append(waiting, ui.WaitingElsewhere{Project: p.Name, Num: n})
			}
		}
		text = ui.FocusLine(m.m, m.cfg.Keys.First(m.cfg.Keys.List, "waiting"), m.cfg.Client.Toggle, run, waiting)
	default:
		text = ui.KeyBar(m.m, m.cfg, ui.BarContext{ClaudeFocused: m.claudeFocused, Session: m.streamNum, Seq: m.seq, Flash: m.flash, Narrow: m.narrow})
	}
	// cut the text to one line: Width would wrap it and push the bar off the screen
	text = ansi.Truncate(" "+text, max(m.width, 1), "…")
	return lipgloss.NewStyle().Foreground(lipgloss.Color("#a9b1d6")).Background(lipgloss.Color("#1a1b26")).Width(m.width).Render(text)
}

func keymapText(cfg *config.Config) string {
	var b strings.Builder
	fmt.Fprintf(&b, "  %-12s %s\n", config.Pretty(cfg.Client.Toggle), "list ⇄ Claude pane")
	fmt.Fprintf(&b, "  %-12s %s\n\n", config.Pretty(cfg.Client.CopyMode), "copy-mode: vi keys, v select, y copy, q quit")
	actions := make([]string, 0, len(cfg.Keys.List))
	for a := range cfg.Keys.List {
		actions = append(actions, a)
	}
	sort.Strings(actions)
	for _, a := range actions {
		fmt.Fprintf(&b, "  %-12s %s\n", strings.Join(cfg.Keys.List[a], " / "), a)
	}
	return b.String()
}
