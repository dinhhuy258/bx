package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o644)
	run("add", "a.txt")
	run("commit", "-q", "-m", "init")
	return dir
}

func TestWorktreeAddDiffRemove(t *testing.T) {
	repo := initRepo(t)
	g := New()
	wt := filepath.Join(t.TempDir(), "worktrees", "proj", "story-1")
	if err := g.WorktreeAdd(repo, wt, "feat/story-1", "main"); err != nil {
		t.Fatal(err)
	}
	if b, _ := g.CurrentBranch(wt); b != "feat/story-1" {
		t.Fatalf("branch = %q", b)
	}
	os.WriteFile(filepath.Join(wt, "a.txt"), []byte("one\ntwo\nthree\n"), 0o644)
	if n, _ := g.DirtyCount(wt); n != 1 {
		t.Fatalf("dirty = %d", n)
	}
	cmd := exec.Command("git", "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qam", "more")
	cmd.Dir = wt
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	add, del, err := g.ShortStat(wt, "main")
	if err != nil || add != 2 || del != 0 {
		t.Fatalf("shortstat = +%d -%d err %v", add, del, err)
	}
	if err := g.WorktreeAdd(repo, wt+"-dup", "feat/story-1", "main"); err == nil {
		t.Fatal("second worktree on the same branch must fail")
	}
	if err := g.WorktreeRemove(repo, wt, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatal("worktree folder still exists")
	}
	if err := g.WorktreePrune(repo); err != nil {
		t.Fatal(err)
	}
}

func TestLocalDefault(t *testing.T) {
	repo := initRepo(t)
	g := New()
	if b, err := g.LocalDefault(repo); err != nil || b != "main" {
		t.Fatalf("default = %q err %v", b, err)
	}
	gitRun(t, repo, "branch", "-m", "main", "develop")
	gitRun(t, repo, "branch", "trunk")
	if b, err := g.LocalDefault(repo); err != nil || b != "develop" {
		t.Fatalf("develop-only repo: %q err %v", b, err)
	}
	gitRun(t, repo, "branch", "master")
	if b, _ := g.LocalDefault(repo); b != "master" {
		t.Fatalf("master before develop: %q", b)
	}
	gitRun(t, repo, "checkout", "-q", "-b", "other")
	gitRun(t, repo, "branch", "-D", "master", "develop", "trunk")
	if _, err := g.LocalDefault(repo); err == nil {
		t.Fatal("no default branch found one")
	}
}

func TestInitMakesMain(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "new")
	g := New()
	if err := g.Init(dest); err != nil {
		t.Fatal(err)
	}
	if b, err := g.LocalDefault(dest); err != nil || b != "main" {
		t.Fatalf("default = %q err %v", b, err)
	}
}

func TestSubRepos(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"api/.git", "web/.git", "web/inner/.git", "libs/auth/.git", "a/b/c/.git", ".hidden/x/.git", "node_modules/y/.git", "docs"} {
		os.MkdirAll(filepath.Join(root, d), 0o755)
	}
	os.WriteFile(filepath.Join(root, "tool"), nil, 0o644)
	os.MkdirAll(filepath.Join(root, "wt"), 0o755)
	os.WriteFile(filepath.Join(root, "wt", ".git"), []byte("gitdir: x\n"), 0o644) // a worktree's .git file
	other := t.TempDir()
	os.MkdirAll(filepath.Join(other, "svc", ".git"), 0o755)
	os.MkdirAll(filepath.Join(other, "group", "pay", ".git"), 0o755)
	os.Symlink(filepath.Join(root, "api"), filepath.Join(root, "api-link"))   // the same repo twice: listed once
	os.Symlink(filepath.Join(other, "svc"), filepath.Join(root, "svc"))       // a linked repo
	os.Symlink(filepath.Join(other, "group"), filepath.Join(root, "group"))   // a linked folder of repos
	os.Symlink(filepath.Join(other, "gone"), filepath.Join(root, "gone"))     // broken: skipped
	os.Symlink(root, filepath.Join(root, "docs", "loop"))                     // a loop: skipped
	os.Symlink(filepath.Join(root, "tool"), filepath.Join(root, "tool-link")) // a link to a file: skipped
	got, err := SubRepos(root, 2)
	if err != nil {
		t.Fatal(err)
	}
	want := "api group/pay libs/auth svc web wt"
	if s := strings.Join(got, " "); s != want {
		t.Fatalf("repos = %q, want %q", s, want)
	}
}
