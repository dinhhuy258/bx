package commands

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

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

func TestPageHookPostsAPage(t *testing.T) {
	dir := t.TempDir()
	var got []api.PageHook
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var h api.PageHook
		if r.URL.Path != "/api/hooks/page" || r.Header.Get("Content-Type") != "application/json" || json.NewDecoder(r.Body).Decode(&h) != nil {
			t.Errorf("bad request %s %s", r.URL.Path, r.Header.Get("Content-Type"))
		}
		got = append(got, h)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()
	t.Setenv("BX_ARTIFACTS", dir)
	run := func(stdin string) {
		t.Helper()
		f, _ := os.CreateTemp(t.TempDir(), "in")
		_, _ = f.WriteString(stdin)
		_, _ = f.Seek(0, 0)
		old := os.Stdin
		os.Stdin = f
		defer func() { os.Stdin = old; f.Close() }()
		if code := runPageHook([]string{ts.URL}, io.Discard, io.Discard); code != 0 {
			t.Fatalf("exit %d", code)
		}
	}
	run(`{"session_id":"u1","tool_name":"Write","tool_input":{"file_path":"` + dir + `/a.html"},"tool_response":{"type":"create"}}`)
	run(`{"session_id":"u1","tool_name":"Edit","tool_input":{"file_path":"` + dir + `/a.html"}}`)
	run(`{"session_id":"u1","tool_name":"Write","tool_input":{"file_path":"/repo/main.go"}}`) // not a page: nothing sent
	run(`not json`)
	if len(got) != 2 || got[0] != (api.PageHook{SessionID: "u1", Path: dir + "/a.html", New: true}) || got[1].New {
		t.Fatalf("%+v", got)
	}
	ts.Close()
	run(`{"session_id":"u1","tool_name":"Write","tool_input":{"file_path":"` + dir + `/a.html"}}`) // server down: still exit 0
}
