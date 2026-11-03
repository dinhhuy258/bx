package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/dinhhuy258/bx/internal/api"
	"github.com/dinhhuy258/bx/internal/tmux"
)

func TestPageOf(t *testing.T) {
	root := t.TempDir()
	for _, c := range []struct {
		path    string
		project string
		num     int
		name    string
	}{
		{root + "/home-network/1/report.html", "home-network", 1, "report.html"},
		{root + "/home-network/12/Report.HTML", "home-network", 12, "Report.HTML"},
		{root + "/home-network/1/sub/report.html", "", 0, ""},
		{root + "/home-network/1/report.txt", "", 0, ""},
		{root + "/home-network/1/.draft.html", "", 0, ""},
		{root + "/home-network/x/report.html", "", 0, ""},
		{root + "/home-network/0/report.html", "", 0, ""},
		{root + "/../etc/1/report.html", "", 0, ""},
		{"/elsewhere/home-network/1/report.html", "", 0, ""},
		{"home-network/1/report.html", "", 0, ""},
	} {
		p, n, name, ok := pageOf(root, c.path)
		if ok != (c.name != "") || p != c.project || n != c.num || name != c.name {
			t.Errorf("pageOf(%q) = %q %d %q %v", c.path, p, n, name, ok)
		}
	}
	// a root reached through a symlink: Claude may write to either path
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "artifacts")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(real, "p", "3"), 0o700); err != nil {
		t.Fatal(err)
	}
	if p, n, name, ok := pageOf(link, filepath.Join(real, "p", "3", "a.html")); !ok || p != "p" || n != 3 || name != "a.html" {
		t.Errorf("through a symlink: %q %d %q %v", p, n, name, ok)
	}
}

// client is one open stream of session 1: its text frames, and seen, which sends page_seen.
type client struct {
	frames <-chan api.Control
	seen   func()
}

// pageServer is a server whose streams attach without tmux, with the file server on. It returns
// session 1's uuid and artifacts folder, open, which opens a stream of session 1, and hook,
// which posts to the page hook.
func pageServer(t *testing.T) (*Server, string, string, func() client, func(body any) int) {
	t.Helper()
	s, f, ts := newServer(t)
	s.App.Tmux = tmux.New(attachFake{f})
	s.App.ArtifactsURL = "http://100.64.0.1:7358"
	p, err := s.load("home-network")
	if err != nil {
		t.Fatal(err)
	}
	uuid := p.Sessions[1].UUID
	dir := s.App.ArtifactsDir("home-network", 1)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	opened, closed := 0, 0
	allClosed := make(chan struct{}, 100)
	s.Log = func(format string, args ...any) {
		if strings.HasSuffix(fmt.Sprintf(format, args...), ": closed") {
			mu.Lock()
			closed++
			mu.Unlock()
			allClosed <- struct{}{}
		}
	}
	var conns []*websocket.Conn
	t.Cleanup(func() { // wait for every handler: they write to the temp dir's tmux fake
		for _, c := range conns {
			_ = c.CloseNow()
		}
		for {
			mu.Lock()
			done := closed >= opened
			mu.Unlock()
			if done {
				return
			}
			select {
			case <-allClosed:
			case <-time.After(5 * time.Second):
				t.Error("stream did not close")
				return
			}
		}
	})
	open := func() client {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/api/projects/home-network/sessions/1/stream", nil)
		if err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		opened++
		mu.Unlock()
		conns = append(conns, c)
		out := make(chan api.Control, 10)
		go func() {
			for {
				typ, data, err := c.Read(context.Background())
				if err != nil {
					return
				}
				var ctl api.Control
				if typ == websocket.MessageText && json.Unmarshal(data, &ctl) == nil {
					out <- ctl
				}
			}
		}()
		seen := func() {
			b, _ := json.Marshal(api.Control{Type: "page_seen"})
			if err := c.Write(context.Background(), websocket.MessageText, b); err != nil {
				t.Error(err)
			}
		}
		return client{out, seen}
	}
	hook := func(body any) int {
		t.Helper()
		_, code := post(t, ts, "/api/hooks/page", body)
		return code
	}
	return s, uuid, dir, open, hook
}

func next(t *testing.T, ch <-chan api.Control) api.Control {
	t.Helper()
	select {
	case c := <-ch:
		return c
	case <-time.After(3 * time.Second):
		t.Fatal("no frame")
		return api.Control{}
	}
}

func none(t *testing.T, ch <-chan api.Control) {
	t.Helper()
	select {
	case c := <-ch:
		t.Fatalf("unexpected frame %+v", c)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestPageHookRefuses(t *testing.T) {
	_, uuid, dir, _, hook := pageServer(t)
	page := filepath.Join(dir, "report.html")
	_ = os.WriteFile(page, []byte("x"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o600)
	_ = os.Symlink("/etc/hostname", filepath.Join(dir, "link.html"))
	for _, c := range []struct {
		body api.PageHook
		want int
	}{
		{api.PageHook{SessionID: "someone-else", Path: page}, 404},
		{api.PageHook{SessionID: uuid, Path: filepath.Join(dir, "notes.txt")}, 400},
		{api.PageHook{SessionID: uuid, Path: filepath.Join(dir, "missing.html")}, 404},
		{api.PageHook{SessionID: uuid, Path: filepath.Join(dir, "link.html")}, 404},
		{api.PageHook{SessionID: uuid, Path: "/tmp/report.html"}, 400},
		{api.PageHook{Path: page}, 400},
		{api.PageHook{SessionID: uuid, Path: page}, 204},
	} {
		if got := hook(c.body); got != c.want {
			t.Errorf("%+v: %d, want %d", c.body, got, c.want)
		}
	}
}

func TestPagePushedToStreamsUntilSeen(t *testing.T) {
	_, uuid, dir, open, hook := pageServer(t)
	page := filepath.Join(dir, "report.html")
	_ = os.WriteFile(page, []byte("x"), 0o600)

	a, b := open(), open()
	if code := hook(api.PageHook{SessionID: uuid, Path: page, New: true}); code != 204 {
		t.Fatalf("hook: %d", code)
	}
	for _, c := range []client{a, b} {
		got := next(t, c.frames)
		if got.Type != "page" || !got.New || got.Page == nil || got.Page.Name != "report.html" ||
			got.Page.URL != "http://100.64.0.1:7358/home-network/1/report.html" {
			t.Fatalf("frame %+v page %+v", got, got.Page)
		}
	}
	// a second write of the same name is an update, even if the hook thought it new
	hook(api.PageHook{SessionID: uuid, Path: page, New: true})
	for _, c := range []client{a, b} {
		if got := next(t, c.frames); got.Type != "page" || got.New {
			t.Fatalf("second write: %+v", got)
		}
	}

	// a stream that opens later gets the page nobody has seen yet, first
	c := open()
	if got := next(t, c.frames); got.Type != "page" || got.Page.Name != "report.html" || got.New {
		t.Fatalf("replay: %+v", got)
	}

	// a's client opens it: b and c close theirs, a hears nothing back
	a.seen()
	for _, o := range []client{b, c} {
		if got := next(t, o.frames); got.Type != "page_seen" {
			t.Fatalf("want page_seen, got %+v", got)
		}
	}
	none(t, a.frames)
	// seen is forgotten for good: a new stream gets nothing, a second seen tells nobody
	d := open()
	none(t, d.frames)
	b.seen()
	none(t, a.frames)

	// the next page shows again, on every stream
	hook(api.PageHook{SessionID: uuid, Path: page})
	for _, o := range []client{a, b, c, d} {
		if got := next(t, o.frames); got.Type != "page" || got.New {
			t.Fatalf("after seen: %+v", got)
		}
	}
}
