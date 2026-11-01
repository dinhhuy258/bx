package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerServesApp(t *testing.T) {
	ts := httptest.NewServer(Handler())
	defer ts.Close()
	for _, c := range []struct{ path, want string }{
		{"/", "<title>bx</title>"},
		{"/vendor/xterm.js", "Terminal"},
	} {
		r, err := http.Get(ts.URL + c.path)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		if r.StatusCode != 200 || !strings.Contains(string(b), c.want) {
			t.Errorf("%s: %d, missing %q", c.path, r.StatusCode, c.want)
		}
		if csp := r.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") {
			t.Errorf("%s: CSP %q", c.path, csp)
		}
	}
	if r, _ := http.Get(ts.URL + "/nope.js"); r.StatusCode != 404 {
		t.Errorf("missing file: %d", r.StatusCode)
	}
}
