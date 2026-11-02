package serve

import (
	"os"
	"strings"

	"github.com/dinhhuy258/bx/internal/config"
	"github.com/dinhhuy258/bx/internal/settings"
)

// legacyNotify are the [notify] keys of config.toml and the settings they moved to, in import order.
var legacyNotify = []struct{ toml, key string }{
	{"ntfy_url", "notifications.ntfy_url"},
	{"open_url", "notifications.open_url"},
}

// ImportNotify copies notify.ntfy_url and notify.open_url from config.toml to the settings,
// once: a value is copied only when the setting is empty, and the marker file
// (cfg.NotifyImportedFile) stops later starts from copying again, so a URL cleared in the
// settings stays cleared. While the keys stay in config.toml every start logs that they are no
// longer used. No value is ever logged: the ntfy URL holds a secret topic. Errors are logged,
// never fatal.
func ImportNotify(cfg *config.Config, st *settings.Store, logf func(format string, args ...any)) {
	legacy, err := config.LegacyNotify(cfg.Root)
	if err != nil {
		logf("config.toml: notify: %v", err)
		return
	}
	if len(legacy) == 0 {
		return
	}
	marker := cfg.NotifyImportedFile()
	if _, err := os.Stat(marker); os.IsNotExist(err) {
		failed := false
		for _, l := range legacyNotify {
			v := strings.TrimSpace(legacy[l.toml])
			if v == "" || st.Get(l.key) != "" {
				continue
			}
			if settings.Validate(l.key, v) != nil {
				logf("config.toml: notify.%s is not an http(s) URL · not moved", l.toml)
				failed = true
				continue
			}
			if err := st.Set(map[string]string{l.key: v}); err != nil {
				logf("config.toml: notify.%s: could not move it to settings: %v", l.toml, err)
				failed = true
				continue
			}
			logf("moved notify.%s to settings", l.toml)
		}
		if !failed {
			if err := os.WriteFile(marker, []byte("the [notify] keys of config.toml were moved to settings.json\n"), 0o644); err != nil {
				logf("config.toml: notify: %v", err)
			}
		}
	}
	for _, l := range legacyNotify {
		if _, ok := legacy[l.toml]; ok {
			logf("config.toml: notify.%s is no longer used · set it in settings", l.toml)
		}
	}
}
