package commands

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/dinhhuy258/bx/internal/api"
)

func TestIsPage(t *testing.T) {
	const dir = "/root/.bx/artifacts/home-network/1"
	for file, want := range map[string]bool{
		dir + "/report.html":       true,
		dir + "/Report.HTML":       true,
		dir + "/./report.html":     true,
		dir + "/.draft.html":       false,
		dir + "/report.md":         false,
		dir + "/sub/report.html":   false,
		dir + "/../2/report.html":  false,
		"/home/me/repo/index.html": false,
		"":                         false,
	} {
		if got := isPage(dir, file); got != want {
			t.Errorf("isPage(%q) = %v", file, got)
		}
	}
	if isPage("", dir+"/report.html") {
		t.Error("no $BX_ARTIFACTS: not a page")
	}
}

func TestWriteUpdated(t *testing.T) {
	for resp, want := range map[string]bool{
		`{"type":"update","filePath":"x"}`: true,
		`{"type":"create"}`:                false,
		`"Wrote 3 lines"`:                  false,
		``:                                 false,
	} {
		if got := writeUpdated(json.RawMessage(resp)); got != want {
			t.Errorf("writeUpdated(%s) = %v", resp, got)
		}
	}
}

// pageHookServer is a fake bx serve that records the pages posted to it, and runs the hook with
// stdin and args (the URL is appended when there are none). BX_ARTIFACTS is dir.
func pageHookServer(t *testing.T, dir string) (got *[]api.PageHook, ts *httptest.Server, run func(stdin string, args ...string)) {
	t.Helper()
	got = &[]api.PageHook{}
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var h api.PageHook
		if r.URL.Path != "/api/hooks/page" || r.Header.Get("Content-Type") != "application/json" || json.NewDecoder(r.Body).Decode(&h) != nil {
			t.Errorf("bad request %s %s", r.URL.Path, r.Header.Get("Content-Type"))
		}
		*got = append(*got, h)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(ts.Close)
	t.Setenv("BX_ARTIFACTS", dir)
	t.Setenv("TMPDIR", t.TempDir()) // the Bash snapshots
	run = func(stdin string, args ...string) {
		t.Helper()
		if len(args) == 0 {
			args = []string{ts.URL}
		}
		f, _ := os.CreateTemp(t.TempDir(), "in")
		_, _ = f.WriteString(stdin)
		_, _ = f.Seek(0, 0)
		old := os.Stdin
		os.Stdin = f
		defer func() { os.Stdin = old; f.Close() }()
		if code := runPageHook(args, io.Discard, io.Discard); code != 0 {
			t.Fatalf("exit %d", code)
		}
	}
	return got, ts, run
}

func TestPageHookPostsAPage(t *testing.T) {
	dir := t.TempDir()
	gotp, ts, run := pageHookServer(t, dir)
	run(`{"session_id":"u1","tool_name":"Write","tool_input":{"file_path":"` + dir + `/a.html"},"tool_response":{"type":"create"}}`)
	run(`{"session_id":"u1","tool_name":"Edit","tool_input":{"file_path":"` + dir + `/a.html"}}`)
	run(`{"session_id":"u1","tool_name":"Write","tool_input":{"file_path":"/repo/main.go"}}`) // not a page: nothing sent
	run(`not json`)
	if got := *gotp; len(got) != 2 || got[0] != (api.PageHook{SessionID: "u1", Path: dir + "/a.html", New: true}) || got[1].New {
		t.Fatalf("%+v", got)
	}
	ts.Close()
	run(`{"session_id":"u1","tool_name":"Write","tool_input":{"file_path":"` + dir + `/a.html"}}`) // server down: still exit 0
}

func TestPageHookSeesPagesBashWrote(t *testing.T) {
	dir := t.TempDir()
	got, _, run := pageHookServer(t, dir)
	write := func(name, body string, at time.Time) {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
	}
	t0 := time.Now().Add(-time.Hour)
	write("old.html", "old", t0)
	write("same.html", "same", t0)
	bash := func(id string, body func()) {
		t.Helper()
		in := `{"session_id":"u1","tool_name":"Bash","tool_use_id":"` + id + `","tool_input":{"command":"x"}}`
		run(in, "--before")
		body()
		run(in)
	}
	bash("toolu_1", func() {
		write("b.html", "new", t0.Add(2*time.Minute))
		write("old.html", "changed", t0.Add(time.Minute))
		write("notes.md", "not a page", t0.Add(3*time.Minute))
		write(".draft.html", "hidden", t0.Add(3*time.Minute))
	})
	want := []api.PageHook{{SessionID: "u1", Path: filepath.Join(dir, "old.html")}, {SessionID: "u1", Path: filepath.Join(dir, "b.html"), New: true}}
	if !reflect.DeepEqual(*got, want) {
		t.Fatalf("got %+v, want %+v (oldest first)", *got, want)
	}
	*got = nil
	bash("toolu_2", func() {})                                                                         // touched no page
	run(`{"session_id":"u1","tool_name":"Bash","tool_use_id":"toolu_3","tool_input":{"command":"x"}}`) // no snapshot: nothing
	if len(*got) != 0 {
		t.Fatalf("%+v", *got)
	}
	if left, _ := filepath.Glob(filepath.Join(os.TempDir(), "bx-page-hook-*", "*")); len(left) != 0 {
		t.Fatalf("snapshots left: %v", left)
	}
	run(`{"session_id":"u1","tool_name":"Bash","tool_use_id":"../x","tool_input":{"command":"x"}}`, "--before")
	if snapshotPath("../x") != "" || snapshotPath("") != "" || snapshotPath("toolu_1") == "" {
		t.Fatal("a tool_use_id must name a file in the snapshot folder")
	}
}
