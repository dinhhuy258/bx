package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/dinhhuy258/bx/internal/api"
	"github.com/dinhhuy258/bx/internal/app"
	"github.com/dinhhuy258/bx/internal/ports"
	"github.com/dinhhuy258/bx/internal/runcfg"
	"github.com/dinhhuy258/bx/internal/state"
	"github.com/dinhhuy258/bx/internal/ui"
)

const listPanes = "list-panes -a -F #{pane_pid}\t#{session_id}"

func TestScanPorts(t *testing.T) {
	s, f, _ := newServer(t)
	p, _ := s.App.LoadProject("home-network")
	wt := s.Cfg.WorktreeDir("home-network", "s1")
	os.MkdirAll(s.Cfg.RunsDir(), 0o755)
	os.WriteFile(s.Cfg.RunFile("home-network"), []byte("[[run]]\nname = \"web\"\ncmd = \"npm run dev\"\nopen = \"/login\"\n"), 0o644)
	f.Replies[listShells] = "$7\tbxsh-home-network-s1-1\thome-network\ts1\t1\t\tnode\tweb\n"
	f.Replies[listPanes] = "10\t$7\n20\t$5\n"
	s.Proc = ports.Fake{
		Files: map[string]string{
			"/proc/net/tcp": "  sl local rem st\n" +
				"   0: 0100007F:0BB8 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000 0 111 1\n" + // 3000 loopback, run web
				"   1: 0100007F:0FA0 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000 0 222 1\n", // 4000 loopback, held by chat 1
			"/proc/42/stat": "42 (node) S 10 0",
			"/proc/43/stat": "43 (go) S 20 0",
		},
		Links: map[string]string{
			"/proc/42/cwd": wt, "/proc/42/fd/3": "socket:[111]",
			"/proc/43/cwd": wt, "/proc/43/fd/3": "socket:[222]",
		},
	}
	s.RunHost = "dev"
	s.Forwards = NewForwards("127.0.0.1")
	s.Forwards.listen = func(string, string) (net.Listener, error) { return net.Listen("tcp", "127.0.0.1:0") }
	t.Cleanup(s.Forwards.Close)

	s.scanPorts()
	l := s.collect(p)
	want := []ui.Port{
		{Port: 3000, URL: "http://dev:3000/login", Forwarded: true, Shell: 1, Run: "web"},
	} // 4000 is a chat's (an MCP server): not a run, neither listed nor forwarded
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

func TestRunRoutes(t *testing.T) {
	s, f, ts := newServer(t)
	os.MkdirAll(s.Cfg.RunsDir(), 0o755)
	os.WriteFile(s.Cfg.RunFile("home-network"), []byte("[[run]]\nname = \"web\"\ncmd = \"npm run dev\"\n"), 0o644)
	s.App.Self, s.RunHost = "/bin/bx", "dev"
	wt := s.Cfg.WorktreeDir("home-network", "s1")
	f.Replies["new-session -d -s bxsh-home-network-s1-1 -c "+wt+" -e PAGER=cat -e GIT_PAGER=cat -e BX_RUN_HOST=dev -P -F #{session_id}"] = "$9\n"

	res, code := post(t, ts, "/api/projects/home-network/runs/run", api.Action{Story: "s1"})
	if code != 200 || res.Error != "" {
		t.Fatalf("run: %d %+v", code, res)
	}
	if len(f.Inputs) == 0 || f.Inputs[len(f.Inputs)-1] != "/bin/bx run-exec home-network s1 web" {
		t.Errorf("typed %q", f.Inputs)
	}

	// another story runs: 409 with what runs
	f.Replies[listShells] = "$9\tbxsh-home-network-s1-1\thome-network\ts1\t1\t\tnode\tweb\n"
	s.App.NewStory(context.Background(), mustLoad(t, s), app.StoryOpts{Name: "s2"})
	res, code = post(t, ts, "/api/projects/home-network/runs/run", api.Action{Story: "s2"})
	if code != 409 || res.RunStory != "s1" || !slices.Equal(res.RunNames, []string{"web"}) {
		t.Errorf("conflict: %d %+v", code, res)
	}
	if _, code = post(t, ts, "/api/projects/home-network/runs/bogus", api.Action{Story: "s1"}); code != 404 {
		t.Errorf("bogus action: %d", code)
	}
}

func mustLoad(t *testing.T, s *Server) *state.Project {
	t.Helper()
	p, err := s.App.LoadProject("home-network")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRunConfigRoutes(t *testing.T) {
	s, _, ts := newServer(t)
	_ = s
	empty, code := get[api.RunConfig](t, ts, "/api/projects/home-network/runconfig")
	if code != 200 || len(empty.Runs) != 0 || empty.Repos != nil {
		t.Fatalf("empty: %d %+v", code, empty)
	}
	// a structured save: refused fields, then a good one
	bad := api.RunConfig{Runs: []runcfg.Run{{Name: "web"}}}
	res, code := putAs[api.RunConfig](t, ts, "/api/projects/home-network/runconfig", bad)
	if code != 400 || len(res.Errors) != 1 || res.Errors[0].Field != "cmd" {
		t.Errorf("refused: %d %+v", code, res)
	}
	good := api.RunConfig{Runs: []runcfg.Run{{Name: "web", Cmd: "npm run dev", Port: runcfg.Ports{3000}}}, Hide: []int{5173}}
	if _, code = putAs[api.RunConfig](t, ts, "/api/projects/home-network/runconfig", good); code != 200 {
		t.Fatalf("save: %d", code)
	}
	list, _ := get[[]api.RunConfigs](t, ts, "/api/runconfigs")
	if len(list) != 1 || list[0].Project != "home-network" || !slices.Equal(list[0].Runs, []string{"web"}) {
		t.Errorf("list: %+v", list)
	}
	// text: read, a syntax error with its line, a commented save
	txt, _ := get[api.RunConfigText](t, ts, "/api/projects/home-network/runconfig?text=1")
	if !strings.Contains(txt.Text, `name = "web"`) {
		t.Errorf("text: %q", txt.Text)
	}
	r, code := putAs[api.Result](t, ts, "/api/projects/home-network/runconfig?text=1", api.RunConfigText{Text: "[[run]]\nname = \n"})
	if code != 400 || r.Line != 2 {
		t.Errorf("syntax: %d %+v", code, r)
	}
	if _, code = putAs[api.Result](t, ts, "/api/projects/home-network/runconfig?text=1", api.RunConfigText{Text: "# mine\n[[run]]\nname = \"a\"\ncmd = \"x\"\n"}); code != 200 {
		t.Errorf("text save: %d", code)
	}
	back, _ := get[api.RunConfig](t, ts, "/api/projects/home-network/runconfig")
	if !back.HasComments || back.Runs[0].Name != "a" {
		t.Errorf("after text save: %+v", back)
	}
	if code := del(t, ts, "/api/projects/home-network/runconfig"); code != 200 {
		t.Errorf("delete: %d", code)
	}
}

// putAs is a PUT whose reply is decoded whatever the status.
func putAs[T any](t *testing.T, ts *httptest.Server, path string, body any) (T, int) {
	t.Helper()
	data, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPut, ts.URL+path, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	var out T
	_ = json.NewDecoder(r.Body).Decode(&out)
	return out, r.StatusCode
}

func del(t *testing.T, ts *httptest.Server, path string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+path, nil)
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	return r.StatusCode
}
