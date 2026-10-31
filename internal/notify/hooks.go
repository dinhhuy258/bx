package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Entries returns the two hook entries bx adds to ~/.claude/settings.json.
func Entries(bxPath string) map[string]any {
	entry := func() any {
		return map[string]any{
			"hooks": []any{map[string]any{"type": "command", "command": bxPath + " notify", "timeout": 10}},
		}
	}
	return map[string]any{"Notification": entry(), "PermissionRequest": entry()}
}

// MergeHooks adds the bx entries to the settings JSON without touching anything else.
// changed is false when both entries were already present.
func MergeHooks(settings []byte, bxPath string) ([]byte, bool, error) {
	m := map[string]any{}
	if len(bytes.TrimSpace(settings)) > 0 {
		if err := json.Unmarshal(settings, &m); err != nil {
			return nil, false, fmt.Errorf("settings.json: %w", err)
		}
	}
	hooks, _ := m["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	want := bxPath + " notify"
	changed := false
	for event, entry := range Entries(bxPath) {
		list, _ := hooks[event].([]any)
		if hasCommand(list, want) {
			continue
		}
		hooks[event] = append(list, entry)
		changed = true
	}
	if !changed {
		return settings, false, nil
	}
	m["hooks"] = hooks
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		return nil, false, err
	}
	return buf.Bytes(), true, nil
}

func hasCommand(list []any, cmd string) bool {
	for _, e := range list {
		em, _ := e.(map[string]any)
		hs, _ := em["hooks"].([]any)
		for _, h := range hs {
			hm, _ := h.(map[string]any)
			if hm["command"] == cmd {
				return true
			}
		}
	}
	return false
}
