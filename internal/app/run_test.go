package app

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dinhhuy258/bx/internal/state"
	"github.com/dinhhuy258/bx/internal/tmux"
)

const runCfg = `[[run]]
name = "web"
cmd = "npm run dev"

[[run]]
name = "api"
cmd = "php artisan serve"
stop = "pkill php"
`

func newRunApp(t *testing.T) (*App, *tmux.Fake, *state.Project) {
	t.Helper()
	a, f, p := newApp(t)
	for _, s := range []string{"s1", "s2"} {
		if _, err := a.NewStory(context.Background(), p, StoryOpts{Name: s}); err != nil {
			t.Fatal(err)
		}
	}
	os.MkdirAll(a.Cfg.AppsDir(), 0o755)
	os.WriteFile(a.Cfg.AppFile(p.Name), []byte(runCfg), 0o644)
	a.Self = "/usr/local/bin/bx"
	oldWait, oldPoll, oldCmd := stopWait, stopPoll, stopCmdWait
	stopWait, stopPoll, stopCmdWait = 20*time.Millisecond, time.Millisecond, 20*time.Millisecond
	t.Cleanup(func() { stopWait, stopPoll, stopCmdWait = oldWait, oldPoll, oldCmd })
	return a, f, p
}

func TestRunStoryOpensARunShell(t *testing.T) {
	a, f, p := newRunApp(t)
	wt := a.Cfg.WorktreeDir(p.Name, "s1")
	f.Replies["new-session -d -s bxsh-home-network-s1-1 -c "+wt+" -e PAGER=cat -e GIT_PAGER=cat -e BX_APP_HOST=dev -P -F #{session_id}"] = "$9\n"
	f.Calls, f.Inputs = nil, nil
	if err := a.RunStory(context.Background(), p, "s1", RunOpts{Names: []string{"web"}, Host: "dev"}); err != nil {
		t.Fatal(err)
	}
	got := calls(f)
	for _, w := range []string{"set-option -t $9 @bx_story s1", "set-option -t $9 @bx_run web", "send-keys -t $9 Enter"} {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q in\n%s", w, got)
		}
	}
	if len(f.Inputs) != 1 || f.Inputs[0] != "/usr/local/bin/bx run-exec home-network s1 web" {
		t.Errorf("typed %q", f.Inputs)
	}
}

func TestRunStoryReusesAnExitedShell(t *testing.T) {
	a, f, p := newRunApp(t)
	f.Replies[listShells] = "$4\tbxsh-home-network-s1-1\thome-network\ts1\t1\t\tzsh\tweb\n"
	f.Calls, f.Inputs = nil, nil
	if err := a.RunStory(context.Background(), p, "s1", RunOpts{Names: []string{"web"}, Setup: true}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(calls(f), "new-session") || len(f.Inputs) != 1 || f.Inputs[0] != "/usr/local/bin/bx run-exec -setup home-network s1 web" {
		t.Errorf("calls %s inputs %q", calls(f), f.Inputs)
	}
}

func TestRunStoryConflict(t *testing.T) {
	a, f, p := newRunApp(t)
	f.Replies[listShells] = "$5\tbxsh-home-network-s2-1\thome-network\ts2\t1\t\tphp\tapi\n"
	err := a.RunStory(context.Background(), p, "s1", RunOpts{})
	var c *RunConflictError
	if !errors.As(err, &c) || c.Story != "s2" || !slices.Equal(c.Runs, []string{"api"}) {
		t.Fatalf("%v", err)
	}
	if s, _ := a.RunningStory(p); s != "s2" {
		t.Errorf("running %q", s)
	}
	if err := a.RunStory(context.Background(), p, "s1", RunOpts{Names: []string{"nope"}}); err == nil || !strings.Contains(err.Error(), `no run "nope"`) {
		t.Errorf("unknown run: %v", err)
	}
}

func TestStopStory(t *testing.T) {
	a, f, p := newRunApp(t)
	f.Replies[listShells] = "$5\tbxsh-home-network-s2-1\thome-network\ts2\t1\t\tphp\tapi\n"
	var docker []string
	a.Docker = func(_ context.Context, args ...string) (string, error) {
		docker = append(docker, strings.Join(args, " "))
		if args[0] == "ps" {
			return "abc\n", nil
		}
		return "", nil
	}
	f.Calls, f.Inputs = nil, nil
	if err := a.StopStory(context.Background(), p, "s2", nil); err != nil {
		t.Fatal(err)
	}
	got := calls(f)
	ci, ki := strings.Index(got, "send-keys -t $5 C-c"), strings.Index(got, "kill-session -t $5")
	if ci < 0 || ki < ci {
		t.Errorf("calls:\n%s", got)
	}
	if len(f.Inputs) != 1 || f.Inputs[0] != "/usr/local/bin/bx run-exec -stop home-network s2 api" {
		t.Errorf("stop command: %q", f.Inputs)
	}
	want := []string{"ps -q --filter label=bx.project=home-network --filter label=bx.story=s2", "stop abc"}
	if !slices.Equal(docker, want) {
		t.Errorf("docker: %q", docker)
	}
}

func TestCloseStoryStopsRunsAndForgetsSetup(t *testing.T) {
	a, f, p := newRunApp(t)
	mark := a.Cfg.SetupMark(p.Name, "s1", "web")
	os.MkdirAll(a.Cfg.SetupMarks(p.Name, "s1"), 0o755)
	os.WriteFile(mark, nil, 0o644)
	f.Replies[listShells] = "$5\tbxsh-home-network-s1-1\thome-network\ts1\t1\t\tnode\tweb\n"
	f.Calls = nil
	if _, err := a.CloseStory(p, "s1", true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(calls(f), "send-keys -t $5 C-c") {
		t.Errorf("not stopped first:\n%s", calls(f))
	}
	if _, err := os.Stat(mark); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("mark kept: %v", err)
	}
}
