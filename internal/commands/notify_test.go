package commands

import (
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
