package ports

import (
	"bytes"
	"strconv"
	"strings"
)

// Job is what runs in the foreground of the terminal of the shell pid: the argv of the leader
// of the terminal's foreground process group. busy is false when the shell's own group has the
// terminal (it waits at its prompt). ok is false when /proc does not tell (not Linux, the shell
// is gone, or it has no terminal). A busy job whose leader already exited has a nil argv.
//
// tmux names a pane by its foreground process's comm, which is the interpreter for a script
// ("bash" for ./vendor/bin/sail), so it cannot tell a script from a shell at its prompt; the
// foreground group can.
func Job(p Proc, pid int) (argv []string, busy, ok bool) {
	stat, err := p.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return nil, false, false
	}
	// "pid (comm) state ppid pgrp session tty_nr tpgid ...": comm may hold spaces and ")"
	i := bytes.LastIndexByte(stat, ')')
	if i < 0 {
		return nil, false, false
	}
	f := strings.Fields(string(stat[i+1:]))
	if len(f) < 6 {
		return nil, false, false
	}
	pgrp, err1 := strconv.Atoi(f[2])
	tpgid, err2 := strconv.Atoi(f[5])
	if err1 != nil || err2 != nil || tpgid <= 0 {
		return nil, false, false
	}
	if tpgid == pgrp {
		return nil, false, true
	}
	cmdline, err := p.ReadFile("/proc/" + strconv.Itoa(tpgid) + "/cmdline")
	if err != nil || len(cmdline) == 0 {
		return nil, true, true
	}
	return strings.Split(strings.TrimRight(string(cmdline), "\x00"), "\x00"), true, true
}
