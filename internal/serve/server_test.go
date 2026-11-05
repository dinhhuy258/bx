package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dinhhuy258/bx/internal/accounts"
	"github.com/dinhhuy258/bx/internal/api"
	"github.com/dinhhuy258/bx/internal/app"
	"github.com/dinhhuy258/bx/internal/claude"
	"github.com/dinhhuy258/bx/internal/config"
	"github.com/dinhhuy258/bx/internal/git"
	"github.com/dinhhuy258/bx/internal/settings"
	"github.com/dinhhuy258/bx/internal/state"
	"github.com/dinhhuy258/bx/internal/tmux"
)

const listSessions = "list-sessions -F #{session_id}\t#{session_name}"

const listShells = "list-sessions -F " + tmux.ShellFormat

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
	f := &tmux.Fake{Replies: map[string]string{listSessions: "$5\tbx-home-network-1\n", listShells: ""}}
	a := &app.App{Cfg: cfg, Tmux: tmux.New(f), Git: g, UUID: func() string { return "fixed-uuid" }}
	var err error
	if a.Settings, err = settings.Open(cfg.SettingsFile(), func(string, ...any) {}); err != nil {
		t.Fatal(err)
	}
	p := state.New("home-network", repo)
	if _, err := a.NewStory(context.Background(), p, app.StoryOpts{Name: "s1"}); err != nil {
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
	s, _, ts := newServer(t)
	if err := os.WriteFile(filepath.Join(s.Cfg.WorktreeDir("home-network", "s1"), "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	list, code := get[[]api.ProjectSummary](t, ts, "/api/projects")
	if code != 200 || len(list) != 1 || list[0].Name != "home-network" || list[0].Waiting != 1 || len(list[0].WaitingSessions) != 1 || list[0].WaitingSessions[0] != 1 {
		t.Fatalf("%d %+v", code, list)
	}
	snap, code := get[api.Snapshot](t, ts, "/api/projects/home-network")
	if code != 200 || snap.Project.Sessions[1].Title != "Add lease time" || snap.Live.Agents["u1"].State() != claude.Waiting || snap.Live.Branch == "" {
		t.Fatalf("%d %+v", code, snap)
	}
	if d, ok := snap.Live.Diff["s1"]; !ok || d.Dirty != 1 {
		t.Fatalf("diff for s1 should count its untracked file: %+v", snap.Live.Diff)
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
	if _, code := post(t, ts, "/api/projects/home-network/yes", api.Action{Num: 1}); (code != 404 && code != 405) || strings.Contains(calls(f), "send-keys") {
		t.Fatalf("the quick answer is gone: %d\n%s", code, calls(f))
	}
	if _, code := post(t, ts, "/api/projects/home-network/rename", api.Action{Num: 1, Title: "renamed"}); code != 200 {
		t.Fatal(code)
	}
	snap, _ := get[api.Snapshot](t, ts, "/api/projects/home-network")
	if snap.Project.Sessions[1].Title != "renamed" || snap.Project.Sessions[2] == nil {
		t.Fatalf("%+v", snap.Project)
	}
	if res, code := post(t, ts, "/api/projects/home-network/stories", api.Action{Name: "!!!"}); code != 400 || res.Error == "" {
		t.Fatalf("bad story name: %d %+v", code, res)
	}
	if _, code := post(t, ts, "/api/projects/nope/rename", api.Action{Num: 1, Title: "x"}); code != 404 {
		t.Fatalf("unknown project: %d", code)
	}
}

func put(t *testing.T, ts *httptest.Server, path string, body any) (api.Settings, api.Result, int) {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPut, ts.URL+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Body.Close() }()
	var raw json.RawMessage
	_ = json.NewDecoder(r.Body).Decode(&raw)
	var out api.Settings
	var res api.Result
	_ = json.Unmarshal(raw, &out)
	_ = json.Unmarshal(raw, &res)
	return out, res, r.StatusCode
}

func TestSettingsGetPutAndFlags(t *testing.T) {
	s, f, ts := newServer(t)
	got, code := get[api.Settings](t, ts, "/api/settings")
	if code != 200 || len(got.Sections) != 2 || got.Sections[0].ID != "claude" || len(got.Sections[0].Fields) != 3 || len(got.Values) != 0 {
		t.Fatalf("%d %+v", code, got)
	}
	// the phone gets only the on/off switch of the notifications, never the URLs
	if n := got.Sections[1]; n.ID != "notifications" || len(n.Fields) != 1 || n.Fields[0].Key != "notifications.enabled" {
		t.Fatalf("web notifications: %+v", n)
	}
	if _, res, code := put(t, ts, "/api/settings", api.Settings{Values: map[string]string{"claude.model": "opus", "claude.effort": "huge"}}); code != 400 ||
		res.Error != `claude.effort "huge": want one of low, medium, high, xhigh, max` {
		t.Fatalf("bad value: %d %+v", code, res)
	}
	if s.App.Settings.Get("claude.model") != "" {
		t.Fatal("a refused PUT saved a value")
	}
	saved, _, code := put(t, ts, "/api/settings", api.Settings{Values: map[string]string{"claude.model": "opus", "claude.permission_mode": "acceptEdits"}})
	if code != 200 || saved.Values["claude.model"] != "opus" || len(saved.Sections) != 2 {
		t.Fatalf("%d %+v", code, saved)
	}
	if _, err := os.Stat(s.Cfg.SettingsFile()); err != nil {
		t.Fatal(err)
	}
	// the notification URLs are for the TUI only: the phone never gets the ntfy secret
	if _, res, code := put(t, ts, "/api/settings?tui=1", api.Settings{Values: map[string]string{"notifications.ntfy_url": "nope"}}); code != 400 || res.Error != "notifications.ntfy_url: want an http:// or https:// URL" {
		t.Fatalf("bad URL: %d %+v", code, res)
	}
	secret := "https://ntfy.sh/bx-secret"
	// the web client may not write them either: a 400, and nothing saved
	for _, k := range []string{"notifications.ntfy_url", "notifications.open_url"} {
		for _, v := range []string{"https://ntfy.sh/attacker", ""} {
			if _, res, code := put(t, ts, "/api/settings", api.Settings{Values: map[string]string{"claude.model": "sonnet", k: v}}); code != 400 ||
				res.Error != k+" can only be changed from the TUI" {
				t.Fatalf("web PUT of a TUI-only key: %d %+v", code, res)
			}
		}
	}
	if s.App.Settings.Get("claude.model") != "opus" || s.App.Settings.Get("notifications.ntfy_url") != "" {
		t.Fatalf("a refused web PUT saved: %v", s.App.Settings.Values())
	}
	web, _, code := put(t, ts, "/api/settings?tui=1", api.Settings{Values: map[string]string{"notifications.ntfy_url": secret}})
	if code != 200 || len(web.Sections) != 3 || web.Values["notifications.ntfy_url"] != secret || web.Values["claude.model"] != "opus" {
		t.Fatalf("tui PUT reply: %d %+v", code, web)
	}
	if _, res, code := put(t, ts, "/api/settings", api.Settings{Values: map[string]string{"notifications.ntfy_url": ""}}); code != 400 || res.Error == "" ||
		s.App.Settings.Get("notifications.ntfy_url") != secret {
		t.Fatalf("web PUT cleared the secret: %d %+v", code, res)
	}
	// the phone may turn the notifications off and on
	web, _, code = put(t, ts, "/api/settings", api.Settings{Values: map[string]string{"notifications.enabled": "off"}})
	if code != 200 || s.App.Settings.Get("notifications.enabled") != "off" || web.Values["notifications.enabled"] != "off" || web.Values["notifications.ntfy_url"] != "" {
		t.Fatalf("web PUT of enabled: %d %+v", code, web)
	}
	if web, _ = get[api.Settings](t, ts, "/api/settings"); len(web.Sections) != 2 || len(web.Values) != 3 || web.Values["notifications.ntfy_url"] != "" {
		t.Fatalf("web GET: %+v", web)
	}
	tui, _ := get[api.Settings](t, ts, "/api/settings?tui=1")
	if len(tui.Sections) != 3 || tui.Sections[2].ID != "notifications" || len(tui.Sections[2].Fields) != 3 ||
		tui.Values["notifications.ntfy_url"] != secret || tui.Values["notifications.enabled"] != "off" {
		t.Fatalf("tui GET: %+v", tui)
	}
	if tui, _, code = put(t, ts, "/api/settings?tui=1", api.Settings{Values: map[string]string{"notifications.enabled": "on", "notifications.open_url": "https://bx.example.ts.net"}}); code != 200 ||
		tui.Values["notifications.enabled"] != "on" || tui.Values["notifications.open_url"] != "https://bx.example.ts.net" {
		t.Fatalf("tui PUT: %d %+v", code, tui)
	}
	put(t, ts, "/api/settings?tui=1", api.Settings{Values: map[string]string{"notifications.ntfy_url": "", "notifications.open_url": "", "notifications.enabled": ""}})
	// the next session starts with the saved values, and keeps them
	if _, code := post(t, ts, "/api/projects/home-network/sessions", api.Action{Title: "chat"}); code != 200 {
		t.Fatal(code)
	}
	if want := "claude --session-id fixed-uuid --permission-mode acceptEdits --model opus"; !strings.Contains(calls(f), want) {
		t.Fatalf("calls:\n%s", calls(f))
	}
	put(t, ts, "/api/settings", api.Settings{Values: map[string]string{"claude.model": "", "claude.permission_mode": ""}})
	f.Calls = nil
	if _, code := post(t, ts, "/api/projects/home-network/resume", api.Action{Num: 2}); code != 200 {
		t.Fatal(code)
	}
	if want := "claude --resume fixed-uuid --permission-mode acceptEdits --model opus"; !strings.Contains(calls(f), want) {
		t.Fatalf("resume must reuse the session's own values; calls:\n%s", calls(f))
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
	s.App.GH = func(context.Context, string, ...string) (string, error) { return "a/b\nc/d\n", nil }
	repos, code := get[[]string](t, ts, "/api/github/repos")
	if code != 200 || strings.Join(repos, ",") != "a/b,c/d" {
		t.Fatalf("%d %q", code, repos)
	}
}

func TestResumeAllAtStartup(t *testing.T) {
	s, f, _ := newServer(t)
	p, _ := s.App.LoadProject("home-network")
	p.AddSession(&state.Session{UUID: "u2", Title: "gone"})
	p.AddSession(&state.Session{UUID: "u3", Title: "stopped", Done: true})
	s.App.SaveProject(p)
	s.ResumeAll(context.Background())
	if !strings.Contains(calls(f), "new-session -d -s bx-home-network-2 -c "+p.Path+" -P -F #{session_id} claude --resume u2") {
		t.Fatalf("calls:\n%s", calls(f))
	}
	if strings.Contains(calls(f), "claude --resume u1") {
		t.Fatal("resumed a running session")
	}
	if strings.Contains(calls(f), "--resume u3") {
		t.Fatal("resumed a done session")
	}
}

func TestStopAndDeleteGoThroughApp(t *testing.T) {
	_, f, ts := newServer(t)
	if res, code := post(t, ts, "/api/projects/home-network/stop", api.Action{Num: 1}); code != 200 {
		t.Fatalf("%d %+v", code, res)
	}
	if !strings.Contains(calls(f), "kill-session -t $5") {
		t.Fatalf("calls:\n%s", calls(f))
	}
	snap, _ := get[api.Snapshot](t, ts, "/api/projects/home-network")
	if !snap.Project.Sessions[1].Done {
		t.Fatalf("%+v", snap.Project.Sessions[1])
	}
}

func TestDeleteRunningThenForce(t *testing.T) {
	_, f, ts := newServer(t)
	res, code := post(t, ts, "/api/projects/home-network/delete", api.Action{Num: 1})
	if code != 409 || !res.Running || res.Detail != "" || res.Error == "" {
		t.Fatalf("%d %+v", code, res)
	}
	if strings.Contains(calls(f), "kill-session") {
		t.Fatalf("a refused delete killed:\n%s", calls(f))
	}
	if res, code := post(t, ts, "/api/projects/home-network/delete", api.Action{Num: 1, Force: true}); code != 200 {
		t.Fatalf("%d %+v", code, res)
	}
	if !strings.Contains(calls(f), "kill-session -t $5") {
		t.Fatalf("calls:\n%s", calls(f))
	}
	snap, _ := get[api.Snapshot](t, ts, "/api/projects/home-network")
	if snap.Project.Sessions[1] != nil {
		t.Fatalf("%+v", snap.Project.Sessions)
	}
}

func TestStopKillFailureKeepsSessionNotDone(t *testing.T) {
	_, f, ts := newServer(t)
	f.Err = errors.New("boom")
	if res, code := post(t, ts, "/api/projects/home-network/stop", api.Action{Num: 1}); code == 200 {
		t.Fatalf("%d %+v", code, res)
	}
	snap, _ := get[api.Snapshot](t, ts, "/api/projects/home-network")
	if snap.Project.Sessions[1].Done {
		t.Fatal("a failed stop left the session done")
	}
}

func TestWatchMarksExitedSessionsDone(t *testing.T) {
	s, f, _ := newServer(t)
	s.watchOnce()
	f.Replies[listSessions] = "" // tmux answers, Claude exited
	s.watchOnce()
	p, _ := s.App.LoadProject("home-network")
	if !p.Sessions[1].Done {
		t.Fatal("an exited session is not done")
	}
}

func TestWatchMarksDoneASessionThatExitsBeforeTheFirstTick(t *testing.T) {
	s, _, ts := newServer(t)
	p, _ := s.App.LoadProject("home-network")
	p.AddSession(&state.Session{UUID: "u2", Title: "gone"})                // 2: ResumeAll
	p.AddSession(&state.Session{UUID: "u3", Title: "stopped", Done: true}) // 3: r
	if err := s.App.SaveProject(p); err != nil {
		t.Fatal(err)
	}
	s.ResumeAll(context.Background())
	if res, code := post(t, ts, "/api/projects/home-network/resume", api.Action{Num: 3}); code != 200 {
		t.Fatalf("resume: %d %+v", code, res)
	}
	if res, code := post(t, ts, "/api/projects/home-network/sessions", api.Action{Story: "s1"}); code != 200 || res.Num != 4 {
		t.Fatalf("new: %d %+v", code, res)
	}
	s.watchOnce() // tmux answers with session 1 only: 2, 3 and 4 exited at once
	p, _ = s.App.LoadProject("home-network")
	for _, n := range []int{2, 3, 4} {
		if !p.Sessions[n].Done {
			t.Errorf("session %d exited before the first tick and is not done", n)
		}
	}
	if p.Sessions[1].Done {
		t.Fatal("a running session is done")
	}
}

func TestWatchIgnoresTmuxServerGone(t *testing.T) {
	s, f, _ := newServer(t)
	s.watchOnce()
	delete(f.Replies, listSessions)
	f.Errs = map[string]error{listSessions: errors.New("tmux list-sessions: no server running on /x")}
	s.watchOnce()
	p, _ := s.App.LoadProject("home-network")
	if p.Sessions[1].Done {
		t.Fatal("a session lost with the tmux server is done")
	}
	delete(f.Errs, listSessions)
	f.Replies[listSessions] = "" // a new tmux server, without the session
	s.watchOnce()
	p, _ = s.App.LoadProject("home-network")
	if p.Sessions[1].Done {
		t.Fatal("a session lost with the old tmux server is done after a new one started")
	}
}

func TestWorkspaceStories(t *testing.T) {
	s, _, ts := newServer(t)
	root := filepath.Join(t.TempDir(), "work")
	for _, rel := range []string{"api", "web"} {
		dir := filepath.Join(root, rel)
		if err := s.App.Git.Init(dir); err != nil {
			t.Fatal(err)
		}
	}
	if res, code := post(t, ts, "/api/projects", api.NewProject{Kind: "folder", Path: root}); code != 200 || res.Name != "work" {
		t.Fatalf("%d %+v", code, res)
	}
	repos, code := get[[]string](t, ts, "/api/projects/work/repos")
	if code != 200 || strings.Join(repos, " ") != "api web" {
		t.Fatalf("%d %v", code, repos)
	}
	if _, code := get[[]string](t, ts, "/api/projects/home-network/repos"); code != 400 {
		t.Fatalf("repos of a repo project: %d", code)
	}
	if res, code := post(t, ts, "/api/projects/work/stories", api.Action{Name: "login", Repos: []string{"api"}}); code != 200 {
		t.Fatalf("%d %+v", code, res)
	}
	os.WriteFile(filepath.Join(root, "web", "wip.txt"), []byte("x\n"), 0o644)
	res, code := post(t, ts, "/api/projects/work/stories/repos", api.Action{Name: "login", Repos: []string{"web"}})
	if code != 409 || res.Dirty != 1 || res.Detail != "web: 1" {
		t.Fatalf("%d %+v", code, res)
	}
	if res, code := post(t, ts, "/api/projects/work/stories/repos", api.Action{Name: "login", Repos: []string{"web"}, Force: true}); code != 200 {
		t.Fatalf("%d %+v", code, res)
	}
	os.WriteFile(filepath.Join(s.Cfg.WorktreeDir("work", "login"), "api", "a.txt"), []byte("x\n"), 0o644)
	snap, code := get[api.Snapshot](t, ts, "/api/projects/work")
	if code != 200 || !snap.Project.IsWorkspace() || snap.Live.Branch != "" || snap.Live.Diff["login"].Dirty != 1 ||
		strings.Join(snap.Project.Stories["login"].RepoNames(), " ") != "api web" {
		t.Fatalf("%d %+v", code, snap)
	}
	res, code = post(t, ts, "/api/projects/work/stories/close", api.Action{Name: "login"})
	if code != 409 || res.Detail != "api: 1" {
		t.Fatalf("%d %+v", code, res)
	}
}

func TestSnapshotSessionStates(t *testing.T) {
	s, f, ts := newServer(t)
	s.Lister = fakeLister{[]claude.Agent{
		{SessionID: "u1", Status: "waiting"},
		{SessionID: "u4", Status: "busy"},
		{SessionID: "u5", Status: "idle"},
		{SessionID: "u7", Status: "busy"},
	}}
	p, err := s.App.LoadProject("home-network")
	if err != nil {
		t.Fatal(err)
	}
	p.AddSession(&state.Session{Story: "s1", UUID: "u2", Title: "finished", Done: true}) // 2
	p.AddSession(&state.Session{UUID: "u3", Title: "lost"})                              // 3: no agent, no tmux
	p.AddSession(&state.Session{UUID: "u4", Title: "busy"})                              // 4
	p.AddSession(&state.Session{UUID: "u5", Title: "quiet"})                             // 5
	p.AddSession(&state.Session{UUID: "u6", Title: "new"})                               // 6: tmux, no agent yet
	p.AddSession(&state.Session{UUID: "u7", Title: "detached"})                          // 7: agent, no tmux
	if err := s.App.SaveProject(p); err != nil {
		t.Fatal(err)
	}
	gone := api.SessionState{State: "gone", Text: "gone · ⏎ to resume"}
	for _, c := range []struct {
		name string
		tmux *string // the list-sessions reply; nil: tmux fails
		want map[int]api.SessionState
	}{
		{"tmux known", ptr("$5\tbx-home-network-1\n$6\tbx-home-network-4\n$7\tbx-home-network-5\n$8\tbx-home-network-6\n"),
			map[int]api.SessionState{
				1: {State: "waiting", Text: "asks: needs you"},
				2: {State: "done", Text: "done · ⏎ to resume"},
				3: gone,
				4: {State: "running", Text: "running"},
				5: {State: "idle", Text: "idle"},
				6: {State: "starting", Text: "starting"},
				7: gone,
			}},
		{"tmux failing", nil, // the agents alone decide, as in the TUI
			map[int]api.SessionState{
				1: {State: "waiting", Text: "asks: needs you"},
				2: {State: "done", Text: "done · ⏎ to resume"},
				3: gone,
				4: {State: "running", Text: "running"},
				5: {State: "idle", Text: "idle"},
				6: gone,
				7: {State: "running", Text: "running"},
			}},
	} {
		if c.tmux == nil {
			delete(f.Replies, listSessions)
		} else {
			f.Replies[listSessions] = *c.tmux
		}
		snap, code := get[api.Snapshot](t, ts, "/api/projects/home-network")
		if code != 200 {
			t.Fatal(code)
		}
		if len(snap.SessionStates) != len(c.want) {
			t.Fatalf("%s: %+v", c.name, snap.SessionStates)
		}
		for n, w := range c.want {
			if snap.SessionStates[n] != w {
				t.Errorf("%s: session %d: got %+v, want %+v", c.name, n, snap.SessionStates[n], w)
			}
		}
	}
}

func ptr[T any](v T) *T { return &v }

// failingLister lists its agents until fail is set, then errors.
type failingLister struct {
	agents []claude.Agent
	fail   atomic.Bool
}

func (f *failingLister) Agents(context.Context) ([]claude.Agent, error) {
	if f.fail.Load() {
		return nil, errors.New("claude agents: exit 1")
	}
	return f.agents, nil
}

func TestSnapshotKeepsTheLastAgents(t *testing.T) {
	s, _, ts := newServer(t)
	l := &failingLister{agents: []claude.Agent{{SessionID: "u1", Status: "waiting"}}}
	s.Lister = l
	if snap, _ := get[api.Snapshot](t, ts, "/api/projects/home-network"); snap.SessionStates[1].State != "waiting" {
		t.Fatalf("first poll: %+v", snap.SessionStates)
	}
	l.fail.Store(true)
	s.mu.Lock()
	s.agentsAt = time.Time{} // past the 1 s cache
	s.mu.Unlock()
	snap, _ := get[api.Snapshot](t, ts, "/api/projects/home-network")
	if got, want := snap.SessionStates[1], (api.SessionState{State: "waiting", Text: "asks: needs you ?"}); got != want || !snap.Live.AgentsErr {
		t.Fatalf("lister failing: %+v (agents_err %v), want %+v", got, snap.Live.AgentsErr, want)
	}
	s.mu.Lock()
	s.agentsAt = time.Time{}
	s.mu.Unlock()
	list, _ := get[[]api.ProjectSummary](t, ts, "/api/projects")
	if len(list) != 1 || list[0].Waiting != 1 {
		t.Fatalf("projects with the lister failing: %+v", list)
	}
}

func TestWebAppAndAPIShareTheMux(t *testing.T) {
	_, _, ts := newServer(t)
	r, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(r.Body)
	_ = r.Body.Close()
	if r.StatusCode != 200 || !strings.Contains(string(b), "<title>bx</title>") {
		t.Fatalf("GET /: %d %.80s", r.StatusCode, b)
	}
	if list, code := get[[]api.ProjectSummary](t, ts, "/api/projects"); code != 200 || len(list) != 1 {
		t.Fatalf("GET /api/projects: %d %+v", code, list)
	}
}

func TestCleanupScanAndApply(t *testing.T) {
	s, f, ts := newServer(t)
	p, err := s.App.LoadProject("home-network")
	if err != nil {
		t.Fatal(err)
	}
	p.AddSession(&state.Session{UUID: "u2", Title: "old", Done: true})
	if err := s.App.SaveProject(p); err != nil {
		t.Fatal(err)
	}
	f.Replies[listSessions] = "$5\tbx-home-network-1\n$9\tbx-home-network-7\n"
	c, code := get[api.Cleanup](t, ts, "/api/cleanup")
	if code != 200 || len(c.Items) != 2 || c.Items[0].ID != "tmux:bx-home-network-7" || c.Items[1].ID != "session:home-network:2" {
		t.Fatalf("%d %+v", code, c)
	}
	b, _ := json.Marshal(api.CleanupRequest{IDs: []string{c.Items[0].ID, c.Items[1].ID}})
	r, err := http.Post(ts.URL+"/api/cleanup", "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	var res api.CleanupResult
	_ = json.NewDecoder(r.Body).Decode(&res)
	_ = r.Body.Close()
	if r.StatusCode != 200 || res.Removed != 2 {
		t.Fatalf("%d %+v", r.StatusCode, res)
	}
	if !strings.Contains(calls(f), "kill-session -t $9") || strings.Contains(calls(f), "kill-session -t $5") {
		t.Fatalf("calls:\n%s", calls(f))
	}
	snap, _ := get[api.Snapshot](t, ts, "/api/projects/home-network")
	if snap.Project.Sessions[2] != nil || snap.Project.Sessions[1] == nil {
		t.Fatalf("%+v", snap.Project.Sessions)
	}
	r, err = http.Post(ts.URL+"/api/cleanup", "application/json", strings.NewReader("{"))
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Body.Close()
	if r.StatusCode != 400 {
		t.Fatalf("bad JSON: %d", r.StatusCode)
	}
}

func TestCleanupRefusesWhenAProjectFailsToLoad(t *testing.T) {
	s, f, ts := newServer(t)
	// written by a newer bx: this one can't read it, so its live session must not look orphaned
	if err := os.WriteFile(s.Cfg.StateFile("dotfiles"), []byte(`{"version": 99}`), 0o644); err != nil {
		t.Fatal(err)
	}
	f.Replies[listSessions] = "$5\tbx-home-network-1\n$9\tbx-dotfiles-1\n"
	if c, code := get[api.Cleanup](t, ts, "/api/cleanup"); code != 500 {
		t.Fatalf("scan: %d %+v", code, c)
	}
	if res, code := post(t, ts, "/api/cleanup", api.CleanupRequest{IDs: []string{"tmux:bx-dotfiles-1"}}); code != 500 || !strings.Contains(res.Error, "dotfiles") {
		t.Fatalf("apply: %d %+v", code, res)
	}
	if strings.Contains(calls(f), "kill-session") {
		t.Fatalf("calls:\n%s", calls(f))
	}
}

func TestKeepTmuxServer(t *testing.T) {
	s, f, _ := newServer(t)
	var logged []string
	s.Log = func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) }
	const keep = "set-option -s exit-empty off"

	f.Errs = map[string]error{keep: errors.New("tmux set-option: no server running on /x")}
	s.KeepTmuxServer()
	if len(logged) != 0 {
		t.Fatalf("no server logged: %q", logged)
	}

	f.Errs[keep] = errors.New("tmux set-option: boom")
	s.KeepTmuxServer()
	if len(logged) != 1 || !strings.Contains(logged[0], "tmux: keep server: tmux set-option: boom") {
		t.Fatalf("log: %q", logged)
	}

	delete(f.Errs, keep)
	f.Calls = nil
	s.KeepTmuxServer()
	if calls(f) != keep || len(logged) != 1 {
		t.Fatalf("calls %q log %q", calls(f), logged)
	}
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
	c.Dir = dir
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// withOrigin gives the project repo a bare origin holding main and develop, HEAD develop, and
// returns a clone of it that pushes there.
func withOrigin(t *testing.T, repo string) (seed string) {
	t.Helper()
	root := t.TempDir()
	bare, seed := filepath.Join(root, "origin.git"), filepath.Join(root, "seed")
	gitIn(t, root, "init", "-q", "--bare", bare)
	gitIn(t, repo, "branch", "develop")
	gitIn(t, repo, "remote", "add", "origin", bare)
	gitIn(t, repo, "push", "-q", "origin", "main", "develop")
	gitIn(t, bare, "symbolic-ref", "HEAD", "refs/heads/develop")
	gitIn(t, repo, "remote", "set-head", "origin", "--auto")
	gitIn(t, root, "clone", "-q", bare, seed)
	return seed
}

func TestNewStoryStartsASessionOnTheRemoteDefault(t *testing.T) {
	s, f, ts := newServer(t)
	withOrigin(t, s.Cfg.ProjectDir("home-network"))
	res, code := post(t, ts, "/api/projects/home-network/stories", api.Action{Name: "Login Timeout", Type: "fix"})
	if code != 200 || res.Story != "login-timeout" || res.Num != 2 || res.SessionError != "" ||
		res.Message != "Created login-timeout on fix/login-timeout ← origin/develop" {
		t.Fatalf("%d %+v", code, res)
	}
	wt := s.Cfg.WorktreeDir("home-network", "login-timeout")
	if !strings.Contains(calls(f), "new-session -d -s bx-home-network-2 -c "+wt+" ") {
		t.Fatalf("calls:\n%s", calls(f))
	}
	p, _ := s.App.LoadProject("home-network")
	if st := p.Stories["login-timeout"]; st == nil || st.Base != "origin/develop" || p.LastType != "fix" || p.Sessions[2].Story != "login-timeout" {
		t.Fatalf("%+v", p)
	}
	s.mu.Lock()
	started := s.started["home-network"][2]
	s.mu.Unlock()
	if !started {
		t.Fatal("the new session is not marked started")
	}
}

func TestNewStoryPreparesOutsideTheWriteLock(t *testing.T) {
	s, _, ts := newServer(t)
	fired := make(chan struct{})
	s.prepared = func() { close(fired) }
	s.write.Lock()
	done := make(chan int)
	go func() {
		r, err := http.Post(ts.URL+"/api/projects/home-network/stories", "application/json", strings.NewReader(`{"name":"s2"}`))
		if err != nil {
			done <- 0
			return
		}
		_ = r.Body.Close()
		done <- r.StatusCode
	}()
	select {
	case <-fired: // PrepareStory ran while the lock was held here
	case <-time.After(10 * time.Second):
		s.write.Unlock()
		t.Fatal("PrepareStory waited for the write lock")
	}
	select {
	case code := <-done:
		t.Fatalf("created under someone else's lock: %d", code)
	case <-time.After(100 * time.Millisecond):
	}
	s.write.Unlock()
	if code := <-done; code != 200 {
		t.Fatalf("code %d", code)
	}
}

func TestNewStoryExistsThenUseExisting(t *testing.T) {
	s, _, ts := newServer(t)
	seed := withOrigin(t, s.Cfg.ProjectDir("home-network"))
	gitIn(t, seed, "checkout", "-q", "-b", "feat/x")
	gitIn(t, seed, "push", "-q", "origin", "feat/x")
	res, code := post(t, ts, "/api/projects/home-network/stories", api.Action{Name: "x"})
	if code != 409 || res.Exists != "origin" || res.Branch != "feat/x" || res.Detail != "" || res.Error != "branch feat/x exists on origin" {
		t.Fatalf("%d %+v", code, res)
	}
	if p, _ := s.App.LoadProject("home-network"); p.Stories["x"] != nil {
		t.Fatal("a refused story was recorded")
	}
	res, code = post(t, ts, "/api/projects/home-network/stories", api.Action{Name: "x", UseExisting: true})
	if code != 200 || res.Story != "x" || res.Message != "Created x on feat/x" {
		t.Fatalf("%d %+v", code, res)
	}
}

func TestNewStorySessionFailureKeepsTheStory(t *testing.T) {
	s, f, ts := newServer(t)
	f.Err = errors.New("tmux: boom")
	res, code := post(t, ts, "/api/projects/home-network/stories", api.Action{Name: "y"})
	if code != 200 || res.Story != "y" || res.Num != 0 || !strings.Contains(res.SessionError, "boom") || res.Message != "Created y on feat/y ← main" {
		t.Fatalf("%d %+v", code, res)
	}
	if p, _ := s.App.LoadProject("home-network"); p.Stories["y"] == nil {
		t.Fatal("story lost with its session")
	}
}

func TestStoryInfoBranchesAndPullRequests(t *testing.T) {
	s, _, ts := newServer(t)
	withOrigin(t, s.Cfg.ProjectDir("home-network"))
	info, code := get[api.StoryInfo](t, ts, "/api/projects/home-network/storyinfo")
	if code != 200 || info.Base != "origin/develop" || info.Detected != "origin/develop" || len(info.Types) != 7 {
		t.Fatalf("%d %+v", code, info)
	}
	if _, code := get[api.StoryInfo](t, ts, "/api/projects/nope/storyinfo"); code != 404 {
		t.Fatalf("unknown project: %d", code)
	}
	bl, code := get[api.BranchList](t, ts, "/api/projects/home-network/branches?fetch=1")
	names := map[string]api.Branch{}
	for _, b := range bl.Branches {
		names[b.Name] = b
	}
	// main (the project folder's) and feat/s1 (story s1's) are checked out: listed, marked
	if code != 200 || bl.Note != "" || !names["develop"].Remote || !names["develop"].Local || names["develop"].CheckedOut ||
		!names["feat/s1"].Local || names["feat/s1"].Remote || !names["feat/s1"].CheckedOut || !names["main"].CheckedOut || names["main"].Time == 0 {
		t.Fatalf("%d %+v", code, bl)
	}
	var dirs []string
	s.App.GH = func(_ context.Context, dir string, args ...string) (string, error) {
		dirs = append(dirs, dir)
		if strings.Contains(strings.Join(args, " "), "--search") {
			return "", errors.New("gh pr list: acme requires SSO authorization")
		}
		return `[{"number":12,"title":"Fix login","author":{"login":"alice"},"headRefName":"fix/login","isCrossRepository":true,"headRepositoryOwner":{"login":"bob"},"state":"OPEN"}]`, nil
	}
	prs, code := get[api.PRList](t, ts, "/api/projects/home-network/prs")
	if code != 200 || len(prs.PRs) != 1 || prs.PRs[0] != (api.PR{Number: 12, Title: "Fix login", Author: "alice", Branch: "fix/login", Owner: "bob", State: "OPEN", Fork: true}) ||
		len(dirs) != 1 || dirs[0] != s.Cfg.ProjectDir("home-network") {
		t.Fatalf("%d %+v %q", code, prs, dirs)
	}
	prs, code = get[api.PRList](t, ts, "/api/projects/home-network/prs?q=login")
	if code != 200 || len(prs.PRs) != 0 || len(prs.Errors) != 1 || !strings.Contains(prs.Errors[0], "SSO") {
		t.Fatalf("gh failure: %d %+v", code, prs)
	}
}

func TestPullRequestsStopGHWhenTheClientGoes(t *testing.T) {
	s, _, ts := newServer(t)
	for _, path := range []string{"/api/projects/home-network/prs", "/api/projects/home-network/stories"} {
		killed := make(chan struct{})
		s.App.GH = func(ctx context.Context, _ string, _ ...string) (string, error) {
			select {
			case <-ctx.Done(): // the request's context: the client gave up
				close(killed)
				return "", ctx.Err()
			case <-time.After(5 * time.Second):
				return "", errors.New("gh: never cancelled")
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		method, body := http.MethodGet, ""
		if strings.HasSuffix(path, "stories") {
			method, body = http.MethodPost, `{"source":"pr","pr":7}`
		}
		req, _ := http.NewRequestWithContext(ctx, method, ts.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if res, err := http.DefaultClient.Do(req); err == nil {
			_ = res.Body.Close()
		}
		cancel()
		select {
		case <-killed:
		case <-time.After(10 * time.Second):
			t.Fatalf("%s: gh kept running after the client went", path)
		}
	}
}

func TestBaseOverride(t *testing.T) {
	s, _, ts := newServer(t)
	withOrigin(t, s.Cfg.ProjectDir("home-network"))
	if res, code := post(t, ts, "/api/projects/home-network/base", api.Action{Base: "nope"}); code != 400 || res.Error == "" {
		t.Fatalf("%d %+v", code, res)
	}
	if _, code := post(t, ts, "/api/projects/home-network/base", api.Action{Base: "main"}); code != 200 {
		t.Fatal(code)
	}
	list, _ := get[[]api.ProjectSummary](t, ts, "/api/projects")
	if len(list) != 1 || list[0].BaseOverride != "main" {
		t.Fatalf("%+v", list)
	}
	if info, _ := get[api.StoryInfo](t, ts, "/api/projects/home-network/storyinfo"); info.Base != "origin/main" || info.Override != "main" {
		t.Fatalf("%+v", info)
	}
	if _, code := post(t, ts, "/api/projects/home-network/base", api.Action{}); code != 200 {
		t.Fatal(code)
	}
	list, _ = get[[]api.ProjectSummary](t, ts, "/api/projects")
	if list[0].BaseOverride != "" {
		t.Fatalf("%+v", list)
	}
}

func TestDiffUsesTheRecordedBase(t *testing.T) {
	s, _, ts := newServer(t)
	seed := withOrigin(t, s.Cfg.ProjectDir("home-network"))
	if err := os.WriteFile(filepath.Join(seed, "dev.txt"), []byte("1\n2\n3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, seed, "add", ".")
	gitIn(t, seed, "commit", "-qm", "dev")
	gitIn(t, seed, "push", "-q", "origin", "develop")
	if res, code := post(t, ts, "/api/projects/home-network/stories", api.Action{Name: "login"}); code != 200 {
		t.Fatalf("%d %+v", code, res)
	}
	wt := s.Cfg.WorktreeDir("home-network", "login")
	if err := os.WriteFile(filepath.Join(wt, "mine.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, wt, "add", ".")
	gitIn(t, wt, "commit", "-qm", "mine")
	// against main (the old default) the story would also count develop's 3 lines
	snap, _ := get[api.Snapshot](t, ts, "/api/projects/home-network")
	if d := snap.Live.Diff["login"]; d.Add != 1 {
		t.Fatalf("diff %+v", d)
	}
}

func TestWatchKeepsStartedSessionsWhenTmuxFails(t *testing.T) {
	s, f, ts := newServer(t)
	if res, code := post(t, ts, "/api/projects/home-network/sessions", api.Action{Story: "s1"}); code != 200 || res.Num != 2 {
		t.Fatalf("new: %d %+v", code, res)
	}
	f.Errs = map[string]error{listSessions: errors.New("tmux list-sessions: boom")}
	s.watchOnce()
	p, _ := s.App.LoadProject("home-network")
	if p.Sessions[2].Done {
		t.Fatal("a session started before a failed watch is done")
	}
	delete(f.Errs, listSessions)
	f.Replies[listSessions] = "$5\tbx-home-network-1\n" // session 2 exited while tmux failed
	s.watchOnce()
	p, _ = s.App.LoadProject("home-network")
	if !p.Sessions[2].Done {
		t.Fatal("a started session the failed watch could not check is not done")
	}
	if p.Sessions[1].Done {
		t.Fatal("a running session is done")
	}
}

func TestShellRoutes(t *testing.T) {
	s, f, ts := newServer(t)
	repo := s.Cfg.ProjectDir("home-network")
	wt := s.Cfg.WorktreeDir("home-network", "s1")
	newSession := "new-session -d -s bxsh-home-network-s1-1 -c " + wt + " -e PAGER=cat -e GIT_PAGER=cat -P -F #{session_id}"
	f.Replies[newSession] = "$9\n"
	res, code := post(t, ts, "/api/projects/home-network/shells", api.Action{Story: "s1"})
	if code != 200 || res.Story != "s1" || res.K != 1 {
		t.Fatalf("new: %d %+v", code, res)
	}
	if !strings.Contains(calls(f), newSession) || !strings.Contains(calls(f), "set-option -t $9 @bx_shell 1") {
		t.Fatalf("calls:\n%s", calls(f))
	}
	if _, code := post(t, ts, "/api/projects/home-network/shells", api.Action{Story: "nope"}); code != 400 {
		t.Fatalf("unknown story: %d", code)
	}
	f.Replies[listShells] = "$9\tbxsh-home-network-s1-1\thome-network\ts1\t1\tdev\tnpm\n"
	snap, code := get[api.Snapshot](t, ts, "/api/projects/home-network")
	if code != 200 || len(snap.Shells) != 1 || snap.Shells[0] != (api.Shell{Story: "s1", K: 1, Title: "dev", Command: "npm"}) {
		t.Fatalf("%d %+v", code, snap.Shells)
	}
	if snap.Dirs[""] != repo || snap.Dirs["s1"] != wt {
		t.Fatalf("dirs: %+v", snap.Dirs)
	}
	if _, code := post(t, ts, "/api/projects/home-network/shells/rename", api.Action{Story: "s1", K: 1, Title: "api"}); code != 200 {
		t.Fatalf("rename: %d", code)
	}
	if !strings.Contains(calls(f), "set-option -t $9 @bx_title api") {
		t.Fatalf("calls:\n%s", calls(f))
	}
	if _, code := post(t, ts, "/api/projects/home-network/shells/close", api.Action{Story: "s1", K: 1}); code != 200 {
		t.Fatalf("close: %d", code)
	}
	if !strings.Contains(calls(f), "kill-session -t $9") {
		t.Fatalf("calls:\n%s", calls(f))
	}
	if _, code := post(t, ts, "/api/projects/home-network/shells/close", api.Action{Story: "s1", K: 4}); code != http.StatusGone {
		t.Fatalf("close a gone shell: %d", code)
	}
}

func TestSnapshotWithoutShells(t *testing.T) {
	_, f, ts := newServer(t)
	f.Errs = map[string]error{listShells: errors.New("tmux list-sessions: boom")}
	r, err := http.Get(ts.URL + "/api/projects/home-network")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Body.Close() }()
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil || r.StatusCode != 200 {
		t.Fatalf("%d %v", r.StatusCode, err)
	}
	if string(raw["shells"]) != "[]" {
		t.Fatalf("shells = %s, want []", raw["shells"])
	}
}

func TestMCPModeOnlyFromTheTUI(t *testing.T) {
	_, _, ts := newServer(t)
	if _, res, code := put(t, ts, "/api/settings", api.Settings{Values: map[string]string{"mcp.mode": "off"}}); code != 400 || res.Error != "mcp.mode can only be changed from the TUI" {
		t.Fatalf("web: %d %q", code, res.Error)
	}
	if out, _, code := put(t, ts, "/api/settings?tui=1", api.Settings{Values: map[string]string{"mcp.mode": "strict"}}); code != 200 || out.Values["mcp.mode"] != "strict" {
		t.Fatalf("tui: %d %v", code, out.Values)
	}
}

func TestGitAccountsOnlyFromTheTUI(t *testing.T) {
	s, _, ts := newServer(t)
	if _, code := get[api.Result](t, ts, "/api/git/accounts"); code != 403 {
		t.Fatalf("web GET: %d", code)
	}
	s.App.GH = func(_ context.Context, _ string, args ...string) (string, error) {
		return `{"hosts":{"github.com":[{"state":"success","host":"github.com","login":"huy-acme"}]}}`, nil
	}
	got, code := get[api.GitAccounts](t, ts, "/api/git/accounts?tui=1")
	if code != 200 || !got.Config.Empty() || len(got.Logins) != 1 || len(got.Projects) != 1 {
		t.Fatalf("empty: %d %+v", code, got)
	}
	c := accounts.Config{Default: "work", Accounts: []accounts.Account{{ID: "work", Login: "huy-acme"}}}
	var out api.GitAccounts
	if code := putJSON(t, ts, "/api/git/accounts?tui=1", c, &out); code != 200 || out.Config.Default != "work" || len(out.Projects) != 1 {
		t.Fatalf("put: %d %+v", code, out)
	}
	c.Default = "nope"
	var res api.Result
	if code := putJSON(t, ts, "/api/git/accounts?tui=1", c, &res); code != 400 || !strings.Contains(res.Error, `no account "nope"`) {
		t.Fatalf("bad put: %d %+v", code, res)
	}
	if code := putJSON(t, ts, "/api/git/accounts", c, &res); code != 403 {
		t.Fatalf("web put: %d", code)
	}
}

func putJSON(t *testing.T, ts *httptest.Server, path string, body, out any) int {
	t.Helper()
	data, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPut, ts.URL+path, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	_ = json.NewDecoder(r.Body).Decode(out)
	return r.StatusCode
}

func TestSyncAsksWhileClaudeWorksAndShowsBehind(t *testing.T) {
	s, _, ts := newServer(t)
	repo := s.Cfg.ProjectDir("home-network")
	seed := withOrigin(t, repo)
	s.Lister = fakeLister{[]claude.Agent{{SessionID: "u1", Status: "busy"}}}
	res, code := post(t, ts, "/api/projects/home-network/sync", api.Action{Story: "s1"})
	if code != 409 || !res.Running || res.Num != 1 || res.Error != "Claude is working in s1 (session [1])" {
		t.Fatalf("busy: %d %+v", code, res)
	}
	res, code = post(t, ts, "/api/projects/home-network/sync", api.Action{Story: "s1", Force: true})
	if code != 200 || res.Message != "s1 isn't on origin yet · nothing to sync" {
		t.Fatalf("force: %d %+v", code, res)
	}

	// someone pushed main: ↓1 from the last fetch, then synced
	gitIn(t, seed, "checkout", "-q", "main")
	if err := os.WriteFile(filepath.Join(seed, "new.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, seed, "add", ".")
	gitIn(t, seed, "commit", "-qm", "new")
	gitIn(t, seed, "push", "-q", "origin", "main")
	gitIn(t, repo, "fetch", "-q", "origin")
	if snap, _ := get[api.Snapshot](t, ts, "/api/projects/home-network"); snap.Live.Behind != 1 {
		t.Fatalf("behind %d", snap.Live.Behind)
	}
	res, code = post(t, ts, "/api/projects/home-network/sync", api.Action{Story: ""})
	if code != 200 || res.Message != "main synced · 1 new commit from origin" {
		t.Fatalf("chats: %d %+v", code, res)
	}
	if snap, _ := get[api.Snapshot](t, ts, "/api/projects/home-network"); snap.Live.Behind != 0 {
		t.Fatalf("behind after sync %d", snap.Live.Behind)
	}
}

func TestLinkedWorkspace(t *testing.T) {
	s, _, ts := newServer(t)
	for _, n := range []string{"api", "web"} {
		dir := s.Cfg.ProjectDir(n)
		if err := s.App.Git.Init(dir); err != nil {
			t.Fatal(err)
		}
		if res, code := post(t, ts, "/api/projects", api.NewProject{Kind: "folder", Path: dir}); code != 200 || res.Name != n {
			t.Fatalf("%d %+v", code, res)
		}
	}
	if res, code := post(t, ts, "/api/projects", api.NewProject{Kind: "workspace", Name: "shop", Projects: []string{"api"}}); code != 400 {
		t.Fatalf("one project: %d %+v", code, res)
	}
	if res, code := post(t, ts, "/api/projects", api.NewProject{Kind: "workspace", Name: "shop", Projects: []string{"api", "web"}}); code != 200 || res.Name != "shop" {
		t.Fatalf("%d %+v", code, res)
	}
	sums, _ := get[[]api.ProjectSummary](t, ts, "/api/projects")
	var shop api.ProjectSummary
	for _, p := range sums {
		if p.Name == "shop" {
			shop = p
		}
	}
	if shop.Kind != "workspace" {
		t.Fatalf("%+v", shop)
	}
	links, code := get[[]api.Link](t, ts, "/api/projects/shop/links")
	if code != 200 || len(links) != 2 || links[0].Name != "api" {
		t.Fatalf("%d %+v", code, links)
	}
	if res, code := post(t, ts, "/api/projects/shop/stories", api.Action{Name: "login", Repos: []string{"api"}}); code != 200 {
		t.Fatalf("%d %+v", code, res)
	}
	if res, code := post(t, ts, "/api/projects/shop/links", api.Action{Unlink: []string{"api"}}); code != 409 || !strings.Contains(res.Error, "login") {
		t.Fatalf("%d %+v", code, res)
	}
	if res, code := post(t, ts, "/api/projects/shop/links", api.Action{Unlink: []string{"web"}}); code != 200 {
		t.Fatalf("%d %+v", code, res)
	}
	if repos, _ := get[[]string](t, ts, "/api/projects/shop/repos"); strings.Join(repos, " ") != "api" {
		t.Fatalf("%v", repos)
	}
	if res, code := post(t, ts, "/api/projects/home-network/links", api.Action{Link: []string{"api"}}); code != 400 {
		t.Fatalf("%d %+v", code, res)
	}
}
