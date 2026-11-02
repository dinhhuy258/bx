package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dinhhuy258/bx/internal/state"
)

// upstream is a bare origin and a clone of it (seed) that pushes to it.
type upstream struct{ bare, seed string }

// withOrigin gives repo (on main) a bare origin holding main and develop, with develop as its
// HEAD, and records origin/HEAD in repo. repo gets a local develop, not checked out.
func withOrigin(t *testing.T, repo string) upstream {
	t.Helper()
	root := t.TempDir()
	u := upstream{bare: filepath.Join(root, "origin.git"), seed: filepath.Join(root, "seed")}
	gitIn(t, root, "init", "-q", "--bare", u.bare)
	gitIn(t, repo, "branch", "develop")
	gitIn(t, repo, "remote", "add", "origin", u.bare)
	gitIn(t, repo, "push", "-q", "origin", "main", "develop")
	gitIn(t, u.bare, "symbolic-ref", "HEAD", "refs/heads/develop")
	gitIn(t, repo, "remote", "set-head", "origin", "--auto")
	gitIn(t, root, "clone", "-q", u.bare, u.seed)
	return u
}

// push commits a new file on branch in seed (made from develop if new) and pushes it.
func (u upstream) push(t *testing.T, branch, file string) string {
	t.Helper()
	if out := gitIn(t, u.seed, "branch", "-a", "--list", branch, "origin/"+branch); out == "" {
		gitIn(t, u.seed, "checkout", "-q", "-b", branch, "origin/develop")
	} else {
		gitIn(t, u.seed, "checkout", "-q", branch)
	}
	sha := commitFile(t, u.seed, file)
	gitIn(t, u.seed, "push", "-q", "origin", branch)
	return sha
}

func commitFile(t *testing.T, dir, file string) string {
	t.Helper()
	os.WriteFile(filepath.Join(dir, file), []byte(file+"\n"), 0o644)
	gitIn(t, dir, "add", file)
	gitIn(t, dir, "commit", "-qm", file)
	return gitIn(t, dir, "rev-parse", "HEAD")
}

func newStory(t *testing.T, a *App, p *state.Project, o StoryOpts) *StoryResult {
	t.Helper()
	res, err := a.NewStory(context.Background(), p, o)
	if err != nil {
		t.Fatalf("%+v: %v", o, err)
	}
	return res
}

func TestStoryBaseDetectionOrder(t *testing.T) {
	a, _, p := newApp(t)
	if res := newStory(t, a, p, StoryOpts{Name: "s1"}); res.Message() != "Created s1 on feat/s1 ← main" {
		t.Fatalf("no remote: %q", res.Message())
	}
	withOrigin(t, p.Path)
	if res := newStory(t, a, p, StoryOpts{Name: "s2"}); res.Message() != "Created s2 on feat/s2 ← origin/develop" {
		t.Fatalf("origin/HEAD: %q", res.Message())
	}
	p.BaseOverride = "main"
	if res := newStory(t, a, p, StoryOpts{Name: "s3"}); res.Message() != "Created s3 on feat/s3 ← origin/main" {
		t.Fatalf("override: %q", res.Message())
	}
	p.BaseOverride = ""
	gitIn(t, p.Path, "remote", "set-head", "origin", "-d")
	if res := newStory(t, a, p, StoryOpts{Name: "s4"}); res.Message() != "Created s4 on feat/s4 ← origin/develop" {
		t.Fatalf("set-head: %q", res.Message())
	}
	if b, err := a.Git.OriginHead(p.Path); err != nil || b != "develop" {
		t.Fatalf("origin/HEAD not recorded: %q %v", b, err)
	}
	got, _ := a.LoadProject(p.Name)
	if got.Stories["s1"].Base != "main" || got.Stories["s2"].Base != "origin/develop" || got.Stories["s3"].Base != "origin/main" {
		t.Fatalf("bases %q %q %q", got.Stories["s1"].Base, got.Stories["s2"].Base, got.Stories["s3"].Base)
	}

	// no remote: develop, then trunk; nothing: the error
	b, _, q := newApp(t)
	gitIn(t, q.Path, "branch", "-m", "main", "trunk")
	if res := newStory(t, b, q, StoryOpts{Name: "t1"}); res.Message() != "Created t1 on feat/t1 ← trunk" {
		t.Fatalf("trunk: %q", res.Message())
	}
	gitIn(t, q.Path, "branch", "develop")
	if res := newStory(t, b, q, StoryOpts{Name: "t2"}); res.Message() != "Created t2 on feat/t2 ← develop" {
		t.Fatalf("develop before trunk: %q", res.Message())
	}
	gitIn(t, q.Path, "checkout", "-q", "-b", "other")
	gitIn(t, q.Path, "branch", "-D", "develop", "trunk")
	_, err := b.NewStory(context.Background(), q, StoryOpts{Name: "t3"})
	if err == nil || err.Error() != "no default branch in home-network · set a base override in settings › git" {
		t.Fatalf("err = %v", err)
	}
}

func TestStoryOverrideMissingInAWorkspaceRepo(t *testing.T) {
	a, p := newWorkspace(t)
	gitIn(t, filepath.Join(p.Path, "api"), "branch", "develop")
	p.BaseOverride = "develop"
	res := newStory(t, a, p, StoryOpts{Name: "login", Repos: []string{"api", "web"}})
	if want := "Created login on feat/login ← api: develop, web: main · override develop not in web · used main"; res.Message() != want {
		t.Fatalf("message %q, want %q", res.Message(), want)
	}
	got, _ := a.LoadProject(p.Name)
	if r := got.Stories["login"].Repos; r[0].Base != "develop" || r[1].Base != "main" {
		t.Fatalf("repos %+v", r)
	}
}

func TestStoryFetchesOnlyTheBaseAndHasNoUpstream(t *testing.T) {
	a, _, p := newApp(t)
	u := withOrigin(t, p.Path)
	dev := u.push(t, "develop", "d.txt")
	oldMain := gitIn(t, p.Path, "rev-parse", "origin/main")
	u.push(t, "main", "m.txt")
	newStory(t, a, p, StoryOpts{Name: "s1"})
	wt := a.Dir(p, "s1")
	if gitIn(t, wt, "rev-parse", "HEAD") != dev || gitIn(t, p.Path, "rev-parse", "origin/develop") != dev {
		t.Fatal("story not made from the fetched origin/develop")
	}
	if gitIn(t, p.Path, "rev-parse", "origin/main") != oldMain {
		t.Fatal("origin/main was fetched too")
	}
	if out, err := a.Git.CurrentBranch(wt); err != nil || out != "feat/s1" {
		t.Fatalf("branch %q %v", out, err)
	}
	if c := gitIn(t, p.Path, "config", "--list", "--local"); strings.Contains(c, "branch.feat/s1.") {
		t.Fatalf("new branch has an upstream: %s", c)
	}
}

func TestStoryFetchFailureFallsBack(t *testing.T) {
	a, _, p := newApp(t)
	withOrigin(t, p.Path)
	gitIn(t, p.Path, "fetch", "-q", "origin")
	gitIn(t, p.Path, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "gone.git"))
	res := newStory(t, a, p, StoryOpts{Name: "s1"})
	if !strings.HasPrefix(res.Message(), "Created s1 on feat/s1 ← origin/develop · fetch failed · using origin/develop from ") {
		t.Fatalf("message %q", res.Message())
	}
	for _, ref := range []string{"refs/remotes/origin/HEAD", "refs/remotes/origin/develop", "refs/remotes/origin/main"} {
		gitIn(t, p.Path, "update-ref", "-d", "--no-deref", ref)
	}
	res = newStory(t, a, p, StoryOpts{Name: "s2"})
	if res.Message() != "Created s2 on feat/s2 ← main · fetch failed · using local main" {
		t.Fatalf("message %q", res.Message())
	}
}

func TestStoryCancelledCreatesNothing(t *testing.T) {
	a, _, p := newApp(t)
	withOrigin(t, p.Path)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.NewStory(ctx, p, StoryOpts{Name: "s1"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(a.Dir(p, "s1")); !os.IsNotExist(err) {
		t.Fatal("worktree made")
	}
	if out := gitIn(t, p.Path, "branch", "--list", "feat/s1"); out != "" {
		t.Fatal("branch made")
	}
	if got, _ := a.LoadProject(p.Name); len(got.Stories) != 0 {
		t.Fatal("story recorded")
	}
}

func TestStoryFastForwardsTheLocalBase(t *testing.T) {
	a, _, p := newApp(t)
	u := withOrigin(t, p.Path)
	// behind, not checked out: updated
	sha := u.push(t, "develop", "a.txt")
	if res := newStory(t, a, p, StoryOpts{Name: "s1"}); len(res.Notes) != 0 {
		t.Fatalf("notes %q", res.Notes)
	}
	if gitIn(t, p.Path, "rev-parse", "develop") != sha {
		t.Fatal("local develop not fast-forwarded")
	}
	// checked out and clean: merged
	gitIn(t, p.Path, "checkout", "-q", "develop")
	sha = u.push(t, "develop", "b.txt")
	if res := newStory(t, a, p, StoryOpts{Name: "s2"}); len(res.Notes) != 0 {
		t.Fatalf("notes %q", res.Notes)
	}
	if gitIn(t, p.Path, "rev-parse", "HEAD") != sha {
		t.Fatal("checked-out develop not merged")
	}
	// checked out with changes: left alone
	os.WriteFile(filepath.Join(p.Path, "wip.txt"), []byte("x"), 0o644)
	u.push(t, "develop", "c.txt")
	res := newStory(t, a, p, StoryOpts{Name: "s3"})
	if strings.Join(res.Notes, "|") != "local develop has uncommitted changes · not updated" || gitIn(t, p.Path, "rev-parse", "HEAD") != sha {
		t.Fatalf("dirty: notes %q", res.Notes)
	}
	if err := os.Remove(filepath.Join(p.Path, "wip.txt")); err != nil {
		t.Fatal(err)
	}
	// ahead of origin: left alone
	gitIn(t, p.Path, "merge", "-q", "--ff-only", "origin/develop")
	mine := commitFile(t, p.Path, "mine.txt")
	res = newStory(t, a, p, StoryOpts{Name: "s4"})
	if res.Message() != "Created s4 on feat/s4 ← origin/develop · local develop is 1 ahead of origin · not updated" || gitIn(t, p.Path, "rev-parse", "HEAD") != mine {
		t.Fatalf("ahead: %q", res.Message())
	}
}

func TestStoryNamesAndTypes(t *testing.T) {
	a, _, p := newApp(t)
	for _, c := range []struct {
		o             StoryOpts
		story, branch string
	}{
		{StoryOpts{Name: "MON-123 Sign in form"}, "mon-123-sign-in-form", "feat/mon-123-sign-in-form"},
		{StoryOpts{Name: "Fix/Login Timeout", Type: "docs"}, "login-timeout", "fix/login-timeout"},
		{StoryOpts{Name: "plain", Type: "none"}, "plain", "plain"},
		{StoryOpts{Name: "lease", Type: "chore"}, "lease", "chore/lease"},
		{StoryOpts{Name: "next"}, "next", "chore/next"}, // the last type
		{StoryOpts{Name: "team/x/deep"}, "deep", "team/x/deep"},
	} {
		res := newStory(t, a, p, c.o)
		if res.Story != c.story || res.Branch != c.branch || p.Stories[c.story].Branch != c.branch {
			t.Errorf("%+v: story %q branch %q", c.o, res.Story, res.Branch)
		}
	}
	got, _ := a.LoadProject(p.Name)
	if got.LastType != "chore" {
		t.Fatalf("last type %q", got.LastType)
	}
	_, err := a.NewStory(context.Background(), p, StoryOpts{Name: "login timeout", Type: "fix"})
	if err == nil || err.Error() != `story "login-timeout" already exists (branch fix/login-timeout)` {
		t.Fatalf("clash: %v", err)
	}
	if _, err := a.NewStory(context.Background(), p, StoryOpts{Name: "x", Type: "bogus"}); err == nil {
		t.Fatal("unknown type accepted")
	}
	if _, err := a.NewStory(context.Background(), p, StoryOpts{Name: "a..b"}); err == nil {
		t.Fatal("invalid branch accepted")
	}
}

func TestStoryBranchExists(t *testing.T) {
	a, _, p := newApp(t)
	u := withOrigin(t, p.Path)
	gitIn(t, p.Path, "branch", "feat/loc")
	u.push(t, "feat/rem", "r.txt")
	u.push(t, "feat/both", "b1.txt")
	gitIn(t, p.Path, "fetch", "-q", "origin", "feat/both")
	gitIn(t, p.Path, "branch", "feat/both", "origin/feat/both")
	u.push(t, "feat/both", "b2.txt")
	for name, want := range map[string]string{"loc": "local", "rem": "origin", "both": "both"} {
		var be *BranchExistsError
		if _, err := a.NewStory(context.Background(), p, StoryOpts{Name: name}); !errors.As(err, &be) || be.Where != want || be.Branch != "feat/"+name {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if err := (&BranchExistsError{Branch: "feat/both", Where: "both"}).Error(); err != "branch feat/both exists locally and on origin" {
		t.Fatal(err)
	}
	if res := newStory(t, a, p, StoryOpts{Name: "loc", UseExisting: true}); res.Message() != "Created loc on feat/loc" {
		t.Fatalf("local: %q", res.Message())
	}
	if gitIn(t, p.Path, "rev-parse", "feat/loc") != gitIn(t, p.Path, "rev-parse", "develop") {
		t.Fatal("local branch moved")
	}
	newStory(t, a, p, StoryOpts{Name: "rem", UseExisting: true})
	if up := gitIn(t, a.Dir(p, "rem"), "rev-parse", "--abbrev-ref", "@{upstream}"); up != "origin/feat/rem" {
		t.Fatalf("upstream %q", up)
	}
	if res := newStory(t, a, p, StoryOpts{Name: "both", UseExisting: true}); strings.Join(res.Notes, "|") != "local is 1 behind origin" {
		t.Fatalf("both: %q", res.Notes)
	}
	got, _ := a.LoadProject(p.Name)
	if got.Stories["rem"].Base != "origin/develop" || got.Stories["loc"].Base != "origin/develop" {
		t.Fatalf("bases %+v %+v", got.Stories["rem"], got.Stories["loc"])
	}
}

func TestStoryFromAnExistingBranch(t *testing.T) {
	a, _, p := newApp(t)
	u := withOrigin(t, p.Path)
	gitIn(t, p.Path, "branch", "topic/Local-One")
	sha := u.push(t, "fix/remote-one", "r.txt") // not fetched yet
	if res := newStory(t, a, p, StoryOpts{Source: SourceBranch, Branch: "topic/Local-One"}); res.Story != "local-one" || res.Message() != "Created local-one on topic/Local-One" {
		t.Fatalf("local: %+v %q", res, res.Message())
	}
	newStory(t, a, p, StoryOpts{Source: SourceBranch, Branch: "fix/remote-one"})
	wt := a.Dir(p, "remote-one")
	if gitIn(t, wt, "rev-parse", "HEAD") != sha || gitIn(t, wt, "rev-parse", "--abbrev-ref", "@{upstream}") != "origin/fix/remote-one" {
		t.Fatal("remote branch not tracked")
	}
	if _, err := a.NewStory(context.Background(), p, StoryOpts{Source: SourceBranch, Branch: "nope"}); err == nil || err.Error() != "no branch nope in home-network" {
		t.Fatalf("err = %v", err)
	}
	if _, err := a.NewStory(context.Background(), p, StoryOpts{Source: SourceBranch, Branch: "--upload-pack=x"}); err == nil {
		t.Fatal("option as branch accepted")
	}
}

// fakeGH answers gh by its args joined with spaces, and records the folder of each call.
type fakeGH struct {
	out  map[string]string // by dir + " " + args, or by args
	errs map[string]string
	dirs []string
}

func (f *fakeGH) run(dir string, args ...string) (string, error) {
	f.dirs = append(f.dirs, dir)
	key := strings.Join(args, " ")
	for _, k := range []string{dir + " " + key, key} {
		if e, ok := f.errs[k]; ok {
			return "", fmt.Errorf("gh %s: %s", key, e)
		}
		if o, ok := f.out[k]; ok {
			return o, nil
		}
	}
	return "", fmt.Errorf("gh %s: no reply", key)
}

func TestStoryFromAPullRequest(t *testing.T) {
	a, _, p := newApp(t)
	u := withOrigin(t, p.Path)
	same := u.push(t, "fix/login-timeout", "f.txt")
	// a fork PR whose head is the fork's main: only refs/pull/7/head on origin
	gitIn(t, u.seed, "checkout", "-q", "--detach", "origin/main")
	fork := commitFile(t, u.seed, "fork.txt")
	gitIn(t, u.seed, "push", "-q", "origin", "HEAD:refs/pull/7/head")
	gh := &fakeGH{out: map[string]string{
		"pr view 12 --json " + prFields: `{"number":12,"title":"Fix login","headRefName":"fix/login-timeout","isCrossRepository":false,"headRepositoryOwner":{"login":"acme"},"author":{"login":"alice"}}`,
		"pr view 7 --json " + prFields:  `{"number":7,"title":"Typo","headRefName":"main","isCrossRepository":true,"headRepositoryOwner":{"login":"bob"},"author":{"login":"bob"}}`,
	}}
	a.GH = gh.run
	res := newStory(t, a, p, StoryOpts{Source: SourcePR, PR: 12})
	wt := a.Dir(p, "login-timeout")
	if res.Branch != "fix/login-timeout" || gitIn(t, wt, "rev-parse", "HEAD") != same || gitIn(t, wt, "rev-parse", "--abbrev-ref", "@{upstream}") != "origin/fix/login-timeout" {
		t.Fatalf("same-repo PR: %+v", res)
	}
	if gh.dirs[0] != p.Path {
		t.Fatalf("gh ran in %q", gh.dirs[0])
	}
	res = newStory(t, a, p, StoryOpts{Source: SourcePR, PR: 7})
	if res.Story != "main" || res.Branch != "bob/main" || res.Message() != "Created main on bob/main · PR #7 is from a fork · changes won't be pushed to it" {
		t.Fatalf("fork PR: %q", res.Message())
	}
	if gitIn(t, a.Dir(p, "main"), "rev-parse", "HEAD") != fork {
		t.Fatal("fork PR head not checked out")
	}
	if gitIn(t, p.Path, "rev-parse", "main") == fork {
		t.Fatal("local main was moved to the fork's main")
	}
	if _, err := a.NewStory(context.Background(), p, StoryOpts{Source: SourcePR, PR: 9}); err == nil {
		t.Fatal("unknown PR accepted")
	}
}

func TestStoryFromAPullRequestInAWorkspace(t *testing.T) {
	a, p := newWorkspace(t)
	api := filepath.Join(p.Path, "api")
	u := withOrigin(t, api)
	u.push(t, "fix/x", "x.txt")
	a.GH = (&fakeGH{out: map[string]string{
		"pr view 3 --json " + prFields: `{"number":3,"title":"X","headRefName":"fix/x","isCrossRepository":false}`,
	}}).run
	res := newStory(t, a, p, StoryOpts{Source: SourcePR, PR: 3, PRRepo: "api", Repos: []string{"web"}})
	if res.Message() != "Created x on fix/x ← web: main" {
		t.Fatalf("message %q", res.Message())
	}
	got, _ := a.LoadProject(p.Name)
	if r := got.Stories["x"].Repos; len(r) != 2 || r[0] != (state.StoryRepo{Rel: "web", Base: "main"}) || r[1] != (state.StoryRepo{Rel: "api", Base: "origin/develop"}) {
		t.Fatalf("repos %+v", r)
	}
	// a repository added later starts from its origin's HEAD, as last fetched
	if err := a.AddStoryRepos(p, "x", []string{"libs/auth"}, false); err != nil {
		t.Fatal(err)
	}
	if b, err := a.offlineBase(p, api, "api"); err != nil || b != "origin/develop" {
		t.Fatalf("offline base %q %v", b, err)
	}
}

func TestWorkspacePullRequests(t *testing.T) {
	a, p := newWorkspace(t)
	gitIn(t, filepath.Join(p.Path, "api"), "remote", "add", "origin", "git@github.com:acme/api.git")
	gitIn(t, filepath.Join(p.Path, "web"), "remote", "add", "origin", "https://github.com/other-org/web")
	api, web, auth := filepath.Join(p.Path, "api"), filepath.Join(p.Path, "web"), filepath.Join(p.Path, "libs", "auth")
	list := "pr list --state open --limit 50 --json " + prFields
	view := "pr view 5 --json " + prFields
	gh := &fakeGH{
		out: map[string]string{
			api + " " + list:  `[{"number":5,"title":"Fix login","author":{"login":"alice"},"headRefName":"fix/login","isCrossRepository":false,"headRepositoryOwner":{"login":"acme"},"state":"OPEN"}]`,
			auth + " " + list: `[]`,
			api + " " + view:  `{"number":5,"title":"Fix login","author":{"login":"alice"},"headRefName":"fix/login"}`,
			"pr list --search login --state all --limit 50 --json " + prFields: `[]`,
		},
		errs: map[string]string{web + " " + list: "other-org requires SSO authorization", web + " " + view: "no pull requests found"},
	}
	a.GH = gh.run
	got, err := a.PullRequests(p, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.PRs) != 1 || got.PRs[0] != (PullRequest{Number: 5, Title: "Fix login", Author: "alice", Branch: "fix/login", Owner: "acme", Repo: "api", Slug: "acme/api", State: "OPEN"}) {
		t.Fatalf("prs %+v", got.PRs)
	}
	if strings.Join(got.Errors, "|") != "other-org/web: other-org requires SSO authorization" || got.Direct {
		t.Fatalf("errors %q", got.Errors)
	}
	got, _ = a.PullRequests(p, "#5")
	if !got.Direct || len(got.PRs) != 1 || got.PRs[0].Repo != "api" || len(got.Errors) != 0 {
		t.Fatalf("direct %+v", got)
	}
	gh.dirs = nil
	got, _ = a.PullRequests(p, "https://github.com/acme/api/pull/5/files")
	if !got.Direct || len(got.PRs) != 1 || strings.Join(gh.dirs, " ") != api {
		t.Fatalf("url %+v, asked %q", got, gh.dirs)
	}
	if got, _ = a.PullRequests(p, "https://github.com/zzz/api/pull/5"); len(got.PRs) != 0 || len(got.Errors) != 1 {
		t.Fatalf("url of another repo %+v", got)
	}
	got, _ = a.PullRequests(p, "login")
	if len(got.PRs) != 0 || len(got.Errors) != 0 || got.Direct {
		t.Fatalf("search %+v", got)
	}
}

func TestStoryCopiesEnvAndRecordsBase(t *testing.T) {
	a, _, p := newApp(t)
	os.WriteFile(filepath.Join(p.Path, ".env"), []byte("KEY=1\n"), 0o600)
	newStory(t, a, p, StoryOpts{Name: "s1"})
	if b, err := os.ReadFile(filepath.Join(a.Dir(p, "s1"), ".env")); err != nil || string(b) != "KEY=1\n" {
		t.Fatalf(".env = %q %v", b, err)
	}
	got, _ := a.LoadProject(p.Name)
	if got.Stories["s1"].Base != "main" {
		t.Fatalf("base %q", got.Stories["s1"].Base)
	}
}

func TestStoryInfoBranchesAndOverride(t *testing.T) {
	a, _, p := newApp(t)
	withOrigin(t, p.Path)
	info, err := a.StoryInfo(context.Background(), p)
	if err != nil || info.Base != "origin/develop" || info.Detected != "origin/develop" || info.Override != "" || len(info.Types) != 7 {
		t.Fatalf("%+v %v", info, err)
	}
	if err := a.SetBaseOverride(p, "nope"); err == nil || err.Error() != "no branch nope in home-network" {
		t.Fatalf("err = %v", err)
	}
	if err := a.SetBaseOverride(p, "main"); err != nil {
		t.Fatal(err)
	}
	got, _ := a.LoadProject(p.Name)
	if got.BaseOverride != "main" {
		t.Fatal("override not saved")
	}
	info, _ = a.StoryInfo(context.Background(), got)
	if info.Base != "origin/main" || info.Detected != "origin/develop" || info.Override != "main" {
		t.Fatalf("%+v", info)
	}
	if err := a.SetBaseOverride(got, ""); err != nil || got.BaseOverride != "" {
		t.Fatal(err)
	}
	gitIn(t, p.Path, "branch", "mine")
	bl, err := a.Branches(context.Background(), p, true)
	if err != nil || bl.Note != "" {
		t.Fatalf("%+v %v", bl, err)
	}
	seen := map[string]string{}
	for _, b := range bl.Branches {
		seen[b.Name] = fmt.Sprint(b.Local, b.Remote)
	}
	if seen["develop"] != "true true" || seen["main"] != "true true" || seen["mine"] != "true false" || len(seen) != 3 {
		t.Fatalf("branches %v", seen)
	}
	gitIn(t, p.Path, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "gone.git"))
	if bl, _ = a.Branches(context.Background(), p, true); bl.Note != "fetch failed · showing the last fetched branches" || len(bl.Branches) != 3 {
		t.Fatalf("%+v", bl)
	}
}
