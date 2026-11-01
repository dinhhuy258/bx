package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/dinhhuy258/bx/internal/app"
	"github.com/dinhhuy258/bx/internal/cli"
)

func init() {
	cli.Register(cli.Command{Name: "guard", Help: "internal: Claude Code hook keeping a workspace story's edits in its repos", Run: runGuard})
}

// guardInput is the part of a PreToolUse hook's input the guard reads.
type guardInput struct {
	Cwd       string `json:"cwd"`
	ToolInput struct {
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
	} `json:"tool_input"`
}

// runGuard is a PreToolUse hook: exit 2 with the reason on stderr refuses the edit, and Claude
// reads the reason. Anything it cannot read is allowed, so a broken guard never blocks Claude.
func runGuard(args []string, out, errOut io.Writer) int {
	if len(args) != 1 {
		_, _ = fmt.Fprintln(errOut, "usage: bx guard <story folder>")
		return 0
	}
	return guard(args[0], os.Stdin, errOut)
}

func guard(dir string, in io.Reader, errOut io.Writer) int {
	var h guardInput
	if err := json.NewDecoder(in).Decode(&h); err != nil {
		return 0
	}
	file := h.ToolInput.FilePath
	if file == "" {
		file = h.ToolInput.NotebookPath
	}
	if file == "" {
		return 0
	}
	if !filepath.IsAbs(file) {
		file = filepath.Join(h.Cwd, file)
	}
	if why := app.Guard(dir, file); why != "" {
		_, _ = fmt.Fprintln(errOut, why)
		return 2
	}
	return 0
}
