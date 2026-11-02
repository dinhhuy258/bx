package serve

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
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

func TestShellStreamRefuses(t *testing.T) {
	_, _, ts := newServer(t)
	for path, want := range map[string]int{
		"/api/projects/home-network/shells/stream?story=s1&k=x": http.StatusBadRequest,
		"/api/projects/home-network/shells/stream?story=s1&k=0": http.StatusBadRequest,
		"/api/projects/home-network/shells/stream?story=s1&k=4": http.StatusGone,
		"/api/projects/nope/shells/stream?story=s1&k=1":         http.StatusNotFound,
	} {
		r, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		_ = r.Body.Close()
		if r.StatusCode != want {
			t.Errorf("%s: %d, want %d", path, r.StatusCode, want)
		}
	}
}

func TestShellStreamEnds(t *testing.T) {
	s, f, ts := newServer(t)
	f.Replies[listShells] = "$9\tbxsh-home-network-s1-1\thome-network\ts1\t1\t\tzsh\n"
	s.App.Tmux = tmux.New(endingFake{f})
	var mu sync.Mutex
	var logs []string
	closed := make(chan struct{})
	var once sync.Once
	s.Log = func(format string, args ...any) {
		l := fmt.Sprintf(format, args...)
		mu.Lock()
		logs = append(logs, l)
		mu.Unlock()
		if strings.HasSuffix(l, ": closed") {
			once.Do(func() { close(closed) })
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/api/projects/home-network/shells/stream?story=s1&k=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.CloseNow() }()
	for {
		_, _, err = c.Read(ctx)
		if err != nil {
			break
		}
	}
	if websocket.CloseStatus(err) != websocket.StatusNormalClosure || !strings.Contains(err.Error(), "session ended") {
		t.Fatalf("want a normal close \"session ended\", got %v", err)
	}
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not close")
	}
	mu.Lock()
	defer mu.Unlock()
	opened := false
	for _, l := range logs {
		opened = opened || strings.HasPrefix(l, "stream home-network shell s1$1: open")
	}
	if !opened {
		t.Fatalf("logs: %q", logs)
	}
}

// attachFake is a fake tmux whose attach prints a line every 100 ms in the pty, so a stream
// opens without tmux. It prints because on macOS closing the pty waits for the pending read.
type attachFake struct{ *tmux.Fake }

func (attachFake) Command(...string) *exec.Cmd {
	return exec.Command("sh", "-c", "while :; do echo x; sleep 0.1; done")
}

// silentFake is a fake tmux whose attach prints nothing, like tmux on an idle pane.
type silentFake struct{ *tmux.Fake }

func (silentFake) Command(...string) *exec.Cmd {
	return exec.Command("sh", "-c", "exec sleep 30")
}

// endingFake is a fake tmux whose attach exits after 200 ms: the session ends.
type endingFake struct{ *tmux.Fake }

func (endingFake) Command(...string) *exec.Cmd {
	return exec.Command("sh", "-c", "echo x; sleep 0.2")
}

// pingStream shortens the keepalive, opens session 1's stream and returns the client socket
// and a channel of the server's log lines.
func pingStream(t *testing.T, interval, timeout time.Duration) (*websocket.Conn, <-chan string) {
	t.Helper()
	return pingStreamWith(t, interval, timeout, func(f *tmux.Fake) tmux.Exec { return attachFake{f} })
}

func pingStreamWith(t *testing.T, interval, timeout time.Duration, fake func(*tmux.Fake) tmux.Exec) (*websocket.Conn, <-chan string) {
	t.Helper()
	oldInterval, oldTimeout := pingInterval, pingTimeout
	pingInterval, pingTimeout = interval, timeout
	t.Cleanup(func() { pingInterval, pingTimeout = oldInterval, oldTimeout })
	s, f, ts := newServer(t)
	s.App.Tmux = tmux.New(fake(f))
	logs := make(chan string, 100)
	done := make(chan struct{}) // the stream handler logged "closed"
	var once sync.Once
	s.Log = func(format string, args ...any) {
		l := fmt.Sprintf(format, args...)
		if strings.HasSuffix(l, ": closed") {
			once.Do(func() { close(done) })
		}
		select {
		case logs <- l:
		default:
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/api/projects/home-network/sessions/1/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { // before the package vars are restored: the handler reads them
		c.CloseNow()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("stream did not close")
		}
	})
	return c, logs
}

func TestStreamClosingDetachesASilentAttach(t *testing.T) {
	c, logs := pingStreamWith(t, time.Minute, time.Minute, func(f *tmux.Fake) tmux.Exec { return silentFake{f} })
	c.Close(websocket.StatusNormalClosure, "bye")
	deadline := time.After(2 * time.Second)
	for {
		select {
		case l := <-logs:
			if strings.HasSuffix(l, ": closed") {
				return
			}
		case <-deadline:
			t.Fatal("tmux attach still running 2 s after the client left")
		}
	}
}

func TestStreamPingKeepsAReadingClient(t *testing.T) {
	c, logs := pingStream(t, 50*time.Millisecond, 2*time.Second)
	readErr := make(chan error, 1)
	go func() { // reading answers the server's pings
		for {
			if _, _, err := c.Read(context.Background()); err != nil {
				readErr <- err
				return
			}
		}
	}()
	deadline := time.After(time.Second) // 20 ping intervals
	for {
		select {
		case err := <-readErr:
			t.Fatalf("stream closed: %v", err)
		case l := <-logs:
			if strings.Contains(l, "ping:") || strings.Contains(l, "closed") {
				t.Fatalf("server log: %s", l)
			}
		case <-deadline:
			return
		}
	}
}

func TestStreamPingDropsADeadClient(t *testing.T) {
	interval, timeout := 50*time.Millisecond, 200*time.Millisecond
	_, logs := pingStream(t, interval, timeout) // never reads, so never answers a ping
	start := time.Now()
	deadline := time.After(5 * time.Second)
	pinged := false
	for {
		select {
		case l := <-logs:
			t.Logf("%v %s", time.Since(start), l)
			if strings.Contains(l, "ping:") {
				pinged = true
				if d := time.Since(start); d > interval+timeout+2*time.Second {
					t.Errorf("ping failed after %v", d)
				}
			}
			if strings.Contains(l, "closed") {
				if !pinged {
					t.Fatalf("stream closed without a failed ping")
				}
				return
			}
		case <-deadline:
			t.Fatalf("stream still open (failed ping: %v)", pinged)
		}
	}
}

// TestStreamEndLogsNoPing: the session ends while a ping waits for its pong (the client
// doesn't read). Closing the socket must not log that ping as failed.
func TestStreamEndLogsNoPing(t *testing.T) {
	c, logs := pingStreamWith(t, 50*time.Millisecond, time.Second,
		func(f *tmux.Fake) tmux.Exec { return endingFake{f} })
	deadline := time.After(1500 * time.Millisecond) // past the session end and the ping timeout
	closed := false
	for {
		select {
		case l := <-logs:
			if strings.Contains(l, "ping:") {
				t.Fatalf("server log: %s", l)
			}
			closed = closed || strings.HasSuffix(l, ": closed")
		case <-deadline:
			c.CloseNow() // the server's close handshake may wait for this client
			for !closed {
				select {
				case l := <-logs:
					if strings.Contains(l, "ping:") {
						t.Fatalf("server log: %s", l)
					}
					closed = strings.HasSuffix(l, ": closed")
				case <-time.After(5 * time.Second):
					t.Fatal("stream did not close")
				}
			}
			return
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
