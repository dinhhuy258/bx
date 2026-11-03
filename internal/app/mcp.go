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
