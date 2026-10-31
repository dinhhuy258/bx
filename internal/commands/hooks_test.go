package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallHooksMergesAndBacksUp(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude", "settings.json")
	var out bytes.Buffer
	if err := installHooks(path, "/usr/local/bin/bx", &out); err != nil { // no file yet
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "/usr/local/bin/bx notify") {
		t.Fatalf("%s", b)
	}
	os.WriteFile(path, []byte(`{"theme":"dark"}`), 0o600)
	if err := installHooks(path, "/usr/local/bin/bx", &out); err != nil {
		t.Fatal(err)
	}
	if bak, _ := os.ReadFile(path + ".bx-backup"); string(bak) != `{"theme":"dark"}` {
		t.Fatalf("backup = %s", bak)
	}
	b, _ = os.ReadFile(path)
	if !strings.Contains(string(b), `"theme"`) || !strings.Contains(string(b), "bx notify") {
		t.Fatalf("%s", b)
	}
	out.Reset()
	if err := installHooks(path, "/usr/local/bin/bx", &out); err != nil || !strings.Contains(out.String(), "already") {
		t.Fatalf("second run must be a no-op: %v %q", err, out.String())
	}
}
