// Package api holds the JSON shapes shared by `bx serve` and `bx`. It has no logic.
package api

import (
	"github.com/dinhhuy258/bx/internal/settings"
	"github.com/dinhhuy258/bx/internal/state"
	"github.com/dinhhuy258/bx/internal/ui"
)

// Snapshot is GET /api/projects/{name}.
type Snapshot struct {
	Project       *state.Project       `json:"project"`        // the stored record (stories, sessions)
	Live          ui.Live              `json:"live"`           // agent states, diff, branch, dirty count, server time
	SessionStates map[int]SessionState `json:"session_states"` // derived from Project, Live and tmux; read by the web client
}

// SessionState is the derived state of one session, keyed by session number in Snapshot.
// State is "running", "waiting", "idle", "done" or "gone"; Text is the TUI's row label.
type SessionState struct {
	State string `json:"state"`
	Text  string `json:"text"`
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
	Num   int      `json:"num,omitempty"`   // yes, resume, rename
	Story string   `json:"story,omitempty"` // sessions ("" = chat)
	Title string   `json:"title,omitempty"` // sessions, rename
	Name  string   `json:"name,omitempty"`  // stories, stories/close, stories/repos
	Force bool     `json:"force,omitempty"` // stories/close: discard uncommitted files; stories/repos: add dirty originals
	Repos []string `json:"repos,omitempty"` // stories, stories/repos in a workspace: repositories, relative to it
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
	Num    int    `json:"num,omitempty"`
	Name   string `json:"name,omitempty"`
	Error  string `json:"error,omitempty"`
	Dirty  int    `json:"dirty,omitempty"`
	Detail string `json:"detail,omitempty"` // the dirty files by repository, "api: 3, web: 1"
}

// Settings is GET /api/settings and the reply of PUT: every section and field the server offers,
// and the values that are set (a missing key is the default). The body of PUT has only Values,
// the changed keys; "" puts a key back to its default.
type Settings struct {
	Sections []settings.Section `json:"sections,omitempty"`
	Values   map[string]string  `json:"values"`
}

// Control is a text frame from the client. Binary frames are terminal bytes in both directions.
type Control struct {
	Type  string `json:"type"` // client: "resize", "scroll" or "copy" (enter copy-mode); server: "copy_end"
	Cols  int    `json:"cols,omitempty"`
	Rows  int    `json:"rows,omitempty"`
	Lines int    `json:"lines,omitempty"` // scroll: <0 back into history, >0 toward live, 0 back to live
}

// SyncFile is one file of a Claude sync: in a plan request its hash and mode; in an apply
// request its mode and content (base64 on the wire). Path is relative to the Claude config dir,
// slash-separated, e.g. "skills/brainstorm/SKILL.md".
type SyncFile struct {
	Path    string `json:"path"`
	SHA256  string `json:"sha256,omitempty"`
	Exec    bool   `json:"exec,omitempty"`
	Content []byte `json:"content,omitempty"`
}

// SyncPlanRequest is the body of POST /api/claude/sync/plan: every Mac file of the synced
// folders (skills, agents, commands, rules).
type SyncPlanRequest struct {
	Files []SyncFile `json:"files"`
}

// SyncPlan is its reply. Dir is the server's Claude config dir, for display. Items is sorted
// by name.
type SyncPlan struct {
	Dir   string     `json:"dir"`
	Items []SyncItem `json:"items"`
}

// SyncItem is one item of a plan: "skills/<name>" or "<agents|commands|rules>/<file or
// folder>". Status is "new" (no file of it on the server), "changed", "same", "removed" (bx
// synced it, the Mac no longer has it, the server still does) or "server-only" (only on the
// server, never synced by bx: always kept). Add, Update and Delete are its paths a sync would
// touch. Error says why it can't sync (a symlink or a non-regular file on the server).
// Synced says bx synced it before: it is in the server's record.
type SyncItem struct {
	Name   string   `json:"name"`
	Status string   `json:"status"`
	Add    []string `json:"add,omitempty"`
	Update []string `json:"update,omitempty"`
	Delete []string `json:"delete,omitempty"`
	Error  string   `json:"error,omitempty"`
	Synced bool     `json:"synced,omitempty"`
}

// SyncApplyRequest is the body of POST /api/claude/sync. Items is the scope of the sync;
// nothing outside it is touched. Paths is every Mac file of Items (the server records those
// that exist after the apply), Files the adds and updates with content, Delete the record
// paths of Items the Mac no longer has.
type SyncApplyRequest struct {
	Items  []string   `json:"items"`
	Paths  []string   `json:"paths"`
	Files  []SyncFile `json:"files"`
	Delete []string   `json:"delete"`
}

// SyncResult is its reply.
type SyncResult struct {
	Added   int `json:"added"`
	Updated int `json:"updated"`
	Deleted int `json:"deleted"`
}
