// Package app is the orchestration layer: it combines tmux, git, state and the claude
// CLI into the operations bx serve exposes (projects, stories, sessions).
package app

import (
	"fmt"
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
	Cfg      *config.Config
	Tmux     *tmux.Tmux
	Git      *git.Git
	UUID     func() string
	GH       func(args ...string) (string, error)
	Settings *settings.Store
}

// New returns an App on bx's private tmux server, with the real git and gh.
func New(cfg *config.Config) *App {
	r := &tmux.Runner{Socket: cfg.TmuxSocket(), Config: cfg.TmuxConf()}
	return &App{Cfg: cfg, Tmux: tmux.New(r), Git: git.New(), UUID: uuid.NewString, GH: runGH}
}

func runGH(args ...string) (string, error) {
	out, err := exec.Command("gh", args...).CombinedOutput()
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

// NewStory creates branch feat/<name> and the worktree, then records the story.
func (a *App) NewStory(p *state.Project, name string) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("story name %q: use lowercase letters, digits, . _ -", name)
	}
	if _, exists := p.Stories[name]; exists {
		return fmt.Errorf("story %q already exists", name)
	}
	base, err := a.Git.DefaultBranch(p.Path)
	if err != nil {
		return err
	}
	branch := "feat/" + name
	if err := a.Git.WorktreeAdd(p.Path, a.Cfg.WorktreeDir(p.Name, name), branch, base); err != nil {
		return err
	}
	p.Stories[name] = &state.Story{Branch: branch}
	return a.SaveProject(p)
}

// Dir returns where a session runs: the worktree or the project folder.
func (a *App) Dir(p *state.Project, story string) string {
	if story == "" {
		return p.Path
	}
	return a.Cfg.WorktreeDir(p.Name, story)
}
