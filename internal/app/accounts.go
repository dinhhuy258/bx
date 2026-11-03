package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dinhhuy258/bx/internal/accounts"
	"github.com/dinhhuy258/bx/internal/state"
)

// tokenTTL is how long a token from gh is reused before gh is asked again.
const tokenTTL = 5 * time.Minute

// tokens caches `gh auth token --user` by host and login.
type tokens struct {
	mu   sync.Mutex
	byID map[string]cachedToken
}

type cachedToken struct {
	token string
	at    time.Time
}

// Accounts reads the GitHub accounts of the Git settings.
func (a *App) Accounts() (accounts.Config, error) { return accounts.Load(a.Cfg.AccountsFile()) }

// SaveAccounts checks and writes c. Cached tokens are dropped, so a login added to gh since is
// picked up.
func (a *App) SaveAccounts(c accounts.Config) error {
	if err := accounts.Save(a.Cfg.AccountsFile(), c); err != nil {
		return err
	}
	a.tok.mu.Lock()
	a.tok.byID = nil
	a.tok.mu.Unlock()
	return nil
}

// Logins are the gh logins on this machine.
func (a *App) Logins(ctx context.Context) ([]accounts.Login, error) {
	if a.GH == nil {
		return nil, errors.New("gh is not set up")
	}
	out, err := a.GH(ctx, "", "auth", "status", "--json", "hosts")
	if err != nil {
		return nil, err
	}
	return accounts.ParseLogins([]byte(out))
}

// ProjectOrigin is where the repository of p lives; zero for a workspace or a repo without a
// remote.
func (a *App) ProjectOrigin(p *state.Project) accounts.Origin {
	if p.IsWorkspace() {
		return accounts.Origin{}
	}
	return accounts.ParseRemote(a.Git.OriginURL(p.Path))
}

// accountFor is the account of project for the folder dir, whose origin the rules match;
// false when none applies.
func (a *App) accountFor(project, dir string) (accounts.Account, bool, error) {
	c, err := a.Accounts()
	if err != nil || c.Empty() {
		return accounts.Account{}, false, err
	}
	var o accounts.Origin
	if a.Git.IsRepo(dir) {
		o = accounts.ParseRemote(a.Git.OriginURL(dir))
	}
	acct, ok := c.Resolve(project, o)
	return acct, ok, nil
}

// accountEnv is the environment of a process of project in dir: its account's token and
// identity (see accounts.Env). nil when no account applies; an error when gh has no token for
// it, so nothing runs as the wrong person.
func (a *App) accountEnv(ctx context.Context, project, dir string) ([]string, error) {
	acct, ok, err := a.accountFor(project, dir)
	if err != nil || !ok {
		return nil, err
	}
	return a.envOf(ctx, acct)
}

func (a *App) envOf(ctx context.Context, acct accounts.Account) ([]string, error) {
	tok, err := a.token(ctx, acct)
	if err != nil {
		return nil, err
	}
	home, _ := os.UserHomeDir()
	return accounts.Env(acct, tok, home), nil
}

// token is the gh token of acct, from the cache or from gh.
func (a *App) token(ctx context.Context, acct accounts.Account) (string, error) {
	key := acct.HostOrDefault() + "/" + strings.ToLower(acct.Login)
	a.tok.mu.Lock()
	c, ok := a.tok.byID[key]
	a.tok.mu.Unlock()
	if ok && time.Since(c.at) < tokenTTL {
		return c.token, nil
	}
	out, err := a.GH(ctx, "", "auth", "token", "--hostname", acct.HostOrDefault(), "--user", acct.Login)
	tok := strings.TrimSpace(out)
	if err != nil || tok == "" {
		return "", fmt.Errorf("account %s: gh has no login %s on %s · run gh auth login --hostname %s",
			acct.ID, acct.Login, acct.HostOrDefault(), acct.HostOrDefault())
	}
	a.tok.mu.Lock()
	if a.tok.byID == nil {
		a.tok.byID = map[string]cachedToken{}
	}
	a.tok.byID[key] = cachedToken{tok, time.Now()}
	a.tok.mu.Unlock()
	return tok, nil
}

// envForDir is the account environment of git and gh run in dir by bx itself (fetch, set-head,
// pull requests), found through the project dir belongs to. A failure is logged and leaves the
// environment as it is: gh's active login.
func (a *App) envForDir(ctx context.Context, dir string) []string {
	if dir == "" {
		return nil
	}
	c, err := a.Accounts()
	if err != nil || c.Empty() {
		return nil
	}
	env, err := a.accountEnv(ctx, a.projectOfDir(dir), dir)
	if err != nil {
		a.logf("account: %s: %v", dir, err)
		return nil
	}
	return env
}

// projectOfDir is the project whose folder or worktrees hold dir, "" for none.
func (a *App) projectOfDir(dir string) string {
	dir = filepath.Clean(dir)
	wt := filepath.Join(a.Cfg.Root, "worktrees") + string(filepath.Separator)
	if rest, ok := strings.CutPrefix(dir, wt); ok {
		name, _, _ := strings.Cut(rest, string(filepath.Separator))
		return name
	}
	names, err := state.ListProjects(filepath.Join(a.Cfg.Root, "state"))
	if err != nil {
		return ""
	}
	for _, name := range names {
		p, err := a.LoadProject(name)
		if err != nil {
			continue
		}
		if dir == p.Path || strings.HasPrefix(dir, p.Path+string(filepath.Separator)) {
			return name
		}
	}
	return ""
}

// envKey carries the environment of one gh call in its context (see withEnv).
type envKey struct{}

// withEnv gives the gh calls made with ctx the environment env, for gh run outside a project
// folder on behalf of an account: a clone, a repository list.
func withEnv(ctx context.Context, env []string) context.Context {
	return context.WithValue(ctx, envKey{}, env)
}

// ghEnv is the environment of a gh call: the one ctx carries, else the account of dir.
func (a *App) ghEnv(ctx context.Context, dir string) []string {
	if env, ok := ctx.Value(envKey{}).([]string); ok {
		return env
	}
	return a.envForDir(ctx, dir)
}
