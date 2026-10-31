package commands

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/dinhhuy258/bx/internal/cli"
	"github.com/dinhhuy258/bx/internal/notify"
)

func init() {
	cli.Register(cli.Command{Name: "hooks", Help: "hooks install: add the bx notify hooks to ~/.claude/settings.json", Run: runHooks})
}

func runHooks(args []string, out, errOut io.Writer) int {
	if len(args) != 1 || args[0] != "install" {
		fmt.Fprintln(errOut, "usage: bx hooks install")
		return 2
	}
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintln(errOut, "bx hooks:", err)
		return 1
	}
	home, _ := os.UserHomeDir()
	if err := installHooks(filepath.Join(home, ".claude", "settings.json"), self, out); err != nil {
		fmt.Fprintln(errOut, "bx hooks:", err)
		return 1
	}
	return 0
}

// installHooks merges the Notification and PermissionRequest hooks running `self notify` into
// the Claude settings at path, keeping a backup of the old file. It never touches a project's
// .claude/settings.json.
func installHooks(path, self string, out io.Writer) error {
	cur, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	merged, changed, err := notify.MergeHooks(cur, self)
	if err != nil {
		return err
	}
	if !changed {
		fmt.Fprintln(out, "bx hooks: already installed in", path)
		return nil
	}
	if len(cur) > 0 {
		if err := os.WriteFile(path+".bx-backup", cur, 0o600); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, merged, 0o600); err != nil {
		return err
	}
	fmt.Fprintf(out, "bx hooks: added `%s notify` to %s\n", self, path)
	return nil
}
