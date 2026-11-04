package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/dinhhuy258/bx/internal/claude"
	"github.com/dinhhuy258/bx/internal/git"
	"github.com/dinhhuy258/bx/internal/settings"
	"github.com/dinhhuy258/bx/internal/state"
	"github.com/dinhhuy258/bx/internal/uploads"
)

const defaultTitle = "New session"

// titleLen is how much of the first prompt becomes the title of an unnamed session.
const titleLen = 40

// ErrNotRunning means the session's tmux session does not exist (Claude exited or the
// server restarted); `r` resumes it.
var ErrNotRunning = errors.New("not running")

// TmuxName is the tmux session of a bx session. tmux replaces "." and ":" in names with "_",
// so they are replaced here too and the name found by list-sessions matches.
func TmuxName(project string, num int) string {
	return strings.NewReplacer(".", "_", ":", "_").Replace(fmt.Sprintf("bx-%s-%d", project, num))
}

// Live maps the number of every session whose tmux session exists to its id ($n).
func (a *App) Live(p *state.Project) (map[int]string, error) {
	ss, err := a.Tmux.Sessions()
	if err != nil {
		return nil, err
	}
	byName := make(map[string]string, len(ss))
	for _, se := range ss {
		byName[se.Name] = se.ID
	}
	out := map[int]string{}
	for n := range p.Sessions {
		if id, ok := byName[TmuxName(p.Name, n)]; ok {
			out[n] = id
		}
	}
	return out, nil
}

// Target is the tmux id of a running session.
func (a *App) Target(p *state.Project, num int) (string, error) {
	if p.Sessions[num] == nil {
		return "", fmt.Errorf("no session [%d]", num)
	}
	live, err := a.Live(p)
	if err != nil {
		return "", err
	}
	id, ok := live[num]
	if !ok {
		return "", fmt.Errorf("session [%d] is %w", num, ErrNotRunning)
	}
	return id, nil
}

// NewSession starts Claude in the story's worktree. Returns the number.
func (a *App) NewSession(p *state.Project, story, title string) (int, error) {
	if _, ok := p.Stories[story]; !ok {
		return 0, fmt.Errorf("no story %q", story)
	}
	return a.start(p, story, title)
}

// NewChat starts Claude in the project folder under the chats group.
func (a *App) NewChat(p *state.Project, title string) (int, error) { return a.start(p, "", title) }

// start takes the number first because the tmux session is named after it.
func (a *App) start(p *state.Project, story, title string) (int, error) {
	if strings.TrimSpace(title) == "" {
		title = defaultTitle
	}
	id := a.UUID()
	s := &state.Session{Story: story, UUID: id, Title: title, Created: time.Now(),
		Model: a.Settings.Get("claude.model"), Effort: a.Settings.Get("claude.effort"), PermissionMode: a.Settings.Get("claude.permission_mode")}
	num := p.AddSession(s)
	if err := a.launch(p, num, claude.StartArgs(id, false, a.options(p, num, s))); err != nil {
		delete(p.Sessions, num)
		p.NextNum--
		return 0, err
	}
	return num, a.SaveProject(p)
}

// options are the settings a session started with, for its command line. With Self set, every
// session gets the bx status line, and a session of a workspace story also gets the guard hook,
// which keeps edits out of the original repositories.
// With PluginDir set, every session also gets its artifacts folder (see artifacts.go), and with
// APIURL too the page hook, which tells bx serve when Claude writes a page.
// Every session also gets the MCP servers of its project and of ~/.bx/mcp.json (see mcp.go),
// and the Claude settings of the settings screen as they are now, so a resumed session takes
// today's attribution while it keeps its own model, effort and permission mode.
// A "none" commit trailer or PR line is also said in the system prompt (see settings.ClaudePrompt).
func (a *App) options(p *state.Project, num int, s *state.Session) claude.Options {
	o := claude.Options{Model: s.Model, Effort: s.Effort, PermissionMode: s.PermissionMode}
	var hooks, perms, status map[string]any
	if a.Self != "" {
		status = statusLine(a.Self)
	}
	if p.IsWorkspace() && s.Story != "" && a.Self != "" {
		hooks = guardHooks(a.Self, a.Dir(p, s.Story))
	}
	if a.PluginDir != "" && a.Self != "" && a.APIURL != "" {
		hooks = withHooks(hooks, pageHooks(a.Self, a.APIURL))
	}
	if a.PluginDir != "" {
		dir := a.ArtifactsDir(p.Name, num)
		o.DisallowedTools = artifactTools
		o.PluginDirs = []string{a.PluginDir}
		o.AddDirs = []string{dir}
		o.AppendSystemPrompt = artifactsPrompt(dir, a.ArtifactsLink(p.Name, num))
		perms = artifactPermissions(dir)
	}
	o.MCPConfig, o.StrictMCPConfig = a.sessionMCP(p, num)
	values := a.Settings.Values()
	if extra := settings.ClaudePrompt(values); extra != "" {
		o.AppendSystemPrompt = strings.TrimSpace(o.AppendSystemPrompt + " " + extra)
	}
	o.Settings = settingsJSON(settings.ClaudeSettings(values), hooks, perms, status)
	return o
}

// launch runs args in a new tmux session for session num, in its story's folder, and keeps
// the tmux server up after its last session ends, so bx serve can tell that Claude exited.
// With PluginDir set it first makes the session's artifacts folder and sets $BX_ARTIFACTS.
func (a *App) launch(p *state.Project, num int, args []string) error {
	dir := a.Dir(p, p.Sessions[num].Story)
	env, err := a.accountEnv(context.Background(), p.Name, dir)
	if err != nil {
		return err
	}
	if a.PluginDir != "" {
		art, err := uploads.MakeDir(a.Cfg.ArtifactsDir(), p.Name, num)
		if err != nil {
			return fmt.Errorf("artifacts folder: %w", err)
		}
		args = append([]string{"env", "BX_ARTIFACTS=" + art}, args...)
	}
	if _, err := a.Tmux.NewSession(TmuxName(p.Name, num), dir, shellJoin(args), env...); err != nil {
		return err
	}
	// best effort: a Claude that exits at once may take the server with it
	if err := a.Tmux.KeepServer(); err != nil {
		a.logf("tmux keep server: %v", err)
	}
	return nil
}

// Resume restarts a session that is not running with `claude --resume <uuid>` and the
// settings it started with.
func (a *App) Resume(p *state.Project, num int) error {
	s := p.Sessions[num]
	if s == nil {
		return fmt.Errorf("no session [%d]", num)
	}
	live, err := a.Live(p)
	if err != nil {
		return err
	}
	if _, ok := live[num]; ok {
		return fmt.Errorf("session [%d] is still running", num)
	}
	if err := a.launch(p, num, claude.StartArgs(s.UUID, true, a.options(p, num, s))); err != nil {
		return err
	}
	s.Done = false
	return a.SaveProject(p)
}

// ResumeAll restarts every session that is not done, has no tmux session, and is not live in
// Claude elsewhere (agents, by uuid), and returns their numbers. bx serve calls it at startup,
// after a reboot.
func (a *App) ResumeAll(p *state.Project, agents map[string]claude.Agent) ([]int, error) {
	live, err := a.Live(p)
	if err != nil {
		return nil, err
	}
	nums := make([]int, 0, len(p.Sessions))
	for n := range p.Sessions {
		nums = append(nums, n)
	}
	sort.Ints(nums)
	var resumed []int
	for _, n := range nums {
		s := p.Sessions[n]
		if _, alive := live[n]; s.Done || alive {
			continue
		}
		if _, elsewhere := agents[s.UUID]; elsewhere {
			continue
		}
		if err := a.launch(p, n, claude.StartArgs(s.UUID, true, a.options(p, n, s))); err != nil {
			return resumed, err
		}
		resumed = append(resumed, n)
	}
	return resumed, nil
}

// RunningError refuses to delete a session whose tmux session is alive; force kills it first.
type RunningError struct{ Num int }

func (e *RunningError) Error() string { return fmt.Sprintf("session [%d] is still running", e.Num) }

// Stop kills the session's tmux session and marks it done, so ResumeAll leaves it alone; `r`
// resumes it. Done is saved before the kill: a killed session must never be left with
// Done=false, or ResumeAll would bring it back. A failed kill puts Done back. Stopping a
// session that is gone or done only marks it done.
func (a *App) Stop(p *state.Project, num int) error {
	s := p.Sessions[num]
	if s == nil {
		return fmt.Errorf("no session [%d]", num)
	}
	live, err := a.Live(p)
	if err != nil {
		return err
	}
	s.Done = true
	if err := a.SaveProject(p); err != nil {
		return err
	}
	id, ok := live[num]
	if !ok {
		return nil
	}
	if killErr := a.Tmux.KillSession(id); killErr != nil {
		s.Done = false
		return errors.Join(killErr, a.SaveProject(p))
	}
	return nil
}

// DeleteSession forgets a session and its uploads and artifacts. Without force a session whose tmux session
// is alive is refused with *RunningError; force kills it first. Claude's transcript in
// ~/.claude stays, and the number is not reused.
func (a *App) DeleteSession(p *state.Project, num int, force bool) error {
	if p.Sessions[num] == nil {
		return fmt.Errorf("no session [%d]", num)
	}
	live, err := a.Live(p)
	if err != nil {
		return err
	}
	if id, alive := live[num]; alive {
		if !force {
			return &RunningError{Num: num}
		}
		if err := a.Tmux.KillSession(id); err != nil {
			return err
		}
	}
	a.removeSessionFiles(p.Name, num)
	delete(p.Sessions, num)
	return a.SaveProject(p)
}

// MarkDone marks the sessions done, as when Claude exited. Numbers without a record are
// skipped. It saves, and reports true, only when something changed.
func (a *App) MarkDone(p *state.Project, nums []int) (bool, error) {
	changed := false
	for _, n := range nums {
		if s := p.Sessions[n]; s != nil && !s.Done {
			s.Done, changed = true, true
		}
	}
	if !changed {
		return false, nil
	}
	return true, a.SaveProject(p)
}

// Rename sets the title shown in the list.
func (a *App) Rename(p *state.Project, num int, title string) error {
	s := p.Sessions[num]
	if s == nil {
		return fmt.Errorf("no session [%d]", num)
	}
	s.Title = title
	return a.SaveProject(p)
}

// Titles finds a title for every running session still called "New session": the start of
// the first prompt in its transcript. It only reads; SetTitles stores them.
func (a *App) Titles(p *state.Project, agents map[string]claude.Agent) map[int]string {
	if a.Transcripts == "" {
		return nil
	}
	var out map[int]string
	for n, s := range p.Sessions {
		ag, live := agents[s.UUID]
		if s.Title != defaultTitle || !live || ag.Cwd == "" {
			continue
		}
		if t := claude.FirstPrompt(claude.TranscriptPath(a.Transcripts, ag.Cwd, s.UUID)); t != "" {
			if out == nil {
				out = map[int]string{}
			}
			out[n] = shorten(t, titleLen)
		}
	}
	return out
}

// SetTitles stores titles from Titles on the sessions that are still unnamed, so a rename in
// between wins. Reports whether anything changed.
func (a *App) SetTitles(p *state.Project, titles map[int]string) (bool, error) {
	changed := false
	for n, t := range titles {
		if s := p.Sessions[n]; s != nil && s.Title == defaultTitle {
			s.Title, changed = t, true
		}
	}
	if !changed {
		return false, nil
	}
	return true, a.SaveProject(p)
}

func shorten(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n-1])) + "…"
}

// DirtyError refuses to close a story whose worktree has uncommitted files, or, with Original,
// to add a repository whose original has them. Detail splits Files by repository.
type DirtyError struct {
	Story    string
	Files    int
	Detail   string
	Original bool
}

func (e *DirtyError) Error() string {
	if e.Original {
		return fmt.Sprintf("uncommitted files in the original (%s) will not be in story %q", e.Detail, e.Story)
	}
	if e.Detail != "" {
		return fmt.Sprintf("story %q has %d uncommitted files (%s)", e.Story, e.Files, e.Detail)
	}
	return fmt.Sprintf("story %q has %d uncommitted files", e.Story, e.Files)
}

// CloseStory stops the story's sessions, removes its worktree, forgets it and deletes its
// branch, here and on origin. Without force a dirty worktree is refused with *DirtyError; force
// discards the changes. Once the story is closed a branch that cannot be deleted is no error:
// the returned message says what happened to it (see deleteBranches).
func (a *App) CloseStory(p *state.Project, name string, force bool) (string, error) {
	story, ok := p.Stories[name]
	if !ok {
		return "", fmt.Errorf("no story %q", name)
	}
	var err error
	if p.IsWorkspace() {
		err = a.closeWorkspaceStory(p, name, force)
	} else {
		err = a.closeRepoStory(p, name, force)
	}
	if err != nil {
		return "", err
	}
	return a.deleteBranches(p, story), nil
}

func (a *App) closeRepoStory(p *state.Project, name string, force bool) error {
	dir := a.Cfg.WorktreeDir(p.Name, name)
	if !force {
		if n, err := a.Git.DirtyCount(dir); err == nil && n > 0 {
			return &DirtyError{Story: name, Files: n}
		}
	}
	if err := a.stopStory(p, name); err != nil {
		return err
	}
	if _, err := os.Stat(dir); err == nil {
		if err := a.Git.WorktreeRemove(p.Path, dir, force); err != nil {
			return err
		}
	}
	return a.forgetStory(p, name)
}

// BranchDeleteTimeout bounds the delete of a closed story's branch on origin, in each
// repository. It runs under the write lock, so it is short.
const BranchDeleteTimeout = 30 * time.Second

// defaultBranches are never deleted with a story, whatever it was made from.
var defaultBranches = []string{"main", "master", "develop", "trunk"}

// deleteBranches deletes a closed story's branch in each of its repositories: the local branch,
// then origin's (see git.DeleteRemoteBranch). A base branch (main, master, develop, trunk,
// origin's HEAD, the base override, the branch the story forked from) is kept. It returns one
// line: "deleted branch feat/x (local, origin)", in a workspace "deleted branch feat/x (api:
// local, origin · web: local)", then any branch kept or failed.
func (a *App) deleteBranches(p *state.Project, story *state.Story) string {
	type repo struct{ rel, dir, base string }
	repos := []repo{{"", p.Path, story.Base}}
	if p.IsWorkspace() {
		repos = repos[:0]
		for _, r := range story.Repos {
			repos = append(repos, repo{r.Rel, filepath.Join(p.Path, r.Rel), r.Base})
		}
	}
	b := story.Branch
	var deleted, notes []string
	for _, r := range repos {
		at := func(s string) string { // "api: s" in a workspace
			if r.rel == "" {
				return s
			}
			return r.rel + ": " + s
		}
		if a.isBaseBranch(p, r.dir, r.base, b) {
			notes = append(notes, at("kept "+b+", a base branch"))
			continue
		}
		var done []string
		if a.Git.RefExists(r.dir, "refs/heads/"+b) {
			if err := a.Git.DeleteBranch(r.dir, b); err != nil {
				notes = append(notes, at("local delete failed: "+err.Error()))
			} else {
				done = append(done, "local")
			}
		}
		if a.Git.HasRemote(r.dir) {
			ctx, cancel := context.WithTimeout(context.Background(), BranchDeleteTimeout)
			ok, err := a.Git.DeleteRemoteBranch(ctx, r.dir, b)
			cancel()
			switch {
			case errors.Is(err, git.ErrRemoteMoved):
				notes = append(notes, at("kept "+b+" on origin, "+err.Error()))
			case err != nil:
				notes = append(notes, at("origin delete failed: "+err.Error()))
			case ok:
				done = append(done, "origin")
			}
		}
		if len(done) > 0 {
			deleted = append(deleted, at(strings.Join(done, ", ")))
		}
	}
	var out []string
	if len(deleted) > 0 {
		out = append(out, fmt.Sprintf("deleted branch %s (%s)", b, strings.Join(deleted, " · ")))
	}
	return strings.Join(append(out, notes...), " · ")
}

// isBaseBranch reports whether b is a branch stories of repo start from, so closing a story on
// it must not delete it.
func (a *App) isBaseBranch(p *state.Project, repo, base, b string) bool {
	if slices.Contains(defaultBranches, b) || b == p.BaseOverride || b == strings.TrimPrefix(base, "origin/") {
		return true
	}
	head, err := a.Git.OriginHead(repo)
	return err == nil && b == head
}

// stopStory kills the story's running sessions and shells.
func (a *App) stopStory(p *state.Project, name string) error {
	live, err := a.Live(p)
	if err != nil {
		return err
	}
	for _, n := range p.SessionsOf(name) {
		if id, ok := live[n]; ok {
			_ = a.Tmux.KillSession(id) // may exit on its own meanwhile
		}
	}
	shells, err := a.Shells(p)
	if err != nil {
		return err
	}
	for _, sh := range shells {
		if sh.Story == name {
			_ = a.Tmux.KillSession(sh.ID) // may exit on its own meanwhile
		}
	}
	return nil
}

// forgetStory drops the story and its sessions, with their uploads and artifacts, from the state.
func (a *App) forgetStory(p *state.Project, name string) error {
	for _, n := range p.SessionsOf(name) {
		a.removeSessionFiles(p.Name, n)
		delete(p.Sessions, n)
	}
	_ = os.Remove(a.Cfg.ReviewFile(p.Name, name))  // its review comments go with it
	_ = os.Remove(a.Cfg.ExplainFile(p.Name, name)) // and its explanations
	delete(p.Stories, name)
	return a.SaveProject(p)
}

func shellJoin(args []string) string {
	out := make([]string, len(args))
	for i, a := range args {
		if strings.ContainsAny(a, " '\"$`\\") {
			out[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		} else {
			out[i] = a
		}
	}
	return strings.Join(out, " ")
}
