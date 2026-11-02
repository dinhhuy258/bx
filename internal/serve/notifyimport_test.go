package serve

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dinhhuy258/bx/internal/config"
	"github.com/dinhhuy258/bx/internal/settings"
)

// importSetup writes config.toml (if toml is not "") under a fresh root and opens its settings.
func importSetup(t *testing.T, toml string) (*config.Config, *settings.Store) {
	t.Helper()
	cfg := config.Default(t.TempDir())
	if toml != "" {
		if err := os.WriteFile(filepath.Join(cfg.Root, "config.toml"), []byte(toml), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	st, err := settings.Open(cfg.SettingsFile(), func(string, ...any) {})
	if err != nil {
		t.Fatal(err)
	}
	return cfg, st
}

func runImport(cfg *config.Config, st *settings.Store) []string {
	var logged []string
	ImportNotify(cfg, st, func(f string, a ...any) { logged = append(logged, fmt.Sprintf(f, a...)) })
	return logged
}

const legacyTOML = "[notify]\nntfy_url = \"https://ntfy.sh/bx-secret\"\nopen_url = \"https://bx.example.ts.net\"\n"

func TestImportNotifyMovesOnce(t *testing.T) {
	cfg, st := importSetup(t, legacyTOML)
	logged := runImport(cfg, st)
	for _, want := range []string{
		"moved notify.ntfy_url to settings", "moved notify.open_url to settings",
		"config.toml: notify.ntfy_url is no longer used · set it in settings",
		"config.toml: notify.open_url is no longer used · set it in settings",
	} {
		if !slices.Contains(logged, want) {
			t.Errorf("missing log %q in %q", want, logged)
		}
	}
	if st.Get("notifications.ntfy_url") != "https://ntfy.sh/bx-secret" || st.Get("notifications.open_url") != "https://bx.example.ts.net" {
		t.Fatalf("values = %v", st.Values())
	}
	if _, err := os.Stat(cfg.NotifyImportedFile()); err != nil {
		t.Fatal(err)
	}
	// cleared in the settings: the next start must not bring it back, but still warns
	if err := st.Set(map[string]string{"notifications.ntfy_url": ""}); err != nil {
		t.Fatal(err)
	}
	logged = runImport(cfg, st)
	if st.Get("notifications.ntfy_url") != "" {
		t.Fatal("imported again after the user cleared it")
	}
	if len(logged) != 2 || !strings.Contains(logged[0], "is no longer used") {
		t.Fatalf("second start logged %q", logged)
	}
	for _, l := range logged {
		if strings.Contains(l, "bx-secret") {
			t.Fatalf("the secret was logged: %q", l)
		}
	}
}

func TestImportNotifyKeepsTheSetting(t *testing.T) {
	cfg, st := importSetup(t, legacyTOML)
	if err := st.Set(map[string]string{"notifications.ntfy_url": "https://ntfy.sh/mine"}); err != nil {
		t.Fatal(err)
	}
	logged := runImport(cfg, st)
	if st.Get("notifications.ntfy_url") != "https://ntfy.sh/mine" || st.Get("notifications.open_url") != "https://bx.example.ts.net" {
		t.Fatalf("values = %v", st.Values())
	}
	if slices.Contains(logged, "moved notify.ntfy_url to settings") {
		t.Fatalf("logged %q", logged)
	}
}

func TestImportNotifyBadURL(t *testing.T) {
	cfg, st := importSetup(t, "[notify]\nopen_url = \"bx.example/secret-path\"\n")
	logged := runImport(cfg, st)
	if st.Get("notifications.open_url") != "" {
		t.Fatal("a bad URL was moved")
	}
	if !slices.Contains(logged, "config.toml: notify.open_url is not an http(s) URL · not moved") {
		t.Fatalf("logged %q", logged)
	}
	for _, l := range logged {
		if strings.Contains(l, "secret-path") {
			t.Fatalf("the value was logged: %q", l)
		}
	}
	if _, err := os.Stat(cfg.NotifyImportedFile()); !os.IsNotExist(err) {
		t.Fatal("marker written although a value was not moved")
	}
}

func TestImportNotifyNoConfig(t *testing.T) {
	cfg, st := importSetup(t, "")
	if logged := runImport(cfg, st); len(logged) != 0 || len(st.Values()) != 0 {
		t.Fatalf("logged %q, values %v", logged, st.Values())
	}
}
