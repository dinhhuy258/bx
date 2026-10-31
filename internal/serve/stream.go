package serve

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"

	"github.com/coder/websocket"
	"github.com/creack/pty"

	"github.com/dinhhuy258/bx/internal/api"
)

// stream is one client watching one session.
type stream struct{ cancel context.CancelFunc }

// stream serves GET /api/projects/{name}/sessions/{num}/stream: a pty running
// `tmux attach` to the session, bridged to a WebSocket. Binary frames are terminal bytes both
// ways; a text frame from the client resizes. When the session ends (Claude exited, story
// closed) the socket closes normally with reason "session ended", and the client does not
// reconnect. Several clients may watch one session; tmux sizes it to the latest.
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
		c.Close(websocket.StatusGoingAway, "server shutting down")
		return
	}
	defer s.untrack(st)

	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		s.Log("stream %s [%d]: %v", p.Name, num, err)
		c.Close(websocket.StatusInternalError, "attach failed")
		return
	}
	s.Log("stream %s [%d]: open (%dx%d)", p.Name, num, cols, rows)
	defer func() {
		_ = f.Close() // hangs up tmux attach, which detaches
		_ = cmd.Wait()
		s.Log("stream %s [%d]: closed", p.Name, num)
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
			case websocket.MessageText:
				var ctl api.Control
				if json.Unmarshal(data, &ctl) == nil && ctl.Type == "resize" && ctl.Cols > 0 && ctl.Rows > 0 {
					_ = pty.Setsize(f, &pty.Winsize{Cols: uint16(dim(strconv.Itoa(ctl.Cols), 80)), Rows: uint16(dim(strconv.Itoa(ctl.Rows), 24))})
				}
			}
		}
	}()

	select {
	case <-attachDone:
	case <-ctx.Done():
	}
	switch {
	case ctx.Err() == nil: // tmux attach exited by itself: the session is gone
		c.Close(websocket.StatusNormalClosure, "session ended")
	case s.isClosing():
		c.Close(websocket.StatusGoingAway, "server shutting down")
	default:
		c.Close(websocket.StatusNormalClosure, "bye")
	}
}

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
