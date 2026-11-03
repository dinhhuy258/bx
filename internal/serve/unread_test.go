package serve

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/dinhhuy258/bx/internal/api"
	"github.com/dinhhuy258/bx/internal/claude"
	"github.com/dinhhuy258/bx/internal/tmux"
)

func agent(id, status string) claude.Agent { return claude.Agent{SessionID: id, Status: status} }

func TestUnreadMarksATurnNobodyWatched(t *testing.T) {
	u := newUnread()
	u.observe([]claude.Agent{agent("u1", "idle"), agent("u2", "busy"), agent("u3", "waiting")})
	if len(u.marked) != 0 {
		t.Fatalf("first seen idle (started, resumed) is not unread: %v", u.marked)
	}
	u.open("u3") // a client shows u3
	u.observe([]claude.Agent{agent("u1", "idle"), agent("u2", "idle"), agent("u3", "idle")})
	if fmt.Sprint(u.snapshot()) != "map[u2:true]" {
		t.Fatalf("running → idle unwatched marks, watched does not: %v", u.marked)
	}
	u.close("u3")
	u.observe([]claude.Agent{agent("u1", "idle"), agent("u2", "idle"), agent("u3", "idle")})
	if !u.marked["u2"] || u.marked["u3"] {
		t.Fatalf("staying idle changes nothing: %v", u.marked)
	}
	u.observe([]claude.Agent{agent("u1", "idle"), agent("u2", "busy"), agent("u3", "idle")})
	if len(u.marked) != 0 {
		t.Fatalf("working again clears: %v", u.marked)
	}
	u.observe([]claude.Agent{agent("u1", "idle"), agent("u2", "idle"), agent("u3", "idle")})
	u.open("u2")
	if len(u.marked) != 0 {
		t.Fatalf("opening a stream reads it: %v", u.marked)
	}
	u.close("u2")
	u.observe([]claude.Agent{agent("u2", "busy")})
	u.observe([]claude.Agent{agent("u2", "idle")})
	u.observe(nil)
	if len(u.marked) != 0 || len(u.watching) != 0 {
		t.Fatalf("a process that left is dropped: marked %v watching %v", u.marked, u.watching)
	}
}

func TestUnreadCountsEveryStream(t *testing.T) {
	u := newUnread()
	u.observe([]claude.Agent{agent("u1", "busy")})
	u.open("u1")
	u.open("u1") // TUI and phone
	u.close("u1")
	u.observe([]claude.Agent{agent("u1", "idle")})
	if u.marked["u1"] {
		t.Fatal("one stream is still open: watched")
	}
	u.close("u1")
	if len(u.watching) != 0 {
		t.Fatalf("watching %v", u.watching)
	}
}

// switchLister is a claude agents whose list a test changes.
type switchLister struct {
	mu     sync.Mutex
	agents []claude.Agent
}

func (l *switchLister) set(a ...claude.Agent) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.agents = a
}

func (l *switchLister) Agents(context.Context) ([]claude.Agent, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.agents, nil
}

// poll is one watch tick with a fresh `claude agents`, as when the 1 s cache has expired.
func poll(s *Server) {
	s.mu.Lock()
	s.agentsAt = time.Time{}
	s.mu.Unlock()
	s.watchOnce()
}

// The server marks a turn that ends with no stream open, shows it in both APIs, and an open
// stream reads it.
func TestServerUnreadFollowsStreams(t *testing.T) {
	s, f, ts := newServer(t)
	l := &switchLister{agents: []claude.Agent{agent("u1", "busy")}}
	s.Lister = l
	s.App.Tmux = tmux.New(attachFake{f})
	poll(s)
	l.set(agent("u1", "idle"))
	poll(s)
	snap, code := get[api.Snapshot](t, ts, "/api/projects/home-network")
	if st := snap.SessionStates[1]; code != 200 || st.State != "idle" || !st.Unread || st.Text != "idle · unread" {
		t.Fatalf("%d %+v", code, st)
	}
	list, _ := get[[]api.ProjectSummary](t, ts, "/api/projects")
	if len(list) != 1 || list[0].Unread != 1 {
		t.Fatalf("projects %+v", list)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/api/projects/home-network/sessions/1/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the stream to read it", func() bool { return len(s.unreadSnapshot()) == 0 })
	l.set(agent("u1", "busy"))
	poll(s)
	l.set(agent("u1", "idle"))
	poll(s)
	if len(s.unreadSnapshot()) != 0 {
		t.Fatal("a turn that ends with the stream open is read")
	}

	c.CloseNow()
	waitFor(t, "the stream to close", func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return len(s.unread.watching) == 0
	})
	l.set(agent("u1", "busy"))
	poll(s)
	l.set(agent("u1", "idle"))
	poll(s)
	if !s.unreadSnapshot()["u1"] {
		t.Fatal("a turn that ends after the stream closed is unread")
	}
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
