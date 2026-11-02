package serve

import (
	"net/http"

	"github.com/dinhhuy258/bx/internal/api"
	"github.com/dinhhuy258/bx/internal/claude"
)

// listSlashCommands lists what can follow "/" in a session's prompt, for the web client's
// suggestions: Claude's built-ins, the user's and the session folder's skills and commands, and
// the bx plugin's skills.
func (s *Server) listSlashCommands(w http.ResponseWriter, r *http.Request) {
	p, num, ok := s.session(w, r)
	if !ok {
		return
	}
	dir := s.App.Dir(p, p.Sessions[num].Story)
	writeJSON(w, api.SlashCommandList{Commands: claude.SlashCommands(claude.ConfigDir(), dir, s.App.PluginDir)})
}
