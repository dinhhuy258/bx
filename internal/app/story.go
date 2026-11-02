package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dinhhuy258/bx/internal/naming"
	"github.com/dinhhuy258/bx/internal/state"
)

// FetchTimeout bounds each talk to origin while a story is prepared: a fetch, an ls-remote,
// a set-head. It is fixed on purpose.
const FetchTimeout = 5 * time.Minute

// Story sources: a new branch (the default), an existing branch, a pull request.
const (
	SourceNew    = "new"
	SourceBranch = "branch"
	SourcePR     = "pr"
)

// StoryOpts is a request for a new story.
type StoryOpts struct {
	// Name is what the user typed for a new branch: a name ("login timeout") made into
	// <type>/<slug>, or a full branch when its naming.CleanBranch has a "/" ("fix/login-timeout").
	Name string
	// Type is the branch type of a new branch (one of naming.Types); "" is the project's last
	// type, else feat.
	Type string
	// Source is SourceNew ("" too), SourceBranch or SourcePR.
	Source string
	// Branch is the existing branch of SourceBranch.
	Branch string
	// PR is the pull request number of SourcePR, PRRepo its repository in a workspace
	// (relative to it), added to Repos if missing.
	PR     int
	PRRepo string
	// Repos are the repositories of a workspace story, relative to it.
	Repos []string
	// UseExisting makes a new branch that already exists use it instead of failing with
	// *BranchExistsError.
	UseExisting bool
}

// How a repository of a plan gets its worktree.
const (
	modeNew   = "new"   // a new branch from start, with no upstream
	modeLocal = "local" // the local branch, as it is
	modeTrack = "track" // a new local branch tracking start (origin/<branch>)
)

// repoPlan is what CreateStory does in one repository.
type repoPlan struct {
	rel, repo string // rel is "" in a single-repo project; repo is the repository's folder
	mode      string
	start     string // new: the base it starts from; track: origin/<branch>
	base      string // recorded as the story's base, for the diff
}

// StoryPlan is what PrepareStory found out, everything networked done; CreateStory makes it.
type StoryPlan struct {
	Story  string // the story name: list label, worktree folder and state key
	Branch string
	repos  []repoPlan
	notes  []string
	typ    string // the type to remember as the project's last one, "" for none
	fork   string // the repository where PrepareStory made Branch (a fork PR), "" for none
}

// RepoBase is what a repository of a new story started from. Rel is "" in a single-repo
// project; Base is "" for a repository on an existing branch.
type RepoBase struct{ Rel, Base string }

// StoryResult is a story CreateStory made.
type StoryResult struct {
	Story  string
	Branch string
	Bases  []RepoBase
	Notes  []string
}

// Message is the success line: "Created login-timeout on fix/login-timeout ← origin/develop",
// each repository's base when they differ, then the notes.
func (r *StoryResult) Message() string {
	msg := fmt.Sprintf("Created %s on %s", r.Story, r.Branch)
	var shown []RepoBase
	for _, b := range r.Bases {
		if b.Base != "" {
			shown = append(shown, b)
		}
	}
	same := len(shown) == len(r.Bases)
	for _, b := range shown {
		same = same && b.Base == shown[0].Base
	}
	switch {
	case len(shown) == 0:
	case same:
		msg += " ← " + shown[0].Base
	default:
		parts := make([]string, len(shown))
		for i, b := range shown {
			parts[i] = b.Rel + ": " + b.Base
		}
		msg += " ← " + strings.Join(parts, ", ")
	}
	return strings.Join(append([]string{msg}, r.Notes...), " · ")
}

// BranchExistsError refuses a new branch that already exists. Where is "local", "origin" or
// "both"; Repos are the workspace repositories that have it. Resend with UseExisting to use it.
type BranchExistsError struct {
	Branch string
	Where  string
	Repos  []string
}

func (e *BranchExistsError) Error() string {
	where := map[string]string{"local": "exists locally", "origin": "exists on origin", "both": "exists locally and on origin"}[e.Where]
	msg := "branch " + e.Branch + " " + where
	if len(e.Repos) > 0 {
		msg += " in " + strings.Join(e.Repos, ", ")
	}
	return msg
}

// target is a repository a story is made in.
type target struct {
	rel, repo, name string // name is how messages call it: the project, or rel in a workspace
	local, remote   bool   // the branch exists there (new source)
}

// NewStory prepares and creates a story at once.
func (a *App) NewStory(ctx context.Context, p *state.Project, o StoryOpts) (*StoryResult, error) {
	plan, err := a.PrepareStory(ctx, p, o)
	if err != nil {
		return nil, err
	}
	return a.CreateStory(p, plan)
}

// PrepareStory does the slow part of a new story, without touching bx's state: it finds each
// repository's base and fetches it, fast-forwards the local base when it is safe, checks the
// branch, looks the pull request up. It may make one branch, the local branch of a fork PR. If
// ctx is done it returns ctx.Err(); a fetch that fails on its own falls back to what was
// fetched before, with a note.
func (a *App) PrepareStory(ctx context.Context, p *state.Project, o StoryOpts) (*StoryPlan, error) {
	targets, err := a.storyTargets(p, &o)
	if err != nil {
		return nil, err
	}
	plan := &StoryPlan{}
	switch o.Source {
	case "", SourceNew:
		if err = a.nameNew(p, &o, plan); err == nil {
			err = a.prepareNew(ctx, p, o, plan, targets)
		}
	case SourceBranch:
		plan.Branch, plan.Story = o.Branch, naming.StoryName(o.Branch)
		if err = a.checkStory(p, plan, o.Branch); err == nil {
			err = a.prepareExisting(ctx, p, plan, targets, -1)
		}
	case SourcePR:
		err = a.preparePR(ctx, p, o, plan, targets)
	default:
		err = fmt.Errorf("unknown story source %q", o.Source)
	}
	if err != nil {
		a.dropMade(plan) // a fork branch fetched before the failure
		return nil, err
	}
	return plan, nil
}

// storyTargets are the repositories of the story: the project, or the picked ones of a
// workspace (with the PR's repository added).
func (a *App) storyTargets(p *state.Project, o *StoryOpts) ([]*target, error) {
	if !p.IsWorkspace() {
		return []*target{{repo: p.Path, name: p.Name}}, nil
	}
	if o.Source == SourcePR && o.PRRepo != "" && !slices.Contains(o.Repos, o.PRRepo) {
		o.Repos = append(slices.Clone(o.Repos), o.PRRepo)
	}
	if err := a.checkRepos(p, o.Repos); err != nil {
		return nil, err
	}
	var ts []*target
	for _, rel := range o.Repos {
		ts = append(ts, &target{rel: rel, repo: filepath.Join(p.Path, rel), name: rel})
	}
	return ts, nil
}

// nameNew turns the typed name into the story and its branch.
func (a *App) nameNew(p *state.Project, o *StoryOpts, plan *StoryPlan) error {
	name := strings.TrimSpace(o.Name)
	// decided on the cleaned branch, like the clients: "foo/" is the plain name foo
	if b := naming.CleanBranch(name); strings.Contains(b, "/") {
		plan.Branch = b
		plan.Story = naming.StoryName(plan.Branch)
	} else {
		if o.Type != "" && !naming.ValidType(o.Type) {
			return fmt.Errorf("unknown branch type %q", o.Type)
		}
		typ := o.Type
		if typ == "" {
			typ = p.LastType
		}
		plan.Story = naming.Slug(name)
		plan.Branch = naming.Branch(naming.DefaultType(typ), plan.Story)
		plan.typ = o.Type
	}
	return a.checkStory(p, plan, name)
}

// checkStory validates the story name and branch and refuses a story that exists.
func (a *App) checkStory(p *state.Project, plan *StoryPlan, typed string) error {
	if !nameRe.MatchString(plan.Story) {
		return fmt.Errorf("story name %q: use lowercase letters, digits, . _ -", typed)
	}
	if err := a.Git.ValidBranch(plan.Branch); err != nil {
		return err
	}
	if s, ok := p.Stories[plan.Story]; ok {
		return fmt.Errorf("story %q already exists (branch %s)", plan.Story, s.Branch)
	}
	return nil
}

// prepareNew plans a new branch in every target: first whether it exists anywhere (refused
// without UseExisting), then per repository the existing branch or the base.
func (a *App) prepareNew(ctx context.Context, p *state.Project, o StoryOpts, plan *StoryPlan, targets []*target) error {
	b := plan.Branch
	var anyLocal, anyRemote bool
	var hit []string
	for _, t := range targets {
		var err error
		if t.local, t.remote, err = a.branchExists(ctx, t.repo, b); err != nil {
			return err
		}
		anyLocal, anyRemote = anyLocal || t.local, anyRemote || t.remote
		if t.local || t.remote {
			hit = append(hit, t.rel)
		}
	}
	if (anyLocal || anyRemote) && !o.UseExisting {
		e := &BranchExistsError{Branch: b, Where: "local"}
		switch {
		case anyLocal && anyRemote:
			e.Where = "both"
		case anyRemote:
			e.Where = "origin"
		}
		if p.IsWorkspace() {
			e.Repos = hit
		}
		return e
	}
	ws := p.IsWorkspace()
	for _, t := range targets {
		var rp repoPlan
		var err error
		switch {
		case t.local:
			rp = repoPlan{mode: modeLocal}
			if t.remote {
				if err := a.fetch(ctx, t.repo, b); ctx.Err() != nil {
					return err
				}
				plan.note(ws, t, a.behind(t.repo, b))
			}
		case t.remote:
			// a failed fetch is fine when an older origin/<b> is there to track
			if err := a.fetch(ctx, t.repo, b); err != nil && (ctx.Err() != nil || !a.Git.RefExists(t.repo, "refs/remotes/origin/"+b)) {
				return err
			}
			rp = repoPlan{mode: modeTrack, start: "origin/" + b}
		default:
			if rp, err = a.planBase(ctx, p, plan, t, p.BaseOverride); err != nil {
				return err
			}
		}
		if rp.mode != modeNew {
			if rp.base, err = a.recordedBase(ctx, t, p.BaseOverride, ws); err != nil {
				return err
			}
		}
		rp.rel, rp.repo = t.rel, t.repo
		plan.repos = append(plan.repos, rp)
	}
	return nil
}

// prepareExisting plans a story on plan.Branch, which should exist: each target uses its local
// branch, else a tracking one of origin's; a workspace repository that has neither makes it
// from its base. skip is the target already planned (the PR's), or -1.
func (a *App) prepareExisting(ctx context.Context, p *state.Project, plan *StoryPlan, targets []*target, skip int) error {
	b, ws := plan.Branch, p.IsWorkspace()
	found := skip >= 0
	plans := make([]repoPlan, len(targets))
	for i, t := range targets {
		if i == skip {
			continue
		}
		local := a.Git.RefExists(t.repo, "refs/heads/"+b)
		if a.Git.HasRemote(t.repo) {
			if err := a.fetch(ctx, t.repo, b); ctx.Err() != nil {
				return err
			}
		}
		remote := a.Git.RefExists(t.repo, "refs/remotes/origin/"+b)
		rp := repoPlan{mode: modeLocal}
		switch {
		case local:
			if remote {
				plan.note(ws, t, a.behind(t.repo, b))
			}
		case remote:
			rp = repoPlan{mode: modeTrack, start: "origin/" + b}
		default:
			rp.mode = "" // decided below, once it is known whether any repository has it
		}
		found = found || rp.mode != ""
		plans[i] = rp
	}
	if !found {
		return fmt.Errorf("no branch %s in %s", b, p.Name)
	}
	for i, t := range targets {
		if i == skip {
			plan.repos = append(plan.repos, repoPlan{}) // filled by the caller
			continue
		}
		rp := plans[i]
		var err error
		if rp.mode == "" {
			if rp, err = a.planBase(ctx, p, plan, t, ""); err != nil {
				return err
			}
		} else if rp.base, err = a.recordedBase(ctx, t, "", ws); err != nil {
			return err
		}
		rp.rel, rp.repo = t.rel, t.repo
		plan.repos = append(plan.repos, rp)
	}
	return nil
}

// ghPR is what `gh pr view` and `gh pr list` say about a pull request.
type ghPR struct {
	Number              int                    `json:"number"`
	Title               string                 `json:"title"`
	HeadRefName         string                 `json:"headRefName"`
	IsCrossRepository   bool                   `json:"isCrossRepository"`
	State               string                 `json:"state"`
	Author              struct{ Login string } `json:"author"`
	HeadRepositoryOwner struct{ Login string } `json:"headRepositoryOwner"`
}

// prFields are the --json fields of every gh pr call.
const prFields = "number,title,author,headRefName,isCrossRepository,headRepositoryOwner,state"

// preparePR plans a story on a pull request's branch. A same-repo PR uses or tracks origin's
// branch. A fork PR is fetched from refs/pull/<n>/head into a local branch named after its head
// (owner/head when the head is a branch of origin too, like a fork's main); it is never pushed.
// The other workspace repositories use the branch if they have it, else make it from their base.
func (a *App) preparePR(ctx context.Context, p *state.Project, o StoryOpts, plan *StoryPlan, targets []*target) error {
	idx := 0
	if p.IsWorkspace() {
		idx = slices.IndexFunc(targets, func(t *target) bool { return t.rel == o.PRRepo })
		if idx < 0 {
			return errors.New("pick the repository of the pull request")
		}
	}
	t := targets[idx]
	args := []string{"pr", "view", strconv.Itoa(o.PR), "--json", prFields}
	out, err := a.GH(ctx, t.repo, args...)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	var pr ghPR
	if err := json.Unmarshal([]byte(out), &pr); err != nil {
		return fmt.Errorf("gh pr view: %w", err)
	}
	head := pr.HeadRefName
	if err := a.Git.ValidBranch(head); err != nil {
		return fmt.Errorf("PR #%d: %w", o.PR, err)
	}
	plan.Story, plan.Branch = naming.StoryName(head), head
	if !pr.IsCrossRepository {
		if err := a.checkStory(p, plan, head); err != nil {
			return err
		}
		return a.prepareExisting(ctx, p, plan, targets, -1)
	}
	owner := pr.HeadRepositoryOwner.Login
	if owner == "" {
		owner = fmt.Sprintf("pr-%d", o.PR)
	}
	if a.Git.RefExists(t.repo, "refs/heads/"+head) && a.Git.RefExists(t.repo, "refs/remotes/origin/"+head) {
		plan.Branch = owner + "/" + head // not this PR: a branch of origin with the same name
	}
	if err := a.checkStory(p, plan, head); err != nil {
		return err
	}
	existed := a.Git.RefExists(t.repo, "refs/heads/"+plan.Branch)
	// no "+": a PR that was force-pushed since the branch was made fails instead of resetting it
	if err := a.fetch(ctx, t.repo, fmt.Sprintf("refs/pull/%d/head:refs/heads/%s", o.PR, plan.Branch)); err != nil {
		return err
	}
	if !existed {
		plan.fork = t.repo // a failure from here on deletes it
	}
	rp := repoPlan{rel: t.rel, repo: t.repo, mode: modeLocal}
	if rp.base, err = a.recordedBase(ctx, t, "", p.IsWorkspace()); err != nil {
		return err
	}
	if err := a.prepareExisting(ctx, p, plan, targets, idx); err != nil {
		return err
	}
	plan.repos[idx] = rp
	plan.notes = append(plan.notes, fmt.Sprintf("PR #%d is from a fork · changes won't be pushed to it", o.PR))
	return nil
}

// branchExists says whether branch b is in repo and on origin (asked with ls-remote; when that
// fails, the last fetched origin/<b>).
func (a *App) branchExists(ctx context.Context, repo, b string) (local, remote bool, err error) {
	local = a.Git.RefExists(repo, "refs/heads/"+b)
	if !a.Git.HasRemote(repo) {
		return local, false, nil
	}
	c, cancel := context.WithTimeout(ctx, FetchTimeout)
	remote, err = a.Git.LsRemoteBranch(c, repo, b)
	cancel()
	if ctx.Err() != nil {
		return false, false, ctx.Err()
	}
	if err != nil {
		remote = a.Git.RefExists(repo, "refs/remotes/origin/"+b)
	}
	return local, remote, nil
}

// fetch runs `git fetch origin <refspec>` bounded by FetchTimeout; when ctx is done it returns
// ctx.Err().
func (a *App) fetch(ctx context.Context, repo, refspec string) error {
	c, cancel := context.WithTimeout(ctx, FetchTimeout)
	defer cancel()
	err := a.Git.Fetch(c, repo, refspec)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

// behind is the note for a local branch behind origin's, or "".
func (a *App) behind(repo, b string) string {
	if _, n, err := a.Git.AheadBehind(repo, "refs/heads/"+b, "refs/remotes/origin/"+b); err == nil && n > 0 {
		return fmt.Sprintf("local is %d behind origin", n)
	}
	return ""
}

// note adds a note about target t, named after its repository in a workspace.
func (plan *StoryPlan) note(ws bool, t *target, n string) {
	if n == "" {
		return
	}
	if ws {
		n = t.rel + ": " + n
	}
	plan.notes = append(plan.notes, n)
}

// planBase plans a new branch from the target's base, fetched first.
func (a *App) planBase(ctx context.Context, p *state.Project, plan *StoryPlan, t *target, override string) (repoPlan, error) {
	ws := p.IsWorkspace()
	base, missing, err := a.detectBase(ctx, t, override, ws)
	if err != nil {
		return repoPlan{}, err
	}
	start, notes, err := a.freshBase(ctx, t.repo, base)
	if err != nil {
		return repoPlan{}, err
	}
	if missing {
		plan.notes = append(plan.notes, fmt.Sprintf("override %s not in %s · used %s", override, t.rel, start))
	}
	for _, n := range notes {
		plan.note(ws, t, n)
	}
	return repoPlan{mode: modeNew, start: start, base: start}, nil
}

// detectBase finds the branch a new story starts from in one repository: the override (in a
// workspace only where the repository has it; missing says it did not), origin's HEAD, origin's
// HEAD asked for, then main, master, develop or trunk.
func (a *App) detectBase(ctx context.Context, t *target, override string, ws bool) (base string, missing bool, err error) {
	if override != "" {
		if !ws || a.hasBranch(t.repo, override) {
			return override, false, nil
		}
		missing = true
	}
	if b, err := a.Git.OriginHead(t.repo); err == nil {
		return b, missing, nil
	}
	if a.Git.HasRemote(t.repo) {
		c, cancel := context.WithTimeout(ctx, FetchTimeout)
		err := a.Git.SetHeadAuto(c, t.repo)
		cancel()
		if ctx.Err() != nil {
			return "", false, ctx.Err()
		}
		if err == nil {
			if b, err := a.Git.OriginHead(t.repo); err == nil {
				return b, missing, nil
			}
		}
	}
	if b, err := a.Git.LocalDefault(t.repo); err == nil {
		return b, missing, nil
	}
	return "", false, fmt.Errorf("no default branch in %s · set a base override in settings › git", t.name)
}

// hasBranch reports whether b is a local branch of repo or a branch of its origin.
func (a *App) hasBranch(repo, b string) bool {
	return a.Git.RefExists(repo, "refs/heads/"+b) || a.Git.RefExists(repo, "refs/remotes/origin/"+b)
}

// freshBase fetches base and returns the ref to start from: origin/<base>, the last fetched
// one when the fetch fails, the local base when there is no origin or nothing fetched.
func (a *App) freshBase(ctx context.Context, repo, base string) (start string, notes []string, err error) {
	if !a.Git.HasRemote(repo) {
		return base, nil, nil
	}
	if err := a.fetch(ctx, repo, base); err != nil {
		if ctx.Err() != nil {
			return "", nil, err
		}
		if !a.Git.RefExists(repo, "refs/remotes/origin/"+base) {
			return base, []string{"fetch failed · using local " + base}, nil
		}
		note := "fetch failed · using origin/" + base
		if at, err := a.Git.RefTime(repo, "refs/remotes/origin/"+base); err == nil {
			note += " from " + since(time.Since(at))
		}
		return "origin/" + base, []string{note}, nil
	}
	if n := a.fastForward(repo, base); n != "" {
		notes = append(notes, n)
	}
	return "origin/" + base, notes, nil
}

// since renders an age for the notes: "3h ago".
func since(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "moments ago"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

// fastForward moves the local base up to the origin/<base> just fetched when that is a
// fast-forward and safe: with git fetch . when it is not checked out, with merge --ff-only in
// the worktree that has it when that one is clean. Otherwise it leaves it and says why. It
// never resets, rebases or forces.
func (a *App) fastForward(repo, base string) string {
	if !a.Git.RefExists(repo, "refs/heads/"+base) {
		return ""
	}
	ahead, behind, err := a.Git.AheadBehind(repo, "refs/heads/"+base, "refs/remotes/origin/"+base)
	switch {
	case err != nil:
		return ""
	case ahead > 0:
		return fmt.Sprintf("local %s is %d ahead of origin · not updated", base, ahead)
	case behind == 0:
		return ""
	}
	notUpdated := fmt.Sprintf("local %s is %d behind origin · not updated", base, behind)
	dir, out, err := a.Git.WorktreeOf(repo, base)
	if err != nil {
		return notUpdated
	}
	if !out {
		if a.Git.FastForward(repo, base) != nil {
			return notUpdated
		}
		return ""
	}
	if n, err := a.Git.DirtyCount(dir); err != nil || n > 0 {
		return fmt.Sprintf("local %s has uncommitted changes · not updated", base)
	}
	if a.Git.MergeFFOnly(dir, "refs/remotes/origin/"+base) != nil {
		return notUpdated
	}
	return ""
}

// recordedBase is the base a story on an existing branch is diffed against: the repository's
// usual base, origin's when it has it. It talks to origin only to learn its HEAD; failing to
// find one records nothing.
func (a *App) recordedBase(ctx context.Context, t *target, override string, ws bool) (string, error) {
	b, _, err := a.detectBase(ctx, t, override, ws)
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		return "", nil
	}
	return a.startOf(t.repo, b), nil
}

// startOf is origin/<b> when repo has it, else b.
func (a *App) startOf(repo, b string) string {
	if a.Git.RefExists(repo, "refs/remotes/origin/"+b) {
		return "origin/" + b
	}
	return b
}

// dropMade deletes the branch PrepareStory made (a fork PR's) for a plan that was not created.
func (a *App) dropMade(plan *StoryPlan) {
	if plan.fork != "" {
		_ = a.Git.DeleteBranch(plan.fork, plan.Branch)
	}
}

// CreateStory makes a prepared story: the worktrees, the .env* files, the record. It refuses a
// story that appeared since PrepareStory. On any failure it undoes what it made and deletes
// only the branches it (or PrepareStory) made.
func (a *App) CreateStory(p *state.Project, plan *StoryPlan) (*StoryResult, error) {
	if s, ok := p.Stories[plan.Story]; ok {
		a.dropMade(plan)
		return nil, fmt.Errorf("story %q already exists (branch %s)", plan.Story, s.Branch)
	}
	var err error
	if p.IsWorkspace() {
		err = a.newWorkspaceStory(p, plan)
	} else {
		err = a.newRepoStory(p, plan)
	}
	if err != nil {
		return nil, err
	}
	res := &StoryResult{Story: plan.Story, Branch: plan.Branch, Notes: plan.notes}
	for _, rp := range plan.repos {
		rb := RepoBase{Rel: rp.rel}
		if rp.mode == modeNew {
			rb.Base = rp.start
		}
		res.Bases = append(res.Bases, rb)
	}
	return res, nil
}

// newRepoStory makes the worktree of a single-repo story, copies the .env* files and saves.
func (a *App) newRepoStory(p *state.Project, plan *StoryPlan) error {
	rp := plan.repos[0]
	dir := a.Cfg.WorktreeDir(p.Name, plan.Story)
	if err := a.addWorktree(rp, dir, plan.Branch); err != nil {
		a.dropMade(plan)
		return err
	}
	made := []added{{rp.repo, dir, rp.created(plan.Branch)}}
	fail := func(err error) error {
		a.undo(made)
		a.dropMade(plan)
		return err
	}
	if err := copyEnv(p.Path, dir); err != nil {
		return fail(err)
	}
	return a.record(p, plan, &state.Story{Branch: plan.Branch, Base: rp.base}, fail)
}

// record saves a new story and the type it used; fail undoes the worktrees when the save fails.
func (a *App) record(p *state.Project, plan *StoryPlan, story *state.Story, fail func(error) error) error {
	lastType := p.LastType
	p.Stories[plan.Story] = story
	if plan.typ != "" {
		p.LastType = plan.typ
	}
	if err := a.SaveProject(p); err != nil {
		delete(p.Stories, plan.Story)
		p.LastType = lastType
		return fail(err)
	}
	return nil
}

// addWorktree makes the worktree of one repository of a plan at dir.
func (a *App) addWorktree(rp repoPlan, dir, branch string) error {
	switch rp.mode {
	case modeNew:
		return a.Git.WorktreeAdd(rp.repo, dir, branch, rp.start)
	case modeTrack:
		return a.Git.WorktreeAddTrack(rp.repo, dir, branch, rp.start)
	}
	return a.Git.WorktreeAddExisting(rp.repo, dir, branch)
}

// created is the branch the worktree of rp made, for an undo to delete, or "" when it used one
// that was there (a fork branch is deleted by dropMade).
func (rp repoPlan) created(branch string) string {
	if rp.mode == modeLocal {
		return ""
	}
	return branch
}

// offlineBase is where a repository added to an existing story starts, without talking to
// origin (it runs under the write lock): the override when the repository has it, origin's
// HEAD, else main, master, develop or trunk; from origin's copy when there is one.
func (a *App) offlineBase(p *state.Project, repo, rel string) (string, error) {
	b := p.BaseOverride
	if b == "" || !a.hasBranch(repo, b) {
		var err error
		if b, err = a.Git.OriginHead(repo); err != nil {
			if b, err = a.Git.LocalDefault(repo); err != nil {
				return "", fmt.Errorf("no default branch in %s · set a base override in settings › git", rel)
			}
		}
	}
	return a.startOf(repo, b), nil
}

// BaseInfo is what the new story prompt shows: the base a new branch starts from (Base, with
// the override; "each repo's default" in a workspace whose repositories differ), the one
// detection finds (Detected), the override, the last type and every type.
type BaseInfo struct {
	Base, Detected, Override, LastType string
	Types                              []string
}

// StoryInfo finds the bases of a new story without fetching. It may ask origin for its HEAD.
func (a *App) StoryInfo(ctx context.Context, p *state.Project) (*BaseInfo, error) {
	targets, err := a.allTargets(p)
	if err != nil {
		return nil, err
	}
	info := &BaseInfo{Override: p.BaseOverride, LastType: p.LastType, Types: naming.Types}
	ws := p.IsWorkspace()
	shown := func(override string) (string, error) {
		var bases []string
		for _, t := range targets {
			b, _, err := a.detectBase(ctx, t, override, ws)
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			if err != nil {
				continue
			}
			if a.Git.HasRemote(t.repo) {
				b = "origin/" + b
			}
			if !slices.Contains(bases, b) {
				bases = append(bases, b)
			}
		}
		switch len(bases) {
		case 0:
			return "", nil
		case 1:
			return bases[0], nil
		}
		return "each repo's default", nil
	}
	if info.Base, err = shown(p.BaseOverride); err != nil {
		return nil, err
	}
	if info.Detected, err = shown(""); err != nil {
		return nil, err
	}
	return info, nil
}

// allTargets is every repository of a project: itself, or each repository of a workspace.
func (a *App) allTargets(p *state.Project) ([]*target, error) {
	if !p.IsWorkspace() {
		return []*target{{repo: p.Path, name: p.Name}}, nil
	}
	rels, err := a.WorkspaceRepos(p)
	if err != nil {
		return nil, err
	}
	ts := make([]*target, len(rels))
	for i, rel := range rels {
		ts[i] = &target{rel: rel, repo: filepath.Join(p.Path, rel), name: rel}
	}
	return ts, nil
}

// BranchInfo is a branch of a project, for the existing branch picker: local, on origin or
// both, its last commit, and in a workspace the repositories that have it.
type BranchInfo struct {
	Name          string
	Local, Remote bool
	Time          time.Time
	Repos         []string
}

// BranchList is the reply of Branches. Note says a fetch failed.
type BranchList struct {
	Branches []BranchInfo
	Note     string
}

// Branches lists the branches of a project, newest first; in a workspace the union over its
// repositories. With fetch it first runs `git fetch origin --prune` in each, a failure kept as a
// note.
func (a *App) Branches(ctx context.Context, p *state.Project, fetch bool) (*BranchList, error) {
	targets, err := a.allTargets(p)
	if err != nil {
		return nil, err
	}
	out := &BranchList{Branches: []BranchInfo{}}
	byName := map[string]int{}
	var failed []string
	for _, t := range targets {
		if fetch && a.Git.HasRemote(t.repo) {
			c, cancel := context.WithTimeout(ctx, FetchTimeout)
			err := a.Git.FetchPrune(c, t.repo)
			cancel()
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if err != nil {
				failed = append(failed, t.name)
			}
		}
		bs, err := a.Git.Branches(t.repo)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", t.name, err)
		}
		for _, b := range bs {
			i, ok := byName[b.Name]
			if !ok {
				i = len(out.Branches)
				byName[b.Name] = i
				out.Branches = append(out.Branches, BranchInfo{Name: b.Name})
			}
			bi := &out.Branches[i]
			bi.Local, bi.Remote = bi.Local || b.Local, bi.Remote || b.Remote
			if b.Time.After(bi.Time) {
				bi.Time = b.Time
			}
			if p.IsWorkspace() {
				bi.Repos = append(bi.Repos, t.rel)
			}
		}
	}
	sort.SliceStable(out.Branches, func(i, j int) bool { return out.Branches[i].Time.After(out.Branches[j].Time) })
	switch {
	case len(failed) == 0:
	case p.IsWorkspace():
		out.Note = "fetch failed in " + strings.Join(failed, ", ") + " · showing the last fetched branches"
	default:
		out.Note = "fetch failed · showing the last fetched branches"
	}
	return out, nil
}

// PullRequest is a pull request for the picker. Owner is its head repository's owner, Repo the
// workspace repository it belongs to ("" in a single-repo project), Slug that repository's
// owner/repo.
type PullRequest struct {
	Number                                   int
	Title, Author, Branch, Owner, Repo, Slug string
	State                                    string
	Fork                                     bool
}

// PRList is the reply of PullRequests. Errors are the gh failures, per repository in a
// workspace; Direct says the query named one pull request (#12 or its URL).
type PRList struct {
	PRs    []PullRequest
	Errors []string
	Direct bool
}

var (
	prNumRe = regexp.MustCompile(`^#?(\d+)$`)
	prURLRe = regexp.MustCompile(`^https?://[^/]+/([^/]+)/([^/]+)/pull/(\d+)`)
)

// PullRequests asks gh, in each repository's folder so it picks the repository and host from
// origin, for the open pull requests (q ""), one pull request (q "#12", "12" or its URL) or a
// search (any other q, every state). When ctx is done gh is killed and it returns ctx.Err().
func (a *App) PullRequests(ctx context.Context, p *state.Project, q string) (*PRList, error) {
	targets, err := a.allTargets(p)
	if err != nil {
		return nil, err
	}
	q = strings.TrimSpace(q)
	out := &PRList{PRs: []PullRequest{}}
	args := []string{"pr", "list", "--state", "open", "--limit", "50", "--json", prFields}
	slugs := make([]string, len(targets))
	for i, t := range targets {
		slugs[i] = a.Git.OriginSlug(t.repo)
	}
	if m := prNumRe.FindStringSubmatch(q); m != nil {
		out.Direct, args = true, []string{"pr", "view", m[1], "--json", prFields}
	} else if m := prURLRe.FindStringSubmatch(q); m != nil {
		out.Direct, args = true, []string{"pr", "view", m[3], "--json", prFields}
		var keep []*target
		var keepSlugs []string
		for i, t := range targets {
			if strings.EqualFold(slugs[i], m[1]+"/"+m[2]) {
				keep, keepSlugs = append(keep, t), append(keepSlugs, slugs[i])
			}
		}
		if len(keep) == 0 {
			out.Errors = append(out.Errors, fmt.Sprintf("%s/%s is not a repository of %s", m[1], m[2], p.Name))
			return out, nil
		}
		targets, slugs = keep, keepSlugs
	} else if q != "" {
		args = []string{"pr", "list", "--search", q, "--state", "all", "--limit", "50", "--json", prFields}
	}
	for i, t := range targets {
		label := slugs[i]
		if label == "" {
			label = t.rel
		}
		raw, err := a.GH(ctx, t.repo, args...)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var prs []ghPR
		if err == nil {
			if out.Direct {
				var one ghPR
				err = json.Unmarshal([]byte(raw), &one)
				prs = []ghPR{one}
			} else {
				err = json.Unmarshal([]byte(raw), &prs)
			}
		}
		if err != nil {
			msg := strings.TrimPrefix(err.Error(), "gh "+strings.Join(args, " ")+": ")
			if p.IsWorkspace() {
				msg = label + ": " + msg
			}
			out.Errors = append(out.Errors, msg)
			continue
		}
		for _, pr := range prs {
			out.PRs = append(out.PRs, PullRequest{Number: pr.Number, Title: pr.Title, Author: pr.Author.Login, Branch: pr.HeadRefName,
				Owner: pr.HeadRepositoryOwner.Login, Repo: t.rel, Slug: label, State: pr.State, Fork: pr.IsCrossRepository})
		}
	}
	if out.Direct && len(out.PRs) > 0 {
		out.Errors = nil // the repositories that do not have it
	}
	return out, nil
}

// SetBaseOverride sets the branch new stories of p start from, or with "" goes back to
// detecting it. The branch must be a local or origin branch of the project, of at least one
// repository in a workspace.
func (a *App) SetBaseOverride(p *state.Project, b string) error {
	b = strings.TrimSpace(b)
	if b != "" {
		if err := a.Git.ValidBranch(b); err != nil {
			return err
		}
		targets, err := a.allTargets(p)
		if err != nil {
			return err
		}
		if !slices.ContainsFunc(targets, func(t *target) bool { return a.hasBranch(t.repo, b) }) {
			return fmt.Errorf("no branch %s in %s", b, p.Name)
		}
	}
	p.BaseOverride = b
	return a.SaveProject(p)
}
