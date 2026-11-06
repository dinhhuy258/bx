package ports

import (
	"reflect"
	"testing"
)

const dockerPS = `{"ID":"0123456789ab","Labels":"bx.project=web,bx.story=s2,bx.run=api","Ports":""}
{"ID":"fedcba987654","Labels":"com.docker.compose.project=unem,com.docker.compose.project.working_dir=/home/u/.bx/worktrees/web/s1/be","Ports":"0.0.0.0:8080->80/tcp, :::8080->80/tcp, 0.0.0.0:5432->5432/tcp"}
`

func TestParseDocker(t *testing.T) {
	got := ParseDocker([]byte(dockerPS))
	if len(got) != 2 || got[0].Labels["bx.run"] != "api" || !reflect.DeepEqual(got[1].Ports, []int{8080, 5432}) {
		t.Errorf("%+v", got)
	}
}

func TestMatch(t *testing.T) {
	scan := Scan{
		Listeners: []Listener{
			{Port: 3000, Inode: 1, Loopback: true}, // node in s1, from run shell $2
			{Port: 3000, Inode: 2, Loopback: true}, // the same port on ::1, deduped
			{Port: 8000, Inode: 3},                 // container of s2 (host network)
			{Port: 4000, Inode: 4},                 // a session's dev server in the project folder
			{Port: 9229, Inode: 5, Loopback: true}, // node inspector: hidden
			{Port: 7357, Inode: 6},                 // not in any story: dropped
			{Port: 6000, Inode: 7},                 // held by root: unknown, dropped
		},
		Owners: map[uint64]int{1: 100, 2: 100, 3: 200, 4: 300, 5: 100, 6: 400, 7: 0},
		Procs: map[int]Process{
			100: {PID: 100, PPID: 99, Cwd: "/w/web/s1/fe"},
			200: {PID: 200, PPID: 1, Cwd: "/work", Container: "0123456789abcdef"},
			300: {PID: 300, PPID: 30, Cwd: "/src/web"},
			400: {PID: 400, PPID: 1, Cwd: "/"},
		},
	}
	parents := map[int]int{99: 10, 10: 1, 30: 1}
	panes := []Pane{{PID: 10, Session: "$7"}, {PID: 30, Session: "$3"}}
	w := Want{
		Dirs:     map[string]string{"": "/src/web", "s1": "/w/web/s1", "s2": "/w/web/s2"},
		Shells:   map[string]ShellRef{"$7": {Story: "s1", K: 2, Run: "fe"}},
		Sessions: map[string]int{"$3": 4},
		Project:  "web",
	}
	cs := ParseDocker([]byte(dockerPS))
	cs[1].Labels["com.docker.compose.project.working_dir"] = "/w/web/s1/be"
	got := Match(scan, cs, panes, func(pid int) int { return parents[pid] }, w)
	want := []Found{
		{Story: "", Port: 4000, Session: 4},
		{Story: "s1", Port: 3000, Loopback: true, Shell: 2, Run: "fe"},
		{Story: "s1", Port: 8080},
		{Story: "s2", Port: 8000, Run: "api"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
	w.Show, w.Hide = []int{5432}, []int{4000}
	got = Match(scan, cs, panes, func(pid int) int { return parents[pid] }, w)
	if len(got) != 4 || got[0].Port != 3000 || got[1].Port != 5432 {
		t.Errorf("show/hide: %+v", got)
	}
}

// TestMatchRunOwner: who owns a port once live runs are known. Story s1 lives in /w/s1; its
// repo be holds a compose project (laravel.test publishes 8000 and 5173).
func TestMatchRunOwner(t *testing.T) {
	compose := func(id, dir string, ports ...int) Container {
		return Container{ID: id, Labels: map[string]string{"com.docker.compose.project.working_dir": dir}, Ports: ports}
	}
	api := RunRef{Name: "api", K: 1, Dir: "/w/s1/be"}
	queue := RunRef{Name: "queue", K: 3, Dir: "/w/s1/be"}
	web := RunRef{Name: "web", K: 2, Dir: "/w/s1/fe"}
	base := func(runs ...RunRef) Want {
		return Want{Project: "p", Dirs: map[string]string{"": "/src/p", "s1": "/w/s1"},
			Shells: map[string]ShellRef{}, Sessions: map[string]int{}, Runs: map[string][]RunRef{"s1": runs}}
	}
	declare := func(r RunRef, ports ...int) RunRef { r.Ports = ports; return r }
	laravel := []Container{compose("c1", "/w/s1/be", 8000, 5173)}
	none := func(int) int { return 0 }
	cases := []struct {
		name string
		cs   []Container
		w    Want
		want []Found
	}{
		{"the run whose folder holds working_dir, with its shell", laravel, base(api, web),
			[]Found{{Story: "s1", Port: 5173, Shell: 1, Run: "api"}, {Story: "s1", Port: 8000, Shell: 1, Run: "api"}}},
		{"a run that isn't live owns nothing", laravel, base(web),
			[]Found{{Story: "s1", Port: 5173}, {Story: "s1", Port: 8000}}},
		{"two runs in one folder tie", laravel, base(api, queue),
			[]Found{{Story: "s1", Port: 5173}, {Story: "s1", Port: 8000}}},
		{"a declared port settles the tie, and 5173 follows 8000", laravel, base(declare(api, 8000), queue),
			[]Found{{Story: "s1", Port: 5173, Shell: 1, Run: "api"}, {Story: "s1", Port: 8000, Shell: 1, Run: "api"}}},
		{"two runs declaring one port: neither wins", laravel, base(declare(api, 8000), declare(queue, 8000)),
			[]Found{{Story: "s1", Port: 5173}, {Story: "s1", Port: 8000}}},
		{"two runs declaring a port each split the container", laravel, base(declare(api, 8000), declare(web, 5173)),
			[]Found{{Story: "s1", Port: 5173, Shell: 2, Run: "web"}, {Story: "s1", Port: 8000, Shell: 1, Run: "api"}}},
		{"a declared port beats the folder", laravel, base(api, declare(web, 8000)),
			[]Found{{Story: "s1", Port: 5173, Shell: 2, Run: "web"}, {Story: "s1", Port: 8000, Shell: 2, Run: "web"}}},
		{"a bx.run label beats a declared port", []Container{{ID: "c2", Ports: []int{8000},
			Labels: map[string]string{"bx.project": "p", "bx.story": "s1", "bx.run": "api"}}}, base(api, declare(web, 8000)),
			[]Found{{Story: "s1", Port: 8000, Shell: 1, Run: "api"}}},
		{"the deepest folder wins", []Container{compose("c3", "/w/s1/be/docker", 9000)}, base(api, RunRef{Name: "all", K: 4, Dir: "/w/s1"}),
			[]Found{{Story: "s1", Port: 9000, Shell: 1, Run: "api"}}},
	}
	for _, c := range cases {
		got := Match(Scan{}, c.cs, nil, none, c.w)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s:\n got  %+v\n want %+v", c.name, got, c.want)
		}
	}
}

// An image run (host network, bx.run label) holds a host socket from inside its container: its
// parents end at containerd-shim, not the pane, yet the port gets the run's shell.
func TestMatchImageRunGetsItsShell(t *testing.T) {
	scan := Scan{
		Listeners: []Listener{{Port: 3000, Inode: 1}},
		Owners:    map[uint64]int{1: 200},
		Procs:     map[int]Process{200: {PID: 200, PPID: 150, Cwd: "/work", Container: "0123456789abcdef"}},
	}
	cs := []Container{{ID: "0123456789ab", Labels: map[string]string{"bx.project": "p", "bx.story": "s1", "bx.run": "web"}}}
	parents := map[int]int{150: 1} // containerd-shim
	panes := []Pane{{PID: 10, Session: "$2"}}
	w := Want{Project: "p", Dirs: map[string]string{"": "/src/p", "s1": "/w/s1"},
		Shells: map[string]ShellRef{"$2": {Story: "s1", K: 2, Run: "web"}}, Sessions: map[string]int{},
		Runs: map[string][]RunRef{"s1": {{Name: "web", K: 2, Dir: "/w/s1/fe"}}}}
	got := Match(scan, cs, panes, func(pid int) int { return parents[pid] }, w)
	want := []Found{{Story: "s1", Port: 3000, Shell: 2, Run: "web"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
}

// Host processes: one under a pane keeps its pane even on a declared port; one under no pane
// takes the run that declares its port.
func TestMatchHostProcessDeclared(t *testing.T) {
	scan := Scan{
		Listeners: []Listener{{Port: 8000, Inode: 1}, {Port: 4000, Inode: 2}},
		Owners:    map[uint64]int{1: 100, 2: 300},
		Procs: map[int]Process{
			100: {PID: 100, PPID: 1, Cwd: "/w/s1/be"},  // a daemon under no pane
			300: {PID: 300, PPID: 30, Cwd: "/w/s1/fe"}, // a dev server in plain shell $1
		},
	}
	panes := []Pane{{PID: 30, Session: "$1"}}
	w := Want{Project: "p", Dirs: map[string]string{"": "/src/p", "s1": "/w/s1"},
		Shells: map[string]ShellRef{"$1": {Story: "s1", K: 1}}, Sessions: map[string]int{},
		Runs: map[string][]RunRef{"s1": {{Name: "api", K: 2, Dir: "/w/s1/be", Ports: []int{8000, 4000}}}}}
	got := Match(scan, nil, panes, func(pid int) int { return map[int]int{30: 1}[pid] }, w)
	want := []Found{{Story: "s1", Port: 4000, Shell: 1}, {Story: "s1", Port: 8000, Shell: 2, Run: "api"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
}
