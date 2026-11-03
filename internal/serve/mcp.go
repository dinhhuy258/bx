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
