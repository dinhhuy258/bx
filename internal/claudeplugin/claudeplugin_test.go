package claudeplugin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteInstallsAndRefreshesThePlugin(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "claude-plugin")
	if err := Write(dir); err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(filepath.Join(dir, ".claude-plugin", "plugin.json"))
	if err != nil || !strings.Contains(string(manifest), `"name": "bx"`) {
		t.Fatalf("manifest %q %v", manifest, err)
	}
	skill := filepath.Join(dir, "skills", "artifact", "SKILL.md")
	b, err := os.ReadFile(skill)
	if err != nil || !strings.Contains(string(b), "name: artifact") || !strings.Contains(string(b), "artifact-design") {
		t.Fatalf("skill %q %v", b, err)
	}
	// an unchanged file is left alone; a stale one is replaced
	old := time.Now().Add(-time.Hour)
	_ = os.Chtimes(skill, old, old)
	if err := Write(dir); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(skill); !fi.ModTime().Equal(old) {
		t.Fatal("unchanged skill rewritten")
	}
	_ = os.WriteFile(skill, []byte("stale"), 0o644)
	if err := Write(dir); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(skill); string(b) == "stale" {
		t.Fatal("stale skill kept")
	}
}
