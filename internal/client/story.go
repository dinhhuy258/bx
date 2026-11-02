package client

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/dinhhuy258/bx/internal/api"
	"github.com/dinhhuy258/bx/internal/naming"
	"github.com/dinhhuy258/bx/internal/ui"
)

// The items of the `new story from:` picker.
const (
	fromNewBranch = "new branch"
	fromBranch    = "existing branch"
	fromPR        = "pull request"
)

// noType is the picker item of the "none" branch type: a branch without a type prefix.
const noType = "(none)"

// fetchingOrigin is the key bar while an existing branch or a pull request is fetched.
const fetchingOrigin = "fetching origin… · esc to cancel"

// storyDraft is a new story between its pickers: what POST stories gets, and what the pickers,
// the checklist and the key bar show on the way.
type storyDraft struct {
	project       string
	act           api.Action
	info          api.StoryInfo     // the base and last type, for a new branch
	story, branch string            // for the title of the workspace checklist
	tick          string            // the repository ticked in that checklist (a pull request's)
	busy          string            // the key bar while it is created
	branches      map[string]string // existing branch picker: label → branch
	prs           map[string]api.PR // pull request picker: label → pull request
}

type (
	// storyInfoMsg is the base and last type of a new branch, for its name prompt.
	storyInfoMsg struct {
		project string
		info    api.StoryInfo
		err     error
	}
	// branchesMsg is the fetched branches, for the existing branch picker.
	branchesMsg struct {
		project string
		list    api.BranchList
		err     error
	}
	// prsMsg is the open pull requests (q == "") or the reply of a search for q.
	prsMsg struct {
		project, q string
		list       api.PRList
		err        error
	}
	// storyMsg is the reply of POST stories for draft.
	storyMsg struct {
		draft storyDraft
		res   api.Result
		err   error
	}
)

// openStorySource opens the `new story from:` picker, new branch highlighted.
func (m *Model) openStorySource() {
	m.draft = &storyDraft{project: m.project}
	m.picker, m.pickKind = ui.NewPicker("new story from:", []string{fromNewBranch, fromBranch, fromPR}), pickStorySource
}

// pickStorySource loads what the chosen source needs: the base for a new branch, the fetched
// branches, or the open pull requests.
func (m *Model) pickStorySource(choice string) tea.Cmd {
	be, ctx, project := m.be, m.ctx, m.project
	switch choice {
	case fromNewBranch:
		m.setFlash("loading…")
		return func() tea.Msg {
			info, err := be.StoryInfo(ctx, project)
			return storyInfoMsg{project, info, err}
		}
	case fromBranch:
		ctx := m.startBusy(fetchingOrigin)
		return func() tea.Msg {
			list, err := be.Branches(ctx, project, true)
			return branchesMsg{project, list, err}
		}
	case fromPR:
		m.setFlash("loading pull requests…")
		return func() tea.Msg {
			list, err := be.PullRequests(ctx, project, "")
			return prsMsg{project: project, list: list, err: err}
		}
	}
	return nil
}

// gotStoryInfo opens the name prompt, its label previewing the branch and its base.
func (m *Model) gotStoryInfo(msg storyInfoMsg) {
	if msg.project != m.project || m.draft == nil {
		return
	}
	if msg.err != nil {
		m.setFlash("error: " + msg.err.Error())
		return
	}
	m.draft.info, m.draft.busy = msg.info, createBusy(msg.info.Base)
	branch := "<name>"
	if t := naming.DefaultType(msg.info.LastType); t != "none" {
		branch = t + "/<name>"
	}
	if msg.info.Base != "" {
		branch += " ← " + msg.info.Base
	}
	m.openPrompt(ui.PromptNewStory, "", "story name ("+branch+")", "")
}

// createBusy is the key bar while a new branch is created from base.
func createBusy(base string) string {
	switch {
	case strings.HasPrefix(base, "origin/"):
		return "fetching " + base + "… · esc to cancel"
	case base != "" && !strings.Contains(base, " "): // a local branch: no remote, nothing to fetch
		return "creating story… · esc to cancel"
	}
	return fetchingOrigin // "each repo's default" in a workspace
}

// storyName takes the typed name: a full branch ("fix/x") goes on, a plain name gets the type
// picker with the last used type highlighted.
func (m *Model) storyName(text string) tea.Cmd {
	d := m.draft
	if text == "" || d == nil {
		return nil
	}
	d.act = api.Action{Name: text}
	if b := naming.CleanBranch(text); strings.Contains(b, "/") {
		d.story, d.branch = naming.StoryName(b), b
		return m.storyNext()
	}
	d.story = naming.Slug(text)
	items := make([]string, len(naming.Types))
	for i, t := range naming.Types {
		items[i] = typeItem(t)
	}
	m.picker, m.pickKind = ui.NewPicker("branch type · "+d.story, items), pickStoryType
	m.picker.Select(typeItem(naming.DefaultType(d.info.LastType)))
	return nil
}

// typeItem is the picker item of a branch type.
func typeItem(t string) string {
	if t == "none" {
		return noType
	}
	return t
}

// pickStoryType sets the chosen branch type and goes on.
func (m *Model) pickStoryType(choice string) tea.Cmd {
	d := m.draft
	if d == nil {
		return nil
	}
	typ := choice
	if typ == noType {
		typ = "none"
	}
	d.act.Type, d.branch = typ, naming.Branch(typ, d.story)
	return m.storyNext()
}

// gotBranches opens the existing branch picker, newest first.
func (m *Model) gotBranches(msg branchesMsg) {
	m.stopBusy()
	if msg.project != m.project || m.draft == nil {
		return
	}
	switch {
	case errors.Is(msg.err, context.Canceled):
		m.setFlash("cancelled")
		return
	case msg.err != nil:
		m.setFlash("error: " + msg.err.Error())
		return
	}
	if msg.list.Note != "" {
		m.setFlash(msg.list.Note)
	}
	// a worktree already has a checked-out branch, and git checks a branch out once
	free := slices.DeleteFunc(slices.Clone(msg.list.Branches), func(b api.Branch) bool { return b.CheckedOut })
	if len(free) == 0 {
		m.setFlash("no branches in " + m.project)
		return
	}
	ws := m.m != nil && m.m.P.IsWorkspace()
	m.draft.branches = map[string]string{}
	items := make([]string, 0, len(free))
	for _, b := range free {
		where := "origin"
		if b.Local {
			where = "local"
		}
		label := b.Name + "  " + where + " · " + ui.Ago(time.Since(time.Unix(b.Time, 0)))
		if ws && len(b.Repos) > 0 {
			label += " · " + strings.Join(b.Repos, ", ")
		}
		items = append(items, label)
		m.draft.branches[label] = b.Name
	}
	m.picker, m.pickKind = ui.NewPicker("existing branch", items), pickStoryBranch
}

// pickStoryBranch makes a story on the chosen existing branch.
func (m *Model) pickStoryBranch(choice string) tea.Cmd {
	d := m.draft
	if d == nil {
		return nil
	}
	b := d.branches[choice]
	d.act = api.Action{Source: "branch", Branch: b}
	d.story, d.branch, d.busy = naming.StoryName(b), b, fetchingOrigin
	return m.storyNext()
}

// gotPRs opens the pull request picker, or fills it with the results of a search. A search
// that named one pull request (#12, its URL) creates the story at once.
func (m *Model) gotPRs(msg prsMsg) tea.Cmd {
	if msg.project != m.project || m.draft == nil {
		return nil
	}
	if msg.err != nil {
		m.setFlash("error: " + msg.err.Error())
		return nil
	}
	l := msg.list
	if msg.q == "" {
		if len(l.PRs) == 0 && len(l.Errors) > 0 {
			m.setFlash("gh: " + l.Errors[0])
			return nil
		}
		title := "pull requests"
		if len(l.Errors) > 0 {
			title += " · gh: " + l.Errors[0]
		}
		m.picker, m.pickKind = ui.NewPicker(title, m.prItems(l.PRs)), pickStoryPR
		return nil
	}
	if m.picker == nil || m.pickKind != pickStoryPR { // closed while gh searched
		return nil
	}
	switch {
	case l.Direct && len(l.PRs) == 1:
		m.picker = nil
		return m.pickPR(l.PRs[0])
	case len(l.PRs) == 0 && len(l.Errors) > 0:
		m.setFlash("gh: " + l.Errors[0])
	case len(l.PRs) == 0:
		m.setFlash("no pull requests match " + msg.q)
	default:
		m.picker.SetItems(m.prItems(l.PRs))
		m.picker.Title = "search: " + msg.q
	}
	return nil
}

// prItems are the picker items of prs: "#123 Fix login timeout   alice · fix/login-timeout",
// after the repository's owner/repo in a workspace.
func (m *Model) prItems(prs []api.PR) []string {
	ws := m.m != nil && m.m.P.IsWorkspace()
	m.draft.prs = map[string]api.PR{}
	items := make([]string, 0, len(prs))
	for _, pr := range prs {
		label := fmt.Sprintf("#%d %s   %s · %s", pr.Number, pr.Title, pr.Author, pr.Branch)
		if ws {
			repo := pr.Slug
			if repo == "" {
				repo = pr.Repo
			}
			label = repo + " " + label
		}
		items = append(items, label)
		m.draft.prs[label] = pr
	}
	return items
}

// directPRRe is a filter that names one pull request: #123 or its URL.
var directPRRe = regexp.MustCompile(`^(#\d+|https?://[^/\s]+/[^/\s]+/[^/\s]+/pull/\d+\S*)$`)

// directPR reports whether the picker filter q names one pull request. Enter then looks that
// one up (gh pr view) instead of taking the first row that contains q.
func directPR(q string) bool { return directPRRe.MatchString(strings.TrimSpace(q)) }

// searchPRs asks GitHub for the pull requests matching what was typed when nothing in the
// picker does, or for the one pull request it names. The picker stays open.
func (m *Model) searchPRs(q string) tea.Cmd {
	if q = strings.TrimSpace(q); q == "" {
		return nil
	}
	m.setFlash("searching GitHub…")
	be, ctx, project := m.be, m.ctx, m.project
	return func() tea.Msg {
		list, err := be.PullRequests(ctx, project, q)
		return prsMsg{project, q, list, err}
	}
}

// pickPR makes a story on a pull request's branch.
func (m *Model) pickPR(pr api.PR) tea.Cmd {
	d := m.draft
	if d == nil {
		return nil
	}
	d.act = api.Action{Source: "pr", PR: pr.Number, PRRepo: pr.Repo}
	d.story, d.branch, d.tick, d.busy = naming.StoryName(pr.Branch), pr.Branch, pr.Repo, fetchingOrigin
	return m.storyNext()
}

// storyNext asks for the repositories in a workspace, else creates the story.
func (m *Model) storyNext() tea.Cmd {
	if m.m != nil && m.m.P.IsWorkspace() {
		return m.loadStoryRepos(m.draft.story, false)
	}
	return m.createStory()
}

// createStory sends the draft. It runs in the background, the key bar says so, and Esc
// cancels it, which makes the server stop fetching.
func (m *Model) createStory() tea.Cmd {
	if m.draft == nil {
		return nil
	}
	d, be := *m.draft, m.be
	busy := d.busy
	if busy == "" {
		busy = fetchingOrigin
	}
	ctx := m.startBusy(busy)
	return func() tea.Msg {
		res, err := be.NewStory(ctx, d.project, d.act)
		return storyMsg{d, res, err}
	}
}

// gotStory shows the result of a create. A branch that exists asks to use it. A new story's
// session is selected, shown and given the keys; when it did not start, the story is selected.
func (m *Model) gotStory(msg storyMsg) tea.Cmd {
	m.stopBusy()
	d, res := msg.draft, msg.res
	switch {
	case errors.Is(msg.err, context.Canceled):
		m.setFlash("cancelled")
		return nil
	case res.Exists != "":
		where := map[string]string{"local": "locally", "origin": "on origin", "both": "locally and on origin"}[res.Exists]
		m.draft = &d
		m.openPrompt(ui.PromptUseBranch, "", fmt.Sprintf("branch %s exists %s · use it? y/n", res.Branch, where), "")
		return nil
	case msg.err != nil:
		m.setFlash("error: " + msg.err.Error())
		return nil
	}
	flash := res.Message
	if res.SessionError != "" {
		flash += " · session: " + res.SessionError
	}
	m.setFlash(flash)
	if d.project != m.project {
		return nil
	}
	if res.Num == 0 {
		m.pendingStory = res.Story
		return m.poll()
	}
	m.pendingSel = res.Num
	m.claudeFocused = true
	m.resizeTerm()
	return tea.Batch(m.show(sessionTarget(res.Num)), m.poll())
}

// useBranch sends the draft again, using the branch that exists.
func (m *Model) useBranch() tea.Cmd {
	if m.draft == nil {
		return nil
	}
	m.draft.act.UseExisting = true
	return m.createStory()
}

// startBusy shows text in the key bar until stopBusy, and returns the context Esc cancels.
func (m *Model) startBusy(text string) context.Context {
	m.stopBusy()
	ctx, cancel := context.WithCancel(m.ctx)
	m.busy, m.busyCancel = text, cancel
	return ctx
}

func (m *Model) stopBusy() {
	if m.busyCancel != nil {
		m.busyCancel()
	}
	m.busy, m.busyCancel = "", nil
}

// overrideDraft is a base override between its pickers in settings › git.
type overrideDraft struct {
	project  string
	branches map[string]api.Branch // branch picker: label → branch
}

// overrideBranchesMsg is the detected base and the branches of a project, for the base
// branch picker of settings › git.
type overrideBranchesMsg struct {
	project string
	info    api.StoryInfo
	list    api.BranchList
	err     error
}

// gitOverrides are the projects with a base override, for settings › git.
func (m *Model) gitOverrides() []ui.GitOverride {
	var out []ui.GitOverride
	for _, p := range m.projects {
		if p.BaseOverride != "" {
			out = append(out, ui.GitOverride{Project: p.Name, Base: p.BaseOverride})
		}
	}
	return out
}

// baseOverride is the base override of project, "" for none.
func (m *Model) baseOverride(project string) string {
	for _, p := range m.projects {
		if p.Name == project {
			return p.BaseOverride
		}
	}
	return ""
}

// openOverrideProjects opens the picker of the projects without a base override.
func (m *Model) openOverrideProjects() {
	var items []string
	for _, p := range m.projects {
		if p.BaseOverride == "" {
			items = append(items, p.Name)
		}
	}
	if len(items) == 0 {
		m.setFlash("every project has a base override")
		return
	}
	m.picker, m.pickKind = ui.NewPicker("base override for…", items), pickOverrideProject
}

// chooseOverrideBranch loads the detected base and the branches of project, without fetching,
// for the base branch picker.
func (m *Model) chooseOverrideBranch(project string) tea.Cmd {
	m.override = overrideDraft{project: project}
	m.setFlash("loading…")
	be, ctx := m.be, m.ctx
	return func() tea.Msg {
		info, err := be.StoryInfo(ctx, project)
		if err != nil {
			return overrideBranchesMsg{project: project, err: err}
		}
		list, err := be.Branches(ctx, project, false)
		return overrideBranchesMsg{project, info, list, err}
	}
}

// gotOverrideBranches opens the base branch picker, on the current override if there is one.
// In a workspace a branch some repositories lack says which.
func (m *Model) gotOverrideBranches(msg overrideBranchesMsg) {
	if m.settings == nil || msg.project != m.override.project {
		return
	}
	if msg.err != nil {
		m.setFlash("error: " + msg.err.Error())
		return
	}
	if len(msg.list.Branches) == 0 {
		m.setFlash("no branches in " + msg.project)
		return
	}
	m.flash = ""
	var repos []string // a workspace's: every repository some branch is in
	for _, b := range msg.list.Branches {
		for _, r := range b.Repos {
			if !slices.Contains(repos, r) {
				repos = append(repos, r)
			}
		}
	}
	slices.Sort(repos)
	current := m.baseOverride(msg.project)
	m.override.branches = map[string]api.Branch{}
	items := make([]string, 0, len(msg.list.Branches))
	selected := ""
	for _, b := range msg.list.Branches {
		label := b.Name
		if missing := slices.DeleteFunc(slices.Clone(repos), func(r string) bool { return slices.Contains(b.Repos, r) }); len(missing) > 0 {
			label += "  · not in " + strings.Join(missing, ", ")
		}
		items = append(items, label)
		m.override.branches[label] = b
		if b.Name == current {
			selected = label
		}
	}
	title := "base branch for " + msg.project
	if msg.info.Detected != "" {
		title += " · detected: " + msg.info.Detected
	}
	m.picker, m.pickKind = ui.NewPicker(title, items), pickOverrideBranch
	if selected != "" {
		m.picker.Select(selected)
	}
}

// setOverride saves the chosen branch as the base override of the picked project.
func (m *Model) setOverride(choice string) tea.Cmd {
	p, b := m.override.project, m.override.branches[choice]
	if p == "" || b.Name == "" {
		return nil
	}
	if _, err := m.be.Do(m.ctx, p, "base", api.Action{Base: b.Name}); err != nil {
		m.setFlash("error: " + err.Error())
		return nil
	}
	base := b.Name
	if b.Remote {
		base = "origin/" + base
	}
	m.setFlash(p + ": new stories start from " + base)
	return m.loadProjects()
}

// removeOverride drops the base override of project: new stories go back to the detected base.
func (m *Model) removeOverride(project string) tea.Cmd {
	if _, err := m.be.Do(m.ctx, project, "base", api.Action{}); err != nil {
		m.setFlash("error: " + err.Error())
		return nil
	}
	m.setFlash(project + ": new stories start from each repo's default branch")
	return m.loadProjects()
}
