package app

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/dinhhuy258/bx/internal/claude"
	"github.com/dinhhuy258/bx/internal/state"
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
	if err := a.launch(p, num, claude.StartArgs(id, false, a.options(p, s))); err != nil {
		delete(p.Sessions, num)
		p.NextNum--
		return 0, err
	}
	return num, a.SaveProject(p)
}

// options are the settings a session started with, for its command line. A session of a
// workspace story also gets the guard hook, which keeps edits out of the original repositories.
func (a *App) options(p *state.Project, s *state.Session) claude.Options {
	o := claude.Options{Model: s.Model, Effort: s.Effort, PermissionMode: s.PermissionMode}
	if p.IsWorkspace() && s.Story != "" && a.Self != "" {
		o.Settings = GuardSettings(a.Self, a.Dir(p, s.Story))
	}
	return o
}

// launch runs args in a new tmux session for session num, in its story's folder.
func (a *App) launch(p *state.Project, num int, args []string) error {
	_, err := a.Tmux.NewSession(TmuxName(p.Name, num), a.Dir(p, p.Sessions[num].Story), shellJoin(args))
	return err
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
	if err := a.launch(p, num, claude.StartArgs(s.UUID, true, a.options(p, s))); err != nil {
		return err
	}
	s.Done = false
	return a.SaveProject(p)
}

// ResumeAll restarts every session that is not done, has no tmux session, and is not live in
// Claude elsewhere (agents, by uuid). bx serve calls it at startup, after a reboot.
func (a *App) ResumeAll(p *state.Project, agents map[string]claude.Agent) (int, error) {
	live, err := a.Live(p)
	if err != nil {
		return 0, err
	}
	nums := make([]int, 0, len(p.Sessions))
	for n := range p.Sessions {
		nums = append(nums, n)
	}
	sort.Ints(nums)
	resumed := 0
	for _, n := range nums {
		s := p.Sessions[n]
		if _, alive := live[n]; s.Done || alive {
			continue
		}
		if _, elsewhere := agents[s.UUID]; elsewhere {
			continue
		}
		if err := a.launch(p, n, claude.StartArgs(s.UUID, true, a.options(p, s))); err != nil {
			return resumed, err
		}
		resumed++
	}
	return resumed, nil
}

// AnswerYes presses Enter in the session. Claude's permission prompt highlights "Yes" by default.
func (a *App) AnswerYes(p *state.Project, num int) error {
	id, err := a.Target(p, num)
	if err != nil {
		return err
	}
	return a.Tmux.SendKeys(id, "Enter")
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

// CloseStory stops the story's sessions, removes its worktree and forgets it. Without force
// a dirty worktree is refused with *DirtyError; force discards the changes. The branches stay.
func (a *App) CloseStory(p *state.Project, name string, force bool) error {
	if _, ok := p.Stories[name]; !ok {
		return fmt.Errorf("no story %q", name)
	}
	if p.IsWorkspace() {
		return a.closeWorkspaceStory(p, name, force)
	}
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

// stopStory kills the story's running sessions.
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
	return nil
}

// forgetStory drops the story and its sessions from the state.
func (a *App) forgetStory(p *state.Project, name string) error {
	for _, n := range p.SessionsOf(name) {
		delete(p.Sessions, n)
	}
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
