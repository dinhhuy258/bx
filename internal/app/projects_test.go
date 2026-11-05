package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dinhhuy258/bx/internal/state"
)

func TestProjectName(t *testing.T) {
	for in, want := range map[string]string{"Home Network": "home-network", "/x/My.Repo": "my.repo", "owner/bx": "bx", "-odd_": "odd_"} {
		if got := ProjectName(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}

func TestAddFolder(t *testing.T) {
	a, _, p := newApp(t)
	if _, err := a.AddFolder("rel/path"); err == nil {
		t.Fatal("relative path accepted")
	}
	if _, err := a.AddFolder(filepath.Join(t.TempDir(), "gone")); err == nil || !strings.Contains(err.Error(), "not a folder") {
		t.Fatalf("missing folder: %v", err)
	}
	ws := filepath.Join(t.TempDir(), "Work")
	os.Mkdir(ws, 0o755)
	if name, err := a.AddFolder(ws); err != nil || name != "work" {
		t.Fatalf("plain folder: %q %v", name, err)
	}
	if got, _ := a.LoadProject("work"); !got.IsWorkspace() {
		t.Fatalf("kind = %q", got.Kind)
	}
	other := filepath.Join(t.TempDir(), "Lab-Tools")
	if err := a.Git.Init(other); err != nil {
		t.Fatal(err)
	}
	name, err := a.AddFolder(other)
	if err != nil || name != "lab-tools" {
		t.Fatalf("%q %v", name, err)
	}
	got, err := a.LoadProject("lab-tools")
	if err != nil || got.Path != other || got.IsWorkspace() {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := a.AddFolder(p.Path); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate: %v", err)
	}
}

func TestNewProject(t *testing.T) {
	a, _, _ := newApp(t)
	name, err := a.NewProject("scratch")
	if err != nil || name != "scratch" {
		t.Fatal(name, err)
	}
	if !a.Git.IsRepo(a.Cfg.ProjectDir("scratch")) {
		t.Fatal("no repo")
	}
	if _, err := a.NewProject("Bad Name"); err == nil {
		t.Fatal("bad name accepted")
	}
}

func TestCloneProjectUsesGH(t *testing.T) {
	a, _, _ := newApp(t)
	var got []string
	a.GH = func(_ context.Context, _ string, args ...string) (string, error) {
		got = args
		return "", a.Git.Init(args[3])
	}
	if _, err := a.CloneProject("not a repo"); err == nil || got != nil {
		t.Fatal("bad repo accepted")
	}
	name, err := a.CloneProject("dinhhuy258/dotfiles")
	if err != nil || name != "dotfiles" {
		t.Fatal(name, err)
	}
	if strings.Join(got, " ") != "repo clone dinhhuy258/dotfiles "+a.Cfg.ProjectDir("dotfiles") {
		t.Fatalf("gh %q", got)
	}
	if _, err := os.Stat(a.Cfg.StateFile("dotfiles")); err != nil {
		t.Fatal(err)
	}
}

func TestGitHubRepos(t *testing.T) {
	a, _, _ := newApp(t)
	a.GH = func(_ context.Context, _ string, args ...string) (string, error) { return "a/b\nc/d\n", nil }
	repos, err := a.GitHubRepos()
	if err != nil || strings.Join(repos, ",") != "a/b,c/d" {
		t.Fatal(repos, err)
	}
}

// addRepoProject registers a git repo project called name in the projects folder.
func addRepoProject(t *testing.T, a *App, name string) string {
	t.Helper()
	dir := a.Cfg.ProjectDir(name)
	if err := a.Git.Init(dir); err != nil {
		t.Fatal(err)
	}
	if err := a.register(name, dir, state.KindRepo); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestNewWorkspace(t *testing.T) {
	a, _, _ := newApp(t)
	api := addRepoProject(t, a, "api")
	addRepoProject(t, a, "web")
	name, err := a.NewWorkspace("shop", []string{"api", "web"})
	if err != nil || name != "shop" {
		t.Fatalf("%q %v", name, err)
	}
	p, _ := a.LoadProject("shop")
	if !p.IsWorkspace() || p.Path != a.Cfg.ProjectDir("shop") {
		t.Fatalf("%+v", p)
	}
	if target, _ := os.Readlink(filepath.Join(p.Path, "api")); target != api {
		t.Fatalf("api -> %q", target)
	}
	if got, _ := a.WorkspaceRepos(p); strings.Join(got, " ") != "api web" {
		t.Fatalf("repos = %v", got)
	}
	for _, c := range []struct {
		name  string
		picks []string
		want  string
	}{
		{"one", []string{"api"}, "at least 2"},
		{"dup", []string{"api", "api"}, "twice"},
		{"nope", []string{"api", "ghost"}, "no project"},
		{"ws", []string{"api", "shop"}, "is a workspace"},
		{"shop", []string{"api", "web"}, "already exists"},
		{"Bad Name", []string{"api", "web"}, "project name"},
	} {
		if _, err := a.NewWorkspace(c.name, c.picks); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.want)
		}
		if c.name != "shop" {
			if _, err := os.Lstat(a.Cfg.ProjectDir(c.name)); err == nil {
				t.Errorf("%s: folder left behind", c.name)
			}
		}
	}
}
