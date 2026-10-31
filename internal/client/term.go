package client

import (
	"context"
	"encoding/json"
	"sync"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
	"github.com/coder/websocket"

	"github.com/dinhhuy258/bx/internal/api"
)

// Term is the Claude pane on the client: a terminal emulator fed by the stream. Bytes from the
// server go into the emulator; key presses are encoded by the emulator (it knows the modes the
// program set, such as application cursor keys) and come out of Input for the socket.
type Term struct {
	mu    sync.Mutex
	emu   *vt.Emulator
	input chan []byte
	cols  int
	rows  int
}

// NewTerm builds an emulator of cols x rows and starts the input reader.
func NewTerm(cols, rows int) *Term {
	t := &Term{emu: vt.NewEmulator(cols, rows), input: make(chan []byte, 64), cols: cols, rows: rows}
	go t.readInput()
	return t
}

// readInput drains the emulator's input pipe (an unbuffered pipe: SendKey blocks until read).
func (t *Term) readInput() {
	buf := make([]byte, 1024)
	for {
		n, err := t.emu.Read(buf)
		if n > 0 {
			b := make([]byte, n)
			copy(b, buf[:n])
			t.input <- b
		}
		if err != nil {
			close(t.input)
			return
		}
	}
}

// Input delivers the bytes the terminal would send to the program (key presses).
func (t *Term) Input() <-chan []byte { return t.input }

// Feed writes bytes from the server into the screen.
func (t *Term) Feed(b []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, _ = t.emu.Write(b)
}

// SendKey encodes a key press for the program on the other side.
func (t *Term) SendKey(k tea.KeyPressMsg) { t.emu.SendKey(uv.KeyPressEvent(uv.Key(tea.Key(k)))) }

// View renders the screen as rows lines of styled text.
func (t *Term) View() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.emu.Render()
}

// Cursor is the cursor position, or -1,-1 when hidden.
func (t *Term) Cursor() (int, int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.emu.CursorHidden() {
		return -1, -1
	}
	p := t.emu.CursorPosition()
	return p.X, p.Y
}

// Resize changes the screen size; the caller also tells the server.
func (t *Term) Resize(cols, rows int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if cols == t.cols && rows == t.rows {
		return
	}
	t.cols, t.rows = cols, rows
	t.emu.Resize(cols, rows)
}

// Cols and Rows are the current size.
func (t *Term) Cols() int { return t.cols }
func (t *Term) Rows() int { return t.rows }

// Close stops the input reader.
func (t *Term) Close() { _ = t.emu.Close() }

// Stream is one WebSocket to the server for this Term.
type Stream struct {
	conn *websocket.Conn
	term *Term
}

// Bubble Tea messages produced by the stream.
type (
	streamOutputMsg struct{}
	streamClosedMsg struct {
		conn Conn
		err  error
	}
)

// Connect dials one session's stream and starts the pumps: one streamOutputMsg per burst of
// output (the model redraws), then streamClosedMsg.
func Connect(ctx context.Context, url string, term *Term, send func(tea.Msg)) (Conn, error) {
	c, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		return nil, err
	}
	c.SetReadLimit(1 << 20)
	// sctx ends with the connection, so a stale writeLoop never steals keys meant for the next stream.
	sctx, cancel := context.WithCancel(ctx)
	s := &Stream{conn: c, term: term}
	go func() {
		defer cancel()
		s.readLoop(sctx, send)
	}()
	go s.writeLoop(sctx)
	return s, nil
}

func (s *Stream) readLoop(ctx context.Context, send func(tea.Msg)) {
	for {
		typ, data, err := s.conn.Read(ctx)
		if err != nil {
			send(streamClosedMsg{s, err})
			return
		}
		switch typ {
		case websocket.MessageBinary:
			s.term.Feed(data)
			send(streamOutputMsg{})
		}
	}
}

func (s *Stream) writeLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case b, ok := <-s.term.Input():
			if !ok {
				return
			}
			if err := s.conn.Write(ctx, websocket.MessageBinary, b); err != nil {
				return
			}
		}
	}
}

// Resize tells the server the new size.
func (s *Stream) Resize(ctx context.Context, cols, rows int) error {
	b, _ := json.Marshal(api.Control{Type: "resize", Cols: cols, Rows: rows})
	return s.conn.Write(ctx, websocket.MessageText, b)
}

// Close ends the stream.
func (s *Stream) Close() { _ = s.conn.Close(websocket.StatusNormalClosure, "bye") }
