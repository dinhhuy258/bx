package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/dinhhuy258/bx/internal/ports"
	"github.com/dinhhuy258/bx/internal/state"
	"github.com/dinhhuy258/bx/internal/tmux"
)

// ShellName is the tmux session of shell k of a story ("" = the project root). It uses the
// same replacements as TmuxName. Shells are found by their options, never by this name.
func ShellName(project, story string, k int) string {
	name := fmt.Sprintf("bxsh-%s-%d", project, k)
	if story != "" {
		name = fmt.Sprintf("bxsh-%s-%s-%d", project, story, k)
	}
	return strings.NewReplacer(".", "_", ":", "_").Replace(name)
}

// Shell is a running shell of a project: shell K of Story ("" = chats, the project folder).
// Title is its rename ("" = none) and Command what its pane runs now. Run is the run of a
// run shell (see run.go); "" for a plain shell.
type Shell struct {
	ID, Story           string
	K                   int
	Title, Command, Run string
}

// Shells lists the project's shells, sorted by story then number.
func (a *App) Shells(p *state.Project) ([]Shell, error) {
	all, err := a.Tmux.Shells()
	if err != nil {
		return nil, err
	}
	var out []Shell
	for _, sh := range all {
		if sh.Project == p.Name {
			out = append(out, Shell{ID: sh.ID, Story: sh.Story, K: sh.K, Title: sh.Title, Command: a.paneCommand(sh), Run: sh.Run})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Story != out[j].Story {
			return out[i].Story < out[j].Story
		}
		return out[i].K < out[j].K
	})
	return out, nil
}

// ShellTarget is the tmux id of shell k of a story.
func (a *App) ShellTarget(p *state.Project, story string, k int) (string, error) {
	shells, err := a.Shells(p)
	if err != nil {
		return "", err
	}
	for _, sh := range shells {
		if sh.Story == story && sh.K == k {
			return sh.ID, nil
		}
	}
	return "", fmt.Errorf("shell %s$%d is %w", shellGroup(story), k, ErrNotRunning)
}

// NewShell starts a shell in the story's folder ("" = the project folder) and returns its
// number: the lowest one the story's shells do not use. The shell's tmux options say which
// project, story and number it is.
func (a *App) NewShell(p *state.Project, story string) (int, error) {
	env, err := a.accountEnv(context.Background(), p.Name, a.Dir(p, story))
	if err != nil {
		return 0, err
	}
	return a.openShell(p, story, "", "", env)
}

// openShell starts shell k of a story running cmd ("" = the default shell) with env, titled
// title ("" = its command).
func (a *App) openShell(p *state.Project, story, cmd, title string, env []string) (int, error) {
	k, _, err := a.openShellID(p, story, cmd, title, env, nil)
	return k, err
}

// openShellID is openShell that also gives the tmux id and sets the extra options.
func (a *App) openShellID(p *state.Project, story, cmd, title string, env []string, extra [][2]string) (int, string, error) {
	if story != "" && p.Stories[story] == nil {
		return 0, "", fmt.Errorf("no story %q", story)
	}
	shells, err := a.Shells(p)
	if err != nil {
		return 0, "", err
	}
	used := map[int]bool{}
	for _, sh := range shells {
		if sh.Story == story {
			used[sh.K] = true
		}
	}
	k := 1
	for used[k] {
		k++
	}
	id, err := a.Tmux.NewShell(ShellName(p.Name, story, k), a.Dir(p, story), cmd, env...)
	if err != nil {
		return 0, "", err
	}
	opts := [][2]string{{"@bx_project", p.Name}}
	if story != "" {
		opts = append(opts, [2]string{"@bx_story", story})
	}
	opts = append(opts, [2]string{"@bx_shell", strconv.Itoa(k)})
	if title != "" {
		opts = append(opts, [2]string{"@bx_title", title})
	}
	opts = append(opts, extra...)
	for _, o := range opts {
		if err := a.Tmux.SetOption(id, o[0], o[1]); err != nil {
			return 0, "", errors.Join(err, a.Tmux.KillSession(id))
		}
	}
	// best effort, as in launch
	if err := a.Tmux.KeepServer(); err != nil {
		a.logf("tmux keep server: %v", err)
	}
	return k, id, nil
}

// CloseShell kills shell k of a story.
func (a *App) CloseShell(p *state.Project, story string, k int) error {
	id, err := a.ShellTarget(p, story, k)
	if err != nil {
		return err
	}
	return a.Tmux.KillSession(id)
}

// RenameShell sets the name shown for shell k of a story. Control characters become spaces;
// an empty title removes the name, so the shell shows its command again.
func (a *App) RenameShell(p *state.Project, story string, k int, title string) error {
	id, err := a.ShellTarget(p, story, k)
	if err != nil {
		return err
	}
	title = strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, title))
	if title == "" {
		return a.Tmux.UnsetOption(id, "@bx_title")
	}
	return a.Tmux.SetOption(id, "@bx_title", title)
}

// shellGroup names a story's group in messages: the story, or "chats" for the project folder.
func shellGroup(story string) string {
	if story == "" {
		return "chats"
	}
	return story
}

// paneCommand is what the pane of sh runs. tmux names a pane by its foreground process, and a
// script runs as its interpreter: ./vendor/bin/sail shows as "bash", the same as the shell at
// its prompt. When tmux names a bare shell, /proc tells the two apart: another process group
// in the terminal's foreground is a job, named after its script or program ("sail"), or "job"
// when its leader already exited. A nested interactive shell still counts as the prompt.
func (a *App) paneCommand(sh tmux.Shell) string {
	if sh.PID <= 0 || !plainShell(sh.Command) {
		return sh.Command
	}
	proc := a.Proc
	if proc == nil {
		proc = ports.OS
	}
	argv, busy, ok := ports.Job(proc, sh.PID)
	switch {
	case !ok || !busy:
		return sh.Command
	case argv == nil:
		return "job"
	}
	if name := jobName(argv); name != "" && !plainShell(name) {
		return name
	}
	return sh.Command
}

// jobName is the program argv runs, past the interpreters in front of it: "sail" for
// [bash ./vendor/bin/sail up], "npm" for [sh -c "npm run dev"], "node" for [env PORT=1 node a.js].
// It is "" when argv is only a shell.
func jobName(argv []string) string {
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		if i > 0 && strings.HasPrefix(arg, "-") {
			if commandFlag(arg) && i+1 < len(argv) {
				if f := strings.Fields(argv[i+1]); len(f) > 0 {
					return filepath.Base(f[0])
				}
				return ""
			}
			continue
		}
		base := filepath.Base(strings.TrimPrefix(arg, "-"))
		if base == "env" || plainShell(base) || base == "dash" || (i > 0 && strings.Contains(arg, "=")) {
			continue
		}
		return base
	}
	return ""
}

// commandFlag reports whether a shell flag takes a command string: -c, or -lc and the like.
func commandFlag(f string) bool {
	return !strings.HasPrefix(f, "--") && strings.Contains(f[1:], "c")
}
