package ports

import (
	"reflect"
	"testing"
)

const tcp = `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:0BB8 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 111 1 0000000000000000 100 0 0 10 0
   1: 00000000:1F40 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 222 1 0000000000000000 100 0 0 10 0
   2: 0100007F:0BB8 0100007F:D431 01 00000000:00000000 00:00000000 00000000  1000        0 333 1 0000000000000000 100 0 0 10 0
`

const tcp6 = `  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000000000000000000001000000:1538 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 444 1 0000000000000000 100 0 0 10 0
   1: 00000000000000000000000000000000:2328 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 555 1 0000000000000000 100 0 0 10 0
`

func TestParseTCP(t *testing.T) {
	got := ParseTCP([]byte(tcp), false)
	want := []Listener{{Port: 3000, Inode: 111, Loopback: true}, {Port: 8000, Inode: 222}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("tcp: %+v", got)
	}
	got = ParseTCP([]byte(tcp6), true)
	want = []Listener{{Port: 5432, Inode: 444, Loopback: true}, {Port: 9000, Inode: 555}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("tcp6: %+v", got)
	}
}

func TestRead(t *testing.T) {
	p := Fake{
		Files: map[string]string{
			"/proc/net/tcp":   tcp,
			"/proc/net/tcp6":  tcp6,
			"/proc/42/stat":   "42 (node (x)) S 7 42 42 0",
			"/proc/42/cgroup": "0::/user.slice/user-1000.slice/session-1.scope\n",
			"/proc/43/stat":   "43 (php) S 1 43 43 0",
			"/proc/43/cgroup": "0::/system.slice/docker-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef.scope\n",
			"/proc/self/stat": "1 (x) S 0",
			"/proc/net/unix":  "",
		},
		Links: map[string]string{
			"/proc/42/cwd":  "/home/u/.bx/worktrees/web/s1",
			"/proc/42/fd/3": "socket:[111]",
			"/proc/42/fd/4": "/dev/null",
			"/proc/43/cwd":  "/work",
			"/proc/43/fd/5": "socket:[222]",
		},
	}
	s, err := Read(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Listeners) != 4 {
		t.Fatalf("listeners: %+v", s.Listeners)
	}
	if s.Owners[111] != 42 || s.Owners[222] != 43 || s.Owners[444] != 0 {
		t.Errorf("owners: %v", s.Owners)
	}
	want := map[int]Process{
		42: {PID: 42, PPID: 7, Cwd: "/home/u/.bx/worktrees/web/s1"},
		43: {PID: 43, PPID: 1, Cwd: "/work", Container: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"},
	}
	if !reflect.DeepEqual(s.Procs, want) {
		t.Errorf("procs: %+v", s.Procs)
	}
}
