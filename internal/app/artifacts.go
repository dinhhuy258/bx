package app

import (
	"net/url"
	"path/filepath"
	"strconv"

	"github.com/dinhhuy258/bx/internal/uploads"
)

// artifactTools are the claude.ai Artifact tools; bx sessions keep their pages local instead.
var artifactTools = []string{"Artifact", "ArtifactComments", "ArtifactData"}

// ArtifactsDir is session num's artifacts folder: <root>/artifacts/<project>/<num>.
func (a *App) ArtifactsDir(project string, num int) string {
	return uploads.Dir(a.Cfg.ArtifactsDir(), project, num)
}

// ArtifactsLink is the file server URL of that folder, without a trailing slash; "" when the
// file server is off.
func (a *App) ArtifactsLink(project string, num int) string {
	if a.ArtifactsURL == "" {
		return ""
	}
	return a.ArtifactsURL + "/" + url.PathEscape(project) + "/" + strconv.Itoa(num)
}

// artifactsPrompt is the --append-system-prompt of a session. Claude records it on the first
// request and reuses it on resume, so it holds only what never changes for the session.
func artifactsPrompt(dir, link string) string {
	s := `This session runs in bx. When the user wants an HTML page, report, chart, diagram or mockup to open in a browser (an "artifact"), use the bx:artifact skill and save it in ` + dir + "."
	if link != "" {
		s += " Its link is " + link + "/<file name>."
	}
	return s
}

// artifactPermissions let Claude write and edit in dir without asking. "//" starts an
// absolute path in a permission rule.
func artifactPermissions(dir string) map[string]any {
	rule := "/" + filepath.ToSlash(dir) + "/**"
	return map[string]any{"allow": []string{"Write(" + rule + ")", "Edit(" + rule + ")"}}
}

// removeSessionFiles deletes session num's uploads, artifacts and MCP config. Best effort: a leftover
// folder is harmless, and uploads.Remove refuses a symlinked path rather than follow it.
func (a *App) removeSessionFiles(project string, num int) {
	for _, base := range []string{a.Cfg.UploadsDir(), a.Cfg.ArtifactsDir(), a.Cfg.SessionMCPDir()} {
		_ = uploads.Remove(base, project, num)
	}
}

// pageHookTools are the Claude tools that write a page.
const pageHookTools = "Write|Edit|MultiEdit"

// pageHooks are the --settings hooks that run `bx page-hook <api>` after each write or edit. The
// hook does nothing unless the file is a page in the session's artifacts folder ($BX_ARTIFACTS).
func pageHooks(self, api string) map[string]any {
	hook := map[string]any{"type": "command", "command": shellJoin([]string{self, "page-hook", api}), "timeout": 5}
	return map[string]any{"PostToolUse": []any{map[string]any{"matcher": pageHookTools, "hooks": []any{hook}}}}
}

// withHooks merges the hook events of add into hooks (nil is empty), keeping both sides' entries.
func withHooks(hooks, add map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range hooks {
		out[k] = v
	}
	for k, v := range add {
		if cur, ok := out[k].([]any); ok {
			out[k] = append(append([]any{}, cur...), v.([]any)...)
		} else {
			out[k] = v
		}
	}
	return out
}
