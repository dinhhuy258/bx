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
