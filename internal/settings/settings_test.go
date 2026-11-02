package settings

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func nolog(string, ...any) {}

func TestOpenMissingHasNoValues(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "settings.json"), nolog)
	if err != nil {
		t.Fatal(err)
	}
	if s.Get("claude.model") != "" || len(s.Values()) != 0 {
		t.Fatalf("values = %v", s.Values())
	}
}

func TestSetThenOpenRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	s, _ := Open(path, nolog)
	if err := s.Set(map[string]string{"claude.model": "opus", "claude.permission_mode": "acceptEdits"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Set(map[string]string{"claude.model": ""}); err != nil { // back to the default
		t.Fatal(err)
	}
	got, err := Open(path, nolog)
	if err != nil {
		t.Fatal(err)
	}
	if got.Get("claude.model") != "" || got.Get("claude.permission_mode") != "acceptEdits" {
		t.Fatalf("values = %v", got.Values())
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("temp file left behind: %v", entries)
	}
}

func TestSetBadValueSavesNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	s, _ := Open(path, nolog)
	err := s.Set(map[string]string{"claude.model": "opus", "claude.effort": "huge"})
	if err == nil || err.Error() != `claude.effort "huge": want one of low, medium, high, xhigh, max` {
		t.Fatalf("err = %v", err)
	}
	if s.Get("claude.model") != "" {
		t.Fatal("model was set")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("file was written")
	}
	if err := s.Set(map[string]string{"claude.colour": "red"}); err == nil {
		t.Fatal("unknown key accepted")
	}
}

func TestOpenDropsBadValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"claude.model": "gpt", "claude.effort": "high", "list.colour": "red"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var logged []string
	s, err := Open(path, func(f string, a ...any) { logged = append(logged, f) })
	if err != nil {
		t.Fatal(err)
	}
	if s.Get("claude.model") != "" || s.Get("claude.effort") != "high" || len(s.Values()) != 1 {
		t.Fatalf("values = %v", s.Values())
	}
	if len(logged) != 2 {
		t.Fatalf("logged %d lines", len(logged))
	}
}

func TestOpenQuarantinesCorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path, nolog)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Values()) != 0 {
		t.Fatal("values from a corrupt file")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), "settings.corrupt-") {
		t.Fatalf("entries = %v", entries)
	}
}

func TestNilStore(t *testing.T) {
	var s *Store
	if s.Get("claude.model") != "" || len(s.Values()) != 0 {
		t.Fatal("nil store has values")
	}
}

func TestValidateURL(t *testing.T) {
	for _, ok := range []string{"", "http://ntfy.example/bx", "https://ntfy.sh/bx-secret"} {
		if err := Validate("notifications.ntfy_url", ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"ftp://x", "ntfy.sh/topic", "https://", "http://a b"} {
		err := Validate("notifications.ntfy_url", bad)
		if err == nil {
			t.Errorf("%q accepted", bad)
			continue
		}
		if err.Error() != "notifications.ntfy_url: want an http:// or https:// URL" {
			t.Errorf("%q: err = %v", bad, err)
		}
	}
}

func TestSetBadURLSavesNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	s, _ := Open(path, nolog)
	if err := s.Set(map[string]string{"claude.model": "opus", "notifications.open_url": "nope"}); err == nil {
		t.Fatal("bad URL accepted")
	}
	if len(s.Values()) != 0 {
		t.Fatalf("values = %v", s.Values())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("file was written")
	}
}

func TestOpenDropsBadURL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"notifications.ntfy_url": "ntfy.sh/secret-topic", "notifications.open_url": "https://bx.example"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var logged []string
	s, err := Open(path, func(f string, a ...any) { logged = append(logged, fmt.Sprintf(f, a...)) })
	if err != nil {
		t.Fatal(err)
	}
	if s.Get("notifications.ntfy_url") != "" || s.Get("notifications.open_url") != "https://bx.example" {
		t.Fatalf("values = %v", s.Values())
	}
	if len(logged) != 1 || strings.Contains(logged[0], "secret-topic") {
		t.Fatalf("logged %q", logged)
	}
}

func TestTUIOnlyKey(t *testing.T) {
	for key, want := range map[string]bool{"notifications.ntfy_url": true, "notifications.open_url": true, "claude.model": false, "nope": false} {
		if got := TUIOnlyKey(key); got != want {
			t.Errorf("TUIOnlyKey(%q) = %v", key, got)
		}
	}
}

func TestVisibleHidesTUIOnly(t *testing.T) {
	values := map[string]string{"claude.model": "opus", "notifications.ntfy_url": "https://ntfy.sh/secret"}
	secs, got := Visible(false, values)
	if len(secs) != 1 || secs[0].ID != "claude" {
		t.Fatalf("web sections = %v", secs)
	}
	if len(got) != 1 || got["claude.model"] != "opus" {
		t.Fatalf("web values = %v", got)
	}
	secs, got = Visible(true, values)
	if len(secs) != 2 || secs[1].ID != "notifications" || got["notifications.ntfy_url"] != "https://ntfy.sh/secret" {
		t.Fatalf("tui = %v %v", secs, got)
	}
}
