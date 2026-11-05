// Package config loads ~/.bx/config.toml and provides the paths under the bx root.
// Missing keys fall back to the defaults in Default. An empty string unbinds a key.
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
	Serve  Serve  `toml:"serve"`
	Client Client `toml:"client"`
}

// Serve holds `bx serve` settings. Listen is "auto" (the Tailscale address, port 7357)
// or an explicit host:port; only Tailscale (100.64.0.0/10) and loopback addresses are accepted.
// FilesListen is the artifact file server: "auto" (Listen's host, port 7358), "off", or an
// explicit host:port under the same rule.
type Serve struct {
	Listen      string `toml:"listen"`
	FilesListen string `toml:"files_listen"`
}

// Client holds `bx` client settings: the server URL and the keys that work from both panes.
// SwitchPane moves between the list and the Claude pane, both ways. Moving to the tmux panes
// beside bx is left to tmux.
type Client struct {
	Server     string `toml:"server"`
	SwitchPane string `toml:"switch_pane"`
	OpenPage   string `toml:"open_page"` // opens the page Claude just made; "" = the artifacts key does
}

// PageKey is the key that opens the page Claude just made: open_page when set, else the
// artifacts key, which opens the list of pages when there is no new one.
func (c *Config) PageKey() string {
	if c.Client.OpenPage != "" {
		return c.Client.OpenPage
	}
	return c.Keys.First(c.Keys.List, "artifacts")
}

// Keys holds the list binds. A bind is a list of key names; "z M" is a two-key sequence.
type Keys struct {
	List map[string][]string `toml:"list"`
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
				// Ctrl and Alt keys work from the Claude pane too, taking them from Claude (see
				// Keys.FromClaude); plain keys only from the list.
				"down": {"j", "Down"}, "up": {"k", "Up"}, "open": {"Enter"}, "fold_all": {"M-z"}, "focus": {"M-f"},
				"new_session": {"M-n"}, "new_story": {"M-w"}, "edit_workspace": {"M-e"},
				"shell": {"M-t"}, "keymap": {"?"},
				"rename": {"M-r"}, "close_story": {"M-d"}, "add_repo": {"+"},
				"inbox": {"M-a"}, "projects": {"M-o"},
				"prev_project": {"["}, "next_project": {"]"}, "close_project": {"M-x"},
				"project_1": {"1"}, "project_2": {"2"}, "project_3": {"3"}, "project_4": {"4"}, "project_5": {"5"},
				"project_6": {"6"}, "project_7": {"7"}, "project_8": {"8"}, "project_9": {"9"},
				"settings": {"M-p"}, "stop_session": {"M-s"}, "delete_session": {"M-d"}, "close_shell": {"M-d"}, "cleanup": {"M-c"}, "artifacts": {"M-v"},
				"review": {"M-g"}, "sync": {"M-u"},
			},
		},
		Serve:  Serve{Listen: "auto", FilesListen: "auto"},
		Client: Client{SwitchPane: "C-q"},
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
	Serve struct {
		Listen      *string `toml:"listen"`
		FilesListen *string `toml:"files_listen"`
	} `toml:"serve"`
	Client struct {
		Server     *string `toml:"server"`
		SwitchPane *string `toml:"switch_pane"`
		OpenPage   *string `toml:"open_page"`
	} `toml:"client"`
}

func (r rawConfig) applyTo(c *Config) {
	applyBinds(c.Keys.List, r.Keys.List)
	if r.Serve.Listen != nil {
		c.Serve.Listen = *r.Serve.Listen
	}
	if r.Serve.FilesListen != nil {
		c.Serve.FilesListen = *r.Serve.FilesListen
	}
	if r.Client.Server != nil {
		c.Client.Server = *r.Client.Server
	}
	if r.Client.SwitchPane != nil {
		c.Client.SwitchPane = *r.Client.SwitchPane
	}
	if r.Client.OpenPage != nil {
		c.Client.OpenPage = *r.Client.OpenPage
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

// ReviewFile is a story's review comments: <root>/review/<project>/<story>.json.
func (c *Config) ReviewFile(project, story string) string {
	return filepath.Join(c.Root, "review", project, story+".json")
}

// ExplainFile is a story's review explanations: <root>/review/<project>/<story>.explain.json.
func (c *Config) ExplainFile(project, story string) string {
	return filepath.Join(c.Root, "review", project, story+".explain.json")
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

// ArtifactsDir is <root>/artifacts: the HTML pages Claude makes for each session, outside every
// repository, served read-only by bx serve's file listener.
func (c *Config) ArtifactsDir() string { return filepath.Join(c.Root, "artifacts") }

// PluginDir is <root>/claude-plugin: the Claude Code plugin bx gives every session it starts.
func (c *Config) PluginDir() string { return filepath.Join(c.Root, "claude-plugin") }

// AccountsFile is <root>/git-accounts.json: the GitHub accounts of the Git settings.
func (c *Config) AccountsFile() string { return filepath.Join(c.Root, "git-accounts.json") }

// MCPFile is <root>/mcp.json: the MCP servers every new session gets.
func (c *Config) MCPFile() string { return filepath.Join(c.Root, "mcp.json") }

// SessionMCPDir is <root>/run/mcp: <project>/<num>/mcp.json is the --mcp-config bx wrote for
// that session.
func (c *Config) SessionMCPDir() string { return filepath.Join(c.Root, "run", "mcp") }

// EnsureDirs creates every folder under the root.
func (c *Config) EnsureDirs() error {
	for _, d := range []string{"projects", "worktrees", "state", "logs", "run"} {
		if err := os.MkdirAll(filepath.Join(c.Root, d), 0o755); err != nil {
			return err
		}
	}
	return nil
}
