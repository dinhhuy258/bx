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
	s.ClaudeDir = filepath.Join(t.TempDir(), "claude") // missing: created by the first apply
	return s, ts, s.ClaudeDir
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
		out = append(out, api.SyncFile{Path: p, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(c))})
	}
	return out
}

func TestClaudeSyncPlanAndApply(t *testing.T) {
	s, ts, dir := syncServer(t)
	all := claudesync.Folders
	mac := map[string]string{"skills/a/SKILL.md": "a", "agents/b.md": "b"}
	plan, _, code := postAs[api.SyncPlan](t, ts, "/api/claude/sync/plan", api.SyncPlanRequest{Folders: all, Files: manifest(mac)})
	if code != 200 || plan.Dir != dir || len(plan.Add) != 2 || len(plan.Update)+len(plan.Delete)+plan.Unchanged+plan.Kept != 0 {
		t.Fatalf("%d %+v", code, plan)
	}
	req := api.SyncApplyRequest{Folders: all, Paths: []string{"skills/a/SKILL.md", "agents/b.md"}, Delete: []string{},
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
	plan, _, _ = postAs[api.SyncPlan](t, ts, "/api/claude/sync/plan", api.SyncPlanRequest{Folders: all, Files: m})
	if len(plan.Add)+len(plan.Update)+len(plan.Delete) != 0 || plan.Unchanged != 2 {
		t.Fatalf("want up to date, got %+v", plan)
	}
	putFile(t, dir, "skills/c/SKILL.md", "mine")
	plan, _, _ = postAs[api.SyncPlan](t, ts, "/api/claude/sync/plan", api.SyncPlanRequest{Folders: all, Files: manifest(map[string]string{"skills/a/SKILL.md": "a2"})})
	if strings.Join(plan.Update, ",") != "skills/a/SKILL.md" || strings.Join(plan.Delete, ",") != "agents/b.md" || plan.Kept != 1 || len(plan.Add) != 0 {
		t.Fatalf("%+v", plan)
	}
	got, res, code = postAs[api.SyncResult](t, ts, "/api/claude/sync", api.SyncApplyRequest{Folders: all, Paths: []string{"skills/a/SKILL.md"},
		Files: []api.SyncFile{{Path: "skills/a/SKILL.md", Content: []byte("a2")}}, Delete: plan.Delete})
	if code != 200 || got != (api.SyncResult{Updated: 1, Deleted: 1}) {
		t.Fatalf("%d %+v %+v", code, got, res)
	}
	if _, err := os.Stat(filepath.Join(dir, "skills/c/SKILL.md")); err != nil {
		t.Fatalf("server-only file must stay: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "agents")); err != nil {
		t.Fatalf("the top folder must stay: %v", err)
	}
}

func TestClaudeSyncRefusals(t *testing.T) {
	_, ts, dir := syncServer(t)
	for _, tc := range []struct {
		name, path string
		body       any
		want       int
	}{
		{"plan: bad folder", "/api/claude/sync/plan", api.SyncPlanRequest{Folders: []string{"other"}}, 400},
		{"plan: bad path", "/api/claude/sync/plan", api.SyncPlanRequest{Folders: []string{"skills"}, Files: []api.SyncFile{{Path: "skills/../../x"}}}, 400},
		{"apply: bad folder", "/api/claude/sync", api.SyncApplyRequest{Folders: []string{"other"}}, 400},
		{"apply: bad path", "/api/claude/sync", api.SyncApplyRequest{Folders: []string{"skills"}, Files: []api.SyncFile{{Path: "skills/../../x", Content: []byte("x")}}}, 400},
		{"apply: file over 1 MB", "/api/claude/sync", api.SyncApplyRequest{Folders: []string{"skills"}, Files: []api.SyncFile{{Path: "skills/big", Content: make([]byte, 2<<20)}}}, 413},
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
	body := `{"folders":["skills"],"files":[{"path":"skills/a","content":"` + strings.Repeat("A", maxApplyBody) + `"}]}`
	r, err := http.Post(ts.URL+"/api/claude/sync", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Body.Close() }()
	var res api.Result
	_ = json.NewDecoder(r.Body).Decode(&res)
	if r.StatusCode != http.StatusRequestEntityTooLarge || !strings.Contains(res.Error, "20 MB") {
		t.Fatalf("%d %+v", r.StatusCode, res)
	}
}

func TestClaudeSyncRefusesServerSymlink(t *testing.T) {
	_, ts, dir := syncServer(t)
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "skills"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "skills/x")); err != nil {
		t.Fatal(err)
	}
	_, res, code := postAs[struct{}](t, ts, "/api/claude/sync", api.SyncApplyRequest{Folders: []string{"skills"}, Paths: []string{"skills/x/SKILL.md"},
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
	r, err := http.Post(ts.URL+"/api/claude/sync", "text/plain", strings.NewReader(`{"folders":["skills"]}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Body.Close()
	if r.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("text/plain: %d", r.StatusCode)
	}
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/claude/sync/plan", strings.NewReader(`{"folders":["skills"]}`))
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
