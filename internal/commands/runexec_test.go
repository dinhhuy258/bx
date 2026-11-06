package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runExecEnv(t *testing.T, cfg string) (root, story string, ran *[]string, execd *[]string) {
	t.Helper()
	root = t.TempDir()
	t.Setenv("BX_ROOT", root)
	t.Setenv("BX_RUN_HOST", "dev")
	t.Setenv("SHELL", "/bin/zsh")
	os.MkdirAll(filepath.Join(root, "runs"), 0o755)
	os.WriteFile(filepath.Join(root, "runs", "web.toml"), []byte(cfg), 0o644)
	story = filepath.Join(root, "worktrees", "web", "s1")
	os.MkdirAll(filepath.Join(story, "fe"), 0o755)
	var r, e []string
	ran, execd = &r, &e
	oldRun, oldExec := runStep, execStep
	t.Cleanup(func() { runStep, execStep = oldRun, oldExec })
	runStep = func(s stepT) error { r = append(r, strings.Join(s.Argv, " ")+" @"+s.Dir); return nil }
	execStep = func(s stepT) error { e = append(e, strings.Join(s.Argv, " ")); return nil }
	return
}

const webCfg = "[[run]]\nname = \"web\"\ncmd = \"npm run dev\"\ndir = \"fe\"\nsetup = \"npm ci\"\nstop = \"pkill vite\"\n"

func TestRunExecSetupOnce(t *testing.T) {
	root, story, ran, execd := runExecEnv(t, webCfg)
	var out, errOut bytes.Buffer
	if code := runRunExec([]string{"web", "s1", "web"}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if len(*ran) != 1 || (*ran)[0] != "/bin/zsh -lc npm ci @"+filepath.Join(story, "fe") {
		t.Errorf("setup: %v", *ran)
	}
	if len(*execd) != 1 || (*execd)[0] != "/bin/zsh -lc npm run dev" {
		t.Errorf("exec: %v", *execd)
	}
	if !strings.Contains(out.String(), "\x1b[2m$ npm ci\x1b[0m") {
		t.Errorf("no dimmed command: %q", out.String())
	}
	if _, err := os.Stat(filepath.Join(root, "runs-setup", "web", "s1", "web")); err != nil {
		t.Errorf("no mark: %v", err)
	}
	*ran = nil
	runRunExec([]string{"web", "s1", "web"}, &out, &errOut)
	if len(*ran) != 0 {
		t.Errorf("setup ran twice: %v", *ran)
	}
	runRunExec([]string{"-setup", "web", "s1", "web"}, &out, &errOut)
	if len(*ran) != 1 {
		t.Errorf("-setup did not run it: %v", *ran)
	}
}

func TestRunExecCreates(t *testing.T) {
	_, story, ran, _ := runExecEnv(t, webCfg+"creates = \"node_modules\"\n")
	var out, errOut bytes.Buffer
	os.MkdirAll(filepath.Join(story, "fe", "node_modules"), 0o755)
	runRunExec([]string{"web", "s1", "web"}, &out, &errOut)
	if len(*ran) != 0 {
		t.Errorf("setup ran with creates present: %v", *ran)
	}
}

func TestRunExecStopAndErrors(t *testing.T) {
	_, _, _, execd := runExecEnv(t, webCfg)
	var out, errOut bytes.Buffer
	if code := runRunExec([]string{"-stop", "web", "s1", "web"}, &out, &errOut); code != 0 || (*execd)[0] != "/bin/zsh -lc pkill vite" {
		t.Errorf("stop: %d %v", code, *execd)
	}
	if code := runRunExec([]string{"web", "s1", "nope"}, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), `no run "nope"`) {
		t.Errorf("missing run: %d %q", code, errOut.String())
	}
}

// A host run whose setup runs in an image mounts the cache home too: run-exec makes it, as
// the user, before docker would make it as root.
func TestRunExecSetupImageMakesHome(t *testing.T) {
	root, _, ran, _ := runExecEnv(t, "[[run]]\nname = \"api\"\ncmd = \"sail up\"\nsetup = { cmd = \"composer install\", image = \"composer:2\" }\n")
	var out, errOut bytes.Buffer
	if code := runRunExec([]string{"web", "s1", "api"}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if len(*ran) != 1 || !strings.Contains((*ran)[0], "composer:2") {
		t.Errorf("setup: %v", *ran)
	}
	if fi, err := os.Stat(filepath.Join(root, "cache", "web", "home")); err != nil || !fi.IsDir() {
		t.Errorf("no home: %v", err)
	}
}

func TestRunExecUnwritableHome(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root writes anywhere")
	}
	root, _, _, execd := runExecEnv(t, "[[run]]\nname = \"web\"\nimage = \"node:20\"\ncmd = \"yarn start\"\n")
	home := filepath.Join(root, "cache", "web", "home")
	os.MkdirAll(home, 0o755)
	os.Chmod(home, 0o555) // as docker leaves it: someone else's
	t.Cleanup(func() { os.Chmod(home, 0o755) })
	var out, errOut bytes.Buffer
	if code := runRunExec([]string{"web", "s1", "web"}, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "not writable") || !strings.Contains(errOut.String(), "chown") {
		t.Errorf("exit %d: %q", code, errOut.String())
	}
	if len(*execd) != 0 {
		t.Errorf("ran anyway: %v", *execd)
	}
}
