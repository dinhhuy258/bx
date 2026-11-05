package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/dinhhuy258/bx/internal/runcfg"
	"github.com/dinhhuy258/bx/internal/state"
)

// How long a stop waits: for the runs to end after C-c, polling every stopPoll, and for a
// run's stop command. Tests shorten them.
var (
	stopWait    = 5 * time.Second
	stopPoll    = 250 * time.Millisecond
	stopCmdWait = 30 * time.Second
)

// RunConflictError refuses a run while another story of the project runs: Story and its runs.
// Resend with Replace to stop it first.
type RunConflictError struct {
	Story string
	Runs  []string
}

func (e *RunConflictError) Error() string {
	return fmt.Sprintf("%s is running %s", e.Story, strings.Join(e.Runs, ", "))
}

// RunOpts are a run: the runs to start (nil = all), Replace stops the story that runs,
// Setup runs the setups even when they ran, Host fills ${host}.
type RunOpts struct {
	Names   []string
	Replace bool
	Setup   bool
	Host    string
}

// RunConfig reads the project's run config; none is an empty one.
func (a *App) RunConfig(p *state.Project) (runcfg.File, error) {
	data, err := os.ReadFile(a.Cfg.AppFile(p.Name))
	if errors.Is(err, os.ErrNotExist) {
		return runcfg.File{}, nil
	}
	if err != nil {
		return runcfg.File{}, err
	}
	return runcfg.Parse(data)
}

// SaveRunConfig writes the run config, replacing the file in one rename.
func (a *App) SaveRunConfig(p *state.Project, data []byte) error {
	if err := os.MkdirAll(a.Cfg.AppsDir(), 0o755); err != nil {
		return err
	}
	tmp := a.Cfg.AppFile(p.Name) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, a.Cfg.AppFile(p.Name))
}

// DeleteRunConfig removes the run config; none is not an error.
func (a *App) DeleteRunConfig(p *state.Project) error {
	if err := os.Remove(a.Cfg.AppFile(p.Name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// runShells are the project's run shells, of story when it is not "".
func (a *App) runShells(p *state.Project, story string) ([]Shell, error) {
	all, err := a.Shells(p)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(all, func(sh Shell) bool { return sh.Run == "" || story != "" && sh.Story != story }), nil
}

// RunningStory is the story whose runs are open, "" when none.
func (a *App) RunningStory(p *state.Project) (string, error) {
	rs, err := a.runShells(p, "")
	if err != nil || len(rs) == 0 {
		return "", err
	}
	return rs[0].Story, nil
}

// RunStory starts runs of a story, each in its run shell: a shell with @bx_run that types
// `bx run-exec`. A run whose shell exists is typed again when the shell is idle, left alone
// when it runs. Another story running refuses with a RunConflictError unless Replace.
func (a *App) RunStory(ctx context.Context, p *state.Project, story string, o RunOpts) error {
	if p.Stories[story] == nil {
		return fmt.Errorf("no story %q", story)
	}
	f, err := a.RunConfig(p)
	if err != nil {
		return err
	}
	if len(f.Runs) == 0 {
		return errors.New("no runs: add one in Settings › Apps")
	}
	names := o.Names
	if len(names) == 0 {
		names = f.Names()
	}
	for _, n := range names {
		if _, ok := f.Find(n); !ok {
			return fmt.Errorf("no run %q", n)
		}
	}
	all, err := a.runShells(p, "")
	if err != nil {
		return err
	}
	var other []string
	otherStory := ""
	for _, sh := range all {
		if sh.Story != story {
			otherStory, other = sh.Story, append(other, sh.Run)
		}
	}
	if otherStory != "" {
		if !o.Replace {
			return &RunConflictError{Story: otherStory, Runs: other}
		}
		if err := a.StopStory(ctx, p, otherStory, nil); err != nil {
			return err
		}
	}
	env, err := a.accountEnv(ctx, p.Name, a.Dir(p, story))
	if err != nil {
		return err
	}
	if o.Host != "" {
		env = append(env, "BX_APP_HOST="+o.Host)
	}
	for _, n := range names {
		i := slices.IndexFunc(all, func(sh Shell) bool { return sh.Story == story && sh.Run == n })
		sid := ""
		if i >= 0 {
			if !plainShell(all[i].Command) {
				continue // still running
			}
			sid = all[i].ID
		} else {
			_, id, err := a.openShellID(p, story, "", "", env, [][2]string{{"@bx_run", n}})
			if err != nil {
				return err
			}
			sid = id
		}
		if err := a.Tmux.Submit(sid, a.runExecLine(p.Name, story, n, o.Setup, false)); err != nil {
			return err
		}
	}
	return nil
}

// runExecLine is the command a run shell types.
func (a *App) runExecLine(project, story, name string, setup, stop bool) string {
	self := a.Self
	if self == "" {
		self = "bx"
	}
	parts := []string{sq(self), "run-exec"}
	if setup {
		parts = append(parts, "-setup")
	}
	if stop {
		parts = append(parts, "-stop")
	}
	return strings.Join(append(parts, sq(project), sq(story), sq(name)), " ")
}

// StopStory stops runs of a story (nil = all): C-c, the run's stop command when the run has
// one, docker stop for its containers, then the shells close.
func (a *App) StopStory(ctx context.Context, p *state.Project, story string, names []string) error {
	shells, err := a.runShells(p, story)
	if err != nil {
		return err
	}
	if len(names) > 0 {
		shells = slices.DeleteFunc(shells, func(sh Shell) bool { return !slices.Contains(names, sh.Run) })
	}
	if len(shells) == 0 {
		return nil
	}
	for _, sh := range shells {
		_ = a.Tmux.SendKeys(sh.ID, "C-c") // it may have exited meanwhile
	}
	a.waitIdle(p, shells, stopWait)
	f, _ := a.RunConfig(p) // a broken config still stops: no stop commands then
	var stopping []Shell
	for _, sh := range shells {
		if r, ok := f.Find(sh.Run); ok && r.Stop != "" {
			if err := a.Tmux.Submit(sh.ID, a.runExecLine(p.Name, story, sh.Run, false, true)); err == nil {
				stopping = append(stopping, sh)
			}
		}
	}
	a.waitIdle(p, stopping, stopCmdWait)
	if a.Docker != nil {
		args := []string{"ps", "-q", "--filter", "label=bx.project=" + p.Name, "--filter", "label=bx.story=" + story}
		if len(names) == 1 {
			args = append(args, "--filter", "label=bx.run="+names[0])
		}
		if out, err := a.Docker(ctx, args...); err != nil {
			a.logf("docker ps: %v", err)
		} else if ids := strings.Fields(out); len(ids) > 0 {
			if _, err := a.Docker(ctx, append([]string{"stop"}, ids...)...); err != nil {
				a.logf("docker stop: %v", err)
			}
		}
	}
	for _, sh := range shells {
		_ = a.Tmux.KillSession(sh.ID) // may be gone already
	}
	return nil
}

// waitIdle waits until every shell is back at its prompt or gone, or for at most d.
func (a *App) waitIdle(p *state.Project, shells []Shell, d time.Duration) {
	if len(shells) == 0 {
		return
	}
	for end := time.Now().Add(d); time.Now().Before(end); time.Sleep(stopPoll) {
		now, err := a.Shells(p)
		if err != nil {
			return
		}
		busy := false
		for _, sh := range shells {
			i := slices.IndexFunc(now, func(x Shell) bool { return x.ID == sh.ID })
			if i >= 0 && !plainShell(now[i].Command) {
				busy = true
			}
		}
		if !busy {
			return
		}
	}
}

// SetupAgain stops a run and starts it with its setup, which then runs even when it ran.
func (a *App) SetupAgain(ctx context.Context, p *state.Project, story, name, host string) error {
	if err := a.StopStory(ctx, p, story, []string{name}); err != nil {
		return err
	}
	_ = os.Remove(a.Cfg.SetupMark(p.Name, story, name))
	return a.RunStory(ctx, p, story, RunOpts{Names: []string{name}, Setup: true, Host: host})
}

// plainShell reports whether a pane runs only the shell, the same rule as ui.PlainShell.
func plainShell(cmd string) bool {
	switch strings.TrimPrefix(cmd, "-") {
	case "zsh", "bash", "sh", "fish":
		return true
	}
	return false
}

// sq quotes s for the shell when it holds anything but safe characters.
func sq(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789/._-+:@") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
