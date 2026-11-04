package client

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
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

	copied []string // clipboard text from OSC 52 not yet taken
}

// NewTerm builds an emulator of cols x rows and starts the input reader.
func NewTerm(cols, rows int) *Term {
	t := &Term{emu: vt.NewEmulator(cols, rows), input: make(chan []byte, 64), cols: cols, rows: rows}
	t.emu.RegisterOscHandler(52, t.osc52)
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

// osc52 keeps the text a program puts on the clipboard ("52;c;<base64>"); tmux sends one on a
// copy-mode yank. It runs inside Feed, under mu.
func (t *Term) osc52(data []byte) bool {
	parts := bytes.SplitN(data, []byte{';'}, 3)
	if len(parts) != 3 {
		return false
	}
	if b, err := base64.StdEncoding.DecodeString(string(parts[2])); err == nil && len(b) > 0 {
		t.copied = append(t.copied, string(b)) // "?" (a query) is never answered
	}
	return true
}

// TakeCopied returns the clipboard text received since the last call.
func (t *Term) TakeCopied() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	c := t.copied
	t.copied = nil
	return c
}

// SendKey encodes a key press for the program on the other side.
func (t *Term) SendKey(k tea.KeyPressMsg) { t.emu.SendKey(uv.KeyPressEvent(uv.Key(tea.Key(k)))) }

// Paste sends pasted text, bracketed when the program asked for bracketed paste (a mode Feed
// sets, hence the lock).
func (t *Term) Paste(text string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.emu.Paste(text)
}

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

// Close stops the input reader: readInput gets EOF, closes Input and exits. It closes the
// emulator's input pipe directly rather than calling emu.Close, which sets an unsynchronized
// flag that readInput checks on every Read (a data race).
func (t *Term) Close() {
	if c, ok := t.emu.InputPipe().(io.Closer); ok {
		_ = c.Close()
	}
}

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
	clipboardMsg string              // text copied in tmux copy-mode
	copyEndMsg   struct{ conn Conn } // the stream's pane left copy-mode
	pageMsg      struct {            // Claude wrote a page in the stream's session
		conn  Conn
		page  api.Artifact
		isNew bool
	}
	pageSeenMsg struct{ conn Conn } // another client opened or dismissed the page
)

// Connect dials one session's stream and starts the pumps: one streamOutputMsg per burst of
// output (the model redraws), then streamClosedMsg.
func Connect(ctx context.Context, url string, term *Term, send func(tea.Msg)) (Conn, error) {
	c, resp, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		if resp != nil && resp.StatusCode != http.StatusSwitchingProtocols {
			// e.g. 410: the session or shell is not running
			return nil, &StatusError{Code: resp.StatusCode, Text: err.Error()}
		}
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
			for _, text := range s.term.TakeCopied() {
				send(clipboardMsg(text))
			}
			send(streamOutputMsg{})
		case websocket.MessageText:
			var ctl api.Control
			if json.Unmarshal(data, &ctl) != nil {
				continue
			}
			switch {
			case ctl.Type == "copy_end":
				send(copyEndMsg{s})
			case ctl.Type == "page" && ctl.Page != nil:
				send(pageMsg{s, *ctl.Page, ctl.New})
			case ctl.Type == "page_seen":
				send(pageSeenMsg{s})
			}
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
	return s.control(ctx, api.Control{Type: "resize", Cols: cols, Rows: rows})
}

// Scroll moves the session's view through its history (see api.Control).
func (s *Stream) Scroll(ctx context.Context, lines int) error {
	return s.control(ctx, api.Control{Type: "scroll", Lines: lines})
}

// PageSeen tells the server the session's page was opened or dismissed.
func (s *Stream) PageSeen(ctx context.Context) error {
	return s.control(ctx, api.Control{Type: "page_seen"})
}

// control sends a text frame to the server.
func (s *Stream) control(ctx context.Context, ctl api.Control) error {
	b, _ := json.Marshal(ctl)
	return s.conn.Write(ctx, websocket.MessageText, b)
}

// Close ends the stream.
func (s *Stream) Close() { _ = s.conn.Close(websocket.StatusNormalClosure, "bye") }
