package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/dinhhuy258/bx/internal/state"
)

func TestShellName(t *testing.T) {
	if got := ShellName("my.proj", "s.1", 2); got != "bxsh-my_proj-s_1-2" {
		t.Fatal(got)
	}
	if got := ShellName("my.proj", "", 1); got != "bxsh-my_proj-1" {
		t.Fatal(got)
	}
}

func TestNewShellTakesTheLowestFreeNumber(t *testing.T) {
	a, f, p := newApp(t)
	if _, err := a.NewStory(context.Background(), p, StoryOpts{Name: "s1"}); err != nil {
		t.Fatal(err)
	}
	wt := a.Cfg.WorktreeDir("home-network", "s1")
	f.Replies[listShells] = "$1\tbxsh-home-network-s1-1\thome-network\ts1\t1\t\tzsh\n" +
		"$3\tbxsh-home-network-s1-3\thome-network\ts1\t3\t\tzsh\n" +
		"$2\tbxsh-home-network-2\thome-network\t\t2\t\tzsh\n" +
		"$4\tbxsh-other-s1-2\tother\ts1\t2\t\tzsh\n"
	f.Replies["new-session -d -s bxsh-home-network-s1-2 -c "+wt+" -e PAGER=cat -e GIT_PAGER=cat -P -F #{session_id}"] = "$9\n"
	f.Replies["new-session -d -s bxsh-home-network-1 -c "+p.Path+" -e PAGER=cat -e GIT_PAGER=cat -P -F #{session_id}"] = "$8\n"
	f.Calls = nil
	k, err := a.NewShell(p, "s1")
	if err != nil || k != 2 {
		t.Fatalf("%d %v", k, err)
	}
	want := "new-session -d -s bxsh-home-network-s1-2 -c " + wt + " -e PAGER=cat -e GIT_PAGER=cat -P -F #{session_id}\n" +
		"set-option -t $9 @bx_project home-network\n" +
		"set-option -t $9 @bx_story s1\n" +
		"set-option -t $9 @bx_shell 2\n" +
		"set-option -s exit-empty off"
	if calls(f) != want {
		t.Fatalf("calls:\n%s", calls(f))
	}
	f.Calls = nil
	if k, err := a.NewShell(p, ""); err != nil || k != 1 {
		t.Fatalf("root: %d %v", k, err)
	}
	want = "new-session -d -s bxsh-home-network-1 -c " + p.Path + " -e PAGER=cat -e GIT_PAGER=cat -P -F #{session_id}\n" +
		"set-option -t $8 @bx_project home-network\n" +
		"set-option -t $8 @bx_shell 1\n" +
		"set-option -s exit-empty off"
	if calls(f) != want {
		t.Fatalf("root calls:\n%s", calls(f))
	}
	f.Calls = nil
	if _, err := a.NewShell(p, "nope"); err == nil || calls(f) != "" {
		t.Fatalf("unknown story: %v\n%s", err, calls(f))
	}
	// a shell that cannot be marked is not left behind
	f.Errs = map[string]error{"set-option -t $8 @bx_shell 1": errors.New("boom")}
	if _, err := a.NewShell(p, ""); err == nil || !strings.Contains(calls(f), "kill-session -t $8") {
		t.Fatalf("set-option failure: %v\n%s", err, calls(f))
	}
}

func TestShellsTargetRenameClose(t *testing.T) {
	a, f, p := newApp(t)
	f.Replies[listShells] = "$9\tbxsh-home-network-s1-2\thome-network\ts1\t2\tdev\tnpm\n" +
		"$4\tbxsh-other-1\tother\t\t1\t\tzsh\n" +
		"$7\tbxsh-home-network-s1-1\thome-network\ts1\t1\t\tzsh\n" +
		"$6\tbxsh-home-network-1\thome-network\t\t1\t\tbash\n"
	sh, err := a.Shells(p)
	if err != nil {
		t.Fatal(err)
	}
	want := []Shell{
		{ID: "$6", Story: "", K: 1, Command: "bash"},
		{ID: "$7", Story: "s1", K: 1, Command: "zsh"},
		{ID: "$9", Story: "s1", K: 2, Title: "dev", Command: "npm"},
	}
	if len(sh) != len(want) {
		t.Fatalf("%+v", sh)
	}
	for i := range want {
		if sh[i] != want[i] {
			t.Fatalf("%d: %+v", i, sh[i])
		}
	}
	if id, err := a.ShellTarget(p, "s1", 2); err != nil || id != "$9" {
		t.Fatalf("%q %v", id, err)
	}
	if _, err := a.ShellTarget(p, "s1", 4); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("want ErrNotRunning, got %v", err)
	}
	if err := a.RenameShell(p, "s1", 2, " dev\tserver "); err != nil {
		t.Fatal(err)
	}
	if err := a.RenameShell(p, "s1", 2, ""); err != nil {
		t.Fatal(err)
	}
	if err := a.CloseShell(p, "s1", 2); err != nil {
		t.Fatal(err)
	}
	want2 := "set-option -t $9 @bx_title dev server\nset-option -u -t $9 @bx_title\nkill-session -t $9"
	if calls(f) != want2 {
		t.Fatalf("calls:\n%s", calls(f))
	}
	if err := a.CloseShell(p, "s1", 4); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("closing a gone shell: %v", err)
	}
}

func TestCloseStoryKillsItsShells(t *testing.T) {
	a, f, p := newApp(t)
	if _, err := a.NewStory(context.Background(), p, StoryOpts{Name: "s1"}); err != nil {
		t.Fatal(err)
	}
	f.Replies[listShells] = "$8\tbxsh-home-network-s1-1\thome-network\ts1\t1\t\tzsh\n" +
		"$9\tbxsh-home-network-1\thome-network\t\t1\t\tzsh\n"
	f.Calls = nil
	if err := a.CloseStory(p, "s1", true); err != nil {
		t.Fatal(err)
	}
	if calls(f) != "kill-session -t $8" {
		t.Fatalf("calls:\n%s", calls(f))
	}
}

func TestCleanupScanListsOrphanShells(t *testing.T) {
	a, f, p := newApp(t)
	p.Stories["s1"] = &state.Story{Branch: "s1"}
	f.Replies[listSessions] = "$1\tbxsh-home-network-s1-1\n$2\tbxsh-home-network-gone-1\n$3\tbxsh-nope-1\n$4\tbxsh-home-network-1\n"
	f.Replies[listShells] = "$1\tbxsh-home-network-s1-1\thome-network\ts1\t1\t\tzsh\n" +
		"$2\tbxsh-home-network-gone-1\thome-network\tgone\t1\t\tzsh\n" +
		"$3\tbxsh-nope-1\tnope\t\t1\t\tzsh\n" +
		"$4\tbxsh-home-network-1\thome-network\t\t1\t\tzsh\n"
	ps := []*state.Project{p}
	items, err := a.CleanupScan(ps)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, it := range items {
		if it.Kind == "orphan" {
			ids = append(ids, it.ID)
		}
	}
	if got := strings.Join(ids, " "); got != "tmux:bxsh-home-network-gone-1 tmux:bxsh-nope-1" {
		t.Fatalf("orphans = %q", got)
	}
	n, err := a.CleanupApply(ps, ids)
	if err != nil || n != 2 {
		t.Fatalf("removed %d, %v", n, err)
	}
	if got := calls(f); got != "kill-session -t $2\nkill-session -t $3" {
		t.Fatalf("calls:\n%s", got)
	}
}
