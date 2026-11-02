package commands

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dinhhuy258/bx/internal/config"
	"github.com/dinhhuy258/bx/internal/settings"
)

func TestNotifyTargetsReadTheSettings(t *testing.T) {
	cfg := config.Default(t.TempDir())
	if ntfy, open := notifyTargets(cfg); ntfy != "" || open != "" {
		t.Fatalf("unset: %q %q", ntfy, open)
	}
	st, err := settings.Open(cfg.SettingsFile(), func(string, ...any) {})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Set(map[string]string{"notifications.ntfy_url": "https://ntfy.sh/bx", "notifications.open_url": "https://bx.example.ts.net"}); err != nil {
		t.Fatal(err)
	}
	if ntfy, open := notifyTargets(cfg); ntfy != "https://ntfy.sh/bx" || open != "https://bx.example.ts.net" {
		t.Fatalf("set: %q %q", ntfy, open)
	}
}

func TestNotifyTargetsOffSendsNothing(t *testing.T) {
	cfg := config.Default(t.TempDir())
	st, err := settings.Open(cfg.SettingsFile(), func(string, ...any) {})
	if err != nil {
		t.Fatal(err)
	}
	urls := map[string]string{"notifications.ntfy_url": "https://ntfy.sh/bx", "notifications.open_url": "https://bx.example.ts.net"}
	if err := st.Set(urls); err != nil {
		t.Fatal(err)
	}
	if err := st.Set(map[string]string{"notifications.enabled": "off"}); err != nil {
		t.Fatal(err)
	}
	if ntfy, open := notifyTargets(cfg); ntfy != "" || open != "" {
		t.Fatalf("off: %q %q", ntfy, open)
	}
	if err := st.Set(map[string]string{"notifications.enabled": "on"}); err != nil {
		t.Fatal(err)
	}
	if ntfy, open := notifyTargets(cfg); ntfy != "https://ntfy.sh/bx" || open != "https://bx.example.ts.net" {
		t.Fatalf("on: %q %q", ntfy, open)
	}
}

func TestNotifyTargetsLeaveACorruptFile(t *testing.T) {
	cfg := config.Default(t.TempDir())
	path := cfg.SettingsFile()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ntfy, open := notifyTargets(cfg); ntfy != "" || open != "" {
		t.Fatalf("corrupt: %q %q", ntfy, open)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if data, err := os.ReadFile(path); err != nil || string(data) != "{not json" || len(entries) != 1 {
		t.Fatalf("the hook touched the settings: %q %v %v", data, err, entries)
	}
}
