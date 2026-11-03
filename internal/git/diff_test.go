package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMergeBaseAndOutput(t *testing.T) {
	repo := initRepo(t)
	g := New()
	ctx := context.Background()
	main, err := g.Output(ctx, repo, nil, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("checkout", "-q", "-b", "feat/x")
	os.WriteFile(filepath.Join(repo, "b.txt"), []byte("b\n"), 0o644)
	run("add", "b.txt")
	run("commit", "-q", "-m", "b")
	mb, err := g.MergeBase(ctx, repo, "main")
	if err != nil || mb != strings.TrimSpace(string(main)) {
		t.Fatalf("merge-base = %q %v, want %s", mb, err, main)
	}
	if _, err := g.MergeBase(ctx, repo, "nope"); err == nil {
		t.Fatal("merge-base with a missing branch must fail")
	}
	// stderr stays out of the output and goes into the error
	_, err = g.Output(ctx, repo, nil, "rev-parse", "--verify", "nope")
	if err == nil || !strings.Contains(err.Error(), "rev-parse --verify nope") {
		t.Fatalf("err = %v", err)
	}
	// env reaches git
	out, err := g.Output(ctx, repo, []string{"GIT_INDEX_FILE=" + filepath.Join(t.TempDir(), "none")}, "ls-files")
	if err != nil || len(out) != 0 {
		t.Fatalf("ls-files with an empty index = %q %v", out, err)
	}
}
