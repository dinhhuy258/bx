package app

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/dinhhuy258/bx/internal/state"
)

// syncStory runs a whole sync of story ("" = the project folder) and returns its message.
func syncStory(t *testing.T, a *App, p *state.Project, story string) string {
	t.Helper()
	plan, err := a.PrepareSync(context.Background(), p, story)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := a.ApplySync(p, plan)
	if err != nil {
		t.Fatal(err)
	}
	return msg
}

func TestSyncFastForwardsOnlyWhenSafe(t *testing.T) {
	a, _, p := newApp(t)
	u := withOrigin(t, p.Path)
	newStory(t, a, p, StoryOpts{Name: "s1"})
	dir := a.Dir(p, "s1")
	if got := syncStory(t, a, p, "s1"); got != "s1 isn't on origin yet · nothing to sync" {
		t.Fatalf("not pushed: %q", got)
	}
	gitIn(t, dir, "push", "-q", "origin", "feat/s1")
	gitIn(t, u.seed, "fetch", "-q", "origin")
	gitIn(t, u.seed, "checkout", "-q", "-b", "feat/s1", "origin/feat/s1")
	pushSeed := func(file string) string {
		sha := commitFile(t, u.seed, file)
		gitIn(t, u.seed, "push", "-q", "origin", "feat/s1")
		return sha
	}

	sha := pushSeed("a.txt") // someone else pushed: fast-forwarded
	if got := syncStory(t, a, p, "s1"); got != "s1 synced · 1 new commit from origin" {
		t.Fatalf("behind: %q", got)
	}
	if gitIn(t, dir, "rev-parse", "HEAD") != sha {
		t.Fatal("not fast-forwarded")
	}
	if got := syncStory(t, a, p, "s1"); got != "s1 is up to date" {
		t.Fatalf("again: %q", got)
	}

	pushSeed("b.txt") // uncommitted files: left alone
	if err := os.WriteFile(filepath.Join(dir, "wip.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := syncStory(t, a, p, "s1"); got != "s1 has 1 uncommitted file · not synced" || gitIn(t, dir, "rev-parse", "HEAD") != sha {
		t.Fatalf("dirty: %q", got)
	}
	if err := os.Remove(filepath.Join(dir, "wip.txt")); err != nil {
		t.Fatal(err)
	}

	mine := commitFile(t, dir, "mine.txt") // commits on both sides: left alone
	if got := syncStory(t, a, p, "s1"); got != "s1 has diverged · 1 here, 1 on origin · not synced" || gitIn(t, dir, "rev-parse", "HEAD") != mine {
		t.Fatalf("diverged: %q", got)
	}
}

func TestSyncTheProjectFolder(t *testing.T) {
	a, _, p := newApp(t)
	u := withOrigin(t, p.Path)
	sha := u.push(t, "main", "a.txt")
	if got := syncStory(t, a, p, ""); got != "main synced · 1 new commit from origin" {
		t.Fatalf("chats: %q", got)
	}
	if gitIn(t, p.Path, "rev-parse", "HEAD") != sha {
		t.Fatal("main not fast-forwarded")
	}
	commitFile(t, p.Path, "mine.txt")
	if got := syncStory(t, a, p, ""); got != "main is up to date · 1 commit not pushed" {
		t.Fatalf("ahead: %q", got)
	}
}

func TestSyncWithoutOriginAndUnknownStory(t *testing.T) {
	a, _, p := newApp(t)
	if got := syncStory(t, a, p, ""); got != "main has no origin · nothing to sync" {
		t.Fatalf("no origin: %q", got)
	}
	if _, err := a.PrepareSync(context.Background(), p, "nope"); err == nil {
		t.Fatal("unknown story synced")
	}
}

func TestBusySessions(t *testing.T) {
	p := state.New("x", "/x")
	p.Sessions = map[int]*state.Session{
		1: {Story: "s1"}, 2: {Story: "s1", Done: true}, 3: {Story: "s1"}, 4: {Story: ""},
	}
	got := BusySessions(p, "s1", func(int) bool { return true })
	if !slices.Equal(got, []int{1, 3}) {
		t.Fatalf("busy %v", got)
	}
	if got := BusySessions(p, "s1", func(n int) bool { return n == 4 }); len(got) != 0 {
		t.Fatalf("busy %v", got)
	}
}
