package tmux

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunnerArgs(t *testing.T) {
	r := &Runner{Socket: "/s/tmux.sock", Config: "/s/tmux.conf"}
	got := strings.Join(r.Command("list-sessions").Args, " ")
	if got != "tmux -S /s/tmux.sock -f /s/tmux.conf list-sessions" {
		t.Fatal(got)
	}
	if got := strings.Join((&Runner{}).Command("ls").Args, " "); got != "tmux ls" {
		t.Fatal(got)
	}
}

func TestNewSessionAndSessions(t *testing.T) {
	f := &Fake{Replies: map[string]string{
		"new-session -d -s bx-p-1 -c /w -P -F #{session_id} claude --session-id u": "$4\n",
		"list-sessions -F #{session_id}\t#{session_name}":                          "$4\tbx-p-1\n$5\tother\n",
	}}
	tm := New(f)
	id, err := tm.NewSession("bx-p-1", "/w", "claude --session-id u")
	if err != nil || id != "$4" {
		t.Fatalf("%q %v", id, err)
	}
	ss, err := tm.Sessions()
	if err != nil || len(ss) != 2 || ss[0] != (Session{ID: "$4", Name: "bx-p-1"}) {
		t.Fatalf("%+v %v", ss, err)
	}
	if err := tm.SendKeys("$4", "Enter"); err != nil {
		t.Fatal(err)
	}
	if err := tm.KillSession("$4"); err != nil {
		t.Fatal(err)
	}
	var calls []string
	for _, c := range f.Calls {
		calls = append(calls, strings.Join(c, " "))
	}
	// the fake records every call it has a reply for, queries included
	want := "new-session -d -s bx-p-1 -c /w -P -F #{session_id} claude --session-id u|list-sessions -F #{session_id}\t#{session_name}|send-keys -t $4 Enter|kill-session -t $4"
	if strings.Join(calls, "|") != want {
		t.Fatalf("calls:\n%s", strings.Join(calls, "\n"))
	}
}

type failExec struct{ err error }

func (x failExec) Run(...string) ([]byte, error) { return nil, x.err }

func TestListSaysWhetherTheServerAnswered(t *testing.T) {
	f := &Fake{Replies: map[string]string{"list-sessions -F #{session_id}\t#{session_name}": "$4\tbx-p-1\n"}}
	ss, up, err := New(f).List()
	if err != nil || !up || len(ss) != 1 || ss[0] != (Session{ID: "$4", Name: "bx-p-1"}) {
		t.Fatalf("%+v %v %v", ss, up, err)
	}
	tm := New(failExec{errors.New("tmux list-sessions: no server running on /tmp/x")})
	ss, up, err = tm.List()
	if err != nil || up || ss != nil {
		t.Fatalf("no server: %+v %v %v", ss, up, err)
	}
	if ss, err := tm.Sessions(); err != nil || ss != nil {
		t.Fatalf("Sessions with no server: %+v %v", ss, err)
	}
	if _, up, err := New(failExec{errors.New("boom")}).List(); err == nil || up {
		t.Fatalf("other errors are errors: %v %v", up, err)
	}
}

func TestScroll(t *testing.T) {
	f := &Fake{}
	tm := New(f)
	for _, n := range []int{-3, 3, 0} {
		if err := tm.Scroll("$4", n); err != nil {
			t.Fatal(err)
		}
	}
	var calls []string
	for _, c := range f.Calls {
		calls = append(calls, strings.Join(c, " "))
	}
	want := "copy-mode -e -t $4|send-keys -t $4 -X -N 3 scroll-up|send-keys -t $4 -X -N 3 scroll-down|send-keys -t $4 -X cancel"
	if strings.Join(calls, "|") != want {
		t.Fatalf("calls:\n%s", strings.Join(calls, "\n"))
	}
	// leaving copy-mode when not in it is fine; entering it must work
	f.Err = errors.New("not in a mode")
	if tm.Scroll("$4", 0) != nil || tm.Scroll("$4", 3) != nil || tm.Scroll("$4", -3) == nil {
		t.Fatal("only scrolling back reports errors")
	}
}

func TestCopyMode(t *testing.T) {
	f := &Fake{Replies: map[string]string{"display-message -p -t $4 #{pane_in_mode}": "1\n"}}
	tm := New(f)
	if err := tm.PrepareCopy(); err != nil {
		t.Fatal(err)
	}
	if err := tm.CopyMode("$4"); err != nil {
		t.Fatal(err)
	}
	if in, err := tm.InMode("$4"); err != nil || !in {
		t.Fatalf("in=%v err=%v", in, err)
	}
	var calls []string
	for _, c := range f.Calls {
		calls = append(calls, strings.Join(c, " "))
	}
	got := strings.Join(calls, "\n")
	for _, want := range []string{
		"set-option -g mode-keys vi", "set-option -g assume-paste-time 0", "set-option -s set-clipboard on",
		"set-option -s terminal-features[90] xterm-256color:clipboard",
		"bind-key -T copy-mode-vi v send-keys -X begin-selection",
		"bind-key -T copy-mode-vi y send-keys -X copy-pipe-and-cancel",
		"copy-mode -t $4",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
}

func TestWriteConfOnlyOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tmux.conf")
	if err := WriteConf(path); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	for _, want := range []string{"set -g prefix None", "set -g status off", "set -g escape-time 0", "window-size latest", "set -s exit-empty off"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("missing %q", want)
		}
	}
	os.WriteFile(path, []byte("# mine\n"), 0o644)
	if err := WriteConf(path); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "# mine\n" {
		t.Fatal("WriteConf overwrote the user's file")
	}
}

// TestRealServer runs against a private tmux server.
func TestRealServer(t *testing.T) {
	if os.Getenv("BX_INTEGRATION") != "1" {
		t.Skip("set BX_INTEGRATION=1")
	}
	dir, err := os.MkdirTemp("", "bxt") // short path: unix socket paths are limited to ~104 bytes
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	conf := filepath.Join(dir, "tmux.conf")
	if err := WriteConf(conf); err != nil {
		t.Fatal(err)
	}
	r := &Runner{Socket: filepath.Join(dir, "s"), Config: conf}
	t.Cleanup(func() { r.Command("kill-server").Run() })
	tm := New(r)
	if ss, err := tm.Sessions(); err != nil || len(ss) != 0 {
		t.Fatalf("before the server starts: %+v %v", ss, err)
	}
	id, err := tm.NewSession("bx-p-1", dir, "sleep 30")
	if err != nil || !strings.HasPrefix(id, "$") {
		t.Fatalf("%q %v", id, err)
	}
	ss, _ := tm.Sessions()
	if len(ss) != 1 || ss[0].Name != "bx-p-1" {
		t.Fatalf("%+v", ss)
	}
	if out, _ := exec.Command("tmux", "-S", r.Socket, "show", "-gv", "prefix").Output(); strings.TrimSpace(string(out)) != "None" {
		t.Fatalf("conf not loaded: prefix=%q", out)
	}
	if err := tm.KillSession(id); err != nil {
		t.Fatal(err)
	}
}
