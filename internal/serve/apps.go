package serve

import (
	"context"
	"fmt"
	"time"

	"github.com/dinhhuy258/bx/internal/ports"
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
			pt := ui.Port{Port: f.Port, URL: fmt.Sprintf("http://%s:%d%s", s.AppHost, f.Port, open),
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
