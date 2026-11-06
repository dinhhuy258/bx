package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/dinhhuy258/bx/internal/accounts"
	"github.com/dinhhuy258/bx/internal/state"
)

var repoRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// ProjectName derives a project name from a folder path or owner/repo: the last element,
// lowercased, with anything outside [a-z0-9._-] turned into "-".
func ProjectName(s string) string {
	s = strings.ToLower(strings.TrimSuffix(filepath.Base(s), ".git"))
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return strings.TrimLeft(b.String(), "-._")
}

// checkNew validates a new project name before anything is created.
func (a *App) checkNew(name string) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("project name %q: use lowercase letters, digits, . _ -", name)
	}
	if _, err := os.Stat(a.Cfg.StateFile(name)); err == nil {
		return fmt.Errorf("project %q already exists", name)
	}
	return nil
}

func (a *App) register(name, path, kind string) error {
	p := state.New(name, path)
	p.Kind = kind
	return a.SaveProject(p)
}

// AddFolder registers an existing folder on the server where it is: a git repository, or else
// a workspace, a folder of repositories.
func (a *App) AddFolder(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("%q is not an absolute path", path)
	}
	path = filepath.Clean(path)
	if fi, err := os.Stat(path); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("%s is not a folder", path)
	}
	kind := state.KindRepo
	if !a.Git.IsRepo(path) {
		kind = state.KindWorkspace
	}
	name := ProjectName(path)
	if err := a.checkNew(name); err != nil {
		return "", err
	}
	return name, a.register(name, path, kind)
}

// CloneProject clones owner/repo with gh into the projects folder.
func (a *App) CloneProject(repo string) (string, error) {
	if !repoRe.MatchString(repo) {
		return "", fmt.Errorf("%q is not owner/repo", repo)
	}
	name := ProjectName(repo)
	if err := a.checkNew(name); err != nil {
		return "", err
	}
	dest := a.Cfg.ProjectDir(name)
	if _, err := os.Stat(dest); err == nil {
		return "", fmt.Errorf("%s already exists", dest)
	}
	owner, rest, _ := strings.Cut(repo, "/")
	env, err := a.cloneEnv(name, accounts.Origin{Host: accounts.DefaultHost, Owner: owner, Repo: rest})
	if err != nil {
		return "", err
	}
	if _, err := a.GH(withEnv(context.Background(), env), "", "repo", "clone", repo, dest); err != nil {
		return "", err
	}
	return name, a.register(name, dest, state.KindRepo)
}

// NewProject creates an empty repository in the projects folder.
func (a *App) NewProject(name string) (string, error) {
	if err := a.checkNew(name); err != nil {
		return "", err
	}
	dest := a.Cfg.ProjectDir(name)
	if _, err := os.Stat(dest); err == nil {
		return "", fmt.Errorf("%s already exists", dest)
	}
	if err := a.Git.Init(dest); err != nil {
		return "", err
	}
	return name, a.register(name, dest, state.KindRepo)
}

// linkTargets returns the folder of each named repo project. A workspace, a name given twice
// and a project that does not exist are refused.
func (a *App) linkTargets(names []string) (map[string]string, error) {
	out := make(map[string]string, len(names))
	for _, n := range names {
		if _, ok := out[n]; ok {
			return nil, fmt.Errorf("%s is picked twice", n)
		}
		if !nameRe.MatchString(n) {
			return nil, fmt.Errorf("no project %q", n)
		}
		if _, err := os.Stat(a.Cfg.StateFile(n)); err != nil {
			return nil, fmt.Errorf("no project %q", n)
		}
		q, err := a.LoadProject(n)
		if err != nil {
			return nil, err
		}
		if q.IsWorkspace() {
			return nil, fmt.Errorf("%s is a workspace: link repositories only", n)
		}
		out[n] = q.Path
	}
	return out, nil
}

// NewWorkspace makes a workspace in the projects folder: a folder of links, one per repo
// project, named after it.
func (a *App) NewWorkspace(name string, projects []string) (string, error) {
	if err := a.checkNew(name); err != nil {
		return "", err
	}
	if len(projects) < 2 {
		return "", errors.New("pick at least 2 projects")
	}
	targets, err := a.linkTargets(projects)
	if err != nil {
		return "", err
	}
	dir := a.Cfg.ProjectDir(name)
	if _, err := os.Lstat(dir); err == nil {
		return "", fmt.Errorf("%s already exists", dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	for _, n := range projects {
		if err := os.Symlink(targets[n], filepath.Join(dir, n)); err != nil {
			_ = os.RemoveAll(dir) // only links: RemoveAll does not follow them
			return "", err
		}
	}
	if err := a.register(name, dir, state.KindWorkspace); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	return name, nil
}

// cloneEnv is the account environment of a clone of o into the new project name.
func (a *App) cloneEnv(name string, o accounts.Origin) ([]string, error) {
	c, err := a.Accounts()
	if err != nil {
		return nil, err
	}
	acct, ok := c.Resolve(name, o)
	if !ok {
		return nil, nil
	}
	return a.envOf(context.Background(), acct)
}
