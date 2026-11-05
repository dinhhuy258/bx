package serve

import (
	"io"
	"net"
	"strconv"
	"testing"
)

// Open reports whether port has a forward.
func (f *Forwards) Open(port int) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.open[port] != nil
}

// echo listens on [::1]:0 and echoes, standing in for an app bound to loopback.
func echo(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "[::1]:0")
	if err != nil {
		t.Skip("no IPv6 loopback:", err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() { io.Copy(c, c); c.Close() }()
		}
	}()
	return l.Addr().(*net.TCPAddr).Port
}

func TestForwardsSync(t *testing.T) {
	port := echo(t)
	f := NewForwards("127.0.0.1")
	t.Cleanup(f.Close)
	got := f.Sync(map[int]string{port: "[::1]:" + strconv.Itoa(port)})
	if got[port] != nil {
		t.Fatalf("open: %v", got[port])
	}
	c, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		t.Fatal(err)
	}
	c.Write([]byte("ping"))
	buf := make([]byte, 4)
	if _, err := io.ReadFull(c, buf); err != nil || string(buf) != "ping" {
		t.Fatalf("echo: %q %v", buf, err)
	}
	c.Close()

	// one scan without the port keeps it, the second closes it
	f.Sync(map[int]string{})
	if !f.Open(port) {
		t.Error("closed after one miss")
	}
	f.Sync(map[int]string{})
	if f.Open(port) {
		t.Error("open after two misses")
	}
}

func TestForwardsTaken(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	port := busy.Addr().(*net.TCPAddr).Port
	f := NewForwards("127.0.0.1")
	t.Cleanup(f.Close)
	got := f.Sync(map[int]string{port: "[::1]:1"})
	if got[port] == nil || got[port].Error() != "taken" {
		t.Fatalf("taken: %v", got[port])
	}
	busy.Close()
	if got = f.Sync(map[int]string{port: "[::1]:1"}); got[port] != nil {
		t.Errorf("retry: %v", got[port])
	}
}
