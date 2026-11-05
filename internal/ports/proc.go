// Package ports finds the TCP ports the apps of bx stories listen on: the listening sockets
// in /proc, the process that holds each one, and the story and bx shell or session that
// process belongs to. It reads Linux's /proc; elsewhere Read finds nothing.
package ports

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Proc is the part of /proc the scan reads. OS reads the real one; tests use a map.
type Proc interface {
	ReadFile(name string) ([]byte, error)
	ReadDir(name string) ([]string, error) // entry names
	Readlink(name string) (string, error)
}

// OS is the machine's /proc.
var OS Proc = osProc{}

type osProc struct{}

func (osProc) ReadFile(name string) ([]byte, error) { return os.ReadFile(name) }
func (osProc) Readlink(name string) (string, error) { return os.Readlink(name) }
func (osProc) ReadDir(name string) ([]string, error) {
	es, err := os.ReadDir(name)
	out := make([]string, 0, len(es))
	for _, e := range es {
		out = append(out, e.Name())
	}
	return out, err
}

// Listener is a socket in the LISTEN state. Loopback means it takes only 127.0.0.1 or ::1,
// so the Tailscale address needs a forward.
type Listener struct {
	Port     int
	Inode    uint64
	Loopback bool
}

// Process is a process that holds a listener. Container is the Docker container id from its
// cgroup, "" on the host.
type Process struct {
	PID, PPID int
	Cwd       string
	Container string
}

// Scan is one look at /proc: the listeners, the pid holding each inode (0 when no readable
// process holds it, e.g. one of root's), and those processes.
type Scan struct {
	Listeners []Listener
	Owners    map[uint64]int
	Procs     map[int]Process
}

// ParseTCP reads /proc/net/tcp (v6 false) or tcp6: the LISTEN (0A) rows.
func ParseTCP(data []byte, v6 bool) []Listener {
	var out []Listener
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Scan() // header
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 10 || f[3] != "0A" {
			continue
		}
		addr, port, ok := strings.Cut(f[1], ":")
		if !ok {
			continue
		}
		p, err1 := strconv.ParseUint(port, 16, 16)
		inode, err2 := strconv.ParseUint(f[9], 10, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		out = append(out, Listener{Port: int(p), Inode: inode, Loopback: loopback(addr, v6)})
	}
	return out
}

// loopback reports whether a hex /proc address is 127.0.0.0/8 or ::1 (also ::ffff:127.x).
// The words are little-endian 32-bit groups.
func loopback(hex string, v6 bool) bool {
	if !v6 {
		return len(hex) == 8 && strings.HasSuffix(hex, "7F")
	}
	switch {
	case hex == "00000000000000000000000001000000":
		return true
	case len(hex) == 32 && hex[:16] == "0000000000000000" && hex[16:24] == "FFFF0000":
		return strings.HasSuffix(hex, "7F")
	}
	return false
}

var containerID = regexp.MustCompile(`[0-9a-f]{64}`)

// Read scans p: the listeners of tcp and tcp6, then every process's fds for their inodes.
func Read(p Proc) (Scan, error) {
	s := Scan{Owners: map[uint64]int{}, Procs: map[int]Process{}}
	for _, f := range []struct {
		name string
		v6   bool
	}{{"/proc/net/tcp", false}, {"/proc/net/tcp6", true}} {
		data, err := p.ReadFile(f.name)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return s, err
		}
		s.Listeners = append(s.Listeners, ParseTCP(data, f.v6)...)
	}
	if len(s.Listeners) == 0 {
		return s, nil
	}
	want := map[string]uint64{}
	for _, l := range s.Listeners {
		s.Owners[l.Inode] = 0
		want["socket:["+strconv.FormatUint(l.Inode, 10)+"]"] = l.Inode
	}
	pids, err := p.ReadDir("/proc")
	if err != nil {
		return s, err
	}
	for _, name := range pids {
		pid, err := strconv.Atoi(name)
		if err != nil {
			continue
		}
		dir := "/proc/" + name
		fds, err := p.ReadDir(dir + "/fd")
		if err != nil {
			continue // another user's process
		}
		held := false
		for _, fd := range fds {
			link, err := p.Readlink(dir + "/fd/" + fd)
			if inode, ok := want[link]; err == nil && ok {
				s.Owners[inode], held = pid, true
			}
		}
		if held {
			s.Procs[pid] = readProcess(p, pid)
		}
	}
	return s, nil
}

// Parent is the parent pid of pid, read from /proc when the scan does not hold it.
func Parent(p Proc, pid int) int { return readProcess(p, pid).PPID }

func readProcess(p Proc, pid int) Process {
	dir := "/proc/" + strconv.Itoa(pid)
	pr := Process{PID: pid}
	if stat, err := p.ReadFile(dir + "/stat"); err == nil {
		// "pid (comm) state ppid ...": comm may hold spaces and ")"
		if i := bytes.LastIndexByte(stat, ')'); i >= 0 {
			if f := strings.Fields(string(stat[i+1:])); len(f) > 1 {
				pr.PPID, _ = strconv.Atoi(f[1])
			}
		}
	}
	if cwd, err := p.Readlink(dir + "/cwd"); err == nil {
		pr.Cwd = filepath.Clean(cwd)
	}
	if cg, err := p.ReadFile(dir + "/cgroup"); err == nil {
		pr.Container = containerID.FindString(string(cg))
	}
	return pr
}
