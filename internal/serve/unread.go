package serve

import "github.com/dinhhuy258/bx/internal/claude"

// unread tracks, by session uuid, the sessions that finished a turn (running or waiting → idle)
// while no client had their stream open, until one opens it. It lives in memory: a restart
// forgets it, so a session is never wrongly marked. Every method is called with Server.mu held.
type unread struct {
	last     map[string]claude.State // the state at the previous `claude agents`
	marked   map[string]bool         // finished a turn nobody watched
	watching map[string]int          // open session streams
}

func newUnread() unread {
	return unread{last: map[string]claude.State{}, marked: map[string]bool{}, watching: map[string]int{}}
}

// observe compares a fresh agents list with the last one. Running or waiting → idle with no
// open stream marks a session; working again clears it. A session first seen idle (just
// started or resumed) is not marked, and one whose process left the list is dropped.
func (u *unread) observe(agents []claude.Agent) {
	now := make(map[string]claude.State, len(agents))
	for _, a := range agents {
		if a.SessionID == "" {
			continue
		}
		st := a.State()
		now[a.SessionID] = st
		prev, known := u.last[a.SessionID]
		switch {
		case st != claude.Idle:
			delete(u.marked, a.SessionID)
		case known && prev != claude.Idle && u.watching[a.SessionID] == 0:
			u.marked[a.SessionID] = true
		}
	}
	for id := range u.marked {
		if _, ok := now[id]; !ok {
			delete(u.marked, id)
		}
	}
	u.last = now
}

// open counts a session stream; opening one reads the session.
func (u *unread) open(id string) {
	u.watching[id]++
	delete(u.marked, id)
}

// close uncounts a session stream.
func (u *unread) close(id string) {
	u.watching[id]--
	if u.watching[id] <= 0 {
		delete(u.watching, id)
	}
}

// snapshot copies the marked set, for ui.Live.Unread.
func (u *unread) snapshot() map[string]bool {
	out := make(map[string]bool, len(u.marked))
	for id := range u.marked {
		out[id] = true
	}
	return out
}
