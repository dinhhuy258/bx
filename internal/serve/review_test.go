package serve

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dinhhuy258/bx/internal/api"
	"github.com/dinhhuy258/bx/internal/claude"
	"github.com/dinhhuy258/bx/internal/state"
	"github.com/dinhhuy258/bx/internal/tmux"
)

const reviewPath = "/api/projects/home-network/stories/s1/review"

// startingFake is the tmux fake where a new session shows up in list-sessions right away, so
// Target finds a session that bx just resumed or started.
type startingFake struct{ *tmux.Fake }

func (f startingFake) Run(args ...string) ([]byte, error) {
	out, err := f.Fake.Run(args...)
	if args[0] == "new-session" {
		f.Replies[listSessions] += "$9\t" + args[3] + "\n"
	}
	return out, err
}

func writeFile(t *testing.T, path, s string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func postStatus(t *testing.T, url string, body any) (api.Result, int) {
	t.Helper()
	b, _ := json.Marshal(body)
	r, err := http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	var res api.Result
	_ = json.NewDecoder(r.Body).Decode(&res)
	return res, r.StatusCode
}

func saveComments(t *testing.T, ts string, c api.ReviewComments) (api.Review, int) {
	t.Helper()
	var out api.Review
	data, _ := json.Marshal(c)
	req, _ := http.NewRequest(http.MethodPut, ts+reviewPath, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	_ = json.NewDecoder(r.Body).Decode(&out)
	return out, r.StatusCode
}

func TestReviewShowsTheDiffAndSavesComments(t *testing.T) {
	s, _, ts := newServer(t)
	writeFile(t, filepath.Join(s.Cfg.WorktreeDir("home-network", "s1"), "notes.txt"), "a\nb\n")

	r, code := get[api.Review](t, ts, reviewPath)
	if code != 200 || len(r.Files) != 1 || r.Files[0].Path != "notes.txt" || r.Files[0].Status != "added" || r.Version != 0 {
		t.Fatalf("%d %+v", code, r)
	}
	r, code = saveComments(t, ts.URL, api.ReviewComments{Version: 0, Comments: []api.Comment{{File: "notes.txt", Side: "new", Line: 2, Code: "b", Body: "why b"}}})
	if code != 200 || r.Version != 1 || len(r.Comments) != 1 || r.Comments[0].ID == "" || r.Comments[0].Outdated {
		t.Fatalf("%d %+v", code, r)
	}
	// a stale version is refused
	if _, code := saveComments(t, ts.URL, api.ReviewComments{Version: 0}); code != http.StatusConflict {
		t.Fatalf("stale version: %d", code)
	}
	// the snapshot counts the unsent comment
	snap, _ := get[api.Snapshot](t, ts, "/api/projects/home-network")
	if snap.Live.Review["s1"] != 1 {
		t.Fatalf("live.review = %v", snap.Live.Review)
	}
	if _, code := get[api.Review](t, ts, "/api/projects/home-network/stories/nope/review"); code != 404 {
		t.Fatalf("unknown story: %d", code)
	}
}

func TestSendPastesIntoARunningSessionAndMarksSent(t *testing.T) {
	s, f, ts := newServer(t)
	writeFile(t, filepath.Join(s.Cfg.WorktreeDir("home-network", "s1"), "notes.txt"), "a\n")
	saveComments(t, ts.URL, api.ReviewComments{Comments: []api.Comment{{File: "notes.txt", Side: "new", Line: 1, Code: "a", Body: "explain a"}}})

	res, code := postStatus(t, ts.URL+reviewPath+"/send", api.ReviewSend{Session: 1})
	if code != 200 || res.Num != 1 || res.Message != "Pasted 1 comment into [1]" {
		t.Fatalf("%d %+v", code, res)
	}
	if len(f.Inputs) != 1 || !strings.Contains(f.Inputs[0], "1. notes.txt:1\n   > a\n   explain a") || strings.HasSuffix(f.Inputs[0], "\n") {
		t.Fatalf("pasted %q", f.Inputs)
	}
	if !strings.Contains(calls(f), "paste-buffer -p -d -b bx-review-5 -t $5") {
		t.Fatalf("calls:\n%s", calls(f))
	}
	r, _ := get[api.Review](t, ts, reviewPath)
	if c := r.Comments[0]; c.SentTo != 1 || c.SentAt == 0 {
		t.Fatalf("not marked sent: %+v", c)
	}
	// round 2: nothing unsent is left
	if res, code := postStatus(t, ts.URL+reviewPath+"/send", api.ReviewSend{Session: 1}); code != http.StatusConflict || res.Error != "nothing to send" {
		t.Fatalf("resend: %d %+v", code, res)
	}
	// a sent comment can't be edited
	sent := r.Comments[0]
	sent.Body = "changed"
	if _, code := saveComments(t, ts.URL, api.ReviewComments{Version: r.Version, Comments: []api.Comment{sent}}); code != http.StatusConflict {
		t.Fatalf("edit sent: %d", code)
	}
	// clear sent drops it
	var cleared api.Review
	b, _ := json.Marshal(struct{}{})
	resp, _ := http.Post(ts.URL+reviewPath+"/clear-sent", "application/json", bytes.NewReader(b))
	_ = json.NewDecoder(resp.Body).Decode(&cleared)
	resp.Body.Close()
	if resp.StatusCode != 200 || len(cleared.Comments) != 0 {
		t.Fatalf("clear sent: %d %+v", resp.StatusCode, cleared)
	}
}

func TestSendResumesADoneSessionWhenClaudeIsReady(t *testing.T) {
	readyTimeout, readyPoll = 2*time.Second, 10*time.Millisecond
	t.Cleanup(func() { readyTimeout, readyPoll = 30*time.Second, 500*time.Millisecond })
	s, f, ts := newServer(t)
	s.App.Tmux = tmux.New(startingFake{f})
	p, _ := s.App.LoadProject("home-network")
	p.AddSession(&state.Session{Story: "s1", UUID: "u2", Title: "finished", Done: true}) // 2
	if err := s.App.SaveProject(p); err != nil {
		t.Fatal(err)
	}
	s.Lister = fakeLister{[]claude.Agent{{SessionID: "u2", Status: "idle"}}}
	writeFile(t, filepath.Join(s.Cfg.WorktreeDir("home-network", "s1"), "notes.txt"), "a\n")
	saveComments(t, ts.URL, api.ReviewComments{Comments: []api.Comment{{File: "notes.txt", Side: "new", Line: 1, Body: "x"}, {File: "notes.txt", Side: "new", Line: 1, Body: "y"}}})

	res, code := postStatus(t, ts.URL+reviewPath+"/send", api.ReviewSend{Session: 2})
	if code != 200 || res.Message != "Resumed [2] and pasted 2 comments" {
		t.Fatalf("%d %+v", code, res)
	}
	if !strings.Contains(calls(f), "paste-buffer -p -d -b bx-review-9 -t $9") {
		t.Fatalf("calls:\n%s", calls(f))
	}
}

func TestSendToANewSessionAndReadyTimeout(t *testing.T) {
	readyTimeout, readyPoll = 50*time.Millisecond, 10*time.Millisecond
	t.Cleanup(func() { readyTimeout, readyPoll = 30*time.Second, 500*time.Millisecond })
	s, f, ts := newServer(t)
	s.App.Tmux = tmux.New(startingFake{f})
	writeFile(t, filepath.Join(s.Cfg.WorktreeDir("home-network", "s1"), "notes.txt"), "a\n")
	saveComments(t, ts.URL, api.ReviewComments{Comments: []api.Comment{{File: "notes.txt", Side: "new", Line: 1, Body: "x"}}})

	// the new session's Claude never shows up: the comments stay unsent
	res, code := postStatus(t, ts.URL+reviewPath+"/send", api.ReviewSend{New: true})
	if code != http.StatusGatewayTimeout || res.Num != 2 || !strings.Contains(res.Error, "Comments not sent") {
		t.Fatalf("%d %+v", code, res)
	}
	if len(f.Inputs) != 0 {
		t.Fatal("nothing must be pasted")
	}
	r, _ := get[api.Review](t, ts, reviewPath)
	if r.Comments[0].SentAt != 0 {
		t.Fatal("marked sent after a timeout")
	}
	// once it is ready, a send goes through
	s.Lister = fakeLister{[]claude.Agent{{SessionID: "fixed-uuid", Status: "idle"}}}
	res, code = postStatus(t, ts.URL+reviewPath+"/send", api.ReviewSend{Session: 2})
	if code != 200 || res.Message != "Pasted 1 comment into [2]" {
		t.Fatalf("%d %+v", code, res)
	}
}

func TestSendRefusesASessionOfAnotherStoryAndAFailedPaste(t *testing.T) {
	s, f, ts := newServer(t)
	writeFile(t, filepath.Join(s.Cfg.WorktreeDir("home-network", "s1"), "notes.txt"), "a\n")
	saveComments(t, ts.URL, api.ReviewComments{Comments: []api.Comment{{File: "notes.txt", Side: "new", Line: 1, Body: "x"}}})
	if _, code := postStatus(t, ts.URL+reviewPath+"/send", api.ReviewSend{Session: 42}); code != 404 {
		t.Fatalf("unknown session: %d", code)
	}
	f.Errs = map[string]error{"paste-buffer -p -d -b bx-review-5 -t $5": os.ErrPermission}
	res, code := postStatus(t, ts.URL+reviewPath+"/send", api.ReviewSend{Session: 1})
	if code != http.StatusBadGateway || !strings.HasPrefix(res.Error, "Couldn't paste into [1]. Comments not sent") {
		t.Fatalf("%d %+v", code, res)
	}
	r, _ := get[api.Review](t, ts, reviewPath)
	if r.Comments[0].SentAt != 0 {
		t.Fatal("marked sent after a failed paste")
	}
}
