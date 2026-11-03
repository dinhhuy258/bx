package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// guardTools are the Claude tools whose edits the guard checks. Bash is not: it cannot tell
// what a command writes.
const guardTools = "Edit|MultiEdit|Write|NotebookEdit"

// guardHooks are the --settings hooks that run `bx guard <dir>` before each edit of a session
// in the workspace story folder dir.
func guardHooks(self, dir string) map[string]any {
	hook := map[string]any{"type": "command", "command": shellJoin([]string{self, "guard", dir}), "timeout": 10}
	return map[string]any{"PreToolUse": []any{map[string]any{"matcher": guardTools, "hooks": []any{hook}}}}
}

// statusLine is the --settings status line that runs `bx statusline`: the model and usage
// under Claude's input box, which the mobile and TUI terminals show.
func statusLine(self string) map[string]any {
	return map[string]any{"type": "command", "command": shellJoin([]string{self, "statusline"})}
}

// settingsJSON is a session's --settings: the guard hooks, the artifacts permissions and the
// status line, each left out when nil; "" when all are.
func settingsJSON(hooks, permissions, status map[string]any) string {
	m := map[string]any{}
	if hooks != nil {
		m["hooks"] = hooks
	}
	if permissions != nil {
		m["permissions"] = permissions
	}
	if status != nil {
		m["statusLine"] = status
	}
	if len(m) == 0 {
		return ""
	}
	b, _ := json.Marshal(m)
	return string(b)
}

// Guard checks an edit of file in the workspace story folder dir. It returns why the edit is
// refused, or "" to allow it: an edit through a link in dir would change the original folder,
// which is what a repository not added to the story is.
func Guard(dir, file string) string {
	dir = filepath.Clean(dir)
	rel, err := filepath.Rel(dir, filepath.Clean(file))
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "" // not in the story folder
	}
	parts := strings.Split(rel, string(filepath.Separator))
	link := -1
	for i := range parts {
		fi, err := os.Lstat(filepath.Join(dir, filepath.Join(parts[:i+1]...)))
		if err != nil {
			break // a new file or folder: the rest does not exist yet
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			link = i
			break
		}
	}
	if link < 0 {
		return ""
	}
	story := filepath.Base(dir)
	for j := len(parts) - 1; j >= link; j-- { // the innermost repository holding the file
		sub := filepath.Join(parts[:j+1]...)
		real, err := filepath.EvalSymlinks(filepath.Join(dir, sub))
		if err != nil {
			continue
		}
		if _, err := os.Lstat(filepath.Join(real, ".git")); err == nil {
			r := filepath.ToSlash(sub)
			return fmt.Sprintf("%s is not part of story %s; edits would change the original repo. Ask the user to add it (press + on the story in bx).", r, story)
		}
	}
	return fmt.Sprintf("%s is a link to the original workspace folder, not part of story %s; edits would change the original. Ask the user before editing it there.",
		filepath.ToSlash(filepath.Join(parts[:link+1]...)), story)
}
