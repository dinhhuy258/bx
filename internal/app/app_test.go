package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dinhhuy258/bx/internal/claude"
	"github.com/dinhhuy258/bx/internal/config"
	"github.com/dinhhuy258/bx/internal/git"
	"github.com/dinhhuy258/bx/internal/state"
	"github.com/dinhhuy258/bx/internal/tmux"
	"github.com/dinhhuy258/bx/internal/uploads"
)

const listSessions = "list-sessions -F #{session_id}\t#{session_name}"

const listShells = "list-sessions -F " + tmux.ShellFormat

func newApp(t *testing.T) (*App, *tmux.Fake, *state.Project) {
	t.Helper()
	cfg := config.Default(t.TempDir())
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	repo := cfg.ProjectDir("home-network")
	g := git.New()
	if err := g.Init(repo); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(repo, "README.md"), []byte("x\n"), 0o644)
	for _, args := range [][]string{{"add", "."}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "init"}} {
		c := exec.Command("git", args...)
		c.Dir = repo
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("%v %s", err, out)
		}
	}
	f := &tmux.Fake{Replies: map[string]string{listSessions: "", listShells: ""}}
	a := &App{Cfg: cfg, Tmux: tmux.New(f), Git: g, UUID: func() string { return "fixed-uuid" }}
	p := state.New("home-network", repo)
	if err := a.SaveProject(p); err != nil {
		t.Fatal(err)
	}
	return a, f, p
}

func calls(f *tmux.Fake) string {
	var out []string
	for _, c := range f.Calls {
		if c[0] != "list-sessions" {
			out = append(out, strings.Join(c, " "))
		}
	}
	return strings.Join(out, "\n")
}

func TestTmuxName(t *testing.T) {
	if got := TmuxName("my.proj", 3); got != "bx-my_proj-3" {
		t.Fatal(got)
	}
}

func TestNewStoryCreatesWorktreeAndRecord(t *testing.T) {
	a, _, p := newApp(t)
	if _, err := a.NewStory(context.Background(), p, StoryOpts{Name: "s1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(a.Cfg.WorktreeDir("home-network", "s1")); err != nil {
		t.Fatal(err)
	}
	if _, err := a.NewStory(context.Background(), p, StoryOpts{Name: "!!!"}); err == nil {
		t.Fatal("a name with nothing left after cleanup accepted")
	}
}

func TestNewSessionStartsItsOwnTmuxSession(t *testing.T) {
	a, f, p := newApp(t)
	if _, err := a.NewStory(context.Background(), p, StoryOpts{Name: "s1"}); err != nil {
		t.Fatal(err)
	}
	num, err := a.NewSession(p, "s1", "Check syntax")
	if err != nil || num != 1 {
		t.Fatalf("%d %v", num, err)
	}
	// exit-empty off: the tmux server outlives its last session, so an exit is seen as one
	want := "new-session -d -s bx-home-network-1 -c " + a.Cfg.WorktreeDir("home-network", "s1") + " -P -F #{session_id} claude --session-id fixed-uuid\n" +
		"set-option -s exit-empty off"
	if calls(f) != want {
		t.Fatalf("calls:\n%s", calls(f))
	}
	got, _ := a.LoadProject("home-network")
	if s := got.Sessions[1]; s == nil || s.Story != "s1" || s.UUID != "fixed-uuid" {
		t.Fatalf("%+v", got.Sessions)
	}
	chat, _ := a.NewChat(p, "")
	if chat != 2 || !strings.Contains(calls(f), "new-session -d -s bx-home-network-2 -c "+p.Path+" ") || p.Sessions[2].Title != "New session" {
		t.Fatalf("chat:\n%s", calls(f))
	}
}

func TestStartFailureLeavesNoRecord(t *testing.T) {
	a, f, p := newApp(t)
	f.Err = errors.New("boom")
	if _, err := a.NewChat(p, "x"); err == nil {
		t.Fatal("want error")
	}
	if len(p.Sessions) != 0 || p.NextNum != 1 {
		t.Fatalf("record left behind: %+v next=%d", p.Sessions, p.NextNum)
	}
}

func TestTargetResume(t *testing.T) {
	a, f, p := newApp(t)
	p.AddSession(&state.Session{UUID: "u1", Title: "a"})
	p.AddSession(&state.Session{UUID: "u2", Title: "b", Done: true})
	f.Replies[listSessions] = "$5\tbx-home-network-1\n$6\tbx-other-2\n"
	if id, err := a.Target(p, 1); err != nil || id != "$5" {
		t.Fatalf("%q %v", id, err)
	}
	if _, err := a.Target(p, 2); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("want ErrNotRunning, got %v", err)
	}
	if err := a.Resume(p, 1); err == nil {
		t.Fatal("resumed a running session")
	}
	if err := a.Resume(p, 2); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(calls(f), "new-session -d -s bx-home-network-2 -c "+p.Path+" -P -F #{session_id} claude --resume u2") || p.Sessions[2].Done {
		t.Fatalf("resume:\n%s", calls(f))
	}
}

func TestResumeAllSkipsDoneLiveAndForeign(t *testing.T) {
	a, f, p := newApp(t)
	p.AddSession(&state.Session{UUID: "u1"})             // tmux session alive
	p.AddSession(&state.Session{UUID: "u2", Done: true}) // done
	p.AddSession(&state.Session{UUID: "u3"})             // Claude runs outside bx
	p.AddSession(&state.Session{UUID: "u4"})             // gone: resume
	f.Replies[listSessions] = "$5\tbx-home-network-1\n"
	nums, err := a.ResumeAll(p, map[string]claude.Agent{"u3": {SessionID: "u3"}})
	if err != nil || len(nums) != 1 || nums[0] != 4 {
		t.Fatalf("%v %v", nums, err)
	}
	if calls(f) != "new-session -d -s bx-home-network-4 -c "+p.Path+" -P -F #{session_id} claude --resume u4\nset-option -s exit-empty off" {
		t.Fatalf("calls:\n%s", calls(f))
	}
}

// killProbe answers from a Fake and, on kill-session, records whether session 1 is done on
// disk at that moment, then fails.
type killProbe struct {
	f      *tmux.Fake
	file   string
	killed bool
	done   bool
}

func (k *killProbe) Run(args ...string) ([]byte, error) {
	if args[0] == "kill-session" {
		p, err := state.Load(k.file, "home-network")
		if err != nil {
			return nil, err
		}
		k.killed, k.done = true, p.Sessions[1].Done
		return nil, errors.New("kill failed")
	}
	return k.f.Run(args...)
}

func TestStopSavesDoneBeforeKilling(t *testing.T) {
	a, f, p := newApp(t)
	p.AddSession(&state.Session{UUID: "u1"})
	if err := a.SaveProject(p); err != nil {
		t.Fatal(err)
	}
	f.Replies[listSessions] = "$5\tbx-home-network-1\n"
	k := &killProbe{f: f, file: a.Cfg.StateFile("home-network")}
	a.Tmux = tmux.New(k)
	if err := a.Stop(p, 1); err == nil {
		t.Fatal("want the kill error")
	}
	if !k.killed || !k.done {
		t.Fatalf("killed=%v, done on disk during the kill=%v", k.killed, k.done)
	}
	got, _ := a.LoadProject("home-network")
	if got.Sessions[1].Done || p.Sessions[1].Done {
		t.Fatal("a failed kill left the session done")
	}
}

func TestStopMarksDone(t *testing.T) {
	a, f, p := newApp(t)
	p.AddSession(&state.Session{UUID: "u1"}) // live
	p.AddSession(&state.Session{UUID: "u2"}) // gone
	f.Replies[listSessions] = "$5\tbx-home-network-1\n"
	if err := a.Stop(p, 1); err != nil {
		t.Fatal(err)
	}
	if calls(f) != "kill-session -t $5" {
		t.Fatalf("calls:\n%s", calls(f))
	}
	if err := a.Stop(p, 2); err != nil {
		t.Fatal(err)
	}
	if calls(f) != "kill-session -t $5" {
		t.Fatalf("stopping a gone session killed something:\n%s", calls(f))
	}
	got, _ := a.LoadProject("home-network")
	if !got.Sessions[1].Done || !got.Sessions[2].Done {
		t.Fatalf("%+v %+v", got.Sessions[1], got.Sessions[2])
	}
	if err := a.Stop(p, 9); err == nil {
		t.Fatal("stopped a session that does not exist")
	}
}

func TestDeleteSession(t *testing.T) {
	a, f, p := newApp(t)
	n1 := p.AddSession(&state.Session{UUID: "u1"}) // live
	n2 := p.AddSession(&state.Session{UUID: "u2"}) // gone
	if err := a.SaveProject(p); err != nil {
		t.Fatal(err)
	}
	base := a.Cfg.UploadsDir()
	now := time.Now()
	mine, err := uploads.Save(base, "home-network", n1, "photo.jpg", []byte("x"), now)
	if err != nil {
		t.Fatal(err)
	}
	other, err := uploads.Save(base, "home-network", n2, "keep.txt", []byte("x"), now)
	if err != nil {
		t.Fatal(err)
	}
	f.Replies[listSessions] = "$5\tbx-home-network-1\n"
	var re *RunningError
	if err := a.DeleteSession(p, n1, false); !errors.As(err, &re) || re.Num != n1 {
		t.Fatalf("want RunningError, got %v", err)
	}
	if calls(f) != "" || p.Sessions[n1] == nil {
		t.Fatal("a refused delete changed something")
	}
	if _, err := os.Stat(mine); err != nil {
		t.Fatalf("a refused delete removed the upload: %v", err)
	}
	if err := a.DeleteSession(p, n1, true); err != nil {
		t.Fatal(err)
	}
	if calls(f) != "kill-session -t $5" {
		t.Fatalf("calls:\n%s", calls(f))
	}
	got, _ := a.LoadProject("home-network")
	if got.Sessions[n1] != nil || got.Sessions[n2] == nil {
		t.Fatalf("%+v", got.Sessions)
	}
	if _, err := os.Stat(uploads.Dir(base, "home-network", n1)); !os.IsNotExist(err) {
		t.Fatalf("session uploads kept: %v", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("another session's upload removed: %v", err)
	}
	if err := a.DeleteSession(p, n2, false); err != nil {
		t.Fatalf("a gone session needs no force: %v", err)
	}
	got, _ = a.LoadProject("home-network")
	if len(got.Sessions) != 0 || got.NextNum != 3 {
		t.Fatalf("%+v next=%d", got.Sessions, got.NextNum)
	}
	if err := a.DeleteSession(p, n2, false); err == nil {
		t.Fatal("deleted a session that does not exist")
	}
}

func TestMarkDone(t *testing.T) {
	a, _, p := newApp(t)
	p.AddSession(&state.Session{UUID: "u1"})
	if changed, err := a.MarkDone(p, []int{1, 7}); err != nil || !changed {
		t.Fatalf("%v %v", changed, err)
	}
	if changed, err := a.MarkDone(p, []int{1}); err != nil || changed {
		t.Fatalf("second time: %v %v", changed, err)
	}
	got, _ := a.LoadProject("home-network")
	if !got.Sessions[1].Done {
		t.Fatal("not saved")
	}
}

func TestCloseStoryDirtyThenForce(t *testing.T) {
	a, f, p := newApp(t)
	if _, err := a.NewStory(context.Background(), p, StoryOpts{Name: "s1"}); err != nil {
		t.Fatal(err)
	}
	p.AddSession(&state.Session{Story: "s1", UUID: "u1"})
	wt := a.Cfg.WorktreeDir("home-network", "s1")
	os.WriteFile(filepath.Join(wt, "new.txt"), []byte("x"), 0o644)
	f.Replies[listSessions] = "$5\tbx-home-network-1\n"
	var de *DirtyError
	if _, err := a.CloseStory(p, "s1", false); !errors.As(err, &de) || de.Files != 1 {
		t.Fatalf("want DirtyError, got %v", err)
	}
	if calls(f) != "" || p.Stories["s1"] == nil {
		t.Fatal("a refused close changed something")
	}
	if _, err := a.CloseStory(p, "s1", true); err != nil {
		t.Fatal(err)
	}
	if calls(f) != "kill-session -t $5" {
		t.Fatalf("calls:\n%s", calls(f))
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatal("worktree still there")
	}
	got, _ := a.LoadProject("home-network")
	if got.Stories["s1"] != nil || got.Sessions[1] != nil {
		t.Fatalf("records left: %+v", got)
	}
}

func TestCloseStoryRemovesUploads(t *testing.T) {
	a, f, p := newApp(t)
	if _, err := a.NewStory(context.Background(), p, StoryOpts{Name: "s1"}); err != nil {
		t.Fatal(err)
	}
	n1 := p.AddSession(&state.Session{Story: "s1", UUID: "u1"})
	chat := p.AddSession(&state.Session{UUID: "u2"})
	base := a.Cfg.UploadsDir()
	now := time.Now()
	mine, err := uploads.Save(base, "home-network", n1, "photo.jpg", []byte("x"), now)
	if err != nil {
		t.Fatal(err)
	}
	others := []string{}
	for _, o := range []struct {
		project string
		num     int
	}{{"home-network", chat}, {"bunnix", n1}} {
		path, err := uploads.Save(base, o.project, o.num, "keep.txt", []byte("x"), now)
		if err != nil {
			t.Fatal(err)
		}
		others = append(others, path)
	}
	os.WriteFile(filepath.Join(a.Cfg.WorktreeDir("home-network", "s1"), "new.txt"), []byte("x"), 0o644)
	f.Replies[listSessions] = ""
	if _, err := a.CloseStory(p, "s1", false); err == nil {
		t.Fatal("a dirty story closed without force")
	}
	if _, err := os.Stat(mine); err != nil {
		t.Fatalf("a refused close removed the upload: %v", err)
	}
	if _, err := a.CloseStory(p, "s1", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(uploads.Dir(base, "home-network", n1)); !os.IsNotExist(err) {
		t.Fatalf("session uploads kept: %v", err)
	}
	for _, path := range others {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("another session's upload removed: %v", err)
		}
	}
}

func TestTitlesFromTheTranscript(t *testing.T) {
	a, f, p := newApp(t)
	a.Transcripts = t.TempDir()
	f.Replies[listSessions] = ""
	num, _ := a.NewChat(p, "")
	named, _ := a.NewChat(p, "kept")
	cwd := "/work/home-network"
	path := claude.TranscriptPath(a.Transcripts, cwd, "fixed-uuid")
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, []byte(`{"type":"user","message":{"content":"make the guest network use a shorter DHCP lease please"}}`+"\n"), 0o644)
	agents := map[string]claude.Agent{"fixed-uuid": {SessionID: "fixed-uuid", Cwd: cwd}}

	if got := a.Titles(p, map[string]claude.Agent{}); len(got) != 0 {
		t.Fatalf("a session that is not running is not read: %v", got)
	}
	titles := a.Titles(p, agents)
	if len(titles) != 1 || titles[num] != "make the guest network use a shorter DH…" {
		t.Fatalf("titles = %v", titles)
	}
	if changed, err := a.SetTitles(p, titles); !changed || err != nil {
		t.Fatal(changed, err)
	}
	got, _ := a.LoadProject("home-network")
	if got.Sessions[num].Title != titles[num] || got.Sessions[named].Title != "kept" {
		t.Fatalf("saved %q, %q", got.Sessions[num].Title, got.Sessions[named].Title)
	}
	if len(a.Titles(got, agents)) != 0 {
		t.Fatal("a named session is not read again")
	}
	if !got.Sessions[num].TitleAuto || got.Sessions[named].TitleAuto {
		t.Fatal("only the title bx picked is marked auto")
	}

	// Claude's title replaces the prompt bx stood in with
	os.WriteFile(path, []byte(`{"type":"user","message":{"content":"make the guest network use a shorter DHCP lease please"}}
{"type":"ai-title","aiTitle":"Shorter guest DHCP lease","sessionId":"fixed-uuid"}`+"\n"), 0o644)
	titles = a.Titles(got, agents)
	if len(titles) != 1 || titles[num] != "Shorter guest DHCP lease" {
		t.Fatalf("titles = %v", titles)
	}
	if changed, err := a.SetTitles(got, titles); !changed || err != nil {
		t.Fatal(changed, err)
	}
	if len(a.Titles(got, agents)) != 0 {
		t.Fatal("an unchanged title is not offered again")
	}

	if err := a.Rename(got, num, "mine"); err != nil { // a rename between Titles and SetTitles wins
		t.Fatal(err)
	}
	if changed, _ := a.SetTitles(got, map[int]string{num: "other"}); changed {
		t.Fatal("the rename was overwritten")
	}
	if len(a.Titles(got, agents)) != 0 {
		t.Fatal("a renamed session is not read again")
	}
	got, _ = a.LoadProject("home-network")
	if got.Sessions[num].Title != "mine" || got.Sessions[num].TitleAuto {
		t.Fatalf("saved %q auto=%v", got.Sessions[num].Title, got.Sessions[num].TitleAuto)
	}
}

func TestCleanupScanBuildsNamesForward(t *testing.T) {
	a, f, hn := newApp(t)
	home := state.New("home", t.TempDir())
	home.AddSession(&state.Session{UUID: "h1"})
	hn.NextNum = 12
	hn.AddSession(&state.Session{UUID: "u12"})
	done := hn.AddSession(&state.Session{UUID: "u13", Title: "old", Done: true})
	for _, p := range []*state.Project{home, hn} {
		if err := a.SaveProject(p); err != nil {
			t.Fatal(err)
		}
	}
	// home and home-network share a prefix, so a name can't be split back into a project and
	// a number: only the names built from the records decide
	f.Replies[listSessions] = "$1\tbx-home-1\n$2\tbx-home-network-12\n$3\tbx-home-network-3\n$4\tbx-dotfiles-3\n$5\tscratch\n"
	ps := []*state.Project{hn, home}
	items, err := a.CleanupScan(ps)
	if err != nil {
		t.Fatal(err)
	}
	var orphans, dones []string
	for _, it := range items {
		switch it.Kind {
		case "orphan":
			orphans = append(orphans, it.ID)
		case "done":
			dones = append(dones, it.ID+" "+it.Label)
		}
	}
	if got := strings.Join(orphans, " "); got != "tmux:bx-dotfiles-3 tmux:bx-home-network-3" {
		t.Fatalf("orphans = %q", got)
	}
	if got := strings.Join(dones, "|"); got != "session:home-network:13 home-network · chats · [13] old · done" {
		t.Fatalf("done = %q", got)
	}
	n, err := a.CleanupApply(ps, []string{"tmux:bx-home-network-3", "tmux:bx-dotfiles-3", "session:home-network:13", "session:home:99"})
	if err != nil || n != 3 {
		t.Fatalf("removed %d, %v", n, err)
	}
	got := calls(f)
	if !strings.Contains(got, "kill-session -t $3") || !strings.Contains(got, "kill-session -t $4") || strings.Count(got, "kill-session") != 2 {
		t.Fatalf("calls:\n%s", got)
	}
	disk, err := a.LoadProject("home-network")
	if err != nil {
		t.Fatal(err)
	}
	if disk.Sessions[done] != nil || disk.Sessions[12] == nil {
		t.Fatalf("%+v", disk.Sessions)
	}
}

func TestLaunchLogsKeepServerFailure(t *testing.T) {
	a, f, p := newApp(t)
	f.Errs = map[string]error{"set-option -s exit-empty off": errors.New("boom")}
	var logged []string
	a.Log = func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) }
	num, err := a.NewChat(p, "")
	if err != nil || num != 1 {
		t.Fatalf("a failed keep server failed the start: %d %v", num, err)
	}
	if len(logged) != 1 || !strings.Contains(logged[0], "boom") {
		t.Fatalf("log: %q", logged)
	}
}

func TestCloseStoryRemovesItsReview(t *testing.T) {
	a, f, p := newApp(t)
	if _, err := a.NewStory(context.Background(), p, StoryOpts{Name: "s1"}); err != nil {
		t.Fatal(err)
	}
	path := a.Cfg.ReviewFile(p.Name, "s1")
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, []byte(`{"version":1,"comments":[]}`), 0o644)
	f.Replies[listSessions] = ""
	if _, err := a.CloseStory(p, "s1", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("review file kept: %v", err)
	}
}
