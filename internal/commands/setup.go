package commands

import (
	"log"
	"os"

	"github.com/dinhhuy258/bx/internal/app"
	"github.com/dinhhuy258/bx/internal/config"
	"github.com/dinhhuy258/bx/internal/settings"
)

// setup loads the config and builds the App; used by bx serve.
func setup() (*config.Config, *app.App, *log.Logger, error) {
	cfg, err := config.Load(config.DefaultRoot())
	if err != nil {
		return nil, nil, nil, err
	}
	if err := cfg.EnsureDirs(); err != nil {
		return nil, nil, nil, err
	}
	f, err := os.OpenFile(cfg.LogFile(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, nil, nil, err
	}
	logger := log.New(f, "", log.LstdFlags)
	a := app.New(cfg)
	if a.Settings, err = settings.Open(cfg.SettingsFile(), logger.Printf); err != nil {
		return nil, nil, nil, err
	}
	return cfg, a, logger, nil
}
