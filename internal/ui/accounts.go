package ui

import "github.com/dinhhuy258/bx/internal/accounts"

// GitAccounts is what the GitHub block of the Git section shows: the config, gh's logins
// (LoginError says why they could not be read) and the origin of every project, which the rules
// match.
type GitAccounts struct {
	Config     accounts.Config  `json:"config"`
	Logins     []accounts.Login `json:"logins"`
	LoginError string           `json:"login_error,omitempty"`
	Projects   []ProjectAccount `json:"projects"`
}

// ProjectAccount is a project and its origin (zero for a workspace or a repo without a remote).
type ProjectAccount struct {
	Project string          `json:"project"`
	Origin  accounts.Origin `json:"origin"`
}
