package config

import (
	"os"
	"path/filepath"
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
	if c.Keys.First(c.Keys.List, "waiting") != "a" || c.Keys.First(c.Keys.List, "projects") != "o" {
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
	if c.Keys.First(c.Keys.List, "activity") != "A" || c.Keys.First(c.Keys.List, "prev_project") != "[" || c.Keys.First(c.Keys.List, "next_project") != "]" {
		t.Errorf("project keys: %+v", c.Keys.List)
	}
}

func TestFileOverridesAndUnbind(t *testing.T) {
	root := t.TempDir()
	toml := `
[tmux]
prefix = "C-a"

[tmux.keys]
menu = "m"

[keys.list]
down = ["n"]
nvim = ""
fold_all = "z M"

[list]
auto_expand_waiting = false

[notify]
ntfy_url = "https://ntfy.example/bx-secret"
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
	if _, bound := c.Keys.List["nvim"]; bound {
		t.Error("nvim should be unbound")
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
	if d.Serve.Listen != "auto" || d.Client.Server != "" || d.Client.PaneLeft != "C-h" || d.Client.PaneRight != "C-l" || d.Client.CopyMode != "M-c" {
		t.Fatalf("defaults = %+v %+v", d.Serve, d.Client)
	}
}

func TestLegacyNotify(t *testing.T) {
	root := t.TempDir()
	if got, err := LegacyNotify(root); err != nil || len(got) != 0 {
		t.Fatalf("no file: %v %v", got, err)
	}
	path := filepath.Join(root, "config.toml")
	if err := os.WriteFile(path, []byte("[serve]\nlisten = \"auto\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := LegacyNotify(root); err != nil || len(got) != 0 {
		t.Fatalf("no [notify]: %v %v", got, err)
	}
	toml := "[notify]\nntfy_url = \"https://ntfy.example/x\"\nopen_url = \"https://bx.tail1234.ts.net\"\n"
	if err := os.WriteFile(path, []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := LegacyNotify(root)
	if err != nil || len(got) != 2 || got["ntfy_url"] != "https://ntfy.example/x" || got["open_url"] != "https://bx.tail1234.ts.net" {
		t.Fatalf("notify = %v %v", got, err)
	}
	if _, err := Load(root); err != nil {
		t.Fatalf("an old [notify] must still load: %v", err)
	}
	if got := Default(root).NotifyImportedFile(); got != filepath.Join(root, "notify-imported") {
		t.Error(got)
	}
}

func TestLeftoverYesBindLoads(t *testing.T) {
	root := t.TempDir()
	if _, ok := Default(root).Keys.List["yes"]; ok {
		t.Fatal("yes is no longer an action")
	}
	if err := os.WriteFile(filepath.Join(root, "config.toml"), []byte("[keys.list]\nyes = \"y\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(root); err != nil {
		t.Fatalf("an old yes bind must load: %v", err)
	}
}
