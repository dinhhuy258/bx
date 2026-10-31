package tmux

import (
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

func TestWriteConfOnlyOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tmux.conf")
	if err := WriteConf(path); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	for _, want := range []string{"set -g prefix None", "set -g status off", "set -g escape-time 0", "window-size latest"} {
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
