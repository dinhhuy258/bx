package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dinhhuy258/bx/internal/state"
)

func artifactsApp(t *testing.T) (*App, func() string, *state.Project) {
	t.Helper()
	a, f, p := newApp(t)
	a.PluginDir = "/opt/bx-plugin"
	a.ArtifactsURL = "http://100.64.0.1:7358"
	return a, func() string { return calls(f) }, p
}

func TestSessionGetsArtifacts(t *testing.T) {
	a, got, p := artifactsApp(t)
	num, err := a.NewChat(p, "")
	if err != nil {
		t.Fatal(err)
	}
	dir := a.ArtifactsDir(p.Name, num)
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		t.Fatalf("artifacts folder: %v", err)
	}
	for _, want := range []string{
		"env BX_ARTIFACTS=" + dir + " claude --session-id fixed-uuid",
		"--disallowed-tools Artifact,ArtifactComments,ArtifactData",
		"--plugin-dir /opt/bx-plugin",
		"--add-dir " + dir,
		`"allow":["Write(/` + dir + `/**)","Edit(/` + dir + `/**)"]`,
		"use the bx:artifact skill and save it in " + dir,
		"http://100.64.0.1:7358/home-network/1/<file name>",
	} {
		if !strings.Contains(got(), want) {
			t.Errorf("missing %q in:\n%s", want, got())
		}
	}
	if err := a.Resume(p, num); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(got(), "--plugin-dir /opt/bx-plugin"); n != 2 {
		t.Fatalf("resume lost the flags (%d):\n%s", n, got())
	}
}

func TestArtifactsWithoutFileServerGiveNoLink(t *testing.T) {
	a, got, p := artifactsApp(t)
	a.ArtifactsURL = ""
	if _, err := a.NewChat(p, ""); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got(), "<file name>") {
		t.Fatalf("link without a file server:\n%s", got())
	}
}

func TestStopKeepsArtifactsDeleteRemovesThem(t *testing.T) {
	a, _, p := artifactsApp(t)
	num, err := a.NewChat(p, "")
	if err != nil {
		t.Fatal(err)
	}
	page := filepath.Join(a.ArtifactsDir(p.Name, num), "report.html")
	if err := os.WriteFile(page, []byte("<p>x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := a.Stop(p, num); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(page); err != nil {
		t.Fatalf("stop removed the page: %v", err)
	}
	if err := a.DeleteSession(p, num, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(a.ArtifactsDir(p.Name, num)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("folder left after delete: %v", err)
	}
}

func TestCloseStoryRemovesArtifacts(t *testing.T) {
	a, _, p := artifactsApp(t)
	if _, err := a.NewStory(context.Background(), p, StoryOpts{Name: "s1"}); err != nil {
		t.Fatal(err)
	}
	num, err := a.NewSession(p, "s1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a.ArtifactsDir(p.Name, num), "a.html"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.CloseStory(p, "s1", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(a.ArtifactsDir(p.Name, num)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("folder left after close: %v", err)
	}
}

func TestSessionGetsPageHook(t *testing.T) {
	a, _, p := artifactsApp(t)
	a.Self = "/usr/local/bin/bx"
	a.APIURL = "http://100.64.0.1:7357"
	o := a.options(p, 1, &state.Session{})
	const hook = `"PostToolUse":[{"hooks":[{"command":"/usr/local/bin/bx page-hook http://100.64.0.1:7357","timeout":5,"type":"command"}],"matcher":"Write|Edit|MultiEdit"}]`
	if !strings.Contains(o.Settings, hook) {
		t.Fatalf("no page hook: %s", o.Settings)
	}
	a.APIURL = ""
	if o := a.options(p, 1, &state.Session{}); strings.Contains(o.Settings, "PostToolUse") {
		t.Fatalf("page hook without bx serve's address: %s", o.Settings)
	}
}

func TestWithHooksKeepsBoth(t *testing.T) {
	guard := map[string]any{"PreToolUse": []any{"guard"}, "PostToolUse": []any{"other"}}
	got := withHooks(guard, pageHooks("/bx", "http://h:1"))
	if len(got["PreToolUse"].([]any)) != 1 || len(got["PostToolUse"].([]any)) != 2 || len(guard["PostToolUse"].([]any)) != 1 {
		t.Fatalf("%v (in %v)", got, guard)
	}
	if got := withHooks(nil, pageHooks("/bx", "http://h:1")); len(got["PostToolUse"].([]any)) != 1 {
		t.Fatalf("%v", got)
	}
}
