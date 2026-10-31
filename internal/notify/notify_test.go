package notify

import (
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
	title, body := Message(h, "home-network", 2, "Check RouterOS syntax")
	if title != "home-network [2] needs you" {
		t.Fatal(title)
	}
	if body != "Check RouterOS syntax · permission" {
		t.Fatal(body)
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
	if err := Post(srv.URL, "t", "b"); err != nil {
		t.Fatal(err)
	}
	if gotTitle != "t" || gotBody != "b" {
		t.Fatalf("%q %q", gotTitle, gotBody)
	}
}

func TestPostSkipsWhenURLEmpty(t *testing.T) {
	if err := Post("", "t", "b"); err != nil {
		t.Fatal(err)
	}
}
