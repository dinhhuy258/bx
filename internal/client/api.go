// Package client is the bx client: the project list, the Claude pane of the open session and
// the key bar, drawn in the local terminal from what bx serve sends.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/dinhhuy258/bx/internal/accounts"
	"github.com/dinhhuy258/bx/internal/api"
)

// API talks to bx serve. Base is "http://100.x.y.z:7357". Each call has its own timeout:
// a clone may take minutes, a poll must not.
type API struct {
	Base string
	HTTP *http.Client
}

const (
	pollTimeout   = 5 * time.Second
	reposTimeout  = 30 * time.Second
	createTimeout = 10 * time.Minute
	syncTimeout   = 2 * time.Minute
	uploadTimeout = 2 * time.Minute
	sendTimeout   = 60 * time.Second // a send may resume Claude and wait up to 30 s for it
)

// NewAPI builds a client for base.
func NewAPI(base string) *API {
	return &API{Base: strings.TrimRight(base, "/"), HTTP: &http.Client{}}
}

// Projects is GET /api/projects.
func (c *API) Projects(ctx context.Context) ([]api.ProjectSummary, error) {
	var out []api.ProjectSummary
	return out, c.call(ctx, pollTimeout, http.MethodGet, "/api/projects", nil, &out)
}

// Snapshot is GET /api/projects/{name}. The project's Name is filled in (it is not on the wire).
func (c *API) Snapshot(ctx context.Context, project string) (api.Snapshot, error) {
	var out api.Snapshot
	if err := c.call(ctx, pollTimeout, http.MethodGet, "/api/projects/"+url.PathEscape(project), nil, &out); err != nil {
		return out, err
	}
	out.Project.Name = project
	return out, nil
}

// Do is POST /api/projects/{name}/{action}. The Result is filled in on a refusal too: a dirty
// story close returns an error and Result.Dirty.
func (c *API) Do(ctx context.Context, project, action string, a api.Action) (api.Result, error) {
	var res api.Result
	err := c.call(ctx, pollTimeout, http.MethodPost, "/api/projects/"+url.PathEscape(project)+"/"+action, a, &res)
	return res, err
}

// NewStory is POST /api/projects/{name}/stories. The server fetches the base and starts the
// story's first session, so it has the long timeout. The Result is filled in on a refusal too:
// a branch that exists returns an error and Result.Exists.
func (c *API) NewStory(ctx context.Context, project string, a api.Action) (api.Result, error) {
	var res api.Result
	err := c.call(ctx, createTimeout, http.MethodPost, "/api/projects/"+url.PathEscape(project)+"/stories", a, &res)
	return res, err
}

// Sync is POST /api/projects/{name}/sync. The server fetches from origin first, so it has the
// long timeout. A story Claude works in is refused with an error and Result.Running.
func (c *API) Sync(ctx context.Context, project string, a api.Action) (api.Result, error) {
	var res api.Result
	err := c.call(ctx, createTimeout, http.MethodPost, "/api/projects/"+url.PathEscape(project)+"/sync", a, &res)
	return res, err
}

// StoryInfo is GET /api/projects/{name}/storyinfo: the base and last type of a new story. The
// server may ask origin for its HEAD (git remote set-head), so it gets longer than a poll.
func (c *API) StoryInfo(ctx context.Context, project string) (api.StoryInfo, error) {
	var out api.StoryInfo
	return out, c.call(ctx, reposTimeout, http.MethodGet, "/api/projects/"+url.PathEscape(project)+"/storyinfo", nil, &out)
}

// Branches is GET /api/projects/{name}/branches. With fetch the server fetches origin first,
// which may take long.
func (c *API) Branches(ctx context.Context, project string, fetch bool) (api.BranchList, error) {
	var out api.BranchList
	path, timeout := "/api/projects/"+url.PathEscape(project)+"/branches", pollTimeout
	if fetch {
		path, timeout = path+"?fetch=1", createTimeout
	}
	return out, c.call(ctx, timeout, http.MethodGet, path, nil, &out)
}

// PullRequests is GET /api/projects/{name}/prs: the open pull requests, or those matching q
// (a search, #12 or a pull request URL).
func (c *API) PullRequests(ctx context.Context, project, q string) (api.PRList, error) {
	var out api.PRList
	path := "/api/projects/" + url.PathEscape(project) + "/prs"
	if q != "" {
		path += "?q=" + url.QueryEscape(q)
	}
	return out, c.call(ctx, reposTimeout, http.MethodGet, path, nil, &out)
}

// CreateProject is POST /api/projects (add a folder, clone, or start an empty project).
func (c *API) CreateProject(ctx context.Context, p api.NewProject) (api.Result, error) {
	var res api.Result
	err := c.call(ctx, createTimeout, http.MethodPost, "/api/projects", p, &res)
	return res, err
}

// Repos is GET /api/github/repos: owner/name of the repositories gh can see on the server.
func (c *API) Repos(ctx context.Context) ([]string, error) {
	var out []string
	return out, c.call(ctx, reposTimeout, http.MethodGet, "/api/github/repos", nil, &out)
}

// ProjectRepos is GET /api/projects/{name}/repos: the repositories of a workspace, relative to it.
func (c *API) ProjectRepos(ctx context.Context, project string) ([]string, error) {
	var out []string
	return out, c.call(ctx, pollTimeout, http.MethodGet, "/api/projects/"+url.PathEscape(project)+"/repos", nil, &out)
}

// WorkspaceLinks is GET /api/projects/{name}/links: the links at the top of a workspace.
func (c *API) WorkspaceLinks(ctx context.Context, project string) ([]api.Link, error) {
	var out []api.Link
	return out, c.call(ctx, pollTimeout, http.MethodGet, "/api/projects/"+url.PathEscape(project)+"/links", nil, &out)
}

// Settings is GET /api/settings?tui=1: the sections and fields the server offers, and their
// values, the TUI-only ones included.
func (c *API) Settings(ctx context.Context) (api.Settings, error) {
	var out api.Settings
	return out, c.call(ctx, pollTimeout, http.MethodGet, "/api/settings?tui=1", nil, &out)
}

// SaveSettings is PUT /api/settings?tui=1 with the changed values. It returns what the server saved.
func (c *API) SaveSettings(ctx context.Context, values map[string]string) (api.Settings, error) {
	var out api.Settings
	return out, c.call(ctx, pollTimeout, http.MethodPut, "/api/settings?tui=1", api.Settings{Values: values}, &out)
}

// MCP is GET /api/mcp?tui=1: the global MCP servers and every project with its own.
func (c *API) MCP(ctx context.Context) (api.MCP, error) {
	var out api.MCP
	return out, c.call(ctx, pollTimeout, http.MethodGet, "/api/mcp?tui=1", nil, &out)
}

// GitAccounts is GET /api/git/accounts?tui=1: the GitHub accounts, gh's logins and the
// origin of every project.
func (c *API) GitAccounts(ctx context.Context) (api.GitAccounts, error) {
	var out api.GitAccounts
	return out, c.call(ctx, reposTimeout, http.MethodGet, "/api/git/accounts?tui=1", nil, &out)
}

// SaveGitAccounts is PUT /api/git/accounts?tui=1 with the whole config.
func (c *API) SaveGitAccounts(ctx context.Context, x accounts.Config) (api.GitAccounts, error) {
	var out api.GitAccounts
	return out, c.call(ctx, reposTimeout, http.MethodPut, "/api/git/accounts?tui=1", x, &out)
}

// MCPDoc is GET /api/mcp/doc: the text of project's servers ("" = the global ones).
func (c *API) MCPDoc(ctx context.Context, project string) (api.MCPDoc, error) {
	var out api.MCPDoc
	return out, c.call(ctx, pollTimeout, http.MethodGet, "/api/mcp/doc?tui=1&project="+url.QueryEscape(project), nil, &out)
}

// SaveMCPDoc is PUT /api/mcp/doc. A refusal fills Result: the error and its Line.
func (c *API) SaveMCPDoc(ctx context.Context, project, text string) (api.Result, error) {
	var res api.Result
	err := c.call(ctx, pollTimeout, http.MethodPut, "/api/mcp/doc?tui=1&project="+url.QueryEscape(project), api.MCPDoc{Text: text}, &res)
	return res, err
}

// SyncPlan is POST /api/claude/sync/plan: what a Claude sync would change on the server.
func (c *API) SyncPlan(ctx context.Context, r api.SyncPlanRequest) (api.SyncPlan, error) {
	var out api.SyncPlan
	return out, c.call(ctx, reposTimeout, http.MethodPost, "/api/claude/sync/plan", r, &out)
}

// SyncApply is POST /api/claude/sync: write the files and deletes of a Claude sync.
func (c *API) SyncApply(ctx context.Context, r api.SyncApplyRequest) (api.SyncResult, error) {
	var out api.SyncResult
	return out, c.call(ctx, syncTimeout, http.MethodPost, "/api/claude/sync", r, &out)
}

// Cleanup is GET /api/cleanup: the orphan tmux sessions and the done sessions of every project.
func (c *API) Cleanup(ctx context.Context) (api.Cleanup, error) {
	var out api.Cleanup
	return out, c.call(ctx, pollTimeout, http.MethodGet, "/api/cleanup", nil, &out)
}

// CleanupApply is POST /api/cleanup: remove the items with these ids.
func (c *API) CleanupApply(ctx context.Context, ids []string) (api.CleanupResult, error) {
	var out api.CleanupResult
	return out, c.call(ctx, pollTimeout, http.MethodPost, "/api/cleanup", api.CleanupRequest{IDs: ids}, &out)
}

// Artifacts is GET /api/projects/{name}/sessions/{num}/artifacts: the session's HTML pages,
// newest first, with their file server links.
func (c *API) Artifacts(ctx context.Context, project string, num int) (api.ArtifactList, error) {
	var out api.ArtifactList
	path := fmt.Sprintf("/api/projects/%s/sessions/%d/artifacts", url.PathEscape(project), num)
	return out, c.call(ctx, pollTimeout, http.MethodGet, path, nil, &out)
}

func reviewPath(project, story string) string {
	return "/api/projects/" + url.PathEscape(project) + "/stories/" + url.PathEscape(story) + "/review"
}

// Review is GET …/stories/{story}/review: the diff since the story's base, with its comments.
func (c *API) Review(ctx context.Context, project, story string) (api.Review, error) {
	var out api.Review
	return out, c.call(ctx, reposTimeout, http.MethodGet, reviewPath(project, story), nil, &out)
}

// SaveReview is PUT …/review: the whole comment list at the version it was read at.
func (c *API) SaveReview(ctx context.Context, project, story string, cs api.ReviewComments) (api.Review, error) {
	var out api.Review
	return out, c.call(ctx, reposTimeout, http.MethodPut, reviewPath(project, story), cs, &out)
}

// SendReview is POST …/review/send: paste the unsent comments into a session.
func (c *API) SendReview(ctx context.Context, project, story string, r api.ReviewSend) (api.Result, error) {
	var out api.Result
	return out, c.call(ctx, sendTimeout, http.MethodPost, reviewPath(project, story)+"/send", r, &out)
}

func prPath(project, story string) string {
	return "/api/projects/" + url.PathEscape(project) + "/stories/" + url.PathEscape(story) + "/pr"
}

// PR is GET …/stories/{story}/pr: the pull requests of the story as the poller last saw them.
func (c *API) PR(ctx context.Context, project, story string) (api.PRData, error) {
	var out api.PRData
	return out, c.call(ctx, reposTimeout, http.MethodGet, prPath(project, story), nil, &out)
}

// RefreshPR is POST …/pr/refresh: look at GitHub now and answer with the new look.
func (c *API) RefreshPR(ctx context.Context, project, story string) (api.PRData, error) {
	var out api.PRData
	return out, c.call(ctx, sendTimeout, http.MethodPost, prPath(project, story)+"/refresh", struct{}{}, &out)
}

// SendPR is POST …/pr/send: paste and submit the failed checks or the picked threads of a PR.
func (c *API) SendPR(ctx context.Context, project, story string, r api.PRSend) (api.Result, error) {
	var out api.Result
	return out, c.call(ctx, sendTimeout, http.MethodPost, prPath(project, story)+"/send", r, &out)
}

// MarkViewed is PUT …/review/viewed: mark a file viewed, or unmark it.
func (c *API) MarkViewed(ctx context.Context, project, story string, v api.ReviewViewed) (api.Review, error) {
	var out api.Review
	return out, c.call(ctx, reposTimeout, http.MethodPut, reviewPath(project, story)+"/viewed", v, &out)
}

// ClearSent is POST …/review/clear-sent: drop the sent comments.
func (c *API) ClearSent(ctx context.Context, project, story string) (api.Review, error) {
	var out api.Review
	return out, c.call(ctx, reposTimeout, http.MethodPost, reviewPath(project, story)+"/clear-sent", struct{}{}, &out)
}

// Explain is POST …/review/explain: ask Claude about diff lines; the review comes back with it
// running.
func (c *API) Explain(ctx context.Context, project, story string, req api.ExplainRequest) (api.Review, error) {
	var out api.Review
	return out, c.call(ctx, reposTimeout, http.MethodPost, reviewPath(project, story)+"/explain", req, &out)
}

// DeleteExplain is DELETE …/review/explain/{id}: stop it or dismiss its answer.
func (c *API) DeleteExplain(ctx context.Context, project, story, id string) (api.Review, error) {
	var out api.Review
	return out, c.call(ctx, reposTimeout, http.MethodDelete, reviewPath(project, story)+"/explain/"+url.PathEscape(id), nil, &out)
}

// Upload is POST /api/projects/{name}/sessions/{num}/files: one file for the session, saved on
// the server, which replies with its absolute path there.
func (c *API) Upload(ctx context.Context, project string, num int, name string, content []byte) (api.UploadResult, error) {
	var out api.UploadResult
	path := fmt.Sprintf("/api/projects/%s/sessions/%d/files", url.PathEscape(project), num)
	return out, c.call(ctx, uploadTimeout, http.MethodPost, path, api.UploadRequest{Name: name, Content: content}, &out)
}

// StreamURL is the WebSocket address of one session's terminal at a given size.
func (c *API) StreamURL(project string, num, cols, rows int) string {
	ws := "ws" + strings.TrimPrefix(c.Base, "http")
	return fmt.Sprintf("%s/api/projects/%s/sessions/%d/stream?cols=%d&rows=%d", ws, url.PathEscape(project), num, cols, rows)
}

// ShellStreamURL is the WebSocket address of shell k of a story ("" = chats) at a given size.
func (c *API) ShellStreamURL(project, story string, k, cols, rows int) string {
	ws := "ws" + strings.TrimPrefix(c.Base, "http")
	return fmt.Sprintf("%s/api/projects/%s/shells/stream?story=%s&k=%d&cols=%d&rows=%d", ws, url.PathEscape(project), url.QueryEscape(story), k, cols, rows)
}

// call sends body as JSON and decodes the reply into out. A non-2xx reply becomes an error
// carrying the server's text; when out is an *api.Result it also gets the refusal.
// StatusError is a non-2xx reply: its status code and the server's error text.
type StatusError struct {
	Code int
	Text string
}

func (e *StatusError) Error() string { return e.Text }

func (c *API) call(ctx context.Context, timeout time.Duration, method, path string, body, out any) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Base+path, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("bx serve unreachable: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		var res api.Result
		_ = json.NewDecoder(resp.Body).Decode(&res)
		if res.Error == "" {
			res.Error = resp.Status
		}
		if r, ok := out.(*api.Result); ok {
			*r = res
		}
		return &StatusError{Code: resp.StatusCode, Text: res.Error}
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
