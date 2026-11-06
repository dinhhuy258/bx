package app

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/dinhhuy258/bx/internal/accounts"
	"github.com/dinhhuy258/bx/internal/api"
)

// repoPage is how many repositories one GitHubRepos call returns: a page, not the whole owner,
// so the clone picker opens fast; a search finds the rest.
const repoPage = 30

// ownerRe is a GitHub user or organization login. It also keeps an owner from reading as a
// gh flag.
var ownerRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*$`)

// ghOwners remembers which account's gh environment reaches each owner of GitHubOwners, by
// lowercased login, for the GitHubRepos calls that follow.
type ghOwners struct {
	mu  sync.Mutex
	env map[string][]string
}

// githubEnvs are the gh environments of the github.com accounts, or the one of plain gh when
// none is set (or none has a token).
func (a *App) githubEnvs(ctx context.Context) ([]accounts.Account, [][]string, error) {
	c, err := a.Accounts()
	if err != nil {
		return nil, nil, err
	}
	var accts []accounts.Account
	var envs [][]string
	for _, acct := range c.Accounts {
		if acct.HostOrDefault() != accounts.DefaultHost {
			continue
		}
		env, err := a.envOf(ctx, acct)
		if err != nil {
			a.logf("github: %v", err)
			continue
		}
		accts, envs = append(accts, acct), append(envs, env)
	}
	if len(envs) == 0 {
		return []accounts.Account{{}}, [][]string{nil}, nil
	}
	return accts, envs, nil
}

// GitHubOwners lists the owners a clone can pick from: the login of each github.com account,
// then the organizations it belongs to. An account gh fails for is skipped.
func (a *App) GitHubOwners(ctx context.Context) ([]string, error) {
	accts, envs, err := a.githubEnvs(ctx)
	if err != nil {
		return nil, err
	}
	byOwner := map[string][]string{}
	var logins, orgs []string
	var firstErr error
	for i, env := range envs {
		ectx := withEnv(ctx, env)
		login := accts[i].Login
		if login == "" {
			out, err := a.GH(ectx, "", "api", "user", "--jq", ".login")
			if err != nil {
				firstErr = cmp.Or(firstErr, err)
				continue
			}
			login = strings.TrimSpace(out)
		}
		if _, seen := byOwner[strings.ToLower(login)]; login != "" && !seen {
			byOwner[strings.ToLower(login)] = env
			logins = append(logins, login)
		}
		out, err := a.GH(ectx, "", "api", "user/orgs", "--paginate", "--jq", ".[].login")
		if err != nil {
			a.logf("github orgs of %s: %v", login, err)
			continue
		}
		for _, o := range strings.Split(out, "\n") {
			if o = strings.TrimSpace(o); o == "" {
				continue
			}
			if _, seen := byOwner[strings.ToLower(o)]; !seen {
				byOwner[strings.ToLower(o)] = env
				orgs = append(orgs, o)
			}
		}
	}
	if len(logins) == 0 && firstErr != nil {
		return nil, firstErr
	}
	a.owners.mu.Lock()
	a.owners.env = byOwner
	a.owners.mu.Unlock()
	return append(logins, orgs...), nil
}

// ownerEnv is the gh environment that reaches owner, as GitHubOwners found it.
func (a *App) ownerEnv(ctx context.Context, owner string) []string {
	k := strings.ToLower(owner)
	a.owners.mu.Lock()
	env, ok := a.owners.env[k]
	known := a.owners.env != nil
	a.owners.mu.Unlock()
	if ok || known {
		return env
	}
	if _, err := a.GitHubOwners(ctx); err != nil {
		return nil
	}
	a.owners.mu.Lock()
	defer a.owners.mu.Unlock()
	return a.owners.env[k]
}

// GitHubRepos lists a page of owner's repositories: the most recently pushed with q empty,
// else those whose name matches q (a GitHub search, so a word prefix matches: "lang" finds
// heroai-langsmith).
func (a *App) GitHubRepos(ctx context.Context, owner, q string) ([]api.GitHubRepo, error) {
	if !ownerRe.MatchString(owner) {
		return nil, fmt.Errorf("owner %q: not a GitHub login", owner)
	}
	ctx = withEnv(ctx, a.ownerEnv(ctx, owner))
	limit := fmt.Sprint(repoPage)
	var out string
	var err error
	if q = strings.TrimSpace(q); q == "" {
		out, err = a.GH(ctx, "", "repo", "list", owner, "--limit", limit, "--json", "nameWithOwner,description,isPrivate,pushedAt")
	} else {
		out, err = a.GH(ctx, "", "search", "repos", "--owner", owner, "--match", "name", "--limit", limit,
			"--json", "fullName,description,isPrivate,pushedAt", "--", q)
	}
	if err != nil {
		if strings.Contains(err.Error(), "SAML") {
			return nil, fmt.Errorf("%s needs SAML single sign-on: authorize gh for it on github.com (Settings › Applications)", owner)
		}
		return nil, err
	}
	return parseRepos(out)
}

// parseRepos reads the JSON of gh repo list or gh search repos.
func parseRepos(out string) ([]api.GitHubRepo, error) {
	var rows []struct {
		NameWithOwner string    `json:"nameWithOwner"`
		FullName      string    `json:"fullName"`
		Description   string    `json:"description"`
		IsPrivate     bool      `json:"isPrivate"`
		PushedAt      time.Time `json:"pushedAt"`
	}
	if strings.TrimSpace(out) == "" {
		return []api.GitHubRepo{}, nil
	}
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		return nil, errors.New("gh: unexpected repository list")
	}
	repos := make([]api.GitHubRepo, 0, len(rows))
	for _, r := range rows {
		g := api.GitHubRepo{Name: cmp.Or(r.NameWithOwner, r.FullName), Description: r.Description, Private: r.IsPrivate}
		if !r.PushedAt.IsZero() {
			g.Pushed = r.PushedAt.Unix()
		}
		repos = append(repos, g)
	}
	return repos, nil
}
