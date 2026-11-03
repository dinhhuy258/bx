package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dinhhuy258/bx/internal/state"
)

// BusyError refuses to sync a folder while Claude works in it: Nums are the sessions running
// there. Resend with force to sync anyway.
type BusyError struct {
	Story string
	Nums  []int
}

func (e *BusyError) Error() string {
	return fmt.Sprintf("Claude is working in %s (session [%d])", groupName(e.Story), e.Nums[0])
}

// groupName names a story for people: "chats" for the project folder.
func groupName(story string) string {
	if story == "" {
		return "chats"
	}
	return story
}

// BusySessions lists the sessions of story ("" = chats) for which running reports true: the
// ones Claude is working in, as the server sees them.
func BusySessions(p *state.Project, story string, running func(num int) bool) []int {
	var out []int
	for _, n := range p.SessionsOf(story) {
		if !p.Sessions[n].Done && running(n) {
			out = append(out, n)
		}
	}
	return out
}

// StorySyncPlan is a sync between its fetch (no lock) and its fast-forward (under the write lock).
type StorySyncPlan struct {
	Story   string
	targets []syncTarget
}

// syncTarget is one folder to sync: a story worktree, one repository of a workspace story, or
// the project folder. rel names the repository in a workspace; note, once set, is the outcome
// already known after the fetch (no origin, not on origin, fetch failed).
type syncTarget struct {
	rel, dir, branch, note string
}

// PrepareSync fetches, for story ("" = the project folder), the branch checked out in each of
// its folders from origin. It runs without the write lock; ctx done returns ctx.Err(). A folder
// that cannot be fetched gets its note and is left alone by ApplySync.
func (a *App) PrepareSync(ctx context.Context, p *state.Project, story string) (*StorySyncPlan, error) {
	targets, err := a.syncTargets(p, story)
	if err != nil {
		return nil, err
	}
	for i := range targets {
		t := &targets[i]
		if t.branch == "HEAD" {
			t.note = "has no branch checked out · not synced"
			continue
		}
		if !a.Git.HasRemote(t.dir) {
			t.note = "has no origin · nothing to sync"
			continue
		}
		c, cancel := context.WithTimeout(ctx, FetchTimeout)
		on, err := a.Git.LsRemoteBranch(c, t.dir, t.branch)
		if err == nil && on {
			err = a.Git.Fetch(c, t.dir, t.branch)
		}
		cancel()
		switch {
		case ctx.Err() != nil:
			return nil, ctx.Err()
		case err != nil:
			t.note = "couldn't reach origin · not synced"
		case !on:
			t.note = "isn't on origin yet · nothing to sync"
		}
	}
	return &StorySyncPlan{Story: story, targets: targets}, nil
}

// syncTargets lists the folders of story with the branch checked out in each.
func (a *App) syncTargets(p *state.Project, story string) ([]syncTarget, error) {
	var out []syncTarget
	switch {
	case story == "" && p.IsWorkspace():
		return nil, errors.New("a workspace folder is no repository · sync a story instead")
	case story == "":
		out = []syncTarget{{dir: p.Path}}
	default:
		st, ok := p.Stories[story]
		if !ok {
			return nil, fmt.Errorf("no story %q", story)
		}
		dir := a.Dir(p, story)
		if !p.IsWorkspace() {
			out = []syncTarget{{dir: dir}}
		}
		for _, r := range st.Repos {
			out = append(out, syncTarget{rel: r.Rel, dir: filepath.Join(dir, r.Rel)})
		}
	}
	for i := range out {
		b, err := a.Git.CurrentBranch(out[i].dir)
		if err != nil {
			return nil, err
		}
		out[i].branch = b
	}
	return out, nil
}

// ApplySync fast-forwards each folder of plan to the origin/<branch> just fetched, when that
// is a clean fast-forward: no uncommitted files, no local commits origin lacks. It never resets,
// rebases or forces. It returns one line for people: "guest-vlan-fix synced · 3 new commits
// from origin", in a workspace one part per repository ("api: synced · 3 new commits; web: up
// to date").
func (a *App) ApplySync(p *state.Project, plan *StorySyncPlan) (string, error) {
	if plan.Story != "" {
		if _, ok := p.Stories[plan.Story]; !ok {
			return "", fmt.Errorf("no story %q", plan.Story)
		}
	}
	var parts []string
	for _, t := range plan.targets {
		n := t.note
		if n == "" {
			n = a.fastForwardTo(t.dir, t.branch)
		}
		if t.rel != "" {
			n = t.rel + ": " + n
		}
		parts = append(parts, n)
	}
	if p.IsWorkspace() {
		return plan.Story + " · " + strings.Join(parts, "; "), nil
	}
	name := plan.Story
	if name == "" {
		name = plan.targets[0].branch
	}
	return name + " " + parts[0], nil
}

// fastForwardTo moves the branch checked out in dir up to origin/<b> and says what happened.
func (a *App) fastForwardTo(dir, b string) string {
	ahead, behind, err := a.Git.AheadBehind(dir, "HEAD", "refs/remotes/origin/"+b)
	switch {
	case err != nil:
		return "not synced: " + err.Error()
	case behind == 0 && ahead > 0:
		return fmt.Sprintf("is up to date · %s not pushed", plural(ahead, "commit", "commits"))
	case behind == 0:
		return "is up to date"
	case ahead > 0:
		return fmt.Sprintf("has diverged · %d here, %d on origin · not synced", ahead, behind)
	}
	if n, err := a.Git.DirtyCount(dir); err != nil || n > 0 {
		return fmt.Sprintf("has %s · not synced", plural(n, "uncommitted file", "uncommitted files"))
	}
	if err := a.Git.MergeFFOnly(dir, "refs/remotes/origin/"+b); err != nil {
		return "not synced: " + err.Error()
	}
	return fmt.Sprintf("synced · %s from origin", plural(behind, "new commit", "new commits"))
}

// plural is "1 file" or "3 files".
func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}
