package ui

import (
	"fmt"
	"strings"
)

// ReviewData is GET /api/projects/{name}/stories/{story}/review: the story's diff since its base
// with the review comments placed on it. Total counts every changed file; Files may stop
// earlier (Note says so).
type ReviewData struct {
	Base  string     `json:"base"`
	Note  string     `json:"note,omitempty"`
	Total int        `json:"total"`
	Files []DiffFile `json:"files"`
	ReviewComments
	// Explains are Claude's answers about lines of the diff, placed like comments. They are
	// never sent. ExplainWith is the conversation the story's last ask used: a session
	// number, ExplainCodeOnly, or 0 when it never asked.
	Explains    []Explain `json:"explains"`
	ExplainWith int       `json:"explain_with,omitempty"`
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
	// Hash names the file's diff, so a mark can tell it changed; "" for a gone file, which
	// can't be marked. Viewed is "" (not viewed), FileViewed, or FileChanged (viewed, but the
	// diff changed since).
	Hash   string `json:"hash,omitempty"`
	Viewed string `json:"viewed,omitempty"`
}

// The viewed states of a file.
const (
	FileViewed  = "viewed"
	FileChanged = "changed"
)

// ReviewViewed is PUT …/review/viewed: mark Path viewed at the Hash it was shown with, or
// unmark it.
type ReviewViewed struct {
	Path   string `json:"path"`
	Hash   string `json:"hash,omitempty"`
	Viewed bool   `json:"viewed"`
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

// Comment is a review comment on one line, or on the lines StartLine to Line of one hunk when
// StartLine is set. Side new is a line of the worktree, old a removed line; a range on the new
// side holds its kept and added lines, one on the old side only removed lines. Code is the
// lines' text when the comment was made, one per line. SentTo and SentAt are set once the
// comment was pasted into a session; it is then read-only. Outdated says the line is gone
// from the current diff (set by the server on read, ignored on write).
type Comment struct {
	ID        string `json:"id"`
	File      string `json:"file"`
	Side      string `json:"side"`
	StartLine int    `json:"start_line,omitempty"` // a range's first line; 0 for one line
	Line      int    `json:"line"`
	Code      string `json:"code"`
	Body      string `json:"body"`
	SentTo    int    `json:"sent_to,omitempty"`
	SentAt    int64  `json:"sent_at,omitempty"`
	Outdated  bool   `json:"outdated,omitempty"`
}

// First is the comment's first line: StartLine for a range, else Line.
func (c Comment) First() int {
	if c.StartLine > 0 {
		return c.StartLine
	}
	return c.Line
}

// Covers says whether the comment is on line of side.
func (c Comment) Covers(side string, line int) bool {
	return c.Side == side && line >= c.First() && line <= c.Line
}

// Lines names the comment's lines for people: "line 89" or "lines 89–92".
func (c Comment) Lines() string {
	if c.First() < c.Line {
		return fmt.Sprintf("lines %d–%d", c.First(), c.Line)
	}
	return fmt.Sprintf("line %d", c.Line)
}

// Quote is the comment's code for a quote: see QuoteCode.
func (c Comment) Quote(max int) []string { return QuoteCode(c.Code, max) }

// QuoteCode is code's first max lines without the indent they all share or trailing spaces,
// then a line saying how many more there are. Blank code quotes nothing. Callers still drop
// control characters.
func QuoteCode(code string, max int) []string {
	if strings.TrimSpace(code) == "" {
		return nil
	}
	lines := strings.Split(code, "\n")
	more := 0
	if len(lines) > max {
		lines, more = lines[:max], len(lines)-max
	}
	indent, set := "", false
	for i, l := range lines {
		l = strings.TrimRight(l, " \t\r")
		lines[i] = l
		if l == "" {
			continue
		}
		lead := l[:len(l)-len(strings.TrimLeft(l, " \t"))]
		if !set {
			indent, set = lead, true
		}
		for !strings.HasPrefix(lead, indent) {
			indent = indent[:len(indent)-1]
		}
	}
	for i := range lines {
		lines[i] = strings.TrimPrefix(lines[i], indent)
	}
	if more > 0 {
		lines = append(lines, fmt.Sprintf("… %d more lines", more))
	}
	return lines
}

// The states of an explanation.
const (
	ExplainRunning = "running"
	ExplainDone    = "done"
	ExplainFailed  = "failed"
)

// ExplainCodeOnly asks without a session's conversation.
const ExplainCodeOnly = -1

// Explain is Claude's answer about lines of the diff: their place as in Comment, the question
// ("" asks what the code does and why), the session whose conversation it used (0 none) and
// its title then, the state, and the answer or the error. Started is unix seconds; TookMS is
// set when it ends. Outdated is set by the server on read, like a comment's.
type Explain struct {
	ID           string `json:"id"`
	File         string `json:"file"`
	Side         string `json:"side"`
	StartLine    int    `json:"start_line,omitempty"`
	Line         int    `json:"line"`
	Code         string `json:"code"`
	Question     string `json:"question,omitempty"`
	Session      int    `json:"session,omitempty"`
	SessionTitle string `json:"session_title,omitempty"`
	State        string `json:"state"`
	Answer       string `json:"answer,omitempty"`
	Error        string `json:"error,omitempty"`
	Started      int64  `json:"started"`
	TookMS       int64  `json:"took_ms,omitempty"`
	Outdated     bool   `json:"outdated,omitempty"`
}

// AsComment is the explanation's place as a comment, to share Comment's line helpers and
// review.Place.
func (e Explain) AsComment() Comment {
	return Comment{ID: e.ID, File: e.File, Side: e.Side, StartLine: e.StartLine, Line: e.Line, Code: e.Code, Outdated: e.Outdated}
}

// Running says whether Claude is still answering.
func (e Explain) Running() bool { return e.State == ExplainRunning }

// ExplainRequest is POST …/review/explain: the lines, the question and the conversation (a
// session number of the story, or ExplainCodeOnly).
type ExplainRequest struct {
	File      string `json:"file"`
	Side      string `json:"side"`
	StartLine int    `json:"start_line,omitempty"`
	Line      int    `json:"line"`
	Code      string `json:"code"`
	Question  string `json:"question,omitempty"`
	Session   int    `json:"session"`
}
