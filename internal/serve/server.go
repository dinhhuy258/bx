package serve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/dinhhuy258/bx/internal/api"
	"github.com/dinhhuy258/bx/internal/app"
	"github.com/dinhhuy258/bx/internal/claude"
	"github.com/dinhhuy258/bx/internal/config"
	"github.com/dinhhuy258/bx/internal/settings"
	"github.com/dinhhuy258/bx/internal/state"
	"github.com/dinhhuy258/bx/internal/tmux"
	"github.com/dinhhuy258/bx/internal/ui"
	"github.com/dinhhuy258/bx/internal/web"
)

// Server serves the API for every project under the bx root. It is the only writer of the
// state files; write serializes every change.
type Server struct {
	App    *app.App
	Cfg    *config.Config
	Lister claude.Lister
	Log    func(format string, args ...any)

	write sync.Mutex

	mu       sync.Mutex
	agents   []claude.Agent // the last good `claude agents`, shared by every poll for 1 s
	agentsAt time.Time
	diffs    map[string]diffCache // by project, refreshed every 10 s
	streams  map[*stream]bool     // see stream.go
	closing  bool
	seen     map[string]map[int]bool // by project, the sessions in tmux at the last watch
	started  map[string]map[int]bool // by project, the sessions bx serve started since the last watch
	unread   unread                  // the sessions that finished a turn nobody watched; see unread.go
	pages    pages                   // the pages Claude wrote, by session; see pages.go

	sending    map[string]bool               // "project/story" of a review send in progress
	explaining map[string]context.CancelFunc // by explanation id, the ones running; see explain.go

	prepared func() // tests: called by newStory between PrepareStory and taking write; nil in production
}

// WatchEvery is how often Watch looks at tmux.
const WatchEvery = 2 * time.Second

type diffCache struct {
	at   time.Time
	diff map[string]ui.Diff
}

// New builds a Server.
func New(a *app.App, cfg *config.Config, l claude.Lister, log func(string, ...any)) *Server {
	return &Server{App: a, Cfg: cfg, Lister: l, Log: log, diffs: map[string]diffCache{}, streams: map[*stream]bool{},
		seen: map[string]map[int]bool{}, started: map[string]map[int]bool{}, unread: newUnread(), sending: map[string]bool{}, explaining: map[string]context.CancelFunc{}, pages: newPages()}
}

// Handler routes the API.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects", s.listProjects)
	mux.HandleFunc("POST /api/projects", s.createProject)
	mux.HandleFunc("GET /api/github/repos", s.githubRepos)
	mux.HandleFunc("GET /api/settings", s.getSettings)
	mux.HandleFunc("PUT /api/settings", s.putSettings)
	mux.HandleFunc("GET /api/git/accounts", s.getAccounts)
	mux.HandleFunc("PUT /api/git/accounts", s.putAccounts)
	mux.HandleFunc("GET /api/mcp", s.getMCP)
	mux.HandleFunc("GET /api/mcp/doc", s.getMCPDoc)
	mux.HandleFunc("PUT /api/mcp/doc", s.putMCPDoc)
	mux.HandleFunc("GET /api/projects/{name}", s.getProject)
	mux.HandleFunc("GET /api/projects/{name}/sessions/{num}/stream", s.stream)
	mux.HandleFunc("GET /api/projects/{name}/shells/stream", s.shellStream)
	mux.HandleFunc("POST /api/projects/{name}/shells", s.actionResult(func(p *state.Project, r api.Action) (api.Result, error) {
		k, err := s.App.NewShell(p, r.Story)
		if err == nil {
			s.Log("shell %s %s$%d: open", p.Name, group(r.Story), k)
		}
		return api.Result{Story: r.Story, K: k}, err
	}))
	mux.HandleFunc("POST /api/projects/{name}/shells/login", s.actionResult(func(p *state.Project, r api.Action) (api.Result, error) {
		k, err := s.App.LoginShell(p)
		if err == nil {
			s.Log("shell %s %s$%d: gh auth login", p.Name, group(""), k)
		}
		return api.Result{K: k}, err
	}))
	mux.HandleFunc("POST /api/projects/{name}/shells/close", s.action(func(p *state.Project, r api.Action) (int, error) {
		err := s.App.CloseShell(p, r.Story, r.K)
		if err == nil {
			s.Log("shell %s %s$%d: close", p.Name, group(r.Story), r.K)
		}
		return 0, err
	}))
	mux.HandleFunc("POST /api/projects/{name}/shells/rename", s.action(func(p *state.Project, r api.Action) (int, error) {
		return 0, s.App.RenameShell(p, r.Story, r.K, r.Title)
	}))
	mux.HandleFunc("POST /api/projects/{name}/sessions/{num}/files", s.uploadFile)
	mux.HandleFunc("GET /api/projects/{name}/sessions/{num}/artifacts", s.listArtifacts)
	mux.HandleFunc("POST /api/hooks/page", s.pageHook)
	mux.HandleFunc("GET /api/projects/{name}/sessions/{num}/slash-commands", s.listSlashCommands)
	mux.HandleFunc("POST /api/projects/{name}/sessions", s.action(func(p *state.Project, r api.Action) (int, error) {
		var num int
		var err error
		if r.Story == "" {
			num, err = s.App.NewChat(p, r.Title)
		} else {
			num, err = s.App.NewSession(p, r.Story, r.Title)
		}
		if err == nil {
			s.markStarted(p.Name, num)
		}
		return num, err
	}))
	mux.HandleFunc("POST /api/projects/{name}/stories", s.newStory)
	mux.HandleFunc("GET /api/projects/{name}/storyinfo", s.storyInfo)
	mux.HandleFunc("GET /api/projects/{name}/branches", s.branches)
	mux.HandleFunc("GET /api/projects/{name}/prs", s.pullRequests)
	mux.HandleFunc("POST /api/projects/{name}/base", s.action(func(p *state.Project, r api.Action) (int, error) {
		return 0, s.App.SetBaseOverride(p, r.Base)
	}))
	mux.HandleFunc("POST /api/projects/{name}/stories/repos", s.action(func(p *state.Project, r api.Action) (int, error) {
		return 0, s.App.AddStoryRepos(p, r.Name, r.Repos, r.Force)
	}))
	mux.HandleFunc("POST /api/projects/{name}/sync", s.syncStory)
	mux.HandleFunc("GET /api/projects/{name}/repos", s.workspaceRepos)
	mux.HandleFunc("GET /api/projects/{name}/links", s.workspaceLinks)
	mux.HandleFunc("POST /api/projects/{name}/links", s.action(func(p *state.Project, r api.Action) (int, error) {
		return 0, s.App.EditWorkspace(p, r.Link, r.Unlink)
	}))
	mux.HandleFunc("POST /api/projects/{name}/stories/close", s.actionResult(func(p *state.Project, r api.Action) (api.Result, error) {
		msg, err := s.App.CloseStory(p, r.Name, r.Force)
		return api.Result{Message: msg}, err
	}))
	mux.HandleFunc("POST /api/projects/{name}/resume", s.action(func(p *state.Project, r api.Action) (int, error) {
		err := s.App.Resume(p, r.Num)
		if err == nil {
			s.markStarted(p.Name, r.Num)
		}
		return 0, err
	}))
	mux.HandleFunc("POST /api/projects/{name}/rename", s.action(func(p *state.Project, r api.Action) (int, error) {
		return 0, s.App.Rename(p, r.Num, r.Title)
	}))
	mux.HandleFunc("POST /api/projects/{name}/stop", s.action(func(p *state.Project, r api.Action) (int, error) {
		return 0, s.App.Stop(p, r.Num)
	}))
	mux.HandleFunc("POST /api/projects/{name}/delete", s.action(func(p *state.Project, r api.Action) (int, error) {
		return 0, s.App.DeleteSession(p, r.Num, r.Force)
	}))
	mux.HandleFunc("GET /api/cleanup", s.cleanupScan)
	mux.HandleFunc("POST /api/cleanup", s.cleanupApply)
	mux.HandleFunc("POST /api/claude/sync/plan", s.claudeSyncPlan)
	mux.HandleFunc("POST /api/claude/sync", s.claudeSyncApply)
	s.reviewRoutes(mux)
	mux.Handle("GET /", web.Handler()) // the mobile web client; every /api route is more specific
	return sameOrigin(mux)
}

func (s *Server) stateDir() string { return filepath.Join(s.Cfg.Root, "state") }

// ResumeAll restarts, after a reboot, every session that should be running and is not.
// When `claude agents` fails nothing is resumed: a session alive elsewhere would start twice.
func (s *Server) ResumeAll(ctx context.Context) {
	agents, err := s.Lister.Agents(ctx)
	if err != nil {
		s.Log("resume: claude agents: %v · not resuming", err)
		return
	}
	idx := claude.IndexByUUID(agents)
	names, _ := state.ListProjects(s.stateDir())
	s.write.Lock()
	defer s.write.Unlock()
	for _, name := range names {
		p, err := s.App.LoadProject(name)
		if err != nil {
			s.Log("resume: %s: %v", name, err)
			continue
		}
		nums, err := s.App.ResumeAll(p, idx)
		if err != nil {
			s.Log("resume: %s: %v", name, err)
		}
		if len(nums) > 0 {
			s.markStarted(name, nums...)
			s.Log("resume: %s: %d sessions", name, len(nums))
		}
	}
}

// KeepTmuxServer sets exit-empty off on a tmux server that is already running, so it outlives
// its last session even when bx serve starts none. With no server there is nothing to keep:
// the first new session sets it.
func (s *Server) KeepTmuxServer() {
	if err := s.App.Tmux.KeepServer(); err != nil && !tmux.IsNoServer(err) {
		s.Log("tmux: keep server: %v", err)
	}
}

// Watch calls watchOnce every interval until ctx is done. A session whose tmux session ends
// while bx serve watches (Claude exited: /exit, Ctrl-D, a crash) becomes done, so ResumeAll
// leaves it alone. One lost with the tmux server (a reboot) or while bx serve was down stays
// gone, and ResumeAll restarts it.
func (s *Server) Watch(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.watchOnce()
		}
	}
}

// markStarted records sessions bx serve just started or resumed, so watchOnce treats them as
// seen even if Claude exits before the next tick.
func (s *Server) markStarted(name string, nums ...int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started[name] == nil {
		s.started[name] = map[int]bool{}
	}
	for _, n := range nums {
		s.started[name][n] = true
	}
}

// watchOnce marks done the sessions that were in tmux at the last call, or that bx serve
// started since, and no longer are, while the tmux server still answers. With no tmux server
// it forgets what it saw, so sessions lost with the server are never marked done once a new
// one starts. It re-checks under the write lock: a session resumed since the list stays as it
// is.
func (s *Server) watchOnce() {
	// taken before the list, so every session in it was started before the list
	s.mu.Lock()
	started := s.started
	s.started = map[string]map[int]bool{}
	s.mu.Unlock()
	// keeps unread up to date while no client polls, so a turn that ends with every screen
	// closed is still caught
	if _, err := s.liveAgents(); err != nil {
		s.Log("watch: claude agents: %v", err)
	}
	ss, up, err := s.App.Tmux.List()
	if err != nil {
		s.Log("watch: tmux: %v", err)
		s.keepStarted(started)
		return
	}
	if !up {
		s.mu.Lock()
		s.seen = map[string]map[int]bool{}
		s.mu.Unlock()
		return
	}
	listed := make(map[string]bool, len(ss))
	for _, se := range ss {
		listed[se.Name] = true
	}
	names, err := state.ListProjects(s.stateDir())
	if err != nil {
		s.Log("watch: %v", err)
		s.keepStarted(started)
		return
	}
	for _, name := range names {
		p, err := s.App.LoadProject(name)
		if err != nil {
			s.Log("watch: %s: %v", name, err)
			s.keepStarted(map[string]map[int]bool{name: started[name]})
			continue
		}
		now := map[int]bool{}
		for n := range p.Sessions {
			if listed[app.TmuxName(p.Name, n)] {
				now[n] = true
			}
		}
		before := map[int]bool{}
		s.mu.Lock()
		for n := range s.seen[name] {
			before[n] = true
		}
		s.mu.Unlock()
		for n := range started[name] {
			before[n] = true
		}
		var exited []int
		for n := range before {
			if se := p.Sessions[n]; !now[n] && se != nil && !se.Done {
				exited = append(exited, n)
			}
		}
		if len(exited) > 0 {
			sort.Ints(exited)
			s.markExited(name, exited)
		}
		s.mu.Lock()
		s.seen[name] = now
		s.mu.Unlock()
	}
}

// keepStarted puts back the started sessions a watch could not check, for the next one.
func (s *Server) keepStarted(started map[string]map[int]bool) {
	for name, nums := range started {
		for n := range nums {
			s.markStarted(name, n)
		}
	}
}

// markExited marks done the exited sessions that are still not in tmux, under the write lock.
func (s *Server) markExited(name string, exited []int) {
	s.write.Lock()
	defer s.write.Unlock()
	fresh, err := s.load(name)
	if err != nil {
		s.Log("watch: %s: %v", name, err)
		return
	}
	live, err := s.App.Live(fresh)
	if err != nil {
		s.Log("watch: %s: %v", name, err)
		return
	}
	var rest []int
	for _, n := range exited {
		if _, again := live[n]; !again {
			rest = append(rest, n)
		}
	}
	changed, err := s.App.MarkDone(fresh, rest)
	if err != nil {
		s.Log("watch: %s: %v", name, err)
		return
	}
	if changed {
		s.Log("watch: %s: %v exited · done", name, rest)
	}
}

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	names, err := state.ListProjects(s.stateDir())
	if err != nil {
		httpError(w, 500, err)
		return
	}
	sort.Strings(names)
	live := ui.Live{Now: time.Now()}
	agents, err := s.liveAgents()
	live.AgentsErr = err != nil
	if agents != nil { // on an error, the last list
		live.Agents = claude.IndexByUUID(agents)
	}
	live.Unread = s.unreadSnapshot()
	out := []api.ProjectSummary{}
	for _, name := range names {
		p, err := s.App.LoadProject(name)
		if err != nil {
			s.Log("projects: %s: %v", name, err)
			continue
		}
		m := ui.NewModel(p)
		m.SetLive(live)
		sum := api.ProjectSummary{Name: name, BaseOverride: p.BaseOverride, Kind: p.Kind}
		sum.Running, sum.Waiting, sum.Unread = m.Counts()
		for _, n := range sortedNums(p) {
			if !p.Sessions[n].Done && m.State(n) == claude.Waiting {
				sum.WaitingSessions = append(sum.WaitingSessions, n)
			}
		}
		out = append(out, sum)
	}
	writeJSON(w, out)
}

// loadAll loads every project, or fails if any fails to load. Cleanup needs them all: the
// live sessions of a project left out would be listed as orphans and killed.
func (s *Server) loadAll() ([]*state.Project, error) {
	names, err := state.ListProjects(s.stateDir())
	if err != nil {
		return nil, err
	}
	var ps []*state.Project
	for _, name := range names {
		p, err := s.App.LoadProject(name)
		if err != nil {
			return nil, fmt.Errorf("project %s: %w", name, err)
		}
		ps = append(ps, p)
	}
	return ps, nil
}

// cleanupScan lists what cleanup can remove. Cleanup is a top-level route, not a project
// action, because it covers every project; it holds write for the whole scan.
func (s *Server) cleanupScan(w http.ResponseWriter, r *http.Request) {
	s.write.Lock()
	defer s.write.Unlock()
	ps, err := s.loadAll()
	if err != nil {
		httpError(w, 500, err)
		return
	}
	items, err := s.App.CleanupScan(ps)
	if err != nil {
		httpError(w, 500, err)
		return
	}
	out := api.Cleanup{Items: make([]api.CleanupItem, 0, len(items))}
	for _, it := range items {
		out.Items = append(out.Items, api.CleanupItem{Kind: it.Kind, ID: it.ID, Label: it.Label, Detail: it.Detail})
	}
	writeJSON(w, out)
}

// cleanupApply removes the ticked items still found by a fresh scan, holding write
// throughout, across every project.
func (s *Server) cleanupApply(w http.ResponseWriter, r *http.Request) {
	var req api.CleanupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, 400, fmt.Errorf("decode body: %w", err))
		return
	}
	s.write.Lock()
	defer s.write.Unlock()
	ps, err := s.loadAll()
	if err != nil {
		httpError(w, 500, err)
		return
	}
	n, err := s.App.CleanupApply(ps, req.IDs)
	s.Log("cleanup: removed %d", n)
	if err != nil {
		httpError(w, 500, err)
		return
	}
	writeJSON(w, api.CleanupResult{Removed: n})
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	var req api.NewProject
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, 400, fmt.Errorf("decode body: %w", err))
		return
	}
	s.write.Lock()
	defer s.write.Unlock()
	var name string
	var err error
	switch req.Kind {
	case "folder":
		name, err = s.App.AddFolder(req.Path)
	case "clone":
		name, err = s.App.CloneProject(req.Repo)
	case "new":
		name, err = s.App.NewProject(req.Name)
	case "workspace":
		name, err = s.App.NewWorkspace(req.Name, req.Projects)
	default:
		err = fmt.Errorf("unknown kind %q", req.Kind)
	}
	if err != nil {
		httpError(w, 400, err)
		return
	}
	s.Log("projects: added %s (%s)", name, req.Kind)
	writeJSON(w, api.Result{Name: name})
}

func (s *Server) githubRepos(w http.ResponseWriter, r *http.Request) {
	repos, err := s.App.GitHubRepos()
	if err != nil {
		httpError(w, http.StatusBadGateway, err)
		return
	}
	if repos == nil {
		repos = []string{}
	}
	writeJSON(w, repos)
}

// getSettings sends the sections and values. Only the TUI asks with ?tui=1; the web client
// never gets the TUI-only fields (the notification URLs: the ntfy URL holds a secret topic).
func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	secs, values := settings.Visible(r.URL.Query().Get("tui") == "1", s.App.Settings.Values())
	writeJSON(w, api.Settings{Sections: secs, Values: values})
}

// loggable is values with every text value written as <set> or <cleared>: a URL may hold a secret.
func loggable(values map[string]string) map[string]string {
	out := map[string]string{}
	for _, sec := range settings.Sections {
		for _, f := range sec.Fields {
			v, ok := values[f.Key]
			switch {
			case !ok:
			case f.Kind == settings.Text && v == "":
				out[f.Key] = "<cleared>"
			case f.Kind == settings.Text:
				out[f.Key] = "<set>"
			default:
				out[f.Key] = v
			}
		}
	}
	return out
}

// putSettings saves the changed values, all or none. The next new session uses them. Only the
// TUI (?tui=1) may change the TUI-only keys, so the web client cannot replace the ntfy secret;
// it may still turn notifications on and off.
func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var req api.Settings
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, 400, fmt.Errorf("decode body: %w", err))
		return
	}
	if r.URL.Query().Get("tui") != "1" {
		keys := make([]string, 0, len(req.Values))
		for k := range req.Values {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if settings.TUIOnlyKey(k) {
				httpError(w, 400, fmt.Errorf("%s can only be changed from the TUI", k))
				return
			}
		}
	}
	if s.App.Settings == nil {
		httpError(w, 500, errors.New("settings are not loaded"))
		return
	}
	if err := s.App.Settings.Set(req.Values); err != nil {
		httpError(w, 400, err)
		return
	}
	s.Log("settings: saved %v", loggable(req.Values))
	s.getSettings(w, r)
}

func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	p, err := s.load(r.PathValue("name"))
	if err != nil {
		httpError(w, statusOf(err), err)
		return
	}
	live := s.collect(p)
	if titles := s.App.Titles(p, live.Agents); len(titles) > 0 {
		p = s.setTitles(p, titles)
	}
	tm, err := s.App.Live(p)
	if err != nil {
		s.Log("tmux sessions: %v", err)
	}
	states := sessionStates(p, live, tm, err == nil)
	shells := []api.Shell{}
	if all, err := s.App.Shells(p); err != nil {
		s.Log("tmux shells: %v", err)
	} else {
		for _, sh := range all {
			shells = append(shells, api.Shell{Story: sh.Story, K: sh.K, Title: sh.Title, Command: sh.Command})
		}
	}
	dirs := map[string]string{"": s.App.Dir(p, "")}
	for name := range p.Stories {
		dirs[name] = s.App.Dir(p, name)
	}
	p.MCPServers = nil // the web client reads the snapshot; server configs may hold tokens
	writeJSON(w, api.Snapshot{Project: p, Live: live, SessionStates: states, Shells: shells, Dirs: dirs})
}

// sessionStates derives every session's state with the rules the TUI uses (ui.Model), so the
// web client does not carry its own copy. With the bx tmux sessions known (tmuxOK, tm from
// App.Live) two cases differ, because the client attaches to tmux: a session without a tmux
// session is gone even if `claude agents` lists it (a stream can't attach), and one with a
// tmux session that `claude agents` doesn't list yet is starting, not gone.
func sessionStates(p *state.Project, live ui.Live, tm map[int]string, tmuxOK bool) map[int]api.SessionState {
	m := ui.NewModel(p)
	m.SetLive(live)
	out := make(map[int]api.SessionState, len(p.Sessions))
	for n, s := range p.Sessions {
		_, inTmux := tm[n]
		st := "idle"
		switch {
		case s.Done:
			st = "done"
		case tmuxOK && !inTmux:
			out[n] = api.SessionState{State: "gone", Text: "gone · ⏎ to resume"}
			continue
		case tmuxOK && m.Gone(n):
			out[n] = api.SessionState{State: "starting", Text: "starting"}
			continue
		case m.Gone(n):
			st = "gone"
		case m.State(n) == claude.Waiting:
			st = "waiting"
		case m.State(n) == claude.Running:
			st = "running"
		}
		out[n] = api.SessionState{State: st, Text: m.StateText(n), Unread: m.Unread(n)}
	}
	return out
}

// setTitles names unnamed sessions after their first prompt. It reloads the project under the
// write lock, so a change since the load is kept; on an error the old project is shown.
func (s *Server) setTitles(p *state.Project, titles map[int]string) *state.Project {
	s.write.Lock()
	defer s.write.Unlock()
	fresh, err := s.load(p.Name)
	if err != nil {
		s.Log("titles: %s: %v", p.Name, err)
		return p
	}
	if _, err := s.App.SetTitles(fresh, titles); err != nil {
		s.Log("titles: %s: %v", p.Name, err)
		return p
	}
	return fresh
}

// workspaceRepos lists the repositories of a workspace, for the story checklists.
func (s *Server) workspaceRepos(w http.ResponseWriter, r *http.Request) {
	p, err := s.load(r.PathValue("name"))
	if err != nil {
		httpError(w, statusOf(err), err)
		return
	}
	repos, err := s.App.WorkspaceRepos(p)
	if err != nil {
		httpError(w, statusOf(err), err)
		return
	}
	if repos == nil {
		repos = []string{}
	}
	writeJSON(w, repos)
}

// workspaceLinks sends the links at the top of a workspace folder.
func (s *Server) workspaceLinks(w http.ResponseWriter, r *http.Request) {
	p, err := s.load(r.PathValue("name"))
	if err != nil {
		httpError(w, statusOf(err), err)
		return
	}
	if !p.IsWorkspace() {
		httpError(w, 400, fmt.Errorf("%s is not a workspace", p.Name))
		return
	}
	links, err := s.App.WorkspaceLinks(p)
	if err != nil {
		httpError(w, 500, err)
		return
	}
	writeJSON(w, links)
}

// newStory creates a story and starts its first session. The slow part (fetching the base,
// asking origin and gh) runs before the write lock, so it blocks no other change; the client
// going away cancels it. The story is then made under the lock, from a fresh load. A branch that
// exists is a 409 with Exists; a session that fails to start keeps the story and is reported in
// SessionError.
func (s *Server) newStory(w http.ResponseWriter, r *http.Request) {
	var req api.Action
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, 400, fmt.Errorf("decode body: %w", err))
		return
	}
	name := r.PathValue("name")
	p, err := s.load(name)
	if err != nil {
		httpError(w, statusOf(err), err)
		return
	}
	plan, err := s.App.PrepareStory(r.Context(), p, app.StoryOpts{Name: req.Name, Type: req.Type, Source: req.Source, Branch: req.Branch,
		PR: req.PR, PRRepo: req.PRRepo, Repos: req.Repos, UseExisting: req.UseExisting})
	if err != nil {
		storyError(w, err)
		return
	}
	if s.prepared != nil {
		s.prepared()
	}
	s.write.Lock()
	defer s.write.Unlock()
	if p, err = s.load(name); err != nil {
		httpError(w, statusOf(err), err)
		return
	}
	res, err := s.App.CreateStory(p, plan)
	if err != nil {
		storyError(w, err)
		return
	}
	s.Log("stories: %s: %s", name, res.Message())
	s.mu.Lock()
	delete(s.diffs, name) // the new story shows up in the next poll
	s.mu.Unlock()
	out := api.Result{Story: res.Story, Message: res.Message()}
	if num, err := s.App.NewSession(p, res.Story, ""); err != nil {
		out.SessionError = err.Error()
	} else {
		out.Num = num
		s.markStarted(name, num)
	}
	writeJSON(w, out)
}

// syncStory fast-forwards a story ("" = the project folder) to origin. Without force a story
// Claude works in is a 409 with Running and Num, its first busy session. The fetch runs before
// the write lock, so it blocks no other change; the client going away cancels it. The
// fast-forward runs under the lock, from a fresh load. Every outcome, a refusal included, is a
// 200 whose Message says what happened.
func (s *Server) syncStory(w http.ResponseWriter, r *http.Request) {
	var req api.Action
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, 400, fmt.Errorf("decode body: %w", err))
		return
	}
	name := r.PathValue("name")
	p, err := s.load(name)
	if err != nil {
		httpError(w, statusOf(err), err)
		return
	}
	if !req.Force {
		if busy := s.busySessions(p, req.Story); len(busy) > 0 {
			err := &app.BusyError{Story: req.Story, Nums: busy}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(api.Result{Error: err.Error(), Running: true, Num: busy[0]})
			return
		}
	}
	plan, err := s.App.PrepareSync(r.Context(), p, req.Story)
	if err != nil {
		httpError(w, statusOf(err), err)
		return
	}
	s.write.Lock()
	defer s.write.Unlock()
	if p, err = s.load(name); err != nil {
		httpError(w, statusOf(err), err)
		return
	}
	msg, err := s.App.ApplySync(p, plan)
	if err != nil {
		httpError(w, statusOf(err), err)
		return
	}
	s.Log("sync %s %s: %s", name, group(req.Story), msg)
	s.mu.Lock()
	delete(s.diffs, name) // the new diff and ↓N show in the next poll
	s.mu.Unlock()
	writeJSON(w, api.Result{Story: req.Story, Message: msg})
}

// busySessions lists the sessions of story ("" = chats) Claude is working in. When `claude
// agents` fails it uses the last list, like a poll.
func (s *Server) busySessions(p *state.Project, story string) []int {
	m := ui.NewModel(p)
	m.SetLive(s.collectAgents())
	return app.BusySessions(p, story, func(n int) bool { return m.State(n) == claude.Running })
}

// collectAgents is the agents part of collect, without the git work.
func (s *Server) collectAgents() ui.Live {
	l := ui.Live{}
	agents, _ := s.liveAgents()
	if agents != nil {
		l.Agents, l.Dups = claude.IndexByUUID(agents), claude.Duplicates(agents)
	}
	return l
}

// storyError replies to a refused story: a 409 with Exists for a branch that exists.
func storyError(w http.ResponseWriter, err error) {
	var exists *app.BranchExistsError
	if errors.As(err, &exists) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(api.Result{Error: err.Error(), Exists: exists.Where, Branch: exists.Branch})
		return
	}
	httpError(w, statusOf(err), err)
}

// storyInfo is the base and last type of a new story, for its prompt. It does not fetch.
func (s *Server) storyInfo(w http.ResponseWriter, r *http.Request) {
	p, err := s.load(r.PathValue("name"))
	if err != nil {
		httpError(w, statusOf(err), err)
		return
	}
	info, err := s.App.StoryInfo(r.Context(), p)
	if err != nil {
		httpError(w, statusOf(err), err)
		return
	}
	writeJSON(w, api.StoryInfo{Base: info.Base, Detected: info.Detected, Override: info.Override, LastType: info.LastType, Types: info.Types})
}

// branches lists the branches of a project for the existing branch picker; with fetch=1 it
// fetches origin first, outside the write lock.
func (s *Server) branches(w http.ResponseWriter, r *http.Request) {
	p, err := s.load(r.PathValue("name"))
	if err != nil {
		httpError(w, statusOf(err), err)
		return
	}
	bl, err := s.App.Branches(r.Context(), p, r.URL.Query().Get("fetch") == "1")
	if err != nil {
		httpError(w, statusOf(err), err)
		return
	}
	out := api.BranchList{Branches: make([]api.Branch, len(bl.Branches)), Note: bl.Note}
	for i, b := range bl.Branches {
		out.Branches[i] = api.Branch{Name: b.Name, Local: b.Local, Remote: b.Remote, CheckedOut: b.CheckedOut, Time: b.Time.Unix(), Repos: b.Repos}
	}
	writeJSON(w, out)
}

// pullRequests lists pull requests for the picker (q: a search, #12 or a PR URL). A gh failure
// is in Errors with a 200; only a bad project is an HTTP error.
func (s *Server) pullRequests(w http.ResponseWriter, r *http.Request) {
	p, err := s.load(r.PathValue("name"))
	if err != nil {
		httpError(w, statusOf(err), err)
		return
	}
	l, err := s.App.PullRequests(r.Context(), p, r.URL.Query().Get("q"))
	if err != nil {
		httpError(w, statusOf(err), err)
		return
	}
	out := api.PRList{PRs: make([]api.PR, len(l.PRs)), Errors: l.Errors, Direct: l.Direct}
	for i, pr := range l.PRs {
		out.PRs[i] = api.PR{Number: pr.Number, Title: pr.Title, Author: pr.Author, Branch: pr.Branch, Owner: pr.Owner, Repo: pr.Repo,
			Slug: pr.Slug, State: pr.State, Fork: pr.Fork}
	}
	writeJSON(w, out)
}

// collect builds the live view of a project: agents, branch and dirty count of the project
// folder (not for a workspace, which is no repository), and the diff and dirty count of every
// story worktree.
func (s *Server) collect(p *state.Project) ui.Live {
	l := ui.Live{Now: time.Now()}
	agents, err := s.liveAgents()
	if err != nil {
		l.AgentsErr = true
		s.Log("claude agents: %v", err)
	}
	if agents != nil { // on an error, the last list: states keep a " ?" (ui.Model.StateText)
		l.Agents, l.Dups = claude.IndexByUUID(agents), claude.Duplicates(agents)
	}
	if !p.IsWorkspace() {
		if n, err := s.App.Git.DirtyCount(p.Path); err == nil {
			l.DirtyFiles = n
		}
		if b, err := s.App.Git.CurrentBranch(p.Path); err == nil {
			l.Branch = b
		}
		l.Behind = s.behind(p.Path)
	}
	l.Diff = s.diff(p)
	l.Unread = s.unreadSnapshot()
	l.Review = s.reviewCounts(p)
	return l
}

// unreadSnapshot copies the unread set, for ui.Live.Unread.
func (s *Server) unreadSnapshot() map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.unread.snapshot()
}

// liveAgents runs `claude agents`, shared by every poll for 1 s. On an error it returns the
// last good list (nil before the first) with the error: a request builds a fresh ui.Model, so
// unlike the TUI it has no previous agents to keep, and every session would show as idle.
func (s *Server) liveAgents() ([]claude.Agent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.agents != nil && time.Since(s.agentsAt) < time.Second {
		return s.agents, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	agents, err := s.Lister.Agents(ctx)
	if err != nil {
		return s.agents, err
	}
	if agents == nil {
		agents = []claude.Agent{}
	}
	s.agents, s.agentsAt = agents, time.Now()
	s.unread.observe(agents)
	return agents, nil
}

func (s *Server) diff(p *state.Project) map[string]ui.Diff {
	s.mu.Lock()
	c, ok := s.diffs[p.Name]
	s.mu.Unlock()
	if ok && time.Since(c.at) < 10*time.Second {
		return c.diff
	}
	d := map[string]ui.Diff{}
	if p.IsWorkspace() {
		for name, st := range p.Stories { // summed over the story's repositories
			var sd ui.Diff
			for _, r := range st.Repos {
				sd = sd.Plus(s.storyDiff(filepath.Join(s.App.Dir(p, name), r.Rel), r.Base))
			}
			d[name] = sd
		}
	} else {
		fallback, _ := s.App.Git.LocalDefault(p.Path) // for stories made before their base was recorded
		for name, st := range p.Stories {
			base := st.Base
			if base == "" {
				base = fallback
			}
			d[name] = s.storyDiff(s.App.Dir(p, name), base)
		}
	}
	s.mu.Lock()
	s.diffs[p.Name] = diffCache{at: time.Now(), diff: d}
	s.mu.Unlock()
	return d
}

// storyDiff is the lines a worktree's branch changed since base, and its uncommitted files.
func (s *Server) storyDiff(dir, base string) ui.Diff {
	var sd ui.Diff
	if add, del, err := s.App.Git.ShortStat(dir, base); err == nil {
		sd.Add, sd.Del = add, del
	}
	if n, err := s.App.Git.DirtyCount(dir); err == nil {
		sd.Dirty = n
	}
	sd.Behind = s.behind(dir)
	return sd
}

// behind counts the commits origin/<branch> has that the branch checked out in dir lacks, as
// last fetched; 0 with no such branch on origin. It never talks to origin.
func (s *Server) behind(dir string) int {
	b, err := s.App.Git.CurrentBranch(dir)
	if err != nil || b == "HEAD" || !s.App.Git.RefExists(dir, "refs/remotes/origin/"+b) {
		return 0
	}
	_, n, err := s.App.Git.AheadBehind(dir, "HEAD", "refs/remotes/origin/"+b)
	if err != nil {
		return 0
	}
	return n
}

func sortedNums(p *state.Project) []int {
	nums := make([]int, 0, len(p.Sessions))
	for n := range p.Sessions {
		nums = append(nums, n)
	}
	sort.Ints(nums)
	return nums
}

// load reads a project; a missing state file is a 404.
func (s *Server) load(name string) (*state.Project, error) {
	if _, err := os.Stat(s.Cfg.StateFile(name)); err != nil {
		return nil, notFound{err}
	}
	return s.App.LoadProject(name)
}

type notFound struct{ error }

func statusOf(err error) int {
	var nf notFound
	switch {
	case errors.As(err, &nf):
		return http.StatusNotFound
	case errors.Is(err, app.ErrNotRunning):
		return http.StatusGone
	}
	return http.StatusBadRequest
}

// action wraps a mutation: decode the body, load the project, run, reply. A refused close of
// a dirty story is a 409 carrying the number of uncommitted files; a refused delete of a
// session that is still running is a 409 with Running set. The reply carries the number fn
// returns.
func (s *Server) action(fn func(p *state.Project, r api.Action) (int, error)) http.HandlerFunc {
	return s.actionResult(func(p *state.Project, r api.Action) (api.Result, error) {
		num, err := fn(p, r)
		return api.Result{Num: num}, err
	})
}

// actionResult is action for a mutation that builds its own reply.
func (s *Server) actionResult(fn func(p *state.Project, r api.Action) (api.Result, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req api.Action
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpError(w, 400, fmt.Errorf("decode body: %w", err))
			return
		}
		name := r.PathValue("name")
		s.write.Lock()
		defer s.write.Unlock()
		p, err := s.load(name)
		if err != nil {
			httpError(w, statusOf(err), err)
			return
		}
		res, err := fn(p, req)
		var dirty *app.DirtyError
		var running *app.RunningError
		var inUse *app.InUseError
		switch {
		case errors.As(err, &dirty):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(api.Result{Error: err.Error(), Dirty: dirty.Files, Detail: dirty.Detail})
			return
		case errors.As(err, &running):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(api.Result{Error: err.Error(), Running: true})
			return
		case errors.As(err, &inUse):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(api.Result{Error: err.Error()})
			return
		case err != nil:
			httpError(w, statusOf(err), err)
			return
		}
		s.mu.Lock()
		delete(s.diffs, name) // a new or closed story shows up in the next poll
		s.mu.Unlock()
		writeJSON(w, res)
	}
}

// group names a shell's group in the log: its story, or "chats" for the project folder.
func group(story string) string {
	if story == "" {
		return "chats"
	}
	return story
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func httpError(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(api.Result{Error: err.Error()})
}
