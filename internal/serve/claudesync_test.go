package serve

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dinhhuy258/bx/internal/api"
	"github.com/dinhhuy258/bx/internal/claudesync"
)

// postAs posts body as JSON and decodes a 200 reply into T, any other into api.Result.
func postAs[T any](t *testing.T, ts *httptest.Server, path string, body any) (T, api.Result, int) {
	t.Helper()
	var v T
	var res api.Result
	b, _ := json.Marshal(body)
	r, err := http.Post(ts.URL+path, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Body.Close() }()
	if r.StatusCode == http.StatusOK {
		_ = json.NewDecoder(r.Body).Decode(&v)
	} else {
		_ = json.NewDecoder(r.Body).Decode(&res)
	}
	return v, res, r.StatusCode
}

func syncServer(t *testing.T) (*Server, *httptest.Server, string) {
	t.Helper()
	s, _, ts := newServer(t)
	dir := filepath.Join(t.TempDir(), "claude") // missing: created by the first apply
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	return s, ts, dir
}

func putFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func manifest(files map[string]string) []api.SyncFile {
	out := []api.SyncFile{}
	for p, c := range files {
		sum := sha256.Sum256([]byte(c))
		out = append(out, api.SyncFile{Path: p, SHA256: hex.EncodeToString(sum[:])})
	}
	return out
}

// statuses maps each plan item to its status.
func statuses(p api.SyncPlan) map[string]string {
	out := map[string]string{}
	for _, it := range p.Items {
		out[it.Name] = it.Status
	}
	return out
}

func item(p api.SyncPlan, name string) api.SyncItem {
	for _, it := range p.Items {
		if it.Name == name {
			return it
		}
	}
	return api.SyncItem{}
}

func TestClaudeSyncPlanAndApply(t *testing.T) {
	s, ts, dir := syncServer(t)
	mac := map[string]string{"skills/a/SKILL.md": "a", "agents/b.md": "b"}
	plan, _, code := postAs[api.SyncPlan](t, ts, "/api/claude/sync/plan", api.SyncPlanRequest{Files: manifest(mac)})
	want := map[string]string{"agents/b.md": "new", "skills/a": "new"}
	if code != 200 || plan.Dir != dir || !reflect.DeepEqual(statuses(plan), want) {
		t.Fatalf("%d %+v", code, plan)
	}
	if a := item(plan, "skills/a"); !reflect.DeepEqual(a.Add, []string{"skills/a/SKILL.md"}) || a.Error != "" || a.Synced {
		t.Fatalf("%+v", a)
	}
	req := api.SyncApplyRequest{Items: []string{"agents/b.md", "skills/a"}, Paths: []string{"skills/a/SKILL.md", "agents/b.md"}, Delete: []string{},
		Files: []api.SyncFile{{Path: "skills/a/SKILL.md", Content: []byte("a")}, {Path: "agents/b.md", Exec: true, Content: []byte("b")}}}
	got, res, code := postAs[api.SyncResult](t, ts, "/api/claude/sync", req)
	if code != 200 || got != (api.SyncResult{Added: 2}) {
		t.Fatalf("%d %+v %+v", code, got, res)
	}
	if fi, err := os.Stat(dir); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("Claude dir: %v %v", fi, err)
	}
	if fi, err := os.Stat(filepath.Join(dir, "agents/b.md")); err != nil || fi.Mode().Perm() != 0o755 {
		t.Fatalf("exec file: %v %v", fi, err)
	}
	rec, err := claudesync.LoadRecord(filepath.Join(s.Cfg.Root, "claude-sync.json"))
	if err != nil || strings.Join(rec, ",") != "agents/b.md,skills/a/SKILL.md" {
		t.Fatalf("record %q %v", rec, err)
	}

	// the same files again: up to date. Then the Mac changes a, drops b; the server has its own c.
	m := manifest(mac)
	for i := range m {
		m[i].Exec = m[i].Path == "agents/b.md"
	}
	plan, _, _ = postAs[api.SyncPlan](t, ts, "/api/claude/sync/plan", api.SyncPlanRequest{Files: m})
	if want := map[string]string{"agents/b.md": "same", "skills/a": "same"}; !reflect.DeepEqual(statuses(plan), want) {
		t.Fatalf("want up to date, got %+v", plan)
	}
	putFile(t, dir, "skills/c/SKILL.md", "mine")
	plan, _, _ = postAs[api.SyncPlan](t, ts, "/api/claude/sync/plan", api.SyncPlanRequest{Files: manifest(map[string]string{"skills/a/SKILL.md": "a2"})})
	want = map[string]string{"agents/b.md": "removed", "skills/a": "changed", "skills/c": "server-only"}
	if !reflect.DeepEqual(statuses(plan), want) || strings.Join(item(plan, "agents/b.md").Delete, ",") != "agents/b.md" {
		t.Fatalf("%+v", plan)
	}
	if !item(plan, "skills/a").Synced || !item(plan, "agents/b.md").Synced || item(plan, "skills/c").Synced {
		t.Fatalf("synced flags: %+v", plan)
	}

	// b unchecked: only a syncs, b stays on the server and in the record
	got, res, code = postAs[api.SyncResult](t, ts, "/api/claude/sync", api.SyncApplyRequest{Items: []string{"skills/a"}, Paths: []string{"skills/a/SKILL.md"},
		Files: []api.SyncFile{{Path: "skills/a/SKILL.md", Content: []byte("a2")}}})
	if code != 200 || got != (api.SyncResult{Updated: 1}) {
		t.Fatalf("%d %+v %+v", code, got, res)
	}
	if _, err := os.Stat(filepath.Join(dir, "agents/b.md")); err != nil {
		t.Fatalf("an unchecked item must stay: %v", err)
	}
	// b checked: deleted
	got, res, code = postAs[api.SyncResult](t, ts, "/api/claude/sync", api.SyncApplyRequest{Items: []string{"agents/b.md"}, Delete: []string{"agents/b.md"}})
	if code != 200 || got != (api.SyncResult{Deleted: 1}) {
		t.Fatalf("%d %+v %+v", code, got, res)
	}
	if _, err := os.Stat(filepath.Join(dir, "skills/c/SKILL.md")); err != nil {
		t.Fatalf("server-only file must stay: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "agents")); err != nil {
		t.Fatalf("the top folder must stay: %v", err)
	}
	rec, err = claudesync.LoadRecord(filepath.Join(s.Cfg.Root, "claude-sync.json"))
	if err != nil || strings.Join(rec, ",") != "skills/a/SKILL.md" {
		t.Fatalf("record %q %v", rec, err)
	}
}

func TestClaudeSyncRefusals(t *testing.T) {
	s, ts, dir := syncServer(t)
	if err := claudesync.SaveRecord(filepath.Join(s.Cfg.Root, "claude-sync.json"), []string{"skills/b/SKILL.md"}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, path string
		body       any
		want       int
	}{
		{"plan: unknown folder", "/api/claude/sync/plan", api.SyncPlanRequest{Files: []api.SyncFile{{Path: "other/x.md"}}}, 400},
		{"plan: bad path", "/api/claude/sync/plan", api.SyncPlanRequest{Files: []api.SyncFile{{Path: "skills/../../x"}}}, 400},
		{"apply: no items", "/api/claude/sync", api.SyncApplyRequest{}, 400},
		{"apply: bad item", "/api/claude/sync", api.SyncApplyRequest{Items: []string{"other/a"}}, 400},
		{"apply: bad path", "/api/claude/sync", api.SyncApplyRequest{Items: []string{"skills/a"}, Files: []api.SyncFile{{Path: "skills/../../x", Content: []byte("x")}}}, 400},
		{"apply: delete outside items", "/api/claude/sync", api.SyncApplyRequest{Items: []string{"skills/a"}, Delete: []string{"skills/b/SKILL.md"}}, 400},
		{"apply: file over 1 MB", "/api/claude/sync", api.SyncApplyRequest{Items: []string{"skills/big"}, Files: []api.SyncFile{{Path: "skills/big", Content: make([]byte, 2<<20)}}}, 413},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, res, code := postAs[struct{}](t, ts, tc.path, tc.body)
			if code != tc.want || res.Error == "" {
				t.Fatalf("%d %+v", code, res)
			}
		})
	}
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Fatalf("nothing may be written: %v", err)
	}
}

func TestClaudeSyncBodyOverLimit(t *testing.T) {
	_, ts, _ := syncServer(t)
	for _, tc := range []struct{ path, body, want string }{
		{"/api/claude/sync", `{"items":["skills/a"],"files":[{"path":"skills/a","content":"` + strings.Repeat("A", maxApplyBody) + `"}]}`,
			"sync request over 28 MB (about 20 MB of files once base64-encoded)"},
		{"/api/claude/sync/plan", `{"files":[{"path":"skills/a","sha256":"` + strings.Repeat("A", maxPlanBody) + `"}]}`,
			"plan request over 8 MB"},
	} {
		r, err := http.Post(ts.URL+tc.path, "application/json", strings.NewReader(tc.body))
		if err != nil {
			t.Fatal(err)
		}
		var res api.Result
		_ = json.NewDecoder(r.Body).Decode(&res)
		_ = r.Body.Close()
		if r.StatusCode != http.StatusRequestEntityTooLarge || res.Error != tc.want {
			t.Fatalf("%s: %d %+v", tc.path, r.StatusCode, res)
		}
	}
}

// A server item behind a symlink is an error on that item in the plan; the others still sync.
func TestClaudeSyncServerSymlinkIsAnItemError(t *testing.T) {
	_, ts, dir := syncServer(t)
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "skills"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "skills/x")); err != nil {
		t.Fatal(err)
	}
	mac := map[string]string{"skills/x/SKILL.md": "x", "skills/y/SKILL.md": "y"}
	plan, _, code := postAs[api.SyncPlan](t, ts, "/api/claude/sync/plan", api.SyncPlanRequest{Files: manifest(mac)})
	if code != 200 || item(plan, "skills/x").Error != "skills/x: refusing to write through a symlink" || item(plan, "skills/y").Error != "" {
		t.Fatalf("%d %+v", code, plan)
	}
	got, res, code := postAs[api.SyncResult](t, ts, "/api/claude/sync", api.SyncApplyRequest{Items: []string{"skills/y"}, Paths: []string{"skills/y/SKILL.md"},
		Files: []api.SyncFile{{Path: "skills/y/SKILL.md", Content: []byte("y")}}})
	if code != 200 || got != (api.SyncResult{Added: 1}) {
		t.Fatalf("%d %+v %+v", code, got, res)
	}
	// forcing it anyway is still refused
	_, res, code = postAs[struct{}](t, ts, "/api/claude/sync", api.SyncApplyRequest{Items: []string{"skills/x"}, Paths: []string{"skills/x/SKILL.md"},
		Files: []api.SyncFile{{Path: "skills/x/SKILL.md", Content: []byte("x")}}})
	if code != 500 || !strings.Contains(res.Error, "skills/x") {
		t.Fatalf("%d %+v", code, res)
	}
	if _, err := os.Stat(filepath.Join(outside, "SKILL.md")); !os.IsNotExist(err) {
		t.Fatalf("wrote through the symlink: %v", err)
	}
}

func TestClaudeSyncGuards(t *testing.T) {
	_, ts, _ := syncServer(t)
	r, err := http.Post(ts.URL+"/api/claude/sync", "text/plain", strings.NewReader(`{"items":["skills/a"]}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Body.Close()
	if r.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("text/plain: %d", r.StatusCode)
	}
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/claude/sync/plan", strings.NewReader(`{"files":[]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://evil")
	r, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Body.Close()
	if r.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin: %d", r.StatusCode)
	}
}
