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
		{"/js/sessionmenu.js", "export function sessionMenu"},
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
		csp := r.Header.Get("Content-Security-Policy")
		for _, d := range []string{"default-src 'self'", "connect-src 'self';", "frame-ancestors 'none'"} {
			if !strings.Contains(csp, d) {
				t.Errorf("%s: CSP %q lacks %q", c.path, csp, d)
			}
		}
		if xfo := r.Header.Get("X-Frame-Options"); xfo != "DENY" {
			t.Errorf("%s: X-Frame-Options %q", c.path, xfo)
		}
	}
	if r, _ := http.Get(ts.URL + "/nope.js"); r.StatusCode != 404 {
		t.Errorf("missing file: %d", r.StatusCode)
	}
}
