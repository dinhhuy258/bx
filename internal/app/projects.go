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

func (a *App) register(name, path string) error { return a.SaveProject(state.New(name, path)) }

// AddFolder registers an existing git repository on the server where it is.
func (a *App) AddFolder(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("%q is not an absolute path", path)
	}
	path = filepath.Clean(path)
	if !a.Git.IsRepo(path) {
		return "", fmt.Errorf("%s is not a git repository", path)
	}
	name := ProjectName(path)
	if err := a.checkNew(name); err != nil {
		return "", err
	}
	return name, a.register(name, path)
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
	return name, a.register(name, dest)
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
	return name, a.register(name, dest)
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
