// Package git wraps the git commands bx needs: worktrees, diff numbers, dirty count.
package git

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Git runs git. Bin defaults to "git". Env, when set, gives the commands that talk to origin
// (runCtx) extra environment for the repository in dir: the GitHub account of its project.
type Git struct {
	Bin string
	Env func(dir string) []string
}

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

// WorktreeAdd creates path on a new branch from base, with no upstream even when base is a
// remote branch (origin/develop). Fails if the branch already exists or is checked out
// elsewhere (git enforces one checkout per branch).
func (g *Git) WorktreeAdd(repo, path, branch, base string) error {
	return g.worktreeAdd(repo, path, "--no-track", "-b", branch, path, base)
}

// WorktreeAddTrack creates path on a new branch from upstream (origin/<branch>), tracking it.
func (g *Git) WorktreeAddTrack(repo, path, branch, upstream string) error {
	return g.worktreeAdd(repo, path, "--track", "-b", branch, path, upstream)
}

// WorktreeAddExisting creates path on a local branch that already exists.
func (g *Git) WorktreeAddExisting(repo, path, branch string) error {
	return g.worktreeAdd(repo, path, path, branch)
}

func (g *Git) worktreeAdd(repo, path string, args ...string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	_, err := g.run(repo, append([]string{"worktree", "add"}, args...)...)
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

// DeleteBranch deletes a branch even when it is not merged.
func (g *Git) DeleteBranch(repo, branch string) error {
	_, err := g.run(repo, "branch", "-D", branch)
	return err
}

// CurrentBranch returns the branch checked out in dir.
func (g *Git) CurrentBranch(dir string) (string, error) {
	return g.run(dir, "rev-parse", "--abbrev-ref", "HEAD")
}

// RevParse is the commit ref names in dir.
func (g *Git) RevParse(dir, ref string) (string, error) {
	return g.run(dir, "rev-parse", ref)
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

// Init creates a repo with branch main and an empty first commit, so main exists for stories
// to branch from. Without a configured identity the commit is made as bx.
func (g *Git) Init(dest string) error {
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	if _, err := g.run(dest, "init", "-q", "-b", "main"); err != nil {
		return err
	}
	args := []string{"commit", "-q", "--allow-empty", "-m", "init"}
	if email, _ := g.run(dest, "config", "user.email"); email == "" {
		args = append([]string{"-c", "user.name=bx", "-c", "user.email=bx@localhost"}, args...)
	}
	_, err := g.run(dest, args...)
	return err
}

// SubRepos returns the folders under dir, up to depth levels down, that hold a .git entry,
// as slash paths relative to dir, sorted. It does not look inside a repo it found, nor in
// hidden folders or node_modules. Links to folders are followed; a broken link, a link to a
// file, and a folder already seen by its real path (a loop, or a second link to one repo)
// are skipped.
func SubRepos(dir string, depth int) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		seen[real] = true
	}
	var walk func(rel string, level int) error
	walk = func(rel string, level int) error {
		entries, err := os.ReadDir(filepath.Join(dir, rel))
		if err != nil {
			return err
		}
		for _, e := range entries {
			name := e.Name()
			if strings.HasPrefix(name, ".") || name == "node_modules" {
				continue
			}
			sub := filepath.Join(rel, name)
			full := filepath.Join(dir, sub)
			if !e.IsDir() {
				if e.Type()&os.ModeSymlink == 0 {
					continue
				}
				if fi, err := os.Stat(full); err != nil || !fi.IsDir() {
					continue
				}
			}
			real, err := filepath.EvalSymlinks(full)
			if err != nil || seen[real] {
				continue
			}
			seen[real] = true
			if _, err := os.Lstat(filepath.Join(full, ".git")); err == nil {
				out = append(out, filepath.ToSlash(sub))
				continue
			}
			if level < depth {
				if err := walk(sub, level+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk("", 1); err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

// IsRepo reports whether dir is inside a git work tree.
func (g *Git) IsRepo(dir string) bool {
	out, err := g.run(dir, "rev-parse", "--is-inside-work-tree")
	return err == nil && out == "true"
}
