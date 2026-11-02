package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dinhhuy258/bx/internal/api"
	"github.com/dinhhuy258/bx/internal/serve/servetest"
)

func TestAPIAgainstServe(t *testing.T) {
	_, _, ts := servetest.New(t)
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
	if _, err := c.Do(ctx, "home-network", "stories", api.Action{Name: "Bad Name"}); err == nil || err.Error() == "" {
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
	if err != nil || len(got.Sections) != 1 || got.Sections[0].Fields[0].Key != "claude.model" {
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
	s, _, ts := servetest.New(t)
	s.ClaudeDir = t.TempDir()
	c := NewAPI(ts.URL)
	ctx := context.Background()
	plan, err := c.SyncPlan(ctx, api.SyncPlanRequest{Files: []api.SyncFile{{Path: "skills/a/SKILL.md", SHA256: "x", Size: 1}}})
	if err != nil || len(plan.Items) != 1 || plan.Items[0].Name != "skills/a" || plan.Items[0].Status != "new" || plan.Dir != s.ClaudeDir {
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
