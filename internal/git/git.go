// Package git wraps the git commands bx needs: worktrees, diff numbers, dirty count.
package git

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Git runs git. Bin defaults to "git".
type Git struct{ Bin string }

// New returns a Git using PATH.
func New() *Git { return &Git{Bin: "git"} }

func (g *Git) run(dir string, args ...string) (string, error) {
	cmd := exec.Command(g.Bin, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
	}
	return strings.TrimRight(string(out), "\n"), nil
}

// WorktreeAdd creates path on a new branch from base. Fails if the branch already exists
// or is checked out elsewhere (git enforces one checkout per branch).
func (g *Git) WorktreeAdd(repo, path, branch, base string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	_, err := g.run(repo, "worktree", "add", path, "-b", branch, base)
	return err
}

// WorktreeRemove removes a worktree. force discards uncommitted changes.
func (g *Git) WorktreeRemove(repo, path string, force bool) error {
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	_, err := g.run(repo, append(args, path)...)
	return err
}

// WorktreePrune drops worktree records whose folder is gone.
func (g *Git) WorktreePrune(repo string) error {
	_, err := g.run(repo, "worktree", "prune")
	return err
}

// CurrentBranch returns the branch checked out in dir.
func (g *Git) CurrentBranch(dir string) (string, error) {
	return g.run(dir, "rev-parse", "--abbrev-ref", "HEAD")
}

// DefaultBranch returns main or master, whichever exists, preferring main.
func (g *Git) DefaultBranch(repo string) (string, error) {
	for _, b := range []string{"main", "master"} {
		if _, err := g.run(repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+b); err == nil {
			return b, nil
		}
	}
	return "", fmt.Errorf("no main or master branch in %s", repo)
}

var statRe = regexp.MustCompile(`(\d+) insertion|(\d+) deletion`)

// ShortStat returns added and deleted lines of dir's branch since it left base (base...HEAD).
func (g *Git) ShortStat(dir, base string) (add, del int, err error) {
	out, err := g.run(dir, "diff", "--shortstat", base+"...HEAD")
	if err != nil {
		return 0, 0, err
	}
	for _, m := range statRe.FindAllStringSubmatch(out, -1) {
		if m[1] != "" {
			add, _ = strconv.Atoi(m[1])
		}
		if m[2] != "" {
			del, _ = strconv.Atoi(m[2])
		}
	}
	return add, del, nil
}

// DirtyCount returns the number of changed or untracked files in dir.
func (g *Git) DirtyCount(dir string) (int, error) {
	out, err := g.run(dir, "status", "--porcelain")
	if err != nil {
		return 0, err
	}
	if out == "" {
		return 0, nil
	}
	return len(strings.Split(out, "\n")), nil
}

// Clone clones url into dest.
func (g *Git) Clone(url, dest string) error {
	_, err := g.run(filepath.Dir(dest), "clone", url, dest)
	return err
}

// Init creates a repo with branch main.
func (g *Git) Init(dest string) error {
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	_, err := g.run(dest, "init", "-q", "-b", "main")
	return err
}

// IsRepo reports whether dir is inside a git work tree.
func (g *Git) IsRepo(dir string) bool {
	out, err := g.run(dir, "rev-parse", "--is-inside-work-tree")
	return err == nil && out == "true"
}
