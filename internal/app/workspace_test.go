package app

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dinhhuy258/bx/internal/settings"
	"github.com/dinhhuy258/bx/internal/state"
)

// newWorkspace makes a workspace project of repos api, web and libs/auth, a notes file and a
// docs folder.
func newWorkspace(t *testing.T) (*App, *state.Project) {
	t.Helper()
	a, _, _ := newApp(t)
	root := filepath.Join(t.TempDir(), "work")
	for _, rel := range []string{"api", "web", "libs/auth"} {
		dir := filepath.Join(root, rel)
		if err := a.Git.Init(dir); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644)
		os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".env*\n"), 0o644)
		gitIn(t, dir, "add", ".")
		gitIn(t, dir, "commit", "-qm", "code")
	}
	os.WriteFile(filepath.Join(root, "api", ".env"), []byte("KEY=1\n"), 0o600)
	os.WriteFile(filepath.Join(root, "notes.md"), []byte("notes\n"), 0o644)
	os.MkdirAll(filepath.Join(root, "docs"), 0o755)
	os.WriteFile(filepath.Join(root, "libs", "README.md"), []byte("libs\n"), 0o644)
	name, err := a.AddFolder(root)
	if err != nil {
		t.Fatal(err)
	}
	p, err := a.LoadProject(name)
	if err != nil {
		t.Fatal(err)
	}
	return a, p
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
	c.Dir = dir
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func isLink(t *testing.T, path string) bool {
	t.Helper()
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return fi.Mode()&os.ModeSymlink != 0
}

func TestWorkspaceRepos(t *testing.T) {
	a, p := newWorkspace(t)
	got, err := a.WorkspaceRepos(p)
	if err != nil || strings.Join(got, " ") != "api libs/auth web" {
		t.Fatalf("%v %v", got, err)
	}
}

func TestWorkspaceStoryMirror(t *testing.T) {
	a, p := newWorkspace(t)
	if _, err := a.NewStory(context.Background(), p, StoryOpts{Name: "login"}); err == nil {
		t.Fatal("story without repos")
	}
	if _, err := a.NewStory(context.Background(), p, StoryOpts{Name: "login", Repos: []string{"nope"}}); err == nil {
		t.Fatal("unknown repo accepted")
	}
	if _, err := a.NewStory(context.Background(), p, StoryOpts{Name: "login", Repos: []string{"api", "libs/auth"}}); err != nil {
		t.Fatal(err)
	}
	m := a.Dir(p, "login")
	if b, _ := a.Git.CurrentBranch(filepath.Join(m, "api")); b != "feat/login" {
		t.Fatalf("api branch = %q", b)
	}
	if b, _ := a.Git.CurrentBranch(filepath.Join(m, "libs", "auth")); b != "feat/login" {
		t.Fatalf("libs/auth branch = %q", b)
	}
	for _, rel := range []string{"web", "notes.md", "docs", "libs/README.md"} {
		if !isLink(t, filepath.Join(m, rel)) {
			t.Errorf("%s is not a link", rel)
		}
	}
	if isLink(t, filepath.Join(m, "libs")) || isLink(t, filepath.Join(m, "api")) {
		t.Fatal("folder on the way to a repo, or the repo, is a link")
	}
	if b, err := os.ReadFile(filepath.Join(m, "api", ".env")); err != nil || string(b) != "KEY=1\n" {
		t.Fatalf(".env = %q %v", b, err)
	}
	got, _ := a.LoadProject(p.Name)
	if r := got.Stories["login"].Repos; len(r) != 2 || r[0] != (state.StoryRepo{Rel: "api", Base: "main"}) {
		t.Fatalf("repos = %+v", r)
	}
}

func TestWorkspaceStoryRollsBack(t *testing.T) {
	a, p := newWorkspace(t)
	web := filepath.Join(p.Path, "web")
	gitIn(t, web, "branch", "feat/login")
	var be *BranchExistsError
	if _, err := a.NewStory(context.Background(), p, StoryOpts{Name: "login", Repos: []string{"api", "web"}}); !errors.As(err, &be) ||
		be.Where != "local" || strings.Join(be.Repos, ",") != "web" || err.Error() != "branch feat/login exists locally in web" {
		t.Fatalf("err = %v", err)
	}
	gitIn(t, web, "checkout", "-q", "feat/login") // web cannot take its branch: it is checked out
	_, err := a.NewStory(context.Background(), p, StoryOpts{Name: "login", Repos: []string{"api", "web"}, UseExisting: true})
	if err == nil || !strings.Contains(err.Error(), "web") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Lstat(a.Dir(p, "login")); !os.IsNotExist(err) {
		t.Fatal("story folder left behind")
	}
	if out := gitIn(t, filepath.Join(p.Path, "api"), "branch", "--list", "feat/login"); out != "" {
		t.Fatalf("api branch left behind: %q", out)
	}
	if out := gitIn(t, filepath.Join(p.Path, "web"), "branch", "--list", "feat/login"); out == "" {
		t.Fatal("web's own branch was deleted")
	}
	if out := gitIn(t, filepath.Join(p.Path, "api"), "worktree", "list"); strings.Count(out, "\n") != 0 {
		t.Fatalf("worktree left behind: %s", out)
	}
	if _, ok := p.Stories["login"]; ok {
		t.Fatal("story recorded")
	}
}

func TestWorkspaceCloseKeepsOriginals(t *testing.T) {
	a, p := newWorkspace(t)
	if _, err := a.NewStory(context.Background(), p, StoryOpts{Name: "login", Repos: []string{"api"}}); err != nil {
		t.Fatal(err)
	}
	m := a.Dir(p, "login")
	os.WriteFile(filepath.Join(m, "api", "new.go"), []byte("x\n"), 0o644)
	os.WriteFile(filepath.Join(m, "scratch.txt"), []byte("x\n"), 0o644)
	var d *DirtyError
	if err := a.CloseStory(p, "login", false); !errors.As(err, &d) || d.Files != 2 || d.Detail != "api: 1, outside the repos: 1" {
		t.Fatalf("err = %v", err)
	}
	if err := a.CloseStory(p, "login", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(m); !os.IsNotExist(err) {
		t.Fatal("story folder left behind")
	}
	for _, rel := range []string{"notes.md", "web/main.go", "libs/README.md", "api/.env", "api/main.go"} {
		if _, err := os.Stat(filepath.Join(p.Path, rel)); err != nil {
			t.Errorf("original %s: %v", rel, err)
		}
	}
	if out := gitIn(t, filepath.Join(p.Path, "api"), "branch", "--list", "feat/login"); out == "" {
		t.Fatal("branch deleted")
	}
	if _, ok := p.Stories["login"]; ok {
		t.Fatal("story still recorded")
	}
}

func TestAddStoryRepos(t *testing.T) {
	a, p := newWorkspace(t)
	if _, err := a.NewStory(context.Background(), p, StoryOpts{Name: "login", Repos: []string{"api"}}); err != nil {
		t.Fatal(err)
	}
	m := a.Dir(p, "login")
	if err := a.AddStoryRepos(p, "login", []string{"api"}, false); err == nil {
		t.Fatal("repo added twice")
	}
	os.WriteFile(filepath.Join(p.Path, "web", "wip.go"), []byte("x\n"), 0o644)
	var d *DirtyError
	if err := a.AddStoryRepos(p, "login", []string{"web"}, false); !errors.As(err, &d) || !d.Original || d.Detail != "web: 1" {
		t.Fatalf("err = %v", err)
	}
	if !isLink(t, filepath.Join(m, "web")) {
		t.Fatal("refused add changed the link")
	}
	if err := a.AddStoryRepos(p, "login", []string{"web", "libs/auth"}, true); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"web", "libs/auth"} {
		if b, _ := a.Git.CurrentBranch(filepath.Join(m, rel)); b != "feat/login" {
			t.Fatalf("%s branch = %q", rel, b)
		}
	}
	if isLink(t, filepath.Join(m, "libs")) || !isLink(t, filepath.Join(m, "libs", "README.md")) {
		t.Fatal("libs was not made a real folder of links")
	}
	if _, err := os.Lstat(filepath.Join(p.Path, "libs", "auth", "libs")); err == nil {
		t.Fatal("wrote into the original through a link")
	}
	got, _ := a.LoadProject(p.Name)
	if names := strings.Join(got.Stories["login"].RepoNames(), " "); names != "api web libs/auth" {
		t.Fatalf("repos = %q", names)
	}
}

func TestAddStoryRepoFailureRelinks(t *testing.T) {
	a, p := newWorkspace(t)
	if _, err := a.NewStory(context.Background(), p, StoryOpts{Name: "login", Repos: []string{"api"}}); err != nil {
		t.Fatal(err)
	}
	gitIn(t, filepath.Join(p.Path, "web"), "branch", "feat/login")
	if err := a.AddStoryRepos(p, "login", []string{"web"}, false); err == nil {
		t.Fatal("add over an existing branch")
	}
	if !isLink(t, filepath.Join(a.Dir(p, "login"), "web")) {
		t.Fatal("link not restored")
	}
}

func TestGuard(t *testing.T) {
	a, p := newWorkspace(t)
	if _, err := a.NewStory(context.Background(), p, StoryOpts{Name: "login", Repos: []string{"api"}}); err != nil {
		t.Fatal(err)
	}
	m := a.Dir(p, "login")
	for file, want := range map[string]string{
		"api/main.go":         "",
		"api/new/dir/file.go": "",
		"new.md":              "",
		"/elsewhere/x.go":     "",
		"web/main.go":         "web is not part of story login",
		"web/pkg/new.go":      "web is not part of story login",
		"libs/auth/main.go":   "libs/auth is not part of story login",
		"notes.md":            "notes.md is a link to the original workspace folder",
		"libs/README.md":      "libs is a link to the original workspace folder",
	} {
		if !filepath.IsAbs(file) {
			file = filepath.Join(m, file)
		}
		got := Guard(m, file)
		if want == "" && got != "" || want != "" && !strings.HasPrefix(got, want) {
			t.Errorf("%s: %q, want %q", file, got, want)
		}
	}
	if err := a.AddStoryRepos(p, "login", []string{"web"}, false); err != nil {
		t.Fatal(err)
	}
	if got := Guard(m, filepath.Join(m, "web", "main.go")); got != "" {
		t.Fatalf("after add: %q", got)
	}
}

func TestWorkspaceSessionGetsGuard(t *testing.T) {
	a, p := newWorkspace(t)
	a.Self = "/usr/local/bin/bx"
	if _, err := a.NewStory(context.Background(), p, StoryOpts{Name: "login", Repos: []string{"api"}}); err != nil {
		t.Fatal(err)
	}
	story := a.options(p, 1, &state.Session{Story: "login"})
	chat := a.options(p, 2, &state.Session{})
	if !strings.Contains(story.Settings, `"PreToolUse"`) || !strings.Contains(story.Settings, "/usr/local/bin/bx guard "+a.Dir(p, "login")) ||
		strings.Contains(chat.Settings, `"PreToolUse"`) {
		t.Fatalf("story %q chat %q", story.Settings, chat.Settings)
	}
	const status = `"statusLine":{"command":"/usr/local/bin/bx statusline","type":"command"}`
	if !strings.Contains(story.Settings, status) || !strings.Contains(chat.Settings, status) {
		t.Fatalf("no status line: story %q chat %q", story.Settings, chat.Settings)
	}
	a.Self = ""
	if none := a.options(p, 2, &state.Session{}); none.Settings != "" {
		t.Fatalf("no bx binary: %q", none.Settings)
	}
	a.Self = "/usr/local/bin/bx"
	a.PluginDir = "/opt/bx-plugin"
	both := a.options(p, 1, &state.Session{Story: "login"})
	if !strings.Contains(both.Settings, `"PreToolUse"`) || !strings.Contains(both.Settings, `"permissions"`) {
		t.Fatalf("guard and artifacts not merged: %q", both.Settings)
	}
}

func TestSessionGetsClaudeSettings(t *testing.T) {
	a, p := newWorkspace(t)
	var err error
	if a.Settings, err = settings.Open(filepath.Join(t.TempDir(), "settings.json"), func(string, ...any) {}); err != nil {
		t.Fatal(err)
	}
	if got := a.options(p, 1, &state.Session{}).Settings; got != "" {
		t.Fatalf("nothing set, no bx binary: %q", got)
	}
	if err := a.Settings.Set(map[string]string{"claude.commit": "none", "claude.pr": "custom", "claude.pr_text": "Made with bx"}); err != nil {
		t.Fatal(err)
	}
	const want = `"attribution":{"commit":"","pr":"Made with bx"}`
	if got := a.options(p, 1, &state.Session{}).Settings; got != "{"+want+"}" {
		t.Fatalf("alone: %q", got)
	}
	a.Self = "/usr/local/bin/bx"
	if got := a.options(p, 1, &state.Session{}).Settings; !strings.Contains(got, want) || !strings.Contains(got, `"statusLine"`) {
		t.Fatalf("with the status line: %q", got)
	}
}
