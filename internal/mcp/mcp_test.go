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
	for _, c := range []struct {
		in, want string
		line     int
	}{
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
