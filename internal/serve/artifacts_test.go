package serve

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dinhhuy258/bx/internal/api"
)

func TestListArtifacts(t *testing.T) {
	s, _, ts := newServer(t)
	s.App.ArtifactsURL = "http://100.64.0.1:7358"
	const path = "/api/projects/home-network/sessions/1/artifacts"
	got, code := get[api.ArtifactList](t, ts, path)
	if code != 200 || got.Files == nil || len(got.Files) != 0 {
		t.Fatalf("no folder yet: %d %+v", code, got)
	}
	dir := s.App.ArtifactsDir("home-network", 1)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"old.html", "new.html", "plan.md", "notes.txt", ".draft.html"} {
		_ = os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600)
	}
	hourAgo := time.Now().Add(-time.Hour)
	_ = os.Chtimes(filepath.Join(dir, "old.html"), hourAgo, hourAgo)
	twoHoursAgo := time.Now().Add(-2 * time.Hour)
	_ = os.Chtimes(filepath.Join(dir, "plan.md"), twoHoursAgo, twoHoursAgo)
	got, code = get[api.ArtifactList](t, ts, path)
	if code != 200 || len(got.Files) != 3 || got.Files[0].Name != "new.html" || got.Files[1].Name != "old.html" || got.Files[2].Name != "plan.md" {
		t.Fatalf("%d %+v", code, got)
	}
	if got.Files[0].URL != "http://100.64.0.1:7358/home-network/1/new.html" {
		t.Fatalf("url %q", got.Files[0].URL)
	}
	if _, code = get[api.ArtifactList](t, ts, "/api/projects/home-network/sessions/9/artifacts"); code != 404 {
		t.Fatalf("unknown session: %d", code)
	}
}

func TestIsPageName(t *testing.T) {
	for name, want := range map[string]bool{
		"report.html": true, "Report.HTML": true, "plan.md": true, "PLAN.MD": true,
		"notes.txt": false, ".draft.html": false, ".plan.md": false, "md": false, "": false,
	} {
		if got := IsPageName(name); got != want {
			t.Errorf("IsPageName(%q) = %v", name, got)
		}
	}
}
