package ui

import "github.com/dinhhuy258/bx/internal/accounts"

// GitAccounts is what the GitHub block of the Git section shows: the config, gh's logins
// (LoginError says why they could not be read) and the account each project resolves to.
type GitAccounts struct {
	Config     accounts.Config  `json:"config"`
	Logins     []accounts.Login `json:"logins"`
	LoginError string           `json:"login_error,omitempty"`
	Projects   []ProjectAccount `json:"projects"`
}

// ProjectAccount is a project, its origin and the account it uses ("" = none) and why.
type ProjectAccount struct {
	Project string          `json:"project"`
	Origin  accounts.Origin `json:"origin"`
	Account string          `json:"account,omitempty"`
	Reason  string          `json:"reason,omitempty"`
	Rule    string          `json:"rule,omitempty"`
}
