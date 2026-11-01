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
	"github.com/dinhhuy258/bx/internal/ui"
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
	agents   []claude.Agent // `claude agents` is shared by every poll for 1 s
	agentsAt time.Time
	diffs    map[string]diffCache // by project, refreshed every 10 s
	streams  map[*stream]bool     // see stream.go
	closing  bool
}

type diffCache struct {
	at   time.Time
	diff map[string]ui.Diff
}

// New builds a Server.
func New(a *app.App, cfg *config.Config, l claude.Lister, log func(string, ...any)) *Server {
	return &Server{App: a, Cfg: cfg, Lister: l, Log: log, diffs: map[string]diffCache{}, streams: map[*stream]bool{}}
}

// Handler routes the API.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects", s.listProjects)
	mux.HandleFunc("POST /api/projects", s.createProject)
	mux.HandleFunc("GET /api/github/repos", s.githubRepos)
	mux.HandleFunc("GET /api/settings", s.getSettings)
	mux.HandleFunc("PUT /api/settings", s.putSettings)
	mux.HandleFunc("GET /api/projects/{name}", s.getProject)
	mux.HandleFunc("GET /api/projects/{name}/sessions/{num}/stream", s.stream)
	mux.HandleFunc("POST /api/projects/{name}/sessions", s.action(func(p *state.Project, r api.Action) (int, error) {
		if r.Story == "" {
			return s.App.NewChat(p, r.Title)
		}
		return s.App.NewSession(p, r.Story, r.Title)
	}))
	mux.HandleFunc("POST /api/projects/{name}/stories", s.action(func(p *state.Project, r api.Action) (int, error) {
		return 0, s.App.NewStory(p, r.Name, r.Repos)
	}))
	mux.HandleFunc("POST /api/projects/{name}/stories/repos", s.action(func(p *state.Project, r api.Action) (int, error) {
		return 0, s.App.AddStoryRepos(p, r.Name, r.Repos, r.Force)
	}))
	mux.HandleFunc("GET /api/projects/{name}/repos", s.workspaceRepos)
	mux.HandleFunc("POST /api/projects/{name}/stories/close", s.action(func(p *state.Project, r api.Action) (int, error) {
		return 0, s.App.CloseStory(p, r.Name, r.Force)
	}))
	mux.HandleFunc("POST /api/projects/{name}/yes", s.action(func(p *state.Project, r api.Action) (int, error) {
		return 0, s.App.AnswerYes(p, r.Num)
	}))
	mux.HandleFunc("POST /api/projects/{name}/resume", s.action(func(p *state.Project, r api.Action) (int, error) {
		return 0, s.App.Resume(p, r.Num)
	}))
	mux.HandleFunc("POST /api/projects/{name}/rename", s.action(func(p *state.Project, r api.Action) (int, error) {
		return 0, s.App.Rename(p, r.Num, r.Title)
	}))
	return mux
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
		n, err := s.App.ResumeAll(p, idx)
		if err != nil {
			s.Log("resume: %s: %v", name, err)
		}
		if n > 0 {
			s.Log("resume: %s: %d sessions", name, n)
		}
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
	if agents, err := s.liveAgents(); err != nil {
		live.AgentsErr = true
	} else {
		live.Agents = claude.IndexByUUID(agents)
	}
	out := []api.ProjectSummary{}
	for _, name := range names {
		p, err := s.App.LoadProject(name)
		if err != nil {
			s.Log("projects: %s: %v", name, err)
			continue
		}
		m := ui.NewModel(p, ui.Options{})
		m.SetLive(live)
		sum := api.ProjectSummary{Name: name}
		sum.Running, sum.Waiting = m.Counts()
		for _, n := range sortedNums(p) {
			if !p.Sessions[n].Done && m.State(n) == claude.Waiting {
				sum.WaitingSessions = append(sum.WaitingSessions, n)
			}
		}
		out = append(out, sum)
	}
	writeJSON(w, out)
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

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, api.Settings{Sections: settings.Sections, Values: s.App.Settings.Values()})
}

// putSettings saves the changed values, all or none. The next new session uses them.
func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var req api.Settings
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, 400, fmt.Errorf("decode body: %w", err))
		return
	}
	if s.App.Settings == nil {
		httpError(w, 500, errors.New("settings are not loaded"))
		return
	}
	if err := s.App.Settings.Set(req.Values); err != nil {
		httpError(w, 400, err)
		return
	}
	s.Log("settings: saved %v", req.Values)
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
	writeJSON(w, api.Snapshot{Project: p, Live: live})
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

// collect builds the live view of a project: agents, branch and dirty count of the project
// folder (not for a workspace, which is no repository), and the diff and dirty count of every
// story worktree.
func (s *Server) collect(p *state.Project) ui.Live {
	l := ui.Live{Now: time.Now()}
	if agents, err := s.liveAgents(); err != nil {
		l.AgentsErr = true
		s.Log("claude agents: %v", err)
	} else {
		l.Agents, l.Dups = claude.IndexByUUID(agents), claude.Duplicates(agents)
	}
	if !p.IsWorkspace() {
		if n, err := s.App.Git.DirtyCount(p.Path); err == nil {
			l.DirtyFiles = n
		}
		if b, err := s.App.Git.CurrentBranch(p.Path); err == nil {
			l.Branch = b
		}
	}
	l.Diff = s.diff(p)
	return l
}

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
		return nil, err
	}
	if agents == nil {
		agents = []claude.Agent{}
	}
	s.agents, s.agentsAt = agents, time.Now()
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
		base, _ := s.App.Git.DefaultBranch(p.Path)
		for name := range p.Stories {
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
	return sd
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
// a dirty story is a 409 carrying the number of uncommitted files.
func (s *Server) action(fn func(p *state.Project, r api.Action) (int, error)) http.HandlerFunc {
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
		num, err := fn(p, req)
		var dirty *app.DirtyError
		switch {
		case errors.As(err, &dirty):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(api.Result{Error: err.Error(), Dirty: dirty.Files, Detail: dirty.Detail})
			return
		case err != nil:
			httpError(w, statusOf(err), err)
			return
		}
		s.mu.Lock()
		delete(s.diffs, name) // a new or closed story shows up in the next poll
		s.mu.Unlock()
		writeJSON(w, api.Result{Num: num})
	}
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
