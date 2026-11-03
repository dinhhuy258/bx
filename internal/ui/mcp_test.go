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
