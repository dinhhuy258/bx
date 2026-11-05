package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/coder/websocket"

	"github.com/dinhhuy258/bx/internal/accounts"
	"github.com/dinhhuy258/bx/internal/api"
	"github.com/dinhhuy258/bx/internal/claude"
	"github.com/dinhhuy258/bx/internal/config"
	"github.com/dinhhuy258/bx/internal/naming"
	"github.com/dinhhuy258/bx/internal/settings"
	"github.com/dinhhuy258/bx/internal/state"
	"github.com/dinhhuy258/bx/internal/ui"
	"github.com/dinhhuy258/bx/internal/uploads"
)

// fakeBackend serves fixed projects and snapshots and records every action.
type fakeBackend struct {
	projects  []api.ProjectSummary
	snaps     map[string]api.Snapshot
	actions   []string
	dirty     int    // stories/close without force is refused with this many files
	closeMsg  string // the Message of a stories/close that goes through
	created   []api.NewProject
	links     []api.Link // the links of every workspace
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
	review      api.Review // the stored review: GET returns it, PUT and clear-sent change it
	saveRev     []api.ReviewComments
	sends       []api.ReviewSend
	sendRes     api.Result
	sendErr     error
	reviewErr   error                 // PUT fails with this
	explains    []api.ExplainRequest  // every explain asked
	dismissed   []string              // every explanation stopped or dismissed
	artifacts   api.ArtifactList      // the reply of every artifacts list
	reads       []string              // "storyinfo", "branches fetch", "prs <q>"
	info        api.StoryInfo         // the reply of storyinfo
	branchList  api.BranchList        // the reply of branches
	prs         map[string]api.PRList // the reply of prs, by query
	exists      string                // POST stories without use_existing is refused: local, origin or both
	storyErr    error                 // POST stories fails
	sessionErr  string                // the new story's session does not start
	storyCtxErr error                 // ctx.Err() as POST stories saw it
	// settings › mcp
	mcp      api.MCP
	mcpDocs  map[string]string // by scope ("" = global)
	mcpSaves []string          // "<scope>=<text>"
	mcpErr   *api.Result       // SaveMCPDoc refuses with it
	// settings › git: GitHub accounts
	acc      api.GitAccounts
	accSaves []accounts.Config
	accErr   error // the next SaveGitAccounts fails with it
	logins   int   // shells/login calls
	// story sync
	syncRes api.Result
	syncErr error // refuses a sync without force
	// attach
	uploads   []string // "<project> <num> <name> <bytes>"
	uploadErr error
}

func (f *fakeBackend) Upload(_ context.Context, project string, num int, name string, content []byte) (api.UploadResult, error) {
	f.uploads = append(f.uploads, fmt.Sprintf("%s %d %s %d", project, num, name, len(content)))
	if f.uploadErr != nil {
		return api.UploadResult{}, f.uploadErr
	}
	return api.UploadResult{Path: "/srv/uploads/" + name}, nil
}

// Sync records "<project> sync <story> [force]" and replies with syncRes, or refuses with
// syncErr (syncRes filled in too, as the API does on a 409).
func (f *fakeBackend) Sync(_ context.Context, project string, a api.Action) (api.Result, error) {
	act := project + " sync " + a.Story
	if a.Force {
		act += " force"
	}
	f.actions = append(f.actions, act)
	if f.syncErr != nil && !a.Force {
		return f.syncRes, f.syncErr
	}
	return f.syncRes, nil
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

func (f *fakeBackend) Review(_ context.Context, project, story string) (api.Review, error) {
	f.reads = append(f.reads, "review "+project+" "+story)
	return f.review, nil
}

func (f *fakeBackend) SaveReview(_ context.Context, _, _ string, c api.ReviewComments) (api.Review, error) {
	f.saveRev = append(f.saveRev, c)
	if f.reviewErr != nil {
		return api.Review{}, f.reviewErr
	}
	for i := range c.Comments {
		if c.Comments[i].ID == "" {
			c.Comments[i].ID = fmt.Sprintf("n%d", i)
		}
	}
	f.review.Version++
	f.review.Comments = c.Comments
	return f.review, nil
}

func (f *fakeBackend) SendReview(_ context.Context, _, _ string, r api.ReviewSend) (api.Result, error) {
	f.sends = append(f.sends, r)
	return f.sendRes, f.sendErr
}

func (f *fakeBackend) Explain(_ context.Context, _, _ string, r api.ExplainRequest) (api.Review, error) {
	f.explains = append(f.explains, r)
	f.review.Explains = append(f.review.Explains, api.Explain{ID: fmt.Sprintf("e%d", len(f.explains)), File: r.File, Side: r.Side,
		StartLine: r.StartLine, Line: r.Line, Code: r.Code, Question: r.Question, State: api.ExplainRunning, Started: time.Now().Unix()})
	return f.review, nil
}

func (f *fakeBackend) DeleteExplain(_ context.Context, _, _, id string) (api.Review, error) {
	f.dismissed = append(f.dismissed, id)
	f.review.Explains = slices.DeleteFunc(f.review.Explains, func(e api.Explain) bool { return e.ID == id })
	return f.review, nil
}

func (f *fakeBackend) ClearSent(context.Context, string, string) (api.Review, error) {
	var left []api.Comment
	for _, c := range f.review.Comments {
		if c.SentAt == 0 {
			left = append(left, c)
		}
	}
	f.review.Comments = left
	return f.review, nil
}

func (f *fakeBackend) Artifacts(_ context.Context, project string, num int) (api.ArtifactList, error) {
	f.reads = append(f.reads, fmt.Sprintf("artifacts %s %d", project, num))
	return f.artifacts, nil
}

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

func (f *fakeBackend) WorkspaceLinks(context.Context, string) ([]api.Link, error) {
	return f.links, nil
}

func (f *fakeBackend) Projects(context.Context) ([]api.ProjectSummary, error) { return f.projects, nil }

func (f *fakeBackend) Snapshot(_ context.Context, p string) (api.Snapshot, error) {
	s, ok := f.snaps[p]
	if !ok {
		return s, errors.New("project not found")
	}
	return s, nil
}

// Do records "<project> <action> [num] [story] [title] [name] [base] [repos] [$k] force" (the
// set parts); base also sets the project's override in the project list, shells adds a zsh
// shell to the snapshot (the next number after the highest) and shells/close drops it.
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
	if a.K != 0 {
		parts = append(parts, fmt.Sprintf("$%d", a.K))
	}
	for _, n := range a.Link {
		parts = append(parts, "+"+n)
	}
	for _, n := range a.Unlink {
		parts = append(parts, "-"+n)
	}
	if a.Force {
		parts = append(parts, "force")
	}
	f.actions = append(f.actions, strings.Join(parts, " "))
	switch {
	case action == "sessions":
		return api.Result{Num: 9}, nil
	case action == "shells":
		snap := f.snaps[project]
		k := 1
		for _, sh := range snap.Shells {
			if sh.Story == a.Story && sh.K >= k {
				k = sh.K + 1
			}
		}
		snap.Shells = append(snap.Shells, api.Shell{Story: a.Story, K: k, Command: "zsh"})
		f.snaps[project] = snap
		return api.Result{Story: a.Story, K: k}, nil
	case action == "shells/login":
		f.logins++
		snap := f.snaps[project]
		snap.Shells = append(snap.Shells, api.Shell{K: 1, Title: "gh auth login", Command: "gh"})
		f.snaps[project] = snap
		return api.Result{K: 1}, nil
	case action == "shells/close":
		snap := f.snaps[project]
		var kept []api.Shell
		for _, sh := range snap.Shells {
			if sh.Story != a.Story || sh.K != a.K {
				kept = append(kept, sh)
			}
		}
		snap.Shells = kept
		f.snaps[project] = snap
		return api.Result{}, nil
	case action == "base":
		for i := range f.projects {
			if f.projects[i].Name == project {
				f.projects[i].BaseOverride = a.Base
			}
		}
	case action == "stories/close" && !a.Force && f.dirty > 0:
		return api.Result{Error: "dirty", Dirty: f.dirty}, errors.New("dirty")
	case action == "stories/close":
		return api.Result{Message: f.closeMsg}, nil
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
	seen     int
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
func (c *fakeConn) PageSeen(context.Context) error {
	c.seen++
	return nil
}
func (c *fakeConn) Close() { c.closed = true }

// snapshot is a project with session 1 in story s1, in the agent status given; "unread" is an
// idle session that finished a turn nobody has opened since.
func snapshot(name, status string) api.Snapshot {
	p := state.New(name, "/p/"+name)
	p.Stories["s1"] = &state.Story{Branch: "feat/s1"}
	p.AddSession(&state.Session{Story: "s1", UUID: name + "-u1", Title: "Add lease time", Created: time.Now()})
	live := ui.Live{Now: time.Now(), Agents: map[string]claude.Agent{name + "-u1": {SessionID: name + "-u1", Status: status}}}
	if status == "unread" {
		live.Agents[name+"-u1"] = claude.Agent{SessionID: name + "-u1", Status: "idle"}
		live.Unread = map[string]bool{name + "-u1": true}
	}
	return api.Snapshot{Project: p, Live: live}
}

type harness struct {
	m      *Model
	be     *fakeBackend
	opened []string // "project num"
	conns  []*fakeConn
	dial   error // when set, every dial fails with it
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
		case "unread":
			sum.Unread = 1
		}
		be.projects = append(be.projects, sum)
	}
	hs := &harness{be: be}
	hs.m = NewModel(config.Default(root), be, project)
	hs.m.tick = func() tea.Cmd { return nil }
	hs.m.SetStreamer(func(_ context.Context, project string, t Target, cols, rows int, term *Term, _ func(tea.Msg)) (Conn, error) {
		if t.Shell() {
			hs.opened = append(hs.opened, fmt.Sprintf("%s sh %s %d", project, t.Story, t.K))
		} else {
			hs.opened = append(hs.opened, fmt.Sprintf("%s %d", project, t.Num))
		}
		if hs.dial != nil {
			return nil, hs.dial
		}
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
		case "C-c", "C-h", "C-l", "C-q", "C-v", "C-s":
			msg = tea.KeyPressMsg{Code: rune(k[2]), Mod: tea.ModCtrl}
		case "M-a", "M-c", "M-d", "M-e", "M-f", "M-g", "M-n", "M-o", "M-p", "M-r", "M-s", "M-t", "M-u", "M-v", "M-w", "M-x", "M-z":
			msg = tea.KeyPressMsg{Code: rune(k[2]), Mod: tea.ModAlt}
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
	if !strings.Contains(lines[29], "j/k move") || !strings.Contains(lines[29], "M-o projects") {
		t.Fatalf("key bar: %q", lines[29])
	}
	if !strings.Contains(v, "No session open") || len(h.opened) != 0 {
		t.Fatalf("no stream before a session is chosen: %q\n%s", h.opened, v)
	}
}

func TestStartupWithNothingOpenedIsEmpty(t *testing.T) {
	h := startIn(t, t.TempDir(), "", 120, 30, "running", "running")
	v := h.m.View().Content
	if h.m.picker != nil || h.m.project != "" || !strings.Contains(v, "No project open.") || !strings.Contains(v, "M-o open a project") {
		t.Fatalf("picker=%v project=%q\n%s", h.m.picker != nil, h.m.project, v)
	}
	h.press("M-o", "Down", "Enter")
	if h.m.project != "lab" || h.m.picker != nil || !strings.Contains(h.m.View().Content, "Add lease time") {
		t.Fatalf("project=%q\n%s", h.m.project, h.m.View().Content)
	}
}

func TestOpenedProjectsAreSavedAndRestored(t *testing.T) {
	root := t.TempDir()
	h := startIn(t, root, "", 120, 30, "running", "running")
	h.press("M-o", "Enter") // home-network
	h.press("M-o")
	if v := view(h); !strings.Contains(v, "OPEN HERE") || strings.Index(v, "◉ home-network") > strings.Index(v, "ON THE SERVER") || !strings.Contains(v, "○ lab") {
		t.Fatalf("open projects come first:\n%s", v)
	}
	h.press("Enter") // lab, the first one not open
	h.press("[")     // back to home-network: shown last
	h = startIn(t, root, "", 120, 30, "running", "running")
	if h.m.project != "home-network" || strings.Join(h.m.opened.Projects, ",") != "home-network,lab" {
		t.Fatalf("restored project=%q opened=%v", h.m.project, h.m.opened.Projects)
	}
	if v := ansi.Strip(h.m.View().Content); !strings.Contains(v, "1 home-network  2 lab ●1") {
		t.Fatalf("strip:\n%s", v)
	}
}

func TestNumberKeysOpenProjectsByTheirTab(t *testing.T) {
	h := start(t, 120, 30, "running", "running")
	h.press("M-o", "Enter") // lab: tabs 1 home-network, 2 lab
	if h.m.project != "lab" {
		t.Fatal(h.m.project)
	}
	h.press("1")
	if h.m.project != "home-network" {
		t.Fatalf("1 opened %q", h.m.project)
	}
	h.press("2", "9") // 9: no such tab, nothing happens
	if h.m.project != "lab" {
		t.Fatalf("2 then 9 left %q", h.m.project)
	}
}

func TestCloseProject(t *testing.T) {
	root := t.TempDir()
	h := startIn(t, root, "", 120, 30, "running", "running")
	h.press("M-o", "Enter", "M-o", "Enter") // home-network, then lab
	h.press("M-x")
	if v := view(h); h.m.project != "lab" || !strings.Contains(v, "Close project lab?") || !strings.Contains(v, "Its sessions keep running on the server.") {
		t.Fatalf("X asks first: project=%q\n%s", h.m.project, v)
	}
	h.press("n")
	if h.m.project != "lab" || len(h.m.opened.Projects) != 2 {
		t.Fatalf("n keeps it open: project=%q opened=%v", h.m.project, h.m.opened.Projects)
	}
	h.press("M-x", "y")
	if h.m.project != "home-network" || strings.Join(h.m.opened.Projects, ",") != "home-network" {
		t.Fatalf("project=%q opened=%v", h.m.project, h.m.opened.Projects)
	}
	h.press("M-o")
	if v := ansi.Strip(h.m.View().Content); !strings.Contains(v, "lab") {
		t.Fatalf("a closed project goes back to the picker:\n%s", v)
	}
	h.press("Esc", "M-x", "y")
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
	if c := h.m.View().Cursor; c == nil || c.X < ui.ListWidth {
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
	h.press("C-q")
	if h.m.claudeFocused {
		t.Fatal("C-q must return to the list")
	}
	h.press("C-q")
	if !h.m.claudeFocused || len(h.opened) != 1 {
		t.Fatalf("C-q on the open session must not reconnect: %q", h.opened)
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

func TestWheelScrollsTheClaudePane(t *testing.T) {
	h := start(t, 120, 30, "idle")
	if h.m.View().MouseMode != tea.MouseModeNone {
		t.Fatal("no session open: the mouse stays with the terminal")
	}
	h.press("Enter", "C-q") // a session open, keys back on the list
	if h.m.View().MouseMode != tea.MouseModeCellMotion {
		t.Fatal("a session is open: the wheel should be captured")
	}
	c := h.conns[0]
	h.update(tea.MouseWheelMsg{X: 5, Y: 5, Button: tea.MouseWheelUp})                  // over the list
	h.update(tea.MouseWheelMsg{X: ui.ListWidth + 5, Y: 5, Button: tea.MouseWheelDown}) // already live
	h.press("PgUp")                                                                    // no key scrolls
	if len(c.scrolled) != 0 || h.m.scrolling {
		t.Fatalf("scroll frames %v scrolling=%v", c.scrolled, h.m.scrolling)
	}
	h.update(tea.MouseWheelMsg{X: ui.ListWidth + 5, Y: 5, Button: tea.MouseWheelUp})
	if !h.m.scrolling || !h.m.claudeFocused {
		t.Fatalf("scrolling=%v focused=%v: scrolling back gives the pane the keys", h.m.scrolling, h.m.claudeFocused)
	}
	if bar := ansi.Strip(h.m.bar()); !strings.Contains(bar, "SCROLL") || !strings.Contains(bar, "esc back to live") {
		t.Fatalf("the bar should list the scroll keys:\n%s", bar)
	}
	h.press("k") // to tmux, without going back to live first
	if b := <-h.m.term.Input(); string(b) != "k" {
		t.Fatalf("forwarded %q", b)
	}
	h.update(tea.MouseWheelMsg{X: ui.ListWidth + 5, Y: 5, Button: tea.MouseWheelDown})
	if got, want := fmt.Sprint(c.scrolled), fmt.Sprint([]int{-wheelLines, wheelLines}); got != want {
		t.Fatalf("scroll frames %s, want %s", got, want)
	}
	h.update(copyEndMsg{conn: &fakeConn{}}) // from an old stream
	if !h.m.scrolling {
		t.Fatal("a stale mode frame must be ignored")
	}
	h.update(copyEndMsg{conn: c})
	if h.m.scrolling || strings.Contains(ansi.Strip(h.m.bar()), "SCROLL") {
		t.Fatal("tmux left copy-mode")
	}
	h.press("x") // live again: straight to Claude, no frame
	if b := <-h.m.term.Input(); string(b) != "x" {
		t.Fatalf("forwarded %q", b)
	}
	if len(c.scrolled) != 2 {
		t.Fatalf("scroll frames %v", c.scrolled)
	}
	_, cmd := h.m.Update(clipboardMsg("héllo")) // a yank in the mode reaches this clipboard
	if cmd == nil || !strings.Contains(h.m.flash, "copied 5 chars") {
		t.Fatalf("flash=%q cmd=%v", h.m.flash, cmd)
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

// termInput is what the model sent to Claude since the last call.
func termInput(h *harness) string {
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

func TestAttachUploadsTheClipboardAndTypesItsPath(t *testing.T) {
	h := start(t, 120, 30, "idle")
	h.press("Enter")
	h.m.clipboard = func() (clipFile, error) { return clipFile{name: "shot.png", data: []byte("png")}, nil }
	h.press("C-v")
	if len(h.be.uploads) != 1 || h.be.uploads[0] != "home-network 1 shot.png 3" {
		t.Fatalf("uploads %v", h.be.uploads)
	}
	if got := termInput(h); got != "/srv/uploads/shot.png " {
		t.Fatalf("typed %q", got)
	}
	if h.m.flash != "attached shot.png" {
		t.Fatalf("flash %q", h.m.flash)
	}
}

func TestEmptyPasteAttachesTheClipboardImage(t *testing.T) {
	h := start(t, 120, 30, "idle")
	h.press("Enter")
	h.m.clipboard = func() (clipFile, error) { return clipFile{name: "clipboard.png", data: []byte("png")}, nil }
	h.update(tea.PasteMsg{Content: ""}) // ⌘V with only an image on the clipboard
	if len(h.be.uploads) != 1 || termInput(h) != "/srv/uploads/clipboard.png " {
		t.Fatalf("uploads %v", h.be.uploads)
	}
	h.update(tea.PasteMsg{Content: "text"})
	if len(h.be.uploads) != 1 || termInput(h) != "text" {
		t.Fatalf("a text paste must go to Claude as text: %v", h.be.uploads)
	}
	h.press("C-q")
	h.update(tea.PasteMsg{Content: ""})
	if len(h.be.uploads) != 1 {
		t.Fatalf("an empty paste on the list must not attach: %v", h.be.uploads)
	}
}

func TestAttachWithNothingToAttach(t *testing.T) {
	h := start(t, 120, 30, "idle")
	h.press("Enter")
	h.m.clipboard = func() (clipFile, error) { return clipFile{}, errNoClip }
	h.press("C-v")
	if len(h.be.uploads) != 0 || termInput(h) != "" {
		t.Fatalf("nothing must be uploaded or typed: %v", h.be.uploads)
	}
	if !strings.HasPrefix(h.m.flash, "nothing to attach") {
		t.Fatalf("flash %q", h.m.flash)
	}
}

func TestAttachRefusals(t *testing.T) {
	h := start(t, 120, 30, "idle")
	h.press("Enter")
	h.m.clipboard = func() (clipFile, error) { return clipFile{name: "big.mov", data: make([]byte, uploads.MaxFile+1)}, nil }
	h.press("C-v")
	if len(h.be.uploads) != 0 || h.m.flash != "attach: big.mov is over 20 MB" {
		t.Fatalf("uploads %v flash %q", h.be.uploads, h.m.flash)
	}
	h.m.clipboard = func() (clipFile, error) { return clipFile{name: "a.png", data: []byte("x")}, nil }
	h.be.uploadErr = errors.New("disk full")
	h.press("C-v")
	if termInput(h) != "" || h.m.flash != "attach: disk full" {
		t.Fatalf("flash %q", h.m.flash)
	}
}

func TestAttachAfterLeavingTheSessionOnlySaysWhere(t *testing.T) {
	h := start(t, 120, 30, "idle")
	h.press("Enter")
	h.m.gotAttached(attachedMsg{project: "home-network", num: 7, name: "a.png", path: "/srv/uploads/a.png"})
	if termInput(h) != "" || h.m.flash != "uploaded /srv/uploads/a.png" {
		t.Fatalf("flash %q", h.m.flash)
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
	h.press("C-q")
	h.update(tea.PasteMsg{Content: "list"})
	h.update(tea.MouseWheelMsg{X: ui.ListWidth + 5, Y: 5, Button: tea.MouseWheelUp}) // scrolled back
	h.update(tea.PasteMsg{Content: "copy-mode"})
	if got := input(); got != "" {
		t.Fatalf("a paste on the list or in copy-mode must not reach Claude, got %q", got)
	}
}

func TestSwitchPaneReturnsToTheOpenSessionOnly(t *testing.T) {
	h := start(t, 160, 30, "waiting")
	h.press("C-q")
	if h.m.claudeFocused || len(h.opened) != 0 {
		t.Fatalf("with no session open C-q does nothing: focused=%v opened=%q", h.m.claudeFocused, h.opened)
	}
	h.press("Enter", "C-q") // open session 1, back to the list
	if h.m.claudeFocused {
		t.Fatal("C-q in Claude must return to the list")
	}
	h.press("k") // cursor to the chats row, away from session 1
	h.press("C-q")
	if !h.m.claudeFocused || h.m.target.Num != 1 || len(h.opened) != 1 {
		t.Fatalf("C-q should go back to session 1 without opening another: focused=%v num=%d opened=%q", h.m.claudeFocused, h.m.target.Num, h.opened)
	}
}

func TestSwitchPaneNeverReachesClaude(t *testing.T) {
	h := start(t, 160, 30, "waiting")
	h.press("Enter", "C-q", "C-q", "C-h", "C-l") // C-h and C-l are tmux's now: they go to Claude
	if !h.m.claudeFocused {
		t.Fatal("C-h must not leave Claude")
	}
	var got []byte
	for len(got) < 2 {
		select {
		case b := <-h.m.term.Input():
			got = append(got, b...)
		case <-time.After(time.Second):
			t.Fatalf("Claude got %q, want C-h C-l", got)
		}
	}
	if string(got) != "\x08\x0c" {
		t.Fatalf("Claude got %q, want only C-h C-l", got)
	}
}

func TestCtrlCQuitsFromClaudeButNotFromAShell(t *testing.T) {
	h := start(t, 200, 30, "running")
	h.setShells(nil, map[string]string{"": "/p/home-network", "s1": "/w/s1"})
	h.press("M-t", "y", "C-c") // a shell keeps C-c for its commands
	if h.m.ctx.Err() != nil {
		t.Fatal("C-c in a shell closed bx")
	}
	select {
	case b := <-h.m.term.Input():
		if string(b) != "\x03" {
			t.Fatalf("the shell got %q", b)
		}
	case <-time.After(time.Second):
		t.Fatal("C-c did not reach the shell")
	}

	h = start(t, 200, 30, "running")
	h.press("Enter", "C-c") // in Claude: closes bx, Claude never sees it
	if h.m.ctx.Err() == nil {
		t.Fatal("C-c in Claude must close bx")
	}
	select {
	case b := <-h.m.term.Input():
		t.Fatalf("C-c reached Claude: %q", b)
	default:
	}
}

func TestPromptsCallTheBackend(t *testing.T) {
	h := start(t, 120, 30, "waiting")
	h.press("k", "k") // session 1 → story s1 → chats
	h.press("M-n")    // a new session in chats
	if v := view(h); len(h.opened) != 0 || !strings.Contains(v, "New session") || !strings.Contains(v, "in chats") || !strings.Contains(v, "/p/home-network") || !strings.Contains(v, "y  Start") {
		t.Fatalf("n on chats asks first: opened=%q\n%s", h.opened, v)
	}
	h.press("y") // no title prompt: the chat opens and takes the focus
	if !h.m.claudeFocused || strings.Join(h.opened, "|") != "home-network 9" {
		t.Fatalf("a session in chats should open and focus: %q", h.opened)
	}
	h.press("C-q", "y")     // back to the list; y on the waiting session 1 does nothing
	h.press("M-w", "Enter") // new branch
	h.typeText("guest")
	h.press("Enter", "Enter") // the name, then feat
	if got := strings.Join(h.be.actions, "|"); got != "home-network sessions|home-network stories guest type=feat" {
		t.Fatalf("actions = %q", got)
	}
}

// newBranch opens the name prompt of a new branch story.
func (h *harness) newBranch() {
	h.press("M-w", "Enter")
}

func TestNewStorySourcePicker(t *testing.T) {
	h := start(t, 120, 30, "idle")
	h.press("M-w")
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

func TestNewStoryForm(t *testing.T) {
	h := start(t, 120, 30, "idle")
	h.be.info = api.StoryInfo{Base: "origin/develop", LastType: "fix", Types: naming.Types}
	h.newBranch()
	v := view(h)
	for _, want := range []string{"New story", "Name", "Type", "[fix]", "Base    origin/develop", "Branch  fix/<name>"} {
		if !strings.Contains(ansi.Strip(h.m.prompt.Dialog().Render(120, 30, true)), want) {
			t.Errorf("no %q in the form:\n%s", want, v)
		}
	}
	h.typeText("Login Timeout")
	if !strings.Contains(view(h), "Branch  fix/login-timeout") {
		t.Fatalf("the branch follows the name:\n%s", view(h))
	}
	h.press("Tab", "Right") // chore, after fix
	if !strings.Contains(view(h), "Branch  chore/login-timeout") || h.m.prompt.Input != "Login Timeout" {
		t.Fatalf("the type changes the branch:\n%s", view(h))
	}
	h.press("Enter")
	if got := strings.Join(h.be.actions, "|"); got != "home-network stories Login Timeout type=chore" {
		t.Fatalf("actions = %q", got)
	}
	if !strings.Contains(h.m.flash, "Created login-timeout on chore/login-timeout") {
		t.Fatalf("flash = %q", h.m.flash)
	}

	h = start(t, 120, 30, "idle")
	h.be.info = api.StoryInfo{Base: "main", LastType: "none", Types: naming.Types}
	h.newBranch()
	h.typeText("x")
	if !strings.Contains(view(h), "Branch  x") {
		t.Fatalf("no type, no prefix:\n%s", view(h))
	}
	h.press("Enter")
	if got := strings.Join(h.be.actions, "|"); got != "home-network stories x type=none" {
		t.Fatalf("actions = %q", got)
	}
}

func TestNewBranchWithSlashIgnoresTheType(t *testing.T) {
	h := start(t, 120, 30, "idle")
	h.newBranch()
	h.typeText("fix/x")
	if !strings.Contains(view(h), "Branch  fix/x") {
		t.Fatalf("a full branch is the branch:\n%s", view(h))
	}
	h.press("Enter")
	if got := strings.Join(h.be.actions, "|"); got != "home-network stories fix/x" {
		t.Fatalf("actions = %q", got)
	}
}

func TestNewBranchExistsSaysSoInTheForm(t *testing.T) {
	for _, tc := range []struct{ exists, where string }{{"origin", "on origin"}, {"local", "locally"}, {"both", "locally and on origin"}} {
		h := start(t, 160, 30, "idle")
		h.be.exists = tc.exists
		h.newBranch()
		h.typeText("fix/login-timeout")
		h.press("Enter")
		if want := "fix/login-timeout exists " + tc.where + " · ⏎ again starts the story on it"; h.m.prompt.Kind != ui.PromptNewStory || h.m.prompt.Note != want || h.m.prompt.Input != "fix/login-timeout" {
			t.Fatalf("form note %q input %q, want %q", h.m.prompt.Note, h.m.prompt.Input, want)
		}
		h.press("Enter")
		if got := strings.Join(h.be.actions, "|"); got != "home-network stories fix/login-timeout|home-network stories fix/login-timeout use" {
			t.Fatalf("actions = %q", got)
		}
		if strings.Join(h.opened, "|") != "home-network 9" {
			t.Fatalf("opened = %q", h.opened)
		}
	}
	// Esc sends nothing more; a changed name is a new try, not the branch that exists
	h := start(t, 160, 30, "idle")
	h.be.exists = "origin"
	h.newBranch()
	h.typeText("fix/x")
	h.press("Enter", "Esc")
	if got := strings.Join(h.be.actions, "|"); got != "home-network stories fix/x" || len(h.opened) != 0 {
		t.Fatalf("esc sends nothing more: actions=%q opened=%q", got, h.opened)
	}
	h.newBranch()
	h.typeText("fix/x")
	h.press("Enter")
	h.press("BSpace")
	h.typeText("y")
	h.press("Enter")
	if got := strings.Join(h.be.actions, "|"); got != "home-network stories fix/x|home-network stories fix/x|home-network stories fix/y" {
		t.Fatalf("a new name tries again: actions=%q", got)
	}
}

func TestNewStoryStreamsItsSessionAndFocusesClaude(t *testing.T) {
	h := start(t, 120, 30, "idle")
	h.newBranch()
	h.typeText("login")
	h.press("Enter")
	if !h.m.claudeFocused || h.m.target.Num != 9 || strings.Join(h.opened, "|") != "home-network 9" {
		t.Fatalf("focused=%v num=%d opened=%q", h.m.claudeFocused, h.m.target.Num, h.opened)
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
	h.press("Enter")
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
	h.press("M-w", "Down")
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
	h.press("M-w", "Down", "Enter")
	if h.m.pickKind == pickStoryBranch && h.m.picker != nil || h.m.flash != "no branches in home-network" {
		t.Fatalf("pickKind=%v flash=%q", h.m.pickKind, h.m.flash)
	}
}

func TestWorkspaceExistingBranchShowsItsRepos(t *testing.T) {
	h := start(t, 160, 30, "idle")
	workspace(h)
	h.be.branchList = api.BranchList{Branches: []api.Branch{{Name: "fix/x", Local: true, Time: time.Now().Unix(), Repos: []string{"api", "web"}}}}
	h.press("M-w", "Down", "Enter")
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
	h.press("M-w", "Down", "Down", "Enter")
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
	h.press("M-w", "Down", "Down", "Enter")
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
	h.press("M-w", "Down", "Down", "Enter")
	h.typeText("oauth")
	h.press("Enter") // nothing matches: search GitHub
	v := ansi.Strip(h.m.View().Content)
	if h.m.picker == nil || !strings.Contains(v, "search: oauth") || !strings.Contains(v, "#99 OAuth flow   carol · feat/oauth") || h.m.picker.Filter() != "" {
		t.Fatalf("search results:\n%s", v)
	}
	h.press("Esc", "M-w", "Down", "Down", "Enter")
	h.typeText("nothin")
	h.press("Enter")
	if h.m.picker == nil || h.m.flash != "no pull requests match nothin" {
		t.Fatalf("flash=%q", h.m.flash)
	}
	h.press("Esc", "M-w", "Down", "Down", "Enter")
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
		h.press("M-w", "Down", "Down", "Enter")
		h.typeText(q)
		h.press("Enter")
		if got := strings.Join(h.be.actions, "|"); got != fmt.Sprintf("home-network stories source=pr pr=%d", pr.Number) {
			t.Fatalf("%s: actions = %q", q, got)
		}
		if got := strings.Join(h.be.reads, "|"); got != "prs |prs "+q {
			t.Fatalf("%s: reads = %q", q, got)
		}
	}
	// moving the cursor down picks the highlighted row instead
	h := start(t, 160, 30, "idle")
	h.be.prs = map[string]api.PRList{"": {PRs: append(l.PRs, api.PR{Number: 4567, Title: "Later", Author: "eve", Branch: "feat/later"})}}
	h.press("M-w", "Down", "Down", "Enter")
	h.typeText("#45") // #456 and #4567 match
	h.press("Down", "Enter")
	if got := strings.Join(h.be.actions, "|"); got != "home-network stories source=pr pr=4567" || strings.Join(h.be.reads, "|") != "prs " {
		t.Fatalf("moved cursor: actions = %q reads = %q", got, h.be.reads)
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
	h.press("M-w", "Down", "Down", "Enter")
	if h.m.picker != nil || h.m.flash != "gh: acme requires SSO authorization" {
		t.Fatalf("picker=%v flash=%q", h.m.picker != nil, h.m.flash)
	}
	workspace(h)
	l := prs()
	l.Errors = []string{"acme/api: HTTP 401"}
	h.be.prs = map[string]api.PRList{"": l}
	h.press("M-w", "Down", "Down", "Enter")
	if v := ansi.Strip(h.m.View().Content); !strings.Contains(v, "pull requests · gh: acme/api: HTTP 401") {
		t.Fatalf("title:\n%s", v)
	}
}

func TestCloseStoryAsksAgainWhenDirty(t *testing.T) {
	h := start(t, 120, 30, "running")
	h.be.dirty = 2
	h.press("k", "M-d") // from the story row
	if !strings.Contains(view(h), "Close story s1?") {
		t.Fatal(h.m.View().Content)
	}
	h.press("y")
	if !strings.Contains(h.m.View().Content, "2 uncommitted") {
		t.Fatal(h.m.View().Content)
	}
	h.be.closeMsg = "deleted branch feat/s1 (local, origin)"
	h.press("y")
	if got := strings.Join(h.be.actions, "|"); got != "home-network stories/close s1|home-network stories/close s1 force" {
		t.Fatalf("actions = %q", got)
	}
	if h.m.flash != "closed story s1 · deleted branch feat/s1 (local, origin)" {
		t.Fatalf("flash = %q", h.m.flash)
	}
}

func TestCloseStoryOnlyFromStoryRows(t *testing.T) {
	h := start(t, 120, 30, "running")
	h.press("M-d") // the cursor starts on session 1: D deletes it, the story stays
	if v := view(h); strings.Contains(v, "Close story") || !strings.Contains(v, "Delete [1]?") {
		t.Fatalf("D on a session row deletes the session:\n%s", h.m.View().Content)
	}
	h.press("Esc", "k", "M-d")
	if !strings.Contains(view(h), "Close story s1?") {
		t.Fatal(h.m.View().Content)
	}
}

func TestStopAsksThenStops(t *testing.T) {
	h := start(t, 120, 30, "running")
	h.press("M-s")
	if v := view(h); !strings.Contains(v, "Stop [1]?") || !strings.Contains(v, "Add lease time") {
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

func TestStopAsksWhenNotAlive(t *testing.T) {
	h := start(t, 120, 30, "idle")
	snap := h.be.snaps["home-network"]
	snap.Live.Agents = map[string]claude.Agent{} // its Claude is gone
	h.be.snaps["home-network"] = snap
	h.update(tickMsg(time.Now()))
	if h.m.m.Alive(1) {
		t.Fatal("session 1 should not be alive")
	}
	h.press("M-s")
	if !strings.Contains(view(h), "Stop [1]?") || len(h.be.actions) != 0 {
		t.Fatalf("actions=%q\n%s", h.be.actions, h.m.View().Content)
	}
	h.press("n")
	if len(h.be.actions) != 0 {
		t.Fatalf("n must not stop: %q", h.be.actions)
	}
	h.press("M-s", "y")
	if got := strings.Join(h.be.actions, "|"); got != "home-network stop 1" {
		t.Fatalf("actions = %q", got)
	}
}

func TestNewSessionAsksFirst(t *testing.T) {
	h := start(t, 120, 30, "running")
	h.press("M-n")
	if v := view(h); len(h.be.actions) != 0 || !strings.Contains(v, "in s1") || !strings.Contains(v, "y  Start") {
		t.Fatalf("actions=%q\n%s", h.be.actions, v)
	}
	h.press("Esc")
	if len(h.be.actions) != 0 || h.m.prompt.Active() {
		t.Fatalf("Esc cancels: %q", h.be.actions)
	}
	h.press("M-n", "Enter") // Enter also starts it
	if got := strings.Join(h.be.actions, "|"); got != "home-network sessions s1" {
		t.Fatalf("actions = %q", got)
	}
}

func TestStopTheOpenSessionClosesItsStream(t *testing.T) {
	h := start(t, 120, 30, "running")
	h.press("Enter", "C-q", "M-s", "y")
	if h.m.target.Num != 0 || !h.conns[0].closed || h.m.claudeFocused {
		t.Fatalf("num=%d closed=%v focused=%v", h.m.target.Num, h.conns[0].closed, h.m.claudeFocused)
	}
	h.update(streamClosedMsg{conn: h.conns[0], err: websocket.CloseError{Code: websocket.StatusNormalClosure, Reason: "session ended"}})
	if h.m.flash != "stopped [1]" {
		t.Fatalf("flash = %q", h.m.flash)
	}
}

func TestEnterOnADoneSessionAsksThenResumes(t *testing.T) {
	for _, k := range []string{"Enter", "M-f"} {
		h := start(t, 120, 30, "idle")
		h.markDone()
		h.press(k)
		if v := view(h); len(h.be.actions) != 0 || h.m.claudeFocused || !strings.Contains(v, "Resume [1]?") || !strings.Contains(v, "Claude starts again where it left off.") {
			t.Fatalf("%s asks first: actions=%q\n%s", k, h.be.actions, v)
		}
		h.press("Enter") // Resume is the default
		if got := strings.Join(h.be.actions, "|"); got != "home-network resume 1" {
			t.Fatalf("%s: actions = %q", k, got)
		}
		if !h.m.claudeFocused || h.m.target.Num != 1 {
			t.Fatalf("%s: the resumed session is shown with the keys: focused=%v num=%d", k, h.m.claudeFocused, h.m.target.Num)
		}
	}
	h := start(t, 120, 30, "idle")
	h.markDone()
	h.press("Enter", "n")
	if len(h.be.actions) != 0 || h.m.claudeFocused {
		t.Fatalf("n resumes nothing: %q", h.be.actions)
	}
	h.press("r") // no resume key any more
	if len(h.be.actions) != 0 || h.m.prompt.Active() {
		t.Fatalf("r did something: %q", h.be.actions)
	}
}

func TestDeleteAsksTwiceWhenRunning(t *testing.T) {
	h := start(t, 160, 30, "running")
	h.be.running = map[int]bool{1: true}
	h.press("M-d")
	if v := view(h); !strings.Contains(v, "Delete [1]?") || !strings.Contains(v, "Add lease time") {
		t.Fatal(h.m.View().Content)
	}
	h.press("y")
	if v := view(h); !strings.Contains(v, "[1] is still open") || !strings.Contains(v, "Kill it and delete its record?") {
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

// a then Enter opens the most urgent session, in another project too.
func TestInboxOpensWaitingAcrossProjects(t *testing.T) {
	h := start(t, 120, 30, "running", "waiting")
	h.press("M-a", "Enter")
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
	if h.m.claudeFocused || h.m.target.Num != 0 || !strings.Contains(h.m.View().Content, "ended") {
		t.Fatalf("focused=%v num=%d\n%s", h.m.claudeFocused, h.m.target.Num, h.m.View().Content)
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
	h.press("M-o")
	h.typeText("existing")
	h.press("Enter")
	h.update(tea.PasteMsg{Content: "/srv/lab\r\n"}) // ⌘V, with the newline a copied path often has
	h.press("Enter")
	if len(h.be.created) != 1 || !reflect.DeepEqual(h.be.created[0], api.NewProject{Kind: "folder", Path: "/srv/lab"}) {
		t.Fatalf("%+v", h.be.created)
	}
	if h.m.project != "lab" {
		t.Fatalf("should switch to the new project: %q", h.m.project)
	}
}

func TestAddFirstFolderWithNoProjects(t *testing.T) {
	h := start(t, 120, 30) // no projects: the empty screen
	h.be.snaps["lab"] = snapshot("lab", "running")
	h.press("M-o")
	h.typeText("existing")
	h.press("Enter")
	h.typeText("/srv/lab")
	if v := h.m.View().Content; !strings.Contains(v, "/srv/lab") {
		t.Fatalf("the prompt should show what is typed:\n%s", v)
	}
	h.press("Enter")
	if len(h.be.created) != 1 || !reflect.DeepEqual(h.be.created[0], api.NewProject{Kind: "folder", Path: "/srv/lab"}) {
		t.Fatalf("%+v", h.be.created)
	}
	if h.m.project != "lab" || !strings.Contains(h.m.View().Content, "Add lease time") {
		t.Fatalf("project=%q\n%s", h.m.project, h.m.View().Content)
	}
}

func TestCancelledOrFailedFirstAddStaysEmpty(t *testing.T) {
	h := start(t, 120, 30)
	h.press("M-o")
	h.typeText("existing")
	h.press("Enter", "Esc")
	if v := h.m.View().Content; h.m.picker != nil || !strings.Contains(v, "No project open.") {
		t.Fatalf("Esc should go back to the empty screen:\n%s", v)
	}
	h.be.createErr = errors.New("not a git repository")
	h.press("M-o", "Enter") // + Add existing folder
	h.typeText("/srv/nope")
	h.press("Enter")
	if v := h.m.View().Content; h.m.picker != nil || !strings.Contains(v, "No project open.") || !strings.Contains(v, "not a git repository") {
		t.Fatalf("a failed add should show the error on the empty screen:\n%s", v)
	}
}

func TestCloneFromThePicker(t *testing.T) {
	h := start(t, 120, 30, "running")
	h.press("M-o")
	h.update(tea.PasteMsg{Content: "clone"}) // a paste filters the picker
	h.press("Enter")
	if !strings.Contains(h.m.View().Content, "me/other") {
		t.Fatal(h.m.View().Content)
	}
	h.press("Down", "Enter")
	if len(h.be.created) != 1 || !reflect.DeepEqual(h.be.created[0], api.NewProject{Kind: "clone", Repo: "me/other"}) {
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

// setShells puts shells (and group folders) in home-network's snapshot and polls it.
func (h *harness) setShells(shells []api.Shell, dirs map[string]string) {
	snap := h.be.snaps["home-network"]
	snap.Shells, snap.Dirs = shells, dirs
	h.be.snaps["home-network"] = snap
	h.update(tickMsg(time.Now()))
}

// dropShells takes every shell out of home-network's snapshot without polling it, as when they
// exit just before their streams close.
func (h *harness) dropShells() {
	snap := h.be.snaps["home-network"]
	snap.Shells = nil
	h.be.snaps["home-network"] = snap
}

// shellEnded is the server closing the newest stream because its shell exited.
func (h *harness) shellEnded() {
	h.update(streamClosedMsg{conn: h.conns[len(h.conns)-1], err: websocket.CloseError{Code: websocket.StatusNormalClosure, Reason: "session ended"}})
}

func TestNewShellAsksThenOpens(t *testing.T) {
	h := start(t, 200, 30, "running")
	h.setShells(nil, map[string]string{"": "/p/home-network", "s1": "/w/s1"})
	h.press("M-t") // on session 1 of s1
	if v := view(h); !strings.Contains(v, "New shell $1") || !strings.Contains(v, "in s1") || !strings.Contains(v, "/w/s1") || !strings.Contains(v, "y  Open") {
		t.Fatalf("view:\n%s", v)
	}
	h.press("n")
	if len(h.be.actions) != 0 || h.m.prompt.Active() {
		t.Fatalf("n must do nothing: %q", h.be.actions)
	}
	h.press("M-t", "y")
	if got := strings.Join(h.be.actions, "|"); got != "home-network shells s1" {
		t.Fatalf("actions = %q", got)
	}
	if got := strings.Join(h.opened, "|"); got != "home-network sh s1 1" || !h.m.claudeFocused {
		t.Fatalf("opened=%q focused=%v", got, h.m.claudeFocused)
	}
	if v := ansi.Strip(h.m.View().Content); !strings.Contains(v, "s1 › $1 zsh") || !strings.Contains(v, "SHELL") {
		t.Fatalf("pane title:\n%s", v)
	}
	if bar := ansi.Strip(h.m.bar()); !strings.Contains(bar, "SHELL  type to run · exit closes it  ^q to list") {
		t.Fatalf("bar = %q", bar)
	}
	if sh, ok := h.m.m.SelectedShell(); !ok || sh.K != 1 {
		t.Fatalf("the cursor should be on the new shell: %+v", h.m.m.Selected())
	}
	h.press("C-q", "k", "k", "k") // $1 → session 1 → s1 → the chats row
	h.press("M-t")
	if v := view(h); !strings.Contains(v, "New shell $1") || !strings.Contains(v, "in chats") || !strings.Contains(v, "/p/home-network") {
		t.Fatalf("view:\n%s", v)
	}
	h.press("Enter") // Enter opens too
	if got := strings.Join(h.opened, "|"); got != "home-network sh s1 1|home-network sh  1" {
		t.Fatalf("opened = %q", got)
	}
}

func TestShellRowRenameAndClose(t *testing.T) {
	h := start(t, 200, 30, "running")
	h.setShells([]api.Shell{{Story: "s1", K: 1, Command: "zsh"}, {Story: "s1", K: 2, Command: "npm"}}, nil)
	h.press("j", "j") // session 1 → $1 → $2
	if sh, ok := h.m.m.SelectedShell(); !ok || sh.K != 2 {
		t.Fatalf("selected %+v", h.m.m.Selected())
	}
	if bar := ansi.Strip(h.m.bar()); !strings.Contains(bar, "$2 npm › ⏎ open  M-r rename  M-d close  │") {
		t.Fatalf("bar = %q", bar)
	}
	h.press("M-r")
	h.typeText("api")
	h.press("Enter")
	if got := strings.Join(h.be.actions, "|"); got != "home-network shells/rename s1 api $2" {
		t.Fatalf("actions = %q", got)
	}
	h.press("M-d")
	if v := view(h); !strings.Contains(v, "Close shell $2?") || !strings.Contains(v, "npm is still running and will be killed.") || !strings.Contains(v, "y  Close") {
		t.Fatalf("view:\n%s", v)
	}
	h.press("y")
	if got := h.be.actions[len(h.be.actions)-1]; got != "home-network shells/close s1 $2" {
		t.Fatalf("action = %q", got)
	}
	if !strings.Contains(h.m.flash, "closed shell $2") {
		t.Fatalf("flash = %q", h.m.flash)
	}
	if sh, ok := h.m.m.SelectedShell(); !ok || sh.K != 1 {
		t.Fatalf("the cursor should move to the shell left: %+v", h.m.m.Selected())
	}
	h.press("M-d")
	if v := view(h); !strings.Contains(v, "Close shell $1?") || !strings.Contains(v, "zsh in s1") || strings.Contains(v, "still running") {
		t.Fatalf("view:\n%s", v)
	}
	h.press("n")
	if n := len(h.be.actions); n != 2 {
		t.Fatalf("n must not close: %q", h.be.actions)
	}
}

func TestShellEndedKeepsTheOutputDimmed(t *testing.T) {
	h := start(t, 200, 30, "running")
	h.press("M-t", "y")
	h.dropShells() // the shell exited
	h.shellEnded()
	if h.m.claudeFocused || !h.m.paneClosed {
		t.Fatalf("focused=%v closed=%v", h.m.claudeFocused, h.m.paneClosed)
	}
	if v := ansi.Strip(h.m.View().Content); !strings.Contains(v, "CLOSED") || !strings.Contains(v, "CLAUDE SAYS HI") {
		t.Fatalf("the pane keeps the last output:\n%s", v)
	}
	if !strings.Contains(h.m.View().Content, "\x1b[2mCLAUDE SAYS HI") {
		t.Fatal("the closed output is dim")
	}
	if bar := ansi.Strip(h.m.bar()); !strings.Contains(bar, "shell $1 closed") {
		t.Fatalf("bar = %q", bar)
	}
	if r := h.m.m.Selected(); r.Kind != ui.RowStory || r.Story != "s1" {
		t.Fatalf("the cursor goes to the story: %+v", r)
	}
	h.update(tickMsg(time.Now()))
	if len(h.opened) != 1 {
		t.Fatalf("a closed shell must not reconnect: %q", h.opened)
	}

	h.press("j", "Enter") // session 1
	if h.m.paneClosed || strings.Join(h.opened, "|") != "home-network sh s1 1|home-network 1" {
		t.Fatalf("closed=%v opened=%q", h.m.paneClosed, h.opened)
	}

	h.press("C-q", "M-t", "y")
	delete(h.be.snaps["home-network"].Project.Stories, "s1") // D closed the story, and its shells
	h.dropShells()
	h.shellEnded()
	if bar := ansi.Strip(h.m.bar()); !strings.Contains(bar, "shell $1 closed · story s1 was closed") {
		t.Fatalf("bar = %q", bar)
	}
}

func TestClosedShellKeepsItsLabel(t *testing.T) {
	h := start(t, 200, 30, "running")
	h.setShells([]api.Shell{{Story: "s1", K: 1, Title: "api", Command: "npm"}}, nil)
	h.press("j", "Enter") // session 1 → $1
	h.dropShells()
	h.shellEnded()
	h.update(tickMsg(time.Now())) // the shell's row is gone from the list
	if _, ok := h.m.m.Shell("s1", 1); ok {
		t.Fatal("the row should be gone")
	}
	if v := ansi.Strip(h.m.View().Content); !strings.Contains(v, "home-network › s1 › $1 api") || !strings.Contains(v, "CLOSED") {
		t.Fatalf("pane title:\n%s", v)
	}
}

// The stream drops, the shell exits meanwhile and the reconnect gets 410: the pane closes as if
// the shell had ended with the stream up, and stops retrying.
func TestShellGoneDuringADropCloses(t *testing.T) {
	h := start(t, 200, 30, "running")
	h.press("M-t", "y")
	h.update(streamClosedMsg{conn: h.conns[0], err: errors.New("EOF")})
	h.dropShells()
	h.dial = &StatusError{Code: http.StatusGone, Text: "failed to WebSocket dial: expected handshake response status code 101 but got 410"}
	h.update(connectMsg{gen: h.m.gen, err: h.dial})
	if !h.m.paneClosed || h.m.claudeFocused {
		t.Fatalf("closed=%v focused=%v", h.m.paneClosed, h.m.claudeFocused)
	}
	if bar := ansi.Strip(h.m.bar()); !strings.Contains(bar, "shell $1 closed") || strings.Contains(bar, "retrying") {
		t.Fatalf("bar = %q", bar)
	}
	if v := ansi.Strip(h.m.View().Content); !strings.Contains(v, "$1 zsh") || !strings.Contains(v, "CLOSED") {
		t.Fatalf("pane:\n%s", v)
	}
	if r := h.m.m.Selected(); r.Kind != ui.RowStory || r.Story != "s1" {
		t.Fatalf("the cursor goes to the story: %+v", r)
	}
	h.update(tickMsg(time.Now()))
	if len(h.opened) != 1 {
		t.Fatalf("a closed shell must not reconnect: %q", h.opened)
	}
}

// The reconnect fails for another reason but the snapshot no longer lists the shell: closed too,
// with the note when its story went with it.
func TestShellMissingFromTheSnapshotAfterADropCloses(t *testing.T) {
	h := start(t, 200, 30, "running")
	h.press("M-t", "y")
	h.update(streamClosedMsg{conn: h.conns[0], err: errors.New("EOF")})
	delete(h.be.snaps["home-network"].Project.Stories, "s1")
	h.dropShells()
	h.dial = errors.New("dial tcp: connection refused")
	h.update(tickMsg(time.Now()))
	if !h.m.paneClosed {
		t.Fatal("the pane should be closed")
	}
	if bar := ansi.Strip(h.m.bar()); !strings.Contains(bar, "shell $1 closed · story s1 was closed") {
		t.Fatalf("bar = %q", bar)
	}
	h.update(tickMsg(time.Now()))
	if len(h.opened) != 2 { // the first open and the one failed reconnect
		t.Fatalf("a closed shell must not reconnect: %q", h.opened)
	}
}

// A snapshot without the shell while its stream is down closes the pane before any reconnect.
func TestSnapshotWithoutTheShellWhileDownCloses(t *testing.T) {
	h := start(t, 200, 30, "running")
	h.press("M-t", "y")
	h.update(streamClosedMsg{conn: h.conns[0], err: errors.New("EOF")})
	h.dropShells()
	h.update(snapshotMsg{project: "home-network", snap: h.be.snaps["home-network"]})
	if !h.m.paneClosed || !strings.Contains(h.m.flash, "shell $1 closed") {
		t.Fatalf("closed=%v flash=%q", h.m.paneClosed, h.m.flash)
	}
	if r := h.m.m.Selected(); r.Kind != ui.RowStory || r.Story != "s1" {
		t.Fatalf("the cursor goes to the story: %+v", r)
	}
	h.update(tickMsg(time.Now()))
	if len(h.opened) != 1 {
		t.Fatalf("a closed shell must not reconnect: %q", h.opened)
	}
}

// A Claude session that is not running keeps retrying: it can be resumed.
func TestGoneSessionKeepsRetrying(t *testing.T) {
	h := start(t, 200, 30, "running")
	h.press("Enter")
	h.update(streamClosedMsg{conn: h.conns[0], err: errors.New("EOF")})
	h.dial = &StatusError{Code: http.StatusGone, Text: "got 410"}
	h.update(tickMsg(time.Now()))
	h.update(tickMsg(time.Now()))
	if strings.Join(h.opened, "|") != "home-network 1|home-network 1|home-network 1" || h.m.paneClosed || h.m.target != sessionTarget(1) {
		t.Fatalf("opened=%q closed=%v target=%+v", h.opened, h.m.paneClosed, h.m.target)
	}
	if !strings.Contains(h.m.flash, "got 410 · retrying") {
		t.Fatalf("flash = %q", h.m.flash)
	}
}

func TestCloseStoryCountsShells(t *testing.T) {
	h := start(t, 200, 30, "running")
	h.setShells([]api.Shell{{Story: "s1", K: 1, Command: "zsh"}, {Story: "s1", K: 2, Command: "npm"}}, nil)
	h.m.m.SelectStory("s1")
	h.press("M-d")
	if v := view(h); !strings.Contains(v, "Its sessions and 2 shells stop and its worktree is removed") || !strings.Contains(v, "Branch feat/s1 is deleted here and on origin.") {
		t.Fatalf("view:\n%s", v)
	}
}

func TestProjectStripAndSwitchKeys(t *testing.T) {
	h := start(t, 280, 30, "waiting", "running") // the key bar with the switch keys is about 270 columns
	h.press("M-o", "Enter", "[")                 // open lab, then back to home-network
	v := ansi.Strip(h.m.View().Content)
	if !strings.Contains(v, "1 home-network  2 lab ●1") || !strings.Contains(v, "[ ] switch project") || !strings.Contains(v, "M-a inbox") {
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

func TestInboxListsEveryProject(t *testing.T) {
	h := start(t, 160, 30, "unread", "waiting")
	h.press("M-a") // the inbox
	v := ansi.Strip(h.m.View().Content)
	// lab waits, so it comes first
	lab, home := strings.Index(v, "  lab "), strings.Index(v, "  home-network ")
	if !strings.Contains(v, "bx inbox") || lab < 0 || home < 0 || lab > home {
		t.Fatalf("inbox:\n%s", v)
	}
	if !strings.Contains(v, "s1 · asks: needs you") || !strings.Contains(v, "s1 · idle · unread") || !strings.Contains(v, "M-a back") || strings.Contains(v, "y yes") {
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
	h.press("C-q", "k", "Enter") // back to the list, up to lab's header: its list
	if h.m.activity || h.m.project != "lab" {
		t.Fatalf("activity=%v project=%q", h.m.activity, h.m.project)
	}
}

// Running and read idle sessions need nothing from you: the inbox leaves them out.
func TestInboxEmpty(t *testing.T) {
	h := start(t, 160, 30, "idle", "running")
	h.press("M-a")
	v := ansi.Strip(h.m.View().Content)
	if !strings.Contains(v, "Nothing needs you.") || !strings.Contains(v, "M-a back to home-network · M-o projects") {
		t.Fatalf("empty inbox:\n%s", v)
	}
	h.press("M-a")
	if h.m.activity {
		t.Fatal("a leaves the inbox")
	}
}

func TestSettingsScreenEditsAndSaves(t *testing.T) {
	h := start(t, 120, 30, "running")
	h.press("M-p")
	v := ansi.Strip(h.m.View().Content)
	if h.m.settings == nil || !strings.Contains(v, "bx settings") || !strings.Contains(v, "Permission mode") {
		t.Fatalf("P should open the settings:\n%s", v)
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
	h.press("M-p", "l", "Tab", "Tab", "Tab", "j") // an unsaved model change, then git, mcp, then the ntfy URL under Enabled
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
	h.press("M-p", "l", "Esc")
	if h.m.settings == nil || !strings.Contains(view(h), "Discard 1 change?") {
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
	h.press("M-p", "l", "Enter")
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
	if v := ansi.Strip(h.m.View().Content); !strings.Contains(v, "workspace") || !strings.Contains(v, "api") {
		t.Fatalf("view:\n%s", v)
	}
	h.be.info.LastType = "fix"
	h.newBranch()
	h.typeText("login")
	h.press("Enter") // the name, then fix, the last type
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
	if v := h.m.View().Content; !strings.Contains(v, "M-p settings · ? keys") {
		t.Fatal(v)
	}
	h.press("?")
	if v := h.m.View().Content; !strings.Contains(v, "bx keymap") || !strings.Contains(v, "add repositories") {
		t.Fatal(v)
	}
	h.press("x")
	if v := h.m.View().Content; !strings.Contains(v, "No project open.") {
		t.Fatal(v)
	}
}

func TestCleanupNothingToDo(t *testing.T) {
	h := start(t, 120, 30, "idle")
	h.press("M-c")
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
	h.press("M-c")
	v := h.m.View().Content
	for _, want := range []string{"cleanup · tick what to remove", "[x] bx-home-network-7", "[ ] home-network · chats"} {
		if !strings.Contains(v, want) {
			t.Fatalf("no %q in\n%s", want, v)
		}
	}
	h.press("Enter")
	v = view(h)
	if !strings.Contains(v, "Remove 1 item?") || !strings.Contains(v, "y  Remove") {
		t.Fatal(v)
	}
	h.press("Esc")
	if len(h.be.cleaned) != 0 {
		t.Fatalf("cleaned = %v", h.be.cleaned)
	}
	h.press("M-c", "Enter", "y")
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
	h.press("M-p", "Tab")
	if h.m.settings == nil || !h.m.settings.GitShown() {
		t.Fatalf("P, Tab should open settings › git:\n%s", ansi.Strip(h.m.View().Content))
	}
	return h
}

func TestSettingsGitAddsAnOverride(t *testing.T) {
	// develop is checked out in the project folder: still a base to pick
	h := gitSettings(t, api.Branch{Name: "develop", Local: true, Remote: true, CheckedOut: true}, api.Branch{Name: "main", Remote: true})
	v := ansi.Strip(h.m.View().Content)
	if !strings.Contains(v, "No overrides. New stories start from each repo's default branch.") || !strings.Contains(v, "j/k move · ⏎ add · r recheck gh · tab section · esc close") {
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
	h.press("Enter") // still on the add row: lab is left out now
	if v = ansi.Strip(h.m.picker.Render(120, 10)); h.m.pickKind != pickOverrideProject || strings.Contains(v, "lab") {
		t.Fatalf("the picker must leave out lab:\n%s", v)
	}
}

func TestSettingsGitChangesAndRemovesAnOverride(t *testing.T) {
	h := start(t, 120, 30, "running", "running")
	h.be.projects[1].BaseOverride = "develop"
	h.be.branchList = api.BranchList{Branches: []api.Branch{{Name: "main", Remote: true}, {Name: "develop", Remote: true}, {Name: "release/1.4", Local: true}}}
	drain(h.m, h.m.loadProjects())
	h.press("M-p", "Tab")
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
	if v := ansi.Strip(h.m.View().Content); !strings.Contains(v, "Remove base override?") || !strings.Contains(v, "New stories in lab stop starting from release/1.4") {
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

func TestArtifactsPickerOpensThePage(t *testing.T) {
	h := start(t, 120, 30, "idle")
	var browsed []string
	h.m.browse = func(u string) tea.Cmd { browsed = append(browsed, u); return nil }
	if bar := ansi.Strip(h.m.bar()); !strings.Contains(bar, "M-v artifacts") {
		t.Fatalf("bar = %q", bar)
	}
	h.press("M-v")
	if !strings.Contains(h.m.View().Content, "no artifacts yet") {
		t.Fatal(h.m.View().Content)
	}
	const report = "http://100.64.0.1:7358/home-network/1/report.html"
	h.be.artifacts = api.ArtifactList{Files: []api.Artifact{
		{Name: "report.html", URL: report},
		{Name: "old.html", URL: "http://100.64.0.1:7358/home-network/1/old.html"},
	}}
	h.press("M-v")
	v := h.m.View().Content
	for _, want := range []string{"artifacts · [1]", "report.html", "old.html"} {
		if !strings.Contains(v, want) {
			t.Fatalf("no %q in\n%s", want, v)
		}
	}
	h.press("Enter")
	if fmt.Sprint(browsed) != "["+report+"]" {
		t.Fatalf("browsed = %v", browsed)
	}
	if !strings.Contains(h.m.View().Content, report) {
		t.Fatal(h.m.View().Content)
	}
	if got := fmt.Sprint(h.be.reads); got != "[artifacts home-network 1 artifacts home-network 1]" {
		t.Fatalf("reads = %s", got)
	}
}

func TestArtifactsWithFileServerOff(t *testing.T) {
	h := start(t, 120, 30, "idle")
	h.m.browse = func(string) tea.Cmd { t.Fatal("browsed with no link"); return nil }
	h.be.artifacts = api.ArtifactList{Files: []api.Artifact{{Name: "report.html"}}}
	h.press("M-v", "Enter")
	if !strings.Contains(h.m.View().Content, "files_listen") {
		t.Fatal(h.m.View().Content)
	}
}

func (f *fakeBackend) MCP(context.Context) (api.MCP, error) { return f.mcp, nil }

func (f *fakeBackend) GitAccounts(context.Context) (api.GitAccounts, error) { return f.acc, nil }

func (f *fakeBackend) SaveGitAccounts(_ context.Context, c accounts.Config) (api.GitAccounts, error) {
	if err := f.accErr; err != nil {
		f.accErr = nil
		return api.GitAccounts{}, err
	}
	f.accSaves = append(f.accSaves, c)
	f.acc.Config = c
	return f.acc, nil
}

func (f *fakeBackend) MCPDoc(_ context.Context, scope string) (api.MCPDoc, error) {
	return api.MCPDoc{Text: f.mcpDocs[scope]}, nil
}

func (f *fakeBackend) SaveMCPDoc(_ context.Context, scope, text string) (api.Result, error) {
	f.mcpSaves = append(f.mcpSaves, scope+"="+text)
	if f.mcpErr != nil {
		return *f.mcpErr, &StatusError{Code: 400, Text: f.mcpErr.Error}
	}
	return api.Result{}, nil
}

func TestSyncFlashesAndAsksWhileClaudeWorks(t *testing.T) {
	h := start(t, 120, 30, "running")
	h.press("M-u") // on a session row: nothing
	if len(h.be.actions) != 0 {
		t.Fatalf("U on a session synced: %q", h.be.actions)
	}
	h.be.syncRes = api.Result{Running: true, Num: 1}
	h.be.syncErr = errors.New("Claude is working in s1 (session [1])")
	h.press("k", "M-u")
	if v := view(h); !strings.Contains(v, "Sync s1 while Claude works in it?") || !strings.Contains(v, "[1] Add lease time is running.") {
		t.Fatal(h.m.View().Content)
	}
	h.be.syncRes = api.Result{Message: "s1 synced · 3 new commits from origin"}
	h.press("y")
	if got := strings.Join(h.be.actions, "|"); got != "home-network sync s1|home-network sync s1 force" {
		t.Fatalf("actions = %q", got)
	}
	if h.m.flash != "s1 synced · 3 new commits from origin" || h.m.busy != "" {
		t.Fatalf("flash = %q busy = %q", h.m.flash, h.m.busy)
	}
}

func TestPageStripOpensFromEitherPane(t *testing.T) {
	h := start(t, 120, 30, "idle")
	var browsed []string
	h.m.browse = func(u string) tea.Cmd { browsed = append(browsed, u); return nil }
	h.press("Enter") // session 1 open, typing in Claude
	c := h.conns[0]
	const u = "http://100.64.0.1:7358/home-network/1/report.html"
	h.update(pageMsg{conn: &fakeConn{}, page: api.Artifact{Name: "old.html", URL: u}}) // from an old stream
	if h.m.page != nil {
		t.Fatal("a stale page frame must be ignored")
	}
	h.update(pageMsg{conn: c, page: api.Artifact{Name: "report.html", URL: u}, isNew: true})
	v := ansi.Strip(h.m.View().Content)
	if !strings.Contains(v, "▤ New page  report.html") || !strings.Contains(v, "Open M-v") || !strings.Contains(ansi.Strip(h.m.bar()), "M-v open page") {
		t.Fatalf("no strip or key:\n%s", v)
	}
	h.update(pageMsg{conn: c, page: api.Artifact{Name: "report.html", URL: u}})
	if v := ansi.Strip(h.m.View().Content); !strings.Contains(v, "▤ Updated  report.html") {
		t.Fatalf("an edit says Updated:\n%s", v)
	}
	h.update(tea.KeyPressMsg{Code: 'v', Mod: tea.ModAlt}) // while focused on Claude: not sent to it
	select {
	case b := <-h.m.term.Input():
		t.Fatalf("M-v reached Claude: %q", b)
	default:
	}
	if len(browsed) != 1 || browsed[0] != u || c.seen != 1 || h.m.page != nil {
		t.Fatalf("browsed %q seen %d page %v", browsed, c.seen, h.m.page)
	}
	if v := ansi.Strip(h.m.View().Content); strings.Contains(v, "report.html ━") || strings.Contains(ansi.Strip(h.m.bar()), "open page") {
		t.Fatalf("strip left after opening:\n%s", v)
	}

	// from the list too; a page_seen from another client closes it
	h.press("C-q")
	h.update(pageMsg{conn: c, page: api.Artifact{Name: "report.html", URL: u}})
	h.update(pageSeenMsg{conn: c})
	if h.m.page != nil || c.seen != 1 {
		t.Fatalf("page_seen from the server: page %v seen %d", h.m.page, c.seen)
	}
	h.update(pageMsg{conn: c, page: api.Artifact{Name: "report.html", URL: u}})
	h.update(tea.KeyPressMsg{Code: 'v', Mod: tea.ModAlt})
	if len(browsed) != 2 || c.seen != 2 {
		t.Fatalf("from the list: browsed %q seen %d", browsed, c.seen)
	}
}

func TestPageStripClicks(t *testing.T) {
	h := start(t, 120, 30, "idle")
	var browsed []string
	h.m.browse = func(u string) tea.Cmd { browsed = append(browsed, u); return nil }
	h.press("Enter")
	c := h.conns[0]
	page := pageMsg{conn: c, page: api.Artifact{Name: "report.html", URL: "http://h/report.html"}, isNew: true}
	h.update(page)
	_, closeAt := h.m.pageStrip("━", focusColor)
	off := ui.ListWidth + 1
	h.update(tea.MouseClickMsg{X: off + 3, Y: 0, Button: tea.MouseLeft}) // the title line: nothing
	if h.m.page == nil {
		t.Fatal("a click off the strip")
	}
	h.update(tea.MouseClickMsg{X: off + closeAt, Y: paneHeader - 1, Button: tea.MouseLeft}) // ✕
	if h.m.page != nil || len(browsed) != 0 || c.seen != 1 {
		t.Fatalf("✕: page %v browsed %q seen %d", h.m.page, browsed, c.seen)
	}
	h.update(page)
	h.update(tea.MouseClickMsg{X: off + 6, Y: paneHeader - 1, Button: tea.MouseLeft}) // the name
	if h.m.page != nil || len(browsed) != 1 || c.seen != 2 {
		t.Fatalf("open: page %v browsed %q seen %d", h.m.page, browsed, c.seen)
	}
	// the strip fits the pane at every width, and says so when there is no link
	h.update(pageMsg{conn: c, page: api.Artifact{Name: strings.Repeat("long-", 30) + ".html"}})
	for _, w := range []int{120, 80, 52} {
		h.update(tea.WindowSizeMsg{Width: w, Height: 30})
		line, _ := h.m.pageStrip("━", focusColor)
		if got := ansi.StringWidth(line); got != h.m.term.Cols() {
			t.Errorf("width %d: strip %d cols, pane %d", w, got, h.m.term.Cols())
		}
		if !strings.Contains(ansi.Strip(line), "file server off") {
			t.Errorf("width %d: %q", w, ansi.Strip(line))
		}
	}
}

// withProjects adds repo projects api, web and pay, and the workspace shop, to the project list.
func withProjects(h *harness) {
	h.m.projects = append(h.m.projects,
		api.ProjectSummary{Name: "api"}, api.ProjectSummary{Name: "web"}, api.ProjectSummary{Name: "pay"},
		api.ProjectSummary{Name: "shop", Kind: state.KindWorkspace})
}

func TestNewWorkspaceFlow(t *testing.T) {
	h := start(t, 120, 30, "idle", "idle")
	withProjects(h)
	h.press("M-o")
	h.typeText("workspace") // "work" alone also matches home-network
	h.press("Enter")
	h.typeText("ws")
	h.press("Enter")
	v := ansi.Strip(h.m.View().Content)
	if h.m.picker == nil || !strings.Contains(v, "link repositories into ws") || strings.Contains(v, "shop") {
		t.Fatalf("checklist:\n%s", v)
	}
	h.press(" ")            // api
	h.press("Down", "Down") // home-network, lab
	h.press("Down", "Down") // pay, web
	h.press(" ", "Enter")
	want := api.NewProject{Kind: "workspace", Name: "ws", Projects: []string{"api", "web"}}
	if n := len(h.be.created); n != 1 || !reflect.DeepEqual(h.be.created[0], want) {
		t.Fatalf("%+v", h.be.created)
	}
}

func TestNewWorkspaceNeedsTwo(t *testing.T) {
	h := start(t, 120, 30, "idle", "idle")
	withProjects(h)
	h.press("M-o")
	h.typeText("workspace")
	h.press("Enter")
	h.typeText("ws")
	h.press("Enter")
	h.press(" ", "Enter") // only api
	if h.m.picker == nil || !strings.Contains(h.m.flash, "pick at least 2") || len(h.be.created) != 0 {
		t.Fatalf("flash=%q created=%+v", h.m.flash, h.be.created)
	}
}

func TestNewWorkspaceNeedsTwoRepoProjects(t *testing.T) {
	h := start(t, 120, 30, "idle") // one repo project
	h.press("M-o")
	h.typeText("workspace")
	h.press("Enter")
	h.typeText("ws")
	h.press("Enter")
	if h.m.picker != nil || !strings.Contains(h.m.flash, "need at least 2 repo projects") {
		t.Fatalf("flash=%q", h.m.flash)
	}
}

func TestEditWorkspaceFlow(t *testing.T) {
	h := start(t, 120, 30, "idle", "idle")
	workspace(h) // home-network is a workspace
	withProjects(h)
	h.be.links = []api.Link{{Name: "api"}, {Name: "web", Missing: true}}
	h.press("M-e")
	v := ansi.Strip(h.m.View().Content)
	if h.m.picker == nil || !strings.Contains(v, "repositories linked into home-network") ||
		!strings.Contains(v, "[x] api") || !strings.Contains(v, "[x] web (missing)") || !strings.Contains(v, "[ ] pay") {
		t.Fatalf("checklist:\n%s", v)
	}
	// api, lab, pay, web: tick pay, untick web
	h.press("Down", "Down", " ", "Down", " ", "Enter")
	if got := h.be.actions[len(h.be.actions)-1]; got != "home-network links +pay -web" {
		t.Fatalf("actions = %q", h.be.actions)
	}
}

func TestEditWorkspaceNoChange(t *testing.T) {
	h := start(t, 120, 30, "idle", "idle")
	workspace(h)
	withProjects(h)
	h.be.links = []api.Link{{Name: "api"}}
	h.press("M-e", "Enter")
	if h.m.flash != "no change" || len(h.be.actions) != 0 {
		t.Fatalf("flash=%q actions=%q", h.m.flash, h.be.actions)
	}
}

func TestEditWorkspaceNotAWorkspace(t *testing.T) {
	h := start(t, 120, 30, "idle")
	h.press("M-e")
	if h.m.picker != nil || !strings.Contains(h.m.flash, "home-network is not a workspace") {
		t.Fatalf("flash=%q", h.m.flash)
	}
}

// reachedClaude reports whether a key went to the Claude pane's terminal. Its input is read
// on a goroutine: a key sent gets a moment to arrive.
func reachedClaude(h *harness) bool {
	select {
	case <-h.m.term.Input():
		return true
	case <-time.After(100 * time.Millisecond):
		return false
	}
}

func TestAltListKeysWorkFromClaude(t *testing.T) {
	h := start(t, 120, 30, "idle")
	h.press("Enter") // session 1 open, typing in Claude
	if !h.m.claudeFocused {
		t.Fatal("not in Claude")
	}
	h.press("M-r") // renames the session in the pane
	if !h.m.prompt.Active() || h.m.prompt.Kind != ui.PromptRename || reachedClaude(h) {
		t.Fatalf("M-r: prompt %+v", h.m.prompt)
	}
	h.press("Esc")
	h.press("M-d") // deletes it, not its story: the pane holds a session
	if v := view(h); !strings.Contains(v, "Delete [1]?") || reachedClaude(h) {
		t.Fatalf("M-d:\n%s", v)
	}
	h.press("Esc")
	if !h.m.claudeFocused {
		t.Fatal("the keys left Claude")
	}
	h.press("M-f") // focus mode on and off, staying in Claude
	if !h.m.focusMode || !h.m.claudeFocused {
		t.Fatalf("M-f: focus %v claude %v", h.m.focusMode, h.m.claudeFocused)
	}
	h.press("M-f")
	if h.m.focusMode {
		t.Fatal("M-f again leaves focus mode")
	}
	h.press("M-z") // folding changes the list: the keys go there
	if h.m.claudeFocused {
		t.Fatal("M-z stayed in Claude")
	}
}

func TestClaudeKeepsKeysTheListDoesNotBind(t *testing.T) {
	h := start(t, 120, 30, "idle")
	h.press("Enter")
	h.press("?") // plain keys are typing, bound in the list or not
	if !reachedClaude(h) || h.m.showKeys {
		t.Fatal("? did not reach Claude")
	}
	h.press("M-t") // new shell
	if reachedClaude(h) || !h.m.prompt.Active() {
		t.Fatal("bound M-t reached Claude")
	}
	h.press("Esc")
	h.m.cfg.Keys.List["shell"] = nil // unbound: M-t is Claude's again (its thinking toggle)
	h.press("M-t")
	if !reachedClaude(h) || h.m.prompt.Active() {
		t.Fatal("unbound M-t did not reach Claude")
	}
}
