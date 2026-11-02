// Package config loads ~/.bx/config.toml and provides the paths under the bx root.
// Missing keys fall back to the defaults from docs/keymap.md. An empty string unbinds a key.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Config is the parsed config plus the resolved root directory.
type Config struct {
	Root   string `toml:"-"`
	Keys   Keys   `toml:"keys"`
	List   List   `toml:"list"`
	Notify Notify `toml:"notify"`
	Serve  Serve  `toml:"serve"`
	Client Client `toml:"client"`
}

// Serve holds `bx serve` settings. Listen is "auto" (the Tailscale address, port 7357)
// or an explicit host:port; only Tailscale (100.64.0.0/10) and loopback addresses are accepted.
type Serve struct {
	Listen string `toml:"listen"`
}

// Client holds `bx` client settings: the server URL and the keys that work from both panes.
// PaneLeft and PaneRight move between the list and the Claude pane; past either edge they move
// to the tmux pane on that side, like vim-tmux-navigator.
type Client struct {
	Server    string `toml:"server"`
	PaneLeft  string `toml:"pane_left"`
	PaneRight string `toml:"pane_right"`
	CopyMode  string `toml:"copy_mode"` // tmux copy-mode with vi keys; a yank goes to the clipboard
}

// Keys holds the list binds. A bind is a list of key names; "z M" is a two-key sequence.
type Keys struct {
	List map[string][]string `toml:"list"`
}

// List holds list pane options.
type List struct {
	AutoExpandWaiting bool `toml:"auto_expand_waiting"`
}

// Notify holds push settings. Empty NtfyURL means off.
type Notify struct {
	NtfyURL string `toml:"ntfy_url"`
	// OpenURL is the base URL of the web client for a notification's tap, e.g. http://100.x.y.z:7357
	// or an https ts.net name. Empty: the serve.listen address.
	OpenURL string `toml:"open_url"`
}

// DefaultRoot is $BX_ROOT or ~/.bx.
func DefaultRoot() string {
	if r := os.Getenv("BX_ROOT"); r != "" {
		return r
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".bx"
	}
	return filepath.Join(home, ".bx")
}

// Default returns the built-in config for root.
func Default(root string) *Config {
	return &Config{
		Root: root,
		Keys: Keys{
			List: map[string][]string{
				"down": {"j", "Down"}, "up": {"k", "Up"}, "open": {"Enter"}, "fold_all": {"Z"}, "focus": {"F"},
				"yes": {"y"}, "new_chat": {"c"}, "new_session": {"n"}, "new_story": {"w"},
				"lazygit": {"d"}, "nvim": {"e"}, "shell": {"t"}, "prs": {"p"}, "keymap": {"?"},
				"rename": {"R"}, "resume": {"r"}, "close_story": {"D"}, "add_repo": {"+"},
				"waiting": {"a"}, "projects": {"o"}, "scroll_up": {"PgUp"}, "scroll_down": {"PgDn"},
				"activity": {"A"}, "prev_project": {"["}, "next_project": {"]"}, "close_project": {"x"},
				"settings": {"S"}, "stop": {"s"}, "delete": {"X"}, "cleanup": {"C"},
			},
		},
		List:   List{AutoExpandWaiting: true},
		Notify: Notify{},
		Serve:  Serve{Listen: "auto"},
		Client: Client{PaneLeft: "C-h", PaneRight: "C-l", CopyMode: "M-c"},
	}
}

// Load reads <root>/config.toml over the defaults. A missing file is not an error.
func Load(root string) (*Config, error) {
	c := Default(root)
	path := filepath.Join(root, "config.toml")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var raw rawConfig
	if _, err := toml.Decode(string(data), &raw); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	raw.applyTo(c)
	return c, nil
}

// rawConfig mirrors the file so single strings and arrays are both accepted for binds.
type rawConfig struct {
	Keys struct {
		List map[string]bind `toml:"list"`
	} `toml:"keys"`
	List struct {
		AutoExpandWaiting *bool `toml:"auto_expand_waiting"`
	} `toml:"list"`
	Notify struct {
		NtfyURL *string `toml:"ntfy_url"`
		OpenURL *string `toml:"open_url"`
	} `toml:"notify"`
	Serve struct {
		Listen *string `toml:"listen"`
	} `toml:"serve"`
	Client struct {
		Server    *string `toml:"server"`
		PaneLeft  *string `toml:"pane_left"`
		PaneRight *string `toml:"pane_right"`
		CopyMode  *string `toml:"copy_mode"`
	} `toml:"client"`
}

func (r rawConfig) applyTo(c *Config) {
	applyBinds(c.Keys.List, r.Keys.List)
	if r.List.AutoExpandWaiting != nil {
		c.List.AutoExpandWaiting = *r.List.AutoExpandWaiting
	}
	if r.Notify.NtfyURL != nil {
		c.Notify.NtfyURL = *r.Notify.NtfyURL
	}
	if r.Notify.OpenURL != nil {
		c.Notify.OpenURL = *r.Notify.OpenURL
	}
	if r.Serve.Listen != nil {
		c.Serve.Listen = *r.Serve.Listen
	}
	if r.Client.Server != nil {
		c.Client.Server = *r.Client.Server
	}
	if r.Client.PaneLeft != nil {
		c.Client.PaneLeft = *r.Client.PaneLeft
	}
	if r.Client.PaneRight != nil {
		c.Client.PaneRight = *r.Client.PaneRight
	}
	if r.Client.CopyMode != nil {
		c.Client.CopyMode = *r.Client.CopyMode
	}
}

func applyBinds(dst map[string][]string, src map[string]bind) {
	for action, b := range src {
		if len(b) == 0 {
			delete(dst, action)
			continue
		}
		dst[action] = []string(b)
	}
}

// ProjectDir is <root>/projects/<project>.
func (c *Config) ProjectDir(project string) string {
	return filepath.Join(c.Root, "projects", project)
}

// WorktreeDir is <root>/worktrees/<project>/<story>.
func (c *Config) WorktreeDir(project, story string) string {
	return filepath.Join(c.Root, "worktrees", project, story)
}

// StateFile is <root>/state/<project>.json.
func (c *Config) StateFile(project string) string {
	return filepath.Join(c.Root, "state", project+".json")
}

// SettingsFile is <root>/settings.json: what the settings screen saves. It is outside state/,
// where every .json file is a project.
func (c *Config) SettingsFile() string { return filepath.Join(c.Root, "settings.json") }

// TmuxSocket is the private tmux server bx runs every Claude on: <root>/run/tmux.sock.
func (c *Config) TmuxSocket() string { return filepath.Join(c.Root, "run", "tmux.sock") }

// TmuxConf is the config file of that server: <root>/tmux.conf. bx writes it once and never overwrites it.
func (c *Config) TmuxConf() string { return filepath.Join(c.Root, "tmux.conf") }

// LogFile is <root>/logs/bx.log.
func (c *Config) LogFile() string { return filepath.Join(c.Root, "logs", "bx.log") }

// UploadsDir is <root>/uploads: files sent from the phone, outside every repository.
func (c *Config) UploadsDir() string { return filepath.Join(c.Root, "uploads") }

// EnsureDirs creates every folder under the root.
func (c *Config) EnsureDirs() error {
	for _, d := range []string{"projects", "worktrees", "state", "logs", "run"} {
		if err := os.MkdirAll(filepath.Join(c.Root, d), 0o755); err != nil {
			return err
		}
	}
	return nil
}
