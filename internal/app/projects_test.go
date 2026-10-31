package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	if _, err := a.AddFolder(t.TempDir()); err == nil || !strings.Contains(err.Error(), "not a git repository") {
		t.Fatalf("plain folder: %v", err)
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
	if err != nil || got.Path != other {
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
	a.GH = func(args ...string) (string, error) {
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
	a.GH = func(args ...string) (string, error) { return "a/b\nc/d\n", nil }
	repos, err := a.GitHubRepos()
	if err != nil || strings.Join(repos, ",") != "a/b,c/d" {
		t.Fatal(repos, err)
	}
}
