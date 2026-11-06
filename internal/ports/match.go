package ports

import (
	"bufio"
	"bytes"
	"cmp"
	"encoding/json"
	"maps"
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

// RunRef is a live run of a story: a shell with @bx_run, busy or back at its prompt. K is its
// shell, Dir its folder (the story folder and the run's dir; "" when its config is gone) and
// Ports the ports its config declares.
type RunRef struct {
	Name  string
	K     int
	Dir   string
	Ports []int
}

// Want is one project for Match: the folder of each story ("" is the project folder, where
// the chats run), its shells and sessions by tmux id, its live runs by story, and its hide
// and show lists. Only a live run can own a port: a run that is only in the config has no
// shell to show it on or to stop.
type Want struct {
	Project    string
	Dirs       map[string]string
	Shells     map[string]ShellRef
	Sessions   map[string]int
	Runs       map[string][]RunRef
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
//
// The owner of a port, first answer wins: (1) the shell or session of the tmux pane a host
// process runs under; (2) a container's bx.run label; (3) the live run of the story that
// declares the port; (4) for a compose container, the live run whose folder holds its
// working_dir, the deepest; (5) else the story alone. A container is owned as a whole, so its
// other ports follow a declared one. A port with a run but no shell gets the run's shell.
func Match(s Scan, cs []Container, panes []Pane, parent func(int) int, w Want) []Found {
	byContainer := map[string]Container{}
	for _, c := range cs {
		byContainer[c.ID] = c
	}
	paneOf := map[int]string{}
	for _, p := range panes {
		paneOf[p.PID] = p.Session
	}
	// the ports each container holds: published, and its sockets on the host network
	held := map[string][]int{}
	for _, c := range cs {
		held[c.ID] = append(held[c.ID], c.Ports...)
	}
	for _, l := range s.Listeners {
		if pr, ok := s.Procs[s.Owners[l.Inode]]; ok && pr.Container != "" {
			if c, ok := containerOf(byContainer, pr.Container); ok && !slices.Contains(held[c.ID], l.Port) {
				held[c.ID] = append(held[c.ID], l.Port)
			}
		}
	}
	owners := map[string]owner{}
	ownerOf := func(c Container) owner {
		o, ok := owners[c.ID]
		if !ok {
			o = w.ownContainer(c, held[c.ID])
			owners[c.ID] = o
		}
		return o
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
		if f.Run != "" && f.Shell == 0 && f.Session == 0 {
			f.Shell = w.shellOf(f.Story, f.Run)
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
		inContainer := pr.Container != ""
		if inContainer {
			if c, ok := containerOf(byContainer, pr.Container); ok {
				o := ownerOf(c)
				f.Story, f.Run = o.story, o.run(l.Port)
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
		if !inContainer && f.Shell == 0 && f.Session == 0 {
			f.Run = w.declared(f.Story, l.Port) // a host process under no pane
		}
		add(f)
	}
	for _, c := range cs { // published ports: docker-proxy holds them, so the cwd says nothing
		o := ownerOf(c)
		for _, port := range c.Ports {
			add(Found{Story: o.story, Port: port, Run: o.run(port)})
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

// owner is who owns a container: its story ("\x00" = none of the project's), the run that
// owns it whole ("" = none), and runs that own single ports when no one run owns it whole.
type owner struct {
	story  string
	all    string
	byPort map[int]string
}

func (o owner) run(port int) string {
	if o.all != "" {
		return o.all
	}
	return o.byPort[port]
}

// ownContainer finds the story and run of container c, which holds ports: its bx labels, else
// the ports the story's live runs declare, else the live run whose folder holds its compose
// working_dir. When live runs declare its ports, the folder is not asked: one run declaring
// them owns it whole, otherwise each port that one run alone declares is that run's.
func (w Want) ownContainer(c Container, ports []int) owner {
	if c.Labels["bx.project"] == w.Project {
		if _, ok := w.Dirs[c.Labels["bx.story"]]; ok {
			return owner{story: c.Labels["bx.story"], all: c.Labels["bx.run"]}
		}
	}
	wd := c.Labels["com.docker.compose.project.working_dir"]
	if wd == "" {
		return owner{story: "\x00"}
	}
	wd = filepath.Clean(wd)
	o := owner{story: w.storyOf(wd), byPort: map[int]string{}}
	declared, tied := false, false
	for _, port := range ports {
		names := w.declaring(o.story, port)
		switch {
		case len(names) == 1:
			o.byPort[port] = names[0]
		case len(names) > 1:
			tied = true
		}
		declared = declared || len(names) > 0
	}
	if declared {
		runs := slices.Compact(slices.Sorted(maps.Values(o.byPort)))
		if !tied && len(runs) == 1 {
			o.all = runs[0]
		}
		return o
	}
	o.all = w.runIn(o.story, wd)
	return o
}

// declaring are the live runs of story that declare port.
func (w Want) declaring(story string, port int) []string {
	var names []string
	for _, r := range w.Runs[story] {
		if slices.Contains(r.Ports, port) {
			names = append(names, r.Name)
		}
	}
	return names
}

// declared is the one live run of story that declares port; "" when none or several do.
func (w Want) declared(story string, port int) string {
	if names := w.declaring(story, port); len(names) == 1 {
		return names[0]
	}
	return ""
}

// runIn is the live run of story whose folder holds dir, the deepest; "" when none does or two
// runs share the deepest folder.
func (w Want) runIn(story, dir string) string {
	best, bestLen, tie := "", -1, false
	for _, r := range w.Runs[story] {
		if r.Dir == "" || dir != r.Dir && !strings.HasPrefix(dir, r.Dir+string(filepath.Separator)) {
			continue
		}
		switch {
		case len(r.Dir) > bestLen:
			best, bestLen, tie = r.Name, len(r.Dir), false
		case len(r.Dir) == bestLen:
			tie = true
		}
	}
	if tie {
		return ""
	}
	return best
}

// shellOf is the shell of the live run name of story; 0 when it has none.
func (w Want) shellOf(story, name string) int {
	for _, r := range w.Runs[story] {
		if r.Name == name {
			return r.K
		}
	}
	return 0
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
