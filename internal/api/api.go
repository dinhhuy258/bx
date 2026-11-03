// Package api holds the JSON shapes shared by `bx serve` and `bx`. It has no logic.
package api

import (
	"time"

	"github.com/dinhhuy258/bx/internal/claude"
	"github.com/dinhhuy258/bx/internal/settings"
	"github.com/dinhhuy258/bx/internal/state"
	"github.com/dinhhuy258/bx/internal/ui"
)

// Snapshot is GET /api/projects/{name}.
type Snapshot struct {
	Project       *state.Project       `json:"project"`        // the stored record (stories, sessions)
	Live          ui.Live              `json:"live"`           // agent states, diff, branch, dirty count, server time
	SessionStates map[int]SessionState `json:"session_states"` // derived from Project, Live and tmux; read by the web client
	Shells        []Shell              `json:"shells"`         // the running shells, by story then number
	Dirs          map[string]string    `json:"dirs"`           // the folder of each group: "" (chats) and every story
}

// Shell is one running shell of a project, from its tmux options. It is ui.Shell, which the
// list pane renders.
type Shell = ui.Shell

// MCP is GET /api/mcp?tui=1: the global MCP servers and every project with its own. Only the
// TUI may ask: a server's config may hold a token.
type MCP = ui.MCP

// GitAccounts is GET /api/git/accounts?tui=1: the GitHub accounts, gh's logins and the origin
// of every project. PUT with the whole Config saves it and returns the same. Only the TUI may
// ask.
type GitAccounts = ui.GitAccounts

// MCPDoc is GET and PUT /api/mcp/doc?tui=1&project=<name> ("" = the global servers): the
// servers as the .mcp.json text the TUI edits. PUT with empty text removes them. A refused PUT
// is a 400 whose Result has the error and its Line.
type MCPDoc struct {
	Text string `json:"text"`
}

// SessionState is the derived state of one session, keyed by session number in Snapshot.
// State is "starting", "running", "waiting", "idle", "done" or "gone"; Text is the TUI's row
// label. Unread marks an idle session that finished a turn nobody has opened since.
type SessionState struct {
	State  string `json:"state"`
	Text   string `json:"text"`
	Unread bool   `json:"unread,omitempty"`
}

// ProjectSummary is one row of GET /api/projects.
type ProjectSummary struct {
	Name            string `json:"name"`
	Running         int    `json:"running"`
	Waiting         int    `json:"waiting"`
	WaitingSessions []int  `json:"waiting_sessions,omitempty"` // numbers, ascending
	Unread          int    `json:"unread"`                     // idle sessions that finished a turn nobody has opened since
	BaseOverride    string `json:"base_override,omitempty"`    // the branch new stories start from, set in settings › git
}

// Action is the body of every POST /api/projects/{name}/<action>, shells, shells/close and
// shells/rename included.
type Action struct {
	Num   int      `json:"num,omitempty"`   // yes, resume, rename, stop, delete
	Story string   `json:"story,omitempty"` // sessions ("" = chat), shells, shells/close, shells/rename ("" = chats)
	Title string   `json:"title,omitempty"` // sessions, rename, shells/rename ("" = show the command)
	K     int      `json:"k,omitempty"`     // shells/close, shells/rename: the shell number
	Name  string   `json:"name,omitempty"`  // stories (new branch: a name, or a full branch with "/"), stories/close, stories/repos
	Force bool     `json:"force,omitempty"` // stories/close: discard uncommitted files; stories/repos: add dirty originals; delete: kill a live session first
	Repos []string `json:"repos,omitempty"` // stories, stories/repos in a workspace: repositories, relative to it
	// stories: Type is the branch type of a new branch (feat, fix, chore, hotfix, refactor,
	// docs, none; "" = the last one); Source is "" or "new", "branch" or "pr"; Branch the
	// existing branch, PR the pull request and PRRepo its workspace repository; UseExisting
	// uses a new branch that exists (after a 409 with Exists).
	Type        string `json:"type,omitempty"`
	Source      string `json:"source,omitempty"`
	Branch      string `json:"branch,omitempty"`
	PR          int    `json:"pr,omitempty"`
	PRRepo      string `json:"pr_repo,omitempty"`
	UseExisting bool   `json:"use_existing,omitempty"`
	Base        string `json:"base,omitempty"` // base: the base override; "" removes it
}

// NewProject is the body of POST /api/projects.
type NewProject struct {
	Kind string `json:"kind"`           // "folder", "clone" or "new"
	Name string `json:"name,omitempty"` // new
	Path string `json:"path,omitempty"` // folder: absolute path on the server
	Repo string `json:"repo,omitempty"` // clone: owner/repo
}

// Result is the reply of every POST. Num is the new session (POST sessions), Story and K the
// new shell (POST shells), Name the new project (POST /api/projects), Dirty the uncommitted
// files when a close is refused (409).
type Result struct {
	Num    int    `json:"num,omitempty"`
	K      int    `json:"k,omitempty"`
	Name   string `json:"name,omitempty"`
	Error  string `json:"error,omitempty"`
	Dirty  int    `json:"dirty,omitempty"`
	Detail string `json:"detail,omitempty"` // the dirty files by repository, "api: 3, web: 1"
	// Running is set when a delete is refused (409) because the session's tmux session is
	// alive; resend with force to kill it.
	Running bool `json:"running,omitempty"`
	// POST stories: Story is the new story, Num its first session, Message the success line
	// and SessionError why that session did not start (the story is kept). POST stories/close:
	// Message is what happened to the branch, "deleted branch feat/x (local, origin)".
	Story        string `json:"story,omitempty"`
	Message      string `json:"message,omitempty"`
	SessionError string `json:"session_error,omitempty"`
	// Exists is set when a new branch is refused (409) because it exists: "local", "origin"
	// or "both"; Branch is that branch. Resend with use_existing to use it.
	Exists string `json:"exists,omitempty"`
	Branch string `json:"branch,omitempty"`
	// Line is the line of a refused MCP document (PUT /api/mcp/doc), 0 when unknown.
	Line int `json:"line,omitempty"`
}

// StoryInfo is GET /api/projects/{name}/storyinfo, for the new story prompt: the base a new
// branch starts from (with the override; "each repo's default" in a workspace whose repos
// differ), the detected one, the override, the last type and every type.
type StoryInfo struct {
	Base     string   `json:"base"`
	Detected string   `json:"detected"`
	Override string   `json:"override,omitempty"`
	LastType string   `json:"last_type,omitempty"`
	Types    []string `json:"types"`
}

// Branch is one branch of GET /api/projects/{name}/branches: local, on origin or both, Time
// its last commit (unix seconds), Repos the workspace repositories that have it. CheckedOut
// says a worktree has it, so no story can: the existing branch pickers leave it out.
type Branch struct {
	Name       string   `json:"name"`
	Local      bool     `json:"local,omitempty"`
	Remote     bool     `json:"remote,omitempty"`
	CheckedOut bool     `json:"checkedOut,omitempty"`
	Time       int64    `json:"time"`
	Repos      []string `json:"repos,omitempty"`
}

// BranchList is its reply, newest first. Note says a fetch failed.
type BranchList struct {
	Branches []Branch `json:"branches"`
	Note     string   `json:"note,omitempty"`
}

// PR is one pull request of GET /api/projects/{name}/prs. Owner is its head repository's
// owner, Repo its workspace repository ("" in a single-repo project), Slug that repository's
// owner/repo; Fork says the head is in another repository.
type PR struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Author string `json:"author"`
	Branch string `json:"branch"`
	Owner  string `json:"owner,omitempty"`
	Repo   string `json:"repo,omitempty"`
	Slug   string `json:"slug,omitempty"`
	State  string `json:"state,omitempty"`
	Fork   bool   `json:"fork,omitempty"`
}

// PRList is its reply. Errors are the gh failures (per repository in a workspace); Direct
// says the query named one pull request (#12 or its URL).
type PRList struct {
	PRs    []PR     `json:"prs"`
	Errors []string `json:"errors,omitempty"`
	Direct bool     `json:"direct,omitempty"`
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
	// client: "resize", "scroll", "copy" (enter copy-mode) or "page_seen" (the page was opened
	// or dismissed); server: "copy_end", "page" (Claude wrote a page; also sent when the stream
	// opens, until a client sends page_seen) or "page_seen" (another client saw it)
	Type  string `json:"type"`
	Cols  int    `json:"cols,omitempty"`
	Rows  int    `json:"rows,omitempty"`
	Lines int    `json:"lines,omitempty"` // scroll: <0 back into history, >0 toward live, 0 back to live
	// page: the page, and whether this is the first write of its name ("New page", else "Updated")
	Page *Artifact `json:"page,omitempty"`
	New  bool      `json:"new,omitempty"`
}

// PageHook is the body of POST /api/hooks/page, sent by `bx page-hook` after Claude writes or
// edits a page: the Claude session and the file. New is false when the hook knows the file
// existed (an Edit, or a Write that updated it).
type PageHook struct {
	SessionID string `json:"session_id"`
	Path      string `json:"path"`
	New       bool   `json:"new"`
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

// UploadRequest is the body of POST /api/projects/{name}/sessions/{num}/files: one file, its
// content base64 in JSON.
type UploadRequest struct {
	Name    string `json:"name"`
	Content []byte `json:"content"`
}

// UploadResult is its reply: the absolute path of the file on the server.
type UploadResult struct {
	Path string `json:"path"`
}

// Artifact is one HTML page in a session's artifacts folder. URL is "" when the file server
// is off.
type Artifact struct {
	Name     string    `json:"name"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
	URL      string    `json:"url,omitempty"`
}

// ArtifactList is GET /api/projects/{name}/sessions/{num}/artifacts, newest first.
type ArtifactList struct {
	Files []Artifact `json:"files"`
}

// SlashCommandList is GET /api/projects/{name}/sessions/{num}/slash-commands: what can follow
// "/" in the session's prompt, sorted by name.
type SlashCommandList struct {
	Commands []claude.SlashCommand `json:"commands"`
}

// CleanupItem is one thing cleanup can remove: Kind "orphan" (a bx-* tmux session with no
// record) or "done" (a done session's record). ID names it in a CleanupRequest.
type CleanupItem struct {
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	Label  string `json:"label"`
	Detail string `json:"detail"`
}

// Cleanup is the reply of GET /api/cleanup: the orphans, then the done sessions of every
// project.
type Cleanup struct {
	Items []CleanupItem `json:"items"`
}

// CleanupRequest is the body of POST /api/cleanup: the ids to remove.
type CleanupRequest struct {
	IDs []string `json:"ids"`
}

// CleanupResult is its reply: how many items were removed.
type CleanupResult struct {
	Removed int `json:"removed"`
}

// The review types live in ui, which draws the review screen and cannot import api.
type (
	Review         = ui.ReviewData
	ReviewComments = ui.ReviewComments
	DiffFile       = ui.DiffFile
	Hunk           = ui.Hunk
	DiffRow        = ui.DiffRow
	Comment        = ui.Comment
)

// ReviewSend is POST …/review/send: the session to paste into, or New for a new one.
type ReviewSend struct {
	Session int  `json:"session,omitempty"`
	New     bool `json:"new,omitempty"`
}
