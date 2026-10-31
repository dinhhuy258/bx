// Package notify turns Claude Code hook events into push notifications.
// The message carries the project, the session number and title, and the event kind.
// It never carries Claude's prompt text or the tool input.
package notify

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Hook is the JSON Claude Code writes to a hook's stdin. Only the fields bx uses.
type Hook struct {
	SessionID string `json:"session_id"`
	Event     string `json:"hook_event_name"`
	Type      string `json:"notification_type"` // Notification only
	Message   string `json:"message"`           // Notification only; never forwarded
	ToolName  string `json:"tool_name"`         // PermissionRequest only
	Cwd       string `json:"cwd"`
}

func ParseHook(r io.Reader) (Hook, error) {
	var h Hook
	err := json.NewDecoder(r).Decode(&h)
	return h, err
}

// Message builds the ntfy title and body. Body shows the kind of wait, not the question.
func Message(h Hook, project string, num int, title string) (string, string) {
	kind := "needs you"
	switch {
	case h.Event == "PermissionRequest", h.Type == "permission_prompt":
		kind = "permission"
	case h.Type == "idle_prompt":
		kind = "idle"
	case h.Type == "elicitation_dialog":
		kind = "question"
	case h.Event == "Stop":
		kind = "finished"
	}
	return fmt.Sprintf("%s [%d] needs you", project, num), fmt.Sprintf("%s · %s", title, kind)
}

// Post sends one ntfy message. An empty URL means notifications are off.
func Post(url, title, body string) error {
	if strings.TrimSpace(url) == "" {
		return nil
	}
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Title", title)
	req.Header.Set("Tags", "bell")
	c := &http.Client{Timeout: 5 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("ntfy: %s", resp.Status)
	}
	return nil
}
