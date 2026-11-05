package serve

import (
	"errors"
	"io"
	"net"
	"strconv"
	"sync"
	"syscall"
)

// errTaken is a forward whose port something else holds on the Tailscale address.
var errTaken = errors.New("taken")

// Forwards are the TCP forwards from the Tailscale address to runs that listen on loopback
// only, one per port, same port. Sync opens and closes them after every port scan.
type Forwards struct {
	Host   string // the address the forwards listen on: the Tailscale IP
	listen func(network, addr string) (net.Listener, error)

	mu   sync.Mutex
	open map[int]*forward
}

type forward struct {
	l      net.Listener
	dial   string
	misses int
}

// NewForwards gives forwards listening on host.
func NewForwards(host string) *Forwards {
	return &Forwards{Host: host, listen: net.Listen, open: map[int]*forward{}}
}

// Open reports whether port has a forward.
func (f *Forwards) Open(port int) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.open[port] != nil
}

// Sync makes the forwards match want (port → the loopback address to dial). A forward stays
// through one scan without its port, so an app that restarts keeps it. The result holds an
// entry per wanted port: nil when it is open, errTaken or the bind error when not; a failed
// port is tried again on the next Sync.
func (f *Forwards) Sync(want map[int]string) map[int]error {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[int]error{}
	for port, fw := range f.open {
		if _, ok := want[port]; ok {
			fw.misses = 0
			continue
		}
		if fw.misses++; fw.misses >= 2 {
			fw.l.Close()
			delete(f.open, port)
		}
	}
	for port, dial := range want {
		if fw := f.open[port]; fw != nil {
			fw.dial = dial
			continue
		}
		l, err := f.listen("tcp", net.JoinHostPort(f.Host, strconv.Itoa(port)))
		if err != nil {
			if errors.Is(err, syscall.EADDRINUSE) {
				err = errTaken
			}
			out[port] = err
			continue
		}
		fw := &forward{l: l, dial: dial}
		f.open[port] = fw
		go f.serve(fw)
		out[port] = nil
	}
	return out
}

func (f *Forwards) serve(fw *forward) {
	for {
		c, err := fw.l.Accept()
		if err != nil {
			return
		}
		f.mu.Lock()
		dial := fw.dial
		f.mu.Unlock()
		go splice(c, dial)
	}
}

// splice copies both ways between c and a new connection to addr, closing each write side
// when its reader ends.
func splice(c net.Conn, addr string) {
	defer c.Close()
	b, err := net.Dial("tcp", addr)
	if err != nil {
		return
	}
	defer b.Close()
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(b, c)
		if t, ok := b.(*net.TCPConn); ok {
			_ = t.CloseWrite()
		}
		close(done)
	}()
	_, _ = io.Copy(c, b)
	if t, ok := c.(*net.TCPConn); ok {
		_ = t.CloseWrite()
	}
	<-done
}

// Close closes every forward.
func (f *Forwards) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for port, fw := range f.open {
		fw.l.Close()
		delete(f.open, port)
	}
}
