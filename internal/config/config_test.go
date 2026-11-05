package config

import (
	"os"
	"path/filepath"
	"reflect"
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
	if c.Keys.First(c.Keys.List, "inbox") != "M-a" || c.Keys.First(c.Keys.List, "projects") != "M-o" {
		t.Fatalf("list keys: %+v", c.Keys.List)
	}
	if got := c.Keys.List["down"]; len(got) != 2 || got[0] != "j" || got[1] != "Down" {
		t.Errorf("down = %v", got)
	}
	if c.Keys.List["fold_all"][0] != "M-z" {
		t.Errorf("fold_all = %v", c.Keys.List["fold_all"])
	}
	if _, ok := c.Keys.List["activity"]; ok || c.Keys.First(c.Keys.List, "prev_project") != "[" || c.Keys.First(c.Keys.List, "next_project") != "]" {
		t.Errorf("project keys: %+v", c.Keys.List)
	}
	if got := strings.Join(c.Keys.ListActions("M-d"), ","); got != "close_shell,close_story,delete_session" {
		t.Errorf("M-d = %s", got)
	}
	if c.Keys.ListAction("M-x") != "close_project" || c.Keys.ListAction("M-s") != "stop_session" || c.Keys.ListAction("M-p") != "settings" || c.Keys.ListAction("x") != "" {
		t.Errorf("list keys: %+v", c.Keys.List)
	}
	for _, k := range []string{"n", "w", "t", "a", "o", "v", "s", "d", "N", "W", "T", "A", "O", "V", "S", "D", "M-h", "M-j", "M-k", "M-l"} { // actions take Alt keys; letters are free, and so is M-hjkl
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

func TestPRFile(t *testing.T) {
	c, _ := Load("/tmp/bxroot")
	if got := c.PRFile("p", "s"); got != "/tmp/bxroot/pr/p/s.json" {
		t.Fatalf("PRFile = %s", got)
	}
}

func TestDefaultPRKey(t *testing.T) {
	c, _ := Load("/tmp/bxroot")
	if got := c.Keys.List["pr"]; len(got) != 1 || got[0] != "M-m" {
		t.Fatalf("pr = %v", got)
	}
	if a := c.Keys.ListAction("M-m"); a != "pr" {
		t.Fatalf("M-m = %q", a)
	}
}

func TestActionForKey(t *testing.T) {
	c, _ := Load(t.TempDir())
	if a := c.Keys.ListAction("Down"); a != "down" {
		t.Errorf("Down -> %q", a)
	}
	if a := c.Keys.ListAction("M-z"); a != "fold_all" {
		t.Errorf("M-z -> %q", a)
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
	toml := "[serve]\nlisten = \"100.100.1.2:7357\"\n\n[client]\nserver = \"http://100.100.1.2:7357\"\nswitch_pane = \"C-g\"\nopen_page = \"\"\nquit = \"C-c C-c\"\n"
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
	if c.Client.Server != "http://100.100.1.2:7357" || c.Client.SwitchPane != "C-g" || c.Client.OpenPage != "" || c.Client.Quit != "C-c C-c" {
		t.Fatalf("client = %+v", c.Client)
	}
	d := Default(root)
	if d.Serve.Listen != "auto" || d.Client.Server != "" || d.Client.SwitchPane != "C-q" || d.Client.Quit != "C-c" || d.Client.OpenPage != "" || d.PageKey() != "M-v" {
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
	if !reflect.DeepEqual(c.Serve.DocPages, []string{"docs/*.md", "docs/*/*.md"}) {
		t.Errorf("doc_pages default = %q", c.Serve.DocPages)
	}
	if c.ArtifactsDir() != filepath.Join(root, "artifacts") || c.PluginDir() != filepath.Join(root, "claude-plugin") {
		t.Errorf("dirs %q %q", c.ArtifactsDir(), c.PluginDir())
	}
	if err := os.WriteFile(filepath.Join(root, "config.toml"), []byte("[serve]\nfiles_listen = \"off\"\ndoc_pages = []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if c, err = Load(root); err != nil || c.Serve.FilesListen != "off" || len(c.Serve.DocPages) != 0 {
		t.Fatalf("override: %q %q %v", c.Serve.FilesListen, c.Serve.DocPages, err)
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
	if a := c.Keys.ListAction("M-g"); a != "review" {
		t.Errorf("M-g = %q", a)
	}
}

func TestDefaultEditWorkspaceKey(t *testing.T) {
	if k := Default(t.TempDir()).Keys.List["edit_workspace"]; len(k) != 1 || k[0] != "M-e" {
		t.Fatalf("edit_workspace = %v", k)
	}
}

func TestRunPaths(t *testing.T) {
	c := &Config{Root: "/r"}
	for got, want := range map[string]string{
		c.RunsDir():                      "/r/runs",
		c.RunFile("unem"):                "/r/runs/unem.toml",
		c.SetupMark("unem", "s1", "web"): "/r/runs-setup/unem/s1/web",
		c.SetupMarks("unem", "s1"):       "/r/runs-setup/unem/s1",
		c.RunCache("unem"):               "/r/cache/unem",
	} {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}

func TestRunHostAndKey(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config.toml"), []byte("[serve]\nrun_host = \"box\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if c.Serve.RunHost != "box" {
		t.Errorf("RunHost = %q", c.Serve.RunHost)
	}
	if c.Keys.First(c.Keys.List, "run") != "M-b" {
		t.Errorf("run = %v", c.Keys.List["run"])
	}
	for _, a := range []string{"run_story", "stop_story", "open_run"} {
		if _, ok := c.Keys.List[a]; ok {
			t.Errorf("%s has a default bind", a)
		}
	}
}
