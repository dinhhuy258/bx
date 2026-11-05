package serve

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dinhhuy258/bx/internal/api"
	"github.com/dinhhuy258/bx/internal/prwatch"
	"github.com/dinhhuy258/bx/internal/tmux"
	"github.com/dinhhuy258/bx/internal/ui"
)

// prFixture makes story s1 look pushed to acme/netcfg and answers gh api graphql with reply().
// It returns the calls gh got.
func prFixture(t *testing.T, s *Server, reply func() string) *[][]string {
	t.Helper()
	dir := s.Cfg.WorktreeDir("home-network", "s1")
	branch, err := s.App.Git.CurrentBranch(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"remote", "add", "origin", "git@github.com:acme/netcfg.git"},
		{"update-ref", "refs/remotes/origin/" + branch, "HEAD"},
	} {
		c := exec.Command("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	var mu sync.Mutex
	var calls [][]string
	s.App.GH = func(_ context.Context, d string, args ...string) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, append([]string{d}, args...))
		if len(args) >= 2 && args[0] == "api" && args[1] == "graphql" {
			return reply(), nil
		}
		return "", nil
	}
	return &calls
}

func graphqlJSON(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("../prwatch/testdata/graphql.json")
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestPollPRsSavesBadgeAndNotifies(t *testing.T) {
	s, _, _ := newServer(t)
	raw := graphqlJSON(t)
	passing := strings.Replace(raw, `"conclusion":"FAILURE"`, `"conclusion":"SUCCESS"`, 1)
	cur := passing
	prFixture(t, s, func() string { return cur })

	var mu sync.Mutex
	var titles []string
	ntfy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		titles = append(titles, r.Header.Get("Title"))
		mu.Unlock()
	}))
	defer ntfy.Close()
	if err := s.App.Settings.Set(map[string]string{"notifications.ntfy_url": ntfy.URL}); err != nil {
		t.Fatal(err)
	}

	// the first look is silent
	s.pollPRs(context.Background(), nil)
	f, err := prwatch.Load(s.Cfg.PRFile("home-network", "s1"))
	if err != nil || len(f.PRs) != 1 || f.PRs[0].Number != 142 {
		t.Fatalf("file = %+v %v", f, err)
	}
	p, _ := s.App.LoadProject("home-network")
	if b := s.collect(p).PR["s1"]; b.Number != 142 || b.Count != 1 {
		t.Fatalf("badge = %+v", b)
	}
	if len(titles) != 0 {
		t.Fatalf("first look posted %v", titles)
	}

	// a check fails
	cur = raw
	s.pollPRs(context.Background(), map[string]bool{prKey("home-network", "s1"): true})
	mu.Lock()
	defer mu.Unlock()
	if len(titles) != 1 || titles[0] != "✗ CI failed · s1 #142" {
		t.Fatalf("titles = %q", titles)
	}
	p, _ = s.App.LoadProject("home-network")
	if b := s.collect(p).PR["s1"]; !b.Fail {
		t.Fatalf("badge = %+v", b)
	}
}

func TestPollPRsNotPushed(t *testing.T) {
	s, _, _ := newServer(t)
	calls := prFixture(t, s, func() string { return graphqlJSON(t) })
	dir := s.Cfg.WorktreeDir("home-network", "s1")
	branch, _ := s.App.Git.CurrentBranch(dir)
	c := exec.Command("git", "update-ref", "-d", "refs/remotes/origin/"+branch)
	c.Dir = dir
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	s.pollPRs(context.Background(), nil)
	f, err := prwatch.Load(s.Cfg.PRFile("home-network", "s1"))
	if err != nil || len(f.PRs) != 1 || f.PRs[0].Missing != "not pushed" {
		t.Fatalf("file = %+v %v", f, err)
	}
	if len(*calls) != 0 {
		t.Fatalf("gh was called: %v", *calls)
	}
}

const prPath = "/api/projects/home-network/stories/s1/pr"

// polled is a server whose story s1 has been looked at once: PR #142 with a failed check
// (run 9182736) and the open thread T1.
func polled(t *testing.T) (*Server, *tmux.Fake, *httptest.Server, *[][]string) {
	t.Helper()
	s, f, ts := newServer(t)
	calls := prFixture(t, s, func() string { return graphqlJSON(t) })
	s.pollPRs(context.Background(), nil)
	return s, f, ts, calls
}

func TestGetPR(t *testing.T) {
	_, _, ts, _ := polled(t)
	d, code := get[api.PRData](t, ts, prPath)
	if code != 200 || len(d.PRs) != 1 || d.PRs[0].Number != 142 || d.Me != "huy" || len(d.PRs[0].Threads) != 1 || d.PRs[0].Threads[0].SentAt != 0 {
		t.Fatalf("%d %+v", code, d)
	}
	if _, code := get[api.PRData](t, ts, "/api/projects/home-network/stories/nope/pr"); code != 404 {
		t.Fatalf("unknown story: %d", code)
	}
	r, err := http.Post(ts.URL+prPath+"/refresh", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		t.Fatalf("refresh: %d", r.StatusCode)
	}
}

func TestSendAddressSubmitsAndStamps(t *testing.T) {
	_, f, ts, _ := polled(t)
	res, code := postStatus(t, ts.URL+prPath+"/send", api.PRSend{Kind: "address", Threads: []string{"T1"}, Session: 1})
	if code != 200 || res.Num != 1 || res.Message != "Sent 1 comment to [1]" {
		t.Fatalf("%d %+v", code, res)
	}
	if len(f.Inputs) != 1 || !strings.Contains(f.Inputs[0], "Review comments on PR #142") || !strings.Contains(f.Inputs[0], "dhcp/lease.go:24") {
		t.Fatalf("pasted %q", f.Inputs)
	}
	c := calls(f)
	if !strings.Contains(c, "paste-buffer -p -d -b bx-review-5 -t $5") || !strings.HasSuffix(strings.TrimSpace(c), "send-keys -t $5 Enter") {
		t.Fatalf("calls:\n%s", c)
	}
	d, _ := get[api.PRData](t, ts, prPath)
	if d.PRs[0].Threads[0].SentAt == 0 {
		t.Fatalf("not stamped: %+v", d.PRs[0].Threads[0])
	}
}

func TestSendFixReadsTheFailedLog(t *testing.T) {
	s, f, ts, _ := polled(t)
	var runs []string
	graphql := s.App.GH
	s.App.GH = func(ctx context.Context, dir string, args ...string) (string, error) {
		if len(args) > 0 && args[0] == "run" {
			runs = append(runs, strings.Join(args, " "))
			return "--- FAIL: TestLease\n", nil
		}
		return graphql(ctx, dir, args...)
	}
	res, code := postStatus(t, ts.URL+prPath+"/send", api.PRSend{Kind: "fix", Session: 1})
	if code != 200 || res.Message != "Sent the failed checks of #142 to [1]" {
		t.Fatalf("%d %+v", code, res)
	}
	if len(runs) != 1 || runs[0] != "run view 9182736 --log-failed" {
		t.Fatalf("runs = %v", runs)
	}
	if len(f.Inputs) != 1 || !strings.Contains(f.Inputs[0], "Failed check: test / go 1.24") || !strings.Contains(f.Inputs[0], "--- FAIL: TestLease") {
		t.Fatalf("pasted %q", f.Inputs)
	}
}

func TestSendNothing(t *testing.T) {
	s, _, ts, _ := polled(t)
	if res, code := postStatus(t, ts.URL+prPath+"/send", api.PRSend{Kind: "address", Session: 1}); code != http.StatusConflict || res.Error != "pick a comment first" {
		t.Fatalf("%d %+v", code, res)
	}
	passing := strings.ReplaceAll(graphqlJSON(t), `"conclusion":"FAILURE"`, `"conclusion":"SUCCESS"`)
	s.App.GH = func(context.Context, string, ...string) (string, error) { return passing, nil }
	s.pollPRs(context.Background(), map[string]bool{prKey("home-network", "s1"): true})
	if res, code := postStatus(t, ts.URL+prPath+"/send", api.PRSend{Kind: "fix", Session: 1}); code != http.StatusConflict || res.Error != "no failed check" {
		t.Fatalf("%d %+v", code, res)
	}
}

func TestPollPRsKeepsTheLastLookWhenGHFails(t *testing.T) {
	s, _, _, _ := polled(t)
	s.App.GH = func(context.Context, string, ...string) (string, error) {
		return "", errors.New("gh: HTTP 502")
	}
	s.pollPRs(context.Background(), map[string]bool{prKey("home-network", "s1"): true})
	f, err := prwatch.Load(s.Cfg.PRFile("home-network", "s1"))
	if err != nil || len(f.PRs) != 1 || f.PRs[0].Number != 142 {
		t.Fatalf("a failed look replaced the PR: %+v %v", f, err)
	}
	p, _ := s.App.LoadProject("home-network")
	if b := s.collect(p).PR["s1"]; b.Number != 142 {
		t.Fatalf("badge = %+v", b)
	}
}

func TestSavePRLookLeavesAClosedStoryAlone(t *testing.T) {
	s, _, _, _ := polled(t)
	p, _ := s.App.LoadProject("home-network")
	ts := s.prTargets(p, "s1")
	delete(p.Stories, "s1")
	if err := s.App.SaveProject(p); err != nil {
		t.Fatal(err)
	}
	path := s.Cfg.PRFile("home-network", "s1")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	s.savePRLook("home-network", "s1", ts, []prLook{{pr: ui.PRInfo{Number: 142, State: ui.PROpen}}}, time.Now())
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("closed story's PR file written back: %v", err)
	}
	if _, ok := s.prBadgesOf("home-network")["s1"]; ok {
		t.Fatal("closed story kept its badge")
	}
}

func TestPRBadgesWhilePolling(t *testing.T) {
	s, _, _, _ := polled(t)
	p, _ := s.App.LoadProject("home-network")
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range 20 {
			s.pollPRs(context.Background(), map[string]bool{prKey("home-network", "s1"): true})
		}
	}()
	go func() {
		defer wg.Done()
		for range 200 {
			_ = s.collect(p).PR
		}
	}()
	wg.Wait()
}
