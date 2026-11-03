package git

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

// runCtx runs git like run, but stops it when ctx is done and never lets it ask for a
// password: for the commands that talk to origin.
func (g *Git) runCtx(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, g.Bin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if g.Env != nil {
		cmd.Env = append(cmd.Env, g.Env(dir)...)
	}
	cmd.WaitDelay = time.Second // a killed git's helpers (ssh, git-remote-https) may hold the pipe
	out, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), ctx.Err())
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
	}
	return strings.TrimRight(string(out), "\n"), nil
}

// HasRemote reports whether repo has a remote named origin.
func (g *Git) HasRemote(repo string) bool {
	_, err := g.run(repo, "remote", "get-url", "origin")
	return err == nil
}

// OriginHead is origin's default branch as last recorded (refs/remotes/origin/HEAD), without
// the "origin/": "develop".
func (g *Git) OriginHead(repo string) (string, error) {
	out, err := g.run(repo, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimPrefix(out, "origin/"), nil
}

// SetHeadAuto asks origin for its default branch and records it as refs/remotes/origin/HEAD.
func (g *Git) SetHeadAuto(ctx context.Context, repo string) error {
	_, err := g.runCtx(ctx, repo, "remote", "set-head", "origin", "--auto")
	return err
}

// LocalDefault is the first local branch of main, master, develop and trunk that exists, for
// repos with no remote default.
func (g *Git) LocalDefault(repo string) (string, error) {
	for _, b := range []string{"main", "master", "develop", "trunk"} {
		if g.RefExists(repo, "refs/heads/"+b) {
			return b, nil
		}
	}
	return "", fmt.Errorf("no main, master, develop or trunk branch in %s", repo)
}

// RefExists reports whether ref (a full name: refs/heads/x, refs/remotes/origin/x) exists.
func (g *Git) RefExists(repo, ref string) bool {
	_, err := g.run(repo, "rev-parse", "--verify", "--quiet", ref)
	return err == nil
}

// Fetch runs `git fetch origin <refspecs>`. A branch name as refspec updates only
// origin/<branch>.
func (g *Git) Fetch(ctx context.Context, repo string, refspecs ...string) error {
	_, err := g.runCtx(ctx, repo, append([]string{"fetch", "origin"}, refspecs...)...)
	return err
}

// FetchPrune fetches every branch of origin and drops the remote branches origin no longer has.
func (g *Git) FetchPrune(ctx context.Context, repo string) error {
	_, err := g.runCtx(ctx, repo, "fetch", "origin", "--prune")
	return err
}

// LsRemoteBranch asks origin whether it has branch b.
func (g *Git) LsRemoteBranch(ctx context.Context, repo, b string) (bool, error) {
	out, err := g.runCtx(ctx, repo, "ls-remote", "origin", "refs/heads/"+b)
	if err != nil {
		return false, err
	}
	for _, l := range strings.Split(out, "\n") {
		if _, ref, ok := strings.Cut(l, "\t"); ok && ref == "refs/heads/"+b {
			return true, nil
		}
	}
	return false, nil
}

// RefTime is when ref last changed here, from its reflog (for origin/<b>: the last fetch that
// moved it), else its commit time.
func (g *Git) RefTime(repo, ref string) (time.Time, error) {
	if out, err := g.run(repo, "log", "-g", "-1", "--date=unix", "--format=%gd", ref); err == nil {
		if i, j := strings.LastIndex(out, "@{"), strings.LastIndex(out, "}"); i >= 0 && j > i {
			if n, err := strconv.ParseInt(out[i+2:j], 10, 64); err == nil {
				return time.Unix(n, 0), nil
			}
		}
	}
	out, err := g.run(repo, "log", "-1", "--format=%ct", ref)
	if err != nil {
		return time.Time{}, err
	}
	n, err := strconv.ParseInt(out, 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("commit time of %s: %q", ref, out)
	}
	return time.Unix(n, 0), nil
}

// AheadBehind counts the commits of a not in b (ahead) and of b not in a (behind).
func (g *Git) AheadBehind(repo, a, b string) (ahead, behind int, err error) {
	out, err := g.run(repo, "rev-list", "--left-right", "--count", a+"..."+b)
	if err != nil {
		return 0, 0, err
	}
	f := strings.Fields(out)
	if len(f) != 2 {
		return 0, 0, fmt.Errorf("rev-list --count: %q", out)
	}
	ahead, err1 := strconv.Atoi(f[0])
	behind, err2 := strconv.Atoi(f[1])
	if err := errors.Join(err1, err2); err != nil {
		return 0, 0, err
	}
	return ahead, behind, nil
}

// OnOrigin reports whether the commit ref points at is on a branch of origin (as last fetched).
func (g *Git) OnOrigin(repo, ref string) bool {
	out, err := g.run(repo, "for-each-ref", "--count=1", "--contains", ref, "--format=%(refname)", "refs/remotes/origin")
	return err == nil && out != ""
}

// DeleteRef deletes ref (a full name).
func (g *Git) DeleteRef(repo, ref string) error {
	_, err := g.run(repo, "update-ref", "-d", ref)
	return err
}

// ErrRemoteMoved refuses to delete a branch of origin that moved since it was last fetched:
// someone pushed to it, and deleting would lose that.
var ErrRemoteMoved = errors.New("it moved on origin since the last fetch")

// DeleteRemoteBranch deletes branch b on origin, and reports whether it did. Only a branch known
// here as origin/<b> is deleted, and only where origin/<b> points (a lease): a moved branch is
// refused with ErrRemoteMoved. A branch origin no longer has is no error; its stale origin/<b>
// is dropped.
func (g *Git) DeleteRemoteBranch(ctx context.Context, repo, b string) (bool, error) {
	tracking := "refs/remotes/origin/" + b
	sha, err := g.run(repo, "rev-parse", "--verify", "--quiet", tracking)
	if err != nil {
		return false, nil
	}
	_, err = g.runCtx(ctx, repo, "push", "--force-with-lease=refs/heads/"+b+":"+sha, "origin", ":refs/heads/"+b)
	if err == nil {
		return true, nil
	}
	if ctx.Err() != nil || !strings.Contains(err.Error(), "stale info") {
		return false, err
	}
	// stale info: origin has no b any more, or b moved there; push says the same for both
	if there, lsErr := g.LsRemoteBranch(ctx, repo, b); lsErr != nil {
		return false, lsErr
	} else if there {
		return false, ErrRemoteMoved
	}
	_ = g.DeleteRef(repo, tracking)
	return false, nil
}

// IsAncestor reports whether commit a is an ancestor of b (or b itself).
func (g *Git) IsAncestor(repo, a, b string) bool {
	_, err := g.run(repo, "merge-base", "--is-ancestor", a, b)
	return err == nil
}

// WorktreeOf finds the worktree, the main one included, that has branch checked out.
func (g *Git) WorktreeOf(repo, branch string) (dir string, ok bool, err error) {
	m, err := g.CheckedOut(repo)
	if err != nil {
		return "", false, err
	}
	dir, ok = m[branch]
	return dir, ok, nil
}

// CheckedOut maps each branch checked out in a worktree of repo, the main one included, to that
// worktree's folder.
func (g *Git) CheckedOut(repo string) (map[string]string, error) {
	out, err := g.run(repo, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	var cur string
	for _, l := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(l, "worktree "):
			cur = strings.TrimPrefix(l, "worktree ")
		case strings.HasPrefix(l, "branch refs/heads/"):
			m[strings.TrimPrefix(l, "branch refs/heads/")] = cur
		}
	}
	return m, nil
}

// FastForward moves local branch b to origin/<b>, which must be ahead of it. b must not be
// checked out: git refuses a branch that is, and any move that is not a fast-forward.
func (g *Git) FastForward(repo, b string) error {
	_, err := g.run(repo, "fetch", ".", "refs/remotes/origin/"+b+":refs/heads/"+b)
	return err
}

// MergeFFOnly fast-forwards the branch checked out in dir to ref, or fails.
func (g *Git) MergeFFOnly(dir, ref string) error {
	_, err := g.run(dir, "merge", "--ff-only", "--quiet", ref)
	return err
}

// Branch is a branch of a repo, local, on origin or both. Time is its last commit: the local
// one when it is local.
type Branch struct {
	Name   string
	Local  bool
	Remote bool
	Time   time.Time
}

// Branches lists the local branches and those of origin, merged by name, newest first.
func (g *Git) Branches(repo string) ([]Branch, error) {
	out, err := g.run(repo, "for-each-ref", "--sort=-committerdate", "--format=%(refname)%09%(committerdate:unix)", "refs/heads", "refs/remotes/origin")
	if err != nil {
		return nil, err
	}
	byName := map[string]*Branch{}
	var order []*Branch
	for _, l := range strings.Split(out, "\n") {
		ref, ts, ok := strings.Cut(l, "\t")
		if !ok {
			continue
		}
		n, _ := strconv.ParseInt(ts, 10, 64)
		local := strings.HasPrefix(ref, "refs/heads/")
		name := strings.TrimPrefix(strings.TrimPrefix(ref, "refs/heads/"), "refs/remotes/origin/")
		if !local && name == "HEAD" {
			continue
		}
		b := byName[name]
		if b == nil {
			b = &Branch{Name: name}
			byName[name] = b
			order = append(order, b)
		}
		if local {
			b.Local, b.Time = true, time.Unix(n, 0)
		} else {
			b.Remote = true
			if !b.Local {
				b.Time = time.Unix(n, 0)
			}
		}
	}
	out2 := make([]Branch, len(order))
	for i, b := range order {
		out2[i] = *b
	}
	sort.SliceStable(out2, func(i, j int) bool { return out2[i].Time.After(out2[j].Time) })
	return out2, nil
}

// OriginSlug is owner/repo from origin's URL (git@host:owner/repo.git, https://host/owner/repo,
// ssh://git@host/owner/repo.git), or "" when there is no origin or it is a local path.
func (g *Git) OriginSlug(repo string) string {
	u, err := g.run(repo, "remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	return slugOf(u)
}

// OriginURL is origin's URL, "" when there is none.
func (g *Git) OriginURL(repo string) string {
	u, _ := g.run(repo, "remote", "get-url", "origin")
	return u
}

func slugOf(raw string) string {
	var p string
	if pu, err := url.Parse(raw); err == nil && pu.Host != "" && pu.Scheme != "file" {
		p = pu.Path
	} else if at := strings.Index(raw, "@"); at >= 0 && strings.Contains(raw[at:], ":") && !strings.Contains(raw, "://") {
		p = raw[strings.Index(raw[at:], ":")+at+1:] // scp-like: user@host:owner/repo.git
	} else {
		return ""
	}
	parts := strings.Split(strings.Trim(strings.TrimSuffix(strings.Trim(p, "/"), ".git"), "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return ""
	}
	return parts[0] + "/" + parts[1]
}

// ValidBranch checks that name can be a branch (git check-ref-format --branch).
func (g *Git) ValidBranch(name string) error {
	if name == "" || strings.HasPrefix(name, "-") {
		return fmt.Errorf("%q is not a valid branch name", name)
	}
	if _, err := g.run(os.TempDir(), "check-ref-format", "--branch", name); err != nil {
		return fmt.Errorf("%q is not a valid branch name", name)
	}
	return nil
}
