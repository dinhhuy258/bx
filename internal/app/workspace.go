package app

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dinhhuy258/bx/internal/git"
	"github.com/dinhhuy258/bx/internal/state"
)

// repoDepth is how deep under a workspace folder repositories are looked for.
const repoDepth = 2

// WorkspaceRepos lists the repositories of a workspace project, as paths relative to it.
func (a *App) WorkspaceRepos(p *state.Project) ([]string, error) {
	if !p.IsWorkspace() {
		return nil, fmt.Errorf("project %q is not a workspace", p.Name)
	}
	return git.SubRepos(p.Path, repoDepth)
}

// checkRepos refuses an empty pick or a path that is not a repository of the workspace.
func (a *App) checkRepos(p *state.Project, rels []string) error {
	if len(rels) == 0 {
		return errors.New("pick at least one repository")
	}
	all, err := a.WorkspaceRepos(p)
	if err != nil {
		return err
	}
	for _, r := range rels {
		if !slices.Contains(all, r) {
			return fmt.Errorf("%q is not a repository of %s", r, p.Name)
		}
	}
	return nil
}

// added is a worktree made for a story, so a failure can undo it.
type added struct{ repo, dir, branch string }

func (a *App) undo(wts []added) {
	for _, w := range slices.Backward(wts) {
		_ = a.Git.WorktreeRemove(w.repo, w.dir, true)
		_ = a.Git.DeleteBranch(w.repo, w.branch)
	}
}

// newWorkspaceStory builds the story folder: each picked repository is a worktree on branch,
// from its main or master, and everything else a link to the original. Any failure undoes
// everything made so far.
func (a *App) newWorkspaceStory(p *state.Project, name, branch string, rels []string) error {
	if err := a.checkRepos(p, rels); err != nil {
		return err
	}
	mirror := a.Cfg.WorktreeDir(p.Name, name)
	if _, err := os.Lstat(mirror); err == nil {
		return fmt.Errorf("%s already exists", mirror)
	}
	story := &state.Story{Branch: branch}
	var made []added
	fail := func(err error) error {
		a.undo(made)
		_ = os.RemoveAll(mirror) // only links and empty folders are left; RemoveAll does not follow links
		return err
	}
	for _, rel := range rels {
		repo, dir := filepath.Join(p.Path, rel), filepath.Join(mirror, rel)
		base, err := a.Git.DefaultBranch(repo)
		if err != nil {
			return fail(err)
		}
		if err := a.Git.WorktreeAdd(repo, dir, branch, base); err != nil {
			return fail(fmt.Errorf("%s: %w", rel, err))
		}
		made = append(made, added{repo, dir, branch})
		if err := copyEnv(repo, dir); err != nil {
			return fail(err)
		}
		story.Repos = append(story.Repos, state.StoryRepo{Rel: rel, Base: base})
	}
	if err := fill(p.Path, mirror, "", story.RepoNames()); err != nil {
		return fail(err)
	}
	p.Stories[name] = story
	if err := a.SaveProject(p); err != nil {
		delete(p.Stories, name)
		return fail(err)
	}
	return nil
}

// fill links every entry of orig/rel into mirror/rel, except the repos, and makes the folders
// on the way to a repo real ones, replacing a link there. Entries already in the mirror stay.
func fill(orig, mirror, rel string, repos []string) error {
	entries, err := os.ReadDir(filepath.Join(orig, rel))
	if err != nil {
		return err
	}
	for _, e := range entries {
		sub := path.Join(rel, e.Name())
		target := filepath.Join(mirror, sub)
		switch {
		case slices.Contains(repos, sub):
		case slices.ContainsFunc(repos, func(r string) bool { return strings.HasPrefix(r, sub+"/") }):
			if fi, err := os.Lstat(target); err == nil && fi.Mode()&os.ModeSymlink != 0 {
				if err := os.Remove(target); err != nil { // never write into the original through it
					return err
				}
			}
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			if err := fill(orig, mirror, sub, repos); err != nil {
				return err
			}
		default:
			if _, err := os.Lstat(target); err == nil {
				continue
			}
			if err := os.Symlink(filepath.Join(orig, sub), target); err != nil {
				return err
			}
		}
	}
	return nil
}

// copyEnv copies the .env* files at the top of repo into a new worktree of it, which git
// leaves out because they are ignored. A file the worktree already has is kept.
func copyEnv(repo, dir string) error {
	entries, err := os.ReadDir(repo)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), ".env") || !e.Type().IsRegular() {
			continue
		}
		dst := filepath.Join(dir, e.Name())
		if _, err := os.Lstat(dst); err == nil {
			continue
		}
		if err := copyFile(filepath.Join(repo, e.Name()), dst); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	fi, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fi.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// AddStoryRepos makes repositories of the workspace part of a story: the link to each becomes a
// worktree on the story's branch. Without force a repository whose original has uncommitted
// files is refused with *DirtyError, since those changes are not in the story. A repository
// that fails gets its link back; the ones before it stay added.
func (a *App) AddStoryRepos(p *state.Project, name string, rels []string, force bool) error {
	story, ok := p.Stories[name]
	if !ok {
		return fmt.Errorf("no story %q", name)
	}
	if err := a.checkRepos(p, rels); err != nil {
		return err
	}
	for _, r := range rels {
		if slices.Contains(story.RepoNames(), r) {
			return fmt.Errorf("%s is already part of story %q", r, name)
		}
	}
	if !force {
		if d := a.dirty(p.Path, rels); d.Files > 0 {
			d.Story, d.Original = name, true
			return d
		}
	}
	mirror := a.Cfg.WorktreeDir(p.Name, name)
	for _, rel := range rels {
		if err := a.addStoryRepo(p, story, mirror, rel); err != nil {
			return fmt.Errorf("%s: %w", rel, err)
		}
		if err := a.SaveProject(p); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) addStoryRepo(p *state.Project, story *state.Story, mirror, rel string) error {
	if err := fill(p.Path, mirror, "", append(story.RepoNames(), rel)); err != nil {
		return err
	}
	repo, dir := filepath.Join(p.Path, rel), filepath.Join(mirror, rel)
	if fi, err := os.Lstat(dir); err == nil {
		if fi.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf("%s is not a link", dir)
		}
		if err := os.Remove(dir); err != nil {
			return err
		}
	}
	restore := func(err error) error { // the link back, as fill would have made it
		_ = os.RemoveAll(dir)
		_ = os.Symlink(repo, dir)
		return err
	}
	base, err := a.Git.DefaultBranch(repo)
	if err != nil {
		return restore(err)
	}
	if err := a.Git.WorktreeAdd(repo, dir, story.Branch, base); err != nil {
		return restore(err)
	}
	if err := copyEnv(repo, dir); err != nil {
		a.undo([]added{{repo, dir, story.Branch}})
		return restore(err)
	}
	story.Repos = append(story.Repos, state.StoryRepo{Rel: rel, Base: base})
	return nil
}

// dirty counts the uncommitted files of each repository under root.
func (a *App) dirty(root string, rels []string) *DirtyError {
	d := &DirtyError{}
	var parts []string
	for _, rel := range rels {
		if n, err := a.Git.DirtyCount(filepath.Join(root, rel)); err == nil && n > 0 {
			d.Files += n
			parts = append(parts, fmt.Sprintf("%s: %d", rel, n))
		}
	}
	d.Detail = strings.Join(parts, ", ")
	return d
}

// loose counts what was made in a story folder outside its repositories: anything that is not a
// link nor a folder on the way to a repository. Closing the story would delete it.
func loose(mirror, rel string, repos []string) int {
	entries, err := os.ReadDir(filepath.Join(mirror, rel))
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		sub := path.Join(rel, e.Name())
		switch {
		case e.Type()&os.ModeSymlink != 0, slices.Contains(repos, sub):
		case e.IsDir() && slices.ContainsFunc(repos, func(r string) bool { return strings.HasPrefix(r, sub+"/") }):
			n += loose(mirror, sub, repos)
		default:
			n++
		}
	}
	return n
}

// closeWorkspaceStory removes a workspace story's worktrees, then its folder, which by then
// holds only links (removed, not followed), folders and, with force, loose files.
func (a *App) closeWorkspaceStory(p *state.Project, name string, force bool) error {
	story := p.Stories[name]
	mirror := a.Cfg.WorktreeDir(p.Name, name)
	if !force {
		d := a.dirty(mirror, story.RepoNames())
		if n := loose(mirror, "", story.RepoNames()); n > 0 {
			d.Files += n
			d.Detail = strings.TrimPrefix(d.Detail+fmt.Sprintf(", outside the repos: %d", n), ", ")
		}
		if d.Files > 0 {
			d.Story = name
			return d
		}
	}
	if err := a.stopStory(p, name); err != nil {
		return err
	}
	for _, r := range story.Repos {
		repo, dir := filepath.Join(p.Path, r.Rel), filepath.Join(mirror, r.Rel)
		if _, err := os.Lstat(dir); err == nil {
			if err := a.Git.WorktreeRemove(repo, dir, force); err != nil {
				return fmt.Errorf("%s: %w", r.Rel, err)
			}
		} else {
			_ = a.Git.WorktreePrune(repo)
		}
	}
	if err := os.RemoveAll(mirror); err != nil {
		return err
	}
	return a.forgetStory(p, name)
}
