package serve

import (
	"context"
	"encoding/base64"
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
	inMode := func(want string) {
		t.Helper()
		for i := 0; ; i++ {
			out, _ := r.Command("display-message", "-p", "-t", id, "#{pane_in_mode}").Output()
			if strings.TrimSpace(string(out)) == want {
				return
			}
			if i == 50 {
				t.Fatalf("pane_in_mode = %q, want %s", out, want)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	for _, step := range []struct {
		lines int
		want  string
	}{{-1, "1"}, {0, "0"}} {
		ctl, _ := json.Marshal(api.Control{Type: "scroll", Lines: step.lines})
		if err := c.Write(ctx, websocket.MessageText, ctl); err != nil {
			t.Fatal(err)
		}
		inMode(step.want)
	}
	// copy-mode: select the echoed line, yank it; tmux sends it as OSC 52 and the server
	// reports that copy-mode ended
	ctl, _ = json.Marshal(api.Control{Type: "copy"})
	if err := c.Write(ctx, websocket.MessageText, ctl); err != nil {
		t.Fatal(err)
	}
	inMode("1")
	for _, k := range []string{"k", "0", "v", "$", "h", "y"} { // back to back: not taken for a paste
		if err := c.Write(ctx, websocket.MessageBinary, []byte(k)); err != nil {
			t.Fatal(err)
		}
	}
	want := "\x1b]52;;" + base64.StdEncoding.EncodeToString([]byte("hello-bx"))
	var screen []byte
	for gotMode := false; !gotMode || !strings.Contains(string(screen), want); {
		typ, data, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("no OSC 52 %q / mode frame: %v\n%q", want, err, screen)
		}
		if typ == websocket.MessageBinary {
			screen = append(screen, data...)
			continue
		}
		var m api.Control
		if json.Unmarshal(data, &m) == nil && m.Type == "copy_end" {
			gotMode = true
		}
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
