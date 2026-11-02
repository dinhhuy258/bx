// Package notify turns Claude Code hook events into push notifications.
// The message carries the project, the session number and title, and the event kind.
// It never carries Claude's prompt text or the tool input.
// The Click link (tap to open the session in the web client) holds only the project name and
// the session number.
package notify

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
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

// SessionURL is the web client's page for one session: <base>/#/p/<project>/s/<num>.
func SessionURL(base, project string, num int) string {
	return strings.TrimRight(base, "/") + "/#/p/" + url.PathEscape(project) + "/s/" + strconv.Itoa(num)
}

// ClickBase picks the web client's base URL for a notification's tap: openURL if set, else
// http://<the address bx serve listens on>. A bad openURL is an error and gives no link; a listen
// address that doesn't resolve gives no link and no error (serve may not run on this machine).
func ClickBase(openURL, listen string, resolve func(string) (string, error)) (string, error) {
	if o := strings.TrimSpace(openURL); o != "" {
		u, err := url.Parse(o)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return "", errors.New("notifications.open_url: want an http:// or https:// URL")
		}
		return strings.TrimRight(o, "/"), nil
	}
	addr, err := resolve(listen)
	if err != nil {
		return "", nil
	}
	return "http://" + addr, nil
}

// Post sends one ntfy message. An empty URL means notifications are off.
// A non-empty click is sent as ntfy's Click header: the page a tap opens.
func Post(url, title, body, click string) error {
	if strings.TrimSpace(url) == "" {
		return nil
	}
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Title", title)
	req.Header.Set("Tags", "bell")
	if click != "" {
		req.Header.Set("Click", click)
	}
	c := &http.Client{Timeout: 5 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("ntfy: %s", resp.Status)
	}
	return nil
}
