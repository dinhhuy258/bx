package serve

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dinhhuy258/bx/internal/api"
)

// upload posts body to path with the given content type and origin and decodes the reply.
func upload(t *testing.T, ts *httptest.Server, path, ctype, origin string, body []byte) (api.UploadResult, string, int) {
	t.Helper()
	req, _ := http.NewRequest("POST", ts.URL+path, bytes.NewReader(body))
	req.Header.Set("Content-Type", ctype)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Body.Close() }()
	var raw struct {
		Path  string `json:"path"`
		Error string `json:"error"`
	}
	_ = json.NewDecoder(r.Body).Decode(&raw)
	return api.UploadResult{Path: raw.Path}, raw.Error, r.StatusCode
}

func uploadJSON(t *testing.T, ts *httptest.Server, path, name string, content []byte) (api.UploadResult, string, int) {
	t.Helper()
	b, _ := json.Marshal(api.UploadRequest{Name: name, Content: content})
	return upload(t, ts, path, "application/json", "", b)
}

const filesPath = "/api/projects/home-network/sessions/1/files"

// countFiles counts the regular files under dir; a missing dir holds none.
func countFiles(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	_ = filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			n++
		}
		return nil
	})
	return n
}

func TestUploadSavesFile(t *testing.T) {
	s, _, ts := newServer(t)
	res, msg, code := uploadJSON(t, ts, filesPath, "photo.jpg", []byte("jpeg bytes"))
	if code != 200 {
		t.Fatalf("%d %s", code, msg)
	}
	dir := filepath.Join(s.Cfg.UploadsDir(), "home-network", "1")
	if !filepath.IsAbs(res.Path) || filepath.Dir(res.Path) != dir || !strings.HasSuffix(res.Path, "-photo.jpg") {
		t.Fatalf("path %q", res.Path)
	}
	if b, err := os.ReadFile(res.Path); err != nil || string(b) != "jpeg bytes" {
		t.Fatalf("content %q %v", b, err)
	}
}

func TestUploadSanitizesName(t *testing.T) {
	s, _, ts := newServer(t)
	res, msg, code := uploadJSON(t, ts, filesPath, "../../evil/.x.png", []byte("x"))
	if code != 200 {
		t.Fatalf("%d %s", code, msg)
	}
	if filepath.Dir(res.Path) != filepath.Join(s.Cfg.UploadsDir(), "home-network", "1") || !strings.HasSuffix(res.Path, "-x.png") {
		t.Fatalf("path %q", res.Path)
	}
	if n := countFiles(t, s.Cfg.UploadsDir()); n != 1 {
		t.Fatalf("%d files under uploads", n)
	}
	if _, err := os.Stat(filepath.Join(s.Cfg.Root, "evil")); !os.IsNotExist(err) {
		t.Fatalf("evil dir: %v", err)
	}
}

func TestUploadSizeLimits(t *testing.T) {
	s, _, ts := newServer(t)
	_, msg, code := uploadJSON(t, ts, filesPath, "big.bin", make([]byte, 20<<20+1))
	if code != http.StatusRequestEntityTooLarge || !strings.Contains(msg, "20 MB") {
		t.Fatalf("file over 20 MB: %d %q", code, msg)
	}
	body := `{"name":"huge.bin","content":"` + strings.Repeat("A", 29<<20) + `"}`
	_, msg, code = upload(t, ts, filesPath, "application/json", "", []byte(body))
	if code != http.StatusRequestEntityTooLarge || !strings.Contains(msg, "28 MB") {
		t.Fatalf("body over 28 MB: %d %q", code, msg)
	}
	if n := countFiles(t, s.Cfg.UploadsDir()); n != 0 {
		t.Fatalf("%d files written", n)
	}
}

func TestUploadUnknownSession(t *testing.T) {
	_, _, ts := newServer(t)
	for _, p := range []string{
		"/api/projects/home-network/sessions/9/files",
		"/api/projects/home-network/sessions/abc/files",
		"/api/projects/nope/sessions/1/files",
	} {
		if _, msg, code := uploadJSON(t, ts, p, "a.txt", []byte("x")); code != 404 {
			t.Errorf("%s: %d %s", p, code, msg)
		}
	}
}

func TestUploadRefusesSymlink(t *testing.T) {
	s, _, ts := newServer(t)
	target := t.TempDir()
	if err := os.MkdirAll(s.Cfg.UploadsDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(s.Cfg.UploadsDir(), "home-network")); err != nil {
		t.Fatal(err)
	}
	if _, msg, code := uploadJSON(t, ts, filesPath, "a.txt", []byte("x")); code != 500 || !strings.Contains(msg, "refusing") {
		t.Fatalf("%d %s", code, msg)
	}
	if n := countFiles(t, target); n != 0 {
		t.Fatalf("%d files written through the symlink", n)
	}
}

func TestUploadSameOrigin(t *testing.T) {
	_, _, ts := newServer(t)
	b, _ := json.Marshal(api.UploadRequest{Name: "a.txt", Content: []byte("x")})
	if _, _, code := upload(t, ts, filesPath, "multipart/form-data; boundary=x", "", b); code != 415 {
		t.Errorf("multipart: %d", code)
	}
	if _, _, code := upload(t, ts, filesPath, "application/json", "http://evil.example", b); code != 403 {
		t.Errorf("cross-origin: %d", code)
	}
	if _, msg, code := upload(t, ts, filesPath, "application/json", ts.URL, b); code != 200 {
		t.Errorf("same origin: %d %s", code, msg)
	}
}
