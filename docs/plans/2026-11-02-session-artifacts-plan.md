# Session Artifacts Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use build to implement this plan task-by-task.

**Goal:** Claude sessions started by bx save HTML "artifacts" (designed with Claude's `artifact-design` skill) to a local per-session folder instead of uploading them to claude.ai, and the phone and Mac open them over Tailscale.

**Architecture:** `bx serve` writes an embedded Claude Code plugin (`bx:artifact` skill) to `~/.bx/claude-plugin` and starts every session with `--plugin-dir`, `--disallowed-tools Artifact,…`, `--add-dir <artifacts folder>`, an allow rule for that folder and a one-line `--append-system-prompt`. Pages land in `~/.bx/artifacts/<project>/<num>/`, outside every repository. A second listener on the Tailscale address, port 7358, serves that folder read-only with a sandbox CSP. The web client (session menu) and the TUI (`v`) list a session's pages from a new API route. Deleting a session (or closing its story) deletes its folder; stopping it does not.

**Tech Stack:** Go 1.26 (`net/http`, `os.Root`, `embed`), plain ES modules for the web client, Claude Code CLI flags (`--plugin-dir`, `--disallowed-tools`, `--add-dir`, `--append-system-prompt`).

## Decisions (from the 2026-11-02 conversation; there is no separate design doc)

- **Storage:** `<bx root>/artifacts/<project>/<num>/`. It's per session, and bx never reuses a session number. It's outside git, so pages never show as untracked files and never block closing a story.
- **Cleanup:** `DeleteSession` and `forgetStory` (close story, which the cleanup screen also reaches) remove the folder. `Stop` and Claude exiting keep it, because a resumed session should still have its pages. bx has no "delete project" today, so there's nothing to hook there.
- **No upload:** `--disallowed-tools Artifact,ArtifactComments,ArtifactData` removes the claude.ai tools from the model. The `artifact-design` skill still loads (checked by the user), and `bx:artifact` reuses it, replacing only its publish and host rules.
- **No install step:** the plugin is embedded in the binary and written at `bx serve` start. Only sessions started by bx see it.
- **Prompt:** inline `--append-system-prompt`, one sentence holding the session's folder and link. Claude Code records the system prompt on the first request and reuses it on resume, so the text holds only values that stay fixed for the life of a session.
- **Safety:** the file server is a separate port. `sameOrigin` refuses any `Origin` whose host differs from the request's, and `CSP: sandbox` gives the page an opaque `null` origin. It uses `os.Root`, so neither `..` nor a symlink can escape. Dotfiles and folders return 404, and only GET and HEAD are allowed.
- **Gating:** everything runs only when `App.PluginDir != ""`. `bx serve` sets it, and tests leave it empty, so every existing launch-command assertion stays unchanged.
- **Out of scope:** an ntfy push when Claude writes a page (a later `PostToolUse` hook in the same plugin) and a "delete project" action.

---

### Task 0: Branch

```bash
cd /Users/dinhhuy258/Workspace/bx
git status --short          # expect only .kiro/ and docs/ untracked; stop and ask if anything else is dirty
git checkout -b feat/session-artifacts master
git add docs/plans/2026-11-02-session-artifacts-plan.md
git commit -m "docs: session artifacts plan"
```

All later commits go on `feat/session-artifacts`. Do not merge into `master` or push.

---

### Task 1: Config — artifacts and plugin folders, `serve.files_listen`

**Files:**
- Modify: `internal/config/config.go` (`Serve` struct ~line 24, `Default` ~line 70, `rawConfig`/`applyTo` ~lines 100–125, paths after `UploadsDir` ~line 168)
- Test: `internal/config/config_test.go` (append)

**Step 1: Write the failing test.** Append to `internal/config/config_test.go`:

```go
func TestArtifactsConfig(t *testing.T) {
	root := t.TempDir()
	c, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if c.Serve.FilesListen != "auto" {
		t.Errorf("files_listen default = %q", c.Serve.FilesListen)
	}
	if c.ArtifactsDir() != filepath.Join(root, "artifacts") || c.PluginDir() != filepath.Join(root, "claude-plugin") {
		t.Errorf("dirs %q %q", c.ArtifactsDir(), c.PluginDir())
	}
	if err := os.WriteFile(filepath.Join(root, "config.toml"), []byte("[serve]\nfiles_listen = \"off\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if c, err = Load(root); err != nil || c.Serve.FilesListen != "off" {
		t.Fatalf("override: %q %v", c.Serve.FilesListen, err)
	}
}
```

(Add `"os"` and `"path/filepath"` to the test file's imports if they aren't already there.)

**Step 2: Run the test and check that it fails.**

Run: `go test ./internal/config/ -run TestArtifactsConfig`
Expected: FAIL to compile with `c.Serve.FilesListen undefined`

**Step 3: Write the implementation.**

In `internal/config/config.go`, replace the `Serve` struct:

```go
// Serve holds `bx serve` settings. Listen is "auto" (the Tailscale address, port 7357)
// or an explicit host:port; only Tailscale (100.64.0.0/10) and loopback addresses are accepted.
// FilesListen is the artifact file server: "auto" (Listen's host, port 7358), "off", or an
// explicit host:port under the same rule.
type Serve struct {
	Listen      string `toml:"listen"`
	FilesListen string `toml:"files_listen"`
}
```

In `Default`, replace `Serve:  Serve{Listen: "auto"},` with:

```go
		Serve:  Serve{Listen: "auto", FilesListen: "auto"},
```

In `rawConfig`, replace the `Serve` field with:

```go
	Serve struct {
		Listen      *string `toml:"listen"`
		FilesListen *string `toml:"files_listen"`
	} `toml:"serve"`
```

In `applyTo`, after the `r.Serve.Listen` block, add:

```go
	if r.Serve.FilesListen != nil {
		c.Serve.FilesListen = *r.Serve.FilesListen
	}
```

After `UploadsDir`, add:

```go
// ArtifactsDir is <root>/artifacts: the HTML pages Claude makes for each session, outside every
// repository, served read-only by bx serve's file listener.
func (c *Config) ArtifactsDir() string { return filepath.Join(c.Root, "artifacts") }

// PluginDir is <root>/claude-plugin: the Claude Code plugin bx gives every session it starts.
func (c *Config) PluginDir() string { return filepath.Join(c.Root, "claude-plugin") }
```

**Step 4: Run the test and check that it passes.**

Run: `go test ./internal/config/`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/config && git commit -m "feat(config): artifacts and plugin folders, serve.files_listen"
```

---

### Task 2: `uploads.MakeDir`, the shared per-session folder helper

The artifacts folder has the same shape and the same symlink rules as the uploads folder (`base/<project>/<num>`). Reuse `uploads.Dir`, `uploads.Remove` and an exported `makeDirs` with a different base rather than copying them.

**Files:**
- Modify: `internal/uploads/uploads.go` (package doc lines 1–4, `Save` ~line 88, `makeDirs` ~line 99)
- Test: `internal/uploads/uploads_test.go` (append)

**Step 1: Write the failing test.** Append to `internal/uploads/uploads_test.go`:

```go
func TestMakeDirIsTheSessionFolder(t *testing.T) {
	base := filepath.Join(t.TempDir(), "artifacts")
	dir, err := MakeDir(base, "home-network", 4)
	if err != nil || dir != Dir(base, "home-network", 4) {
		t.Fatalf("%q %v", dir, err)
	}
	if fi, err := os.Stat(dir); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("%v %v", fi, err)
	}
	if _, err := MakeDir(base, "../x", 4); !errors.Is(err, ErrInvalid) {
		t.Fatalf("dotted project: %v", err)
	}
}
```

**Step 2: Run the test and check that it fails.**

Run: `go test ./internal/uploads/ -run TestMakeDirIsTheSessionFolder`
Expected: FAIL to compile with `undefined: MakeDir`

**Step 3: Write the implementation.**

Replace the package doc comment (lines 1–4) with:

```go
// Package uploads stores the files sent from the phone for a session, at
// <bx root>/uploads/<project>/<num>/<YYYYMMDD-HHMMSS>-<safe name>: outside every repository,
// so worktrees never get untracked files. It never follows a symlink: every folder on the way
// must be a real directory, or nothing is written or removed. Dir, MakeDir and Remove take the
// base folder, so the session artifacts (<bx root>/artifacts) use them too.
```

Replace `makeDirs` with:

```go
// MakeDir creates base, base/project and base/project/num (mode 0700), checking each is a
// real directory, and returns base/project/num.
func MakeDir(base, project string, num int) (string, error) {
	if err := validProject(project); err != nil {
		return "", err
	}
	if num <= 0 {
		return "", fmt.Errorf("session %d: %w", num, ErrInvalid)
	}
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", err
	}
	if err := realDir(base, base); err != nil {
		return "", err
	}
	dir := base
	for _, part := range []string{project, strconv.Itoa(num)} {
		dir = filepath.Join(dir, part)
		if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return "", err
		}
		if err := realDir(base, dir); err != nil {
			return "", err
		}
	}
	return dir, nil
}
```

In `Save`, replace `dir, err := makeDirs(base, project, num)` with `dir, err := MakeDir(base, project, num)`. `Save` keeps its own `validProject` and `num` checks before them, because its size check must come first.

**Step 4: Run the test and check that it passes.**

Run: `go test ./internal/uploads/`
Expected: PASS (all existing tests too)

**Step 5: Commit**

```bash
git add internal/uploads && git commit -m "refactor(uploads): export MakeDir for other per-session folders"
```

---

### Task 3: New Claude flags in `StartArgs`

**Files:**
- Modify: `internal/claude/agents.go:98-116`
- Test: `internal/claude/agents_test.go` (append)

**Step 1: Write the failing test.** Append to `internal/claude/agents_test.go`:

```go
func TestStartArgsArtifactFlags(t *testing.T) {
	o := Options{Settings: `{"permissions":{}}`, DisallowedTools: []string{"Artifact", "ArtifactData"},
		PluginDirs: []string{"/bx/plugin"}, AddDirs: []string{"/bx/artifacts/p/1"}, AppendSystemPrompt: "Save pages in /bx/artifacts/p/1."}
	want := []string{"claude", "--resume", "u", "--settings", `{"permissions":{}}`,
		"--disallowed-tools", "Artifact,ArtifactData", "--plugin-dir", "/bx/plugin",
		"--add-dir", "/bx/artifacts/p/1", "--append-system-prompt", "Save pages in /bx/artifacts/p/1."}
	if got := StartArgs("u", true, o); !equal(got, want) {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}
```

**Step 2: Run the test and check that it fails.**

Run: `go test ./internal/claude/ -run TestStartArgsArtifactFlags`
Expected: FAIL to compile with `unknown field DisallowedTools`

**Step 3: Write the implementation.** Replace `Options` and `StartArgs` in `internal/claude/agents.go`:

```go
type Options struct {
	Model, Effort, PermissionMode string
	Settings                      string   // extra settings JSON for --settings, such as hooks
	DisallowedTools               []string // --disallowed-tools, comma-joined: one value, so it never swallows a later argument
	PluginDirs                    []string // --plugin-dir, one per folder
	AddDirs                       []string // --add-dir, one per folder
	AppendSystemPrompt            string   // --append-system-prompt
}

// StartArgs builds the command for a new or resumed session. Run it in the target folder.
// No -n: bx shows the title in its own pane header, and Claude would repeat it on the input box.
// A resume passes every flag again: --resume does not remember them.
func StartArgs(uuid string, resume bool, o Options) []string {
	args := []string{"claude", "--session-id", uuid}
	if resume {
		args = []string{"claude", "--resume", uuid}
	}
	for _, f := range [][2]string{{"--permission-mode", o.PermissionMode}, {"--model", o.Model}, {"--effort", o.Effort}, {"--settings", o.Settings}} {
		if f[1] != "" {
			args = append(args, f[0], f[1])
		}
	}
	if len(o.DisallowedTools) > 0 {
		args = append(args, "--disallowed-tools", strings.Join(o.DisallowedTools, ","))
	}
	for _, d := range o.PluginDirs {
		args = append(args, "--plugin-dir", d)
	}
	for _, d := range o.AddDirs {
		args = append(args, "--add-dir", d)
	}
	if o.AppendSystemPrompt != "" {
		args = append(args, "--append-system-prompt", o.AppendSystemPrompt)
	}
	return args
}
```

Add `"strings"` to the imports of `agents.go` if it isn't already there.

**Step 4: Run the test and check that it passes.**

Run: `go test ./internal/claude/`
Expected: PASS (the existing `TestStartArgs` too)

**Step 5: Commit**

```bash
git add internal/claude && git commit -m "feat(claude): plugin, add-dir, disallowed-tools and system prompt flags"
```

---

### Task 4: The embedded plugin with the `bx:artifact` skill

**Files:**
- Create: `internal/claudeplugin/plugin/.claude-plugin/plugin.json`
- Create: `internal/claudeplugin/plugin/skills/artifact/SKILL.md`
- Create: `internal/claudeplugin/claudeplugin.go`
- Test: `internal/claudeplugin/claudeplugin_test.go`

**Step 1: Write the plugin files.**

`internal/claudeplugin/plugin/.claude-plugin/plugin.json`:

```json
{
  "name": "bx",
  "version": "1.0.0",
  "description": "What bx gives the Claude sessions it starts: local HTML artifacts."
}
```

`internal/claudeplugin/plugin/skills/artifact/SKILL.md`:

```markdown
---
name: artifact
description: Make an HTML page, report, chart, diagram, dashboard or mockup the user will open in a browser (an "artifact"). In bx it is saved in this session's artifacts folder and served over Tailscale, never uploaded to claude.ai.
---

# Artifacts in bx

This session runs under bx. An artifact here is a local HTML file, not a claude.ai page; the
Artifact tool is turned off.

1. Load the `artifact-design` skill and follow its design guidance: tokens, both themes,
   phone width, typography, copy, charts drawn to scale.
2. Ignore what it says about publishing and about the claude.ai viewer. Instead:
   - Write a complete document: `<!doctype html>`, `<html lang="en">`, a `<head>` with
     `<meta charset="utf-8">` and
     `<meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">`,
     the `<title>`, then `<style>`, then `<body>`.
   - Add the reset the claude.ai skeleton would have added:
     `body{margin:0;font:14px/1.5 system-ui,sans-serif}`, `img{max-width:100%}`,
     `[hidden]{display:none!important}`, and pad `:root` top and bottom with
     `env(safe-area-inset-top, 0px)` and `env(safe-area-inset-bottom, 0px)`.
   - Mermaid is not rendered for you: load it from cdnjs at an exact version and call
     `mermaid.initialize({ startOnLoad: true })`.
   - There is no `window.claude`, no runtime capabilities, no ArtifactData or ArtifactComments
     and no preview tool. Don't use or mention them, and skip the artifact-capabilities skill.
   - The page is served with `Content-Security-Policy: sandbox allow-scripts …`, so it runs in
     an opaque origin: `localStorage` throws (keep the try/catch the design skill asks for).
3. Save the page in this session's artifacts folder, named in your system prompt (also in
   `$BX_ARTIFACTS`). Use a short kebab-case file name ending in `.html`. Put extra files
   (images, data, scripts) next to it and reference them by relative path. Never write the page
   into the repository unless the user asks to add it to the project.
4. To change a page, edit the same file; the user reloads the link.
5. Reply in one line with the link from your system prompt plus the file name. If there is no
   link, give the file's path.
```

**Step 2: Write the failing test.** Create `internal/claudeplugin/claudeplugin_test.go`:

```go
package claudeplugin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteInstallsAndRefreshesThePlugin(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "claude-plugin")
	if err := Write(dir); err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(filepath.Join(dir, ".claude-plugin", "plugin.json"))
	if err != nil || !strings.Contains(string(manifest), `"name": "bx"`) {
		t.Fatalf("manifest %q %v", manifest, err)
	}
	skill := filepath.Join(dir, "skills", "artifact", "SKILL.md")
	b, err := os.ReadFile(skill)
	if err != nil || !strings.Contains(string(b), "name: artifact") || !strings.Contains(string(b), "artifact-design") {
		t.Fatalf("skill %q %v", b, err)
	}
	// an unchanged file is left alone; a stale one is replaced
	old := time.Now().Add(-time.Hour)
	_ = os.Chtimes(skill, old, old)
	if err := Write(dir); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(skill); !fi.ModTime().Equal(old) {
		t.Fatal("unchanged skill rewritten")
	}
	_ = os.WriteFile(skill, []byte("stale"), 0o644)
	if err := Write(dir); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(skill); string(b) == "stale" {
		t.Fatal("stale skill kept")
	}
}
```

**Step 3: Run the test and check that it fails.**

Run: `go test ./internal/claudeplugin/`
Expected: FAIL to compile with `undefined: Write`

**Step 4: Write the implementation.** Create `internal/claudeplugin/claudeplugin.go`:

```go
// Package claudeplugin is the Claude Code plugin bx gives every session it starts
// (--plugin-dir): the bx:artifact skill. It ships inside the bx binary and bx serve writes it
// out at start, so upgrading bx upgrades the plugin and nothing is installed into ~/.claude.
package claudeplugin

import (
	"bytes"
	"embed"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

//go:embed all:plugin
var files embed.FS

// Write puts the plugin in dir. A file whose content already matches is left alone; any other
// is replaced atomically.
func Write(dir string) error {
	return fs.WalkDir(files, "plugin", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(dir, filepath.FromSlash(strings.TrimPrefix(strings.TrimPrefix(p, "plugin"), "/")))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		want, err := files.ReadFile(p)
		if err != nil {
			return err
		}
		if got, err := os.ReadFile(target); err == nil && bytes.Equal(got, want) {
			return nil
		}
		return writeAtomic(target, want)
	})
}

// writeAtomic writes a temp file next to p and renames it over p.
func writeAtomic(p string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(p), "."+path.Base(filepath.ToSlash(p))+"-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(data)
	if err == nil {
		err = f.Chmod(0o644)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, p)
	}
	if err != nil {
		_ = os.Remove(tmp)
	}
	return err
}
```

**Step 5: Run the test and check that it passes.**

Run: `go test ./internal/claudeplugin/`
Expected: PASS

**Step 6: Check that Claude loads the plugin (manual, about 1 minute).**

```bash
go run ./cmd/bx >/dev/null 2>&1 || true   # nothing to run; write the plugin with a tiny test instead:
go test ./internal/claudeplugin/ -run TestWrite -v
mkdir -p /tmp/bx-plugin-check && cp -R internal/claudeplugin/plugin/. /tmp/bx-plugin-check/
claude --plugin-dir /tmp/bx-plugin-check --disallowed-tools Artifact,ArtifactComments,ArtifactData
#   > /skills            → lists bx:artifact
#   > load artifact-design → it loads (the Artifact tool is off, the skill is not)
rm -rf /tmp/bx-plugin-check
```

If `bx:artifact` isn't listed, **stop and ask the user**. The `--plugin-dir` layout is the one assumption in this plan that hasn't been checked.

**Step 7: Commit**

```bash
git add internal/claudeplugin && git commit -m "feat(claudeplugin): embedded plugin with the bx:artifact skill"
```

---

### Task 5: Start sessions with their artifacts folder

**Files:**
- Modify: `internal/app/app.go:23-38` (the `App` struct)
- Modify: `internal/app/guard.go:15-23` (`GuardSettings` becomes `guardHooks` + `settingsJSON`)
- Modify: `internal/app/sessions.go` (`options` ~line 96, `launch` ~line 106, the three `a.options(p, s)` calls at ~lines 86, 131, 160)
- Create: `internal/app/artifacts.go`
- Modify: `internal/app/workspace_test.go:261-262`
- Test: `internal/app/artifacts_test.go`

**Step 1: Write the failing test.** Create `internal/app/artifacts_test.go`:

```go
package app

import (
	"os"
	"strings"
	"testing"
)

func artifactsApp(t *testing.T) (*App, func() string, *state.Project) {
	t.Helper()
	a, f, p := newApp(t)
	a.PluginDir = "/opt/bx-plugin"
	a.ArtifactsURL = "http://100.64.0.1:7358"
	return a, func() string { return calls(f) }, p
}

func TestSessionGetsArtifacts(t *testing.T) {
	a, got, p := artifactsApp(t)
	num, err := a.NewChat(p, "")
	if err != nil {
		t.Fatal(err)
	}
	dir := a.ArtifactsDir(p.Name, num)
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		t.Fatalf("artifacts folder: %v", err)
	}
	for _, want := range []string{
		"env BX_ARTIFACTS=" + dir + " claude --session-id fixed-uuid",
		"--disallowed-tools Artifact,ArtifactComments,ArtifactData",
		"--plugin-dir /opt/bx-plugin",
		"--add-dir " + dir,
		`"allow":["Write(/` + dir + `/**)","Edit(/` + dir + `/**)"]`,
		"use the bx:artifact skill and save it in " + dir,
		"http://100.64.0.1:7358/home-network/1/<file name>",
	} {
		if !strings.Contains(got(), want) {
			t.Errorf("missing %q in:\n%s", want, got())
		}
	}
	if err := a.Resume(p, num); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(got(), "--plugin-dir /opt/bx-plugin"); n != 2 {
		t.Fatalf("resume lost the flags (%d):\n%s", n, got())
	}
}

func TestArtifactsWithoutFileServerGiveNoLink(t *testing.T) {
	a, got, p := artifactsApp(t)
	a.ArtifactsURL = ""
	if _, err := a.NewChat(p, ""); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got(), "<file name>") {
		t.Fatalf("link without a file server:\n%s", got())
	}
}
```

Add `"github.com/dinhhuy258/bx/internal/state"` to that file's imports.

In `internal/app/workspace_test.go`, change lines 261–262 to the new signature, and append a merge check after the existing `if`:

```go
	story := a.options(p, 1, &state.Session{Story: "login"})
	chat := a.options(p, 2, &state.Session{})
```

```go
	a.PluginDir = "/opt/bx-plugin"
	both := a.options(p, 1, &state.Session{Story: "login"})
	if !strings.Contains(both.Settings, `"PreToolUse"`) || !strings.Contains(both.Settings, `"permissions"`) {
		t.Fatalf("guard and artifacts not merged: %q", both.Settings)
	}
```

**Step 2: Run the tests and check that they fail.**

Run: `go test ./internal/app/ -run 'Artifacts|WorkspaceSessionGetsGuard'`
Expected: FAIL to compile with `a.PluginDir undefined`

**Step 3: Write the implementation.**

In `internal/app/app.go`, add to the `App` struct after `Self`:

```go
	// PluginDir is the bx Claude plugin (bx:artifact). When set, every session gets an
	// artifacts folder, the plugin, and no claude.ai Artifact tools; "" gives none of that.
	PluginDir string
	// ArtifactsURL is the file server's base, e.g. http://100.64.0.1:7358; "" when it is off,
	// and Claude then gives the file path instead of a link.
	ArtifactsURL string
```

In `internal/app/guard.go`, replace `GuardSettings` (lines 15–23) with:

```go
// guardHooks are the --settings hooks that run `bx guard <dir>` before each edit of a session
// in the workspace story folder dir.
func guardHooks(self, dir string) map[string]any {
	hook := map[string]any{"type": "command", "command": shellJoin([]string{self, "guard", dir}), "timeout": 10}
	return map[string]any{"PreToolUse": []any{map[string]any{"matcher": guardTools, "hooks": []any{hook}}}}
}

// settingsJSON is a session's --settings: the guard hooks and the artifacts permissions, each
// left out when nil; "" when both are.
func settingsJSON(hooks, permissions map[string]any) string {
	m := map[string]any{}
	if hooks != nil {
		m["hooks"] = hooks
	}
	if permissions != nil {
		m["permissions"] = permissions
	}
	if len(m) == 0 {
		return ""
	}
	b, _ := json.Marshal(m)
	return string(b)
}
```

Create `internal/app/artifacts.go`:

```go
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
```

In `internal/app/sessions.go`, replace `options`:

```go
// options are the settings a session started with, for its command line. A session of a
// workspace story also gets the guard hook, which keeps edits out of the original repositories.
// With PluginDir set, every session also gets its artifacts folder (see artifacts.go).
func (a *App) options(p *state.Project, num int, s *state.Session) claude.Options {
	o := claude.Options{Model: s.Model, Effort: s.Effort, PermissionMode: s.PermissionMode}
	var hooks, perms map[string]any
	if p.IsWorkspace() && s.Story != "" && a.Self != "" {
		hooks = guardHooks(a.Self, a.Dir(p, s.Story))
	}
	if a.PluginDir != "" {
		dir := a.ArtifactsDir(p.Name, num)
		o.DisallowedTools = artifactTools
		o.PluginDirs = []string{a.PluginDir}
		o.AddDirs = []string{dir}
		o.AppendSystemPrompt = artifactsPrompt(dir, a.ArtifactsLink(p.Name, num))
		perms = artifactPermissions(dir)
	}
	o.Settings = settingsJSON(hooks, perms)
	return o
}
```

Update the three call sites:
- in `start`: `a.options(p, num, s)`
- in `Resume`: `a.options(p, num, s)`
- in `ResumeAll`: `a.options(p, n, s)`

Replace `launch`:

```go
// launch runs args in a new tmux session for session num, in its story's folder, and keeps
// the tmux server up after its last session ends, so bx serve can tell that Claude exited.
// With PluginDir set it first makes the session's artifacts folder and sets $BX_ARTIFACTS.
func (a *App) launch(p *state.Project, num int, args []string) error {
	if a.PluginDir != "" {
		dir, err := uploads.MakeDir(a.Cfg.ArtifactsDir(), p.Name, num)
		if err != nil {
			return fmt.Errorf("artifacts folder: %w", err)
		}
		args = append([]string{"env", "BX_ARTIFACTS=" + dir}, args...)
	}
	if _, err := a.Tmux.NewSession(TmuxName(p.Name, num), a.Dir(p, p.Sessions[num].Story), shellJoin(args)); err != nil {
		return err
	}
	// best effort: a Claude that exits at once may take the server with it
	if err := a.Tmux.KeepServer(); err != nil {
		a.logf("tmux keep server: %v", err)
	}
	return nil
}
```

**Step 4: Run the tests and check that they pass.**

Run: `go test ./internal/app/`
Expected: PASS, including every existing test. They don't set `PluginDir`, so their launch commands are unchanged.

**Step 5: Commit**

```bash
git add internal/app && git commit -m "feat(app): start sessions with a local artifacts folder"
```

---

### Task 6: Delete artifacts with the session

**Files:**
- Modify: `internal/app/sessions.go` (`DeleteSession` ~line 220, `forgetStory` ~line 366)
- Modify: `internal/app/artifacts.go` (append)
- Test: `internal/app/artifacts_test.go` (append)

**Step 1: Write the failing test.** Append to `internal/app/artifacts_test.go`:

```go
func TestStopKeepsArtifactsDeleteRemovesThem(t *testing.T) {
	a, _, p := artifactsApp(t)
	num, err := a.NewChat(p, "")
	if err != nil {
		t.Fatal(err)
	}
	page := filepath.Join(a.ArtifactsDir(p.Name, num), "report.html")
	if err := os.WriteFile(page, []byte("<p>x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := a.Stop(p, num); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(page); err != nil {
		t.Fatalf("stop removed the page: %v", err)
	}
	if err := a.DeleteSession(p, num, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(a.ArtifactsDir(p.Name, num)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("folder left after delete: %v", err)
	}
}

func TestCloseStoryRemovesArtifacts(t *testing.T) {
	a, _, p := artifactsApp(t)
	if _, err := a.NewStory(context.Background(), p, StoryOpts{Name: "s1"}); err != nil {
		t.Fatal(err)
	}
	num, err := a.NewSession(p, "s1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a.ArtifactsDir(p.Name, num), "a.html"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := a.CloseStory(p, "s1", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(a.ArtifactsDir(p.Name, num)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("folder left after close: %v", err)
	}
}
```

Add `"context"`, `"errors"` and `"path/filepath"` to the file's imports.

**Step 2: Run the tests and check that they fail.**

Run: `go test ./internal/app/ -run 'StopKeeps|CloseStoryRemovesArtifacts'`
Expected: FAIL with `folder left after delete` and `folder left after close`

**Step 3: Write the implementation.** Append to `internal/app/artifacts.go`:

```go
// removeSessionFiles deletes session num's uploads and artifacts. Best effort: a leftover
// folder is harmless, and uploads.Remove refuses a symlinked path rather than follow it.
func (a *App) removeSessionFiles(project string, num int) {
	for _, base := range []string{a.Cfg.UploadsDir(), a.Cfg.ArtifactsDir()} {
		_ = uploads.Remove(base, project, num)
	}
}
```

In `internal/app/sessions.go`, in `DeleteSession` replace
`_ = uploads.Remove(a.Cfg.UploadsDir(), p.Name, num) // best effort: a leftover folder is harmless`
with `a.removeSessionFiles(p.Name, num)`. In `forgetStory` replace
`_ = uploads.Remove(a.Cfg.UploadsDir(), p.Name, n) // best effort: a leftover folder is harmless`
with `a.removeSessionFiles(p.Name, n)`.

Update the comments of both functions to say "and its uploads and artifacts". `sessions.go` still uses `uploads` in `launch`, so the import stays.

**Step 4: Run the tests and check that they pass.**

Run: `go test ./internal/app/`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/app && git commit -m "feat(app): delete a session's artifacts with it"
```

---

### Task 7: The file server, `ResolveFiles` and `FilesHandler`

**Files:**
- Create: `internal/serve/files.go`
- Test: `internal/serve/files_test.go`

**Step 1: Write the failing test.** Create `internal/serve/files_test.go`:

```go
package serve

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveFiles(t *testing.T) {
	for _, c := range []struct {
		cfg, want string
		bad       bool
	}{
		{"auto", "100.64.0.1:7358", false},
		{"", "100.64.0.1:7358", false},
		{"off", "", false},
		{"localhost:9000", "localhost:9000", false},
		{"0.0.0.0:7358", "", true},
	} {
		got, err := ResolveFiles(c.cfg, "100.64.0.1:7357")
		if (err != nil) != c.bad || got != c.want {
			t.Errorf("ResolveFiles(%q) = %q, %v", c.cfg, got, err)
		}
	}
}

func TestFilesHandler(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "home-network", "3")
	if err := os.MkdirAll(filepath.Join(dir, "img"), 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "report.html"), []byte("<title>Lease report</title>"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, ".env"), []byte("SECRET=1"), 0o600)
	outside := filepath.Join(t.TempDir(), "secret.txt")
	_ = os.WriteFile(outside, []byte("secret"), 0o600)
	if err := os.Symlink(outside, filepath.Join(dir, "link.txt")); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(FilesHandler(root))
	defer ts.Close()

	r, err := http.Get(ts.URL + "/home-network/3/report.html")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(r.Body)
	_ = r.Body.Close()
	if r.StatusCode != 200 || !strings.Contains(string(b), "Lease report") {
		t.Fatalf("page: %d %q", r.StatusCode, b)
	}
	for k, want := range map[string]string{
		"Content-Security-Policy": "sandbox allow-scripts",
		"X-Content-Type-Options":  "nosniff",
		"Cache-Control":           "no-store",
		"Referrer-Policy":         "no-referrer",
		"Content-Type":            "text/html",
	} {
		if !strings.HasPrefix(r.Header.Get(k), want) {
			t.Errorf("%s = %q, want %q…", k, r.Header.Get(k), want)
		}
	}
	for _, p := range []string{"/home-network/3/.env", "/home-network/3/link.txt", "/home-network/3/img",
		"/home-network/3", "/home-network/3/missing.html", "/home-network/3/../../../etc/passwd", "/"} {
		r, err := http.Get(ts.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		_ = r.Body.Close()
		if r.StatusCode != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", p, r.StatusCode)
		}
	}
	r, err = http.Post(ts.URL+"/home-network/3/report.html", "text/plain", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Body.Close()
	if r.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d", r.StatusCode)
	}
}
```

**Step 2: Run the tests and check that they fail.**

Run: `go test ./internal/serve/ -run 'ResolveFiles|FilesHandler'`
Expected: FAIL to compile with `undefined: ResolveFiles`

**Step 3: Write the implementation.** Create `internal/serve/files.go`:

```go
package serve

import (
	"net"
	"net/http"
	"os"
	"path"
	"strings"
)

// FilesPort is the artifact file server's port with files_listen = "auto".
const FilesPort = "7358"

// filesCSP runs a page's own scripts in an opaque origin: it can never call the bx API (whose
// same-origin guard refuses Origin "null"), read another origin's storage, or reach the
// listener on :7357 as same-origin.
const filesCSP = "sandbox allow-scripts allow-forms allow-popups allow-popups-to-escape-sandbox allow-modals"

// ResolveFiles turns serve.files_listen into the file server's host:port, or "" when it is
// "off". "auto" (or empty) is the API's host on FilesPort; an explicit address follows the same
// Tailscale-or-loopback rule as serve.listen.
func ResolveFiles(cfg, apiAddr string) (string, error) {
	switch cfg {
	case "off":
		return "", nil
	case "", "auto":
		host, _, err := net.SplitHostPort(apiAddr)
		if err != nil {
			return "", err
		}
		return net.JoinHostPort(host, FilesPort), nil
	}
	return ResolveListen(cfg)
}

// FilesHandler serves the session artifacts under root, read-only: GET or HEAD of
// /<project>/<num>/<file…>. Folders, dotfiles and anything outside root (by "..", or by a
// symlink, which os.Root refuses) are 404. It is the second listener of bx serve, on its own
// port, so the pages it serves are never same-origin with the API.
func FilesHandler(root string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "read-only", http.StatusMethodNotAllowed)
			return
		}
		rel := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		parts := strings.Split(rel, "/")
		if len(parts) < 3 {
			http.NotFound(w, r)
			return
		}
		for _, part := range parts {
			if strings.HasPrefix(part, ".") {
				http.NotFound(w, r)
				return
			}
		}
		rt, err := os.OpenRoot(root)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer func() { _ = rt.Close() }()
		f, err := rt.Open(rel)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer func() { _ = f.Close() }()
		fi, err := f.Stat()
		if err != nil || !fi.Mode().IsRegular() {
			http.NotFound(w, r)
			return
		}
		h := w.Header()
		h.Set("Content-Security-Policy", filesCSP)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Cache-Control", "no-store")
		h.Set("Referrer-Policy", "no-referrer")
		http.ServeContent(w, r, fi.Name(), fi.ModTime(), f)
	})
}
```

**Step 4: Run the tests and check that they pass.**

Run: `go test ./internal/serve/ -run 'ResolveFiles|FilesHandler'`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/serve/files.go internal/serve/files_test.go && git commit -m "feat(serve): read-only artifact file server"
```

---

### Task 8: List a session's artifacts in the API

**Files:**
- Modify: `internal/api/api.go` (imports lines 4–8; append the types after `UploadResult` ~line 230)
- Modify: `internal/serve/uploads.go` (move the num/project/session lookup into a shared helper)
- Create: `internal/serve/artifacts.go`
- Modify: `internal/serve/server.go:90` (route next to `/files`)
- Test: `internal/serve/artifacts_test.go`

**Step 1: Write the failing test.** Create `internal/serve/artifacts_test.go`:

```go
package serve

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dinhhuy258/bx/internal/api"
)

func TestListArtifacts(t *testing.T) {
	s, _, ts := newServer(t)
	s.App.ArtifactsURL = "http://100.64.0.1:7358"
	const path = "/api/projects/home-network/sessions/1/artifacts"
	got, code := get[api.ArtifactList](t, ts, path)
	if code != 200 || got.Files == nil || len(got.Files) != 0 {
		t.Fatalf("no folder yet: %d %+v", code, got)
	}
	dir := s.App.ArtifactsDir("home-network", 1)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"old.html", "new.html", "notes.txt", ".draft.html"} {
		_ = os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600)
	}
	hourAgo := time.Now().Add(-time.Hour)
	_ = os.Chtimes(filepath.Join(dir, "old.html"), hourAgo, hourAgo)
	got, code = get[api.ArtifactList](t, ts, path)
	if code != 200 || len(got.Files) != 2 || got.Files[0].Name != "new.html" || got.Files[1].Name != "old.html" {
		t.Fatalf("%d %+v", code, got)
	}
	if got.Files[0].URL != "http://100.64.0.1:7358/home-network/1/new.html" {
		t.Fatalf("url %q", got.Files[0].URL)
	}
	if _, code = get[api.ArtifactList](t, ts, "/api/projects/home-network/sessions/9/artifacts"); code != 404 {
		t.Fatalf("unknown session: %d", code)
	}
}
```

**Step 2: Run the test and check that it fails.**

Run: `go test ./internal/serve/ -run TestListArtifacts`
Expected: FAIL to compile with `undefined: api.ArtifactList`

**Step 3: Write the implementation.**

In `internal/api/api.go`, add `"time"` to the imports, and append after `UploadResult`:

```go
// Artifact is one HTML page in a session's artifacts folder. URL is "" when the file server
// is off.
type Artifact struct {
	Name     string    `json:"name"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
	URL      string    `json:"url,omitempty"`
}

// ArtifactList is GET /api/projects/{name}/sessions/{num}/artifacts, newest first.
type ArtifactList struct {
	Files []Artifact `json:"files"`
}
```

Replace `internal/serve/uploads.go` with this (the lookup moves into `session`, which the new route shares):

```go
package serve

import (
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	"github.com/dinhhuy258/bx/internal/api"
	"github.com/dinhhuy258/bx/internal/state"
	"github.com/dinhhuy258/bx/internal/uploads"
)

// maxUploadBody caps an upload request: one file of at most 20 MB, base64, in JSON.
// JSON keeps the same-origin guard's application/json rule without a route exception.
const maxUploadBody = 28 << 20

// session finds the project and session of a /sessions/{num}/ route, or writes a 404.
func (s *Server) session(w http.ResponseWriter, r *http.Request) (*state.Project, int, bool) {
	num, err := strconv.Atoi(r.PathValue("num"))
	if err != nil {
		httpError(w, http.StatusNotFound, fmt.Errorf("no session %q", r.PathValue("num")))
		return nil, 0, false
	}
	p, err := s.load(r.PathValue("name"))
	if err != nil {
		httpError(w, statusOf(err), err)
		return nil, 0, false
	}
	if p.Sessions[num] == nil {
		httpError(w, http.StatusNotFound, fmt.Errorf("no session #%d", num))
		return nil, 0, false
	}
	return p, num, true
}

// uploadFile saves one file sent from the phone for a session and replies with its path,
// which the web client puts in the message box for Claude to read.
func (s *Server) uploadFile(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBody)
	var req api.UploadRequest
	if !decodeSync(w, r, &req, "upload over 28 MB (one file of at most 20 MB)") {
		return
	}
	p, num, ok := s.session(w, r)
	if !ok {
		return
	}
	path, err := uploads.Save(s.Cfg.UploadsDir(), p.Name, num, req.Name, req.Content, time.Now())
	if err != nil {
		code := http.StatusInternalServerError
		switch {
		case errors.Is(err, uploads.ErrTooLarge):
			code = http.StatusRequestEntityTooLarge
		case errors.Is(err, uploads.ErrInvalid):
			code = http.StatusBadRequest
		}
		httpError(w, code, err)
		return
	}
	s.Log("upload: %s #%d %s (%d bytes)", p.Name, num, filepath.Base(path), len(req.Content))
	writeJSON(w, api.UploadResult{Path: path})
}
```

Create `internal/serve/artifacts.go`:

```go
package serve

import (
	"errors"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dinhhuy258/bx/internal/api"
)

// listArtifacts lists the .html pages at the top of a session's artifacts folder, newest
// first, with their file server links. No folder yet is an empty list.
func (s *Server) listArtifacts(w http.ResponseWriter, r *http.Request) {
	p, num, ok := s.session(w, r)
	if !ok {
		return
	}
	entries, err := os.ReadDir(s.App.ArtifactsDir(p.Name, num))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		httpError(w, http.StatusInternalServerError, err)
		return
	}
	link := s.App.ArtifactsLink(p.Name, num)
	files := []api.Artifact{}
	for _, e := range entries {
		name := e.Name()
		if !e.Type().IsRegular() || strings.HasPrefix(name, ".") || !strings.EqualFold(filepath.Ext(name), ".html") {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		f := api.Artifact{Name: name, Size: fi.Size(), Modified: fi.ModTime()}
		if link != "" {
			f.URL = link + "/" + url.PathEscape(name)
		}
		files = append(files, f)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Modified.After(files[j].Modified) })
	writeJSON(w, api.ArtifactList{Files: files})
}
```

In `internal/serve/server.go`, after the `POST /api/projects/{name}/sessions/{num}/files` line (~line 90), add:

```go
	mux.HandleFunc("GET /api/projects/{name}/sessions/{num}/artifacts", s.listArtifacts)
```

**Step 4: Run the tests and check that they pass.**

Run: `go test ./internal/serve/`
Expected: PASS (the upload tests too, unchanged)

**Step 5: Commit**

```bash
git add internal/api internal/serve && git commit -m "feat(serve): list a session's artifacts"
```

---

### Task 9: Wire it into `bx serve`

No new unit test. The pieces are tested in Tasks 4–8, and this task is the composition in `runServe`, which has no test today. Check it with the build and the manual run in Task 11.

**Files:**
- Modify: `internal/commands/serve.go`

**Step 1: Write the implementation.** Replace `runServe` in `internal/commands/serve.go`:

```go
func runServe(args []string, out, errOut io.Writer) int {
	cfg, a, logger, err := setup()
	if err != nil {
		fmt.Fprintln(errOut, "bx serve:", err)
		return 1
	}
	listen := cfg.Serve.Listen
	for i := 0; i < len(args); i++ {
		if args[i] == "--listen" && i+1 < len(args) {
			i++
			listen = args[i]
		}
	}
	addr, err := serve.ResolveListen(listen)
	if err != nil {
		fmt.Fprintln(errOut, "bx serve:", err)
		return 1
	}
	filesAddr, err := serve.ResolveFiles(cfg.Serve.FilesListen, addr)
	if err != nil {
		fmt.Fprintln(errOut, "bx serve: files_listen:", err)
		return 1
	}
	lock, err := state.Lock(filepath.Join(cfg.Root, "run", "serve"))
	if err != nil {
		fmt.Fprintln(errOut, "bx serve:", err)
		return 1
	}
	defer lock.Unlock()
	if err := tmux.WriteConf(cfg.TmuxConf()); err != nil {
		fmt.Fprintln(errOut, "bx serve:", err)
		return 1
	}
	// before ResumeAll: every session it starts gets the plugin and its artifacts folder
	if err := claudeplugin.Write(cfg.PluginDir()); err != nil {
		fmt.Fprintln(errOut, "bx serve: claude plugin:", err)
		return 1
	}
	if err := os.MkdirAll(cfg.ArtifactsDir(), 0o700); err != nil {
		fmt.Fprintln(errOut, "bx serve:", err)
		return 1
	}
	a.PluginDir = cfg.PluginDir()
	if filesAddr != "" {
		a.ArtifactsURL = "http://" + filesAddr
	}
	log := func(format string, v ...any) {
		logger.Printf(format, v...)
		fmt.Fprintf(out, format+"\n", v...)
	}
	s := serve.New(a, cfg, claude.CLI{}, log)
	s.ResumeAll(context.Background()) // after a reboot: start every session that should be running
	s.KeepTmuxServer()                // a tmux server already running outlives its last session
	srv := &http.Server{Addr: addr, Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	var files *http.Server
	if filesAddr != "" {
		files = &http.Server{Addr: filesAddr, Handler: serve.FilesHandler(cfg.ArtifactsDir()), ReadHeaderTimeout: 10 * time.Second}
		go func() {
			if err := files.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log("bx serve: artifacts file server: %v", err)
			}
		}()
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go s.Watch(ctx, serve.WatchEvery) // a session whose Claude exits becomes done
	go func() {
		<-ctx.Done()
		log("bx serve: shutting down (Claude sessions keep running in tmux)")
		s.Shutdown()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if files != nil {
			_ = files.Shutdown(shutdownCtx)
		}
		_ = srv.Shutdown(shutdownCtx)
	}()
	log("bx serve: listening on http://%s (Tailscale only, no login) · tmux -S %s", addr, cfg.TmuxSocket())
	if filesAddr != "" {
		log("bx serve: artifacts on http://%s (read-only, %s)", filesAddr, cfg.ArtifactsDir())
	}
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintln(errOut, "bx serve:", err)
		return 1
	}
	return 0
}
```

Add `"os"` and `"github.com/dinhhuy258/bx/internal/claudeplugin"` to the imports.

**Step 2: Build and test.**

Run: `go build ./... && go test ./...`
Expected: PASS

**Step 3: Commit**

```bash
git add internal/commands/serve.go && git commit -m "feat(serve): run the artifact file server and give sessions the plugin"
```

---

### Task 10: Web client — "Artifacts" in the session menu

**Files:**
- Modify: `internal/web/static/js/api.js` (the `api` object, after `upload`)
- Modify: `internal/web/static/js/sessionmenu.js` (`sessionMenu`, before the Rename item)

**Step 1: Write the implementation.**

In `api.js`, after the `upload:` entry, add:

```js
  // the session's HTML pages, newest first: [{ name, size, modified, url }]
  artifacts: (p, num) => call('GET', `/api/projects/${enc(p)}/sessions/${num}/artifacts`),
```

In `sessionmenu.js`, change the first import to `import { api } from './api.js';` (it's already imported, so check). Add this item just before `items.push({ label: 'Rename', …`:

```js
  items.push({ label: 'Artifacts', run: async () => {
    let files;
    try {
      ({ files } = await api.artifacts(project, num));
    } catch (e) {
      toast(e.message);
      return;
    }
    if (!files.length) { toast('No artifacts yet. Ask Claude for a page.'); return; }
    // the tap on a row opens the page: window.open runs inside that click, so it is not blocked
    sheet('Artifacts', files.map((f) => ({ label: f.name, run: () => {
      if (f.url) window.open(f.url, '_blank', 'noopener');
      else toast('The file server is off (serve.files_listen = "off").');
    } })));
  } });
```

**Step 2: Check the syntax.**

Run: `node --check internal/web/static/js/sessionmenu.js && node --check internal/web/static/js/api.js && go test ./internal/web/`
Expected: no output from node, then PASS

**Step 3: Commit**

```bash
git add internal/web && git commit -m "feat(web): open a session's artifacts from its menu"
```

---

### Task 11: TUI — `v` lists the selected session's artifacts

The TUI runs on the Mac, which is on the tailnet, so it opens the page in the Mac's browser. Press `v` on a session row to open a picker of its pages, newest first. Enter opens the chosen page with `open` (`xdg-open` on Linux) and shows its link in the key bar, so you can still cmd-click it if no browser opened, for example over ssh.

**Files:**
- Modify: `internal/client/api.go` (after `CleanupApply` ~line 155)
- Modify: `internal/client/model.go`:
  - the `Backend` interface (~line 32)
  - the message types (~line 99)
  - `pickKind` (~line 118)
  - the `Model` fields (~line 137)
  - `NewModel` (~line 208)
  - `Update` (~line 403)
  - `listKey` (~line 776)
  - `pickerKey` (~line 1114)
- Modify: `internal/config/config.go:60-67` (default key)
- Modify: `internal/ui/keybar.go` (`labels` ~line 22, session keys ~line 93)
- Test: `internal/client/model_test.go` (the fake backend ~line 127, plus a new test after `TestCleanupChecklistAndConfirm`)

**Step 1: Write the failing test.**

In `internal/client/model_test.go`, add a field to `fakeBackend`:

```go
	artifacts api.ArtifactList // the reply of every artifacts list
```

and after `func (f *fakeBackend) CleanupApply`, add:

```go
func (f *fakeBackend) Artifacts(_ context.Context, project string, num int) (api.ArtifactList, error) {
	f.reads = append(f.reads, fmt.Sprintf("artifacts %s %d", project, num))
	return f.artifacts, nil
}
```

After `TestCleanupChecklistAndConfirm`, add:

```go
func TestArtifactsPickerOpensThePage(t *testing.T) {
	h := start(t, 120, 30, "idle")
	var browsed []string
	h.m.browse = func(u string) tea.Cmd { browsed = append(browsed, u); return nil }
	if bar := ansi.Strip(h.m.bar()); !strings.Contains(bar, "v artifacts") {
		t.Fatalf("bar = %q", bar)
	}
	h.press("v")
	if !strings.Contains(h.m.View().Content, "no artifacts yet") {
		t.Fatal(h.m.View().Content)
	}
	const report = "http://100.64.0.1:7358/home-network/1/report.html"
	h.be.artifacts = api.ArtifactList{Files: []api.Artifact{
		{Name: "report.html", URL: report},
		{Name: "old.html", URL: "http://100.64.0.1:7358/home-network/1/old.html"},
	}}
	h.press("v")
	v := h.m.View().Content
	for _, want := range []string{"artifacts · [1]", "report.html", "old.html"} {
		if !strings.Contains(v, want) {
			t.Fatalf("no %q in\n%s", want, v)
		}
	}
	h.press("Enter")
	if fmt.Sprint(browsed) != "["+report+"]" {
		t.Fatalf("browsed = %v", browsed)
	}
	if !strings.Contains(h.m.View().Content, report) {
		t.Fatal(h.m.View().Content)
	}
	if got := fmt.Sprint(h.be.reads); got != "[artifacts home-network 1 artifacts home-network 1]" {
		t.Fatalf("reads = %s", got)
	}
}

func TestArtifactsWithFileServerOff(t *testing.T) {
	h := start(t, 120, 30, "idle")
	h.m.browse = func(string) tea.Cmd { t.Fatal("browsed with no link"); return nil }
	h.be.artifacts = api.ArtifactList{Files: []api.Artifact{{Name: "report.html"}}}
	h.press("v", "Enter")
	if !strings.Contains(h.m.View().Content, "files_listen") {
		t.Fatal(h.m.View().Content)
	}
}
```

(`start` selects session 1 of story s1 first, which is the same row the delete tests use. If the bar check fails because the cursor is on the story row, press `j` first.)

**Step 2: Run the tests and check that they fail.**

Run: `go test ./internal/client/ -run Artifacts`
Expected: FAIL to compile with `h.m.browse undefined`

**Step 3: Write the implementation.**

In `internal/client/api.go`, after `CleanupApply`, add:

```go
// Artifacts is GET /api/projects/{name}/sessions/{num}/artifacts: the session's HTML pages,
// newest first, with their file server links.
func (c *API) Artifacts(ctx context.Context, project string, num int) (api.ArtifactList, error) {
	var out api.ArtifactList
	path := fmt.Sprintf("/api/projects/%s/sessions/%d/artifacts", url.PathEscape(project), num)
	return out, c.call(ctx, pollTimeout, http.MethodGet, path, nil, &out)
}
```

In `internal/client/model.go`:

1. Add this line to the `Backend` interface, after `CleanupApply`:

   ```go
   	Artifacts(ctx context.Context, project string, num int) (api.ArtifactList, error)
   ```

2. In the message `type ( … )` block, after `cleanedMsg`, add:

   ```go
   	artifactsMsg struct {
   		num  int
   		list api.ArtifactList
   		err  error
   	}
   ```

3. In the `pickKind` constants, after `pickOverrideBranch`, add:

   ```go
   	pickArtifact        // a page of the selected session, opened in the browser
   ```

4. In the `Model` struct, next to `picker`/`pickKind`, add:

   ```go
   	artifactURLs map[string]string     // the artifact picker's links, by file name
   	browse       func(url string) tea.Cmd // opens a link in the local browser; tests replace it
   ```

5. In `NewModel`, add `browse: openBrowser` to the struct literal (next to `tmux: tmuxSelect`).

6. After `tmuxSelect`, add:

   ```go
   // openBrowser opens u in this machine's browser: `open` on macOS, `xdg-open` elsewhere. A
   // failure is quiet, because the key bar shows the link anyway.
   func openBrowser(u string) tea.Cmd {
   	return func() tea.Msg {
   		name := "xdg-open"
   		if runtime.GOOS == "darwin" {
   			name = "open"
   		}
   		_ = exec.Command(name, u).Run()
   		return nil
   	}
   }
   ```

   (Add `"runtime"` to the imports.)

7. In `Update`, after the `case cleanedMsg:` block, add:

   ```go
   	case artifactsMsg:
   		m.gotArtifacts(msg)
   		return m, nil
   ```

8. In the list key switch, after `case "cleanup":`, add:

   ```go
   	case "artifacts":
   		if sess != nil {
   			return m, m.loadArtifacts(num)
   		}
   ```

9. In `pickerKey`, inside `case ui.PickChoose:` → `switch kind {`, after `case pickCleanup:`, add:

   ```go
   		case pickArtifact:
   			u := m.artifactURLs[choice]
   			if u == "" {
   				m.setFlash(`no link: the artifacts file server is off (serve.files_listen = "off")`)
   				return nil
   			}
   			m.setFlash(u)
   			return m.browse(u)
   ```

10. After `gotCleanup`, add:

    ```go
    // loadArtifacts asks the server for session num's pages.
    func (m *Model) loadArtifacts(num int) tea.Cmd {
    	be, ctx, project := m.be, m.ctx, m.project
    	return func() tea.Msg {
    		list, err := be.Artifacts(ctx, project, num)
    		return artifactsMsg{num, list, err}
    	}
    }

    // gotArtifacts opens the picker of a session's pages, newest first.
    func (m *Model) gotArtifacts(msg artifactsMsg) {
    	if msg.err != nil {
    		m.setFlash("error: " + msg.err.Error())
    		return
    	}
    	if len(msg.list.Files) == 0 {
    		m.setFlash("no artifacts yet · ask Claude for an HTML page")
    		return
    	}
    	names := make([]string, 0, len(msg.list.Files))
    	m.artifactURLs = map[string]string{}
    	for _, f := range msg.list.Files {
    		names = append(names, f.Name)
    		m.artifactURLs[f.Name] = f.URL
    	}
    	m.picker, m.pickKind = ui.NewPicker(fmt.Sprintf("artifacts · [%d]", msg.num), names), pickArtifact
    }
    ```

In `internal/config/config.go`, in `Default`'s `List` map, add `"artifacts": {"v"},` to the `"settings"…"cleanup"` line.

In `internal/ui/keybar.go`, add `"artifacts": "artifacts",` to `labels`, and change the session row's `parts = append(parts, lk("rename"), lk("delete"))` to:

```go
		parts = append(parts, lk("rename"), lk("delete"), lk("artifacts"))
```

**Step 4: Run the tests and check that they pass.**

Run: `go test ./internal/client/ ./internal/ui/ ./internal/config/`
Expected: PASS. The other key bar tests use `Contains`, so the extra key doesn't break them.

**Step 5: Commit**

```bash
git add internal/client internal/config internal/ui && git commit -m "feat(client): open a session's artifacts from the TUI"
```

---

### Task 12: Verification

**Step 1: The whole suite.**

Run: `make test && make lint`
Expected: PASS, with no vet findings

**Step 2: Deploy.** Ask the user first, because this restarts the live `bx serve`. Claude sessions keep running.

Run: `make deploy`

**Step 3: Check on the server** (`ssh` into the bx container):

```bash
ls ~/.bx/claude-plugin/skills/artifact/SKILL.md        # written at start
journalctl --user -u bx -n 5 | grep artifacts          # "artifacts on http://100.x.y.z:7358"
```

**Step 4: End to end.** Delegate the browser parts to the `browser-verifier` agent.

1. Start a new session from the phone web client. In its pane, run `/skills`: it shows `bx:artifact`.
2. Ask: "make me an HTML page summarising this repo's README". Check:
   - Claude loads `bx:artifact`, and then `artifact-design`.
   - No permission prompt appears for the write.
   - The file is at `~/.bx/artifacts/<project>/<num>/<name>.html`.
   - `git status` in the worktree is clean.
   - Claude's reply ends with a `http://100.x.y.z:7358/...` link.
3. On the phone, open the session menu, then **Artifacts**, then tap the page. It opens in Safari, renders in both themes and fits the phone width.
   On the Mac, run `bx`, select the session and press `v`. The picker lists the page, and Enter opens it in the browser with its link in the key bar.
4. `curl -sI http://100.x.y.z:7358/<project>/<num>/<name>.html` shows the CSP sandbox, nosniff and no-store headers. `curl -s -o /dev/null -w '%{http_code}' http://100.x.y.z:7358/<project>/<num>/../../../config.toml` returns `404`.
5. `curl -s -X POST -H 'Origin: null' -H 'Content-Type: application/json' -d '{}' http://100.x.y.z:7357/api/projects/<project>/stop` returns `403`.
6. Stop the session: the folder stays. Resume it, ask for a change to the page, and check the same file is edited. Delete the session: `~/.bx/artifacts/<project>/<num>` is gone.

**Step 5:** Report the results to the user. Don't merge or push without their go-ahead.
