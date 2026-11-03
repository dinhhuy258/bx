// Package accounts is the GitHub accounts of the Git settings: which gh login, commit name and
// email each project uses. It lives in <root>/git-accounts.json and holds no token: gh keeps
// every login in its keyring, and a session gets the token of its account from
// `gh auth token --user <login>` when it starts.
package accounts

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

// DefaultHost is the host of an account or rule that names none.
const DefaultHost = "github.com"

// Account is one gh login and the identity its commits carry. Name and Email "" keep git's own
// config; SSHKey is used for ssh origins only; SigningKey signs every commit.
type Account struct {
	ID         string `json:"id"`
	Login      string `json:"login"`
	Host       string `json:"host,omitempty"`
	Name       string `json:"name,omitempty"`
	Email      string `json:"email,omitempty"`
	SSHKey     string `json:"ssh_key,omitempty"`
	SigningKey string `json:"signing_key,omitempty"`
}

// HostOrDefault is the account's host, github.com when it names none.
func (a Account) HostOrDefault() string {
	if a.Host == "" {
		return DefaultHost
	}
	return a.Host
}

// Rule gives every project whose origin matches Owner to Account. Owner is "owner/*" (on
// github.com), "host/*" or "host/owner/*".
type Rule struct {
	Owner   string `json:"owner"`
	Account string `json:"account"`
}

// Config is the file: the accounts, the rules (first match wins), the project overrides
// (project name to account id, beating the rules) and the default account ("" = none: gh's
// active login, as before accounts).
type Config struct {
	Default   string            `json:"default_account,omitempty"`
	Accounts  []Account         `json:"accounts"`
	Rules     []Rule            `json:"account_rules"`
	Overrides map[string]string `json:"account_overrides"`
}

// Empty reports whether no account is set: bx then leaves gh and git as they are.
func (c Config) Empty() bool { return len(c.Accounts) == 0 }

// Account finds an account by id.
func (c Config) Account(id string) (Account, bool) {
	i := slices.IndexFunc(c.Accounts, func(a Account) bool { return a.ID == id })
	if i < 0 {
		return Account{}, false
	}
	return c.Accounts[i], true
}

var idRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

// Validate checks the whole config: unique ids, a login on each account, one-line names, rules
// of a known shape, and only known accounts named by rules, overrides and the default.
func (c Config) Validate() error {
	seen := map[string]bool{}
	for _, a := range c.Accounts {
		if !idRe.MatchString(a.ID) {
			return fmt.Errorf("account %q: use letters, digits, - and _", a.ID)
		}
		if seen[a.ID] {
			return fmt.Errorf("account %q twice", a.ID)
		}
		seen[a.ID] = true
		if strings.TrimSpace(a.Login) == "" {
			return fmt.Errorf("account %s: no gh login", a.ID)
		}
		for _, v := range []string{a.Login, a.Host, a.Name, a.Email, a.SSHKey, a.SigningKey} {
			if strings.IndexFunc(v, unicode.IsControl) >= 0 {
				return fmt.Errorf("account %s: want one line of text", a.ID)
			}
		}
		if strings.ContainsAny(a.Host, "/ ") {
			return fmt.Errorf("account %s: host %q is not a host name", a.ID, a.Host)
		}
	}
	known := func(what, id string) error {
		if !seen[id] {
			return fmt.Errorf("%s: no account %q", what, id)
		}
		return nil
	}
	if c.Default != "" {
		if err := known("default account", c.Default); err != nil {
			return err
		}
	}
	for _, r := range c.Rules {
		if _, err := ParseOwner(r.Owner); err != nil {
			return err
		}
		if err := known("rule "+r.Owner, r.Account); err != nil {
			return err
		}
	}
	for p, id := range c.Overrides {
		if err := known("project "+p, id); err != nil {
			return err
		}
	}
	return nil
}

// Pattern is a parsed rule owner. Owner "" matches every owner of Host.
type Pattern struct{ Host, Owner string }

// ParseOwner reads "owner/*", "host/*" or "host/owner/*". A first part with a dot is a host.
func ParseOwner(s string) (Pattern, error) {
	bad := fmt.Errorf("rule %q: want owner/*, host/* or host/owner/*", s)
	rest, ok := strings.CutSuffix(strings.TrimSpace(s), "/*")
	if !ok || rest == "" {
		return Pattern{}, bad
	}
	parts := strings.Split(rest, "/")
	for _, p := range parts {
		if p == "" || strings.ContainsAny(p, " *") {
			return Pattern{}, bad
		}
	}
	switch {
	case len(parts) == 1 && strings.Contains(parts[0], "."):
		return Pattern{Host: strings.ToLower(parts[0])}, nil
	case len(parts) == 1:
		return Pattern{Host: DefaultHost, Owner: parts[0]}, nil
	case len(parts) == 2 && strings.Contains(parts[0], "."):
		return Pattern{Host: strings.ToLower(parts[0]), Owner: parts[1]}, nil
	}
	return Pattern{}, bad
}

// Match reports whether an origin on host, owned by owner, matches.
func (p Pattern) Match(host, owner string) bool {
	return host != "" && strings.EqualFold(p.Host, host) && (p.Owner == "" || strings.EqualFold(p.Owner, owner))
}

// Origin is where a project's repository lives: "github.com", "acme-corp". Zero for a project
// with no remote, or a workspace.
type Origin struct{ Host, Owner, Repo string }

// String is host/owner/repo, with github.com left out; "" for none.
func (o Origin) String() string {
	if o.Host == "" {
		return ""
	}
	s := o.Owner + "/" + o.Repo
	if !strings.EqualFold(o.Host, DefaultHost) {
		s = o.Host + "/" + s
	}
	return s
}

// ParseRemote reads a remote URL: git@host:owner/repo.git, https://host/owner/repo,
// ssh://git@host/owner/repo.git. A local path or anything else is the zero Origin.
func ParseRemote(raw string) Origin {
	raw = strings.TrimSpace(raw)
	var host, path string
	switch {
	case strings.Contains(raw, "://"):
		scheme, rest, _ := strings.Cut(raw, "://")
		if scheme == "file" {
			return Origin{}
		}
		host, path, _ = strings.Cut(rest, "/")
		if at := strings.LastIndex(host, "@"); at >= 0 {
			host = host[at+1:]
		}
		if h, _, ok := strings.Cut(host, ":"); ok {
			host = h // a port
		}
	case strings.Contains(raw, "@") && strings.Contains(raw, ":"):
		at := strings.Index(raw, "@")
		host, path, _ = strings.Cut(raw[at+1:], ":")
	default:
		return Origin{}
	}
	parts := strings.Split(strings.Trim(strings.TrimSuffix(strings.Trim(path, "/"), ".git"), "/"), "/")
	if host == "" || len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return Origin{}
	}
	return Origin{Host: strings.ToLower(host), Owner: parts[0], Repo: parts[1]}
}

// Resolve picks the account of project, whose origin is o: its override, else the first rule
// that matches o, else the default. false when none applies.
func (c Config) Resolve(project string, o Origin) (Account, bool) {
	if id, ok := c.Overrides[project]; ok {
		if a, ok := c.Account(id); ok {
			return a, true
		}
	}
	if r, ok := c.MatchRule(o); ok {
		if a, ok := c.Account(r.Account); ok {
			return a, true
		}
	}
	return c.Account(c.Default)
}

// MatchRule is the first rule that matches o.
func (c Config) MatchRule(o Origin) (Rule, bool) {
	for _, r := range c.Rules {
		if p, err := ParseOwner(r.Owner); err == nil && p.Match(o.Host, o.Owner) {
			return r, true
		}
	}
	return Rule{}, false
}

// Load reads path. A missing file is the empty config.
func Load(path string) (Config, error) {
	c := Config{}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, fmt.Errorf("read accounts: %w", err)
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	if err := c.Validate(); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// Save checks c and writes it atomically: temp file in the same folder, then rename.
func Save(path string, c Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if c.Accounts == nil {
		c.Accounts = []Account{}
	}
	if c.Rules == nil {
		c.Rules = []Rule{}
	}
	if c.Overrides == nil {
		c.Overrides = map[string]string{}
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}

// Login is a gh login on a host, from `gh auth status --json hosts`. OK is false when gh
// reports its token as bad; Active is the login gh uses on its host when nothing says which.
type Login struct {
	Host   string `json:"host"`
	Login  string `json:"login"`
	Scopes string `json:"scopes,omitempty"`
	OK     bool   `json:"ok"`
	Active bool   `json:"active,omitempty"`
}

// ParseLogins reads the output of `gh auth status --json hosts`, sorted by host then login.
func ParseLogins(out []byte) ([]Login, error) {
	var raw struct {
		Hosts map[string][]struct {
			State  string `json:"state"`
			Active bool   `json:"active"`
			Host   string `json:"host"`
			Login  string `json:"login"`
			Scopes string `json:"scopes"`
		} `json:"hosts"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("gh auth status: %w", err)
	}
	var list []Login
	for host, ls := range raw.Hosts {
		for _, l := range ls {
			if l.Login == "" {
				continue
			}
			h := l.Host
			if h == "" {
				h = host
			}
			list = append(list, Login{Host: strings.ToLower(h), Login: l.Login, Scopes: l.Scopes, OK: l.State == "success", Active: l.Active})
		}
	}
	slices.SortFunc(list, func(a, b Login) int {
		if c := strings.Compare(a.Host, b.Host); c != 0 {
			return c
		}
		return strings.Compare(a.Login, b.Login)
	})
	return list, nil
}

// HasLogin reports whether logins hold a working login for a.
func HasLogin(logins []Login, a Account) bool {
	return slices.ContainsFunc(logins, func(l Login) bool {
		return l.OK && strings.EqualFold(l.Host, a.HostOrDefault()) && strings.EqualFold(l.Login, a.Login)
	})
}

// Env is the environment of a process that works as a: the token for gh and for git over
// https (through gh's credential helper), the commit identity, the SSH key and the signing key.
// home expands a leading ~/ in the key paths.
func Env(a Account, token, home string) []string {
	host := a.HostOrDefault()
	env := []string{"BX_ACCOUNT=" + a.ID, "GH_HOST=" + host}
	if strings.EqualFold(host, DefaultHost) {
		env = append(env, "GH_TOKEN="+token)
	} else {
		env = append(env, "GH_ENTERPRISE_TOKEN="+token)
	}
	if a.Name != "" {
		env = append(env, "GIT_AUTHOR_NAME="+a.Name, "GIT_COMMITTER_NAME="+a.Name)
	}
	if a.Email != "" {
		env = append(env, "GIT_AUTHOR_EMAIL="+a.Email, "GIT_COMMITTER_EMAIL="+a.Email)
	}
	if a.SSHKey != "" {
		env = append(env, "GIT_SSH_COMMAND=ssh -i "+shellQuote(expand(a.SSHKey, home))+" -o IdentitiesOnly=yes")
	}
	// an empty helper first drops the helpers of the user's git config for this host
	helper := "credential.https://" + host + ".helper"
	cfg := [][2]string{{helper, ""}, {helper, "!gh auth git-credential"}}
	if a.SigningKey != "" {
		key := expand(a.SigningKey, home)
		cfg = append(cfg, [2]string{"user.signingkey", key}, [2]string{"commit.gpgsign", "true"})
		if strings.HasSuffix(key, ".pub") {
			cfg = append(cfg, [2]string{"gpg.format", "ssh"})
		}
	}
	env = append(env, fmt.Sprintf("GIT_CONFIG_COUNT=%d", len(cfg)))
	for i, kv := range cfg {
		env = append(env, fmt.Sprintf("GIT_CONFIG_KEY_%d=%s", i, kv[0]), fmt.Sprintf("GIT_CONFIG_VALUE_%d=%s", i, kv[1]))
	}
	return env
}

func expand(p, home string) string {
	if rest, ok := strings.CutPrefix(p, "~/"); ok && home != "" {
		return filepath.Join(home, rest)
	}
	return p
}

// shellQuote quotes s for the sh that git runs GIT_SSH_COMMAND with.
func shellQuote(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune("/._-+:@", r)
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
