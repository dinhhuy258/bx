# MCP Settings Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use build to implement this plan task-by-task.

**Goal:** bx gives every new or resumed Claude session a set of MCP servers. The set combines global servers and per-project servers, and you manage both from a new **MCP** section on the TUI settings screen by editing them in `$EDITOR`.

**Architecture:** A new `internal/mcp` package parses, checks, merges and encodes servers in Claude Code's `.mcp.json` shape. The global servers live in `~/.bx/mcp.json`. Each project's servers live in its state file, in an untyped `mcp_servers` field (`json.RawMessage`), so they go away with the project. At launch, `App.options` merges global and project servers (project wins on a shared name) into `~/.bx/run/mcp/<project>/<num>/mcp.json` and passes `--mcp-config=<file>`. In `strict` mode it also passes `--strict-mcp-config`. Three TUI-only routes (`GET /api/mcp`, `GET`/`PUT /api/mcp/doc`) feed the settings screen. The client writes a document to a private temp file, hands the terminal to `$EDITOR` with `tea.ExecProcess`, and sends the result back. The server checks it before saving.

**Tech Stack:** Go 1.26 (`encoding/json`, `os`), Bubble Tea v2 (`tea.ExecProcess`), Claude Code CLI flags `--mcp-config` and `--strict-mcp-config`, plain ES module for the web settings screen.

## Decisions (from the 2026-11-03 conversation; design page: `http://100.86.43.122:7358/bx/4/mcp-settings.html`)

- **Mode:** a setting `mcp.mode` with the values `merge` (default), `strict` and `off`. `merge` passes `--mcp-config`. `strict` also passes `--strict-mcp-config`, even with no servers, so a session sees none of the user's own servers. `off` passes nothing.
- **Storage:** global servers go in `~/.bx/mcp.json` as a plain `{"mcpServers": {…}}` file, mode 600. Project servers go in `~/.bx/state/<project>.json` under `mcp_servers`. That is the inner object, stored untyped, so a value with the wrong shape never stops the project from loading. Deleting a project's state file removes its servers.
- **Per-session file:** `~/.bx/run/mcp/<project>/<num>/mcp.json`, built with `uploads.MakeDir`. `removeSessionFiles` deletes it along with uploads and artifacts.
- **Resume:** a resumed session reads the servers again. `Resume` already rebuilds the options.
- **Editing:** the client pauses the TUI and opens `$EDITOR` (`vi` when unset) on a temp file. Quitting with no change sends nothing. The server rejects invalid JSON with an error message and a line number. The client keeps the rejected text as a draft: ⏎ reopens it at that line (vi, vim, nvim, nano), and ⌫ discards it. Closing the settings screen drops drafts. There is no new bx command.
- **New project template:** `{"mcpServers": {}}`. JSON has no comments, and example servers would become real ones if left in.
- **Web client:** the Mode field is shown but disabled. The new `Field.WebReadOnly` flag sends `locked`, and `PUT /api/settings` without `?tui=1` refuses the key. The server lists never reach the web client. The `/api/mcp*` routes require `?tui=1`, and `GET /api/projects/{name}` strips `mcp_servers`.
- **Section order in the TUI:** Claude · Git · MCP · Notifications. `Settings.Load` already inserts Git right after Claude.
- **Out of scope:** server health checks and a form editor.

## Conventions for every task

- Run all commands from the repository root: `cd ~/Workspace/bx` on the Mac, or the worktree you build in.
- Tests: `go test ./internal/<pkg>/ -run <Name> -v`. After each task, `go test ./...` must pass.
- Commit after each task: `git add -A && git commit -m "<message>"`. The messages are given in each task. End each message with the `Co-Authored-By` line from the session.

---

### Task 0: Branch

```bash
git status --short     # expect a clean tree apart from this plan; stop and ask if not
git switch feat/mcp-setting 2>/dev/null || git switch -c feat/mcp-setting master
git add docs/plans/2026-11-03-mcp-settings-plan.md
git commit -m "docs: MCP settings plan"
```

Do not push or merge.

---

### Task 1: `internal/mcp` — parse, check, merge, encode

**Files:**
- Create: `internal/mcp/mcp.go`
- Test: `internal/mcp/mcp_test.go`

**Step 1: Write the failing test** — `internal/mcp/mcp_test.go`:

```go
package mcp

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const doc = `{
  "mcpServers": {
    "github": {"command": "npx", "args": ["-y", "@modelcontextprotocol/server-github"], "env": {"GITHUB_TOKEN": "x"}},
    "sentry": {"type": "http", "url": "https://mcp.sentry.dev/mcp", "timeout": 30}
  }
}`

func TestParseKeepsUnknownFields(t *testing.T) {
	s, err := Parse([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if len(s) != 2 || !strings.Contains(string(s["sentry"]), `"timeout": 30`) {
		t.Fatalf("servers = %s", s)
	}
	got := Summaries(s)
	if len(got) != 2 || got[0] != (Server{"github", "stdio", "npx -y @modelcontextprotocol/server-github"}) ||
		got[1] != (Server{"sentry", "http", "https://mcp.sentry.dev/mcp"}) {
		t.Fatalf("summaries = %+v", got)
	}
}

func TestParseEmpty(t *testing.T) {
	for _, in := range []string{"", "  \n", "{}", `{"mcpServers": {}}`} {
		if s, err := Parse([]byte(in)); err != nil || s != nil {
			t.Errorf("Parse(%q) = %v, %v", in, s, err)
		}
	}
}

func TestParseErrorsHaveALine(t *testing.T) {
	for _, c := range []struct{ in, want string; line int }{
		{"{\n  \"mcpServers\": {\n    \"a\": {\"command\": \"x\"}\n    \"b\": {}\n  }\n}", "line 4, column 5: invalid character '\"' after object key:value pair", 4},
		{"{\"mcpServers\": {\n\"a\": {\"command\": \"x\", \"args\": \"npx\"}}}", "line 2, column 1: a: args must be a list of strings", 2},
		{"{\"mcpServers\": {\"a\": {\"args\": []}}}", "line 1, column 17: a: needs a command, or a url with \"type\": \"http\"", 1},
		{"{\"mcpServers\": {\"a\": {\"type\": \"ws\", \"url\": \"x\"}}}", "line 1, column 17: a: type \"ws\": want stdio, http or sse", 1},
		{"{\"mcpServers\": {\"a\": {\"type\": \"http\"}}}", "line 1, column 17: a: needs a url", 1},
		{"{\"mcpServer\": {}}", `unknown key "mcpServer": want "mcpServers"`, 0},
		{"{\"mcpServers\": {}} x", "line 1, column 20: unexpected text after the closing }", 1},
		{"{\"mcpServers\": {", "the text ends early: a } or ] is missing", 0},
		{"[]", `want an object: {"mcpServers": {…}}`, 0},
	} {
		_, err := Parse([]byte(c.in))
		var e *Error
		if !errors.As(err, &e) || err.Error() != c.want || e.Line != c.line {
			t.Errorf("Parse(%q)\n got %v (%+v)\nwant %s (line %d)", c.in, err, e, c.want, c.line)
		}
	}
}

func TestDecodeStoredField(t *testing.T) {
	if s, err := Decode(nil); err != nil || s != nil {
		t.Fatalf("nil = %v %v", s, err)
	}
	if _, err := Decode(json.RawMessage(`"npx"`)); err == nil || err.Error() != `mcp_servers: want an object of servers` {
		t.Fatalf("string: %v", err)
	}
	if _, err := Decode(json.RawMessage(`{"a": {"command": "x", "args": "y"}}`)); err == nil || err.Error() != "a: args must be a list of strings" {
		t.Fatalf("bad args: %v", err)
	}
}

func TestMergeProjectWins(t *testing.T) {
	g := Servers{"github": json.RawMessage(`{"command":"g"}`), "postgres": json.RawMessage(`{"command":"dev"}`)}
	p := Servers{"postgres": json.RawMessage(`{"command":"bx"}`)}
	m, replaced := Merge(g, p)
	if len(m) != 2 || string(m["postgres"]) != `{"command":"bx"}` || strings.Join(replaced, ",") != "postgres" {
		t.Fatalf("merged %s, replaced %v", m, replaced)
	}
	if m, _ := Merge(nil, nil); len(m) != 0 {
		t.Fatalf("empty merge = %v", m)
	}
}

func TestEncodeRoundTrips(t *testing.T) {
	if got := string(Encode(nil)); got != "{\n  \"mcpServers\": {}\n}\n" {
		t.Fatalf("Encode(nil) = %q", got)
	}
	s, _ := Parse([]byte(doc))
	back, err := Parse(Encode(s))
	if err != nil || len(back) != 2 {
		t.Fatalf("round trip: %v %v", back, err)
	}
}

func TestWriteAndReadFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "mcp.json")
	if s, err := ReadFile(p); err != nil || s != nil {
		t.Fatalf("missing file = %v %v", s, err)
	}
	if err := WriteFile(p, []byte(doc)); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, want 0600: it may hold tokens", fi.Mode())
	}
	if s, err := ReadFile(p); err != nil || len(s) != 2 {
		t.Fatalf("read back = %v %v", s, err)
	}
}
```

**Step 2: Run it.** `go test ./internal/mcp/ -v`. Expected: FAIL (`package … is not in std` / undefined: Parse).

**Step 3: Write `internal/mcp/mcp.go`:**

```go
// Package mcp reads, checks, merges and writes the MCP servers bx gives Claude sessions: the
// global ~/.bx/mcp.json and each project's mcp_servers. Both use Claude Code's .mcp.json shape,
// {"mcpServers": {"<name>": {...}}}. A server is kept as written, so fields bx does not check
// (timeouts, oauth, ...) reach Claude unchanged.
package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

// Servers maps a server name to its config, as written.
type Servers map[string]json.RawMessage

// Server is one server as the settings screen lists it. Type is stdio, http or sse; Target is
// the command line or the URL.
type Server struct {
	Name, Type, Target string
}

// Error is a document that does not parse or check. Line and Col are 1-based; 0 when the
// place is unknown.
type Error struct {
	Line, Col int
	Msg       string
}

func (e *Error) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("line %d, column %d: %s", e.Line, e.Col, e.Msg)
	}
	return e.Msg
}

// known are the fields bx checks.
type known struct {
	Type    string            `json:"type"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}

// Parse reads a document. Blank text, {} and an empty mcpServers are no servers: nil.
func Parse(data []byte) (Servers, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, nil
	}
	var doc struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return nil, located(data, err)
	}
	if _, err := dec.Token(); err != io.EOF {
		line, col := lineCol(data, int(dec.InputOffset()))
		return nil, &Error{Line: line, Col: col, Msg: "unexpected text after the closing }"}
	}
	s, err := Check(doc.MCPServers)
	var e *Error
	if errors.As(err, &e) && e.Line == 0 {
		if name, _, ok := strings.Cut(e.Msg, ": "); ok {
			if i := bytes.Index(data, []byte(strconv.Quote(name))); i >= 0 {
				e.Line, e.Col = lineCol(data, i)
			}
		}
	}
	return s, err
}

// Decode checks a project's stored mcp_servers, the inner object. nil is no servers.
func Decode(raw json.RawMessage) (Servers, error) {
	if len(bytes.TrimSpace(raw)) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, &Error{Msg: "mcp_servers: want an object of servers"}
	}
	return Check(m)
}

// Check checks every server, by name. An error starts with the server's name.
func Check(m map[string]json.RawMessage) (Servers, error) {
	if len(m) == 0 {
		return nil, nil
	}
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		if strings.TrimSpace(n) == "" {
			return nil, &Error{Msg: "a server needs a name"}
		}
		var k known
		if err := json.Unmarshal(m[n], &k); err != nil {
			var te *json.UnmarshalTypeError
			if errors.As(err, &te) && te.Field != "" {
				return nil, &Error{Msg: fmt.Sprintf("%s: %s must be %s", n, te.Field, kind(te.Type))}
			}
			return nil, &Error{Msg: n + ": want an object"}
		}
		switch k.Type {
		case "", "stdio":
			if k.Command == "" {
				return nil, &Error{Msg: n + `: needs a command, or a url with "type": "http"`}
			}
		case "http", "sse":
			if k.URL == "" {
				return nil, &Error{Msg: n + ": needs a url"}
			}
		default:
			return nil, &Error{Msg: fmt.Sprintf("%s: type %q: want stdio, http or sse", n, k.Type)}
		}
	}
	return Servers(m), nil
}

func kind(t reflect.Type) string {
	switch t.Kind() {
	case reflect.Slice:
		return "a list of strings"
	case reflect.Map:
		return "an object of strings"
	}
	return "a string"
}

// located turns a decode error into an *Error with the line and column of its offset.
func located(data []byte, err error) error {
	var se *json.SyntaxError
	var te *json.UnmarshalTypeError
	switch {
	case errors.Is(err, io.ErrUnexpectedEOF):
		return &Error{Msg: "the text ends early: a } or ] is missing"}
	case errors.As(err, &se):
		line, col := lineCol(data, int(se.Offset)-1)
		return &Error{Line: line, Col: col, Msg: se.Error()}
	case errors.As(err, &te):
		if te.Field == "" {
			return &Error{Msg: `want an object: {"mcpServers": {…}}`}
		}
		return &Error{Msg: `mcpServers must be an object of servers`}
	case strings.HasPrefix(err.Error(), "json: unknown field "):
		return &Error{Msg: fmt.Sprintf("unknown key %s: want \"mcpServers\"", strings.TrimPrefix(err.Error(), "json: unknown field "))}
	}
	return &Error{Msg: err.Error()}
}

// lineCol is the 1-based line and column of byte off in data.
func lineCol(data []byte, off int) (int, int) {
	off = min(max(off, 0), len(data))
	line := 1 + bytes.Count(data[:off], []byte("\n"))
	return line, off - bytes.LastIndexByte(data[:off], '\n')
}

// Summaries lists the servers by name.
func Summaries(s Servers) []Server {
	out := make([]Server, 0, len(s))
	for n, raw := range s {
		var k known
		_ = json.Unmarshal(raw, &k)
		sv := Server{Name: n, Type: k.Type, Target: k.URL}
		if sv.Type == "" {
			sv.Type = "stdio"
		}
		if sv.Type == "stdio" {
			sv.Target = strings.Join(append([]string{k.Command}, k.Args...), " ")
		}
		out = append(out, sv)
	}
	slices.SortFunc(out, func(a, b Server) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// Merge is global with project on top. Replaced are the names both have, sorted.
func Merge(global, project Servers) (merged Servers, replaced []string) {
	merged = Servers{}
	for n, v := range global {
		merged[n] = v
	}
	for n, v := range project {
		if _, ok := merged[n]; ok {
			replaced = append(replaced, n)
		}
		merged[n] = v
	}
	slices.Sort(replaced)
	return merged, replaced
}

// Encode writes s as a document, indented by two spaces. nil is {"mcpServers": {}}.
func Encode(s Servers) []byte {
	if s == nil {
		s = Servers{}
	}
	b, _ := json.MarshalIndent(struct {
		MCPServers Servers `json:"mcpServers"`
	}{s}, "", "  ")
	return append(b, '\n')
}

// ReadFile parses the document at path. A missing file is no servers.
func ReadFile(path string) (Servers, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// WriteFile writes data to path atomically, readable only by its owner: a server's env may
// hold a token.
func WriteFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp)
	}
	return err
}
```

**Step 4: Run it.** `go test ./internal/mcp/ -v`. Expected: PASS. Two expected texts in `TestParseErrorsHaveALine` depend on Go itself: the wording of a `SyntaxError` (first case) and the column of the trailing-text case (`dec.InputOffset()` after a failed `Token`). If only those differ, update the expected text or column to what Go prints, and keep the line check. Change nothing else to make the test pass.

**Commit:** `feat(mcp): parse, check and merge MCP server documents`

---

### Task 2: State — `mcp_servers` on the project

**Files:**
- Modify: `internal/state/state.go` (`Project` ~line 22, `projectWire` ~line 118, `MarshalJSON` ~line 131, `UnmarshalJSON` ~line 150)
- Test: `internal/state/state_test.go` (append; create the file if it doesn't exist, `package state`)

**Step 1: Failing test:**

```go
func TestProjectKeepsMCPServers(t *testing.T) {
	p := New("bx", "/p")
	p.MCPServers = json.RawMessage(`{"a":{"command":"x","args":"wrong shape is still kept"}}`)
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var back Project
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("a wrong-shaped mcp_servers must not fail the project: %v", err)
	}
	if string(back.MCPServers) != string(p.MCPServers) {
		t.Fatalf("mcp_servers = %s", back.MCPServers)
	}
	if b, _ := json.Marshal(New("bx", "/p")); strings.Contains(string(b), "mcp_servers") {
		t.Fatalf("an empty field is left out: %s", b)
	}
}
```

(imports `encoding/json`, `strings`, `testing`)

**Step 2:** `go test ./internal/state/ -run TestProjectKeepsMCPServers -v`. Expected: FAIL (`p.MCPServers undefined`).

**Step 3: Implement.** In `Project`, after `LastType`:

```go
	// MCPServers are the project's own MCP servers, the inner object of a .mcp.json document,
	// stored untyped: a hand edit with the wrong shape never stops the project from loading
	// (see internal/mcp). New sessions get them on top of ~/.bx/mcp.json.
	MCPServers json.RawMessage `json:"-"`
```

In `projectWire`, after `LastType`:

```go
	MCPServers json.RawMessage `json:"mcp_servers,omitempty"`
```

In `MarshalJSON`, add `MCPServers: p.MCPServers,` to the `projectWire{…}` literal. In `UnmarshalJSON`, after `p.BaseOverride, p.LastType = …` add:

```go
	p.MCPServers = w.MCPServers
```

**Step 4:** `go test ./internal/state/ -v`. Expected: PASS.

**Commit:** `feat(state): keep a project's MCP servers in its state file`

---

### Task 3: Config — the MCP paths

**Files:**
- Modify: `internal/config/config.go` (after `PluginDir`, ~line 182)
- Test: `internal/config/config_test.go` (append)

**Step 1: Failing test:**

```go
func TestMCPPaths(t *testing.T) {
	c := Default("/r")
	if c.MCPFile() != "/r/mcp.json" || c.SessionMCPDir() != "/r/run/mcp" {
		t.Fatalf("%q %q", c.MCPFile(), c.SessionMCPDir())
	}
}
```

**Step 2:** `go test ./internal/config/ -run TestMCPPaths`. Expected: FAIL (undefined).

**Step 3:**

```go
// MCPFile is <root>/mcp.json: the MCP servers every new session gets.
func (c *Config) MCPFile() string { return filepath.Join(c.Root, "mcp.json") }

// SessionMCPDir is <root>/run/mcp: <project>/<num>/mcp.json is the --mcp-config bx wrote for
// that session.
func (c *Config) SessionMCPDir() string { return filepath.Join(c.Root, "run", "mcp") }
```

**Step 4:** `go test ./internal/config/`. Expected: PASS.

**Commit:** `feat(config): MCP file paths`

---

### Task 4: Settings — `mcp.mode`, read-only on the web

**Files:**
- Modify: `internal/settings/settings.go` (`Field` ~line 33, `Sections` ~line 52, `Visible` ~line 77, add `WebReadOnlyKey`)
- Modify: `internal/serve/server.go` (`putSettings` ~line 503)
- Modify: `internal/web/static/js/settings.js` (the `select`)
- Test: `internal/settings/settings_test.go`, `internal/serve/server_test.go` (append)

**Step 1: Failing tests.** In `internal/settings/settings_test.go`:

```go
func TestMCPModeIsLockedOnTheWeb(t *testing.T) {
	if err := Validate("mcp.mode", "strict"); err != nil {
		t.Fatal(err)
	}
	if err := Validate("mcp.mode", "on"); err == nil {
		t.Fatal("want an error for an unknown mode")
	}
	web, _ := Visible(false, nil)
	tui, _ := Visible(true, nil)
	find := func(secs []Section) (Field, bool) {
		for _, s := range secs {
			for _, f := range s.Fields {
				if f.Key == "mcp.mode" {
					return f, true
				}
			}
		}
		return Field{}, false
	}
	if f, ok := find(web); !ok || !f.Locked {
		t.Fatalf("web field = %+v %v, want locked", f, ok)
	}
	if f, _ := find(tui); f.Locked {
		t.Fatal("the TUI edits the mode")
	}
	if !WebReadOnlyKey("mcp.mode") || WebReadOnlyKey("claude.model") {
		t.Fatal("WebReadOnlyKey")
	}
}
```

In `internal/serve/server_test.go`:

```go
func TestMCPModeOnlyFromTheTUI(t *testing.T) {
	_, _, ts := newServer(t)
	if _, res, code := put(t, ts, "/api/settings", api.Settings{Values: map[string]string{"mcp.mode": "off"}}); code != 400 || res.Error != "mcp.mode can only be changed from the TUI" {
		t.Fatalf("web: %d %q", code, res.Error)
	}
	if out, _, code := put(t, ts, "/api/settings?tui=1", api.Settings{Values: map[string]string{"mcp.mode": "strict"}}); code != 200 || out.Values["mcp.mode"] != "strict" {
		t.Fatalf("tui: %d %v", code, out.Values)
	}
}
```

**Step 2:** `go test ./internal/settings/ ./internal/serve/ -run 'MCPMode' -v`. Expected: FAIL.

**Step 3: Implement.** In `Field`, after `TUIOnly`:

```go
	// WebReadOnly: the web client shows the field disabled (Locked) and may not change it.
	WebReadOnly bool `json:"-"`
	Locked      bool `json:"locked,omitempty"` // set by Visible for the web client
```

Update the `Field` doc comment's last sentence to: `A TUIOnly field and its value are left out of what the web client gets, and a WebReadOnly one is sent Locked (see Visible).`

In `Sections`, insert between the claude and notifications sections:

```go
}, {
	ID: "mcp", Title: "MCP", Help: "For new and resumed sessions. Running ones keep what they started with.",
	Fields: []Field{
		{Key: "mcp.mode", Label: "Mode", Kind: Choice, Default: "merge", WebReadOnly: true,
			Help:    "Mode: merge = your Claude servers + these · strict = only these (--strict-mcp-config) · off",
			Choices: []string{"merge", "strict", "off"}},
	},
```

In `Visible`, in the web loop, replace `fields = append(fields, f)` with:

```go
			f.Locked = f.WebReadOnly
			fields = append(fields, f)
```

After `TUIOnlyKey` add:

```go
// WebReadOnlyKey reports whether key is a WebReadOnly field.
func WebReadOnlyKey(key string) bool {
	f, ok := field(key)
	return ok && f.WebReadOnly
}
```

In `putSettings` replace `if settings.TUIOnlyKey(k) {` with `if settings.TUIOnlyKey(k) || settings.WebReadOnlyKey(k) {`.

In `settings.js`, change the `select` props to:

```js
        const sel = h('select', { disabled: !!f.locked, onchange: () => save(f.key, sel.value) },
```

**Step 4:** `go test ./internal/settings/ ./internal/serve/ ./internal/web/`. Expected: PASS.

The UI goldens change because a new section appears. That is fixed in Task 9. For now, check `go test ./internal/ui/` and expect only the golden tests and `TestSettingsGitSectionIsTheClients` to fail. In that test, change the expected string to `"claude Claude, git Git, mcp MCP, notifications Notifications"` and `len(s.sections) != 3` to `!= 4`. Then run `go test ./internal/ui/ -update` and check `git diff internal/ui/testdata`: the only change should be the new `MCP` line in the section lists. Commit the goldens with this task.

**Commit:** `feat(settings): mcp.mode, read-only on the web`

---

### Task 5: Claude flags

**Files:**
- Modify: `internal/claude/agents.go` (`Options` ~line 98, `StartArgs` ~line 116)
- Test: `internal/claude/agents_test.go` (append)

**Step 1: Failing test:**

```go
func TestStartArgsMCP(t *testing.T) {
	got := StartArgs("u", false, Options{MCPConfig: "/bx/run/mcp/p/1/mcp.json", StrictMCPConfig: true})
	want := []string{"claude", "--session-id", "u", "--mcp-config=/bx/run/mcp/p/1/mcp.json", "--strict-mcp-config"}
	if !equal(got, want) {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}
```

**Step 2:** `go test ./internal/claude/ -run TestStartArgsMCP`. Expected: FAIL (unknown field).

**Step 3:** In `Options` add:

```go
	MCPConfig       string // --mcp-config=<file>: one value, so the variadic flag never swallows a later argument
	StrictMCPConfig bool   // --strict-mcp-config: only the servers of MCPConfig
```

In `StartArgs`, right after the `for _, f := range [][2]string{…}` loop:

```go
	if o.MCPConfig != "" {
		args = append(args, "--mcp-config="+o.MCPConfig)
	}
	if o.StrictMCPConfig {
		args = append(args, "--strict-mcp-config")
	}
```

**Step 4:** `go test ./internal/claude/`. Expected: PASS. The existing tests are unchanged.

**Commit:** `feat(claude): --mcp-config and --strict-mcp-config`

---

### Task 6: App — merge at launch, save from the settings screen

**Files:**
- Create: `internal/app/mcp.go`
- Modify: `internal/app/sessions.go` (`options` ~line 97)
- Modify: `internal/app/artifacts.go` (`removeSessionFiles` ~line 47)
- Test: `internal/app/mcp_test.go`

**Step 1: Failing test** — `internal/app/mcp_test.go`:

```go
package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dinhhuy258/bx/internal/mcp"
	"github.com/dinhhuy258/bx/internal/settings"
)

const globalDoc = `{"mcpServers": {"github": {"command": "gh-mcp"}, "postgres": {"command": "pg", "args": ["dev"]}}}`

func TestSessionGetsMergedMCP(t *testing.T) {
	a, f, p := newApp(t)
	if err := a.SetGlobalMCP([]byte(globalDoc)); err != nil {
		t.Fatal(err)
	}
	if err := a.SetProjectMCP(p, []byte(`{"mcpServers": {"postgres": {"command": "pg", "args": ["bx"]}}}`)); err != nil {
		t.Fatal(err)
	}
	num, err := a.NewChat(p, "")
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(a.Cfg.SessionMCPDir(), p.Name, "1", "mcp.json")
	if got := calls(f); !strings.Contains(got, "--mcp-config="+file) || strings.Contains(got, "--strict-mcp-config") {
		t.Fatalf("flags:\n%s", got)
	}
	s, err := mcp.ReadFile(file)
	if err != nil || len(s) != 2 || !strings.Contains(string(s["postgres"]), `"bx"`) {
		t.Fatalf("merged file = %s %v", s, err)
	}
	if fi, _ := os.Stat(file); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode())
	}
	if err := a.DeleteSession(p, num, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("delete must remove the session's MCP file: %v", err)
	}
}

func TestMCPModes(t *testing.T) {
	a, f, p := newApp(t)
	var err error
	if a.Settings, err = settings.Open(filepath.Join(t.TempDir(), "settings.json"), func(string, ...any) {}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.NewChat(p, ""); err != nil { // merge, nothing set: no flag
		t.Fatal(err)
	}
	if strings.Contains(calls(f), "--mcp-config") {
		t.Fatalf("no servers, merge: no flag:\n%s", calls(f))
	}
	a.Settings.Set(map[string]string{"mcp.mode": "strict"})
	if _, err := a.NewChat(p, ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(calls(f), "--strict-mcp-config") {
		t.Fatalf("strict passes the flag even with no servers:\n%s", calls(f))
	}
	a.SetGlobalMCP([]byte(globalDoc))
	a.Settings.Set(map[string]string{"mcp.mode": "off"})
	f.Calls = nil
	if _, err := a.NewChat(p, ""); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(calls(f), "mcp-config") {
		t.Fatalf("off: no flag:\n%s", calls(f))
	}
}

func TestBadProjectMCPIsSkippedNotFatal(t *testing.T) {
	a, f, p := newApp(t)
	var logged []string
	a.Log = func(format string, args ...any) { logged = append(logged, format) }
	a.SetGlobalMCP([]byte(globalDoc))
	p.MCPServers = []byte(`{"x": {"command": "y", "args": "z"}}`) // a hand edit
	if _, err := a.NewChat(p, ""); err != nil {
		t.Fatalf("a bad project set must not stop the session: %v", err)
	}
	s, _ := mcp.ReadFile(filepath.Join(a.Cfg.SessionMCPDir(), p.Name, "1", "mcp.json"))
	if len(s) != 2 || len(logged) == 0 || !strings.Contains(calls(f), "--mcp-config=") {
		t.Fatalf("want the global servers only, and a log: %v %v", s, logged)
	}
}

func TestSetMCPRejectsAndClears(t *testing.T) {
	a, _, p := newApp(t)
	if err := a.SetProjectMCP(p, []byte(`{"mcpServers": {`)); err == nil {
		t.Fatal("bad JSON must be refused")
	}
	if p.MCPServers != nil {
		t.Fatal("a refused save leaves the project unchanged")
	}
	a.SetProjectMCP(p, []byte(`{"mcpServers": {"a": {"command": "x"}}}`))
	if err := a.SetProjectMCP(p, nil); err != nil || p.MCPServers != nil {
		t.Fatalf("empty text clears: %v %s", err, p.MCPServers)
	}
	a.SetGlobalMCP([]byte(globalDoc))
	if err := a.SetGlobalMCP([]byte(`{"mcpServers": {}}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(a.Cfg.MCPFile()); !os.IsNotExist(err) {
		t.Fatal("no global servers removes the file")
	}
}
```

**Step 2:** `go test ./internal/app/ -run 'MCP' -v`. Expected: FAIL (undefined: SetGlobalMCP).

**Step 3: Write `internal/app/mcp.go`:**

```go
package app

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/dinhhuy258/bx/internal/mcp"
	"github.com/dinhhuy258/bx/internal/state"
	"github.com/dinhhuy258/bx/internal/uploads"
)

// mcpMode is the mcp.mode setting: merge (the default), strict or off.
func (a *App) mcpMode() string {
	if v := a.Settings.Get("mcp.mode"); v != "" {
		return v
	}
	return "merge"
}

// sessionMCP writes the --mcp-config of session num: the global servers with p's on top, and
// reports whether only they may load (strict). It returns "" when the mode is off, or nothing
// is set in merge mode. A set that fails its check is left out and logged, and a file that can't
// be written gives no flag: the session still starts.
func (a *App) sessionMCP(p *state.Project, num int) (path string, strict bool) {
	mode := a.mcpMode()
	if mode == "off" {
		return "", false
	}
	global, err := mcp.ReadFile(a.Cfg.MCPFile())
	if err != nil {
		a.logf("mcp: %s: %v · skipped", a.Cfg.MCPFile(), err)
		global = nil
	}
	own, err := mcp.Decode(p.MCPServers)
	if err != nil {
		a.logf("mcp: %s: %v · skipped", p.Name, err)
		own = nil
	}
	merged, _ := mcp.Merge(global, own)
	strict = mode == "strict"
	if len(merged) == 0 && !strict {
		return "", false
	}
	dir, err := uploads.MakeDir(a.Cfg.SessionMCPDir(), p.Name, num)
	if err != nil {
		a.logf("mcp: %s [%d]: %v · no MCP servers", p.Name, num, err)
		return "", false
	}
	path = filepath.Join(dir, "mcp.json")
	if err := mcp.WriteFile(path, mcp.Encode(merged)); err != nil {
		a.logf("mcp: %s [%d]: %v · no MCP servers", p.Name, num, err)
		return "", false
	}
	return path, strict
}

// SetGlobalMCP checks text and saves it as ~/.bx/mcp.json; no servers removes the file. A
// refused text leaves the file as it was.
func (a *App) SetGlobalMCP(text []byte) error {
	s, err := mcp.Parse(text)
	if err != nil {
		return err
	}
	if len(s) == 0 {
		if err := os.Remove(a.Cfg.MCPFile()); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	return mcp.WriteFile(a.Cfg.MCPFile(), mcp.Encode(s))
}

// SetProjectMCP checks text and saves its servers in p's state file; no servers clears them.
// A refused text leaves p as it was.
func (a *App) SetProjectMCP(p *state.Project, text []byte) error {
	s, err := mcp.Parse(text)
	if err != nil {
		return err
	}
	var raw json.RawMessage
	if len(s) > 0 {
		if raw, err = json.Marshal(s); err != nil {
			return err
		}
	}
	p.MCPServers = raw
	return a.SaveProject(p)
}
```

`settings.Store.Get` is nil-safe, so `a.Settings == nil` (in tests) gives `merge`.

In `sessions.go` `options`, just before `o.Settings = settingsJSON(hooks, perms)`:

```go
	o.MCPConfig, o.StrictMCPConfig = a.sessionMCP(p, num)
```

Update the doc comment of `options` and add the sentence: `Every session also gets the MCP servers of its project and of ~/.bx/mcp.json (see mcp.go).`

In `artifacts.go` `removeSessionFiles`, change the loop to:

```go
	for _, base := range []string{a.Cfg.UploadsDir(), a.Cfg.ArtifactsDir(), a.Cfg.SessionMCPDir()} {
```

and add `and MCP config` to its doc comment: `deletes session num's uploads, artifacts and MCP config.`

**Step 4:** `go test ./internal/app/ -v`. Expected: PASS. Existing launch assertions are unchanged, because with no servers and `merge` no flag is added.

**Commit:** `feat(app): give sessions their merged MCP servers`

---

### Task 7: API and server routes

**Files:**
- Create: `internal/ui/mcp.go` (the shared shapes; `api` aliases them, as it does `ui.Shell`)
- Modify: `internal/api/api.go` (aliases, `MCPDoc`, `Result.Line`)
- Create: `internal/serve/mcp.go`
- Modify: `internal/serve/server.go` (routes ~line 69; `getProject` ~line 550)
- Test: `internal/serve/mcp_test.go`

**Step 1: Create `internal/ui/mcp.go`.** It contains only types for now; Task 9 adds the screen:

```go
package ui

// MCPServer is one MCP server on the settings screen: Type stdio, http or sse; Target its
// command line or URL.
type MCPServer struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Target string `json:"target"`
}

// MCPSet is the servers of one scope. Error says why they are skipped at launch: a hand edit
// that fails the check.
type MCPSet struct {
	Servers []MCPServer `json:"servers"`
	Error   string      `json:"error,omitempty"`
}

// MCPProject is a project that has its own servers.
type MCPProject struct {
	Project string `json:"project"`
	MCPSet
}

// MCP is what the settings screen shows: the global servers and every project with its own,
// by name.
type MCP struct {
	Global   MCPSet       `json:"global"`
	Projects []MCPProject `json:"projects"`
}
```

**Step 2: API.** In `internal/api/api.go`, after the `Shell` alias:

```go
// MCP is GET /api/mcp?tui=1: the global MCP servers and every project with its own. Only the
// TUI may ask: a server's config may hold a token.
type MCP = ui.MCP

// MCPDoc is GET and PUT /api/mcp/doc?tui=1&project=<name> ("" = the global servers): the
// servers as the .mcp.json text the TUI edits. PUT with empty text removes them. A refused PUT
// is a 400 whose Result has the error and its Line.
type MCPDoc struct {
	Text string `json:"text"`
}
```

In `Result`, after `Branch`:

```go
	// Line is the line of a refused MCP document (PUT /api/mcp/doc), 0 when unknown.
	Line int `json:"line,omitempty"`
```

**Step 3: Failing test** — `internal/serve/mcp_test.go`:

```go
package serve

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dinhhuy258/bx/internal/api"
)

func putDoc(t *testing.T, ts *httptest.Server, path, text string) (api.Result, int) {
	t.Helper()
	b, _ := json.Marshal(api.MCPDoc{Text: text})
	req, _ := http.NewRequest(http.MethodPut, ts.URL+path, bytes.NewReader(b))
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	var res api.Result
	_ = json.NewDecoder(r.Body).Decode(&res)
	return res, r.StatusCode
}

func TestMCPRoutesAreTUIOnly(t *testing.T) {
	_, _, ts := newServer(t)
	for _, path := range []string{"/api/mcp", "/api/mcp/doc"} {
		if _, code := get[api.MCP](t, ts, path); code != http.StatusForbidden {
			t.Errorf("GET %s from the web = %d", path, code)
		}
	}
	if _, code := putDoc(t, ts, "/api/mcp/doc", "{}"); code != http.StatusForbidden {
		t.Errorf("PUT from the web = %d", code)
	}
}

func TestMCPDocSaveAndList(t *testing.T) {
	_, _, ts := newServer(t)
	if res, code := putDoc(t, ts, "/api/mcp/doc?tui=1", `{"mcpServers": {"github": {"command": "gh"}}}`); code != 200 {
		t.Fatalf("global: %d %+v", code, res)
	}
	if res, code := putDoc(t, ts, "/api/mcp/doc?tui=1&project=home-network", `{"mcpServers": {"pw": {"command": "npx", "args": ["pw"], "env": {"T": "secret-token"}}}}`); code != 200 {
		t.Fatalf("project: %d %+v", code, res)
	}
	res, code := putDoc(t, ts, "/api/mcp/doc?tui=1&project=home-network", "{\n\"mcpServers\": {\n}x")
	if code != 400 || res.Line != 3 || !strings.HasPrefix(res.Error, "line 3, column") {
		t.Fatalf("bad doc: %d %+v", code, res)
	}
	m, code := get[api.MCP](t, ts, "/api/mcp?tui=1")
	if code != 200 || len(m.Global.Servers) != 1 || len(m.Projects) != 1 || m.Projects[0].Project != "home-network" ||
		m.Projects[0].Servers[0].Target != "npx pw" {
		t.Fatalf("list: %d %+v", code, m)
	}
	doc, _ := get[api.MCPDoc](t, ts, "/api/mcp/doc?tui=1&project=home-network")
	if !strings.Contains(doc.Text, `"secret-token"`) || !strings.HasPrefix(doc.Text, "{\n  \"mcpServers\"") {
		t.Fatalf("doc = %q", doc.Text)
	}
	if empty, _ := get[api.MCPDoc](t, ts, "/api/mcp/doc?tui=1&project=nope"); empty.Text != "" {
		t.Fatalf("unknown project: %q", empty.Text)
	}
	r, err := http.Get(ts.URL + "/api/projects/home-network")
	if err != nil {
		t.Fatal(err)
	}
	var raw bytes.Buffer
	raw.ReadFrom(r.Body)
	r.Body.Close()
	if strings.Contains(raw.String(), "secret-token") || strings.Contains(raw.String(), "mcp_servers") {
		t.Fatal("the snapshot reaches the web client: it must not carry MCP servers")
	}
	if _, code := putDoc(t, ts, "/api/mcp/doc?tui=1&project=home-network", ""); code != 200 {
		t.Fatal("empty text removes")
	}
	if m, _ := get[api.MCP](t, ts, "/api/mcp?tui=1"); len(m.Projects) != 0 {
		t.Fatalf("after remove: %+v", m.Projects)
	}
}
```

The `unknown project` GET returns a 404, so `get` leaves the zero value. That is the empty text the test checks.

**Step 4:** `go test ./internal/serve/ -run MCP -v`. Expected: FAIL (404s: no routes).

**Step 5: Write `internal/serve/mcp.go`:**

```go
package serve

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"

	"github.com/dinhhuy258/bx/internal/api"
	"github.com/dinhhuy258/bx/internal/mcp"
	"github.com/dinhhuy258/bx/internal/state"
	"github.com/dinhhuy258/bx/internal/ui"
)

// tuiOnly refuses a request without ?tui=1: MCP server configs may hold tokens, and the web
// client never gets them.
func tuiOnly(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Query().Get("tui") == "1" {
		return true
	}
	httpError(w, http.StatusForbidden, errors.New("MCP servers can only be seen and changed from the TUI"))
	return false
}

// getMCP lists the global servers and every project that has its own.
func (s *Server) getMCP(w http.ResponseWriter, r *http.Request) {
	if !tuiOnly(w, r) {
		return
	}
	out := api.MCP{Global: mcpSet(mcp.ReadFile(s.Cfg.MCPFile())), Projects: []ui.MCPProject{}}
	names, err := state.ListProjects(s.stateDir())
	if err != nil {
		httpError(w, 500, err)
		return
	}
	sort.Strings(names)
	for _, name := range names {
		p, err := s.App.LoadProject(name)
		if err != nil {
			s.Log("mcp: %s: %v", name, err)
			continue
		}
		if len(p.MCPServers) > 0 {
			out.Projects = append(out.Projects, ui.MCPProject{Project: name, MCPSet: mcpSet(mcp.Decode(p.MCPServers))})
		}
	}
	writeJSON(w, out)
}

func mcpSet(sv mcp.Servers, err error) ui.MCPSet {
	set := ui.MCPSet{Servers: []ui.MCPServer{}}
	if err != nil {
		set.Error = err.Error()
		return set
	}
	for _, x := range mcp.Summaries(sv) {
		set.Servers = append(set.Servers, ui.MCPServer{Name: x.Name, Type: x.Type, Target: x.Target})
	}
	return set
}

// getMCPDoc sends the text the TUI edits. A set that fails its check is sent as stored, so it
// can be fixed.
func (s *Server) getMCPDoc(w http.ResponseWriter, r *http.Request) {
	if !tuiOnly(w, r) {
		return
	}
	name := r.URL.Query().Get("project")
	if name == "" {
		data, err := os.ReadFile(s.Cfg.MCPFile())
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			httpError(w, 500, err)
			return
		}
		if sv, err := mcp.Parse(data); err == nil {
			data = mcp.Encode(sv)
		}
		writeJSON(w, api.MCPDoc{Text: string(data)})
		return
	}
	p, err := s.load(name)
	if err != nil {
		httpError(w, statusOf(err), err)
		return
	}
	sv, err := mcp.Decode(p.MCPServers)
	if err != nil {
		b, _ := json.MarshalIndent(map[string]json.RawMessage{"mcpServers": p.MCPServers}, "", "  ")
		writeJSON(w, api.MCPDoc{Text: string(b) + "\n"})
		return
	}
	writeJSON(w, api.MCPDoc{Text: string(mcp.Encode(sv))})
}

// putMCPDoc checks and saves the text; a refused one changes nothing and is a 400 with the
// line. The log never has the text: it may hold a token.
func (s *Server) putMCPDoc(w http.ResponseWriter, r *http.Request) {
	if !tuiOnly(w, r) {
		return
	}
	var req api.MCPDoc
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, 400, fmt.Errorf("decode body: %w", err))
		return
	}
	name := r.URL.Query().Get("project")
	s.write.Lock()
	defer s.write.Unlock()
	var err error
	if name == "" {
		err = s.App.SetGlobalMCP([]byte(req.Text))
	} else {
		var p *state.Project
		if p, err = s.load(name); err == nil {
			err = s.App.SetProjectMCP(p, []byte(req.Text))
		}
	}
	var me *mcp.Error
	switch {
	case errors.As(err, &me):
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(api.Result{Error: err.Error(), Line: me.Line})
		return
	case err != nil:
		httpError(w, statusOf(err), err)
		return
	}
	scope := name
	if scope == "" {
		scope = "global"
	}
	s.Log("mcp: saved the %s servers", scope)
	writeJSON(w, api.Result{})
}
```

In `server.go` `Handler`, after the settings routes:

```go
	mux.HandleFunc("GET /api/mcp", s.getMCP)
	mux.HandleFunc("GET /api/mcp/doc", s.getMCPDoc)
	mux.HandleFunc("PUT /api/mcp/doc", s.putMCPDoc)
```

In `getProject`, just before `writeJSON(w, api.Snapshot{…})`:

```go
	p.MCPServers = nil // the web client reads the snapshot; server configs may hold tokens
```

**Step 6:** `go test ./internal/serve/ ./internal/api/ -v -run 'MCP|Snapshot'`, then `go test ./...`. Expected: PASS.

**Commit:** `feat(serve): TUI-only MCP routes; keep MCP servers out of the snapshot`

---

### Task 8: Client API

**Files:**
- Modify: `internal/client/api.go` (after `SaveSettings`)
- Modify: `internal/client/model.go` (`Backend` interface ~line 45)
- Modify: `internal/client/model_test.go` (`fakeBackend`)
- Test: `internal/client/api_test.go` (append)

**Step 1: Failing test.** Read the top of `internal/client/api_test.go` and follow its existing `httptest` pattern:

```go
func TestAPIMCPDoc(t *testing.T) {
	var gotPath, gotBody string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.Method + " " + r.URL.RequestURI()
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		if r.Method == http.MethodPut {
			w.WriteHeader(400)
			w.Write([]byte(`{"error":"line 2, column 1: x","line":2}`))
			return
		}
		w.Write([]byte(`{"text":"{}"}`))
	}))
	defer ts.Close()
	c := NewAPI(ts.URL)
	if d, err := c.MCPDoc(context.Background(), "my proj"); err != nil || d.Text != "{}" || gotPath != "GET /api/mcp/doc?tui=1&project=my+proj" {
		t.Fatalf("%v %q %s", err, d.Text, gotPath)
	}
	res, err := c.SaveMCPDoc(context.Background(), "", "x")
	if err == nil || res.Line != 2 || gotPath != "PUT /api/mcp/doc?tui=1&project=" || gotBody != `{"text":"x"}` {
		t.Fatalf("%v %+v %s %s", err, res, gotPath, gotBody)
	}
}
```

(add `io`, `net/http/httptest` to the imports if they are missing)

**Step 2:** `go test ./internal/client/ -run TestAPIMCPDoc`. Expected: FAIL (undefined).

**Step 3:** In `api.go`:

```go
// MCP is GET /api/mcp?tui=1: the global MCP servers and every project with its own.
func (c *API) MCP(ctx context.Context) (api.MCP, error) {
	var out api.MCP
	return out, c.call(ctx, pollTimeout, http.MethodGet, "/api/mcp?tui=1", nil, &out)
}

// MCPDoc is GET /api/mcp/doc: the text of project's servers ("" = the global ones).
func (c *API) MCPDoc(ctx context.Context, project string) (api.MCPDoc, error) {
	var out api.MCPDoc
	return out, c.call(ctx, pollTimeout, http.MethodGet, "/api/mcp/doc?tui=1&project="+url.QueryEscape(project), nil, &out)
}

// SaveMCPDoc is PUT /api/mcp/doc. A refusal fills Result: the error and its Line.
func (c *API) SaveMCPDoc(ctx context.Context, project, text string) (api.Result, error) {
	var res api.Result
	err := c.call(ctx, pollTimeout, http.MethodPut, "/api/mcp/doc?tui=1&project="+url.QueryEscape(project), api.MCPDoc{Text: text}, &res)
	return res, err
}
```

In the `Backend` interface, after `SaveSettings`:

```go
	MCP(ctx context.Context) (api.MCP, error)
	MCPDoc(ctx context.Context, project string) (api.MCPDoc, error)
	SaveMCPDoc(ctx context.Context, project, text string) (api.Result, error)
```

In `model_test.go`, add to `fakeBackend`:

```go
	// settings › mcp
	mcp      api.MCP
	mcpDocs  map[string]string // by scope ("" = global)
	mcpSaves []string          // "<scope>=<text>"
	mcpErr   *api.Result       // SaveMCPDoc refuses with it
```

and the methods:

```go
func (f *fakeBackend) MCP(context.Context) (api.MCP, error) { return f.mcp, nil }

func (f *fakeBackend) MCPDoc(_ context.Context, scope string) (api.MCPDoc, error) {
	return api.MCPDoc{Text: f.mcpDocs[scope]}, nil
}

func (f *fakeBackend) SaveMCPDoc(_ context.Context, scope, text string) (api.Result, error) {
	f.mcpSaves = append(f.mcpSaves, scope+"="+text)
	if f.mcpErr != nil {
		return *f.mcpErr, &StatusError{Code: 400, Text: f.mcpErr.Error}
	}
	return api.Result{}, nil
}
```

**Step 4:** `go test ./internal/client/`. Expected: PASS.

**Commit:** `feat(client): MCP API calls`

---

### Task 9: Settings screen — the MCP rows

**Files:**
- Modify: `internal/ui/mcp.go` (add the screen code)
- Modify: `internal/ui/settings.go` (`Settings` struct, `SettingsResult` consts, `rowCount`, `Key`, `Render`, `cursorHelp`)
- Modify: `internal/ui/settings_git.go` (use the shared `window`)
- Test: `internal/ui/mcp_test.go`, goldens `internal/ui/testdata/settings-mcp-120.txt`, `settings-mcp-40.txt`, `settings-mcp-error-120.txt`

**Step 1: Failing test** — `internal/ui/mcp_test.go`:

```go
package ui

import (
	"fmt"
	"strings"
	"testing"
)

// mcpSettings is the sample screen on its MCP section: three global servers, three projects
// with their own (one replaces a global one), out of 34.
func mcpSettings() *Settings {
	s := sampleSettings()
	s.SetMCP(MCP{
		Global: MCPSet{Servers: []MCPServer{{"github", "stdio", "npx -y @modelcontextprotocol/server-github"}, {"postgres", "stdio", "npx -y @mcp/postgres postgres://localhost/dev"}, {"sentry", "http", "https://mcp.sentry.dev/mcp"}}},
		Projects: []MCPProject{
			{Project: "api-server", MCPSet: MCPSet{Servers: []MCPServer{{"stripe", "stdio", "npx -y @stripe/mcp"}}}},
			{Project: "bx", MCPSet: MCPSet{Servers: []MCPServer{{"playwright", "stdio", "npx -y @playwright/mcp@1.2.0"}, {"postgres", "stdio", "npx -y @mcp/postgres postgres://localhost/bx"}}}},
			{Project: "infra", MCPSet: MCPSet{Error: "x: args must be a list of strings"}},
		},
	})
	s.SetMCPTotal(34)
	s.Key("Tab") // git
	s.Key("Tab") // mcp
	return s
}

func TestSettingsMCPRowsAndKeys(t *testing.T) {
	s := mcpSettings()
	if !s.MCPShown() {
		t.Fatal("Tab Tab opens MCP")
	}
	if _, ok := s.MCPChoice(); ok {
		t.Fatal("the cursor starts on the Mode field")
	}
	if s.Key("l"); s.Changes()["mcp.mode"] != "strict" {
		t.Fatalf("h/l change the mode: %v", s.Changes())
	}
	s.Key("j")
	if r, ok := s.MCPChoice(); !ok || !r.Global {
		t.Fatalf("row 1 = %+v %v, want Global", r, ok)
	}
	if s.Key("Enter") != SettingsMCPEdit || s.Key("BSpace") != SettingsMCPRemove || s.Key("r") != SettingsMCPReload {
		t.Fatal("Enter edits, Backspace removes, r reloads")
	}
	if s.Key("l"); s.Changes()["mcp.mode"] != "strict" {
		t.Fatal("l on a row must not change the mode")
	}
	s.Key("j")
	s.Key("j")
	if r, _ := s.MCPChoice(); r.Project != "bx" {
		t.Fatalf("row 3 = %+v", r)
	}
	if s.MCPCount("bx") != 2 || s.MCPCount("") != 3 {
		t.Fatal("MCPCount")
	}
	for range 5 {
		s.Key("j")
	}
	if r, _ := s.MCPChoice(); !r.Add || s.Key("BSpace") != SettingsKeep {
		t.Fatalf("last row = %+v, Backspace there does nothing", r)
	}
	if got := strings.Join(s.MCPProjects(), ","); got != "api-server,bx,infra" {
		t.Fatalf("MCPProjects = %s", got)
	}
}

func TestSettingsMCPGolden(t *testing.T) {
	s := mcpSettings()
	s.Key("j")
	s.Key("j")
	s.Key("j") // bx
	golden(t, "settings-mcp-120", s.Render(120, 30, true, ""), 120)
	golden(t, "settings-mcp-40", s.Render(40, 30, true, ""), 40)
	s.SetMCPError("bx", "line 9, column 5: invalid character")
	golden(t, "settings-mcp-error-120", s.Render(120, 30, true, ""), 120)
	v := s.Render(120, 30, true, "")
	for _, want := range []string{
		"project servers · 3 of 34 projects have their own",
		"Global",
		"3 servers  github · postgres · sentry",
		"✗ not saved  line 9, column 5: invalid character",
		"✗ skipped  x: args must be a list of strings",
		"A new session in bx starts with",
		"bx  replaces global",
		"+ add project servers",
	} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q in:\n%s", want, v)
		}
	}
}

func TestSettingsMCPWindowsManyProjects(t *testing.T) {
	s := sampleSettings()
	var ps []MCPProject
	for i := range 30 {
		ps = append(ps, MCPProject{Project: fmt.Sprintf("p%02d", i), MCPSet: MCPSet{Servers: []MCPServer{{"a", "stdio", "a"}}}})
	}
	s.SetMCP(MCP{Projects: ps})
	s.SetMCPTotal(40)
	s.Key("Tab")
	s.Key("Tab")
	for range 16 {
		s.Key("j")
	}
	v := s.Render(120, 24, true, "")
	if !strings.Contains(v, "more") || !strings.Contains(v, "› p14") || strings.Contains(v, "p00") {
		t.Fatalf("the list must scroll around the cursor:\n%s", v)
	}
}
```

Check the signature of `golden` in `internal/ui/settings_sync_test.go:60` before using it. It is `golden(t, name, got string, w int)`; if its name argument already gets a `.txt` suffix or a `testdata/` prefix, adjust the calls above to match.

**Step 2:** `go test ./internal/ui/ -run MCP`. Expected: FAIL (undefined: SetMCP).

**Step 3: Implement.**

*`settings.go`:* add the results after `SettingsEdit`:

```go
	SettingsMCPEdit   // Enter on an MCP row: edit MCPChoice's servers (or add a project)
	SettingsMCPRemove // Backspace on an MCP row: remove its servers, or drop its draft
	SettingsMCPReload // r on an MCP row: read the servers again
```

In the `Settings` struct, after `git`:

```go
	mcp      MCP               // the MCP section's rows (see mcp.go)
	mcpTotal int               // every project, for "3 of 34"
	mcpErrs  map[string]string // by scope ("" = global): why the last save was refused
```

In `NewSettings`, add `mcpErrs: map[string]string{}` to the literal.

`rowCount` becomes:

```go
	return len(s.fields()) + len(s.syncRows()) + s.mcpRowCount()
```

In `Key`, right after the `if s.GitShown() { return s.gitKey(key) }` block:

```go
	if _, ok := s.MCPChoice(); ok && key != "Tab" && key != "Esc" && key != "C-c" && !isMove(key) {
		return s.mcpKey(key) // moves, Tab and Esc work as on any row
	}
```

and add, in `settings.go`:

```go
// isMove reports whether key moves the cursor up or down.
func isMove(key string) bool {
	switch key {
	case "j", "Down", "C-j", "k", "Up", "C-k":
		return true
	}
	return false
}
```

In `Render`, narrow branch: after `lines = append(lines, s.fieldLines(w, pal)...)`:

```go
		if s.MCPShown() {
			lines = append(lines, s.mcpLines(w, h-len(lines)-len(s.helpLines(w, pal)), pal)...)
		}
```

Wide branch, in `default:` after `r = append(r, s.fieldLines(right, pal)...)` and the `help` computation, before the `syncShown` check:

```go
		if s.MCPShown() {
			r = append(r, s.mcpLines(right, h-len(lines)-len(r)-len(help), pal)...)
		}
```

In `cursorHelp`, first line:

```go
	if _, ok := s.MCPChoice(); ok {
		return "" // the key bar has the row's keys; the rows show the rest
	}
```

*`settings_git.go`:* replace the windowing block at the end of `gitLines` (from `if avail := …` to the closing `}` before `return`) with:

```go
	rows = window(rows, s.fld, max(h-len(out), 3), pal)
```

*Append to `internal/ui/mcp.go`:*

```go
// mcpSection is the ID of the server's MCP section; the client adds its rows under the Mode
// field.
const mcpSection = "mcp"

const mcpAdd = "+ add project servers"

// MCPRow is an MCP row: the Global row, a project's, or the add row.
type MCPRow struct {
	Project     string
	Global, Add bool
}

// SetMCP shows the servers from GET /api/mcp. The cursor stays on its row number.
func (s *Settings) SetMCP(m MCP) {
	s.mcp = m
	s.fld = min(s.fld, max(s.rowCount()-1, 0))
}

// SetMCPTotal is the number of projects, for the section's header.
func (s *Settings) SetMCPTotal(n int) { s.mcpTotal = n }

// SetMCPError marks scope ("" = global) as refused with err; "" clears it.
func (s *Settings) SetMCPError(scope, err string) {
	if err == "" {
		delete(s.mcpErrs, scope)
		return
	}
	s.mcpErrs[scope] = err
}

// MCPShown reports whether the MCP section is open.
func (s *Settings) MCPShown() bool {
	return s.sec < len(s.sections) && s.sections[s.sec].ID == mcpSection
}

// MCPProjects are the projects that have their own servers.
func (s *Settings) MCPProjects() []string {
	out := make([]string, len(s.mcp.Projects))
	for i, p := range s.mcp.Projects {
		out[i] = p.Project
	}
	return out
}

// MCPCount is the number of servers of scope ("" = global).
func (s *Settings) MCPCount(scope string) int {
	if scope == "" {
		return len(s.mcp.Global.Servers)
	}
	for _, p := range s.mcp.Projects {
		if p.Project == scope {
			return len(p.Servers)
		}
	}
	return 0
}

func (s *Settings) mcpRowCount() int {
	if !s.MCPShown() {
		return 0
	}
	return len(s.mcp.Projects) + 2
}

// MCPChoice is the MCP row under the cursor; false on the Mode field or outside the section.
func (s *Settings) MCPChoice() (MCPRow, bool) {
	i := s.fld - len(s.fields())
	switch {
	case !s.MCPShown() || i < 0:
		return MCPRow{}, false
	case i == 0:
		return MCPRow{Global: true}, true
	case i <= len(s.mcp.Projects):
		return MCPRow{Project: s.mcp.Projects[i-1].Project}, true
	}
	return MCPRow{Add: true}, true
}

// mcpKey handles a key on an MCP row other than a move, Tab or Esc, which Key handles.
func (s *Settings) mcpKey(key string) SettingsResult {
	row, _ := s.MCPChoice()
	switch key {
	case "Enter":
		return SettingsMCPEdit
	case "BSpace":
		if !row.Add {
			return SettingsMCPRemove
		}
	case "r":
		return SettingsMCPReload
	}
	return SettingsKeep
}

// mcpLines are the rows under the Mode field: Global, the header, the projects windowed
// around the cursor, the add row, then what a new session of the row under the cursor gets.
func (s *Settings) mcpLines(w, h int, pal palette) []string {
	row, onRow := s.MCPChoice()
	wide := w >= 60
	nameW := runeWidth("Global")
	for _, p := range s.mcp.Projects {
		nameW = max(nameW, runeWidth(p.Project))
	}
	nameW = max(min(nameW, w/3), 6)
	first := len(s.fields())
	line := func(i int, name, state string) string {
		cursor, label := " ", truncate(name, nameW)
		label += strings.Repeat(" ", max(nameW-runeWidth(label), 0))
		if s.fld == first+i {
			cursor, label = pal.blue.Render("›"), pal.bold.Render(label)
		}
		return truncate(" "+cursor+" "+label+"  "+state, w)
	}
	head := pal.bold.Render("project servers") + pal.dim.Render(fmt.Sprintf(" · %d of %d projects have their own", len(s.mcp.Projects), s.mcpTotal))
	if !wide {
		head = pal.bold.Render("project servers") + pal.dim.Render(fmt.Sprintf(" · %d of %d", len(s.mcp.Projects), s.mcpTotal))
	}
	out := []string{"", line(0, "Global", s.mcpState("", s.mcp.Global, wide, pal)), "", truncate(" "+head, w), ""}
	var rows []string
	for i, p := range s.mcp.Projects {
		rows = append(rows, line(i+1, p.Project, s.mcpState(p.Project, p.MCPSet, wide, pal)))
	}
	add := "   " + mcpAdd
	if onRow && row.Add {
		add = " " + pal.blue.Render("›") + " " + pal.bold.Render(mcpAdd)
	}
	rows = append(rows, add)
	preview := s.mcpPreview(row, onRow, w, wide, pal)
	rows = window(rows, max(s.fld-first-1, 0), max(h-len(out)-len(preview), 3), pal)
	return append(append(out, rows...), preview...)
}

// mcpState is a row's state: a refused save, a set skipped at launch, or its servers.
func (s *Settings) mcpState(scope string, set MCPSet, wide bool, pal palette) string {
	if e := s.mcpErrs[scope]; e != "" {
		return pal.red.Render("✗ not saved") + "  " + pal.dim.Render(e)
	}
	if set.Error != "" {
		return pal.red.Render("✗ skipped") + "  " + pal.dim.Render(set.Error)
	}
	if len(set.Servers) == 0 {
		return pal.dim.Render("none")
	}
	n := fmt.Sprintf("%d servers", len(set.Servers))
	if len(set.Servers) == 1 {
		n = "1 server"
	}
	if !wide {
		return pal.green.Render(n)
	}
	names := make([]string, len(set.Servers))
	for i, sv := range set.Servers {
		names[i] = sv.Name
	}
	return pal.green.Render(n) + "  " + pal.dim.Render(strings.Join(names, " · "))
}

// mcpPreview is what a new session of the row under the cursor starts with, and where each
// server comes from; nil off the rows and on the add row.
func (s *Settings) mcpPreview(row MCPRow, onRow bool, w int, wide bool, pal palette) []string {
	if !onRow || row.Add {
		return nil
	}
	type entry struct {
		MCPServer
		from     string
		replaces bool
	}
	var list []entry
	own := map[string]bool{}
	var proj MCPSet
	for _, p := range s.mcp.Projects {
		if p.Project == row.Project {
			proj = p.MCPSet
		}
	}
	for _, sv := range proj.Servers {
		own[sv.Name] = true
	}
	for _, sv := range s.mcp.Global.Servers {
		if !own[sv.Name] {
			list = append(list, entry{sv, "global", false})
		}
	}
	globalNames := map[string]bool{}
	for _, sv := range s.mcp.Global.Servers {
		globalNames[sv.Name] = true
	}
	for _, sv := range proj.Servers {
		list = append(list, entry{sv, row.Project, globalNames[sv.Name]})
	}
	title := "Every new session starts with"
	if !row.Global {
		title = "A new session in " + row.Project + " starts with"
	}
	out := []string{"", truncate(" "+pal.dim.Render(title), w)}
	if len(list) == 0 {
		return append(out, "   "+pal.dim.Render("no MCP servers from bx"))
	}
	if !wide {
		byFrom := map[string][]string{}
		var order []string
		for _, e := range list {
			if _, ok := byFrom[e.from]; !ok {
				order = append(order, e.from)
			}
			byFrom[e.from] = append(byFrom[e.from], e.Name)
		}
		for _, f := range order {
			out = append(out, truncate(" "+pal.dim.Render(strings.Join(byFrom[f], ", ")+" · "+f), w))
		}
		return out
	}
	nameW := 0
	for _, e := range list {
		nameW = max(nameW, runeWidth(e.Name))
	}
	targetW := max(w-3-nameW-2-7-2-len(row.Project)-len("  replaces global"), 10)
	for _, e := range list {
		target := truncate(e.Target, targetW)
		l := "   " + e.Name + strings.Repeat(" ", nameW-runeWidth(e.Name)+2) +
			e.Type + strings.Repeat(" ", max(7-runeWidth(e.Type), 1)) +
			pal.dim.Render(target+strings.Repeat(" ", max(targetW-runeWidth(target), 0))) + "  " + e.from
		if e.replaces {
			l += "  " + pal.amber.Render("replaces global")
		}
		out = append(out, truncate(l, w))
	}
	return out
}

// window keeps at most avail rows around cursor, with "… n more" in place of the first or last
// shown row when rows are hidden that way.
func window(rows []string, cursor, avail int, pal palette) []string {
	if len(rows) <= avail {
		return rows
	}
	start := min(max(cursor-(avail-1)/2, 0), len(rows)-avail)
	end := start + avail
	shown := slices.Clone(rows[start:end])
	more := func(n int) string { return "     " + pal.dim.Render(fmt.Sprintf("… %d more", n)) }
	if start > 0 {
		shown[0] = more(start + 1)
	}
	if end < len(rows) {
		shown[len(shown)-1] = more(len(rows) - end + 1)
	}
	return shown
}

// SettingsMCPBar is the key bar while the cursor is on an MCP row.
func SettingsMCPBar(narrow bool) string {
	if narrow {
		return "⏎ edit · ⌫ remove · esc"
	}
	return "j/k move · ⏎ edit in $EDITOR · ⌫ remove · r reload · tab section · esc close"
}
```

`internal/ui/mcp.go` now needs `import ("fmt"; "slices"; "strings")`. In `settings_git.go`, remove the `slices` and `fmt` imports if nothing else there uses them; `go vet` will say.

**Step 4:** Create the goldens with `go test ./internal/ui/ -run MCP -update`, then run `go test ./internal/ui/` without `-update`. Expected: PASS. Open the three new `testdata/settings-mcp-*.txt` files and compare them by eye with the design page's mockups. The layout should match: rows, header and preview. Exact spacing may differ. Run `go test ./internal/ui/ -run Git` to confirm the `window` refactor kept the Git section the same; its goldens must not change.

**Commit:** `feat(ui): MCP section rows on the settings screen`

---

### Task 10: Client — load, edit in `$EDITOR`, drafts, add, remove

**Files:**
- Create: `internal/client/mcp.go`
- Modify: `internal/client/model.go` (`Model` fields ~line 196, `NewModel` ~line 220, `pickKind` consts ~line 137, `Update` ~line 418, `gotProjects` ~line 476, `openSettings` ~line 1213, `settingsKey` ~line 1245, picker choose ~line 1169, prompt submit ~line 1388, `bar` ~line 1833)
- Modify: `internal/ui/prompt.go` (new `PromptRemoveMCP`)
- Test: `internal/client/mcp_test.go`

**Step 1: Failing test** — `internal/client/mcp_test.go`:

```go
package client

import (
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/dinhhuy258/bx/internal/api"
	"github.com/dinhhuy258/bx/internal/ui"
)

const labDoc = "{\n  \"mcpServers\": {\n    \"pw\": {\"command\": \"npx\"}\n  }\n}\n"

type editCall struct {
	path, text string
	line       int
}

// mcpHarness opens settings › MCP with lab having one server. The editor replaces the file's
// text with what typed returns for it (nil leaves it as it is) and records every call.
func mcpHarness(t *testing.T, typed func(string) string) (*harness, *[]editCall) {
	t.Helper()
	h := start(t, 120, 30, "running", "running")
	h.be.mcp = api.MCP{Projects: []ui.MCPProject{{Project: "lab", MCPSet: ui.MCPSet{Servers: []ui.MCPServer{{Name: "pw", Type: "stdio", Target: "npx"}}}}}}
	h.be.mcpDocs = map[string]string{"lab": labDoc, "home-network": "{\n  \"mcpServers\": {}\n}\n"}
	calls := &[]editCall{}
	h.m.edit = func(path string, line int, done func(error) tea.Msg) tea.Cmd {
		b, _ := os.ReadFile(path)
		if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
			t.Errorf("temp file mode %v", fi.Mode())
		}
		*calls = append(*calls, editCall{path, string(b), line})
		if typed != nil {
			os.WriteFile(path, []byte(typed(string(b))), 0o600)
		}
		return func() tea.Msg { return done(nil) }
	}
	h.press("S", "Tab", "Tab")
	return h, calls
}

func TestSettingsMCPEditsAProject(t *testing.T) {
	h, calls := mcpHarness(t, func(s string) string { return strings.Replace(s, "npx", "pnpx", 1) })
	v := ansi.Strip(h.m.View().Content)
	if !strings.Contains(v, "project servers · 1 of 2 projects have their own") || !strings.Contains(v, "lab") {
		t.Fatalf("MCP section:\n%s", v)
	}
	h.press("j", "j") // Global, lab
	if v := ansi.Strip(h.m.View().Content); !strings.Contains(v, "⏎ edit in $EDITOR") {
		t.Fatalf("key bar:\n%s", v)
	}
	h.press("Enter")
	if len(*calls) != 1 || (*calls)[0].text != labDoc || (*calls)[0].line != 0 {
		t.Fatalf("editor calls = %+v", *calls)
	}
	if got := strings.Join(h.be.mcpSaves, "|"); got != "lab="+strings.Replace(labDoc, "npx", "pnpx", 1) {
		t.Fatalf("saves = %q", got)
	}
	if _, err := os.Stat((*calls)[0].path); !os.IsNotExist(err) {
		t.Fatal("the temp file must be removed")
	}
	if v := ansi.Strip(h.m.View().Content); !strings.Contains(v, "lab: MCP servers saved · new sessions use them") {
		t.Fatalf("flash:\n%s", v)
	}
}

func TestSettingsMCPUnchangedSendsNothing(t *testing.T) {
	h, _ := mcpHarness(t, nil)
	h.press("j", "j", "Enter")
	if len(h.be.mcpSaves) != 0 || !strings.Contains(ansi.Strip(h.m.View().Content), "lab: no changes") {
		t.Fatalf("saves = %q", h.be.mcpSaves)
	}
}

func TestSettingsMCPKeepsARefusedDraft(t *testing.T) {
	h, calls := mcpHarness(t, func(s string) string { return s + "oops" })
	h.be.mcpErr = &api.Result{Error: "line 6, column 1: unexpected text after the closing }", Line: 6}
	h.press("j", "j", "Enter")
	v := ansi.Strip(h.m.View().Content)
	if !strings.Contains(v, "✗ not saved  line 6, column 1") || !strings.Contains(v, "⏎ reopens your draft") {
		t.Fatalf("refused save:\n%s", v)
	}
	h.press("Enter") // reopens the draft at line 6, without a GET
	if len(*calls) != 2 || (*calls)[1].line != 6 || (*calls)[1].text != labDoc+"oops" {
		t.Fatalf("reopen = %+v", (*calls)[1:])
	}
	h.press("BSpace") // discards the draft, asks nothing
	if v := ansi.Strip(h.m.View().Content); strings.Contains(v, "✗ not saved") || !strings.Contains(v, "lab: draft discarded") {
		t.Fatalf("after discard:\n%s", v)
	}
}

func TestSettingsMCPAddsAndRemoves(t *testing.T) {
	h, calls := mcpHarness(t, func(string) string { return `{"mcpServers": {"a": {"command": "b"}}}` })
	h.press("j", "j", "j", "Enter") // + add project servers
	v := ansi.Strip(h.m.View().Content)
	if h.m.pickKind != pickMCPProject || !strings.Contains(v, "MCP servers for…") || !strings.Contains(v, "home-network") {
		t.Fatalf("picker:\n%s", v)
	}
	if strings.Contains(ansi.Strip(h.m.picker.Render(120, 10)), "lab") {
		t.Fatal("the picker leaves out projects that have servers")
	}
	h.press("Enter")
	if len(*calls) != 1 || !strings.Contains(h.be.mcpSaves[0], "home-network=") {
		t.Fatalf("add: calls %+v saves %q", *calls, h.be.mcpSaves)
	}
	h.press("k") // lab
	h.press("BSpace")
	if v := ansi.Strip(h.m.View().Content); !strings.Contains(v, "remove MCP servers of lab (1 server)? y/n") {
		t.Fatalf("remove asks:\n%s", v)
	}
	h.press("y")
	if got := h.be.mcpSaves[len(h.be.mcpSaves)-1]; got != "lab=" {
		t.Fatalf("remove saves empty text, got %q", got)
	}
}

func TestSettingsCloseDropsMCPDrafts(t *testing.T) {
	h, _ := mcpHarness(t, func(s string) string { return s + "x" })
	h.be.mcpErr = &api.Result{Error: "bad"}
	h.press("j", "j", "Enter", "Esc")
	if h.m.settings != nil || len(h.m.mcpDrafts) != 0 {
		t.Fatalf("close keeps drafts: %v", h.m.mcpDrafts)
	}
}
```

After `h.press("j","j","j","Enter")`, if the cursor isn't on the add row, check how many rows lie between Mode and add (Global, lab, add), and adjust the count of `j`.

**Step 2:** `go test ./internal/client/ -run MCP`. Expected: FAIL (`h.m.edit undefined`).

**Step 3: Implement.**

*`ui/prompt.go`:* add `PromptRemoveMCP // y/n: remove the MCP servers of a scope (Target is the project, "" = global)` after `PromptRenameShell`. Add `PromptRemoveMCP` to the `YesNo` list. Add it next to `PromptRemoveOverride` in the verb switch, so it reads "remove".

*`model.go`:*

- `pickKind` consts: after `pickArtifact` add `pickMCPProject // settings › mcp: the project of new servers`.
- `Model` fields, after `override`:

```go
	edit      func(path string, line int, done func(error) tea.Msg) tea.Cmd // runs the editor on path; tests replace it
	mcpDrafts map[string]mcpDraft                                         // settings › mcp: refused saves by scope, see mcp.go
```

- `NewModel`: add `edit: openEditor, mcpDrafts: map[string]mcpDraft{}` to the literal.
- `Update`: next to `case settingsMsg:` add:

```go
	case mcpMsg:
		m.gotMCP(msg)
	case mcpDocMsg:
		return m, m.gotMCPDoc(msg)
	case mcpEditedMsg:
		return m, m.gotMCPEdited(msg)
	case mcpSavedMsg:
		return m, m.gotMCPSaved(msg)
```

  Check that the surrounding `switch` returns `m, cmd` this way. If its cases set a `cmd` variable instead, follow that pattern.
- `gotProjects`: after `m.settings.SetGitOverrides(m.gitOverrides())` add `m.settings.SetMCPTotal(len(m.projects))`.
- `openSettings`: after `m.settings.SetGitOverrides(…)` add `m.settings.SetMCPTotal(len(m.projects))`, and change the return to `tea.Batch(m.loadSettings(), m.scanSync(), m.loadMCP())`.
- `settingsKey`: in `case ui.SettingsClose:` set `m.mcpDrafts = map[string]mcpDraft{}` next to `m.settings = nil`. Do the same in `case ui.PromptDiscardSettings:` of the prompt submit. Add the cases:

```go
	case ui.SettingsMCPEdit:
		row, _ := m.settings.MCPChoice()
		if row.Add {
			m.openMCPProjects()
			return nil
		}
		return m.editMCP(row.Project)
	case ui.SettingsMCPRemove:
		m.removeMCPKey()
	case ui.SettingsMCPReload:
		return m.loadMCP()
```

- Picker choose switch: `case pickMCPProject: return m.editMCP(choice)`.
- Prompt submit: `case ui.PromptRemoveMCP: return m.saveMCP(target, "", "")`.
- `bar`: before `case m.settings != nil && m.settings.GitShown():` add:

```go
	case m.settings != nil && m.settingsOnMCPRow():
		text = ui.SettingsMCPBar(m.narrow)
```

*Create `internal/client/mcp.go`:*

```go
package client

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/dinhhuy258/bx/internal/api"
	"github.com/dinhhuy258/bx/internal/ui"
)

// mcpDraft is a save the server refused: the text as typed, reopened at line by ⏎ until it
// saves or is discarded (⌫, or closing the settings screen). orig is the server's text.
type mcpDraft struct {
	text, orig string
	line       int
}

type (
	mcpMsg struct {
		m   api.MCP
		err error
	}
	mcpDocMsg struct {
		scope, text string
		err         error
	}
	mcpEditedMsg struct {
		scope, path, orig string
		err               error
	}
	mcpSavedMsg struct {
		scope, text string
		res         api.Result
		err         error
		orig        string
	}
)

// lineEditors take +N to open at line N.
var lineEditors = []string{"vi", "vim", "nvim", "nano"}

// openEditor runs $EDITOR (vi when unset) on path in this terminal, the TUI paused.
func openEditor(path string, line int, done func(error) tea.Msg) tea.Cmd {
	args := strings.Fields(os.Getenv("EDITOR"))
	if len(args) == 0 {
		args = []string{"vi"}
	}
	if line > 0 && slices.Contains(lineEditors, filepath.Base(args[0])) {
		args = append(args, fmt.Sprintf("+%d", line))
	}
	args = append(args, path)
	return tea.ExecProcess(exec.Command(args[0], args[1:]...), done)
}

// scopeName names a scope in messages.
func scopeName(scope string) string {
	if scope == "" {
		return "global"
	}
	return scope
}

func (m *Model) loadMCP() tea.Cmd {
	be, ctx := m.be, m.ctx
	return func() tea.Msg {
		x, err := be.MCP(ctx)
		return mcpMsg{m: x, err: err}
	}
}

func (m *Model) gotMCP(msg mcpMsg) {
	if m.settings == nil {
		return
	}
	if msg.err != nil {
		m.setFlash("mcp: " + msg.err.Error())
		return
	}
	m.settings.SetMCP(msg.m)
}

// settingsOnMCPRow reports whether the cursor is on an MCP row, for the key bar.
func (m *Model) settingsOnMCPRow() bool {
	_, ok := m.settings.MCPChoice()
	return ok
}

// editMCP opens scope's servers in the editor: its draft if a save was refused, else the
// server's text.
func (m *Model) editMCP(scope string) tea.Cmd {
	if d, ok := m.mcpDrafts[scope]; ok {
		return m.openMCPEditor(scope, d.text, d.orig, d.line)
	}
	be, ctx := m.be, m.ctx
	return func() tea.Msg {
		d, err := be.MCPDoc(ctx, scope)
		return mcpDocMsg{scope: scope, text: d.Text, err: err}
	}
}

func (m *Model) gotMCPDoc(msg mcpDocMsg) tea.Cmd {
	if msg.err != nil {
		m.setFlash("mcp: " + msg.err.Error())
		return nil
	}
	return m.openMCPEditor(msg.scope, msg.text, msg.text, 0)
}

// openMCPEditor writes text to a temp file only this user can read (os.CreateTemp makes it
// 0600) and hands the terminal to the editor.
func (m *Model) openMCPEditor(scope, text, orig string, line int) tea.Cmd {
	f, err := os.CreateTemp("", "bx-mcp-*.json")
	if err != nil {
		m.setFlash("mcp: " + err.Error())
		return nil
	}
	path := f.Name()
	_, err = f.WriteString(text)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(path)
		m.setFlash("mcp: " + err.Error())
		return nil
	}
	return m.edit(path, line, func(err error) tea.Msg {
		return mcpEditedMsg{scope: scope, path: path, orig: orig, err: err}
	})
}

// gotMCPEdited reads and removes the temp file, then saves what changed.
func (m *Model) gotMCPEdited(msg mcpEditedMsg) tea.Cmd {
	data, rerr := os.ReadFile(msg.path)
	_ = os.Remove(msg.path)
	switch {
	case msg.err != nil:
		m.setFlash("editor: " + msg.err.Error())
		return nil
	case rerr != nil:
		m.setFlash("mcp: " + rerr.Error())
		return nil
	case string(data) == msg.orig:
		m.dropMCPDraft(msg.scope)
		m.setFlash(scopeName(msg.scope) + ": no changes")
		return nil
	}
	return m.saveMCP(msg.scope, string(data), msg.orig)
}

func (m *Model) saveMCP(scope, text, orig string) tea.Cmd {
	be, ctx := m.be, m.ctx
	return func() tea.Msg {
		res, err := be.SaveMCPDoc(ctx, scope, text)
		return mcpSavedMsg{scope: scope, text: text, orig: orig, res: res, err: err}
	}
}

// gotMCPSaved keeps a refused text as the scope's draft; a save reloads the rows.
func (m *Model) gotMCPSaved(msg mcpSavedMsg) tea.Cmd {
	if msg.err != nil {
		if msg.text == "" { // a remove: nothing to keep
			m.setFlash("error: " + msg.err.Error())
			return nil
		}
		m.mcpDrafts[msg.scope] = mcpDraft{text: msg.text, orig: msg.orig, line: msg.res.Line}
		if m.settings != nil {
			m.settings.SetMCPError(msg.scope, msg.err.Error())
		}
		m.setFlash(scopeName(msg.scope) + ": MCP servers not saved · ⏎ reopens your draft")
		return nil
	}
	m.dropMCPDraft(msg.scope)
	if msg.text == "" {
		m.setFlash(scopeName(msg.scope) + ": MCP servers removed")
	} else {
		m.setFlash(scopeName(msg.scope) + ": MCP servers saved · new sessions use them")
	}
	return m.loadMCP()
}

func (m *Model) dropMCPDraft(scope string) {
	delete(m.mcpDrafts, scope)
	if m.settings != nil {
		m.settings.SetMCPError(scope, "")
	}
}

// openMCPProjects opens the picker of the projects without their own servers.
func (m *Model) openMCPProjects() {
	have := m.settings.MCPProjects()
	var items []string
	for _, p := range m.projects {
		if !slices.Contains(have, p.Name) {
			items = append(items, p.Name)
		}
	}
	if len(items) == 0 {
		m.setFlash("every project has MCP servers")
		return
	}
	m.picker, m.pickKind = ui.NewPicker("MCP servers for…", items), pickMCPProject
}

// removeMCPKey is ⌫ on an MCP row: it drops a draft, or asks before removing the servers.
func (m *Model) removeMCPKey() {
	row, _ := m.settings.MCPChoice()
	scope := row.Project
	if _, ok := m.mcpDrafts[scope]; ok {
		m.dropMCPDraft(scope)
		m.setFlash(scopeName(scope) + ": draft discarded")
		return
	}
	n := m.settings.MCPCount(scope)
	if n == 0 {
		return
	}
	label := fmt.Sprintf("remove MCP servers of %s (%s)? y/n", scope, plural(n, "server", "servers"))
	if scope == "" {
		label = fmt.Sprintf("remove the global MCP servers (%s)? y/n", plural(n, "server", "servers"))
	}
	m.openPrompt(ui.PromptRemoveMCP, scope, label, "")
}
```

For a new project, the server's doc is `{"mcpServers": {}}` (from `mcp.Encode(nil)`), so the editor opens with that template.

**Step 4:** `go test ./internal/client/ -v -run MCP`, then `go test ./...`. Expected: PASS. If `drain` meets the real `tea.ExecProcess` message in an existing test, a test is reaching `openEditor`. Every MCP test must set `h.m.edit`.

**Commit:** `feat(client): edit MCP servers in $EDITOR from the settings screen`

---

### Task 11: Check everything, then run it once

**Step 1:** `make lint && go test ./...`. Expected: no vet findings, all PASS.

**Step 2: Manual run.** This is not automated, because it needs a real terminal and editor.

```bash
go build -o /tmp/bx ./cmd/bx
BX_ROOT=$(mktemp -d) /tmp/bx serve &     # or use the dev server
EDITOR=vi /tmp/bx                          # open settings with S, Tab Tab to MCP
```

Check each item:
1. On Global, ⏎ opens vi with `{"mcpServers": {}}`. Add `"github": {"command": "npx", "args": ["-y", "@modelcontextprotocol/server-github"]}` and quit with `:wq`. The row shows `1 server  github`.
2. On `+ add project servers`, ⏎ opens the picker. Choosing a project opens vi. Type a broken edit (a missing comma) and quit. The row shows `✗ not saved  line N…`. ⏎ reopens vi at that line with your text.
3. Start a new chat in that project. `ps -ef | grep -- --mcp-config` shows `--mcp-config=…/run/mcp/<project>/<num>/mcp.json`, and that file is mode 600.
4. Set Mode to `strict` and save with ⏎ on the field. A new session also gets `--strict-mcp-config`.
5. Open the web client's settings. The MCP Mode select is disabled. `curl …/api/mcp` without `?tui=1` returns 403.

**Step 3:** Report the results to the user. Do not push.
