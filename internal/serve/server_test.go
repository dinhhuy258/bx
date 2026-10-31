package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dinhhuy258/bx/internal/api"
	"github.com/dinhhuy258/bx/internal/app"
	"github.com/dinhhuy258/bx/internal/claude"
	"github.com/dinhhuy258/bx/internal/config"
	"github.com/dinhhuy258/bx/internal/git"
	"github.com/dinhhuy258/bx/internal/state"
	"github.com/dinhhuy258/bx/internal/tmux"
)

const listSessions = "list-sessions -F #{session_id}\t#{session_name}"

type fakeLister struct{ agents []claude.Agent }

func (f fakeLister) Agents(context.Context) ([]claude.Agent, error) { return f.agents, nil }

// newServer builds a Server over a fake tmux and a real git repo: project home-network with
// story s1 (a real worktree) and session 1 "Add lease time", running as tmux session $5.
func newServer(t *testing.T) (*Server, *tmux.Fake, *httptest.Server) {
	t.Helper()
	cfg := config.Default(t.TempDir())
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	repo := cfg.ProjectDir("home-network")
	g := git.New()
	if err := g.Init(repo); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(repo, "README.md"), []byte("x\n"), 0o644)
	for _, args := range [][]string{{"add", "."}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "init"}} {
		c := exec.Command("git", args...)
		c.Dir = repo
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("%v %s", err, out)
		}
	}
	f := &tmux.Fake{Replies: map[string]string{listSessions: "$5\tbx-home-network-1\n"}}
	a := &app.App{Cfg: cfg, Tmux: tmux.New(f), Git: g, UUID: func() string { return "fixed-uuid" }}
	p := state.New("home-network", repo)
	if err := a.NewStory(p, "s1"); err != nil {
		t.Fatal(err)
	}
	p.AddSession(&state.Session{Story: "s1", UUID: "u1", Title: "Add lease time"})
	if err := a.SaveProject(p); err != nil {
		t.Fatal(err)
	}
	s := New(a, cfg, fakeLister{[]claude.Agent{{SessionID: "u1", Status: "waiting"}}}, func(string, ...any) {})
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return s, f, ts
}

func get[T any](t *testing.T, ts *httptest.Server, path string) (T, int) {
	t.Helper()
	var v T
	r, err := http.Get(ts.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Body.Close() }()
	if r.StatusCode == http.StatusOK {
		if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
			t.Fatal(err)
		}
	}
	return v, r.StatusCode
}

func post(t *testing.T, ts *httptest.Server, path string, body any) (api.Result, int) {
	t.Helper()
	b, _ := json.Marshal(body)
	r, err := http.Post(ts.URL+path, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	var res api.Result
	_ = json.NewDecoder(r.Body).Decode(&res)
	return res, r.StatusCode
}

func calls(f *tmux.Fake) string {
	var out []string
	for _, c := range f.Calls {
		out = append(out, strings.Join(c, " "))
	}
	return strings.Join(out, "\n")
}

func TestProjectsAndSnapshot(t *testing.T) {
	_, _, ts := newServer(t)
	list, code := get[[]api.ProjectSummary](t, ts, "/api/projects")
	if code != 200 || len(list) != 1 || list[0].Name != "home-network" || list[0].Waiting != 1 || len(list[0].WaitingSessions) != 1 || list[0].WaitingSessions[0] != 1 {
		t.Fatalf("%d %+v", code, list)
	}
	snap, code := get[api.Snapshot](t, ts, "/api/projects/home-network")
	if code != 200 || snap.Project.Sessions[1].Title != "Add lease time" || snap.Live.Agents["u1"].State() != claude.Waiting || snap.Live.Branch == "" {
		t.Fatalf("%d %+v", code, snap)
	}
	if _, ok := snap.Live.Diff["s1"]; !ok {
		t.Fatalf("no diff for s1: %+v", snap.Live.Diff)
	}
	if _, code := get[api.Snapshot](t, ts, "/api/projects/nope"); code != 404 {
		t.Fatalf("unknown project: %d", code)
	}
}

func TestActionsGoThroughApp(t *testing.T) {
	s, f, ts := newServer(t)
	res, code := post(t, ts, "/api/projects/home-network/sessions", api.Action{Story: "s1", Title: "Check syntax"})
	if code != 200 || res.Num != 2 {
		t.Fatalf("%d %+v", code, res)
	}
	want := "new-session -d -s bx-home-network-2 -c " + s.Cfg.WorktreeDir("home-network", "s1") + " -P -F #{session_id} claude --session-id fixed-uuid"
	if !strings.Contains(calls(f), want) {
		t.Fatalf("calls:\n%s", calls(f))
	}
	if _, code := post(t, ts, "/api/projects/home-network/yes", api.Action{Num: 1}); code != 200 || !strings.Contains(calls(f), "send-keys -t $5 Enter") {
		t.Fatalf("yes: %d\n%s", code, calls(f))
	}
	if res, code := post(t, ts, "/api/projects/home-network/yes", api.Action{Num: 2}); code != 410 || res.Error == "" {
		t.Fatalf("yes on a session that is not running: %d %+v", code, res) // the fake lists only $5
	}
	if _, code := post(t, ts, "/api/projects/home-network/rename", api.Action{Num: 1, Title: "renamed"}); code != 200 {
		t.Fatal(code)
	}
	snap, _ := get[api.Snapshot](t, ts, "/api/projects/home-network")
	if snap.Project.Sessions[1].Title != "renamed" || snap.Project.Sessions[2] == nil {
		t.Fatalf("%+v", snap.Project)
	}
	if res, code := post(t, ts, "/api/projects/home-network/stories", api.Action{Name: "Bad Name"}); code != 400 || res.Error == "" {
		t.Fatalf("bad story name: %d %+v", code, res)
	}
	if _, code := post(t, ts, "/api/projects/nope/yes", api.Action{Num: 1}); code != 404 {
		t.Fatalf("unknown project: %d", code)
	}
}

func TestCloseStoryDirtyThenForce(t *testing.T) {
	s, f, ts := newServer(t)
	os.WriteFile(filepath.Join(s.Cfg.WorktreeDir("home-network", "s1"), "wip.txt"), []byte("x"), 0o644)
	res, code := post(t, ts, "/api/projects/home-network/stories/close", api.Action{Name: "s1"})
	if code != 409 || res.Dirty != 1 {
		t.Fatalf("%d %+v", code, res)
	}
	if _, code := post(t, ts, "/api/projects/home-network/stories/close", api.Action{Name: "s1", Force: true}); code != 200 {
		t.Fatal(code)
	}
	if !strings.Contains(calls(f), "kill-session -t $5") {
		t.Fatalf("calls:\n%s", calls(f))
	}
	snap, _ := get[api.Snapshot](t, ts, "/api/projects/home-network")
	if len(snap.Project.Stories) != 0 || len(snap.Project.Sessions) != 0 {
		t.Fatalf("%+v", snap.Project)
	}
}

func TestCreateProjectsAndRepos(t *testing.T) {
	s, _, ts := newServer(t)
	if res, code := post(t, ts, "/api/projects", api.NewProject{Kind: "new", Name: "scratch"}); code != 200 || res.Name != "scratch" {
		t.Fatalf("%d %+v", code, res)
	}
	other := filepath.Join(t.TempDir(), "lab")
	if err := git.New().Init(other); err != nil {
		t.Fatal(err)
	}
	if res, code := post(t, ts, "/api/projects", api.NewProject{Kind: "folder", Path: other}); code != 200 || res.Name != "lab" {
		t.Fatalf("%d %+v", code, res)
	}
	if res, code := post(t, ts, "/api/projects", api.NewProject{Kind: "folder", Path: "rel"}); code != 400 || res.Error == "" {
		t.Fatalf("%d %+v", code, res)
	}
	list, _ := get[[]api.ProjectSummary](t, ts, "/api/projects")
	if len(list) != 3 {
		t.Fatalf("%+v", list)
	}
	s.App.GH = func(...string) (string, error) { return "a/b\nc/d\n", nil }
	repos, code := get[[]string](t, ts, "/api/github/repos")
	if code != 200 || strings.Join(repos, ",") != "a/b,c/d" {
		t.Fatalf("%d %q", code, repos)
	}
}

func TestResumeAllAtStartup(t *testing.T) {
	s, f, _ := newServer(t)
	p, _ := s.App.LoadProject("home-network")
	p.AddSession(&state.Session{UUID: "u2", Title: "gone"})
	s.App.SaveProject(p)
	s.ResumeAll(context.Background())
	if !strings.Contains(calls(f), "new-session -d -s bx-home-network-2 -c "+p.Path+" -P -F #{session_id} claude --resume u2") {
		t.Fatalf("calls:\n%s", calls(f))
	}
	if strings.Contains(calls(f), "claude --resume u1") {
		t.Fatal("resumed a running session")
	}
}
