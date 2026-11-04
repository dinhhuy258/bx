package commands

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
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
	ToolUseID string `json:"tool_use_id"`
	ToolInput struct {
		FilePath string `json:"file_path"`
	} `json:"tool_input"`
	ToolResponse json.RawMessage `json:"tool_response"`
}

// runPageHook is the PostToolUse hook of Write, Edit, MultiEdit and Bash (args: bx serve's base
// URL). When the file is a page in the session's artifacts folder ($BX_ARTIFACTS) it tells bx
// serve, which shows the link in the phone and the TUI. A Bash command names no file: the hook
// compares the folder with the snapshot that `bx page-hook --before` took (the PreToolUse hook
// of Bash) and tells bx serve of each page the command wrote. It runs after every edit and
// command, so anything else exits at once; it never blocks Claude and always exits 0.
func runPageHook(args []string, out, errOut io.Writer) int {
	if len(args) != 1 {
		return 0
	}
	var h postToolUse
	if json.NewDecoder(os.Stdin).Decode(&h) != nil {
		return 0
	}
	dir := os.Getenv("BX_ARTIFACTS")
	if args[0] == "--before" {
		if h.ToolName == "Bash" {
			saveSnapshot(dir, h.ToolUseID)
		}
		return 0
	}
	if h.ToolName == "Bash" {
		for _, p := range changedPages(dir, h.ToolUseID) {
			postPage(args[0], api.PageHook{SessionID: h.SessionID, Path: filepath.Join(dir, p.name), New: p.isNew})
		}
		return 0
	}
	if !isPage(dir, h.ToolInput.FilePath) {
		return 0
	}
	postPage(args[0], api.PageHook{SessionID: h.SessionID, Path: h.ToolInput.FilePath, New: h.ToolName == "Write" && !writeUpdated(h.ToolResponse)})
	return 0
}

// postPage tells bx serve at base about a page. bx serve down: no link, Claude carries on.
func postPage(base string, h api.PageHook) {
	body, _ := json.Marshal(h)
	c := &http.Client{Timeout: 2 * time.Second}
	resp, err := c.Post(strings.TrimRight(base, "/")+"/api/hooks/page", "application/json", bytes.NewReader(body))
	if err == nil {
		_ = resp.Body.Close()
	}
}

// pageStamp is what a snapshot keeps of a page: a page whose size or time differs was written.
type pageStamp struct {
	Size    int64 `json:"size"`
	ModTime int64 `json:"mtime"` // nanoseconds
}

// scanPages are the pages at the top of the artifacts folder dir, by name.
func scanPages(dir string) (map[string]pageStamp, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := map[string]pageStamp{}
	for _, e := range entries {
		if !e.Type().IsRegular() || !isPage(dir, filepath.Join(dir, e.Name())) {
			continue
		}
		if fi, err := e.Info(); err == nil {
			out[e.Name()] = pageStamp{Size: fi.Size(), ModTime: fi.ModTime().UnixNano()}
		}
	}
	return out, nil
}

// snapshotPath is where the snapshot of a Bash call lives between its two hooks: a private
// temp folder, so the artifacts file server never serves it. "" when the id can't name a file.
func snapshotPath(toolUseID string) string {
	if toolUseID == "" || toolUseID != filepath.Base(toolUseID) || strings.HasPrefix(toolUseID, ".") {
		return ""
	}
	return filepath.Join(os.TempDir(), "bx-page-hook-"+strconv.Itoa(os.Getuid()), toolUseID+".json")
}

// saveSnapshot notes the pages of dir before Bash call toolUseID runs.
func saveSnapshot(dir, toolUseID string) {
	path := snapshotPath(toolUseID)
	if dir == "" || path == "" {
		return
	}
	pages, err := scanPages(dir)
	if err != nil {
		return
	}
	if os.MkdirAll(filepath.Dir(path), 0o700) != nil {
		return
	}
	b, _ := json.Marshal(pages)
	_ = os.WriteFile(path, b, 0o600)
}

type changedPage struct {
	name    string
	isNew   bool
	modTime int64
}

// changedPages are the pages of dir that Bash call toolUseID wrote, by name: those not in its
// snapshot (new) or changed since, oldest first, so the banner ends on the latest. It removes
// the snapshot; with none it returns nothing.
func changedPages(dir, toolUseID string) []changedPage {
	path := snapshotPath(toolUseID)
	if dir == "" || path == "" {
		return nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	_ = os.Remove(path)
	var before map[string]pageStamp
	if json.Unmarshal(b, &before) != nil {
		return nil
	}
	now, err := scanPages(dir)
	if err != nil {
		return nil
	}
	var out []changedPage
	for name, st := range now {
		if old, ok := before[name]; !ok || old != st {
			out = append(out, changedPage{name: name, isNew: !ok, modTime: st.ModTime})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].modTime != out[j].modTime {
			return out[i].modTime < out[j].modTime
		}
		return out[i].name < out[j].name
	})
	return out
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
