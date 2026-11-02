package client

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/coder/websocket"

	"github.com/dinhhuy258/bx/internal/api"
	"github.com/dinhhuy258/bx/internal/claude"
	"github.com/dinhhuy258/bx/internal/claudesync"
	"github.com/dinhhuy258/bx/internal/config"
	"github.com/dinhhuy258/bx/internal/settings"
	"github.com/dinhhuy258/bx/internal/ui"
)

// Backend is what the model needs from the server; *API implements it.
type Backend interface {
	Projects(ctx context.Context) ([]api.ProjectSummary, error)
	Snapshot(ctx context.Context, project string) (api.Snapshot, error)
	Do(ctx context.Context, project, action string, a api.Action) (api.Result, error)
	NewStory(ctx context.Context, project string, a api.Action) (api.Result, error)
	StoryInfo(ctx context.Context, project string) (api.StoryInfo, error)
	Branches(ctx context.Context, project string, fetch bool) (api.BranchList, error)
	PullRequests(ctx context.Context, project, q string) (api.PRList, error)
	CreateProject(ctx context.Context, p api.NewProject) (api.Result, error)
	Repos(ctx context.Context) ([]string, error)
	ProjectRepos(ctx context.Context, project string) ([]string, error)
	Settings(ctx context.Context) (api.Settings, error)
	SaveSettings(ctx context.Context, values map[string]string) (api.Settings, error)
	SyncPlan(ctx context.Context, r api.SyncPlanRequest) (api.SyncPlan, error)
	SyncApply(ctx context.Context, r api.SyncApplyRequest) (api.SyncResult, error)
	Cleanup(ctx context.Context) (api.Cleanup, error)
	CleanupApply(ctx context.Context, ids []string) (api.CleanupResult, error)
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
	// storyReposMsg is the repositories of a workspace, for the checklist of a new story or,
	// with add, of the repositories to add to story.
	storyReposMsg struct {
		project, story string
		add            bool
		repos          []string
		err            error
	}
	createdMsg struct {
		res api.Result
		err error
	}
	settingsMsg struct {
		s     api.Settings
		err   error
		saved bool // the reply of a save, not of the GET on open
	}
	cleanupMsg struct {
		c   api.Cleanup
		err error
	}
	cleanedMsg struct {
		removed int
		err     error
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
	pickStoryRepos      // the repositories of a new workspace story
	pickAddRepos        // repositories to add to a workspace story
	pickCleanup         // what cleanup removes
	pickStorySource     // new story from: new branch, existing branch or pull request
	pickStoryType       // the branch type of a new branch
	pickStoryBranch     // the existing branch of a new story
	pickStoryPR         // the pull request of a new story
	pickOverrideProject // settings › git: the project of a new base override
	pickOverrideBranch  // settings › git: the base branch of a project
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
	tmux func(dir string) tea.Cmd // moves to the tmux pane beside bx; see tmuxSelect

	projects []api.ProjectSummary
	opened   opened               // the projects this client has open, saved between runs
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
	// pendingStory is the story row to select on the next snapshot (a new story whose session
	// did not start)
	pendingStory string

	picker    *ui.Picker
	pickKind  pickKind
	pickStory string   // the story of a repository checklist
	addRepos  []string // the repositories of an add waiting for its y/n

	draft      *storyDraft        // the new story being picked; see story.go
	busy       string             // the key bar while a story is fetched or created
	busyCancel context.CancelFunc // Esc: cancels that request

	cleanItems  []api.CleanupItem // the scan behind the cleanup checklist
	cleanIDs    []string          // the ticked ids; non-nil while the confirm box is open
	cleanLabels []string          // their labels, shown in the box

	settings     *ui.Settings // the settings screen, full screen while open
	settingsNote string       // shown in its place until the server's sections arrive
	syncSeq      int          // bumped on every sync scan or apply; older replies are dropped
	syncMac      claudesync.MacScan
	syncPlan     api.SyncPlan
	syncDone     bool          // a sync succeeded since the screen opened or the last r
	override     overrideDraft // the base override being picked in settings › git; see story.go

	activity bool        // the list pane shows the activity list of every project
	act      ui.Activity // its cursor

	width, height int
	narrow        bool
	claudeFocused bool
	focusMode     bool // Claude pane fills the width
	scrolled      bool // the Claude pane may be back in history; the next key returns to live first
	copying       bool // the Claude pane is in tmux copy-mode: keys go to tmux until it leaves
	prompt        ui.Prompt
	seq           string
	flash         string
	flashAt       time.Time
	showKeys      bool
	ctx           context.Context
	cancel        context.CancelFunc
}

// NewModel builds the client. project is the one to open first ("" = the one shown last time, if
// it is still open).
func NewModel(cfg *config.Config, be Backend, project string) *Model {
	ctx, cancel := context.WithCancel(context.Background())
	return &Model{cfg: cfg, be: be, project: project, opened: loadOpened(cfg.Root), models: map[string]*ui.Model{}, term: NewTerm(80, 24), tick: tick, tmux: tmuxSelect, ctx: ctx, cancel: cancel}
}

// SetStreamer sets how a session stream is opened.
func (m *Model) SetStreamer(s Streamer) { m.open = s }

// SetSender gives the model the program's Send so stream goroutines can post messages.
func (m *Model) SetSender(send func(tea.Msg)) { m.send = send }

func (m *Model) Init() tea.Cmd { return tea.Batch(m.loadProjects(), m.tick()) }

// tmuxSelect moves to the tmux pane on side dir ("L" or "R") of bx, if there is one: past the
// edge of bx, PaneLeft and PaneRight act like vim-tmux-navigator. Outside tmux it does nothing.
func tmuxSelect(dir string) tea.Cmd {
	pane := os.Getenv("TMUX_PANE")
	if os.Getenv("TMUX") == "" || pane == "" {
		return nil
	}
	edge := map[string]string{"L": "#{pane_at_left}", "R": "#{pane_at_right}"}[dir]
	return func() tea.Msg {
		_ = exec.Command("tmux", "if-shell", "-F", "-t", pane, edge, "", "select-pane -t "+pane+" -"+dir).Run()
		return nil
	}
}

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

// pollActivity fetches the snapshot of every other project with a running or waiting session,
// while the activity list is shown.
func (m *Model) pollActivity() tea.Cmd {
	if !m.activity {
		return nil
	}
	var cmds []tea.Cmd
	for _, p := range m.projects {
		if p.Name == m.project || p.Running+p.Waiting == 0 {
			continue
		}
		be, ctx, project := m.be, m.ctx, p.Name
		cmds = append(cmds, func() tea.Msg {
			snap, err := be.Snapshot(ctx, project)
			return snapshotMsg{project, snap, err}
		})
	}
	return tea.Batch(cmds...)
}

// activityModels are the lists the activity list draws from: the open project and every other
// one the server says has a running or waiting session.
func (m *Model) activityModels() []*ui.Model {
	var out []*ui.Model
	if m.m != nil {
		out = append(out, m.m)
	}
	for _, p := range m.projects {
		if um := m.models[p.Name]; um != nil && p.Name != m.project && p.Running+p.Waiting > 0 {
			out = append(out, um)
		}
	}
	return out
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
		return m, tea.Batch(m.loadProjects(), m.poll(), m.pollActivity(), m.connect(), m.tick())
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
	case storyReposMsg:
		m.gotStoryRepos(msg)
		return m, nil
	case storyInfoMsg:
		m.gotStoryInfo(msg)
		return m, nil
	case branchesMsg:
		m.gotBranches(msg)
		return m, nil
	case prsMsg:
		return m, m.gotPRs(msg)
	case storyMsg:
		return m, m.gotStory(msg)
	case overrideBranchesMsg:
		m.gotOverrideBranches(msg)
		return m, nil
	case createdMsg:
		if msg.err != nil {
			m.setFlash("error: " + msg.err.Error())
			return m, nil
		}
		m.setFlash("added project " + msg.res.Name)
		return m, tea.Batch(m.switchProject(msg.res.Name), m.loadProjects())
	case settingsMsg:
		m.gotSettings(msg)
		return m, nil
	case syncPlanMsg:
		m.gotSyncPlan(msg)
		return m, nil
	case syncAppliedMsg:
		return m, m.gotSyncApplied(msg)
	case cleanupMsg:
		m.gotCleanup(msg)
		return m, nil
	case cleanedMsg:
		if msg.err != nil {
			m.setFlash("error: " + msg.err.Error())
		} else {
			m.setFlash("removed " + plural(msg.removed, "item", "items"))
		}
		return m, m.poll()
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
		switch {
		case m.prompt.Active():
			m.prompt.Paste(msg.Content)
		case m.picker != nil:
			m.picker.Paste(msg.Content)
		case m.claudeFocused && m.conn != nil && !m.copying:
			m.live()
			m.term.Paste(msg.Content)
		}
		return m, nil
	}
	return m, nil
}

// gotProjects stores the project list and drops opened projects the server no longer has. The
// first reply opens the project to start with: the one asked for, else the one shown last, else
// the first opened one. With none opened the list stays empty until o opens one.
func (m *Model) gotProjects(msg projectsMsg) tea.Cmd {
	if msg.err != nil {
		m.setFlash("error: " + msg.err.Error())
		return nil
	}
	m.projects = msg.list
	if m.settings != nil {
		m.settings.SetGitOverrides(m.gitOverrides())
	}
	if m.opened.keep(func(name string) bool {
		return slices.ContainsFunc(msg.list, func(p api.ProjectSummary) bool { return p.Name == name })
	}) {
		m.saveOpened()
	}
	if m.loaded {
		return nil
	}
	m.loaded = true
	name := m.project
	m.project = ""
	switch {
	case name != "":
	case slices.Contains(m.opened.Projects, m.opened.Current):
		name = m.opened.Current
	case len(m.opened.Projects) > 0:
		name = m.opened.Projects[0]
	default:
		return nil
	}
	return m.switchProject(name)
}

func (m *Model) saveOpened() {
	m.opened.Current = m.project
	if err := m.opened.save(m.cfg.Root); err != nil {
		m.setFlash("save opened projects: " + err.Error())
	}
}

// closeProject takes the open project off this client's list and shows the next opened one, or
// the empty screen. Its sessions keep running on the server.
func (m *Model) closeProject() tea.Cmd {
	next := m.opened.remove(m.project)
	m.setFlash("closed project " + m.project)
	if next != "" {
		return m.switchProject(next)
	}
	m.closeStream()
	m.project, m.m, m.activity = "", nil, false
	m.claudeFocused, m.focusMode, m.prompt = false, false, ui.Prompt{}
	m.resizeTerm()
	m.saveOpened()
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
	var states map[int]string
	if msg.snap.SessionStates != nil {
		states = make(map[int]string, len(msg.snap.SessionStates))
		for n, st := range msg.snap.SessionStates {
			states[n] = st.State
		}
	}
	um.SetStates(states)
	if msg.project == m.project {
		m.m = um
		if m.pendingSel != 0 {
			um.SelectSession(m.pendingSel)
			m.pendingSel = 0
		}
		if m.pendingStory != "" {
			um.SelectStory(m.pendingStory)
			m.pendingStory = ""
		}
	}
}

// switchProject shows another project and adds it to the opened ones: its list (kept from last
// time, if any) and an empty Claude pane. The stream of the previous project is closed.
func (m *Model) switchProject(name string) tea.Cmd {
	if name == m.project {
		return nil
	}
	m.closeStream()
	m.project, m.m = name, m.models[name]
	m.claudeFocused, m.focusMode, m.prompt = false, false, ui.Prompt{}
	m.resizeTerm()
	m.opened.add(name)
	m.saveOpened()
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
		// copy-mode belongs to the pane; the next client to attach (e.g. the phone) would open in it
		if m.scrolled || m.copying {
			_ = m.conn.Scroll(m.ctx, 0)
		}
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
	if m.cleanIDs != nil {
		return m, m.cleanupKey(key)
	}
	// before the m == nil check: adding the first project prompts with no project open
	if m.prompt.Active() {
		kind := m.prompt.Kind
		switch m.prompt.Key(key, k.Text) {
		case ui.PromptSubmit:
			return m, m.submit(kind, strings.TrimSpace(m.prompt.Input), m.prompt.Target)
		case ui.PromptCancel:
		}
		return m, nil
	}
	if m.settings != nil {
		return m, m.settingsKey(key)
	}
	if m.m == nil {
		switch {
		case key == "C-c":
			return m, m.quit()
		case m.project == "" && m.loaded && m.cfg.Keys.ListAction(key) == "projects":
			m.openProjectPicker()
		case m.showKeys:
			m.showKeys = false
		case m.loaded && m.cfg.Keys.ListAction(key) == "settings":
			return m, m.openSettings()
		case m.loaded && m.cfg.Keys.ListAction(key) == "keymap":
			m.showKeys = true
		}
		return m, nil
	}
	if key == m.cfg.Client.CopyMode && key != "" { // from both panes
		m.copyMode()
		return m, nil
	}
	if m.claudeFocused {
		switch key {
		case m.cfg.Client.PaneLeft:
			m.claudeFocused, m.focusMode = false, false
			m.resizeTerm()
			return m, nil
		case m.cfg.Client.PaneRight:
			return m, m.tmux("R")
		}
		if m.conn != nil {
			m.live()
			m.term.SendKey(k)
		}
		return m, nil
	}
	if m.busy != "" && key == "Esc" {
		m.busyCancel() // its reply flashes "cancelled"
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
	switch key {
	case m.cfg.Client.PaneLeft:
		return m, m.tmux("L")
	case m.cfg.Client.PaneRight:
		if m.streamNum == 0 { // nothing in the Claude pane: on to the tmux pane
			return m, m.tmux("R")
		}
		return m, m.backToClaude()
	}
	action := m.cfg.Keys.ListAction(key)
	if action == "" && m.cfg.Keys.IsPrefixOfSequence(key) {
		m.seq = key
		return m, nil
	}
	switch action {
	case "prev_project":
		return m, m.stepProject(-1)
	case "next_project":
		return m, m.stepProject(1)
	case "close_project":
		return m, m.closeProject()
	}
	if m.activity {
		return m, m.activityKey(action)
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
		if sess != nil && sess.Done {
			m.setFlash("done · r to resume")
		} else if sess != nil && !m.m.Gone(num) {
			m.focusMode = true
			return m, m.enterClaude()
		}
	case "yes":
		if sess != nil && strings.HasPrefix(m.m.StateText(num), "asks:") {
			_ = m.do("yes", api.Action{Num: num})
		}
	case "new_chat":
		return m, m.newSession("")
	case "new_session":
		return m, m.newSession(row.Story)
	case "new_story":
		if m.busy == "" { // one story at a time: Esc cancels the one in progress
			m.openStorySource()
		}
	case "rename":
		if sess != nil {
			m.openPrompt(ui.PromptRename, "", "new title", sess.Title)
		}
	case "resume":
		if sess != nil && (m.m.Gone(num) || sess.Done) {
			_ = m.do("resume", api.Action{Num: num})
		}
	case "stop":
		if sess != nil && !sess.Done {
			if !m.m.Alive(num) {
				return m, m.stop(num)
			}
			m.openPrompt(ui.PromptStop, strconv.Itoa(num), fmt.Sprintf("stop [%d] %s? y/n", num, sess.Title), "")
		}
	case "delete":
		if sess != nil {
			m.openPrompt(ui.PromptDelete, strconv.Itoa(num), fmt.Sprintf("delete [%d] %s? y/n", num, sess.Title), "")
		}
	case "add_repo":
		if row.Kind == ui.RowStory && row.Story != "" && m.m.P.IsWorkspace() {
			return m, m.loadStoryRepos(row.Story, true)
		}
	case "cleanup":
		return m, m.loadCleanup()
	case "close_story":
		if row.Kind == ui.RowStory && row.Story != "" {
			m.openPrompt(ui.PromptCloseStory, row.Story, fmt.Sprintf("close story %s, stop its sessions and remove its worktree? y/n", row.Story), "")
		}
	case "waiting":
		return m, m.jumpWaiting()
	case "projects":
		m.openProjectPicker()
	case "activity":
		m.activity, m.act = true, ui.Activity{}
		return m, m.pollActivity()
	case "settings":
		return m, m.openSettings()
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

// activityKey handles a list key while the activity list is shown. Keys that act on one project's
// list (new chat, rename, ...) do nothing here.
func (m *Model) activityKey(action string) tea.Cmd {
	rows := ui.ActivityRows(m.activityModels())
	r := m.act.Selected(rows)
	switch action {
	case "down":
		m.act.Move(rows, 1)
	case "up":
		m.act.Move(rows, -1)
	case "activity":
		m.activity = false
	case "open":
		switch {
		case r.Project == "":
		case r.Num == 0:
			m.activity = false
			return m.switchProject(r.Project)
		default:
			return m.openSession(r.Project, r.Num)
		}
	case "focus":
		if r.Num != 0 {
			m.focusMode = true
			return m.openSession(r.Project, r.Num)
		}
	case "yes":
		if um := m.models[r.Project]; r.Num != 0 && um != nil && strings.HasPrefix(um.StateText(r.Num), "asks:") {
			_ = m.doIn(r.Project, "yes", api.Action{Num: r.Num})
		}
	case "waiting":
		return m.jumpWaiting()
	case "projects":
		m.openProjectPicker()
	case "settings":
		return m.openSettings()
	case "keymap":
		m.showKeys = true
	case "scroll_up":
		m.scroll(-max(m.term.Rows()/2, 1))
	case "scroll_down":
		m.scroll(max(m.term.Rows()/2, 1))
	}
	return nil
}

// stepProject opens the opened project delta places away in name order, wrapping, and leaves
// the activity list.
func (m *Model) stepProject(delta int) tea.Cmd {
	names := m.opened.Projects
	if len(names) < 2 {
		return nil
	}
	i := sort.SearchStrings(names, m.project) // where the open project is, or would be
	if i < len(names) && names[i] == m.project || delta < 0 {
		i += delta
	}
	m.activity = false
	return m.switchProject(names[(i%len(names)+len(names))%len(names)])
}

// openSession opens a session of any project in the Claude pane and gives it the keys,
// switching project first when needed.
func (m *Model) openSession(project string, num int) tea.Cmd {
	cmd := m.switchProject(project)
	if m.m != nil {
		m.m.SelectSession(num)
	} else {
		m.pendingSel = num
	}
	m.claudeFocused = true
	m.resizeTerm()
	return tea.Batch(cmd, m.showSession(num))
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
	if m.m == nil || m.picker != nil || m.settings != nil || m.showKeys || msg.Y < paneHeader {
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
	if sess.Done {
		m.setFlash("done · r to resume")
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

// backToClaude gives the keys back to the session already in the Claude pane. PaneRight never
// opens the session under the cursor (Enter does).
func (m *Model) backToClaude() tea.Cmd {
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
		return m.openSession(p.Name, p.WaitingSessions[0])
	}
	m.setFlash("nothing is waiting")
	return nil
}

// openProjectPicker lists the projects this client has not opened yet, then the ways to add one.
func (m *Model) openProjectPicker() {
	items := make([]string, 0, len(m.projects)+3)
	for _, p := range m.projects {
		if !slices.Contains(m.opened.Projects, p.Name) {
			items = append(items, p.Name)
		}
	}
	m.picker, m.pickKind = ui.NewPicker("projects", append(items, addFolder, cloneRepo, newProject)), pickProjects
}

func (m *Model) pickerKey(key, text string) tea.Cmd {
	switch m.picker.Key(key, text) {
	case ui.PickCancel:
		m.picker = nil
	case ui.PickNoMatch:
		if m.pickKind == pickStoryPR {
			return m.searchPRs(m.picker.Filter())
		}
	case ui.PickChoose:
		choice, checked, kind := m.picker.Choice(), m.picker.Checked(), m.pickKind
		m.picker = nil
		switch kind {
		case pickRepos:
			m.setFlash("cloning " + choice + "…")
			return m.create(api.NewProject{Kind: "clone", Repo: choice})
		case pickStorySource:
			return m.pickStorySource(choice)
		case pickStoryType:
			return m.pickStoryType(choice)
		case pickStoryBranch:
			return m.pickStoryBranch(choice)
		case pickStoryPR:
			if m.draft == nil {
				return nil
			}
			return m.pickPR(m.draft.prs[choice])
		case pickStoryRepos:
			if m.draft == nil {
				return nil
			}
			m.draft.act.Repos = checked
			return m.createStory()
		case pickAddRepos:
			return m.addStoryRepos(m.pickStory, checked, false)
		case pickOverrideProject:
			return m.chooseOverrideBranch(choice)
		case pickOverrideBranch:
			return m.setOverride(choice)
		case pickCleanup:
			m.pickCleanup(checked)
			return nil
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

// openSettings shows the settings screen, asks the server for its sections and values, and
// scans for the Claude sync list.
func (m *Model) openSettings() tea.Cmd {
	host := ""
	if a, ok := m.be.(*API); ok {
		if u, err := url.Parse(a.Base); err == nil {
			host = "server " + u.Hostname()
		}
	}
	m.settings, m.settingsNote, m.syncDone = ui.NewSettings(host), "loading settings…", false
	m.settings.SetGitOverrides(m.gitOverrides())
	return tea.Batch(m.loadSettings(), m.scanSync())
}

func (m *Model) loadSettings() tea.Cmd {
	be, ctx := m.be, m.ctx
	return func() tea.Msg {
		s, err := be.Settings(ctx)
		return settingsMsg{s: s, err: err}
	}
}

// gotSettings fills the screen with what the server holds. A failure keeps the screen and its
// edits, and shows the error in the bar; Enter tries again.
func (m *Model) gotSettings(msg settingsMsg) {
	if m.settings == nil {
		return
	}
	if msg.err != nil {
		m.setFlash("settings: " + msg.err.Error())
		m.settingsNote = "Could not load the settings · ⏎ to try again"
		return
	}
	m.settings.Load(msg.s.Sections, msg.s.Values)
	if msg.saved {
		m.setFlash("settings saved · new sessions use them")
	}
}

// settingsKey handles a key on the settings screen. Esc with unsaved changes asks first.
func (m *Model) settingsKey(key string) tea.Cmd {
	switch m.settings.Key(key) {
	case ui.SettingsClose:
		if n := len(m.settings.Changes()); n > 0 {
			m.openPrompt(ui.PromptDiscardSettings, "", fmt.Sprintf("discard %s? y/n", plural(n, "change", "changes")), "")
			return nil
		}
		m.settings = nil
	case ui.SettingsSave:
		if !m.settings.Loaded() {
			m.settingsNote = "loading settings…"
			return m.loadSettings()
		}
		changes := m.settings.Changes()
		if len(changes) == 0 {
			m.setFlash("no changes to save")
			return nil
		}
		return m.saveSettings(changes)
	case ui.SettingsEdit:
		if f, ok := m.settings.TextField(); ok {
			m.openPrompt(ui.PromptSettingText, f.Key, f.Label, m.settings.Value(f.Key))
		}
	case ui.SettingsSelect:
		m.saveSyncInclude()
	case ui.SettingsRescan:
		m.syncDone = false
		return m.scanSync()
	case ui.SettingsUpToDate:
		m.setFlash("Up to date")
	case ui.SettingsNothingSelected:
		m.setFlash("Nothing selected")
	case ui.SettingsSyncApply:
		return m.applySync()
	case ui.SettingsGitChoose:
		if project, add := m.settings.GitChoice(); !add {
			return m.chooseOverrideBranch(project)
		}
		m.openOverrideProjects()
	case ui.SettingsGitRemove:
		project, _ := m.settings.GitChoice()
		m.openPrompt(ui.PromptRemoveOverride, project, fmt.Sprintf("remove base override of %s (%s)? y/n", project, m.baseOverride(project)), "")
	}
	return nil
}

// saveSettings sends changes; the reply fills the screen again.
func (m *Model) saveSettings(changes map[string]string) tea.Cmd {
	be, ctx := m.be, m.ctx
	return func() tea.Msg {
		s, err := be.SaveSettings(ctx, changes)
		return settingsMsg{s: s, err: err, saved: true}
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func (m *Model) create(p api.NewProject) tea.Cmd {
	be, ctx := m.be, m.ctx
	return func() tea.Msg {
		res, err := be.CreateProject(ctx, p)
		return createdMsg{res, err}
	}
}

// newSession starts a session in story ("" = a chat) with no title: bx serve names it after
// the first prompt. A chat takes the focus, so the prompt can be typed right away.
func (m *Model) newSession(story string) tea.Cmd {
	res, err := m.be.Do(m.ctx, m.project, "sessions", api.Action{Story: story})
	if err != nil {
		m.setFlash("error: " + err.Error())
		return nil
	}
	m.pendingSel = res.Num
	if story == "" {
		m.claudeFocused = true
		m.resizeTerm()
	}
	return tea.Batch(m.showSession(res.Num), m.poll())
}

func (m *Model) submit(kind ui.PromptKind, text, target string) tea.Cmd {
	switch kind {
	case ui.PromptNewStory:
		return m.storyName(text)
	case ui.PromptUseBranch:
		return m.useBranch()
	case ui.PromptAddRepoForce:
		return m.addStoryRepos(target, m.addRepos, true)
	case ui.PromptRename:
		if num, _ := m.m.SelectedSession(); num != 0 && text != "" {
			_ = m.do("rename", api.Action{Num: num, Title: text})
		}
	case ui.PromptCloseStory, ui.PromptCloseStoryForce:
		force := kind == ui.PromptCloseStoryForce
		res, err := m.be.Do(m.ctx, m.project, "stories/close", api.Action{Name: target, Force: force})
		switch {
		case err != nil && res.Dirty > 0 && !force:
			files := fmt.Sprintf("%d uncommitted files", res.Dirty)
			if res.Detail != "" {
				files += " (" + res.Detail + ")"
			}
			m.openPrompt(ui.PromptCloseStoryForce, target, fmt.Sprintf("%s has %s · discard them? y/n", target, files), "")
		case err != nil:
			m.setFlash("error: " + err.Error())
		default:
			m.setFlash("closed story " + target)
			return m.poll()
		}
	case ui.PromptStop:
		num, _ := strconv.Atoi(target)
		return m.stop(num)
	case ui.PromptDelete, ui.PromptDeleteForce:
		num, _ := strconv.Atoi(target)
		force := kind == ui.PromptDeleteForce
		res, err := m.be.Do(m.ctx, m.project, "delete", api.Action{Num: num, Force: force})
		switch {
		case err != nil && res.Running && !force:
			m.openPrompt(ui.PromptDeleteForce, target, fmt.Sprintf("[%d] is still open · kill it and delete? y/n", num), "")
		case err != nil:
			m.setFlash("error: " + err.Error())
		default:
			m.leaveSession(num)
			m.setFlash(fmt.Sprintf("deleted [%d] · transcript kept in ~/.claude", num))
			return m.poll()
		}
	case ui.PromptDiscardSettings:
		m.settings = nil
	case ui.PromptRemoveOverride:
		return m.removeOverride(target)
	case ui.PromptSettingText:
		// saved on its own right away: other unsaved edits stay pending
		if m.settings == nil {
			return nil
		}
		if err := settings.Validate(target, text); err != nil {
			m.setFlash("settings: " + err.Error())
			return nil
		}
		return m.saveSettings(map[string]string{target: text})
	case ui.PromptAddFolder:
		if text == "" {
			return nil
		}
		m.setFlash("adding " + text + "…")
		return m.create(api.NewProject{Kind: "folder", Path: text})
	case ui.PromptNewProject:
		if text == "" {
			return nil
		}
		return m.create(api.NewProject{Kind: "new", Name: text})
	}
	return nil
}

// stop ends a session's Claude and marks it done, so it is not resumed at startup.
func (m *Model) stop(num int) tea.Cmd {
	if m.do("stop", api.Action{Num: num}) != nil {
		return nil
	}
	m.leaveSession(num)
	m.setFlash(fmt.Sprintf("stopped [%d]", num))
	return m.poll()
}

// leaveSession empties the Claude pane when it shows num, which was just stopped or deleted.
// Closing the stream first makes its later "session ended" close stale, so the flash stays.
func (m *Model) leaveSession(num int) {
	if num != m.streamNum {
		return
	}
	m.closeStream()
	m.claudeFocused, m.focusMode = false, false
	m.resizeTerm()
}

// loadStoryRepos asks for the repositories of the open workspace, for the checklist of a new
// story or, with add, of the repositories to add to story.
func (m *Model) loadStoryRepos(story string, add bool) tea.Cmd {
	be, ctx, project := m.be, m.ctx, m.project
	return func() tea.Msg {
		repos, err := be.ProjectRepos(ctx, project)
		return storyReposMsg{project: project, story: story, add: add, repos: repos, err: err}
	}
}

func (m *Model) gotStoryRepos(msg storyReposMsg) {
	if msg.project != m.project || m.m == nil {
		return
	}
	if msg.err != nil {
		m.setFlash("error: " + msg.err.Error())
		return
	}
	repos, title, tick := msg.repos, "", ""
	switch {
	case msg.add:
		var in []string
		if st := m.m.P.Stories[msg.story]; st != nil {
			in = st.RepoNames()
		}
		repos = slices.DeleteFunc(slices.Clone(repos), func(r string) bool { return slices.Contains(in, r) })
		title = "add repositories to story " + msg.story
	case m.draft == nil:
		return
	default: // a new story: its real branch, and a pull request's repository ticked
		title, tick = fmt.Sprintf("repositories of story %s (branch %s)", m.draft.story, m.draft.branch), m.draft.tick
	}
	if len(repos) == 0 {
		if msg.add {
			m.setFlash("every repository is already in story " + msg.story)
		} else {
			m.setFlash("no repositories in " + m.m.P.Path)
		}
		return
	}
	kind := pickStoryRepos
	if msg.add {
		kind = pickAddRepos
	}
	m.picker, m.pickKind, m.pickStory = ui.NewChecklist(title, repos), kind, msg.story
	if tick != "" {
		m.picker.Tick(tick)
	}
}

// loadCleanup asks the server what cleanup can remove.
func (m *Model) loadCleanup() tea.Cmd {
	be, ctx := m.be, m.ctx
	return func() tea.Msg {
		c, err := be.Cleanup(ctx)
		return cleanupMsg{c, err}
	}
}

// gotCleanup opens the cleanup checklist with the orphans ticked: nothing has a record of
// them. Done sessions start unticked.
func (m *Model) gotCleanup(msg cleanupMsg) {
	if msg.err != nil {
		m.setFlash("error: " + msg.err.Error())
		return
	}
	if len(msg.c.Items) == 0 {
		m.setFlash("nothing to clean up")
		return
	}
	labels := make([]string, len(msg.c.Items))
	for i, it := range msg.c.Items {
		labels[i] = it.Label
	}
	m.picker = ui.NewChecklist("cleanup · tick what to remove", labels)
	for _, it := range msg.c.Items {
		if it.Kind == "orphan" {
			m.picker.Tick(it.Label)
		}
	}
	m.pickKind, m.cleanItems = pickCleanup, msg.c.Items
}

// pickCleanup opens the confirm box over the checked labels of the cleanup checklist.
func (m *Model) pickCleanup(checked []string) {
	items := m.cleanItems
	m.cleanItems = nil
	var ids, labels []string
	for _, it := range items {
		if slices.Contains(checked, it.Label) {
			ids, labels = append(ids, it.ID), append(labels, it.Label)
		}
	}
	if len(ids) == 0 {
		m.setFlash("nothing selected")
		return
	}
	m.cleanIDs, m.cleanLabels = ids, labels
}

// cleanupKey answers the cleanup confirm box: y removes, n or Esc cancels, other keys wait.
func (m *Model) cleanupKey(key string) tea.Cmd {
	switch key {
	case "y":
		be, ctx, ids := m.be, m.ctx, m.cleanIDs
		m.cleanIDs, m.cleanLabels = nil, nil
		return func() tea.Msg {
			res, err := be.CleanupApply(ctx, ids)
			return cleanedMsg{res.Removed, err}
		}
	case "n", "Esc":
		m.cleanIDs, m.cleanLabels = nil, nil
	}
	return nil
}

// addStoryRepos adds repositories to a workspace story. When an original has uncommitted
// files, which the story will not have, it asks first.
func (m *Model) addStoryRepos(story string, repos []string, force bool) tea.Cmd {
	res, err := m.be.Do(m.ctx, m.project, "stories/repos", api.Action{Name: story, Repos: repos, Force: force})
	switch {
	case err != nil && res.Dirty > 0 && !force:
		m.addRepos = repos
		m.openPrompt(ui.PromptAddRepoForce, story, fmt.Sprintf("uncommitted files in the original (%s) will not be in the story · add anyway? y/n", res.Detail), "")
	case err != nil:
		m.setFlash("error: " + err.Error())
	default:
		m.setFlash("added " + strings.Join(repos, ", ") + " to story " + story)
		return m.poll()
	}
	return nil
}

func (m *Model) openPrompt(kind ui.PromptKind, target, label, initial string) {
	m.prompt.Open(kind, target, label, initial)
	m.flash = ""
}

// do runs an action in the open project and shows its error in the key bar. The next poll shows the result.
func (m *Model) do(action string, a api.Action) error { return m.doIn(m.project, action, a) }

func (m *Model) doIn(project, action string, a api.Action) error {
	_, err := m.be.Do(m.ctx, project, action, a)
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
	if m.claudeFocused && m.picker == nil && m.settings == nil && m.m != nil && !m.showKeys {
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
	case m.cleanIDs != nil:
		title := fmt.Sprintf("Remove %s?", plural(len(m.cleanIDs), "item", "items"))
		return ui.ConfirmBox(title, m.cleanLabels, "remove", m.width, m.height-1) + "\n" + m.flash
	case m.settings != nil:
		return m.settings.Render(m.width, m.height-1, false, m.settingsNote) + "\n" + m.bar()
	case m.showKeys:
		return "bx keymap · press any key to close\n\n" + keymapText(m.cfg)
	case m.m == nil && m.prompt.Active():
		return "bx · " + m.prompt.Line() + "\n" + m.flash
	case m.m == nil && m.project == "" && m.loaded:
		return m.empty()
	case m.m == nil && m.project == "":
		return "bx · connecting…\n" + m.flash
	case m.m == nil:
		return "bx · loading " + m.project + "…\n" + m.flash
	}
	var body string
	switch {
	case m.focusMode || (m.narrow && m.claudeFocused):
		body = m.pane()
	case m.narrow:
		body = m.list(m.width, true)
	default:
		// the focused pane is lit: its rule is heavy and blue and the separator's joint points at it;
		// in Claude the list is also faint
		list := m.list(ui.ListWidth, !m.claudeFocused)
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

// list is the list pane: the open project's list with the project strip, or the activity list.
func (m *Model) list(width int, focused bool) string {
	o := ui.RenderOptions{Focused: focused}
	if m.activity {
		back := fmt.Sprintf("%s back to %s · %s projects", config.Pretty(m.cfg.Keys.First(m.cfg.Keys.List, "activity")), m.project, config.Pretty(m.cfg.Keys.First(m.cfg.Keys.List, "projects")))
		return ui.RenderActivity(m.activityModels(), &m.act, width, m.height-1, o, back)
	}
	o.Projects = m.tabs()
	return ui.Render(m.m, width, m.height-1, o)
}

// tabs are the opened projects of the strip, in name order.
func (m *Model) tabs() []ui.ProjectTab {
	tabs := make([]ui.ProjectTab, 0, len(m.opened.Projects))
	for _, name := range m.opened.Projects {
		t := ui.ProjectTab{Name: name}
		for _, p := range m.projects {
			if p.Name == name {
				t.Running, t.Waiting = p.Running, p.Waiting
			}
		}
		tabs = append(tabs, t)
	}
	return tabs
}

// empty is the screen with no project open.
func (m *Model) empty() string {
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("#565f89"))
	brand := lipgloss.NewStyle().Foreground(lipgloss.Color("#bb9af7")).Bold(true)
	key := func(action string) string { return config.Pretty(m.cfg.Keys.First(m.cfg.Keys.List, action)) }
	hint := key("projects") + " open a project · " + key("settings") + " settings · " + key("keymap") + " keys · ^c quit"
	lines := []string{brand.Render("bx"), "", "  No project open.", dim.Render("  " + hint)}
	for len(lines) < m.height-1 {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n") + "\n" + m.flash
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
	case m.m.Starting(num):
		state = "◌ starting"
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
	case m.settings != nil && m.flash != "":
		text = m.flash
	case m.settings != nil && m.settings.GitShown():
		text = ui.SettingsGitBar(m.narrow)
	case m.settings != nil:
		_, onText := m.settings.TextField()
		text = ui.SettingsBar(len(m.settings.Changes()), m.settings.SyncChanges(), m.narrow, onText)
	case m.busy != "" && !m.claudeFocused: // Esc on the list cancels it
		text = m.busy
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
		text = ui.FocusLine(m.m, m.cfg.Keys.First(m.cfg.Keys.List, "waiting"), m.cfg.Client.PaneLeft, run, waiting)
	case m.activity && !m.claudeFocused && m.seq == "":
		r := m.act.Selected(ui.ActivityRows(m.activityModels()))
		waiting := r.Num != 0 && strings.HasPrefix(m.models[r.Project].StateText(r.Num), "asks:")
		text = ui.ActivityBar(m.cfg, r.Num == 0, waiting, ui.BarContext{Flash: m.flash, Narrow: m.narrow})
	default:
		text = ui.KeyBar(m.m, m.cfg, ui.BarContext{ClaudeFocused: m.claudeFocused, Session: m.streamNum, Seq: m.seq, Flash: m.flash, Narrow: m.narrow, Projects: len(m.opened.Projects)})
	}
	// cut the text to one line: Width would wrap it and push the bar off the screen
	text = ansi.Truncate(" "+text, max(m.width, 1), "…")
	return lipgloss.NewStyle().Foreground(lipgloss.Color("#a9b1d6")).Background(lipgloss.Color("#1a1b26")).Width(m.width).Render(text)
}

func keymapText(cfg *config.Config) string {
	var b strings.Builder
	fmt.Fprintf(&b, "  %-12s %s\n", config.Pretty(cfg.Client.PaneLeft), "to the list; past it, the tmux pane on the left")
	fmt.Fprintf(&b, "  %-12s %s\n", config.Pretty(cfg.Client.PaneRight), "to the Claude pane; past it, the tmux pane on the right")
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
