package notify

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseHookInput(t *testing.T) {
	in := `{"session_id":"u2","hook_event_name":"PermissionRequest","tool_name":"Bash","cwd":"/x"}`
	h, err := ParseHook(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if h.SessionID != "u2" || h.Event != "PermissionRequest" {
		t.Fatalf("%+v", h)
	}
}

func TestMessageNeverIncludesPromptText(t *testing.T) {
	h := Hook{SessionID: "u2", Event: "Notification", Type: "permission_prompt", Message: "Allow Bash to run rm -rf?"}
	title, body := Message(h, "home-network", "guest-vlan-fix", "Check RouterOS syntax")
	if title != "Check RouterOS syntax" {
		t.Fatal(title)
	}
	if body != "home-network · guest-vlan-fix · permission" {
		t.Fatal(body)
	}
	if _, body := Message(h, "home-network", "", "x"); body != "home-network · chats · permission" {
		t.Fatal("a chat names chats:", body)
	}
	if strings.Contains(body, "rm -rf") {
		t.Fatal("prompt text leaked")
	}
}

func TestPostToNtfy(t *testing.T) {
	var gotTitle, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTitle = r.Header.Get("Title")
		b := make([]byte, 256)
		n, _ := r.Body.Read(b)
		gotBody = string(b[:n])
	}))
	defer srv.Close()
	if err := Post(srv.URL, "t", "b", ""); err != nil {
		t.Fatal(err)
	}
	if gotTitle != "t" || gotBody != "b" {
		t.Fatalf("%q %q", gotTitle, gotBody)
	}
}

func TestPostSkipsWhenURLEmpty(t *testing.T) {
	if err := Post("", "t", "b", "http://h/#/p/x/s/1"); err != nil {
		t.Fatal(err)
	}
}

// clickHeader posts through a test ntfy and returns the Click header values it saw.
func clickHeader(t *testing.T, click string) []string {
	t.Helper()
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Values("Click")
	}))
	defer srv.Close()
	if err := Post(srv.URL, "t", "b", click); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestPostSendsClick(t *testing.T) {
	if got := clickHeader(t, "http://h/#/p/x/s/1"); len(got) != 1 || got[0] != "http://h/#/p/x/s/1" {
		t.Fatalf("Click = %q", got)
	}
}

func TestPostOmitsClick(t *testing.T) {
	if got := clickHeader(t, ""); len(got) != 0 {
		t.Fatalf("Click = %q, want no header", got)
	}
}

func TestSessionURLEscapes(t *testing.T) {
	for _, c := range []struct {
		base, project string
		num           int
		want          string
	}{
		{"http://h:7357", "home-network", 2, "http://h:7357/#/p/home-network/s/2"},
		{"http://h:7357/", "home-network", 2, "http://h:7357/#/p/home-network/s/2"},
		{"https://bx.tail1234.ts.net", "my proj#1/x?", 10, "https://bx.tail1234.ts.net/#/p/my%20proj%231%2Fx%3F/s/10"},
	} {
		if got := SessionURL(c.base, c.project, c.num); got != c.want {
			t.Errorf("SessionURL(%q, %q, %d) = %q, want %q", c.base, c.project, c.num, got, c.want)
		}
	}
}

func TestClickBaseOrder(t *testing.T) {
	noResolve := func(string) (string, error) {
		t.Fatal("resolver called although open_url is set")
		return "", nil
	}
	if got, err := ClickBase(" https://bx.tail1234.ts.net/ ", "auto", noResolve); err != nil || got != "https://bx.tail1234.ts.net" {
		t.Errorf("open_url: %q %v", got, err)
	}

	var gotListen string
	resolve := func(addr string) func(string) (string, error) {
		return func(l string) (string, error) { gotListen = l; return addr, nil }
	}
	if got, err := ClickBase("", "auto", resolve("100.64.1.2:7357")); err != nil || got != "http://100.64.1.2:7357" {
		t.Errorf("listen fallback: %q %v", got, err)
	}
	if gotListen != "auto" {
		t.Errorf("resolver got %q, want auto", gotListen)
	}
	if got, err := ClickBase("", "auto", resolve("[fd7a::1]:7357")); err != nil || got != "http://[fd7a::1]:7357" {
		t.Errorf("ipv6: %q %v", got, err)
	}

	failing := func(string) (string, error) { return "", errors.New("no Tailscale address") }
	if got, err := ClickBase("", "auto", failing); err != nil || got != "" {
		t.Errorf("no base: %q %v", got, err)
	}

	for _, bad := range []string{"100.64.1.2:7357", "ftp://x", "http://"} {
		if got, err := ClickBase(bad, "auto", noResolve); err == nil || got != "" {
			t.Errorf("open_url %q: %q %v, want an error", bad, got, err)
		}
	}
}
