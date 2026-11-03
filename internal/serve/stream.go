package serve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/coder/websocket"
	"github.com/creack/pty"

	"github.com/dinhhuy258/bx/internal/api"
)

// stream is one client watching one session.
type stream struct{ cancel context.CancelFunc }

// stream serves GET /api/projects/{name}/sessions/{num}/stream: a pty running
// `tmux attach` to the session, bridged to a WebSocket. Binary frames are terminal bytes both
// ways; a text frame from the client resizes, scrolls, enters copy-mode (see Tmux.Scroll) or
// says the page was seen, and a "copy_end" frame from the server tells the client that tmux left
// copy-mode; "page" and "page_seen" frames carry the page Claude wrote (see pages.go). When the
// session ends (Claude exited, story closed) the socket closes normally with reason
// "session ended", and the client does not reconnect. Several clients may watch one session; tmux sizes it to the latest.
func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	num, err := strconv.Atoi(r.PathValue("num"))
	if err != nil {
		httpError(w, http.StatusBadRequest, errors.New("bad session number"))
		return
	}
	p, err := s.load(r.PathValue("name"))
	if err != nil {
		httpError(w, statusOf(err), err)
		return
	}
	sid, err := s.App.Target(p, num)
	if err != nil {
		httpError(w, statusOf(err), err)
		return
	}
	uuid := ""
	if se := p.Sessions[num]; se != nil {
		uuid = se.UUID
	}
	s.bridge(w, r, sid, uuid, fmt.Sprintf("%s [%d]", p.Name, num))
}

// shellStream serves GET /api/projects/{name}/shells/stream?story=&k=: shell k of a story
// ("" = chats), bridged like a session. When the shell exits the socket closes normally with
// reason "session ended".
func (s *Server) shellStream(w http.ResponseWriter, r *http.Request) {
	p, err := s.load(r.PathValue("name"))
	if err != nil {
		httpError(w, statusOf(err), err)
		return
	}
	k, err := strconv.Atoi(r.URL.Query().Get("k"))
	if err != nil || k < 1 {
		httpError(w, http.StatusBadRequest, errors.New("bad shell number"))
		return
	}
	story := r.URL.Query().Get("story")
	sid, err := s.App.ShellTarget(p, story, k)
	if err != nil {
		httpError(w, statusOf(err), err)
		return
	}
	s.bridge(w, r, sid, "", fmt.Sprintf("%s shell %s$%d", p.Name, group(story), k))
}

// bridge attaches to tmux session sid in a pty and bridges it to a WebSocket until either side
// ends. uuid is the Claude session watched ("" for a shell): while the stream is open the
// session is read (see unread.go). label names it in the log ("stream <label>: open (80x24)"
// … "stream <label>: closed").
func (s *Server) bridge(w http.ResponseWriter, r *http.Request, sid, uuid, label string) {
	if err := s.App.Tmux.PrepareCopy(); err != nil {
		s.Log("stream %s: copy-mode setup: %v", label, err)
	}
	cmd, err := s.App.Tmux.AttachCommand(sid)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err)
		return
	}
	cols, rows := dim(r.URL.Query().Get("cols"), 80), dim(r.URL.Query().Get("rows"), 24)
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	c.SetReadLimit(1 << 20)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	st := &stream{cancel: cancel}
	if !s.track(st) {
		_ = c.Close(websocket.StatusGoingAway, "server shutting down")
		return
	}
	defer s.untrack(st)
	if uuid != "" {
		s.watch(uuid)
		defer s.unwatch(uuid)
		// the page Claude wrote (see pages.go): now if no client has seen it yet, then as written
		s.subscribePages(uuid, st, func(b []byte) {
			wctx, wcancel := context.WithTimeout(ctx, 5*time.Second)
			defer wcancel()
			_ = c.Write(wctx, websocket.MessageText, b)
		})
		defer s.unsubscribePages(uuid, st)
	}

	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		s.Log("stream %s: %v", label, err)
		_ = c.Close(websocket.StatusInternalError, "attach failed")
		return
	}
	s.Log("stream %s: open (%dx%d)", label, cols, rows)
	defer func() {
		_ = f.Close()
		// The pty → socket goroutine is still blocked reading the pty, so the close above
		// waits for that read and tmux attach gets no hangup until the pane prints. Hang it
		// up directly: tmux attach detaches on SIGHUP, the session keeps running.
		_ = cmd.Process.Signal(syscall.SIGHUP)
		_ = cmd.Wait()
		s.Log("stream %s: closed", label)
	}()

	attachDone := make(chan struct{})
	go func() { // pty → socket; ends when tmux attach exits
		defer close(attachDone)
		buf := make([]byte, 32*1024)
		for {
			n, err := f.Read(buf)
			if n > 0 {
				if c.Write(ctx, websocket.MessageBinary, buf[:n]) != nil {
					cancel() // the client is gone, not the session
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	var copying atomic.Bool // the client is in copy-mode
	var check *time.Timer
	checkMode := func() { // a little after a key: tmux handles it asynchronously
		if !copying.Load() {
			return
		}
		if check != nil {
			check.Stop()
		}
		check = time.AfterFunc(modeCheckDelay, func() {
			if in, err := s.App.Tmux.InMode(sid); err == nil && !in && copying.CompareAndSwap(true, false) {
				b, _ := json.Marshal(api.Control{Type: "copy_end"})
				_ = c.Write(ctx, websocket.MessageText, b)
			}
		})
	}
	go func() { // socket → pty
		defer cancel()
		for {
			typ, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			switch typ {
			case websocket.MessageBinary:
				if _, err := f.Write(data); err != nil {
					return
				}
				checkMode()
			case websocket.MessageText:
				var ctl api.Control
				if json.Unmarshal(data, &ctl) != nil {
					continue
				}
				switch {
				case ctl.Type == "resize" && ctl.Cols > 0 && ctl.Rows > 0:
					_ = pty.Setsize(f, &pty.Winsize{Cols: uint16(dim(strconv.Itoa(ctl.Cols), 80)), Rows: uint16(dim(strconv.Itoa(ctl.Rows), 24))})
				case ctl.Type == "scroll":
					// before the next frame is read, so a key sent after "back to live" reaches Claude
					if err := s.App.Tmux.Scroll(sid, ctl.Lines); err != nil {
						s.Log("stream %s: scroll: %v", label, err)
					}
					checkMode()
				case ctl.Type == "page_seen" && uuid != "":
					s.pageSeen(uuid, st)
				case ctl.Type == "copy":
					if err := s.App.Tmux.CopyMode(sid); err != nil {
						s.Log("stream %s: copy-mode: %v", label, err)
					} else {
						copying.Store(true)
					}
				}
			}
		}
	}()

	interval, timeout := pingInterval, pingTimeout
	// The pinger has its own context, stopped before the socket closes: a ping that fails
	// because we are closing it is not worth a log line.
	pingCtx, stopPing := context.WithCancel(ctx)
	defer stopPing()
	pingDone := make(chan struct{})
	go func() { // keepalive: a phone that slept or changed network can leave a dead socket that looks open
		defer close(pingDone)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-pingCtx.Done():
				return
			case <-t.C:
			}
			pctx, pcancel := context.WithTimeout(pingCtx, timeout)
			err := c.Ping(pctx) // the pong is read by the socket → pty loop
			pcancel()
			if err != nil {
				if pingCtx.Err() == nil {
					s.Log("stream %s: ping: %v", label, err)
				}
				cancel()
				return
			}
		}
	}()
	select {
	case <-attachDone:
	case <-ctx.Done():
	}
	stopPing()
	<-pingDone
	switch {
	case ctx.Err() == nil: // tmux attach exited by itself: the session is gone
		_ = c.Close(websocket.StatusNormalClosure, "session ended")
	case s.isClosing():
		_ = c.Close(websocket.StatusGoingAway, "server shutting down")
	default:
		_ = c.Close(websocket.StatusNormalClosure, "bye")
	}
}

// modeCheckDelay is how long after a key in copy-mode the server asks tmux whether it left.
const modeCheckDelay = 50 * time.Millisecond

// pingInterval is how often the server pings a stream's client; one that does not answer
// within pingTimeout is dropped. Variables so tests can shorten them.
var (
	pingInterval = 30 * time.Second
	pingTimeout  = 10 * time.Second
)

// dim parses a terminal dimension and clamps it to something tmux accepts.
func dim(s string, def int) int {
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		n = def
	}
	return max(10, min(n, 500))
}

func (s *Server) track(st *stream) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return false
	}
	s.streams[st] = true
	return true
}

func (s *Server) untrack(st *stream) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.streams, st)
}

// watch and unwatch count an open stream of a Claude session; opening one reads it.
func (s *Server) watch(uuid string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unread.open(uuid)
}

func (s *Server) unwatch(uuid string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unread.close(uuid)
}

func (s *Server) isClosing() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closing
}

// Shutdown ends every stream: each detaches its tmux client. The Claude sessions keep running.
func (s *Server) Shutdown() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closing = true
	for st := range s.streams {
		st.cancel()
	}
}
