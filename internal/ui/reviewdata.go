package ui

// ReviewData is GET /api/projects/{name}/stories/{story}/review: the story's diff since its base
// with the review comments placed on it. Total counts every changed file; Files may stop
// earlier (Note says so).
type ReviewData struct {
	Base  string     `json:"base"`
	Note  string     `json:"note,omitempty"`
	Total int        `json:"total"`
	Files []DiffFile `json:"files"`
	ReviewComments
}

// ReviewComments is the comment list with the version it was read at. PUT sends it back
// whole; a version that is no longer the stored one is refused (another device saved first).
type ReviewComments struct {
	Version  int       `json:"version"`
	Comments []Comment `json:"comments"`
}

// DiffFile is one changed file. Status is added, deleted, renamed, modified, or gone (a file
// that only has comments and is no longer changed). Folded says why it has no hunks.
type DiffFile struct {
	Path   string `json:"path"`
	Old    string `json:"old,omitempty"` // a renamed file's old path
	Status string `json:"status"`
	Add    int    `json:"add"`
	Del    int    `json:"del"`
	Folded string `json:"folded,omitempty"`
	Hunks  []Hunk `json:"hunks,omitempty"`
}

// Hunk is one @@ block.
type Hunk struct {
	Header string    `json:"header"`
	Rows   []DiffRow `json:"rows"`
}

// DiffRow is one line of a hunk: Kind ctx, add or del. Old is its line in the base (0 for an
// added line), New its line in the worktree (0 for a removed line).
type DiffRow struct {
	Kind string `json:"kind"`
	Old  int    `json:"old"`
	New  int    `json:"new"`
	Text string `json:"text"`
}

// Comment is a review comment on one line. Side new is a line of the worktree, old a removed
// line. Code is the line's text when the comment was made. SentTo and SentAt are set once the
// comment was pasted into a session; it is then read-only. Outdated says the line is gone
// from the current diff (set by the server on read, ignored on write).
type Comment struct {
	ID       string `json:"id"`
	File     string `json:"file"`
	Side     string `json:"side"`
	Line     int    `json:"line"`
	Code     string `json:"code"`
	Body     string `json:"body"`
	SentTo   int    `json:"sent_to,omitempty"`
	SentAt   int64  `json:"sent_at,omitempty"`
	Outdated bool   `json:"outdated,omitempty"`
}
