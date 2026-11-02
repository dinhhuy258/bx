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

// Settings is GET /api/settings: the sections and fields the server offers, and their values.
func (c *API) Settings(ctx context.Context) (api.Settings, error) {
	var out api.Settings
	return out, c.call(ctx, pollTimeout, http.MethodGet, "/api/settings", nil, &out)
}

// SaveSettings is PUT /api/settings with the changed values. It returns what the server saved.
func (c *API) SaveSettings(ctx context.Context, values map[string]string) (api.Settings, error) {
	var out api.Settings
	return out, c.call(ctx, pollTimeout, http.MethodPut, "/api/settings", api.Settings{Values: values}, &out)
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

// StreamURL is the WebSocket address of one session's terminal at a given size.
func (c *API) StreamURL(project string, num, cols, rows int) string {
	ws := "ws" + strings.TrimPrefix(c.Base, "http")
	return fmt.Sprintf("%s/api/projects/%s/sessions/%d/stream?cols=%d&rows=%d", ws, url.PathEscape(project), num, cols, rows)
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
