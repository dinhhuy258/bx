// Package servetest builds a bx serve Server over a fake tmux and a real git repo, for tests of
// the packages that talk to it (the in-package serve tests keep their own copy: they need
// unexported fields, and importing this package from them would be an import cycle).
package servetest

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/dinhhuy258/bx/internal/app"
	"github.com/dinhhuy258/bx/internal/claude"
	"github.com/dinhhuy258/bx/internal/config"
	"github.com/dinhhuy258/bx/internal/git"
	"github.com/dinhhuy258/bx/internal/serve"
	"github.com/dinhhuy258/bx/internal/settings"
	"github.com/dinhhuy258/bx/internal/state"
	"github.com/dinhhuy258/bx/internal/tmux"
)

type fakeLister struct{ agents []claude.Agent }

func (f fakeLister) Agents(context.Context) ([]claude.Agent, error) { return f.agents, nil }

// New builds a Server over a fake tmux and a real git repo, with one project that has one story and one session.
func New(t *testing.T) (*serve.Server, *tmux.Fake, *httptest.Server) {
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
	f := &tmux.Fake{Replies: map[string]string{
		"list-sessions -F #{session_id}\t#{session_name}": "$5\tbx-home-network-1\n",
		"list-sessions -F " + tmux.ShellFormat:            "",
	}}
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
	s := serve.New(a, cfg, fakeLister{[]claude.Agent{{SessionID: "u1", Status: "waiting"}}}, func(string, ...any) {})
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return s, f, ts
}

// DirtyStory writes one uncommitted file into story s1's worktree.
func DirtyStory(t *testing.T, s *serve.Server) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(s.Cfg.WorktreeDir("home-network", "s1"), "wip.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}
