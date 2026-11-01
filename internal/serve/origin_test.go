package serve

import (
	"net/http"
	"strings"
	"testing"
)

func TestSameOriginGuard(t *testing.T) {
	_, _, ts := newServer(t)
	do := func(method, path, ctype, origin, body string) int {
		t.Helper()
		req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
		if ctype != "" {
			req.Header.Set("Content-Type", ctype)
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = r.Body.Close()
		return r.StatusCode
	}
	rename := `{"num":1,"title":"renamed"}`
	cases := []struct {
		name, method, path, ctype, origin, body string
		want                                    int
	}{
		{"text/plain post", "POST", "/api/projects/home-network/rename", "text/plain", "", rename, 415},
		{"form post", "POST", "/api/projects/home-network/rename", "application/x-www-form-urlencoded", "", rename, 415},
		{"no content type", "PUT", "/api/settings", "", "", `{"values":{}}`, 415},
		{"cross-origin json", "POST", "/api/projects/home-network/rename", "application/json", "http://evil.example", rename, 403},
		{"cross-origin get", "GET", "/api/projects", "", "http://evil.example", "", 403},
		{"same-origin json", "POST", "/api/projects/home-network/rename", "application/json; charset=utf-8", ts.URL, rename, 200},
		{"tui json, no origin", "POST", "/api/projects/home-network/rename", "application/json", "", rename, 200},
		{"plain get", "GET", "/api/projects", "", "", "", 200},
	}
	for _, c := range cases {
		if got := do(c.method, c.path, c.ctype, c.origin, c.body); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
}
