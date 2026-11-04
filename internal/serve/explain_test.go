package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dinhhuy258/bx/internal/api"
	"github.com/dinhhuy258/bx/internal/review"
)

func postExplain(t *testing.T, ts *httptest.Server, req api.ExplainRequest) (api.Review, int) {
	t.Helper()
	b, _ := json.Marshal(req)
	r, err := http.Post(ts.URL+reviewPath+"/explain", "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	var out api.Review
	_ = json.NewDecoder(r.Body).Decode(&out)
	return out, r.StatusCode
}

// waitExplain polls GET review until explanation 0 is no longer running.
func waitExplain(t *testing.T, ts *httptest.Server) api.Explain {
	t.Helper()
	for range 200 {
		r, _ := get[api.Review](t, ts, reviewPath)
		if len(r.Explains) > 0 && !r.Explains[0].Running() {
			return r.Explains[0]
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("still running")
	return api.Explain{}
}

func TestExplainForksTheSessionAndStoresTheAnswer(t *testing.T) {
	s, _, ts := newServer(t)
	writeFile(t, filepath.Join(s.Cfg.WorktreeDir("home-network", "s1"), "notes.txt"), "a\nb\n")
	var prompt string
	var args []string
	s.App.Claude = func(_ context.Context, _ string, _ []string, in string, as ...string) ([]byte, error) {
		prompt, args = in, as
		return []byte(`{"result":"It lists notes."}`), nil
	}
	r, code := postExplain(t, ts, api.ExplainRequest{File: "notes.txt", Side: "new", StartLine: 1, Line: 2, Code: "a\nb", Session: 1})
	if code != 200 || len(r.Explains) != 1 || r.ExplainWith != 1 || r.Version != 0 {
		t.Fatalf("%d %+v", code, r)
	}
	e := waitExplain(t, ts)
	if e.State != api.ExplainDone || e.Answer != "It lists notes." || e.SessionTitle != "Add lease time" || e.StartLine != 1 {
		t.Fatalf("%+v", e)
	}
	if !strings.Contains(prompt, "notes.txt lines 1-2") || !strings.Contains(strings.Join(args, " "), "--resume u1 --fork-session") {
		t.Fatalf("prompt %q args %q", prompt, args)
	}
	// the comments did not change version
	if r, _ := get[api.Review](t, ts, reviewPath); r.Version != 0 {
		t.Fatalf("version %d", r.Version)
	}
}

func TestExplainRefusesAndStops(t *testing.T) {
	s, _, ts := newServer(t)
	writeFile(t, filepath.Join(s.Cfg.WorktreeDir("home-network", "s1"), "notes.txt"), "a\n")
	release := make(chan struct{})
	s.App.Claude = func(ctx context.Context, _ string, _ []string, _ string, _ ...string) ([]byte, error) {
		select {
		case <-release:
			return []byte(`{"result":"ok"}`), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	t.Cleanup(func() { close(release) })
	ok := api.ExplainRequest{File: "notes.txt", Side: "new", Line: 1, Code: "a", Session: api.ExplainCodeOnly}
	for _, bad := range []api.ExplainRequest{
		{Side: "new", Line: 1, Session: 1},                    // no file
		{File: "notes.txt", Side: "new", Line: 1, Session: 7}, // not a session of s1
		{File: "notes.txt", Side: "new", Line: 1, Session: 0}, // no conversation chosen
		{File: "notes.txt", Side: "x", Line: 1, Session: 1},   // bad side
		{File: "notes.txt", Side: "new", StartLine: 3, Line: 1, Session: 1},
	} {
		if _, code := postExplain(t, ts, bad); code < 400 {
			t.Errorf("%+v: %d", bad, code)
		}
	}
	postExplain(t, ts, ok)
	r, _ := postExplain(t, ts, ok)
	if _, code := postExplain(t, ts, ok); code != http.StatusConflict {
		t.Fatalf("a third at once: %d", code)
	}
	// DELETE stops a running one and drops it
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+reviewPath+"/explain/"+r.Explains[0].ID, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var after api.Review
	_ = json.NewDecoder(resp.Body).Decode(&after)
	resp.Body.Close()
	if resp.StatusCode != 200 || len(after.Explains) != 1 || after.Explains[0].ID == r.Explains[0].ID {
		t.Fatalf("delete: %d %+v", resp.StatusCode, after.Explains)
	}
}

func TestExplainLeftRunningByARestartShowsFailed(t *testing.T) {
	s, _, ts := newServer(t)
	path := s.Cfg.ExplainFile("home-network", "s1")
	if err := review.SaveExplains(path, review.ExplainFile{Explains: []api.Explain{{ID: "old", File: "x.go", Side: "new", Line: 1, State: api.ExplainRunning}}}); err != nil {
		t.Fatal(err)
	}
	r, _ := get[api.Review](t, ts, reviewPath)
	if e := r.Explains[0]; e.State != api.ExplainFailed || e.Error != "stopped: bx serve restarted" {
		t.Fatalf("%+v", e)
	}
}
