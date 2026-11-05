package commands

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dinhhuy258/bx/internal/api"
	"github.com/dinhhuy258/bx/internal/cli"
	"github.com/dinhhuy258/bx/internal/serve"
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
// serve, which shows the link in the phone and the TUI. A Markdown doc matching $BX_DOCS (the
// doc_pages globs) is first copied into that folder, where it is a page too. A Bash command
// names no file: the hook compares the docs and the folder with the snapshot that
// `bx page-hook --before` took (the PreToolUse hook of Bash), copies each doc the command
// wrote, and tells bx serve of each page that changed. It runs after every edit and command, so
// anything else exits at once; it never blocks Claude and always exits 0.
func runPageHook(args []string, out, errOut io.Writer) int {
	if len(args) != 1 {
		return 0
	}
	var h postToolUse
	if json.NewDecoder(os.Stdin).Decode(&h) != nil {
		return 0
	}
	dir := os.Getenv("BX_ARTIFACTS")
	docs := filepath.SplitList(os.Getenv("BX_DOCS"))
	if args[0] == "--before" {
		if h.ToolName == "Bash" {
			saveSnapshot(dir, docs, h.ToolUseID)
		}
		return 0
	}
	if h.ToolName == "Bash" {
		before, ok := loadSnapshot(h.ToolUseID)
		if !ok {
			return 0
		}
		for _, d := range changed(scanDocs(docs), before.Docs) {
			mirrorDoc(dir, d.name)
		}
		now, err := scanPages(dir)
		if err != nil {
			return 0
		}
		for _, p := range changed(now, before.Pages) {
			postPage(args[0], api.PageHook{SessionID: h.SessionID, Path: filepath.Join(dir, p.name), New: p.isNew})
		}
		return 0
	}
	file, isNew := h.ToolInput.FilePath, h.ToolName == "Write" && !writeUpdated(h.ToolResponse)
	if isDoc(docs, file) {
		var ok bool
		if file, isNew, ok = mirrorDoc(dir, file); !ok {
			return 0
		}
	}
	if !isPage(dir, file) {
		return 0
	}
	postPage(args[0], api.PageHook{SessionID: h.SessionID, Path: file, New: isNew})
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

// pageStamp is what a snapshot keeps of a file: a file whose size or time differs was written.
type pageStamp struct {
	Size    int64 `json:"size"`
	ModTime int64 `json:"mtime"` // nanoseconds
}

// stampOf is the stamp of a regular file, false for anything else.
func stampOf(fi fs.FileInfo) (pageStamp, bool) {
	if !fi.Mode().IsRegular() {
		return pageStamp{}, false
	}
	return pageStamp{Size: fi.Size(), ModTime: fi.ModTime().UnixNano()}, true
}

// scanPages are the pages at the top of the artifacts folder dir, by name.
func scanPages(dir string) (map[string]pageStamp, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := map[string]pageStamp{}
	for _, e := range entries {
		if !isPage(dir, filepath.Join(dir, e.Name())) {
			continue
		}
		if fi, err := e.Info(); err == nil {
			if st, ok := stampOf(fi); ok {
				out[e.Name()] = st
			}
		}
	}
	return out, nil
}

// scanDocs are the docs matching globs, by path.
func scanDocs(globs []string) map[string]pageStamp {
	out := map[string]pageStamp{}
	for _, g := range globs {
		matches, _ := filepath.Glob(g)
		for _, m := range matches {
			if !isDoc(globs, m) {
				continue
			}
			if fi, err := os.Lstat(m); err == nil {
				if st, ok := stampOf(fi); ok {
					out[m] = st
				}
			}
		}
	}
	return out
}

// snapshot is what `bx page-hook --before` notes before a Bash call: the pages of the artifacts
// folder by name and the docs matching $BX_DOCS by path.
type snapshot struct {
	Pages map[string]pageStamp `json:"pages"`
	Docs  map[string]pageStamp `json:"docs"`
}

// snapshotPath is where the snapshot of a Bash call lives between its two hooks: a private
// temp folder, so the artifacts file server never serves it. "" when the id can't name a file.
func snapshotPath(toolUseID string) string {
	if toolUseID == "" || toolUseID != filepath.Base(toolUseID) || strings.HasPrefix(toolUseID, ".") {
		return ""
	}
	return filepath.Join(os.TempDir(), "bx-page-hook-"+strconv.Itoa(os.Getuid()), toolUseID+".json")
}

// saveSnapshot notes the pages of dir and the docs before Bash call toolUseID runs.
func saveSnapshot(dir string, docs []string, toolUseID string) {
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
	b, _ := json.Marshal(snapshot{Pages: pages, Docs: scanDocs(docs)})
	_ = os.WriteFile(path, b, 0o600)
}

// loadSnapshot reads and removes the snapshot of Bash call toolUseID; false when there is none.
func loadSnapshot(toolUseID string) (snapshot, bool) {
	var s snapshot
	path := snapshotPath(toolUseID)
	if path == "" {
		return s, false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return s, false
	}
	_ = os.Remove(path)
	return s, json.Unmarshal(b, &s) == nil
}

type changedPage struct {
	name    string
	isNew   bool
	modTime int64
}

// changed are the files of now not in before (new) or changed since, oldest first, so the
// banner ends on the latest.
func changed(now, before map[string]pageStamp) []changedPage {
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

// isPage reports whether file is a page (serve.IsPageName) at the top of the artifacts folder dir.
func isPage(dir, file string) bool {
	if dir == "" || file == "" || !serve.IsPageName(filepath.Base(file)) {
		return false
	}
	return filepath.Dir(filepath.Clean(file)) == filepath.Clean(dir)
}

// isDoc reports whether file is a Markdown doc matching one of the $BX_DOCS globs.
func isDoc(globs []string, file string) bool {
	if !filepath.IsAbs(file) || strings.HasPrefix(filepath.Base(file), ".") || !strings.EqualFold(filepath.Ext(file), ".md") {
		return false
	}
	file = strings.TrimSuffix(filepath.Clean(file), filepath.Ext(file)) + ".md" // globs say .md; a.MD counts too
	for _, g := range globs {
		if ok, _ := filepath.Match(g, file); ok {
			return true
		}
	}
	return false
}

// mirrorDoc copies doc src to the top of the artifacts folder dir under its own name, through
// a dot-named temp file the file server never serves. isNew is whether no copy was there.
func mirrorDoc(dir, src string) (dst string, isNew, ok bool) {
	if dir == "" {
		return "", false, false
	}
	b, err := os.ReadFile(src)
	if err != nil {
		return "", false, false
	}
	dst = filepath.Join(dir, filepath.Base(src))
	_, err = os.Lstat(dst)
	isNew = errors.Is(err, fs.ErrNotExist)
	f, err := os.CreateTemp(dir, ".doc-*")
	if err != nil {
		return "", false, false
	}
	tmp := f.Name()
	_, err = f.Write(b)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, dst)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return "", false, false
	}
	return dst, isNew, true
}

// writeUpdated reports whether a Write's tool_response says it replaced a file ("type":
// "update"); one that says nothing counts as new.
func writeUpdated(resp json.RawMessage) bool {
	var r struct {
		Type string `json:"type"`
	}
	return json.Unmarshal(resp, &r) == nil && r.Type == "update"
}
