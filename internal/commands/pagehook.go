package commands

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dinhhuy258/bx/internal/api"
	"github.com/dinhhuy258/bx/internal/cli"
)

func init() {
	cli.Register(cli.Command{Name: "page-hook", Help: "internal: Claude Code hook → bx serve, when Claude writes a page", Run: runPageHook})
}

// postToolUse is the JSON Claude Code writes to a PostToolUse hook's stdin. Only the fields bx uses.
type postToolUse struct {
	SessionID string `json:"session_id"`
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		FilePath string `json:"file_path"`
	} `json:"tool_input"`
	ToolResponse json.RawMessage `json:"tool_response"`
}

// runPageHook is the PostToolUse hook of Write, Edit and MultiEdit (args: bx serve's base URL).
// When the file is a page in the session's artifacts folder ($BX_ARTIFACTS) it tells bx serve,
// which shows the link in the phone and the TUI. It runs after every edit, so anything else
// exits at once; it never blocks Claude and always exits 0.
func runPageHook(args []string, out, errOut io.Writer) int {
	if len(args) != 1 {
		return 0
	}
	var h postToolUse
	if json.NewDecoder(os.Stdin).Decode(&h) != nil {
		return 0
	}
	if !isPage(os.Getenv("BX_ARTIFACTS"), h.ToolInput.FilePath) {
		return 0
	}
	body, _ := json.Marshal(api.PageHook{SessionID: h.SessionID, Path: h.ToolInput.FilePath, New: h.ToolName == "Write" && !writeUpdated(h.ToolResponse)})
	c := &http.Client{Timeout: 2 * time.Second}
	resp, err := c.Post(strings.TrimRight(args[0], "/")+"/api/hooks/page", "application/json", bytes.NewReader(body))
	if err != nil {
		return 0 // bx serve is down: no link, Claude carries on
	}
	_ = resp.Body.Close()
	return 0
}

// isPage reports whether file is an .html page at the top of the artifacts folder dir.
func isPage(dir, file string) bool {
	if dir == "" || file == "" || !strings.EqualFold(filepath.Ext(file), ".html") {
		return false
	}
	return filepath.Dir(filepath.Clean(file)) == filepath.Clean(dir) && !strings.HasPrefix(filepath.Base(file), ".")
}

// writeUpdated reports whether a Write's tool_response says it replaced a file ("type":
// "update"); one that says nothing counts as new.
func writeUpdated(resp json.RawMessage) bool {
	var r struct {
		Type string `json:"type"`
	}
	return json.Unmarshal(resp, &r) == nil && r.Type == "update"
}
