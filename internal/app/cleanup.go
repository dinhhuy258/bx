package app

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/dinhhuy258/bx/internal/state"
)

// CleanupItem is something cleanup can remove. Kind is "orphan" (a bx-* tmux session with no
// record) or "done" (a done session's record). ID names it in CleanupApply.
type CleanupItem struct {
	Kind, ID, Label, Detail string
}

// CleanupScan lists the orphan tmux sessions, sorted by name, then the done sessions of every
// project, projects by name and numbers ascending. A tmux name is never parsed back into a
// project and number: TmuxName replaces "." and ":", and project names contain dashes, so
// bx-home-network-12 is ambiguous. Instead every expected name is built forward from the
// records, and any bx-* name outside that set is an orphan.
func (a *App) CleanupScan(ps []*state.Project) ([]CleanupItem, error) {
	expected := map[string]bool{}
	for _, p := range ps {
		for n := range p.Sessions {
			expected[TmuxName(p.Name, n)] = true
		}
	}
	ss, err := a.Tmux.Sessions()
	if err != nil {
		return nil, err
	}
	var orphans []string
	for _, se := range ss {
		if strings.HasPrefix(se.Name, "bx-") && !expected[se.Name] {
			orphans = append(orphans, se.Name)
		}
	}
	sort.Strings(orphans)
	var out []CleanupItem
	for _, name := range orphans {
		out = append(out, CleanupItem{Kind: "orphan", ID: "tmux:" + name, Label: name + " · no record", Detail: "no record"})
	}
	sorted := append([]*state.Project(nil), ps...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	for _, p := range sorted {
		nums := make([]int, 0, len(p.Sessions))
		for n, s := range p.Sessions {
			if s.Done {
				nums = append(nums, n)
			}
		}
		sort.Ints(nums)
		for _, n := range nums {
			s := p.Sessions[n]
			group := s.Story
			if s.IsChat() {
				group = "chats"
			}
			out = append(out, CleanupItem{Kind: "done", ID: doneID(p.Name, n),
				Label: fmt.Sprintf("%s · %s · [%d] %s · done", p.Name, group, n, s.Title), Detail: "done"})
		}
	}
	return out, nil
}

// CleanupApply removes the items named by ids and returns how many it removed. It scans again
// and skips an id that is no longer in the scan (already deleted, or no longer an orphan). An
// orphan's tmux session is killed; a done session is deleted as DeleteSession does. It tries
// every item and returns the errors joined.
func (a *App) CleanupApply(ps []*state.Project, ids []string) (int, error) {
	items, err := a.CleanupScan(ps)
	if err != nil {
		return 0, err
	}
	present := make(map[string]CleanupItem, len(items))
	for _, it := range items {
		present[it.ID] = it
	}
	removed := 0
	var errs []error
	for _, id := range ids {
		it, ok := present[id]
		if !ok {
			continue
		}
		delete(present, id) // an id sent twice is removed once
		var err error
		switch it.Kind {
		case "orphan":
			err = a.killOrphan(strings.TrimPrefix(id, "tmux:"))
		case "done":
			err = a.deleteDone(ps, id)
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		removed++
	}
	return removed, errors.Join(errs...)
}

// killOrphan kills the tmux session called name.
func (a *App) killOrphan(name string) error {
	ss, err := a.Tmux.Sessions()
	if err != nil {
		return err
	}
	for _, se := range ss {
		if se.Name == name {
			return a.Tmux.KillSession(se.ID)
		}
	}
	return fmt.Errorf("no tmux session %s", name)
}

// deleteDone deletes the session whose cleanup id is id, found by building every session's id
// forward, as CleanupScan does.
func (a *App) deleteDone(ps []*state.Project, id string) error {
	for _, p := range ps {
		for n := range p.Sessions {
			if doneID(p.Name, n) == id {
				return a.DeleteSession(p, n, true)
			}
		}
	}
	return fmt.Errorf("no session for %s", id)
}

// doneID is the cleanup id of a done session.
func doneID(project string, num int) string { return fmt.Sprintf("session:%s:%d", project, num) }
