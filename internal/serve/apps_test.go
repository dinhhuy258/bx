package serve

import (
	"net"
	"os"
	"reflect"
	"testing"

	"github.com/dinhhuy258/bx/internal/ports"
	"github.com/dinhhuy258/bx/internal/ui"
)

const listPanes = "list-panes -a -F #{pane_pid}\t#{session_id}"

func TestScanPorts(t *testing.T) {
	s, f, _ := newServer(t)
	p, _ := s.App.LoadProject("home-network")
	wt := s.Cfg.WorktreeDir("home-network", "s1")
	os.MkdirAll(s.Cfg.AppsDir(), 0o755)
	os.WriteFile(s.Cfg.AppFile("home-network"), []byte("[[run]]\nname = \"web\"\ncmd = \"npm run dev\"\nopen = \"/login\"\n"), 0o644)
	f.Replies[listShells] = "$7\tbxsh-home-network-s1-1\thome-network\ts1\t1\t\tnode\tweb\n"
	f.Replies[listPanes] = "10\t$7\n20\t$5\n"
	s.Proc = ports.Fake{
		Files: map[string]string{
			"/proc/net/tcp": "  sl local rem st\n" +
				"   0: 0100007F:0BB8 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000 0 111 1\n" + // 3000 loopback, run web
				"   1: 00000000:0FA0 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000 0 222 1\n", // 4000, session 1
			"/proc/42/stat": "42 (node) S 10 0",
			"/proc/43/stat": "43 (go) S 20 0",
		},
		Links: map[string]string{
			"/proc/42/cwd": wt, "/proc/42/fd/3": "socket:[111]",
			"/proc/43/cwd": wt, "/proc/43/fd/3": "socket:[222]",
		},
	}
	s.AppHost = "dev"
	s.Forwards = NewForwards("127.0.0.1")
	s.Forwards.listen = func(string, string) (net.Listener, error) { return net.Listen("tcp", "127.0.0.1:0") }
	t.Cleanup(s.Forwards.Close)

	s.scanPorts()
	l := s.collect(p)
	want := []ui.Port{
		{Port: 3000, URL: "http://dev:3000/login", Forwarded: true, Shell: 1, Run: "web"},
		{Port: 4000, URL: "http://dev:4000", Session: 1},
	}
	if !reflect.DeepEqual(l.Ports["s1"], want) {
		t.Errorf("ports: %+v", l.Ports)
	}
	if l.Running != "s1" {
		t.Errorf("running %q", l.Running)
	}
	if !s.Forwards.Open(3000) || s.Forwards.Open(4000) {
		t.Error("forwards: 3000 should be open, 4000 not")
	}
}
