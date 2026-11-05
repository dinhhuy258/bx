package serve

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveFiles(t *testing.T) {
	for _, c := range []struct {
		cfg, want string
		bad       bool
	}{
		{"auto", "100.64.0.1:7358", false},
		{"", "100.64.0.1:7358", false},
		{"off", "", false},
		{"localhost:9000", "localhost:9000", false},
		{"0.0.0.0:7358", "", true},
	} {
		got, err := ResolveFiles(c.cfg, "100.64.0.1:7357")
		if (err != nil) != c.bad || got != c.want {
			t.Errorf("ResolveFiles(%q) = %q, %v", c.cfg, got, err)
		}
	}
}

func TestFilesHandler(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "home-network", "3")
	if err := os.MkdirAll(filepath.Join(dir, "img"), 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "report.html"), []byte("<title>Lease report</title>"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, ".env"), []byte("SECRET=1"), 0o600)
	outside := filepath.Join(t.TempDir(), "secret.txt")
	_ = os.WriteFile(outside, []byte("secret"), 0o600)
	if err := os.Symlink(outside, filepath.Join(dir, "link.txt")); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(FilesHandler(root))
	defer ts.Close()

	r, err := http.Get(ts.URL + "/home-network/3/report.html")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(r.Body)
	_ = r.Body.Close()
	if r.StatusCode != 200 || !strings.Contains(string(b), "Lease report") {
		t.Fatalf("page: %d %q", r.StatusCode, b)
	}
	for k, want := range map[string]string{
		"Content-Security-Policy": "sandbox allow-scripts",
		"X-Content-Type-Options":  "nosniff",
		"Cache-Control":           "no-store",
		"Referrer-Policy":         "no-referrer",
		"Content-Type":            "text/html",
	} {
		if !strings.HasPrefix(r.Header.Get(k), want) {
			t.Errorf("%s = %q, want %q…", k, r.Header.Get(k), want)
		}
	}
	for _, p := range []string{"/home-network/3/.env", "/home-network/3/link.txt", "/home-network/3/img",
		"/home-network/3", "/home-network/3/missing.html", "/home-network/3/../../../etc/passwd", "/"} {
		r, err := http.Get(ts.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		_ = r.Body.Close()
		if r.StatusCode != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", p, r.StatusCode)
		}
	}
	r, err = http.Post(ts.URL+"/home-network/3/report.html", "text/plain", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Body.Close()
	if r.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d", r.StatusCode)
	}
}

func TestFilesHandlerRendersMarkdown(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "home-network", "3")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "plan.md"), []byte("# Lease plan\n\n| a |\n|---|\n| 1 |\n"), 0o600)
	ts := httptest.NewServer(FilesHandler(root))
	defer ts.Close()
	for _, c := range []struct{ query, ctype, body string }{
		{"", "text/html; charset=utf-8", "<title>Lease plan</title>"},
		{"?raw", "text/plain; charset=utf-8", "# Lease plan"},
	} {
		r, err := http.Get(ts.URL + "/home-network/3/plan.md" + c.query)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		if r.StatusCode != 200 || r.Header.Get("Content-Type") != c.ctype || !strings.Contains(string(b), c.body) {
			t.Errorf("%q: %d %q %q", c.query, r.StatusCode, r.Header.Get("Content-Type"), b)
		}
		if !strings.HasPrefix(r.Header.Get("Content-Security-Policy"), "sandbox allow-scripts") {
			t.Errorf("%q: no sandbox", c.query)
		}
	}
}
