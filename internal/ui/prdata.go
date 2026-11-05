package ui

import (
	"strconv"
	"strings"
)

// The states of a PR check, folded from GitHub's check runs and commit statuses.
const (
	CheckPass    = "pass"
	CheckFail    = "fail"
	CheckPending = "pending"
	CheckSkip    = "skip" // skipped or neutral: counts as passed
)

// The states of a PR.
const (
	PROpen   = "OPEN"
	PRMerged = "MERGED"
	PRClosed = "CLOSED"
)

// PRCheck is one check of a PR's head commit.
type PRCheck struct {
	Name  string `json:"name"`
	State string `json:"state"`
	URL   string `json:"url,omitempty"`
	RunID int64  `json:"run_id,omitempty"` // the Actions run, for gh run view --log-failed; 0 for a commit status
}

// PRComment is one comment of a review thread.
type PRComment struct {
	Author string `json:"author"`
	Body   string `json:"body"`
	At     int64  `json:"at"` // unix seconds
}

// PRThread is a review thread on a PR's diff. Code is the end of its diff hunk, the lines it is on.
type PRThread struct {
	ID        string      `json:"id"`
	Path      string      `json:"path"`
	Line      int         `json:"line"`
	StartLine int         `json:"start_line,omitempty"`
	Resolved  bool        `json:"resolved,omitempty"`
	Outdated  bool        `json:"outdated,omitempty"`
	Code      string      `json:"code,omitempty"`
	Comments  []PRComment `json:"comments"`
	SentAt    int64       `json:"sent_at,omitempty"` // when Address last sent it to Claude
}

// Open reports whether the thread waits for the user: unresolved, and its last comment is
// someone else's.
func (t PRThread) Open(me string) bool {
	if t.Resolved || len(t.Comments) == 0 {
		return false
	}
	return !strings.EqualFold(t.Comments[len(t.Comments)-1].Author, me)
}

// Where is "path:line" or "path:start-line".
func (t PRThread) Where() string {
	if t.StartLine > 0 && t.StartLine < t.Line {
		return t.Path + ":" + strconv.Itoa(t.StartLine) + "-" + strconv.Itoa(t.Line)
	}
	return t.Path + ":" + strconv.Itoa(t.Line)
}

// PRInfo is the pull request of one repository of a story, or why there is none (Missing:
// "not pushed", "no PR", "repo missing" or a gh error).
type PRInfo struct {
	Repo     string     `json:"repo,omitempty"` // the workspace repo folder; "" in a repo project
	Missing  string     `json:"missing,omitempty"`
	Number   int        `json:"number,omitempty"`
	Title    string     `json:"title,omitempty"`
	URL      string     `json:"url,omitempty"`
	State    string     `json:"state,omitempty"` // PROpen, PRMerged or PRClosed
	Draft    bool       `json:"draft,omitempty"`
	Base     string     `json:"base,omitempty"`
	HeadOID  string     `json:"head_oid,omitempty"`
	Decision string     `json:"decision,omitempty"` // APPROVED, CHANGES_REQUESTED, REVIEW_REQUIRED or ""
	Merge    string     `json:"merge,omitempty"`    // mergeStateStatus: CLEAN, BEHIND, DIRTY, BLOCKED, UNSTABLE, …
	MoreOpen int        `json:"more_open,omitempty"`
	LastCI   string     `json:"last_ci,omitempty"` // the last finished CI outcome, CheckPass or CheckFail, kept over pending looks
	Checks   []PRCheck  `json:"checks,omitempty"`
	Threads  []PRThread `json:"threads,omitempty"`
}

// Has reports whether a PR was found.
func (p PRInfo) Has() bool { return p.Number > 0 }

func (p PRInfo) count(state string) int {
	n := 0
	for _, c := range p.Checks {
		if c.State == state {
			n++
		}
	}
	return n
}

// Failed reports whether a check failed.
func (p PRInfo) Failed() bool { return p.count(CheckFail) > 0 }

// Pending reports whether a check still runs.
func (p PRInfo) Pending() bool { return p.count(CheckPending) > 0 }

// Passed reports whether there are checks and every one passed or was skipped.
func (p PRInfo) Passed() bool { return len(p.Checks) > 0 && !p.Failed() && !p.Pending() }

// FailedChecks are the checks that failed, in order.
func (p PRInfo) FailedChecks() []PRCheck {
	var out []PRCheck
	for _, c := range p.Checks {
		if c.State == CheckFail {
			out = append(out, c)
		}
	}
	return out
}

// OpenThreads counts the threads waiting for me.
func (p PRInfo) OpenThreads(me string) int {
	n := 0
	for _, t := range p.Threads {
		if t.Open(me) {
			n++
		}
	}
	return n
}

// PRData is the reply of GET …/stories/{story}/pr: one entry per repository of the story.
type PRData struct {
	PRs       []PRInfo `json:"prs"`
	Me        string   `json:"me,omitempty"` // the gh login, for PRThread.Open
	CheckedAt int64    `json:"checked_at,omitempty"`
}

// Found are the entries with a PR.
func (d PRData) Found() []PRInfo {
	var out []PRInfo
	for _, p := range d.PRs {
		if p.Has() {
			out = append(out, p)
		}
	}
	return out
}

// PRBadge is what a story row shows of its PRs (Live.PR). Count 0 shows nothing.
type PRBadge struct {
	Count    int  `json:"count"`
	Number   int  `json:"number,omitempty"` // the PR when Count is 1
	Merged   int  `json:"merged,omitempty"`
	Closed   int  `json:"closed,omitempty"`
	Fail     bool `json:"fail,omitempty"`
	Pending  bool `json:"pending,omitempty"`
	Pass     bool `json:"pass,omitempty"`
	Threads  int  `json:"threads,omitempty"`
	Changes  bool `json:"changes,omitempty"`
	Review   bool `json:"review,omitempty"`
	Conflict bool `json:"conflict,omitempty"`
	Behind   bool `json:"behind,omitempty"`
	Ready    bool `json:"ready,omitempty"`
}

// NeedsYou reports whether the badge asks for attention: a failure, changes or a conflict.
func (b PRBadge) NeedsYou() bool { return b.Fail || b.Changes || b.Conflict }
