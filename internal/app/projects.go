package app

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

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
	if _, err := a.GH("repo", "clone", repo, dest); err != nil {
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

// GitHubRepos lists up to 100 repositories of the gh user, as owner/repo.
func (a *App) GitHubRepos() ([]string, error) {
	out, err := a.GH("repo", "list", "--limit", "100", "--json", "nameWithOwner", "--jq", ".[].nameWithOwner")
	if err != nil {
		return nil, err
	}
	var repos []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			repos = append(repos, l)
		}
	}
	return repos, nil
}
