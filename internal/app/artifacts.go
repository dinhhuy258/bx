package app

import (
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

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
	s := `This session runs in bx. When the user wants a page, report, chart, diagram or mockup to open in a browser (an "artifact"), use the bx:artifact skill and save it in ` + dir + "."
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

// pageHookTools are the Claude tools that write a page: the file tools, and Bash, whose
// heredocs, sed -i and scripts write pages too.
const pageHookTools = "Write|Edit|MultiEdit|Bash"

// pageHooks are the --settings hooks that run `bx page-hook <api>` after each write, edit or
// command, and `bx page-hook --before` before each command, which notes the session's pages so
// the hook after it can tell which the command wrote. The hooks do nothing unless a page in the
// session's artifacts folder ($BX_ARTIFACTS) changed.
func pageHooks(self, api string) map[string]any {
	before := map[string]any{"type": "command", "command": shellJoin([]string{self, "page-hook", "--before"}), "timeout": 5}
	after := map[string]any{"type": "command", "command": shellJoin([]string{self, "page-hook", api}), "timeout": 5}
	return map[string]any{
		"PreToolUse":  []any{map[string]any{"matcher": "Bash", "hooks": []any{before}}},
		"PostToolUse": []any{map[string]any{"matcher": pageHookTools, "hooks": []any{after}}},
	}
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

// docGlobs are the doc_pages globs made absolute under the session's folder dir, joined for
// $BX_DOCS: the page hook copies a Markdown or HTML doc Claude writes there into the artifacts folder.
// Blank and absolute globs are skipped: a doc is always the session's own.
func docGlobs(dir string, globs []string) string {
	var out []string
	for _, g := range globs {
		if g = strings.TrimSpace(g); g != "" && !filepath.IsAbs(g) {
			out = append(out, filepath.Join(dir, g))
		}
	}
	return strings.Join(out, string(os.PathListSeparator))
}
