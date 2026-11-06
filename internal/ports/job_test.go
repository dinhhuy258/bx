package ports

import (
	"reflect"
	"testing"
)

func TestJob(t *testing.T) {
	p := Fake{Files: map[string]string{
		// pid (comm) state ppid pgrp session tty_nr tpgid
		"/proc/10/stat":    "10 (bash) S 1 10 10 34816 10 4194560", // at its prompt
		"/proc/20/stat":    "20 (bash) S 1 20 20 34817 25 4194560", // runs the sail script
		"/proc/25/cmdline": "bash\x00./vendor/bin/sail\x00up\x00",
		"/proc/30/stat":    "30 (my (odd) sh) S 1 30 30 34818 35 4194560", // comm with ") "
		"/proc/40/stat":    "40 (bash) S 1 40 40 34819 45 4194560",        // leader already gone
		"/proc/50/stat":    "50 (bash) S 1 50 50 0 -1 4194560",            // no terminal
		"/proc/60/stat":    "60 (bash",                                    // cut short
		"/proc/35/cmdline": "node\x00server.js\x00",
	}}
	cases := []struct {
		pid      int
		argv     []string
		busy, ok bool
	}{
		{10, nil, false, true},
		{20, []string{"bash", "./vendor/bin/sail", "up"}, true, true},
		{30, []string{"node", "server.js"}, true, true},
		{40, nil, true, true},
		{50, nil, false, false},
		{60, nil, false, false},
		{99, nil, false, false}, // no such process
	}
	for _, c := range cases {
		argv, busy, ok := Job(p, c.pid)
		if !reflect.DeepEqual(argv, c.argv) || busy != c.busy || ok != c.ok {
			t.Errorf("Job(%d) = %q %v %v, want %q %v %v", c.pid, argv, busy, ok, c.argv, c.busy, c.ok)
		}
	}
}
