package settings

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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
	for key, want := range map[string]bool{"notifications.ntfy_url": true, "notifications.open_url": true, "notifications.enabled": false, "claude.model": false, "nope": false} {
		if got := TUIOnlyKey(key); got != want {
			t.Errorf("TUIOnlyKey(%q) = %v", key, got)
		}
	}
}

func TestNotificationsEnabled(t *testing.T) {
	f, ok := field("notifications.enabled")
	if !ok || f.Kind != Choice || f.Label != "Enabled" || f.Default != "on" || !slices.Equal(f.Choices, []string{"on", "off"}) || f.TUIOnly {
		t.Fatalf("field = %+v", f)
	}
	if Sections[2].ID != "notifications" || Sections[2].Fields[0].Key != "notifications.enabled" {
		t.Fatalf("not first in notifications: %+v", Sections[2])
	}
	for _, v := range []string{"", "on", "off"} {
		if err := Validate(f.Key, v); err != nil {
			t.Errorf("%q: %v", v, err)
		}
	}
	if err := Validate(f.Key, "yes"); err == nil || err.Error() != `notifications.enabled "yes": want one of on, off` {
		t.Errorf("yes: %v", err)
	}
}

func TestVisibleHidesTUIOnly(t *testing.T) {
	values := map[string]string{"claude.model": "opus", "notifications.enabled": "off", "notifications.ntfy_url": "https://ntfy.sh/secret", "notifications.open_url": "https://bx.example"}
	secs, got := Visible(false, values)
	if len(secs) != 2 || secs[0].ID != "claude" || secs[1].ID != "notifications" || len(secs[1].Fields) != 2 || secs[1].Fields[0].Key != "notifications.enabled" || secs[1].Fields[1].Key != "notifications.prs" {
		t.Fatalf("web sections = %+v", secs)
	}
	if len(got) != 2 || got["claude.model"] != "opus" || got["notifications.enabled"] != "off" {
		t.Fatalf("web values = %v", got)
	}
	if len(Sections[2].Fields) != 4 {
		t.Fatal("Visible changed Sections")
	}
	secs, got = Visible(true, values)
	if len(secs) != 3 || len(secs[2].Fields) != 4 || len(got) != 4 || got["notifications.ntfy_url"] != "https://ntfy.sh/secret" {
		t.Fatalf("tui = %v %v", secs, got)
	}
}

func TestReadNeverMovesOrWrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if s, err := Read(path); err != nil || len(s.Values()) != 0 {
		t.Fatalf("missing: %v %v", s, err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("Read made %v", entries)
	}
	if err := os.WriteFile(path, []byte(`{"claude.model":"opus","nope":"x"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if s, err := Read(path); err != nil || s.Get("claude.model") != "opus" || len(s.Values()) != 1 {
		t.Fatalf("good: %v %v", s, err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); err == nil {
		t.Fatal("a corrupt file read without an error")
	}
	entries, _ := os.ReadDir(dir)
	if data, _ := os.ReadFile(path); len(entries) != 1 || string(data) != "{not json" {
		t.Fatalf("Read touched a corrupt file: %v %q", entries, data)
	}
}

func TestWebSeesOnlyItsFields(t *testing.T) {
	if err := Validate("mcp.mode", "strict"); err != nil {
		t.Fatal(err)
	}
	if err := Validate("mcp.mode", "on"); err == nil {
		t.Fatal("want an error for an unknown mode")
	}
	var keys, titles []string
	web, _ := Visible(false, nil)
	for _, s := range web {
		titles = append(titles, s.Title)
		for _, f := range s.Fields {
			keys = append(keys, f.Key)
		}
	}
	want := []string{"claude.model", "claude.effort", "claude.permission_mode", "notifications.enabled", "notifications.prs"}
	if !slices.Equal(keys, want) || !slices.Equal(titles, []string{"Claude", "Notifications"}) {
		t.Fatalf("web sees %v in %v, want %v in [Claude Notifications]", keys, titles, want)
	}
	if !TUIOnlyKey("mcp.mode") || TUIOnlyKey("claude.model") {
		t.Fatal("TUIOnlyKey")
	}
}

func TestValidateLine(t *testing.T) {
	for _, ok := range []string{"", "Co-Authored-By: Huy <h@x.dev>", "🤖 Generated by bx", strings.Repeat("é", MaxLine)} {
		if err := Validate("claude.commit_text", ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"a\nb", "a\tb", "\x1b[31m", strings.Repeat("x", MaxLine+1), "\xff"} {
		if err := Validate("claude.commit_text", bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if err := Validate("claude.commit", "maybe"); err == nil {
		t.Error("a choice outside the list is accepted")
	}
}

func TestClaudeSettings(t *testing.T) {
	for _, c := range []struct {
		values map[string]string
		want   string
	}{
		{nil, `{}`},
		{map[string]string{"claude.model": "opus", "claude.commit_text": "unused"}, `{}`}, // a flag, and text with no custom
		{map[string]string{"claude.commit": "none"}, `{"attribution":{"commit":""}}`},
		{map[string]string{"claude.commit": "custom"}, `{}`}, // custom with no text sends nothing
		{map[string]string{"claude.commit": "custom", "claude.commit_text": "Co-Authored-By: Huy <h@x.dev>", "claude.pr": "none"},
			`{"attribution":{"commit":"Co-Authored-By: Huy \u003ch@x.dev\u003e","pr":""}}`},
		{map[string]string{"claude.pr": "custom", "claude.pr_text": "Made with bx"}, `{"attribution":{"pr":"Made with bx"}}`},
	} {
		b, _ := json.Marshal(ClaudeSettings(c.values))
		if string(b) != c.want {
			t.Errorf("%v: got %s, want %s", c.values, b, c.want)
		}
	}
}

func TestClaudePrompt(t *testing.T) {
	if got := ClaudePrompt(map[string]string{"claude.commit": "custom", "claude.commit_text": "x"}); got != "" {
		t.Errorf("no none: %q", got)
	}
	commit := ClaudePrompt(map[string]string{"claude.commit": "none"})
	if !strings.Contains(commit, "git commit") || strings.Contains(commit, "pull request") {
		t.Errorf("commit none: %q", commit)
	}
	both := ClaudePrompt(map[string]string{"claude.commit": "none", "claude.pr": "none"})
	if !strings.HasPrefix(both, commit+" ") || !strings.Contains(both, "pull request") {
		t.Errorf("both none: %q", both)
	}
}

func TestVisibleHidesClaudeSettings(t *testing.T) {
	values := map[string]string{"claude.model": "opus", "claude.commit": "custom", "claude.commit_text": "x", "claude.pr": "none"}
	secs, got := Visible(false, values)
	if len(secs[0].Fields) != 3 || len(got) != 1 || got["claude.model"] != "opus" {
		t.Fatalf("web: %+v %v", secs[0].Fields, got)
	}
	for _, k := range []string{"claude.commit", "claude.commit_text", "claude.pr", "claude.pr_text"} {
		if !TUIOnlyKey(k) {
			t.Errorf("%s is not TUI only", k)
		}
	}
}

func TestPRSettings(t *testing.T) {
	for _, key := range []string{"claude.fix_ci", "claude.address"} {
		f, ok := field(key)
		if !ok || f.Kind != Line || !f.TUIOnly {
			t.Errorf("%s = %+v %v, want a TUI-only line", key, f, ok)
		}
	}
	f, ok := field("notifications.prs")
	if !ok || f.Kind != Choice || f.Default != "on" || f.TUIOnly {
		t.Errorf("notifications.prs = %+v %v", f, ok)
	}
	if err := Validate("claude.fix_ci", "Run make test."); err != nil {
		t.Error(err)
	}
}
