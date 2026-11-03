package serve

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"

	"github.com/dinhhuy258/bx/internal/accounts"
	"github.com/dinhhuy258/bx/internal/api"
	"github.com/dinhhuy258/bx/internal/state"
	"github.com/dinhhuy258/bx/internal/ui"
)

// accountsTUIOnly refuses a request without ?tui=1: the accounts hold emails and key paths,
// and the web client never gets them.
func accountsTUIOnly(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Query().Get("tui") == "1" {
		return true
	}
	httpError(w, http.StatusForbidden, errors.New("GitHub accounts can only be seen and changed from the TUI"))
	return false
}

// getAccounts sends the accounts, gh's logins and the origin of every project.
func (s *Server) getAccounts(w http.ResponseWriter, r *http.Request) {
	if !accountsTUIOnly(w, r) {
		return
	}
	c, err := s.App.Accounts()
	if err != nil {
		httpError(w, 500, err)
		return
	}
	out, err := s.accountsView(r, c)
	if err != nil {
		httpError(w, 500, err)
		return
	}
	writeJSON(w, out)
}

// putAccounts saves the whole config and sends the view of it.
func (s *Server) putAccounts(w http.ResponseWriter, r *http.Request) {
	if !accountsTUIOnly(w, r) {
		return
	}
	var c accounts.Config
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		httpError(w, 400, fmt.Errorf("decode body: %w", err))
		return
	}
	s.write.Lock()
	err := s.App.SaveAccounts(c)
	s.write.Unlock()
	if err != nil {
		httpError(w, 400, err)
		return
	}
	s.Log("accounts: saved %d accounts, %d rules, %d overrides", len(c.Accounts), len(c.Rules), len(c.Overrides))
	out, err := s.accountsView(r, c)
	if err != nil {
		httpError(w, 500, err)
		return
	}
	writeJSON(w, out)
}

func (s *Server) accountsView(r *http.Request, c accounts.Config) (api.GitAccounts, error) {
	if c.Accounts == nil {
		c.Accounts = []accounts.Account{}
	}
	if c.Rules == nil {
		c.Rules = []accounts.Rule{}
	}
	if c.Overrides == nil {
		c.Overrides = map[string]string{}
	}
	out := api.GitAccounts{Config: c, Logins: []accounts.Login{}, Projects: []ui.ProjectAccount{}}
	if ls, err := s.App.Logins(r.Context()); err != nil {
		out.LoginError = err.Error()
	} else if ls != nil {
		out.Logins = ls
	}
	names, err := state.ListProjects(s.stateDir())
	if err != nil {
		return out, err
	}
	sort.Strings(names)
	for _, name := range names {
		p, err := s.App.LoadProject(name)
		if err != nil {
			s.Log("accounts: %s: %v", name, err)
			continue
		}
		out.Projects = append(out.Projects, ui.ProjectAccount{Project: name, Origin: s.App.ProjectOrigin(p)})
	}
	return out, nil
}
