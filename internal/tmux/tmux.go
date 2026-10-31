// Package tmux is a thin wrapper over the tmux CLI. Every call is one tmux command so
// behaviour is easy to test against a fake and against a real server on a private socket.
package tmux

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Exec runs one tmux command and returns stdout.
type Exec interface {
	Run(args ...string) ([]byte, error)
}

// Runner runs the real tmux on bx's own server: Socket is passed as -S, Config as -f
// (read only when the server starts).
type Runner struct {
	Socket string
	Config string
}

func (r *Runner) args(args []string) []string {
	var full []string
	if r.Socket != "" {
		full = append(full, "-S", r.Socket)
	}
	if r.Config != "" {
		full = append(full, "-f", r.Config)
	}
	return append(full, args...)
}

// Run implements Exec.
func (r *Runner) Run(args ...string) ([]byte, error) {
	var out, errb bytes.Buffer
	cmd := exec.Command("tmux", r.args(args)...)
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("tmux %s: %s", strings.Join(args, " "), strings.TrimSpace(errb.String()))
	}
	return out.Bytes(), nil
}

// Command builds an exec.Cmd for a tmux command that needs a terminal (attach). It is
// not part of Exec because a fake cannot provide a process.
func (r *Runner) Command(args ...string) *exec.Cmd { return exec.Command("tmux", r.args(args)...) }

// Commander is implemented by an Exec that can start a real tmux client.
type Commander interface {
	Command(args ...string) *exec.Cmd
}

// Session is one row of list-sessions.
type Session struct {
	ID   string
	Name string
}

// Tmux is the API the rest of bx uses.
type Tmux struct{ x Exec }

// New wraps an executor.
func New(x Exec) *Tmux { return &Tmux{x: x} }

func (t *Tmux) run(args ...string) (string, error) {
	out, err := t.x.Run(args...)
	return strings.TrimRight(string(out), "\n"), err
}

// NewSession creates a detached session named name running cmd in dir and returns its id ($n).
func (t *Tmux) NewSession(name, dir, cmd string) (string, error) {
	return t.run("new-session", "-d", "-s", name, "-c", dir, "-P", "-F", "#{session_id}", cmd)
}

// Sessions lists every session with its id and name. No server running means no sessions.
func (t *Tmux) Sessions() ([]Session, error) {
	out, err := t.run("list-sessions", "-F", "#{session_id}\t#{session_name}")
	if err != nil {
		if s := err.Error(); strings.Contains(s, "no server running") || strings.Contains(s, "error connecting") {
			return nil, nil
		}
		return nil, err
	}
	var ss []Session
	for _, line := range strings.Split(out, "\n") {
		if f := strings.SplitN(line, "\t", 2); len(f) == 2 {
			ss = append(ss, Session{ID: f[0], Name: f[1]})
		}
	}
	return ss, nil
}

// KillSession kills a session by id.
func (t *Tmux) KillSession(sid string) error {
	_, err := t.x.Run("kill-session", "-t", sid)
	return err
}

// SendKeys sends keys to a target (a session id targets its active pane).
func (t *Tmux) SendKeys(target string, keys ...string) error {
	_, err := t.x.Run(append([]string{"send-keys", "-t", target}, keys...)...)
	return err
}

// AttachCommand returns `tmux -u attach-session -t sid`, to be run inside a pty.
func (t *Tmux) AttachCommand(sid string) (*exec.Cmd, error) {
	c, ok := t.x.(Commander)
	if !ok {
		return nil, errors.New("tmux executor cannot attach")
	}
	return c.Command("-u", "attach-session", "-t", sid), nil
}

// conf is the tmux.conf of bx's server. tmux is only a process keeper here: no prefix, no
// status line, every key goes to Claude.
const conf = `# Written by bx serve for its private tmux server (-S ~/.bx/run/tmux.sock).
# bx never overwrites this file; delete it to get the defaults back.
# Changes apply after: tmux -S ~/.bx/run/tmux.sock kill-server (stops every Claude).
set -g prefix None
set -g prefix2 None
set -g status off
set -g escape-time 0
set -g default-terminal tmux-256color
set -ga terminal-features ",xterm-256color:RGB"
set-environment -g COLORTERM truecolor
set -g window-size latest
set -g history-limit 50000
`

// WriteConf writes the bx tmux.conf when path does not exist yet.
func WriteConf(path string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := f.WriteString(conf); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
