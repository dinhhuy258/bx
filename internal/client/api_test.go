package client

import (
	"context"
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
