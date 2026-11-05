package prwatch

import (
	"strings"

	"github.com/dinhhuy258/bx/internal/ui"
)

// Choose picks a branch's PR in repository slug: its newest open one (MoreOpen counts the
// others), else its last merged or closed one when that PR's head commit is in the branch
// (inBranch), so a new story that reuses an old branch name does not show the old PR. A PR
// whose head is another repository (a fork with the same branch name) is never chosen.
func Choose(f Found, slug string, inBranch func(oid string) bool) ui.PRInfo {
	var open []PR
	for _, p := range f.Open {
		if strings.EqualFold(p.Slug, slug) {
			open = append(open, p)
		}
	}
	if len(open) > 0 {
		out := open[0].PRInfo
		out.MoreOpen = len(open) - 1
		return out
	}
	if l := f.Last; l != nil && strings.EqualFold(l.Slug, slug) && inBranch(l.HeadOID) {
		return l.PRInfo
	}
	return ui.PRInfo{Missing: "no PR"}
}

// Carry fills what cur takes from old, the last look at the same PR: LastCI, the last
// finished CI outcome, so a pending look between a failure and a pass still reads as fixed;
// and the merge state when GitHub answers UNKNOWN while it recomputes, so CLEAN → UNKNOWN →
// CLEAN does not announce "ready" twice.
func Carry(old, cur ui.PRInfo) ui.PRInfo {
	if !cur.Has() {
		return cur
	}
	same := old.Has() && old.Number == cur.Number
	switch {
	case cur.Failed():
		cur.LastCI = ui.CheckFail
	case cur.Passed():
		cur.LastCI = ui.CheckPass
	case same:
		cur.LastCI = old.LastCI
	}
	if cur.Merge == "UNKNOWN" && same {
		cur.Merge = old.Merge
	}
	return cur
}

// Badge sums a story's PRs for its row: the worst check state, the threads waiting for me,
// and whether any PR asks for changes, conflicts, is behind, or (all of them) is ready.
func Badge(prs []ui.PRInfo, me string) ui.PRBadge {
	var b ui.PRBadge
	ready, open := true, 0
	for _, p := range prs {
		if !p.Has() {
			continue
		}
		b.Count++
		b.Number = p.Number
		switch p.State {
		case ui.PRMerged:
			b.Merged++
			continue
		case ui.PRClosed:
			b.Closed++
			continue
		}
		open++
		b.Fail = b.Fail || p.Failed()
		b.Pending = b.Pending || p.Pending()
		b.Threads += p.OpenThreads(me)
		b.Changes = b.Changes || p.Decision == "CHANGES_REQUESTED"
		b.Review = b.Review || p.Decision == "REVIEW_REQUIRED"
		b.Conflict = b.Conflict || p.Merge == "DIRTY"
		b.Behind = b.Behind || p.Merge == "BEHIND"
		ready = ready && p.Merge == "CLEAN"
	}
	if b.Count != 1 {
		b.Number = 0
	}
	if open > 0 && !b.Fail && !b.Pending {
		b.Pass = true
	}
	b.Ready = open > 0 && ready && b.Pass
	if b.Fail || b.Pending {
		b.Pass = false
	}
	return b
}
