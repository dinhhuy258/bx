package prwatch

import (
	"fmt"
	"strings"

	"github.com/dinhhuy258/bx/internal/ui"
)

// Event is one notification: a title and a body. Thread is the thread a comment event is on.
type Event struct {
	Title, Body string
	Thread      string
}

// Where names a PR in messages: its story and, in a workspace, its repo folder.
type Where struct{ Story, Repo string }

func (w Where) of(n int) string {
	if w.Repo != "" {
		return fmt.Sprintf("%s · %s #%d", w.Story, w.Repo, n)
	}
	return fmt.Sprintf("%s #%d", w.Story, n)
}

// Events are the changes from old to cur worth a notification. The first look at a PR (old
// has none) and anything me did are silent.
func Events(old, cur ui.PRInfo, me string, w Where) []Event {
	if !old.Has() || !cur.Has() || old.Number != cur.Number {
		return nil
	}
	at := w.of(cur.Number)
	var out []Event
	add := func(title, body string) { out = append(out, Event{Title: title + " · " + at, Body: body}) }
	if old.State == ui.PROpen && cur.State == ui.PRMerged {
		add("Merged", "Close the story to remove its worktree")
		return out
	}
	if old.State == ui.PROpen && cur.State == ui.PRClosed {
		add("Closed", "Closed without merging")
		return out
	}
	if cur.State != ui.PROpen {
		return nil
	}
	if cur.Failed() && !old.Failed() {
		names := make([]string, 0)
		for _, c := range cur.FailedChecks() {
			names = append(names, c.Name)
		}
		add("✗ CI failed", strings.Join(names, ", "))
	}
	if (old.Failed() || old.LastCI == ui.CheckFail) && cur.Passed() {
		add("✓ CI fixed", "Every check passed")
	}
	if old.Decision != cur.Decision {
		switch cur.Decision {
		case "CHANGES_REQUESTED":
			add("✎ Changes requested", "")
		case "APPROVED":
			add("✓ Approved", "")
		}
	}
	if old.Merge != "CLEAN" && cur.Merge == "CLEAN" && cur.Passed() {
		add("✓ Ready to merge", "Checks passed")
	}
	if old.Merge != "DIRTY" && cur.Merge == "DIRTY" {
		add("⇄ Conflicts with "+cur.Base, "Sync or ask Claude to rebase")
	}
	if e, ok := newComments(old, cur, me, at); ok {
		out = append(out, e)
	}
	return out
}

func newComments(old, cur ui.PRInfo, me, at string) (Event, bool) {
	seen := map[string]bool{}
	for _, t := range old.Threads {
		for _, c := range t.Comments {
			seen[fmt.Sprintf("%s/%s/%d", t.ID, c.Author, c.At)] = true
		}
	}
	type hit struct {
		t ui.PRThread
		c ui.PRComment
	}
	var hits []hit
	for _, t := range cur.Threads {
		for _, c := range t.Comments {
			if strings.EqualFold(c.Author, me) || seen[fmt.Sprintf("%s/%s/%d", t.ID, c.Author, c.At)] {
				continue
			}
			hits = append(hits, hit{t, c})
		}
	}
	switch len(hits) {
	case 0:
		return Event{}, false
	case 1:
		h := hits[0]
		first, _, _ := strings.Cut(strings.TrimSpace(h.c.Body), "\n")
		return Event{Title: "💬 " + h.c.Author + " commented · " + at, Body: h.t.Where() + " · " + first, Thread: h.t.ID}, true
	}
	var who []string
	for _, h := range hits {
		if !containsFold(who, h.c.Author) {
			who = append(who, h.c.Author)
		}
	}
	return Event{Title: fmt.Sprintf("💬 %d new comments · %s", len(hits), at), Body: "From " + strings.Join(who, ", ")}, true
}

func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}
