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
	num := p.AddSession(&state.Session{Story: story, UUID: id, Title: title, Created: time.Now()})
	if err := a.launch(p, num, claude.StartArgs(id, title, false)); err != nil {
		delete(p.Sessions, num)
		p.NextNum--
		return 0, err
	}
	return num, a.SaveProject(p)
}

// launch runs args in a new tmux session for session num, in its story's folder.
func (a *App) launch(p *state.Project, num int, args []string) error {
	_, err := a.Tmux.NewSession(TmuxName(p.Name, num), a.Dir(p, p.Sessions[num].Story), shellJoin(args))
	return err
}

// Resume restarts a session that is not running with `claude --resume <uuid>`.
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
	if err := a.launch(p, num, claude.StartArgs(s.UUID, "", true)); err != nil {
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
		if err := a.launch(p, n, claude.StartArgs(s.UUID, "", true)); err != nil {
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

// DirtyError refuses to close a story whose worktree has uncommitted files.
type DirtyError struct {
	Story string
	Files int
}

func (e *DirtyError) Error() string {
	return fmt.Sprintf("story %q has %d uncommitted files", e.Story, e.Files)
}

// CloseStory stops the story's sessions, removes its worktree and forgets it. Without force
// a dirty worktree is refused with *DirtyError; force discards the changes.
func (a *App) CloseStory(p *state.Project, name string, force bool) error {
	if _, ok := p.Stories[name]; !ok {
		return fmt.Errorf("no story %q", name)
	}
	dir := a.Cfg.WorktreeDir(p.Name, name)
	if !force {
		if n, err := a.Git.DirtyCount(dir); err == nil && n > 0 {
			return &DirtyError{Story: name, Files: n}
		}
	}
	live, err := a.Live(p)
	if err != nil {
		return err
	}
	for _, n := range p.SessionsOf(name) {
		if id, ok := live[n]; ok {
			_ = a.Tmux.KillSession(id) // may exit on its own meanwhile
		}
	}
	if _, err := os.Stat(dir); err == nil {
		if err := a.Git.WorktreeRemove(p.Path, dir, force); err != nil {
			return err
		}
	}
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
