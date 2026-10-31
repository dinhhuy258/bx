package serve

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/dinhhuy258/bx/internal/api"
	"github.com/dinhhuy258/bx/internal/app"
	"github.com/dinhhuy258/bx/internal/config"
	"github.com/dinhhuy258/bx/internal/git"
	"github.com/dinhhuy258/bx/internal/state"
	"github.com/dinhhuy258/bx/internal/tmux"
)

func TestStreamRefusesASessionThatIsNotRunning(t *testing.T) {
	_, f, ts := newServer(t)
	f.Replies[listSessions] = ""
	for path, want := range map[string]int{
		"/api/projects/home-network/sessions/1/stream": http.StatusGone,
		"/api/projects/home-network/sessions/x/stream": http.StatusBadRequest,
		"/api/projects/nope/sessions/1/stream":         http.StatusNotFound,
	} {
		r, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != want {
			t.Errorf("%s: %d, want %d", path, r.StatusCode, want)
		}
	}
}

// TestRealStream runs against a private tmux server. The "Claude" is `cat`, which echoes.
func TestRealStream(t *testing.T) {
	if os.Getenv("BX_INTEGRATION") != "1" {
		t.Skip("set BX_INTEGRATION=1")
	}
	dir, err := os.MkdirTemp("", "bxs") // short: unix socket paths are limited to ~104 bytes
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	cfg := config.Default(dir)
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if err := tmux.WriteConf(cfg.TmuxConf()); err != nil {
		t.Fatal(err)
	}
	r := &tmux.Runner{Socket: cfg.TmuxSocket(), Config: cfg.TmuxConf()}
	t.Cleanup(func() { r.Command("kill-server").Run() })
	repo := cfg.ProjectDir("proj")
	if err := git.New().Init(repo); err != nil {
		t.Fatal(err)
	}
	a := &app.App{Cfg: cfg, Tmux: tmux.New(r), Git: git.New()}
	p := state.New("proj", repo)
	p.AddSession(&state.Session{UUID: "u1", Title: "t"})
	if err := a.SaveProject(p); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Tmux.NewSession(app.TmuxName("proj", 1), repo, "sh -c 'echo READY; exec cat'"); err != nil {
		t.Fatal(err)
	}
	s := New(a, cfg, fakeLister{}, func(string, ...any) {})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/api/projects/proj/sessions/1/stream?cols=100&rows=30", nil)
	if err != nil {
		t.Fatal(err)
	}
	readUntil := func(want string) {
		t.Helper()
		var screen []byte
		for !strings.Contains(string(screen), want) {
			typ, data, err := c.Read(ctx)
			if err != nil {
				t.Fatalf("stream ended before %q: %v\n%q", want, err, screen)
			}
			if typ == websocket.MessageBinary {
				screen = append(screen, data...)
			}
		}
	}
	readUntil("READY")
	if err := c.Write(ctx, websocket.MessageBinary, []byte("hello-bx\r")); err != nil {
		t.Fatal(err)
	}
	readUntil("hello-bx")
	ctl, _ := json.Marshal(api.Control{Type: "resize", Cols: 80, Rows: 24})
	if err := c.Write(ctx, websocket.MessageText, ctl); err != nil {
		t.Fatal(err)
	}
	id, err := a.Target(p, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Tmux.KillSession(id); err != nil {
		t.Fatal(err)
	}
	for {
		_, _, err := c.Read(ctx)
		if err == nil {
			continue
		}
		var ce websocket.CloseError
		if !errors.As(err, &ce) || ce.Code != websocket.StatusNormalClosure || ce.Reason != "session ended" {
			t.Fatalf("want a normal close \"session ended\", got %v", err)
		}
		return
	}
}
