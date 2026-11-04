// Package app is the orchestration layer: it combines tmux, git, state and the claude
// CLI into the operations bx serve exposes (projects, stories, sessions).
package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"github.com/dinhhuy258/bx/internal/config"
	"github.com/dinhhuy258/bx/internal/git"
	"github.com/dinhhuy258/bx/internal/settings"
	"github.com/dinhhuy258/bx/internal/state"
	"github.com/dinhhuy258/bx/internal/tmux"
)

// App holds the dependencies. UUID and GH are injectable for tests. Settings gives new sessions
// their flags; nil means Claude's defaults.
type App struct {
	Cfg  *config.Config
	Tmux *tmux.Tmux
	Git  *git.Git
	UUID func() string
	Self string // the bx binary, for the hooks it gives Claude; "" gives none
	// PluginDir is the bx Claude plugin (bx:artifact). When set, every session gets an
	// artifacts folder, the plugin, and no claude.ai Artifact tools; "" gives none of that.
	PluginDir string
	// ArtifactsURL is the file server's base, e.g. http://100.64.0.1:7358; "" when it is off,
	// and Claude then gives the file path instead of a link.
	ArtifactsURL string
	// APIURL is bx serve's own base, e.g. http://100.64.0.1:7357. With Self and PluginDir set,
	// every session gets the page hook, which posts there when Claude writes a page; "" gives none.
	APIURL string
	// Transcripts is Claude's projects folder (~/.claude/projects), where Titles reads the
	// first prompt of unnamed sessions; "" leaves them unnamed.
	Transcripts string
	// GH runs gh in dir ("" = bx's own folder); in a repository gh takes the repository and
	// host from its origin. It is killed when ctx is done.
	GH func(ctx context.Context, dir string, args ...string) (string, error)
	// Claude runs claude in dir with env added and stdin, and returns its stdout. It is killed
	// when ctx is done. Tests replace it.
	Claude   func(ctx context.Context, dir string, env []string, stdin string, args ...string) ([]byte, error)
	Settings *settings.Store
	// Log records best-effort failures that do not fail the operation; nil is silent.
	Log func(format string, args ...any)
	tok tokens // gh tokens of the GitHub accounts, see accounts.go
}

// logf calls Log when it is set.
func (a *App) logf(format string, args ...any) {
	if a.Log != nil {
		a.Log(format, args...)
	}
}

// New returns an App on bx's private tmux server, with the real git and gh. Both run as the
// GitHub account of the project they work in (see accounts.go).
func New(cfg *config.Config) *App {
	r := &tmux.Runner{Socket: cfg.TmuxSocket(), Config: cfg.TmuxConf()}
	a := &App{Cfg: cfg, Tmux: tmux.New(r), Git: git.New(), UUID: uuid.NewString}
	a.GH = func(ctx context.Context, dir string, args ...string) (string, error) {
		return runGH(ctx, dir, a.ghEnv(ctx, dir), args...)
	}
	a.Claude = runClaude
	a.Git.Env = func(dir string) []string { return a.envForDir(context.Background(), dir) }
	return a
}

func runGH(ctx context.Context, dir string, env []string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Dir = dir
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		return "", fmt.Errorf("gh %s: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// LoadProject reads the state file of a project.
func (a *App) LoadProject(name string) (*state.Project, error) {
	p, err := state.Load(a.Cfg.StateFile(name), name)
	if err != nil {
		return nil, err
	}
	if p.Path == "" {
		p.Path = a.Cfg.ProjectDir(name)
	}
	return p, nil
}

// SaveProject writes the state file.
func (a *App) SaveProject(p *state.Project) error { return state.Save(a.Cfg.StateFile(p.Name), p) }

// Dir returns where a session runs: the worktree or the project folder.
func (a *App) Dir(p *state.Project, story string) string {
	if story == "" {
		return p.Path
	}
	return a.Cfg.WorktreeDir(p.Name, story)
}
