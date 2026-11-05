package serve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"time"

	"github.com/dinhhuy258/bx/internal/api"
	"github.com/dinhhuy258/bx/internal/app"
	"github.com/dinhhuy258/bx/internal/ports"
	"github.com/dinhhuy258/bx/internal/runcfg"
	"github.com/dinhhuy258/bx/internal/state"
	"github.com/dinhhuy258/bx/internal/ui"
)

// PortsEvery is how often WatchPorts scans.
const PortsEvery = 2 * time.Second

// WatchPorts scans the ports every interval until ctx is done, then closes the forwards.
func (s *Server) WatchPorts(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			if s.Forwards != nil {
				s.Forwards.Close()
			}
			return
		case <-t.C:
			s.scanPorts()
		}
	}
}

// scanPorts reads /proc, docker ps and the tmux panes once, gives every project its ports,
// and makes the forwards match the loopback ones.
func (s *Server) scanPorts() {
	proc := s.Proc
	if proc == nil {
		proc = ports.OS
	}
	scan, err := ports.Read(proc)
	if err != nil {
		s.Log("ports: %v", err)
		return
	}
	var cs []ports.Container
	if s.App.Docker != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		out, err := s.App.Docker(ctx, "ps", "--format", "{{json .}}")
		cancel()
		if err == nil {
			cs = ports.ParseDocker([]byte(out))
		}
	}
	tp, err := s.App.Tmux.Panes()
	if err != nil {
		s.Log("ports: tmux panes: %v", err)
	}
	panes := make([]ports.Pane, len(tp))
	for i, p := range tp {
		panes[i] = ports.Pane{PID: p.PID, Session: p.Session}
	}
	parent := func(pid int) int {
		if pr, ok := scan.Procs[pid]; ok {
			return pr.PPID
		}
		return ports.Parent(proc, pid)
	}
	names, _ := state.ListProjects(s.stateDir())
	by := map[string]map[string][]ui.Port{}
	want := map[int]string{}
	for _, name := range names {
		p, err := s.App.LoadProject(name)
		if err != nil {
			continue
		}
		cfg, _ := s.App.RunConfig(p) // a broken config: no hide list, no open paths
		w := ports.Want{Project: p.Name, Dirs: map[string]string{"": p.Path},
			Shells: map[string]ports.ShellRef{}, Sessions: map[string]int{}, Hide: cfg.Hide, Show: cfg.Show}
		for story := range p.Stories {
			w.Dirs[story] = s.App.Dir(p, story)
		}
		if shells, err := s.App.Shells(p); err == nil {
			for _, sh := range shells {
				w.Shells[sh.ID] = ports.ShellRef{Story: sh.Story, K: sh.K, Run: sh.Run}
			}
		}
		if live, err := s.App.Live(p); err == nil {
			for num, sid := range live {
				w.Sessions[sid] = num
			}
		}
		for _, f := range ports.Match(scan, cs, panes, parent, w) {
			open := ""
			if r, ok := cfg.Find(f.Run); ok {
				open = r.Open
			}
			pt := ui.Port{Port: f.Port, URL: fmt.Sprintf("http://%s:%d%s", s.RunHost, f.Port, open),
				Shell: f.Shell, Session: f.Session, Run: f.Run}
			if f.Loopback && s.Forwards != nil {
				pt.Forwarded = true
				want[f.Port] = fmt.Sprintf("localhost:%d", f.Port)
			}
			if by[p.Name] == nil {
				by[p.Name] = map[string][]ui.Port{}
			}
			by[p.Name][f.Story] = append(by[p.Name][f.Story], pt)
		}
	}
	if s.Forwards != nil {
		errs := s.Forwards.Sync(want)
		for _, stories := range by {
			for _, list := range stories {
				for i := range list {
					if err := errs[list[i].Port]; list[i].Forwarded && err != nil {
						list[i].Forwarded, list[i].Error = false, err.Error()
					}
				}
			}
		}
	}
	s.portsMu.Lock()
	s.portsBy = by
	s.portsMu.Unlock()
}

func (s *Server) runRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/projects/{name}/runs/{action}", s.runAction)
	mux.HandleFunc("GET /api/runconfigs", s.runConfigs)
	mux.HandleFunc("GET /api/projects/{name}/runconfig", s.getRunConfig)
	mux.HandleFunc("PUT /api/projects/{name}/runconfig", s.putRunConfig)
	mux.HandleFunc("DELETE /api/projects/{name}/runconfig", s.deleteRunConfig)
}

// runAction runs, stops or sets up a story's runs. It does not take the write lock: a stop
// waits for the runs to end (up to half a minute) and must not hold every other change.
func (s *Server) runAction(w http.ResponseWriter, r *http.Request) {
	act := r.PathValue("action")
	if act != "run" && act != "stop" && act != "setup" {
		http.NotFound(w, r)
		return
	}
	var a api.Action
	if err := json.NewDecoder(r.Body).Decode(&a); err != nil {
		httpError(w, 400, err)
		return
	}
	p, err := s.load(r.PathValue("name"))
	if err != nil {
		httpError(w, statusOf(err), err)
		return
	}
	s.runMu.Lock()
	defer s.runMu.Unlock()
	ctx := r.Context()
	switch act {
	case "run":
		err = s.App.RunStory(ctx, p, a.Story, app.RunOpts{Names: a.Runs, Replace: a.Replace, Setup: a.Setup, Host: s.RunHost})
	case "stop":
		err = s.App.StopStory(ctx, p, a.Story, a.Runs)
	case "setup":
		if len(a.Runs) != 1 {
			httpError(w, 400, errors.New("setup takes one run"))
			return
		}
		err = s.App.SetupAgain(ctx, p, a.Story, a.Runs[0], s.RunHost)
	}
	var conflict *app.RunConflictError
	switch {
	case errors.As(err, &conflict):
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(api.Result{Error: err.Error(), RunStory: conflict.Story, RunNames: conflict.Runs})
	case err != nil:
		httpError(w, statusOf(err), err)
	default:
		s.Log("run %s: %s/%s %v", act, p.Name, a.Story, a.Runs)
		writeJSON(w, api.Result{Story: a.Story})
	}
}

func (s *Server) runConfigs(w http.ResponseWriter, r *http.Request) {
	names, err := state.ListProjects(s.stateDir())
	if err != nil {
		httpError(w, 500, err)
		return
	}
	out := []api.RunConfigs{}
	for _, name := range names {
		p, err := s.App.LoadProject(name)
		if err != nil {
			continue
		}
		f, _ := s.App.RunConfig(p)
		out = append(out, api.RunConfigs{Project: name, Runs: f.Names()})
	}
	writeJSON(w, out)
}

func (s *Server) getRunConfig(w http.ResponseWriter, r *http.Request) {
	p, err := s.load(r.PathValue("name"))
	if err != nil {
		httpError(w, statusOf(err), err)
		return
	}
	data, err := os.ReadFile(s.Cfg.RunFile(p.Name))
	if err != nil && !os.IsNotExist(err) {
		httpError(w, 500, err)
		return
	}
	if r.URL.Query().Has("text") {
		writeJSON(w, api.RunConfigText{Text: string(data)})
		return
	}
	out := api.RunConfig{Runs: []runcfg.Run{}, HasComments: runcfg.HasComments(data)}
	if p.IsWorkspace() {
		out.Repos, _ = s.App.WorkspaceRepos(p)
	}
	if f, err := runcfg.Parse(data); err != nil {
		out.Error = err.Error()
	} else {
		out.Runs, out.Hide, out.Show = slices.Clip(append(out.Runs, f.Runs...)), f.Hide, f.Show
	}
	writeJSON(w, out)
}

func (s *Server) putRunConfig(w http.ResponseWriter, r *http.Request) {
	p, err := s.load(r.PathValue("name"))
	if err != nil {
		httpError(w, statusOf(err), err)
		return
	}
	var repos []string
	if p.IsWorkspace() {
		repos, _ = s.App.WorkspaceRepos(p)
	}
	var data []byte
	if r.URL.Query().Has("text") {
		var t api.RunConfigText
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&t); err != nil {
			httpError(w, 400, err)
			return
		}
		f, err := runcfg.Parse([]byte(t.Text))
		var se *runcfg.SyntaxError
		if errors.As(err, &se) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(400)
			_ = json.NewEncoder(w).Encode(api.Result{Error: se.Msg, Line: se.Line})
			return
		}
		if errs := runcfg.Validate(f, repos); len(errs) > 0 {
			httpError(w, 400, errs[0])
			return
		}
		data = []byte(t.Text) // as written: its comments stay
	} else {
		var c api.RunConfig
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&c); err != nil {
			httpError(w, 400, err)
			return
		}
		f := runcfg.File{Runs: c.Runs, Hide: c.Hide, Show: c.Show}
		if errs := runcfg.Validate(f, repos); len(errs) > 0 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(400)
			_ = json.NewEncoder(w).Encode(api.RunConfig{Runs: c.Runs, Errors: errs, Error: errs[0].Error()})
			return
		}
		data = runcfg.Format(f)
	}
	if err := s.App.SaveRunConfig(p, data); err != nil {
		httpError(w, 500, err)
		return
	}
	s.Log("run config: saved %s", p.Name)
	r.URL.RawQuery = ""
	s.getRunConfig(w, r)
}

func (s *Server) deleteRunConfig(w http.ResponseWriter, r *http.Request) {
	p, err := s.load(r.PathValue("name"))
	if err != nil {
		httpError(w, statusOf(err), err)
		return
	}
	if err := s.App.DeleteRunConfig(p); err != nil {
		httpError(w, 500, err)
		return
	}
	writeJSON(w, api.Result{})
}
