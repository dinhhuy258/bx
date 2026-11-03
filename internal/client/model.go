package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
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
	"github.com/dinhhuy258/bx/internal/uploads"
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
	MCP(ctx context.Context) (api.MCP, error)
	MCPDoc(ctx context.Context, project string) (api.MCPDoc, error)
	SaveMCPDoc(ctx context.Context, project, text string) (api.Result, error)
	SyncPlan(ctx context.Context, r api.SyncPlanRequest) (api.SyncPlan, error)
	SyncApply(ctx context.Context, r api.SyncApplyRequest) (api.SyncResult, error)
	Cleanup(ctx context.Context) (api.Cleanup, error)
	CleanupApply(ctx context.Context, ids []string) (api.CleanupResult, error)
	Artifacts(ctx context.Context, project string, num int) (api.ArtifactList, error)
	Upload(ctx context.Context, project string, num int, name string, content []byte) (api.UploadResult, error)
}

// Conn is one open session stream.
type Conn interface {
	Resize(ctx context.Context, cols, rows int) error
	Scroll(ctx context.Context, lines int) error
	CopyMode(ctx context.Context) error
	Close()
}

// Streamer opens the stream of one session or shell into term; `bx` passes the WebSocket dialer.
type Streamer func(ctx context.Context, project string, t Target, cols, rows int, term *Term, send func(tea.Msg)) (Conn, error)

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
	artifactsMsg struct {
		num  int
		list api.ArtifactList
		err  error
	}
	// attachedMsg is the end of an attach: the clipboard's file uploaded to session num as path.
	attachedMsg struct {
		project    string
		num        int
		name, path string
		err        error
	}
)

type pickKind int

const (
	pickRepos           pickKind = iota
	pickStoryRepos               // the repositories of a new workspace story
	pickAddRepos                 // repositories to add to a workspace story
	pickCleanup                  // what cleanup removes
	pickStorySource              // new story from: new branch, existing branch or pull request
	pickStoryBranch              // the existing branch of a new story
	pickStoryPR                  // the pull request of a new story
	pickOverrideProject          // settings › git: the project of a new base override
	pickOverrideBranch           // settings › git: the base branch of a project
	pickArtifact                 // a page of the selected session, opened in the browser
	pickMCPProject               // settings › mcp: the project of new servers
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
	target     Target // the session or shell shown in the pane; zero = none
	gen        int    // bumped on every stream switch; older connects are dropped
	connecting bool
	pendingSel int // select this session when the project's first snapshot arrives
	// pendingStory is the story row to select on the next snapshot (a new story whose session
	// did not start)
	pendingStory string
	paneClosed   bool    // the pane shows the last output of a shell that ended; no reconnect
	shellLabel   string  // the last known label of the shell in the pane, for its title once it ended
	pendingShell *Target // select this new shell's row on the next snapshot
	// afterShell is a shell that went away: on the next snapshot the cursor leaves its row
	// (ui.Model.SelectAfterShell); with afterShellNote the key bar also says it closed
	afterShell     *Target
	afterShellNote bool

	picker       *ui.Picker
	switcher     *ui.Switcher // the project dialog
	pickKind     pickKind
	artifactURLs map[string]string        // the artifact picker's links, by file name
	browse       func(url string) tea.Cmd // opens a link in the local browser; tests replace it
	pickStory    string                   // the story of a repository checklist
	addRepos     []string                 // the repositories of an add waiting for its y/n

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
	syncDone     bool                                                          // a sync succeeded since the screen opened or the last r
	override     overrideDraft                                                 // the base override being picked in settings › git; see story.go
	edit         func(path string, line int, done func(error) tea.Msg) tea.Cmd // runs the editor on path; tests replace it
	clipboard    func() (clipFile, error)                                      // reads this machine's clipboard for attach; tests replace it
	mcpDrafts    map[string]mcpDraft                                           // settings › mcp: refused saves by scope, see mcp.go

	activity bool        // the list pane shows the inbox: what needs you in every project
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
	return &Model{cfg: cfg, be: be, project: project, opened: loadOpened(cfg.Root), models: map[string]*ui.Model{}, term: NewTerm(80, 24), tick: tick, tmux: tmuxSelect, browse: openBrowser, edit: openEditor, clipboard: readClipboard, mcpDrafts: map[string]mcpDraft{}, ctx: ctx, cancel: cancel}
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

// openBrowser opens u in this machine's browser: `open` on macOS, `xdg-open` elsewhere. A
// failure is quiet, because the key bar shows the link anyway.
func openBrowser(u string) tea.Cmd {
	return func() tea.Msg {
		name := "xdg-open"
		if runtime.GOOS == "darwin" {
			name = "open"
		}
		_ = exec.Command(name, u).Run()
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

// pollActivity fetches the snapshot of every other project with a waiting or unread session,
// while the inbox is shown.
func (m *Model) pollActivity() tea.Cmd {
	if !m.activity {
		return nil
	}
	var cmds []tea.Cmd
	for _, p := range m.projects {
		if p.Name == m.project || p.Waiting+p.Unread == 0 {
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

// activityModels are the lists the inbox draws from: the open project and every other one the
// server says has a waiting or unread session.
func (m *Model) activityModels() []*ui.Model {
	var out []*ui.Model
	if m.m != nil {
		out = append(out, m.m)
	}
	for _, p := range m.projects {
		if um := m.models[p.Name]; um != nil && p.Name != m.project && p.Waiting+p.Unread > 0 {
			out = append(out, um)
		}
	}
	return out
}

// connect opens the stream of the target when it is not open yet.
func (m *Model) connect() tea.Cmd {
	if m.open == nil || m.target.Zero() || m.conn != nil || m.connecting || m.paneClosed {
		return nil
	}
	m.connecting = true
	open, ctx, project, t, gen := m.open, m.ctx, m.project, m.target, m.gen
	cols, rows, term, send := m.term.Cols(), m.term.Rows(), m.term, m.send
	return func() tea.Msg {
		c, err := open(ctx, project, t, cols, rows, term, send)
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
		if msg.err != nil && m.target.Shell() && m.shellGone(msg.err) {
			// the shell exited while the stream was down
			m.shellClosed()
			return m, m.poll()
		}
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
		ended := websocket.CloseStatus(msg.err) == websocket.StatusNormalClosure && strings.Contains(msg.err.Error(), "session ended")
		if ended && m.target.Shell() {
			m.shellClosed()
			return m, m.poll()
		}
		if ended {
			m.setFlash(fmt.Sprintf("session [%d] ended", m.target.Num))
			m.target, m.claudeFocused, m.focusMode = Target{}, false, false
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
	case mcpMsg:
		m.gotMCP(msg)
		return m, nil
	case mcpDocMsg:
		return m, m.gotMCPDoc(msg)
	case mcpEditedMsg:
		return m, m.gotMCPEdited(msg)
	case mcpSavedMsg:
		return m, m.gotMCPSaved(msg)
	case syncPlanMsg:
		m.gotSyncPlan(msg)
		return m, nil
	case syncAppliedMsg:
		return m, m.gotSyncApplied(msg)
	case cleanupMsg:
		m.gotCleanup(msg)
		return m, nil
	case artifactsMsg:
		m.gotArtifacts(msg)
		return m, nil
	case attachedMsg:
		m.gotAttached(msg)
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
		// a terminal pastes only text: with an image on the clipboard the paste is empty, so
		// attach it, as the attach key does
		case msg.Content == "" && m.claudeFocused && m.conn != nil && !m.copying && !m.target.Shell():
			return m, m.attach()
		case m.prompt.Active():
			m.prompt.Paste(msg.Content)
		case m.switcher != nil:
			m.switcher.Paste(msg.Content)
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
		m.settings.SetMCPTotal(len(m.projects))
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
		um = ui.NewModel(msg.snap.Project)
		m.models[msg.project] = um
	} else {
		um.SetProject(msg.snap.Project)
	}
	if t := m.target; t.Shell() && !m.paneClosed && msg.project == m.project {
		i := slices.IndexFunc(msg.snap.Shells, func(sh api.Shell) bool { return sh.Story == t.Story && sh.K == t.K })
		switch {
		case i >= 0:
			m.shellLabel = msg.snap.Shells[i].Label()
		case m.conn == nil && !m.connecting: // it exited while the stream was down
			m.shellClosed()
		}
	}
	// the cursor leaves a shell that went away only when it was on that shell's row
	var after *Target
	if t := m.afterShell; t != nil && msg.project == m.project && um == m.m && um.Selected() == (ui.Row{Kind: ui.RowShell, Story: t.Story, K: t.K}) {
		after = t
	}
	um.SetShells(msg.snap.Shells, msg.snap.Dirs)
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
		if t := m.pendingShell; t != nil {
			um.SelectShell(t.Story, t.K)
			m.pendingShell = nil
		}
		if t := m.afterShell; t != nil {
			if after != nil {
				um.SelectAfterShell(t.Story, t.K)
			}
			if m.afterShellNote {
				note := fmt.Sprintf("shell $%d closed", t.K)
				if t.Story != "" && um.P.Stories[t.Story] == nil {
					note += " · story " + t.Story + " was closed"
				}
				m.setFlash(note)
			}
			m.afterShell, m.afterShellNote = nil, false
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

// show puts a session or shell in the pane. A target already shown keeps its stream; a shell
// that ended is opened again.
func (m *Model) show(t Target) tea.Cmd {
	if t == m.target && !m.paneClosed && (m.conn != nil || m.connecting) {
		return nil
	}
	m.closeStream()
	m.target = t
	if t.Shell() && m.m != nil {
		if sh, ok := m.m.Shell(t.Story, t.K); ok {
			m.shellLabel = sh.Label()
		}
	}
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
	m.conn, m.connecting, m.target, m.scrolled, m.copying, m.paneClosed, m.shellLabel = nil, false, Target{}, false, false, false, ""
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
	// before the prompt: a prompt opened over the switcher (close project) answers first
	if m.switcher != nil && !m.prompt.Active() {
		return m, m.switcherKey(key, k.Text)
	}
	if m.cleanIDs != nil {
		return m, m.cleanupKey(key)
	}
	// before the m == nil check: adding the first project prompts with no project open
	if m.prompt.Active() {
		kind := m.prompt.Kind
		switch m.prompt.Key(key, k.Text) {
		case ui.PromptSubmit:
			if kind == ui.PromptNewStory {
				return m, m.storyName(strings.TrimSpace(m.prompt.Input), m.prompt.BranchType(), m.prompt.StoryBranch())
			}
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
		// C-v is Claude Code's image paste, but Claude reads the server's clipboard: attach
		// brings this machine's instead. A shell gets C-v as it is.
		if key == "C-v" && !m.target.Shell() && m.conn != nil {
			return m, m.attach()
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
		if m.target.Zero() || m.paneClosed { // nothing live in the pane: on to the tmux pane
			return m, m.tmux("R")
		}
		return m, m.backToClaude()
	}
	action := m.listAction(key)
	if action == "" && m.cfg.Keys.IsPrefixOfSequence(key) {
		m.seq = key
		return m, nil
	}
	switch action {
	case "prev_project":
		return m, m.stepProject(-1)
	case "next_project":
		return m, m.stepProject(1)
	case "project_1", "project_2", "project_3", "project_4", "project_5", "project_6", "project_7", "project_8", "project_9":
		return m, m.nthProject(int(action[len(action)-1] - '0'))
	case "close_project":
		m.askCloseProject()
		return m, nil
	}
	if m.activity {
		return m, m.activityKey(action)
	}
	num, sess := m.m.SelectedSession()
	sh, onShell := m.m.SelectedShell()
	row := m.m.Selected()
	switch action {
	case "down":
		m.m.Move(1)
	case "up":
		m.m.Move(-1)
	case "open":
		if row.Kind == ui.RowStory {
			m.m.ToggleFold()
		} else if onShell {
			return m, m.enterShell(sh)
		} else if sess != nil {
			return m, m.enterClaude()
		}
	case "fold_all":
		m.m.FoldAll()
	case "focus":
		if sess != nil && (sess.Done || m.m.Gone(num)) {
			m.askResume(num, sess.Title)
		} else if sess != nil {
			m.focusMode = true
			return m, m.enterClaude()
		}
	case "new_session":
		m.askNewSession(row.Story)
	case "new_story":
		if m.busy == "" { // one story at a time: Esc cancels the one in progress
			m.openStorySource()
		}
	case "rename":
		if onShell {
			m.openPrompt(ui.PromptRenameShell, shellKey(sh.Story, sh.K), "Name (empty shows the command)", sh.Title)
		} else if sess != nil {
			m.openPrompt(ui.PromptRename, "", fmt.Sprintf("Title of [%d]", num), sess.Title)
		}
	case "stop_session":
		if sess != nil && !sess.Done {
			m.ask(ui.PromptStop, strconv.Itoa(num), fmt.Sprintf("Stop [%d]?", num), sess.Title, "", "Claude stops and its window closes.", "⏎ on it resumes it later.")
		}
	case "close_shell":
		if onShell {
			body := []string{sh.Label() + " in " + groupName(sh.Story)}
			if sh.Command != "" && !ui.PlainShell(sh.Command) {
				body = append(body, "", sh.Command+" is still running and will be killed.")
			}
			m.ask(ui.PromptCloseShell, shellKey(sh.Story, sh.K), fmt.Sprintf("Close shell $%d?", sh.K), body...)
		}
	case "delete_session":
		if sess != nil {
			m.ask(ui.PromptDelete, strconv.Itoa(num), fmt.Sprintf("Delete [%d]?", num), sess.Title, "", "Its record is removed from bx.")
		}
	case "add_repo":
		if row.Kind == ui.RowStory && row.Story != "" && m.m.P.IsWorkspace() {
			return m, m.loadStoryRepos(row.Story, true)
		}
	case "cleanup":
		return m, m.loadCleanup()
	case "artifacts":
		if sess != nil {
			return m, m.loadArtifacts(num)
		}
	case "close_story":
		if row.Kind == ui.RowStory && row.Story != "" {
			what := "Its sessions stop and its worktree is removed"
			if n := len(m.m.ShellsOf(row.Story)); n > 0 {
				what = fmt.Sprintf("Its sessions and %s stop and its worktree is removed", plural(n, "shell", "shells"))
			}
			body := []string{what + "."}
			if dir := ui.ShortPath(m.m.Dir(row.Story)); dir != "" {
				body = []string{what + ":", dir}
			}
			m.ask(ui.PromptCloseStory, row.Story, "Close story "+row.Story+"?", body...)
		}
	case "projects":
		m.openProjectPicker()
	case "inbox": // a fresh cursor starts on the first session: waiting first, then unread
		m.activity, m.act = true, ui.Activity{}
		return m, m.pollActivity()
	case "settings":
		return m, m.openSettings()
	case "shell":
		m.askNewShell(row.Story)
	case "keymap":
		m.showKeys = true
	case "scroll_up":
		m.scroll(-max(m.term.Rows()/2, 1))
	case "scroll_down":
		m.scroll(max(m.term.Rows()/2, 1))
	}
	return m, nil
}

// listAction is the list action of key for the selected row. One key can serve actions of
// different rows (d: delete_session on a session, close_shell on a shell); the one for another
// kind of row loses.
func (m *Model) listAction(key string) string {
	actions := m.cfg.Keys.ListActions(key)
	if len(actions) < 2 || m.activity {
		return m.cfg.Keys.ListAction(key)
	}
	_, onShell := m.m.SelectedShell()
	num, _ := m.m.SelectedSession()
	for _, a := range actions {
		switch {
		case a == "close_shell" && !onShell, (a == "delete_session" || a == "stop_session") && num == 0:
			continue
		}
		return a
	}
	return actions[0]
}

// activityKey handles a list key while the inbox is shown. Keys that act on one project's
// list (new session, rename, ...) do nothing here.
func (m *Model) activityKey(action string) tea.Cmd {
	rows := ui.ActivityRows(m.activityModels())
	r := m.act.Selected(rows)
	switch action {
	case "down":
		m.act.Move(rows, 1)
	case "up":
		m.act.Move(rows, -1)
	case "inbox":
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
// the inbox.
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

// nthProject switches to the n-th project of the tabs, 1 the first; past the last it does nothing.
func (m *Model) nthProject(n int) tea.Cmd {
	if n > len(m.opened.Projects) || m.opened.Projects[n-1] == m.project {
		return nil
	}
	m.activity = false
	return m.switchProject(m.opened.Projects[n-1])
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
	return tea.Batch(cmd, m.show(sessionTarget(num)))
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
	if m.m == nil || m.picker != nil || m.switcher != nil || m.settings != nil || m.showKeys || msg.Y < paneHeader {
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
	if sess.Done || m.m.Gone(num) {
		m.askResume(num, sess.Title)
		return nil
	}
	m.claudeFocused = true
	m.resizeTerm()
	return m.show(sessionTarget(num))
}

// askResume asks before starting Claude again in a done or gone session.
func (m *Model) askResume(num int, title string) {
	m.ask(ui.PromptResume, strconv.Itoa(num), fmt.Sprintf("Resume [%d]?", num), title, "", "Claude starts again where it left off.")
}

// resume starts Claude again in a session, then shows it in the pane with the keys.
func (m *Model) resume(num int) tea.Cmd {
	if m.do("resume", api.Action{Num: num}) != nil {
		return nil
	}
	m.claudeFocused = true
	m.resizeTerm()
	return tea.Batch(m.show(sessionTarget(num)), m.poll())
}

// enterShell shows a shell in the pane and gives it the keys.
func (m *Model) enterShell(sh ui.Shell) tea.Cmd {
	m.claudeFocused = true
	m.resizeTerm()
	return m.show(shellTarget(sh.Story, sh.K))
}

// askNewShell asks before opening a new shell in a story ("" = chats), naming its number and folder.
func (m *Model) askNewShell(story string) {
	k := m.m.NextShell(story)
	m.ask(ui.PromptNewShell, shellKey(story, k), fmt.Sprintf("New shell $%d", k), "in "+groupName(story), ui.ShortPath(m.m.Dir(story)))
}

// newShell opens a shell in a story ("" = chats) and shows it focused.
func (m *Model) newShell(story string) tea.Cmd {
	res, err := m.be.Do(m.ctx, m.project, "shells", api.Action{Story: story})
	if err != nil {
		m.setFlash("error: " + err.Error())
		return nil
	}
	t := shellTarget(story, res.K)
	m.pendingShell = &t
	m.claudeFocused = true
	m.resizeTerm()
	return tea.Batch(m.show(t), m.poll())
}

// closeShell kills a shell and empties the pane when it shows that shell. Closing the stream
// first makes its later "session ended" close stale.
func (m *Model) closeShell(story string, k int) tea.Cmd {
	if m.do("shells/close", api.Action{Story: story, K: k}) != nil {
		return nil
	}
	t := shellTarget(story, k)
	if m.target == t {
		m.closeStream()
		m.claudeFocused, m.focusMode = false, false
		m.resizeTerm()
	}
	m.afterShell = &t
	m.setFlash(fmt.Sprintf("closed shell $%d", k))
	return m.poll()
}

// shellClosed marks the shell in the pane as ended: its last output stays, dimmed, until another
// target is shown, and the next snapshot moves the cursor off its row and says it closed.
func (m *Model) shellClosed() {
	t := m.target
	m.paneClosed, m.scrolled, m.copying = true, false, false
	m.claudeFocused, m.focusMode = false, false
	m.resizeTerm()
	m.afterShell, m.afterShellNote = &t, true
	m.setFlash(fmt.Sprintf("shell $%d closed", t.K))
}

// shellGone reports whether the shell in the pane, whose dial failed with err, is gone: the
// server says it is not running (410), or the latest snapshot no longer lists it.
func (m *Model) shellGone(err error) bool {
	var se *StatusError
	if errors.As(err, &se) && se.Code == http.StatusGone {
		return true
	}
	if m.m == nil {
		return false
	}
	_, ok := m.m.Shell(m.target.Story, m.target.K)
	return !ok
}

// shellKey is the prompt target of shell k of story: "story/k" (root is "/k").
func shellKey(story string, k int) string { return story + "/" + strconv.Itoa(k) }

// parseShellKey reads a shellKey back. Story names never contain "/".
func parseShellKey(s string) (story string, k int) {
	i := strings.LastIndex(s, "/")
	if i < 0 {
		return "", 0
	}
	k, _ = strconv.Atoi(s[i+1:])
	return s[:i], k
}

// groupName is how the list names a story: its name, or "chats" for "".
func groupName(story string) string {
	if story == "" {
		return "chats"
	}
	return story
}

// backToClaude gives the keys back to the session already in the Claude pane. PaneRight never
// opens the session under the cursor (Enter does).
func (m *Model) backToClaude() tea.Cmd {
	m.claudeFocused = true
	m.resizeTerm()
	return m.connect() // nil unless the stream dropped
}

// openProjectPicker opens the project dialog: every project with what runs and waits in it,
// those open in this client first, and the ways to add one.
func (m *Model) openProjectPicker() {
	rows := make([]ui.SwitcherRow, 0, len(m.projects))
	for _, p := range m.projects {
		rows = append(rows, ui.SwitcherRow{Name: p.Name, Open: slices.Contains(m.opened.Projects, p.Name), Running: p.Running, Waiting: p.Waiting, Unread: p.Unread})
	}
	m.switcher = ui.NewSwitcher(rows, m.project)
}

// switcherKey handles a key in the project dialog: open a project, close an open one, or add one.
func (m *Model) switcherKey(key, text string) tea.Cmd {
	switch m.switcher.Key(key, text) {
	case ui.SwitchCancel:
		m.switcher = nil
	case ui.SwitchOpen:
		name := m.switcher.Choice()
		m.switcher = nil
		m.activity = false
		return m.switchProject(name)
	case ui.SwitchClose:
		name := m.switcher.Choice()
		m.switcher = nil
		if name != m.project {
			return m.switchThenAskClose(name)
		}
		m.askCloseProject()
	case ui.SwitchAction:
		action := m.switcher.Action()
		m.switcher = nil
		switch action {
		case ui.SwitchAddFolder:
			m.openPrompt(ui.PromptAddFolder, "", "Folder on the server (absolute path)", "")
		case ui.SwitchNewEmpty:
			m.openPrompt(ui.PromptNewProject, "", "Project name", "")
		case ui.SwitchClone:
			m.setFlash("loading repositories…")
			be, ctx := m.be, m.ctx
			return func() tea.Msg {
				repos, err := be.Repos(ctx)
				return reposMsg{repos, err}
			}
		}
	}
	return nil
}

// switchThenAskClose shows the project to close, then asks: closing takes the one on screen.
func (m *Model) switchThenAskClose(name string) tea.Cmd {
	cmd := m.switchProject(name)
	m.askCloseProject()
	return cmd
}

// askCloseProject asks before taking the open project off this client's tabs.
func (m *Model) askCloseProject() {
	m.ask(ui.PromptCloseProject, m.project, "Close project "+m.project+"?", "It leaves this client's tabs.", "Its sessions keep running on the server.")
}

func (m *Model) pickerKey(key, text string) tea.Cmd {
	// #45 is that pull request, not #456 that contains it, unless ↑↓ picked another row
	if key == "Enter" && m.pickKind == pickStoryPR && directPR(m.picker.Filter()) && m.picker.Cursor() == 0 {
		return m.searchPRs(m.picker.Filter())
	}
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
		case pickMCPProject:
			return m.editMCP(choice)
		case pickCleanup:
			m.pickCleanup(checked)
			return nil
		case pickArtifact:
			u := m.artifactURLs[choice]
			if u == "" {
				m.setFlash(`no link: the artifacts file server is off (serve.files_listen = "off")`)
				return nil
			}
			m.setFlash(u)
			return m.browse(u)
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
	m.settings.SetMCPTotal(len(m.projects))
	return tea.Batch(m.loadSettings(), m.scanSync(), m.loadMCP())
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
			m.ask(ui.PromptDiscardSettings, "", fmt.Sprintf("Discard %s?", plural(n, "change", "changes")), "Your unsaved edits to the settings are lost.")
			return nil
		}
		m.settings, m.mcpDrafts = nil, map[string]mcpDraft{}
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
	case ui.SettingsMCPEdit:
		row, _ := m.settings.MCPChoice()
		if row.Add {
			m.openMCPProjects()
			return nil
		}
		return m.editMCP(row.Project)
	case ui.SettingsMCPRemove:
		m.removeMCPKey()
	case ui.SettingsMCPReload:
		return m.loadMCP()
	case ui.SettingsGitChoose:
		if project, add := m.settings.GitChoice(); !add {
			return m.chooseOverrideBranch(project)
		}
		m.openOverrideProjects()
	case ui.SettingsGitRemove:
		project, _ := m.settings.GitChoice()
		m.ask(ui.PromptRemoveOverride, project, "Remove base override?", fmt.Sprintf("New stories in %s stop starting from %s", project, m.baseOverride(project)), "and use the detected base again.")
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

// askNewSession asks before starting a session in story ("" = chats), naming its group and folder.
func (m *Model) askNewSession(story string) {
	m.ask(ui.PromptNewSession, story, "New session", "in "+groupName(story), ui.ShortPath(m.m.Dir(story)))
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
	return tea.Batch(m.show(sessionTarget(res.Num)), m.poll())
}

func (m *Model) submit(kind ui.PromptKind, text, target string) tea.Cmd {
	switch kind {
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
			m.ask(ui.PromptCloseStoryForce, target, "Discard changes and close "+target+"?", target+" has "+files+".", "", "They are lost with its worktree.")
		case err != nil:
			m.setFlash("error: " + err.Error())
		default:
			m.setFlash("closed story " + target)
			return m.poll()
		}
	case ui.PromptStop:
		num, _ := strconv.Atoi(target)
		return m.stop(num)
	case ui.PromptNewShell:
		story, _ := parseShellKey(target)
		return m.newShell(story)
	case ui.PromptNewSession:
		return m.newSession(target)
	case ui.PromptResume:
		num, _ := strconv.Atoi(target)
		return m.resume(num)
	case ui.PromptCloseProject:
		if target != m.project { // the project changed under the prompt
			return nil
		}
		return m.closeProject()
	case ui.PromptCloseShell:
		return m.closeShell(parseShellKey(target))
	case ui.PromptRenameShell:
		story, k := parseShellKey(target)
		if m.do("shells/rename", api.Action{Story: story, K: k, Title: text}) != nil { // empty clears it
			return nil
		}
		return m.poll()
	case ui.PromptDelete, ui.PromptDeleteForce:
		num, _ := strconv.Atoi(target)
		force := kind == ui.PromptDeleteForce
		res, err := m.be.Do(m.ctx, m.project, "delete", api.Action{Num: num, Force: force})
		switch {
		case err != nil && res.Running && !force:
			m.ask(ui.PromptDeleteForce, target, fmt.Sprintf("[%d] is still open", num), "Kill it and delete its record?")
		case err != nil:
			m.setFlash("error: " + err.Error())
		default:
			m.leaveSession(num)
			m.setFlash(fmt.Sprintf("deleted [%d] · transcript kept in ~/.claude", num))
			return m.poll()
		}
	case ui.PromptDiscardSettings:
		m.settings, m.mcpDrafts = nil, map[string]mcpDraft{}
	case ui.PromptRemoveMCP:
		return m.saveMCP(target, "", "")
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
	if m.target != sessionTarget(num) {
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

// loadArtifacts asks the server for session num's pages.
func (m *Model) loadArtifacts(num int) tea.Cmd {
	be, ctx, project := m.be, m.ctx, m.project
	return func() tea.Msg {
		list, err := be.Artifacts(ctx, project, num)
		return artifactsMsg{num, list, err}
	}
}

// attach uploads the image or file on this machine's clipboard to the session in the pane, as
// the phone's attach button does: Claude Code runs on the server and only sees its clipboard.
func (m *Model) attach() tea.Cmd {
	be, ctx, project, num, clip := m.be, m.ctx, m.project, m.target.Num, m.clipboard
	m.setFlash("attaching the clipboard…")
	return func() tea.Msg {
		f, err := clip()
		if err != nil {
			return attachedMsg{project: project, num: num, err: err}
		}
		if len(f.data) > uploads.MaxFile {
			return attachedMsg{project: project, num: num, err: fmt.Errorf("%s is over 20 MB", f.name)}
		}
		res, err := be.Upload(ctx, project, num, f.name, f.data)
		return attachedMsg{project: project, num: num, name: f.name, path: res.Path, err: err}
	}
}

// gotAttached types the uploaded file's server path into Claude, which reads the image from
// there, if the pane still shows that session; otherwise it only says where the file went.
func (m *Model) gotAttached(msg attachedMsg) {
	switch {
	case errors.Is(msg.err, errNoClip):
		m.setFlash("nothing to attach · copy an image, or a file with ⌘C in Finder")
		return
	case msg.err != nil:
		m.setFlash("attach: " + msg.err.Error())
		return
	}
	if m.project != msg.project || m.target != sessionTarget(msg.num) || m.conn == nil || m.copying {
		m.setFlash("uploaded " + msg.path)
		return
	}
	m.live()
	m.term.Paste(msg.path + " ")
	m.setFlash("attached " + msg.name)
}

// gotArtifacts opens the picker of a session's pages, newest first.
func (m *Model) gotArtifacts(msg artifactsMsg) {
	if msg.err != nil {
		m.setFlash("error: " + msg.err.Error())
		return
	}
	if len(msg.list.Files) == 0 {
		m.setFlash("no artifacts yet · ask Claude for an HTML page")
		return
	}
	names := make([]string, 0, len(msg.list.Files))
	m.artifactURLs = map[string]string{}
	for _, f := range msg.list.Files {
		names = append(names, f.Name)
		m.artifactURLs[f.Name] = f.URL
	}
	m.picker, m.pickKind = ui.NewPicker(fmt.Sprintf("artifacts · [%d]", msg.num), names), pickArtifact
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

// cleanupKey answers the cleanup dialog: y removes, n, Esc or Enter cancels, other keys wait.
func (m *Model) cleanupKey(key string) tea.Cmd {
	switch key {
	case "y":
		be, ctx, ids := m.be, m.ctx, m.cleanIDs
		m.cleanIDs, m.cleanLabels = nil, nil
		return func() tea.Msg {
			res, err := be.CleanupApply(ctx, ids)
			return cleanedMsg{res.Removed, err}
		}
	case "n", "Esc", "Enter":
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
		m.ask(ui.PromptAddRepoForce, story, "Add anyway?", fmt.Sprintf("The originals have uncommitted files (%s).", res.Detail), "The story will not have them.")
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

// ask opens a y/n question as a dialog: its title and the lines that say what will happen.
func (m *Model) ask(kind ui.PromptKind, target, title string, body ...string) {
	m.prompt.Ask(kind, target, title, body...)
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
	if !m.target.Zero() {
		v.MouseMode = tea.MouseModeCellMotion // the wheel scrolls the Claude pane
	}
	if m.claudeFocused && m.picker == nil && m.settings == nil && m.m != nil && !m.showKeys && !m.prompt.Active() && m.cleanIDs == nil && m.switcher == nil {
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

// body is the screen, with the open question or form drawn over it.
func (m *Model) body() string {
	var d ui.Dialog
	switch {
	case m.cleanIDs != nil:
		d = ui.Dialog{
			Title: fmt.Sprintf("Remove %s?", plural(len(m.cleanIDs), "item", "items")), Tone: ui.ToneDanger, Body: m.cleanLabels,
			Buttons: []ui.Button{{Key: "n", Label: "Cancel", Default: true}, {Key: "y", Label: "Remove", Danger: true}},
		}
	case m.prompt.Active():
		d = m.prompt.Dialog()
	case m.switcher != nil:
		d = m.switcher.Dialog(m.height-1, false)
	default:
		return m.screen()
	}
	return ui.Overlay(m.screen(), d.Render(m.width, m.height-1, false), m.width, m.height-1)
}

func (m *Model) screen() string {
	switch {
	case m.picker != nil:
		return m.picker.Render(m.width, m.height-1) + "\n" + m.flash
	case m.settings != nil:
		return m.settings.Render(m.width, m.height-1, false, m.settingsNote) + "\n" + m.bar()
	case m.showKeys:
		return "bx keymap · press any key to close\n\n" + ui.Keymap(m.cfg, m.width)
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
			list = ui.Fade(list)
		}
		// both rules join the separator on the rule's row
		sep := strings.TrimRight("┃\n"+join+"\n"+strings.Repeat("┃\n", max(m.height-3, 0)), "\n")
		body = lipgloss.JoinHorizontal(lipgloss.Top, list, lipgloss.NewStyle().Foreground(lipgloss.Color(focusColor)).Render(sep), m.pane())
	}
	return body + "\n" + m.bar()
}

// list is the list pane: the open project's list with the project strip, or the inbox.
func (m *Model) list(width int, focused bool) string {
	o := ui.RenderOptions{Focused: focused}
	if m.activity {
		back := fmt.Sprintf("%s back to %s · %s projects", config.Pretty(m.cfg.Keys.First(m.cfg.Keys.List, "inbox")), m.project, config.Pretty(m.cfg.Keys.First(m.cfg.Keys.List, "projects")))
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

// pane is the Claude pane: a title line naming the open session and a rule, then the session's
// screen, or a hint when no session is open.
func (m *Model) pane() string {
	ch, color := "─", ruleColor
	if m.claudeFocused {
		ch, color = "━", focusColor
	}
	rule := lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(strings.Repeat(ch, m.term.Cols()))
	if !m.target.Zero() {
		screen := m.term.View()
		if m.paneClosed { // a shell that ended: its last output, faint
			screen = ui.Fade(screen)
		}
		return m.paneTitle() + "\n" + rule + "\n" + screen
	}
	hint := "\n  No session open.\n  Enter on a session shows it here."
	if key := m.cfg.Keys.First(m.cfg.Keys.List, "new_session"); key != "" {
		hint += " " + config.Pretty(key) + " starts one."
	}
	return strings.Repeat(" ", m.term.Cols()) + "\n" + rule + "\n" + lipgloss.NewStyle().Width(m.term.Cols()).Height(m.term.Rows()).Foreground(lipgloss.Color("#565f89")).Render(hint)
}

// paneTitle is the line above the Claude pane: " project › group › [2] title ··· ○ idle ". It names
// the session the pane shows, since the list cursor may be elsewhere. It keeps the terminal's
// background, like Claude; the rule under it (see pane) sets it apart.
func (m *Model) paneTitle() string {
	if m.target.Shell() {
		return m.shellTitle()
	}
	cols, num := m.term.Cols(), m.target.Num
	s := m.m.P.Sessions[num]
	if s == nil {
		return strings.Repeat(" ", cols)
	}
	group := groupName(s.Story)
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
	return m.titleLine(left, right)
}

// shellTitle is the line above a shell in the pane: " project › group › $1 zsh ··· SHELL ",
// or CLOSED once the shell ended.
func (m *Model) shellTitle() string {
	story, k := m.target.Story, m.target.K
	label := "shell"
	if sh, ok := m.m.Shell(story, k); ok {
		label = sh.Label()
	} else if m.shellLabel != "" { // it ended and left the list
		label = m.shellLabel
	}
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("#565f89"))
	path := lipgloss.NewStyle().Foreground(lipgloss.Color("#a9b1d6"))
	left := " " + path.Render(m.project) + dim.Render(" › ") + path.Render(groupName(story)) + dim.Render(" › ") +
		lipgloss.NewStyle().Foreground(lipgloss.Color("#c0caf5")).Bold(true).Render(fmt.Sprintf("$%d %s", k, label))
	tag, bg := " SHELL ", "#7dcfff"
	if m.paneClosed {
		tag, bg = " CLOSED ", "#f7768e"
	}
	right := lipgloss.NewStyle().Foreground(lipgloss.Color("#1a1b26")).Background(lipgloss.Color(bg)).Render(tag) + " "
	return m.titleLine(left, right)
}

// titleLine lays out the pane's title: left, then right at the end of the line, with the COPY
// tag in front of right while in copy-mode. A narrow pane cuts the left side and keeps the right.
func (m *Model) titleLine(left, right string) string {
	cols := m.term.Cols()
	if m.copying {
		right = lipgloss.NewStyle().Foreground(lipgloss.Color("#1a1b26")).Background(lipgloss.Color("#e0af68")).Render(" COPY ") + " " + right
	}
	left = ansi.Truncate(left, max(cols-ansi.StringWidth(right)-1, 0), "…")
	return left + strings.Repeat(" ", max(cols-ansi.StringWidth(left)-ansi.StringWidth(right), 0)) + right
}

// bar is the bottom line: prompt, flash, focus line or key bar.
func (m *Model) bar() string {
	var text string
	switch {
	case m.prompt.Active():
		text = m.prompt.Line()
	case m.switcher != nil:
		text = m.switcher.Line()
	case m.settings != nil && m.flash != "":
		text = m.flash
	case m.settings != nil && m.settingsOnMCPRow():
		text = ui.SettingsMCPBar(m.narrow)
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
		text = ui.FocusLine(m.m, m.cfg.Keys.First(m.cfg.Keys.List, "inbox"), m.cfg.Client.PaneLeft, run, waiting)
	case m.activity && !m.claudeFocused && m.seq == "":
		r := m.act.Selected(ui.ActivityRows(m.activityModels()))
		text = ui.ActivityBar(m.cfg, r.Num == 0, ui.BarContext{Flash: m.flash, Narrow: m.narrow})
	default:
		text = ui.KeyBar(m.m, m.cfg, ui.BarContext{ClaudeFocused: m.claudeFocused, Session: m.target.Num, Seq: m.seq, Flash: m.flash, Narrow: m.narrow, Projects: len(m.opened.Projects), Shell: m.target.Shell() && !m.paneClosed})
	}
	// cut the text to one line: Width would wrap it and push the bar off the screen
	text = ansi.Truncate(" "+text, max(m.width, 1), "…")
	return lipgloss.NewStyle().Foreground(lipgloss.Color("#a9b1d6")).Background(lipgloss.Color("#1a1b26")).Width(m.width).Render(text)
}
