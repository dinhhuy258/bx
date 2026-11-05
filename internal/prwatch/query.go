// Package prwatch is the pure part of PR Watch: the GraphQL query of a repository's story
// branches, its parser, which PR a branch has, the events between two looks, the prompts and
// the per-story store. bx serve runs it (serve/prwatch.go).
package prwatch

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/dinhhuy258/bx/internal/ui"
)

// MaxCodeLines is how many lines of a thread's diff hunk are kept, ending at its line.
const MaxCodeLines = 6

const prFields = `number title url state isDraft baseRefName headRefOid headRepository { nameWithOwner }
reviewDecision mergeStateStatus
commits(last: 1) { nodes { commit { statusCheckRollup { contexts(first: 100) { nodes {
  __typename
  ... on CheckRun { name status conclusion detailsUrl checkSuite { workflowRun { databaseId } } }
  ... on StatusContext { context state targetUrl } } } } } } }
reviewThreads(first: 100) { nodes { id isResolved isOutdated path line startLine originalLine
  comments(first: 50) { nodes { author { login } body createdAt diffHunk } } } }`

// Query asks for every branch: its open PRs (b<i>open) and its last merged or closed one
// (b<i>last), plus who gh is.
func Query(owner, name string, branches []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "query { viewer { login } repository(owner: %s, name: %s) {\n", strconv.Quote(owner), strconv.Quote(name))
	for i, br := range branches {
		q := strconv.Quote(br)
		fmt.Fprintf(&b, "b%dopen: pullRequests(headRefName: %s, states: OPEN, first: 2, orderBy: {field: CREATED_AT, direction: DESC}) { nodes { %s } }\n", i, q, prFields)
		fmt.Fprintf(&b, "b%dlast: pullRequests(headRefName: %s, states: [MERGED, CLOSED], first: 1, orderBy: {field: UPDATED_AT, direction: DESC}) { nodes { %s } }\n", i, q, prFields)
	}
	b.WriteString("} }")
	return b.String()
}

// Found is what one branch has: its open PRs, newest first, and its last merged or closed one.
// Slug on each is the head repository, to tell a fork's PR of the same branch name.
type Found struct {
	Open []PR
	Last *PR
}

// PR is a parsed PR with its head repository.
type PR struct {
	ui.PRInfo
	Slug string
}

type ghResp struct {
	Data struct {
		Viewer     struct{ Login string }     `json:"viewer"`
		Repository map[string]json.RawMessage `json:"repository"`
	} `json:"data"`
	Errors []struct{ Message string } `json:"errors"`
}

type ghConn[T any] struct {
	Nodes []T `json:"nodes"`
}

type ghPR struct {
	Number           int
	Title, URL       string
	State            string
	IsDraft          bool
	BaseRefName      string
	HeadRefOid       string
	HeadRepository   *struct{ NameWithOwner string }
	ReviewDecision   *string
	MergeStateStatus string
	Commits          ghConn[struct {
		Commit struct {
			StatusCheckRollup *struct {
				Contexts ghConn[ghContext]
			}
		}
	}]
	ReviewThreads ghConn[struct {
		ID              string
		IsResolved      bool
		IsOutdated      bool
		Path            string
		Line, StartLine *int
		OriginalLine    *int
		Comments        ghConn[struct {
			Author    *struct{ Login string }
			Body      string
			CreatedAt time.Time
			DiffHunk  string
		}]
	}]
}

type ghContext struct {
	Typename   string `json:"__typename"`
	Name       string
	Status     string
	Conclusion *string
	DetailsURL string `json:"detailsUrl"`
	CheckSuite *struct {
		WorkflowRun *struct{ DatabaseID int64 } `json:"workflowRun"`
	}
	Context   string
	State     string
	TargetURL string `json:"targetUrl"`
}

// Parse reads the reply of Query for n branches: the gh login and what each branch has.
func Parse(raw []byte, n int) (string, []Found, error) {
	var r ghResp
	if err := json.Unmarshal(raw, &r); err != nil {
		return "", nil, fmt.Errorf("gh api graphql: %w", err)
	}
	if len(r.Errors) > 0 {
		msgs := make([]string, len(r.Errors))
		for i, e := range r.Errors {
			msgs[i] = e.Message
		}
		return "", nil, errors.New(strings.Join(msgs, "; "))
	}
	out := make([]Found, n)
	for i := range n {
		open, err := prsOf(r.Data.Repository[fmt.Sprintf("b%dopen", i)])
		if err != nil {
			return "", nil, err
		}
		last, err := prsOf(r.Data.Repository[fmt.Sprintf("b%dlast", i)])
		if err != nil {
			return "", nil, err
		}
		out[i].Open = open
		if len(last) > 0 {
			out[i].Last = &last[0]
		}
	}
	return r.Data.Viewer.Login, out, nil
}

func prsOf(raw json.RawMessage) ([]PR, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var c ghConn[ghPR]
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("gh api graphql: %w", err)
	}
	out := make([]PR, 0, len(c.Nodes))
	for _, g := range c.Nodes {
		out = append(out, convert(g))
	}
	return out, nil
}

func convert(g ghPR) PR {
	p := PR{PRInfo: ui.PRInfo{Number: g.Number, Title: g.Title, URL: g.URL, State: g.State, Draft: g.IsDraft,
		Base: g.BaseRefName, HeadOID: g.HeadRefOid, Merge: g.MergeStateStatus}}
	if g.HeadRepository != nil {
		p.Slug = g.HeadRepository.NameWithOwner
	}
	if g.ReviewDecision != nil {
		p.Decision = *g.ReviewDecision
	}
	for _, c := range g.Commits.Nodes {
		if c.Commit.StatusCheckRollup == nil {
			continue
		}
		for _, x := range c.Commit.StatusCheckRollup.Contexts.Nodes {
			p.Checks = append(p.Checks, check(x))
		}
	}
	for _, t := range g.ReviewThreads.Nodes {
		th := ui.PRThread{ID: t.ID, Path: t.Path, Resolved: t.IsResolved, Outdated: t.IsOutdated}
		switch {
		case t.Line != nil:
			th.Line = *t.Line
		case t.OriginalLine != nil:
			th.Line = *t.OriginalLine
		}
		if t.StartLine != nil {
			th.StartLine = *t.StartLine
		}
		for i, c := range t.Comments.Nodes {
			author := "ghost"
			if c.Author != nil {
				author = c.Author.Login
			}
			th.Comments = append(th.Comments, ui.PRComment{Author: author, Body: c.Body, At: c.CreatedAt.Unix()})
			if i == 0 {
				th.Code = hunkTail(c.DiffHunk, MaxCodeLines)
			}
		}
		p.Threads = append(p.Threads, th)
	}
	return p
}

func check(x ghContext) ui.PRCheck {
	if x.Typename == "StatusContext" {
		c := ui.PRCheck{Name: x.Context, URL: x.TargetURL}
		switch x.State {
		case "SUCCESS":
			c.State = ui.CheckPass
		case "PENDING", "EXPECTED":
			c.State = ui.CheckPending
		default:
			c.State = ui.CheckFail
		}
		return c
	}
	c := ui.PRCheck{Name: x.Name, URL: x.DetailsURL}
	if x.CheckSuite != nil && x.CheckSuite.WorkflowRun != nil {
		c.RunID = x.CheckSuite.WorkflowRun.DatabaseID
	}
	if x.Status != "COMPLETED" || x.Conclusion == nil {
		c.State = ui.CheckPending
		return c
	}
	switch *x.Conclusion {
	case "SUCCESS":
		c.State = ui.CheckPass
	case "SKIPPED", "NEUTRAL":
		c.State = ui.CheckSkip
	default: // FAILURE, TIMED_OUT, CANCELLED, ACTION_REQUIRED, STARTUP_FAILURE, STALE
		c.State = ui.CheckFail
	}
	return c
}

// hunkTail is the last max lines of a diff hunk without its @@ header, the +/- column kept.
func hunkTail(h string, max int) string {
	lines := strings.Split(strings.TrimRight(h, "\n"), "\n")
	if len(lines) > 0 && strings.HasPrefix(lines[0], "@@") {
		lines = lines[1:]
	}
	if len(lines) > max {
		lines = lines[len(lines)-max:]
	}
	return strings.Join(lines, "\n")
}
