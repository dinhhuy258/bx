package claude

import (
	"path/filepath"
	"testing"
)

func TestConfigDir(t *testing.T) {
	tmp := t.TempDir()
	for _, tc := range []struct {
		name, env, home, want string
	}{
		{"env", tmp, "/nowhere", tmp},
		{"home", "", tmp, filepath.Join(tmp, ".claude")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CLAUDE_CONFIG_DIR", tc.env)
			t.Setenv("HOME", tc.home)
			if got := ConfigDir(); got != tc.want {
				t.Fatalf("ConfigDir() = %q, want %q", got, tc.want)
			}
		})
	}
}
