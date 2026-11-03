package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultsWhenFileMissing(t *testing.T) {
	root := t.TempDir()
	c, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if c.Root != root {
		t.Errorf("Root = %q", c.Root)
	}
	if c.Keys.First(c.Keys.List, "inbox") != "A" || c.Keys.First(c.Keys.List, "projects") != "O" {
		t.Fatalf("list keys: %+v", c.Keys.List)
	}
	if got := c.Keys.List["down"]; len(got) != 2 || got[0] != "j" || got[1] != "Down" {
		t.Errorf("down = %v", got)
	}
	if up, down := c.Keys.List["scroll_up"], c.Keys.List["scroll_down"]; len(up) != 1 || up[0] != "PgUp" || len(down) != 1 || down[0] != "PgDn" {
		t.Errorf("scroll = %v %v", up, down)
	}
	if c.Keys.List["fold_all"][0] != "Z" {
		t.Errorf("fold_all = %v", c.Keys.List["fold_all"])
	}
	if _, ok := c.Keys.List["activity"]; ok || c.Keys.First(c.Keys.List, "prev_project") != "[" || c.Keys.First(c.Keys.List, "next_project") != "]" {
		t.Errorf("project keys: %+v", c.Keys.List)
	}
	if got := strings.Join(c.Keys.ListActions("D"), ","); got != "close_shell,close_story,delete_session" {
		t.Errorf("D = %s", got)
	}
	if c.Keys.ListAction("X") != "close_project" || c.Keys.ListAction("S") != "stop_session" || c.Keys.ListAction("P") != "settings" || c.Keys.ListAction("x") != "" {
		t.Errorf("list keys: %+v", c.Keys.List)
	}
	for _, k := range []string{"n", "w", "t", "a", "o", "v", "s", "d"} { // actions take capitals; lowercase is free
		if a := c.Keys.ListAction(k); a != "" {
			t.Errorf("%s is bound to %s", k, a)
		}
	}
}

func TestFileOverridesAndUnbind(t *testing.T) {
	root := t.TempDir()
	toml := `
[keys.list]
down = ["n"]
shell = ""
fold_all = "z M"
`
	if err := os.WriteFile(filepath.Join(root, "config.toml"), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Keys.List["down"]; len(got) != 1 || got[0] != "n" {
		t.Errorf("down = %v", got)
	}
	if _, bound := c.Keys.List["shell"]; bound {
		t.Error("shell should be unbound")
	}
	if c.Keys.List["fold_all"][0] != "z M" {
		t.Errorf("fold_all = %v", c.Keys.List["fold_all"])
	}
}

func TestPaths(t *testing.T) {
	c, _ := Load("/tmp/bxroot")
	if c.ProjectDir("home-network") != "/tmp/bxroot/projects/home-network" {
		t.Error(c.ProjectDir("home-network"))
	}
	if c.WorktreeDir("home-network", "guest-vlan-fix") != "/tmp/bxroot/worktrees/home-network/guest-vlan-fix" {
		t.Error(c.WorktreeDir("home-network", "guest-vlan-fix"))
	}
	if c.StateFile("bunnix") != "/tmp/bxroot/state/bunnix.json" {
		t.Error(c.StateFile("bunnix"))
	}
	if got := c.TmuxSocket(); got != "/tmp/bxroot/run/tmux.sock" {
		t.Errorf("TmuxSocket = %s", got)
	}
	if got := c.TmuxConf(); got != "/tmp/bxroot/tmux.conf" {
		t.Errorf("TmuxConf = %s", got)
	}
	if got := c.UploadsDir(); got != "/tmp/bxroot/uploads" {
		t.Errorf("UploadsDir = %s", got)
	}
}

func TestActionForKey(t *testing.T) {
	c, _ := Load(t.TempDir())
	if a := c.Keys.ListAction("Down"); a != "down" {
		t.Errorf("Down -> %q", a)
	}
	if a := c.Keys.ListAction("Z"); a != "fold_all" {
		t.Errorf("Z -> %q", a)
	}
	if a := c.Keys.ListAction("Q"); a != "" {
		t.Errorf("Q -> %q", a)
	}
}

func TestRootFromEnv(t *testing.T) {
	t.Setenv("BX_ROOT", "/tmp/other")
	if got := DefaultRoot(); got != "/tmp/other" {
		t.Errorf("DefaultRoot = %q", got)
	}
	t.Setenv("BX_ROOT", "")
	home, _ := os.UserHomeDir()
	if got := DefaultRoot(); got != filepath.Join(home, ".bx") {
		t.Errorf("DefaultRoot = %q", got)
	}
}

func TestServeAndClientSections(t *testing.T) {
	root := t.TempDir()
	toml := "[serve]\nlisten = \"100.100.1.2:7357\"\n\n[client]\nserver = \"http://100.100.1.2:7357\"\npane_left = \"C-g\"\ncopy_mode = \"\"\n"
	if err := os.WriteFile(filepath.Join(root, "config.toml"), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if c.Serve.Listen != "100.100.1.2:7357" {
		t.Fatalf("serve.listen = %q", c.Serve.Listen)
	}
	if c.Client.Server != "http://100.100.1.2:7357" || c.Client.PaneLeft != "C-g" || c.Client.PaneRight != "C-l" || c.Client.CopyMode != "" {
		t.Fatalf("client = %+v", c.Client)
	}
	d := Default(root)
	if d.Serve.Listen != "auto" || d.Client.Server != "" || d.Client.PaneLeft != "C-h" || d.Client.PaneRight != "C-l" || d.Client.CopyMode != "M-c" || d.Client.OpenPage != "M-o" {
		t.Fatalf("defaults = %+v %+v", d.Serve, d.Client)
	}
}

func TestArtifactsConfig(t *testing.T) {
	root := t.TempDir()
	c, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if c.Serve.FilesListen != "auto" {
		t.Errorf("files_listen default = %q", c.Serve.FilesListen)
	}
	if c.ArtifactsDir() != filepath.Join(root, "artifacts") || c.PluginDir() != filepath.Join(root, "claude-plugin") {
		t.Errorf("dirs %q %q", c.ArtifactsDir(), c.PluginDir())
	}
	if err := os.WriteFile(filepath.Join(root, "config.toml"), []byte("[serve]\nfiles_listen = \"off\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if c, err = Load(root); err != nil || c.Serve.FilesListen != "off" {
		t.Fatalf("override: %q %v", c.Serve.FilesListen, err)
	}
}

func TestMCPPaths(t *testing.T) {
	c := Default("/r")
	if c.MCPFile() != "/r/mcp.json" || c.SessionMCPDir() != "/r/run/mcp" {
		t.Fatalf("%q %q", c.MCPFile(), c.SessionMCPDir())
	}
}

func TestReviewFileAndKey(t *testing.T) {
	c := Default("/r")
	if got := c.ReviewFile("home-network", "s1"); got != filepath.Join("/r", "review", "home-network", "s1.json") {
		t.Errorf("ReviewFile = %s", got)
	}
	if a := c.Keys.ListAction("G"); a != "review" {
		t.Errorf("G = %q", a)
	}
}
