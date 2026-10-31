package notify

import (
	"encoding/json"
	"testing"
)

func TestMergeHooksKeepsExistingEntries(t *testing.T) {
	existing := []byte(`{
  "model": "opus",
  "hooks": {
    "Notification": [{"hooks": [{"type": "command", "command": "~/.claude/hooks/claude-notify.sh"}]}],
    "PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "rtk hook claude"}]}]
  }
}`)
	out, changed, err := MergeHooks(existing, "/usr/local/bin/bx")
	if err != nil || !changed {
		t.Fatal(err, changed)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	if m["model"] != "opus" {
		t.Fatal("lost unrelated key")
	}
	hooks := m["hooks"].(map[string]any)
	if len(hooks["Notification"].([]any)) != 2 {
		t.Fatalf("Notification = %v", hooks["Notification"])
	}
	if len(hooks["PermissionRequest"].([]any)) != 1 {
		t.Fatalf("PermissionRequest = %v", hooks["PermissionRequest"])
	}
	if len(hooks["PreToolUse"].([]any)) != 1 {
		t.Fatal("touched PreToolUse")
	}
	// idempotent
	out2, changed2, err := MergeHooks(out, "/usr/local/bin/bx")
	if err != nil || changed2 {
		t.Fatal("second merge should be a no-op", err, changed2)
	}
	if string(out2) != string(out) {
		t.Fatal("second merge changed bytes")
	}
}

func TestMergeHooksFromEmptyFile(t *testing.T) {
	out, changed, err := MergeHooks(nil, "/b/bx")
	if err != nil || !changed {
		t.Fatal(err, changed)
	}
	var m map[string]any
	_ = json.Unmarshal(out, &m)
	if _, ok := m["hooks"].(map[string]any)["Notification"]; !ok {
		t.Fatal(string(out))
	}
}
