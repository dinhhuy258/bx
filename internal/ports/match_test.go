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
