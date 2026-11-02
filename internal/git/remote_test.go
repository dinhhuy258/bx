package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// gitRun runs git in dir with a fixed identity and returns its trimmed output.
func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// commit writes file in dir and commits it.
func commit(t *testing.T, dir, file string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(file+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", file)
	gitRun(t, dir, "commit", "-qm", file)
	return gitRun(t, dir, "rev-parse", "HEAD")
}

// remote is a bare origin with branches main and develop, HEAD develop; seed pushes to it and
// clone is a clone of it, on develop.
type remote struct{ origin, seed, clone string }

func cloneOf(t *testing.T) remote {
	t.Helper()
	r := remote{seed: initRepo(t)}
	root := t.TempDir()
	r.origin, r.clone = filepath.Join(root, "origin.git"), filepath.Join(root, "clone")
	gitRun(t, root, "init", "-q", "--bare", r.origin)
	gitRun(t, r.seed, "branch", "develop")
	gitRun(t, r.seed, "remote", "add", "origin", r.origin)
	gitRun(t, r.seed, "push", "-q", "origin", "main", "develop")
	gitRun(t, r.origin, "symbolic-ref", "HEAD", "refs/heads/develop")
	gitRun(t, root, "clone", "-q", r.origin, r.clone)
	return r
}

// push commits file on branch in seed and pushes it.
func (r remote) push(t *testing.T, branch, file string) string {
	t.Helper()
	gitRun(t, r.seed, "checkout", "-q", branch)
	sha := commit(t, r.seed, file)
	gitRun(t, r.seed, "push", "-q", "origin", branch)
	return sha
}

func rev(t *testing.T, dir, ref string) string { return gitRun(t, dir, "rev-parse", ref) }

func TestOriginHeadAndSetHeadAuto(t *testing.T) {
	r, g := cloneOf(t), New()
	if !g.HasRemote(r.clone) || g.HasRemote(initRepo(t)) {
		t.Fatal("HasRemote")
	}
	if b, err := g.OriginHead(r.clone); err != nil || b != "develop" {
		t.Fatalf("origin head = %q %v", b, err)
	}
	gitRun(t, r.clone, "remote", "set-head", "origin", "-d")
	if _, err := g.OriginHead(r.clone); err == nil {
		t.Fatal("unset origin/HEAD found")
	}
	if err := g.SetHeadAuto(context.Background(), r.clone); err != nil {
		t.Fatal(err)
	}
	if b, err := g.OriginHead(r.clone); err != nil || b != "develop" {
		t.Fatalf("after set-head: %q %v", b, err)
	}
}

func TestFetchOnlyTheBase(t *testing.T) {
	r, g := cloneOf(t), New()
	dev := r.push(t, "develop", "d.txt")
	oldMain := rev(t, r.clone, "origin/main")
	r.push(t, "main", "m.txt")
	if err := g.Fetch(context.Background(), r.clone, "develop"); err != nil {
		t.Fatal(err)
	}
	if rev(t, r.clone, "origin/develop") != dev {
		t.Fatal("origin/develop not updated")
	}
	if rev(t, r.clone, "origin/main") != oldMain {
		t.Fatal("fetch of develop updated origin/main")
	}
	if at, err := g.RefTime(r.clone, "refs/remotes/origin/develop"); err != nil || time.Since(at) > time.Minute {
		t.Fatalf("ref time %v %v", at, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := g.Fetch(ctx, r.clone, "main"); err == nil {
		t.Fatal("cancelled fetch succeeded")
	}
	if ok, err := g.LsRemoteBranch(context.Background(), r.clone, "develop"); !ok || err != nil {
		t.Fatalf("ls-remote develop: %v %v", ok, err)
	}
	if ok, err := g.LsRemoteBranch(context.Background(), r.clone, "dev"); ok || err != nil {
		t.Fatalf("ls-remote dev: %v %v", ok, err)
	}
}

func TestFastForwardAndMerge(t *testing.T) {
	r, g := cloneOf(t), New()
	gitRun(t, r.clone, "branch", "main", "origin/main") // not checked out
	r.push(t, "main", "m.txt")
	dev := r.push(t, "develop", "d.txt")
	if err := g.Fetch(context.Background(), r.clone, "main", "develop"); err != nil {
		t.Fatal(err)
	}
	if a, b, err := g.AheadBehind(r.clone, "refs/heads/main", "refs/remotes/origin/main"); err != nil || a != 0 || b != 1 {
		t.Fatalf("ahead %d behind %d %v", a, b, err)
	}
	if !g.IsAncestor(r.clone, "main", "origin/main") || g.IsAncestor(r.clone, "origin/main", "main") {
		t.Fatal("IsAncestor")
	}
	if err := g.FastForward(r.clone, "main"); err != nil {
		t.Fatal(err)
	}
	if rev(t, r.clone, "main") != rev(t, r.clone, "origin/main") {
		t.Fatal("main not fast-forwarded")
	}
	// checked out develop: fetch . refuses it, merge --ff-only moves it
	if err := g.FastForward(r.clone, "develop"); err == nil {
		t.Fatal("fast-forwarded a checked-out branch")
	}
	if dir, ok, err := g.WorktreeOf(r.clone, "develop"); err != nil || !ok || !sameDir(dir, r.clone) {
		t.Fatalf("worktree of develop = %q %v %v", dir, ok, err)
	}
	if _, ok, _ := g.WorktreeOf(r.clone, "main"); ok {
		t.Fatal("main is not checked out")
	}
	if err := g.MergeFFOnly(r.clone, "origin/develop"); err != nil {
		t.Fatal(err)
	}
	if rev(t, r.clone, "HEAD") != dev {
		t.Fatal("develop not merged")
	}
	// a local commit and a new one on origin: not a fast-forward, refused, main kept
	gitRun(t, r.clone, "checkout", "-q", "main")
	mine := commit(t, r.clone, "mine.txt")
	gitRun(t, r.clone, "checkout", "-q", "develop")
	r.push(t, "main", "m2.txt")
	if err := g.Fetch(context.Background(), r.clone, "main"); err != nil {
		t.Fatal(err)
	}
	if err := g.FastForward(r.clone, "main"); err == nil {
		t.Fatal("non-fast-forward accepted")
	}
	if rev(t, r.clone, "main") != mine {
		t.Fatal("main moved")
	}
	if a, b, _ := g.AheadBehind(r.clone, "main", "origin/main"); a != 1 || b != 1 {
		t.Fatalf("ahead %d behind %d", a, b)
	}
}

func sameDir(a, b string) bool {
	ra, _ := filepath.EvalSymlinks(a)
	rb, _ := filepath.EvalSymlinks(b)
	return ra == rb
}

func TestWorktreeAddTracking(t *testing.T) {
	r, g := cloneOf(t), New()
	root := t.TempDir()
	if err := g.WorktreeAdd(r.clone, filepath.Join(root, "a"), "feat/a", "origin/develop"); err != nil {
		t.Fatal(err)
	}
	if out, err := g.run(r.clone, "rev-parse", "--abbrev-ref", "feat/a@{upstream}"); err == nil {
		t.Fatalf("new branch tracks %q", out)
	}
	if err := g.WorktreeAddTrack(r.clone, filepath.Join(root, "b"), "main", "origin/main"); err != nil {
		t.Fatal(err)
	}
	if up := gitRun(t, r.clone, "rev-parse", "--abbrev-ref", "main@{upstream}"); up != "origin/main" {
		t.Fatalf("upstream = %q", up)
	}
	gitRun(t, r.clone, "branch", "local-only", "develop")
	wt := filepath.Join(root, "c")
	if err := g.WorktreeAddExisting(r.clone, wt, "local-only"); err != nil {
		t.Fatal(err)
	}
	if b, _ := g.CurrentBranch(wt); b != "local-only" {
		t.Fatalf("branch = %q", b)
	}
	if dir, ok, _ := g.WorktreeOf(r.clone, "local-only"); !ok || !sameDir(dir, wt) {
		t.Fatalf("worktree of local-only = %q %v", dir, ok)
	}
}

// commitAt commits file in dir with a committer date days after the first commit.
func commitAt(t *testing.T, dir, file string, days int) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(file+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", file)
	date := time.Now().Add(time.Duration(days) * 24 * time.Hour).Format(time.RFC3339)
	cmd := exec.Command("git", "commit", "-qm", file)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", "GIT_COMMITTER_DATE="+date)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("commit: %v %s", err, out)
	}
}

func TestBranches(t *testing.T) {
	r, g := cloneOf(t), New()
	gitRun(t, r.seed, "checkout", "-q", "main")
	commitAt(t, r.seed, "m.txt", 1)
	gitRun(t, r.seed, "checkout", "-q", "-b", "fix/x")
	commitAt(t, r.seed, "x.txt", 2)
	gitRun(t, r.seed, "push", "-q", "origin", "main", "fix/x")
	if err := g.FetchPrune(context.Background(), r.clone); err != nil {
		t.Fatal(err)
	}
	gitRun(t, r.clone, "checkout", "-q", "-b", "mine")
	commitAt(t, r.clone, "mine.txt", 3)
	bs, err := g.Branches(r.clone)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, b := range bs {
		s := b.Name
		if b.Local {
			s += " local"
		}
		if b.Remote {
			s += " remote"
		}
		got = append(got, s)
	}
	if want := "mine local|fix/x remote|main remote|develop local remote"; strings.Join(got, "|") != want {
		t.Fatalf("branches %q, want %q", strings.Join(got, "|"), want)
	}
}

func TestOriginSlug(t *testing.T) {
	for in, want := range map[string]string{
		"git@github.com:acme/api.git":         "acme/api",
		"https://github.com/acme/web":         "acme/web",
		"https://github.com/acme/web.git/":    "acme/web",
		"ssh://git@ghe.corp.io/team/tool.git": "team/tool",
		"/tmp/x/origin.git":                   "",
		"file:///tmp/a/b":                     "",
		"https://github.com/acme":             "",
	} {
		if got := slugOf(in); got != want {
			t.Errorf("slugOf(%q) = %q, want %q", in, got, want)
		}
	}
	r, g := cloneOf(t), New()
	if s := g.OriginSlug(r.clone); s != "" {
		t.Fatalf("local origin slug = %q", s)
	}
	gitRun(t, r.clone, "remote", "set-url", "origin", "git@github.com:a/b.git")
	if s := g.OriginSlug(r.clone); s != "a/b" {
		t.Fatalf("slug = %q", s)
	}
}

func TestValidBranch(t *testing.T) {
	g := New()
	for _, ok := range []string{"fix/login-timeout", "release/1.4", "x"} {
		if err := g.ValidBranch(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "-x", "a..b", "a/", "x.lock", "a b"} {
		if err := g.ValidBranch(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
