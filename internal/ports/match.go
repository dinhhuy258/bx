package ports

import (
	"bufio"
	"bytes"
	"cmp"
	"encoding/json"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// Hidden are the ports a story's list leaves out unless the project shows them: databases,
// caches and debuggers, not something to open in a browser.
var Hidden = []int{5432, 3306, 6379, 27017, 9229, 11211}

// Container is a running container from `docker ps --format '{{json .}}'`: its id (short),
// labels, and the host ports it publishes.
type Container struct {
	ID     string
	Labels map[string]string
	Ports  []int
}

// ParseDocker reads `docker ps --no-trunc=false --format '{{json .}}'`, one object per line.
func ParseDocker(out []byte) []Container {
	var cs []Container
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		var raw struct{ ID, Labels, Ports string }
		if json.Unmarshal(sc.Bytes(), &raw) != nil {
			continue
		}
		c := Container{ID: raw.ID, Labels: map[string]string{}}
		for _, kv := range strings.Split(raw.Labels, ",") {
			if k, v, ok := strings.Cut(kv, "="); ok {
				c.Labels[k] = v
			}
		}
		for _, p := range strings.Split(raw.Ports, ",") {
			// "0.0.0.0:8080->80/tcp", ":::8080->80/tcp"
			host, _, ok := strings.Cut(strings.TrimSpace(p), "->")
			if !ok {
				continue
			}
			n, err := strconv.Atoi(host[strings.LastIndexByte(host, ':')+1:])
			if err == nil && !slices.Contains(c.Ports, n) {
				c.Ports = append(c.Ports, n)
			}
		}
		cs = append(cs, c)
	}
	return cs
}

// Pane is a tmux pane's process and its session id, from
// `list-panes -a -F "#{pane_pid}\t#{session_id}"`.
type Pane struct {
	PID     int
	Session string
}

// ShellRef is a bx shell: its story, number and, for a run shell, the run's name.
type ShellRef struct {
	Story string
	K     int
	Run   string
}

// Want is one project for Match: the folder of each story ("" is the project folder, where
// the chats run), its shells and sessions by tmux id, and its hide and show lists.
type Want struct {
	Project    string
	Dirs       map[string]string
	Shells     map[string]ShellRef
	Sessions   map[string]int
	Hide, Show []int
}

// Found is a port of a story: Loopback when only loopback takes it, and the shell (K), the
// session (number) or the run that owns it, when known.
type Found struct {
	Story    string
	Port     int
	Loopback bool
	Shell    int
	Session  int
	Run      string
}

// Match gives the project's ports, sorted by story then port. A process is in the story whose
// folder holds its cwd (the longest one); a container is in the story of its bx labels or of
// its compose working_dir. parent gives a pid's parent, for the walk to a tmux pane. A port
// listed twice (0.0.0.0 and ::) is one; it is Loopback only when every socket is.
func Match(s Scan, cs []Container, panes []Pane, parent func(int) int, w Want) []Found {
	byContainer := map[string]Container{}
	for _, c := range cs {
		byContainer[c.ID] = c
	}
	paneOf := map[int]string{}
	for _, p := range panes {
		paneOf[p.PID] = p.Session
	}
	type key struct {
		story string
		port  int
	}
	found := map[key]*Found{}
	add := func(f Found) {
		if f.Story == "\x00" || !w.shown(f.Port) {
			return
		}
		k := key{f.Story, f.Port}
		if old, ok := found[k]; ok {
			old.Loopback = old.Loopback && f.Loopback
			if old.Shell == 0 && old.Session == 0 {
				old.Shell, old.Session = f.Shell, f.Session
			}
			old.Run = cmp.Or(old.Run, f.Run)
			return
		}
		found[k] = &f
	}
	for _, l := range s.Listeners {
		pid := s.Owners[l.Inode]
		pr, ok := s.Procs[pid]
		if pid == 0 || !ok {
			continue
		}
		f := Found{Port: l.Port, Loopback: l.Loopback, Story: "\x00"}
		if pr.Container != "" {
			if c, ok := containerOf(byContainer, pr.Container); ok {
				f.Story, f.Run = w.storyOfContainer(c)
			}
		} else {
			f.Story = w.storyOf(pr.Cwd)
		}
		next := func(p int) int { // the scan already holds the holder's parent
			if p == pid {
				return pr.PPID
			}
			return parent(p)
		}
		for p, n := pid, 0; p > 1 && n < 64; p, n = next(p), n+1 {
			if sid, ok := paneOf[p]; ok {
				if sh, ok := w.Shells[sid]; ok && sh.Story == f.Story {
					f.Shell, f.Run = sh.K, cmp.Or(f.Run, sh.Run)
				} else if num, ok := w.Sessions[sid]; ok {
					f.Session = num
				}
				break
			}
		}
		add(f)
	}
	for _, c := range cs { // published ports: docker-proxy holds them, so the cwd says nothing
		story, run := w.storyOfContainer(c)
		for _, port := range c.Ports {
			add(Found{Story: story, Port: port, Run: run})
		}
	}
	out := make([]Found, 0, len(found))
	for _, f := range found {
		out = append(out, *f)
	}
	slices.SortFunc(out, func(a, b Found) int {
		return cmp.Or(strings.Compare(a.Story, b.Story), a.Port-b.Port)
	})
	return out
}

func containerOf(m map[string]Container, full string) (Container, bool) {
	for id, c := range m {
		if strings.HasPrefix(full, id) {
			return c, true
		}
	}
	return Container{}, false
}

func (w Want) shown(port int) bool {
	if slices.Contains(w.Show, port) {
		return true
	}
	return !slices.Contains(w.Hide, port) && !slices.Contains(Hidden, port)
}

// storyOf is the story whose folder holds dir, the longest match; "\x00" is none.
func (w Want) storyOf(dir string) string {
	best, bestLen := "\x00", -1
	for story, d := range w.Dirs {
		if d == "" {
			continue
		}
		if dir == d || strings.HasPrefix(dir, d+string(filepath.Separator)) {
			if len(d) > bestLen {
				best, bestLen = story, len(d)
			}
		}
	}
	return best
}

func (w Want) storyOfContainer(c Container) (story, run string) {
	if c.Labels["bx.project"] == w.Project {
		if _, ok := w.Dirs[c.Labels["bx.story"]]; ok {
			return c.Labels["bx.story"], c.Labels["bx.run"]
		}
	}
	if d := c.Labels["com.docker.compose.project.working_dir"]; d != "" {
		return w.storyOf(filepath.Clean(d)), ""
	}
	return "\x00", ""
}
