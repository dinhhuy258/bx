// Package api holds the JSON shapes shared by `bx serve` and `bx`. It has no logic.
package api

import (
	"github.com/dinhhuy258/bx/internal/state"
	"github.com/dinhhuy258/bx/internal/ui"
)

// Snapshot is GET /api/projects/{name}.
type Snapshot struct {
	Project *state.Project `json:"project"` // the stored record (stories, sessions)
	Live    ui.Live        `json:"live"`    // agent states, diff, branch, dirty count, server time
}

// ProjectSummary is one row of GET /api/projects.
type ProjectSummary struct {
	Name            string `json:"name"`
	Running         int    `json:"running"`
	Waiting         int    `json:"waiting"`
	WaitingSessions []int  `json:"waiting_sessions,omitempty"` // numbers, ascending; for the a key
}

// Action is the body of every POST /api/projects/{name}/<action>.
type Action struct {
	Num   int    `json:"num,omitempty"`   // yes, resume, rename
	Story string `json:"story,omitempty"` // sessions ("" = chat)
	Title string `json:"title,omitempty"` // sessions, rename
	Name  string `json:"name,omitempty"`  // stories, stories/close
	Force bool   `json:"force,omitempty"` // stories/close: discard uncommitted files
}

// NewProject is the body of POST /api/projects.
type NewProject struct {
	Kind string `json:"kind"`           // "folder", "clone" or "new"
	Name string `json:"name,omitempty"` // new
	Path string `json:"path,omitempty"` // folder: absolute path on the server
	Repo string `json:"repo,omitempty"` // clone: owner/repo
}

// Result is the reply of every POST. Num is the new session (POST sessions), Name the new
// project (POST /api/projects), Dirty the uncommitted files when a close is refused (409).
type Result struct {
	Num   int    `json:"num,omitempty"`
	Name  string `json:"name,omitempty"`
	Error string `json:"error,omitempty"`
	Dirty int    `json:"dirty,omitempty"`
}

// Control is a text frame from the client. Binary frames are terminal bytes in both directions.
type Control struct {
	Type string `json:"type"` // "resize"
	Cols int    `json:"cols,omitempty"`
	Rows int    `json:"rows,omitempty"`
}
