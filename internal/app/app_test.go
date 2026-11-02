package app

import (
	"errors"
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
	f := &tmux.Fake{Replies: map[string]string{listSessions: ""}}
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
	if err := a.NewStory(p, "s1", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(a.Cfg.WorktreeDir("home-network", "s1")); err != nil {
		t.Fatal(err)
	}
	if err := a.NewStory(p, "Bad Name", nil); err == nil {
		t.Fatal("bad name accepted")
	}
}

func TestNewSessionStartsItsOwnTmuxSession(t *testing.T) {
	a, f, p := newApp(t)
	if err := a.NewStory(p, "s1", nil); err != nil {
		t.Fatal(err)
	}
	num, err := a.NewSession(p, "s1", "Check syntax")
	if err != nil || num != 1 {
		t.Fatalf("%d %v", num, err)
	}
	want := "new-session -d -s bx-home-network-1 -c " + a.Cfg.WorktreeDir("home-network", "s1") + " -P -F #{session_id} claude --session-id fixed-uuid"
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

func TestTargetAnswerYesResume(t *testing.T) {
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
	if err := a.AnswerYes(p, 1); err != nil || calls(f) != "send-keys -t $5 Enter" {
		t.Fatalf("%v\n%s", err, calls(f))
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
	n, err := a.ResumeAll(p, map[string]claude.Agent{"u3": {SessionID: "u3"}})
	if err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	if calls(f) != "new-session -d -s bx-home-network-4 -c "+p.Path+" -P -F #{session_id} claude --resume u4" {
		t.Fatalf("calls:\n%s", calls(f))
	}
}

func TestCloseStoryDirtyThenForce(t *testing.T) {
	a, f, p := newApp(t)
	if err := a.NewStory(p, "s1", nil); err != nil {
		t.Fatal(err)
	}
	p.AddSession(&state.Session{Story: "s1", UUID: "u1"})
	wt := a.Cfg.WorktreeDir("home-network", "s1")
	os.WriteFile(filepath.Join(wt, "new.txt"), []byte("x"), 0o644)
	f.Replies[listSessions] = "$5\tbx-home-network-1\n"
	var de *DirtyError
	if err := a.CloseStory(p, "s1", false); !errors.As(err, &de) || de.Files != 1 {
		t.Fatalf("want DirtyError, got %v", err)
	}
	if calls(f) != "" || p.Stories["s1"] == nil {
		t.Fatal("a refused close changed something")
	}
	if err := a.CloseStory(p, "s1", true); err != nil {
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
	if err := a.NewStory(p, "s1", nil); err != nil {
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
	if err := a.CloseStory(p, "s1", false); err == nil {
		t.Fatal("a dirty story closed without force")
	}
	if _, err := os.Stat(mine); err != nil {
		t.Fatalf("a refused close removed the upload: %v", err)
	}
	if err := a.CloseStory(p, "s1", true); err != nil {
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

func TestTitlesFromTheFirstPrompt(t *testing.T) {
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
	got.Sessions[num].Title = "mine" // a rename between Titles and SetTitles wins
	if changed, _ := a.SetTitles(got, titles); changed {
		t.Fatal("the rename was overwritten")
	}
}
