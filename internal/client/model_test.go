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
	"github.com/dinhhuy258/bx/internal/naming"
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
	origDirty int          // stories/repos without force is refused with this many files
	plan      api.SyncPlan // the reply of every sync plan
	planErr   error
	planReqs  []api.SyncPlanRequest
	applyRes  api.SyncResult
	applyErr  error
	applies   []api.SyncApplyRequest
	running   map[int]bool // delete without force on these sessions is refused as still running
	cleanup   api.Cleanup  // the reply of every cleanup scan
	cleaned   [][]string   // the ids of every cleanup apply
	// new stories
	reads       []string              // "storyinfo", "branches fetch", "prs <q>"
	info        api.StoryInfo         // the reply of storyinfo
	branchList  api.BranchList        // the reply of branches
	prs         map[string]api.PRList // the reply of prs, by query
	exists      string                // POST stories without use_existing is refused: local, origin or both
	storyErr    error                 // POST stories fails
	sessionErr  string                // the new story's session does not start
	storyCtxErr error                 // ctx.Err() as POST stories saw it
}

func (f *fakeBackend) StoryInfo(context.Context, string) (api.StoryInfo, error) {
	f.reads = append(f.reads, "storyinfo")
	return f.info, nil
}

func (f *fakeBackend) Branches(_ context.Context, _ string, fetch bool) (api.BranchList, error) {
	f.reads = append(f.reads, fmt.Sprintf("branches fetch=%v", fetch))
	return f.branchList, nil
}

func (f *fakeBackend) PullRequests(_ context.Context, _, q string) (api.PRList, error) {
	f.reads = append(f.reads, "prs "+q)
	return f.prs[q], nil
}

// NewStory records "<project> stories <name> type=<t> source=<s> branch=<b> pr_repo=<r>
// pr=<n> [repos] use" (the set parts) and adds the story and its session 9 to the snapshot.
func (f *fakeBackend) NewStory(ctx context.Context, project string, a api.Action) (api.Result, error) {
	if f.storyCtxErr = ctx.Err(); f.storyCtxErr != nil {
		return api.Result{}, fmt.Errorf("bx serve unreachable: %w", f.storyCtxErr)
	}
	parts := []string{project, "stories"}
	for _, kv := range [][2]string{{"", a.Name}, {"type=", a.Type}, {"source=", a.Source}, {"branch=", a.Branch}, {"pr_repo=", a.PRRepo}} {
		if kv[1] != "" {
			parts = append(parts, kv[0]+kv[1])
		}
	}
	if a.PR != 0 {
		parts = append(parts, fmt.Sprintf("pr=%d", a.PR))
	}
	if len(a.Repos) > 0 {
		parts = append(parts, "["+strings.Join(a.Repos, ",")+"]")
	}
	if a.UseExisting {
		parts = append(parts, "use")
	}
	f.actions = append(f.actions, strings.Join(parts, " "))
	branch := a.Branch
	switch a.Source {
	case "pr":
		branch = fmt.Sprintf("pr/%d", a.PR)
	case "":
		branch = naming.CleanBranch(a.Name)
		if !strings.Contains(branch, "/") {
			branch = naming.Branch(naming.DefaultType(a.Type), naming.Slug(a.Name))
		}
	}
	switch {
	case f.storyErr != nil:
		return api.Result{Error: f.storyErr.Error()}, f.storyErr
	case f.exists != "" && !a.UseExisting:
		return api.Result{Error: "branch exists", Exists: f.exists, Branch: branch}, &StatusError{Code: 409, Text: "branch exists"}
	}
	story := naming.StoryName(branch)
	snap, ok := f.snaps[project]
	if ok {
		snap.Project.Stories[story] = &state.Story{Branch: branch}
	}
	res := api.Result{Story: story, Message: "Created " + story + " on " + branch}
	if f.sessionErr != "" {
		res.SessionError = f.sessionErr
		return res, nil
	}
	res.Num = 9
	if ok {
		snap.Project.Sessions[9] = &state.Session{Story: story, UUID: "u9", Created: time.Now()}
	}
	return res, nil
}

func (f *fakeBackend) Cleanup(context.Context) (api.Cleanup, error) { return f.cleanup, nil }

func (f *fakeBackend) CleanupApply(_ context.Context, ids []string) (api.CleanupResult, error) {
	f.cleaned = append(f.cleaned, ids)
	return api.CleanupResult{Removed: len(ids)}, nil
}

func (f *fakeBackend) SyncPlan(_ context.Context, r api.SyncPlanRequest) (api.SyncPlan, error) {
	f.planReqs = append(f.planReqs, r)
	return f.plan, f.planErr
}

func (f *fakeBackend) SyncApply(_ context.Context, r api.SyncApplyRequest) (api.SyncResult, error) {
	f.applies = append(f.applies, r)
	return f.applyRes, f.applyErr
}

func (f *fakeBackend) ProjectRepos(context.Context, string) ([]string, error) {
	return []string{"api", "libs/auth", "web"}, nil
}

func (f *fakeBackend) Projects(context.Context) ([]api.ProjectSummary, error) { return f.projects, nil }

func (f *fakeBackend) Snapshot(_ context.Context, p string) (api.Snapshot, error) {
	s, ok := f.snaps[p]
	if !ok {
		return s, errors.New("project not found")
	}
	return s, nil
}

// Do records "<project> <action> [num] [story] [title] [name] [base] [repos] force" (the set
// parts); base also sets the project's override in the project list.
func (f *fakeBackend) Do(_ context.Context, project, action string, a api.Action) (api.Result, error) {
	parts := []string{project, action}
	if a.Num != 0 {
		parts = append(parts, fmt.Sprint(a.Num))
	}
	for _, s := range []string{a.Story, a.Title, a.Name, a.Base} {
		if s != "" {
			parts = append(parts, s)
		}
	}
	if len(a.Repos) > 0 {
		parts = append(parts, "["+strings.Join(a.Repos, ",")+"]")
	}
	if a.Force {
		parts = append(parts, "force")
	}
	f.actions = append(f.actions, strings.Join(parts, " "))
	switch {
	case action == "sessions":
		return api.Result{Num: 9}, nil
	case action == "base":
		for i := range f.projects {
			if f.projects[i].Name == project {
				f.projects[i].BaseOverride = a.Base
			}
		}
	case action == "stories/close" && !a.Force && f.dirty > 0:
		return api.Result{Error: "dirty", Dirty: f.dirty}, errors.New("dirty")
	case action == "delete" && !a.Force && f.running[a.Num]:
		return api.Result{Error: "running", Running: true}, errors.New("running")
	case action == "stories/repos" && !a.Force && f.origDirty > 0:
		return api.Result{Error: "dirty", Dirty: f.origDirty, Detail: fmt.Sprintf("web: %d", f.origDirty)}, errors.New("dirty")
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
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir()) // the settings screen scans it; never the real one
	be := &fakeBackend{snaps: map[string]api.Snapshot{}, info: api.StoryInfo{Base: "origin/main", Detected: "origin/main", Types: naming.Types}}
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
		case "BSpace":
			msg = tea.KeyPressMsg{Code: tea.KeyBackspace}
		case "Tab":
			msg = tea.KeyPressMsg{Code: tea.KeyTab}
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

// markDone makes session 1 of home-network done on the server and polls it.
func (h *harness) markDone() {
	h.be.snaps["home-network"].Project.Sessions[1].Done = true
	h.update(tickMsg(time.Now()))
}

func (h *harness) typeText(s string) {
	for _, r := range s {
		h.press(string(r))
	}
}

func TestStartupOpensTheOnlyProject(t *testing.T) {
	h := start(t, 220, 30, "waiting") // the full key bar is about 210 columns wide
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

func TestClaudePaneForwardsDigitsWhileAsking(t *testing.T) {
	h := start(t, 120, 30, "waiting")
	h.press("Enter") // type in Claude
	if !h.m.claudeFocused {
		t.Fatal("Enter should focus the Claude pane")
	}
	if bar := ansi.Strip(h.m.bar()); strings.Contains(bar, "1-3") || !strings.Contains(bar, "⏎ confirm") {
		t.Fatalf("bar %q", bar)
	}
	h.press("1")
	select {
	case b := <-h.m.term.Input():
		if string(b) != "1" {
			t.Fatalf("forwarded %q", b)
		}
	case <-time.After(time.Second):
		t.Fatal("1 was not forwarded to Claude")
	}
	if len(h.be.actions) != 0 {
		t.Fatalf("actions %q", h.be.actions)
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

func TestClosingAStreamLeavesCopyMode(t *testing.T) {
	h := start(t, 120, 30, "idle")
	h.press("Enter")
	c := h.conns[0]
	h.update(tea.MouseWheelMsg{X: ui.ListWidth + 5, Y: 5, Button: tea.MouseWheelUp})
	h.m.closeStream()
	if !c.closed || fmt.Sprint(c.scrolled) != fmt.Sprint([]int{-wheelLines, 0}) {
		t.Fatalf("closed=%v scroll frames %v, want the pane back at live before closing", c.closed, c.scrolled)
	}

	h = start(t, 120, 30, "idle")
	h.press("Enter")
	c = h.conns[0]
	h.m.closeStream()
	if !c.closed || len(c.scrolled) != 0 {
		t.Fatalf("closed=%v scroll frames %v: a live stream closes without one", c.closed, c.scrolled)
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
	h.press("c") // no title prompt: the chat opens and takes the focus
	if !h.m.claudeFocused || strings.Join(h.opened, "|") != "home-network 9" {
		t.Fatalf("a new chat should open and focus: %q", h.opened)
	}
	h.press("C-h", "y")                      // back to the list; y on the waiting session 1 does nothing
	h.m.cfg.Keys.List["yes"] = []string{"y"} // an old bind in config.toml: an unknown action, ignored
	h.press("y")
	h.press("w", "Enter") // new branch
	h.typeText("guest")
	h.press("Enter", "Enter") // the name, then feat
	if got := strings.Join(h.be.actions, "|"); got != "home-network sessions|home-network stories guest type=feat" {
		t.Fatalf("actions = %q", got)
	}
}

// newBranch opens the name prompt of a new branch story.
func (h *harness) newBranch() {
	h.press("w", "Enter")
}

func TestNewStorySourcePicker(t *testing.T) {
	h := start(t, 120, 30, "idle")
	h.press("w")
	v := ansi.Strip(h.m.View().Content)
	if h.m.picker == nil || h.m.pickKind != pickStorySource || h.m.picker.Choice() != "new branch" {
		t.Fatalf("picker:\n%s", v)
	}
	for _, want := range []string{"new story from:", "▸ new branch", "existing branch", "pull request"} {
		if !strings.Contains(v, want) {
			t.Fatalf("no %q in\n%s", want, v)
		}
	}
	h.press("Esc")
	if h.m.picker != nil || len(h.be.actions)+len(h.be.reads) != 0 {
		t.Fatalf("Esc must do nothing: %q %q", h.be.actions, h.be.reads)
	}
}

func TestNewBranchPreviewAndTypePicker(t *testing.T) {
	h := start(t, 120, 30, "idle")
	h.be.info = api.StoryInfo{Base: "origin/develop", LastType: "fix", Types: naming.Types}
	h.newBranch()
	if got := h.m.prompt.Label; got != "story name (fix/<name> ← origin/develop)" {
		t.Fatalf("label = %q", got)
	}
	h.typeText("Login Timeout")
	h.press("Enter")
	v := ansi.Strip(h.m.View().Content)
	if h.m.pickKind != pickStoryType || h.m.picker.Choice() != "fix" || !strings.Contains(v, "branch type · login-timeout") || !strings.Contains(v, "(none)") {
		t.Fatalf("type picker:\n%s", v)
	}
	h.press("Down", "Enter") // chore, after fix
	if got := strings.Join(h.be.actions, "|"); got != "home-network stories Login Timeout type=chore" {
		t.Fatalf("actions = %q", got)
	}
	if !strings.Contains(h.m.flash, "Created login-timeout on chore/login-timeout") {
		t.Fatalf("flash = %q", h.m.flash)
	}

	h = start(t, 120, 30, "idle")
	h.be.info = api.StoryInfo{Base: "main", LastType: "none", Types: naming.Types}
	h.newBranch()
	if got := h.m.prompt.Label; got != "story name (<name> ← main)" {
		t.Fatalf("label = %q", got)
	}
	h.typeText("x")
	h.press("Enter")
	if h.m.picker.Choice() != "(none)" {
		t.Fatalf("highlighted %q", h.m.picker.Choice())
	}
	h.press("Enter")
	if got := strings.Join(h.be.actions, "|"); got != "home-network stories x type=none" {
		t.Fatalf("actions = %q", got)
	}
}

func TestNewBranchWithSlashSkipsTheTypePicker(t *testing.T) {
	h := start(t, 120, 30, "idle")
	h.newBranch()
	h.typeText("fix/x")
	h.press("Enter")
	if h.m.picker != nil {
		t.Fatalf("no type picker for a full branch:\n%s", h.m.View().Content)
	}
	if got := strings.Join(h.be.actions, "|"); got != "home-network stories fix/x" {
		t.Fatalf("actions = %q", got)
	}
}

func TestNewBranchExistsAsksToUseIt(t *testing.T) {
	for _, tc := range []struct{ exists, where string }{{"origin", "on origin"}, {"local", "locally"}, {"both", "locally and on origin"}} {
		h := start(t, 160, 30, "idle")
		h.be.exists = tc.exists
		h.newBranch()
		h.typeText("fix/login-timeout")
		h.press("Enter")
		if want := "branch fix/login-timeout exists " + tc.where + " · use it? y/n"; h.m.prompt.Kind != ui.PromptUseBranch || h.m.prompt.Label != want {
			t.Fatalf("prompt %q, want %q", h.m.prompt.Label, want)
		}
		h.press("y")
		if got := strings.Join(h.be.actions, "|"); got != "home-network stories fix/login-timeout|home-network stories fix/login-timeout use" {
			t.Fatalf("actions = %q", got)
		}
		if strings.Join(h.opened, "|") != "home-network 9" {
			t.Fatalf("opened = %q", h.opened)
		}
	}
	h := start(t, 160, 30, "idle")
	h.be.exists = "origin"
	h.newBranch()
	h.typeText("fix/x")
	h.press("Enter", "n")
	if got := strings.Join(h.be.actions, "|"); got != "home-network stories fix/x" || len(h.opened) != 0 {
		t.Fatalf("n sends nothing more: actions=%q opened=%q", got, h.opened)
	}
}

func TestNewStoryStreamsItsSessionAndFocusesClaude(t *testing.T) {
	h := start(t, 120, 30, "idle")
	h.newBranch()
	h.typeText("login")
	h.press("Enter", "Enter")
	if !h.m.claudeFocused || h.m.streamNum != 9 || strings.Join(h.opened, "|") != "home-network 9" {
		t.Fatalf("focused=%v num=%d opened=%q", h.m.claudeFocused, h.m.streamNum, h.opened)
	}
	if h.m.flash != "Created login on feat/login" || h.m.busy != "" {
		t.Fatalf("flash=%q busy=%q", h.m.flash, h.m.busy)
	}
	if n, _ := h.m.m.SelectedSession(); n != 9 {
		t.Fatalf("selected %d, want the new session 9", n)
	}
}

func TestNewStorySessionErrorKeepsTheStory(t *testing.T) {
	h := start(t, 120, 30, "idle")
	h.be.sessionErr = "tmux: boom"
	h.newBranch()
	h.typeText("login")
	h.press("Enter", "Enter")
	if h.m.flash != "Created login on feat/login · session: tmux: boom" || h.m.claudeFocused || len(h.opened) != 0 {
		t.Fatalf("flash=%q focused=%v opened=%q", h.m.flash, h.m.claudeFocused, h.opened)
	}
	if r := h.m.m.Selected(); r.Kind != ui.RowStory || r.Story != "login" {
		t.Fatalf("selected %+v, want the story row", r)
	}
}

func TestNewStoryErrorFlashes(t *testing.T) {
	h := start(t, 120, 30, "idle")
	h.be.storyErr = errors.New(`story "login" already exists (branch fix/login)`)
	h.newBranch()
	h.typeText("fix/login")
	h.press("Enter")
	if h.m.flash != `error: story "login" already exists (branch fix/login)` || h.m.busy != "" {
		t.Fatalf("flash=%q busy=%q", h.m.flash, h.m.busy)
	}
}

func TestNewStoryBusyBarAndEscCancels(t *testing.T) {
	h := start(t, 160, 30, "idle")
	h.be.info = api.StoryInfo{Base: "origin/develop", Types: naming.Types}
	h.newBranch()
	h.typeText("fix/x")
	_, cmd := h.m.Update(tea.KeyPressMsg{Code: tea.KeyEnter}) // not drained: the create is in flight
	if cmd == nil || !strings.Contains(ansi.Strip(h.m.View().Content), "fetching origin/develop… · esc to cancel") {
		t.Fatalf("busy bar:\n%s", ansi.Strip(h.m.View().Content))
	}
	h.press("Esc")
	drain(h.m, cmd)
	if h.be.storyCtxErr == nil || h.m.flash != "cancelled" || h.m.busy != "" {
		t.Fatalf("ctxErr=%v flash=%q busy=%q", h.be.storyCtxErr, h.m.flash, h.m.busy)
	}
}

func TestNewStoryFromAnExistingBranch(t *testing.T) {
	h := start(t, 160, 30, "idle")
	now := time.Now()
	h.be.branchList = api.BranchList{Branches: []api.Branch{
		{Name: "fix/login-timeout", Remote: true, Time: now.Add(-49 * time.Hour).Unix()},
		{Name: "main", Local: true, Remote: true, CheckedOut: true, Time: now.Unix()}, // the project folder's
		{Name: "develop", Local: true, Remote: true, Time: now.Add(-5 * 24 * time.Hour).Unix()},
	}}
	h.press("w", "Down")
	_, cmd := h.m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !strings.Contains(ansi.Strip(h.m.View().Content), "fetching origin… · esc to cancel") {
		t.Fatalf("busy bar:\n%s", ansi.Strip(h.m.View().Content))
	}
	drain(h.m, cmd)
	v := ansi.Strip(h.m.View().Content)
	for _, want := range []string{"existing branch", "fix/login-timeout  origin · 2d", "develop  local · 5d"} {
		if !strings.Contains(v, want) {
			t.Fatalf("no %q in\n%s", want, v)
		}
	}
	if strings.Contains(v, "main  local") {
		t.Fatalf("checked-out main offered:\n%s", v)
	}
	h.press("Enter")
	if got := strings.Join(h.be.actions, "|"); got != "home-network stories source=branch branch=fix/login-timeout" {
		t.Fatalf("actions = %q", got)
	}
	if strings.Join(h.be.reads, "|") != "branches fetch=true" || strings.Join(h.opened, "|") != "home-network 9" {
		t.Fatalf("reads=%q opened=%q", h.be.reads, h.opened)
	}
}

func TestExistingBranchesAllCheckedOut(t *testing.T) {
	h := start(t, 160, 30, "idle")
	h.be.branchList = api.BranchList{Branches: []api.Branch{{Name: "main", Local: true, CheckedOut: true, Time: time.Now().Unix()}}}
	h.press("w", "Down", "Enter")
	if h.m.pickKind == pickStoryBranch && h.m.picker != nil || h.m.flash != "no branches in home-network" {
		t.Fatalf("pickKind=%v flash=%q", h.m.pickKind, h.m.flash)
	}
}

func TestWorkspaceExistingBranchShowsItsRepos(t *testing.T) {
	h := start(t, 160, 30, "idle")
	workspace(h)
	h.be.branchList = api.BranchList{Branches: []api.Branch{{Name: "fix/x", Local: true, Time: time.Now().Unix(), Repos: []string{"api", "web"}}}}
	h.press("w", "Down", "Enter")
	if v := ansi.Strip(h.m.View().Content); !strings.Contains(v, "fix/x  local · now · api, web") {
		t.Fatalf("label:\n%s", v)
	}
	h.press("Enter")
	if v := ansi.Strip(h.m.View().Content); !strings.Contains(v, "repositories of story x (branch fix/x)") {
		t.Fatalf("checklist:\n%s", v)
	}
	h.press("Enter")
	if got := strings.Join(h.be.actions, "|"); got != "home-network stories source=branch branch=fix/x [api]" {
		t.Fatalf("actions = %q", got)
	}
}

// prs are two open pull requests of api and web.
func prs() api.PRList {
	return api.PRList{PRs: []api.PR{
		{Number: 123, Title: "Fix login timeout", Author: "alice", Branch: "fix/login-timeout", Repo: "web", Slug: "acme/web"},
		{Number: 7, Title: "Add lease", Author: "bob", Branch: "feat/lease", Repo: "api", Slug: "acme/api"},
	}}
}

func TestNewStoryFromAPullRequest(t *testing.T) {
	h := start(t, 160, 30, "idle")
	l := prs()
	for i := range l.PRs {
		l.PRs[i].Repo, l.PRs[i].Slug = "", "acme/app"
	}
	h.be.prs = map[string]api.PRList{"": l}
	h.press("w", "Down", "Down", "Enter")
	v := ansi.Strip(h.m.View().Content)
	if !strings.Contains(v, "pull requests") || !strings.Contains(v, "#123 Fix login timeout   alice · fix/login-timeout") || strings.Contains(v, "acme/app #") {
		t.Fatalf("picker:\n%s", v)
	}
	h.typeText("lease")
	h.press("Enter")
	if got := strings.Join(h.be.actions, "|"); got != "home-network stories source=pr pr=7" {
		t.Fatalf("actions = %q", got)
	}
}

func TestWorkspacePullRequestTicksItsRepo(t *testing.T) {
	h := start(t, 160, 30, "idle")
	workspace(h)
	h.be.prs = map[string]api.PRList{"": prs()}
	h.press("w", "Down", "Down", "Enter")
	if v := ansi.Strip(h.m.View().Content); !strings.Contains(v, "acme/web #123 Fix login timeout   alice · fix/login-timeout") {
		t.Fatalf("picker:\n%s", v)
	}
	h.press("Enter")
	v := ansi.Strip(h.m.View().Content)
	if !strings.Contains(v, "repositories of story login-timeout (branch fix/login-timeout)") || !strings.Contains(v, "[x] web") || !strings.Contains(v, "[ ] api") {
		t.Fatalf("checklist:\n%s", v)
	}
	h.press(" ", "Enter") // tick api, the highlighted one
	if got := strings.Join(h.be.actions, "|"); got != "home-network stories source=pr pr_repo=web pr=123 [api,web]" {
		t.Fatalf("actions = %q", got)
	}
}

func TestPullRequestSearchAndDirectLookup(t *testing.T) {
	h := start(t, 160, 30, "idle")
	h.be.prs = map[string]api.PRList{
		"":                                   prs(),
		"oauth":                              {PRs: []api.PR{{Number: 99, Title: "OAuth flow", Author: "carol", Branch: "feat/oauth", State: "MERGED"}}},
		"nothin":                             {},
		"https://github.com/acme/web/pull/5": {PRs: []api.PR{{Number: 5, Title: "Old", Author: "dan", Branch: "fix/old"}}, Direct: true},
	}
	h.press("w", "Down", "Down", "Enter")
	h.typeText("oauth")
	h.press("Enter") // nothing matches: search GitHub
	v := ansi.Strip(h.m.View().Content)
	if h.m.picker == nil || !strings.Contains(v, "search: oauth") || !strings.Contains(v, "#99 OAuth flow   carol · feat/oauth") || h.m.picker.Filter() != "" {
		t.Fatalf("search results:\n%s", v)
	}
	h.press("Esc", "w", "Down", "Down", "Enter")
	h.typeText("nothin")
	h.press("Enter")
	if h.m.picker == nil || h.m.flash != "no pull requests match nothin" {
		t.Fatalf("flash=%q", h.m.flash)
	}
	h.press("Esc", "w", "Down", "Down", "Enter")
	h.update(tea.PasteMsg{Content: "https://github.com/acme/web/pull/5"})
	h.press("Enter") // one pull request: created at once
	if got := strings.Join(h.be.actions, "|"); got != "home-network stories source=pr pr=5" || h.m.picker != nil {
		t.Fatalf("actions = %q", got)
	}
	if got := strings.Join(h.be.reads, "|"); got != "prs |prs oauth|prs |prs nothin|prs |prs https://github.com/acme/web/pull/5" {
		t.Fatalf("reads = %q", got)
	}
}

func TestPullRequestNumberIsThatPullRequest(t *testing.T) {
	l := prs()
	l.PRs = append([]api.PR{{Number: 456, Title: "Newer", Author: "eve", Branch: "feat/newer"}}, l.PRs...)
	for i := range l.PRs {
		l.PRs[i].Repo, l.PRs[i].Slug = "", ""
	}
	// #456 contains #45 and is listed first; #123 contains #12: both still looked up, not picked
	for q, pr := range map[string]api.PR{
		"#45": {Number: 45, Title: "Older", Author: "dan", Branch: "fix/older"},
		"#12": {Number: 12, Title: "Typo", Author: "bob", Branch: "fix/typo"},
	} {
		h := start(t, 160, 30, "idle")
		h.be.prs = map[string]api.PRList{"": l, q: {PRs: []api.PR{pr}, Direct: true}}
		h.press("w", "Down", "Down", "Enter")
		h.typeText(q)
		h.press("Enter")
		if got := strings.Join(h.be.actions, "|"); got != fmt.Sprintf("home-network stories source=pr pr=%d", pr.Number) {
			t.Fatalf("%s: actions = %q", q, got)
		}
		if got := strings.Join(h.be.reads, "|"); got != "prs |prs "+q {
			t.Fatalf("%s: reads = %q", q, got)
		}
	}
	for q, want := range map[string]bool{"#45": true, " #45 ": true, "https://github.com/acme/web/pull/5/files": true, "45": false, "#45 x": false, "fix #45": false, "#": false} {
		if directPR(q) != want {
			t.Errorf("directPR(%q) = %v", q, !want)
		}
	}
}

func TestPullRequestGhErrors(t *testing.T) {
	h := start(t, 160, 30, "idle")
	h.be.prs = map[string]api.PRList{"": {Errors: []string{"acme requires SSO authorization"}}}
	h.press("w", "Down", "Down", "Enter")
	if h.m.picker != nil || h.m.flash != "gh: acme requires SSO authorization" {
		t.Fatalf("picker=%v flash=%q", h.m.picker != nil, h.m.flash)
	}
	workspace(h)
	l := prs()
	l.Errors = []string{"acme/api: HTTP 401"}
	h.be.prs = map[string]api.PRList{"": l}
	h.press("w", "Down", "Down", "Enter")
	if v := ansi.Strip(h.m.View().Content); !strings.Contains(v, "pull requests · gh: acme/api: HTTP 401") {
		t.Fatalf("title:\n%s", v)
	}
}

func TestCloseStoryAsksAgainWhenDirty(t *testing.T) {
	h := start(t, 120, 30, "running")
	h.be.dirty = 2
	h.press("k", "D") // from the story row
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

func TestCloseStoryOnlyFromStoryRows(t *testing.T) {
	h := start(t, 120, 30, "running")
	h.press("D") // the cursor starts on session 1
	if h.m.prompt.Active() || strings.Contains(h.m.View().Content, "close story") {
		t.Fatalf("D on a session row must do nothing:\n%s", h.m.View().Content)
	}
	h.press("k", "D")
	if !strings.Contains(h.m.View().Content, "close story s1") {
		t.Fatal(h.m.View().Content)
	}
}

func TestStopAsksThenStops(t *testing.T) {
	h := start(t, 120, 30, "running")
	h.press("s")
	if !strings.Contains(h.m.View().Content, "stop [1] Add lease time? y/n") {
		t.Fatal(h.m.View().Content)
	}
	h.press("y")
	if got := strings.Join(h.be.actions, "|"); got != "home-network stop 1" {
		t.Fatalf("actions = %q", got)
	}
	if !strings.Contains(h.m.View().Content, "stopped [1]") {
		t.Fatal(h.m.View().Content)
	}
}

func TestStopTheOpenSessionClosesItsStream(t *testing.T) {
	h := start(t, 120, 30, "running")
	h.press("Enter", "C-h", "s", "y")
	if h.m.streamNum != 0 || !h.conns[0].closed || h.m.claudeFocused {
		t.Fatalf("num=%d closed=%v focused=%v", h.m.streamNum, h.conns[0].closed, h.m.claudeFocused)
	}
	h.update(streamClosedMsg{conn: h.conns[0], err: websocket.CloseError{Code: websocket.StatusNormalClosure, Reason: "session ended"}})
	if h.m.flash != "stopped [1]" {
		t.Fatalf("flash = %q", h.m.flash)
	}
}

func TestResumeDoneSession(t *testing.T) {
	h := start(t, 120, 30, "idle")
	h.markDone()
	h.press("r")
	if got := strings.Join(h.be.actions, "|"); got != "home-network resume 1" {
		t.Fatalf("actions = %q", got)
	}
}

func TestDoneSessionRefusesEnterAndFocus(t *testing.T) {
	h := start(t, 120, 30, "idle")
	h.markDone()
	for _, k := range []string{"Enter", "F"} {
		h.m.flash = ""
		h.press(k)
		if len(h.opened) != 0 || h.m.claudeFocused || !strings.Contains(h.m.View().Content, "done · r to resume") {
			t.Fatalf("%s: opened=%q focused=%v\n%s", k, h.opened, h.m.claudeFocused, h.m.View().Content)
		}
	}
}

func TestDeleteAsksTwiceWhenRunning(t *testing.T) {
	h := start(t, 160, 30, "running")
	h.be.running = map[int]bool{1: true}
	h.press("X")
	if !strings.Contains(h.m.View().Content, "delete [1] Add lease time? y/n") {
		t.Fatal(h.m.View().Content)
	}
	h.press("y")
	if !strings.Contains(h.m.View().Content, "[1] is still open · kill it and delete? y/n") {
		t.Fatal(h.m.View().Content)
	}
	h.press("y")
	if got := strings.Join(h.be.actions, "|"); got != "home-network delete 1|home-network delete 1 force" {
		t.Fatalf("actions = %q", got)
	}
	if !strings.Contains(h.m.View().Content, "deleted [1] · transcript kept in ~/.claude") {
		t.Fatal(h.m.View().Content)
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
	h := start(t, 240, 30, "waiting", "running") // the key bar with the switch keys is about 230 columns
	h.press("o", "Enter", "[")                   // open lab, then back to home-network
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
	if !strings.Contains(v, "s1 · asks: needs you") || !strings.Contains(v, "s1 · running") || !strings.Contains(v, "A back") || strings.Contains(v, "y yes") {
		t.Fatalf("rows and key bar:\n%s", v)
	}
	h.press("y") // the cursor starts on lab's waiting session: no quick answer
	if len(h.be.actions) != 0 {
		t.Fatalf("actions %q", h.be.actions)
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

func TestSettingsEditsATextField(t *testing.T) {
	h := start(t, 120, 30, "running")
	h.be.values = map[string]string{"notifications.ntfy_url": "https://ntfy.sh/a"}
	h.press("S", "l", "Tab", "Tab") // an unsaved model change, then git, then the notifications section
	if !strings.Contains(h.m.bar(), "⏎ edit") {
		t.Fatalf("bar on a text field: %q", h.m.bar())
	}
	h.press("Enter")
	if h.m.prompt.Kind != ui.PromptSettingText || h.m.prompt.Input != "https://ntfy.sh/a" || h.m.prompt.Target != "notifications.ntfy_url" {
		t.Fatalf("prompt = %+v", h.m.prompt)
	}
	h.press("Esc")
	if h.m.settings == nil || h.m.prompt.Active() || len(h.be.saves) != 0 {
		t.Fatal("Esc cancels the edit and keeps the screen")
	}
	h.press("Enter")
	for range "https://ntfy.sh/a" {
		h.press("BSpace")
	}
	h.typeText("nope")
	h.press("Enter")
	if v := ansi.Strip(h.m.View().Content); len(h.be.saves) != 0 || h.m.settings == nil || !strings.Contains(v, "want an http:// or https:// URL") {
		t.Fatalf("a bad URL is refused: saves=%v\n%s", h.be.saves, v)
	}
	h.press("Enter")
	h.m.prompt.Input = ""
	h.typeText("https://ntfy.sh/b")
	h.press("Enter")
	if len(h.be.saves) != 1 || len(h.be.saves[0]) != 1 || h.be.saves[0]["notifications.ntfy_url"] != "https://ntfy.sh/b" {
		t.Fatalf("saves = %v", h.be.saves)
	}
	if v := ansi.Strip(h.m.View().Content); !strings.Contains(v, "settings saved") || !strings.Contains(v, "https://ntfy.sh/b") {
		t.Fatalf("after the save:\n%s", v)
	}
	if got := h.m.settings.Changes(); len(got) != 1 || got["claude.model"] != "opus" {
		t.Fatalf("the unsaved choice must stay unsaved: %v", got)
	}
	h.press("Enter")
	for range "https://ntfy.sh/b" {
		h.press("BSpace")
	}
	h.press("Enter") // empty: notifications off
	if len(h.be.saves) != 2 || h.be.saves[1]["notifications.ntfy_url"] != "" || len(h.be.saves[1]) != 1 {
		t.Fatalf("saves = %v", h.be.saves)
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

// workspace makes home-network a workspace whose story s1 has the repository api.
func workspace(h *harness) {
	snap := h.be.snaps["home-network"]
	snap.Project.Kind = state.KindWorkspace
	snap.Project.Stories["s1"].Repos = []state.StoryRepo{{Rel: "api", Base: "main"}}
	h.be.snaps["home-network"] = snap
	drain(h.m, h.m.poll())
}

func TestWorkspaceNewStoryPicksRepos(t *testing.T) {
	h := start(t, 160, 30, "running")
	workspace(h)
	if v := ansi.Strip(h.m.View().Content); !strings.Contains(v, "workspace · /p/home-network") || !strings.Contains(v, "api") {
		t.Fatalf("view:\n%s", v)
	}
	h.be.info.LastType = "fix"
	h.newBranch()
	h.typeText("login")
	h.press("Enter", "Enter") // the name, then fix, the last type
	v := ansi.Strip(h.m.View().Content)
	if h.m.picker == nil || !strings.Contains(v, "repositories of story login (branch fix/login)") || !strings.Contains(v, "[ ] libs/auth") {
		t.Fatalf("no checklist:\n%s", v)
	}
	h.press(" ", "Down", "Down", " ", "Enter")
	if got := strings.Join(h.be.actions, "|"); got != "home-network stories login type=fix [api,web]" {
		t.Fatalf("actions = %q", got)
	}
}

func TestAddRepoAsksWhenTheOriginalIsDirty(t *testing.T) {
	h := start(t, 240, 30, "running")
	h.press("k", "+") // on story s1 of a repo project: nothing
	if h.m.picker != nil {
		t.Fatal("add repo outside a workspace")
	}
	workspace(h)
	if bar := h.m.View().Content; !strings.Contains(bar, "+ add repo") {
		t.Fatalf("key bar:\n%s", bar)
	}
	h.press("j")
	if bar := h.m.View().Content; strings.Contains(bar, "+ add repo") {
		t.Fatal("add repo shown on a session row")
	}
	h.press("k")
	h.be.origDirty = 3
	h.press("+")
	if v := h.m.View().Content; strings.Contains(v, "] api") || !strings.Contains(v, "[ ] web") {
		t.Fatalf("checklist must leave out api:\n%s", v)
	}
	h.press("Down", "Enter") // web, the highlighted one
	if v := h.m.View().Content; !strings.Contains(v, "(web: 3)") {
		t.Fatalf("no y/n:\n%s", v)
	}
	h.press("y")
	if got := strings.Join(h.be.actions, "|"); got != "home-network stories/repos s1 [web]|home-network stories/repos s1 [web] force" {
		t.Fatalf("actions = %q", got)
	}
}

func TestKeymapWithNoProjectOpen(t *testing.T) {
	h := startIn(t, t.TempDir(), "", 120, 30, "running")
	if v := h.m.View().Content; !strings.Contains(v, "S settings · ? keys") {
		t.Fatal(v)
	}
	h.press("?")
	if v := h.m.View().Content; !strings.Contains(v, "bx keymap") || !strings.Contains(v, "add_repo") {
		t.Fatal(v)
	}
	h.press("x")
	if v := h.m.View().Content; !strings.Contains(v, "No project open.") {
		t.Fatal(v)
	}
}

func TestCleanupNothingToDo(t *testing.T) {
	h := start(t, 120, 30, "idle")
	h.press("C")
	if !strings.Contains(h.m.View().Content, "nothing to clean up") {
		t.Fatal(h.m.View().Content)
	}
}

func TestCleanupChecklistAndConfirm(t *testing.T) {
	h := start(t, 120, 30, "idle")
	h.be.cleanup = api.Cleanup{Items: []api.CleanupItem{
		{Kind: "orphan", ID: "tmux:bx-home-network-7", Label: "bx-home-network-7 · no record", Detail: "no record"},
		{Kind: "done", ID: "session:home-network:2", Label: "home-network · chats · [2] old · done", Detail: "done"},
	}}
	h.press("C")
	v := h.m.View().Content
	for _, want := range []string{"cleanup · tick what to remove", "[x] bx-home-network-7", "[ ] home-network · chats"} {
		if !strings.Contains(v, want) {
			t.Fatalf("no %q in\n%s", want, v)
		}
	}
	h.press("Enter")
	v = h.m.View().Content
	if !strings.Contains(v, "Remove 1 item?") || !strings.Contains(v, "y remove") {
		t.Fatal(v)
	}
	h.press("Esc")
	if len(h.be.cleaned) != 0 {
		t.Fatalf("cleaned = %v", h.be.cleaned)
	}
	h.press("C", "Enter", "y")
	if got := fmt.Sprint(h.be.cleaned); got != "[[tmux:bx-home-network-7]]" {
		t.Fatalf("cleaned = %s", got)
	}
	if !strings.Contains(h.m.View().Content, "removed 1 item") {
		t.Fatal(h.m.View().Content)
	}
}

// gitSettings opens settings › git over home-network and lab, lab's branches being list.
func gitSettings(t *testing.T, list ...api.Branch) *harness {
	t.Helper()
	h := start(t, 120, 30, "running", "running")
	h.be.info.Detected = "origin/main"
	h.be.branchList = api.BranchList{Branches: list}
	h.press("S", "Tab")
	if h.m.settings == nil || !h.m.settings.GitShown() {
		t.Fatalf("S, Tab should open settings › git:\n%s", ansi.Strip(h.m.View().Content))
	}
	return h
}

func TestSettingsGitAddsAnOverride(t *testing.T) {
	// develop is checked out in the project folder: still a base to pick
	h := gitSettings(t, api.Branch{Name: "develop", Local: true, Remote: true, CheckedOut: true}, api.Branch{Name: "main", Remote: true})
	v := ansi.Strip(h.m.View().Content)
	if !strings.Contains(v, "No overrides. New stories start from each repo's default branch.") || !strings.Contains(v, "j/k move · ⏎ choose · ⌫ remove · esc back") {
		t.Fatalf("empty git section:\n%s", v)
	}
	h.press("Enter") // + add override
	v = ansi.Strip(h.m.View().Content)
	if h.m.picker == nil || h.m.pickKind != pickOverrideProject || !strings.Contains(v, "base override for…") || !strings.Contains(v, "home-network") {
		t.Fatalf("no project picker:\n%s", v)
	}
	h.typeText("lab")
	h.press("Enter")
	if got := strings.Join(h.be.reads, ","); got != "storyinfo,branches fetch=false" {
		t.Fatalf("reads = %s (no fetch for the base picker)", got)
	}
	v = ansi.Strip(h.m.View().Content)
	if h.m.pickKind != pickOverrideBranch || !strings.Contains(v, "base branch for lab · detected: origin/main") || strings.Contains(v, "not in") {
		t.Fatalf("no branch picker:\n%s", v)
	}
	h.press("Enter") // develop
	if got := h.be.actions[len(h.be.actions)-1]; got != "lab base develop" {
		t.Fatalf("actions = %q", h.be.actions)
	}
	v = ansi.Strip(h.m.View().Content)
	if h.m.picker != nil || !strings.Contains(v, "lab: new stories start from origin/develop") || !strings.Contains(v, "lab  develop") {
		t.Fatalf("after the save the row should show:\n%s", v)
	}
	if len(h.be.saves) != 0 {
		t.Fatalf("Enter in settings › git must not save the Claude fields: %v", h.be.saves)
	}
	h.press("j", "Enter") // the add row again: lab is left out now
	if v = ansi.Strip(h.m.picker.Render(120, 10)); h.m.pickKind != pickOverrideProject || strings.Contains(v, "lab") {
		t.Fatalf("the picker must leave out lab:\n%s", v)
	}
}

func TestSettingsGitChangesAndRemovesAnOverride(t *testing.T) {
	h := start(t, 120, 30, "running", "running")
	h.be.projects[1].BaseOverride = "develop"
	h.be.branchList = api.BranchList{Branches: []api.Branch{{Name: "main", Remote: true}, {Name: "develop", Remote: true}, {Name: "release/1.4", Local: true}}}
	drain(h.m, h.m.loadProjects())
	h.press("S", "Tab")
	if v := ansi.Strip(h.m.View().Content); !strings.Contains(v, "› lab  develop") {
		t.Fatalf("the override row:\n%s", v)
	}
	h.press("Enter") // change it
	if h.m.pickKind != pickOverrideBranch || h.m.picker.Choice() != "develop" {
		t.Fatalf("the branch picker should open on the current override, at %q", h.m.picker.Choice())
	}
	h.typeText("release")
	h.press("Enter")
	if got := h.be.actions[len(h.be.actions)-1]; got != "lab base release/1.4" {
		t.Fatalf("actions = %q", h.be.actions)
	}
	if v := ansi.Strip(h.m.View().Content); !strings.Contains(v, "lab: new stories start from release/1.4") || !strings.Contains(v, "lab  release/1.4") {
		t.Fatalf("a local-only branch has no origin/:\n%s", v)
	}
	n := len(h.be.actions)
	h.press("BSpace")
	if v := ansi.Strip(h.m.View().Content); !strings.Contains(v, "remove base override of lab (release/1.4)? y/n") {
		t.Fatalf("Backspace should ask:\n%s", v)
	}
	h.press("n")
	if len(h.be.actions) != n || h.m.settings == nil {
		t.Fatalf("n keeps the override: %q", h.be.actions[n:])
	}
	h.press("BSpace", "y")
	if got := h.be.actions[len(h.be.actions)-1]; got != "lab base" {
		t.Fatalf("actions = %q", h.be.actions)
	}
	v := ansi.Strip(h.m.View().Content)
	if !strings.Contains(v, "lab: new stories start from each repo's default branch") || !strings.Contains(v, "No overrides.") {
		t.Fatalf("after the remove:\n%s", v)
	}
}

func TestSettingsGitWorkspaceBranchesNameTheMissingRepos(t *testing.T) {
	h := gitSettings(t, api.Branch{Name: "develop", Remote: true, Repos: []string{"api"}}, api.Branch{Name: "main", Remote: true, Repos: []string{"api", "web"}})
	h.press("Enter")
	h.typeText("lab")
	h.press("Enter")
	v := ansi.Strip(h.m.View().Content)
	if !strings.Contains(v, "develop  · not in web") || strings.Contains(v, "main  · not in") {
		t.Fatalf("workspace branch picker:\n%s", v)
	}
	h.press("Enter")
	if got := h.be.actions[len(h.be.actions)-1]; got != "lab base develop" {
		t.Fatalf("the label's note must not reach the server: %q", h.be.actions)
	}
}
