package app

import (
	"os"
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
