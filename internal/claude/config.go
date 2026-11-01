package claude

import (
	"os"
	"path/filepath"
)

// ConfigDir is Claude Code's config folder: $CLAUDE_CONFIG_DIR, else ~/.claude ("" when neither is known).
func ConfigDir() string {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".claude")
}
