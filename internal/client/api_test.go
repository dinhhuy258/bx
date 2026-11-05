package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/dinhhuy258/bx/internal/api"
	"github.com/dinhhuy258/bx/internal/serve/servetest"
	"github.com/dinhhuy258/bx/internal/tmux"
)

func TestAPIAgainstServe(t *testing.T) {
	_, f, ts := servetest.New(t)
	c := NewAPI(ts.URL)
	ctx := context.Background()
	list, err := c.Projects(ctx)
	if err != nil || len(list) != 1 || list[0].Name != "home-network" {
		t.Fatalf("%+v %v", list, err)
	}
	snap, err := c.Snapshot(ctx, "home-network")
	if err != nil || snap.Project.Name != "home-network" || snap.Project.Sessions[1] == nil {
		t.Fatalf("%+v %v", snap, err)
	}
	res, err := c.Do(ctx, "home-network", "sessions", api.Action{Title: "hello"})
	if err != nil || res.Num != 2 {
		t.Fatalf("%+v %v", res, err)
	}
	if _, err := c.Do(ctx, "home-network", "stories", api.Action{Name: "!!!"}); err == nil || err.Error() == "" {
		t.Fatalf("want the server's error text, got %v", err)
	}
	if res, err := c.CreateProject(ctx, api.NewProject{Kind: "new", Name: "scratch"}); err != nil || res.Name != "scratch" {
		t.Fatalf("%+v %v", res, err)
	}
	if _, err := c.Snapshot(ctx, "nope"); err == nil {
		t.Fatal("unknown project must fail")
	}
	if got := c.StreamURL("home-network", 3, 100, 30); got != "ws"+ts.URL[4:]+"/api/projects/home-network/sessions/3/stream?cols=100&rows=30" {
		t.Fatal(got)
	}
	if got := c.ShellStreamURL("home network", "s 1", 2, 100, 30); got != "ws"+ts.URL[4:]+"/api/projects/home%20network/shells/stream?story=s+1&k=2&cols=100&rows=30" {
		t.Fatal(got)
	}
	if res, err := c.Do(ctx, "home-network", "shells", api.Action{Story: "s1"}); err != nil || res.Story != "s1" || res.K != 1 {
		t.Fatalf("new shell: %+v %v", res, err)
	}
	f.Replies["list-sessions -F "+tmux.ShellFormat] = "$9\tbxsh-home-network-s1-1\thome-network\ts1\t1\t\tzsh\n"
	if _, err := c.Do(ctx, "home-network", "shells/close", api.Action{Story: "s1", K: 1}); err != nil {
		t.Fatalf("close shell: %v", err)
	}
}

func TestUploadAgainstServe(t *testing.T) {
	_, _, ts := servetest.New(t)
	res, err := NewAPI(ts.URL).Upload(context.Background(), "home-network", 1, "shot.png", []byte("\x89PNG"))
	if err != nil || !strings.HasSuffix(res.Path, "-shot.png") {
		t.Fatalf("%+v %v", res, err)
	}
	if b, err := os.ReadFile(res.Path); err != nil || string(b) != "\x89PNG" {
		t.Fatalf("saved %q %v", b, err)
	}
}

func TestDoReturnsTheResultOfARefusal(t *testing.T) {
	s, _, ts := servetest.New(t)
	servetest.DirtyStory(t, s) // s1 gets one uncommitted file
	res, err := NewAPI(ts.URL).Do(context.Background(), "home-network", "stories/close", api.Action{Name: "s1"})
	if err == nil || res.Dirty != 1 {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestSettingsAgainstServe(t *testing.T) {
	_, _, ts := servetest.New(t)
	c := NewAPI(ts.URL)
	ctx := context.Background()
	got, err := c.Settings(ctx)
	if err != nil || len(got.Sections) != 3 || got.Sections[0].Fields[0].Key != "claude.model" || got.Sections[2].ID != "notifications" {
		t.Fatalf("%+v %v", got, err)
	}
	saved, err := c.SaveSettings(ctx, map[string]string{"claude.model": "sonnet"})
	if err != nil || saved.Values["claude.model"] != "sonnet" {
		t.Fatalf("%+v %v", saved, err)
	}
	if _, err := c.SaveSettings(ctx, map[string]string{"claude.effort": "huge"}); err == nil || !strings.Contains(err.Error(), "want one of") {
		t.Fatalf("want the server's message, got %v", err)
	}
}

func TestClaudeSyncAgainstServe(t *testing.T) {
	_, _, ts := servetest.New(t)
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	c := NewAPI(ts.URL)
	ctx := context.Background()
	plan, err := c.SyncPlan(ctx, api.SyncPlanRequest{Files: []api.SyncFile{{Path: "skills/a/SKILL.md", SHA256: "x"}}})
	if err != nil || len(plan.Items) != 1 || plan.Items[0].Name != "skills/a" || plan.Items[0].Status != "new" || plan.Dir != dir {
		t.Fatalf("%+v %v", plan, err)
	}
	res, err := c.SyncApply(ctx, api.SyncApplyRequest{Items: []string{"skills/a"}, Paths: []string{"skills/a/SKILL.md"}, Files: []api.SyncFile{{Path: "skills/a/SKILL.md", Content: []byte("a")}}})
	if err != nil || res.Added != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	_, err = c.SyncPlan(ctx, api.SyncPlanRequest{Files: []api.SyncFile{{Path: "other/x.md"}}})
	var se *StatusError
	if !errors.As(err, &se) || se.Code != 400 || !strings.Contains(err.Error(), "bad path") {
		t.Fatalf("want the server's error text, got %v", err)
	}
}

func TestNewStoryKeepsTheExistsRefusal(t *testing.T) {
	var got []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Method+" "+r.URL.RequestURI())
		switch r.URL.Path {
		case "/api/projects/lab/stories":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(api.Result{Error: "branch fix/x exists on origin", Exists: "origin", Branch: "fix/x"})
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer ts.Close()
	c, ctx := NewAPI(ts.URL), context.Background()
	res, err := c.NewStory(ctx, "lab", api.Action{Name: "fix/x"})
	var se *StatusError
	if !errors.As(err, &se) || se.Code != 409 || res.Exists != "origin" || res.Branch != "fix/x" || err.Error() != "branch fix/x exists on origin" {
		t.Fatalf("%+v %v", res, err)
	}
	_, _ = c.StoryInfo(ctx, "lab")
	_, _ = c.Branches(ctx, "lab", true)
	_, _ = c.Branches(ctx, "lab", false)
	_, _ = c.PullRequests(ctx, "lab", "")
	_, _ = c.PullRequests(ctx, "lab", "#12 x&y")
	want := "POST /api/projects/lab/stories|GET /api/projects/lab/storyinfo|GET /api/projects/lab/branches?fetch=1|GET /api/projects/lab/branches|GET /api/projects/lab/prs|GET /api/projects/lab/prs?q=%2312+x%26y"
	if strings.Join(got, "|") != want {
		t.Fatalf("requests\n%s\nwant\n%s", strings.Join(got, "|"), want)
	}
}

// An old server without the sync routes: the status code reaches the caller.
func TestStatusErrorCarriesTheCode(t *testing.T) {
	for _, code := range []int{http.StatusNotFound, http.StatusMethodNotAllowed} {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "nope", code)
		}))
		_, err := NewAPI(ts.URL).SyncPlan(context.Background(), api.SyncPlanRequest{})
		ts.Close()
		var se *StatusError
		if !errors.As(err, &se) || se.Code != code || !strings.Contains(err.Error(), fmt.Sprint(code)) {
			t.Fatalf("%d: %#v", code, err)
		}
	}
}

func TestAPIMCPDoc(t *testing.T) {
	var gotPath, gotBody string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.Method + " " + r.URL.RequestURI()
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		if r.Method == http.MethodPut {
			w.WriteHeader(400)
			w.Write([]byte(`{"error":"line 2, column 1: x","line":2}`))
			return
		}
		w.Write([]byte(`{"text":"{}"}`))
	}))
	defer ts.Close()
	c := NewAPI(ts.URL)
	if d, err := c.MCPDoc(context.Background(), "my proj"); err != nil || d.Text != "{}" || gotPath != "GET /api/mcp/doc?tui=1&project=my+proj" {
		t.Fatalf("%v %q %s", err, d.Text, gotPath)
	}
	res, err := c.SaveMCPDoc(context.Background(), "", "x")
	if err == nil || res.Line != 2 || gotPath != "PUT /api/mcp/doc?tui=1&project=" || gotBody != `{"text":"x"}` {
		t.Fatalf("%v %+v %s %s", err, res, gotPath, gotBody)
	}
}

func TestReviewCalls(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got = append(got, r.Method+" "+r.URL.EscapedPath()+" "+strings.TrimSpace(string(body)))
		if strings.HasSuffix(r.URL.Path, "/send") {
			_, _ = w.Write([]byte(`{"num":3,"message":"Pasted 1 comment into [3]"}`))
			return
		}
		_, _ = w.Write([]byte(`{"base":"main","total":0,"files":[],"version":4,"comments":[]}`))
	}))
	defer srv.Close()
	c := &API{Base: srv.URL, HTTP: srv.Client()}
	ctx := context.Background()
	r, err := c.Review(ctx, "home network", "s1")
	if err != nil || r.Version != 4 {
		t.Fatal(r, err)
	}
	_, _ = c.SaveReview(ctx, "p", "s1", api.ReviewComments{Version: 4})
	res, _ := c.SendReview(ctx, "p", "s1", api.ReviewSend{Session: 3})
	_, _ = c.ClearSent(ctx, "p", "s1")
	_, _ = c.Explain(ctx, "p", "s1", api.ExplainRequest{File: "a.go", Side: "new", Line: 2, Session: api.ExplainCodeOnly})
	_, _ = c.DeleteExplain(ctx, "p", "s1", "e 1")
	_, _ = c.MarkViewed(ctx, "p", "s1", api.ReviewViewed{Path: "a.go", Hash: "h", Viewed: true})
	want := []string{
		"GET /api/projects/home%20network/stories/s1/review ",
		`PUT /api/projects/p/stories/s1/review {"version":4,"comments":null}`,
		`POST /api/projects/p/stories/s1/review/send {"session":3}`,
		"POST /api/projects/p/stories/s1/review/clear-sent {}",
		`POST /api/projects/p/stories/s1/review/explain {"file":"a.go","side":"new","line":2,"code":"","session":-1}`,
		"DELETE /api/projects/p/stories/s1/review/explain/e%201 ",
		`PUT /api/projects/p/stories/s1/review/viewed {"path":"a.go","hash":"h","viewed":true}`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") || res.Num != 3 {
		t.Fatalf("%s\n%+v", strings.Join(got, "\n"), res)
	}
}
