package accounts

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func sample() Config {
	return Config{
		Default: "personal",
		Accounts: []Account{
			{ID: "personal", Login: "dinhhuy258", Name: "Huy", Email: "h@x.dev"},
			{ID: "work", Login: "huy-acme", Email: "huy@acme.io", SSHKey: "~/.ssh/id_acme"},
			{ID: "bot", Login: "acme-release", Host: "ghe.acme.io", SigningKey: "~/.ssh/sign.pub"},
		},
		Rules: []Rule{
			{Owner: "acme-corp/*", Account: "work"},
			{Owner: "ghe.acme.io/*", Account: "bot"},
		},
		Overrides: map[string]string{"dotfiles": "work", "side": "personal"},
	}
}

func TestParseOwner(t *testing.T) {
	for in, want := range map[string]Pattern{
		"acme-corp/*":        {Host: "github.com", Owner: "acme-corp"},
		"ghe.acme.io/*":      {Host: "ghe.acme.io"},
		"GHE.acme.io/team/*": {Host: "ghe.acme.io", Owner: "team"},
	} {
		if got, err := ParseOwner(in); err != nil || got != want {
			t.Errorf("%s = %+v %v, want %+v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "acme", "acme/repo", "*", "a/b/c/*", "acme corp/*", "/*"} {
		if _, err := ParseOwner(bad); err == nil {
			t.Errorf("%q: want an error", bad)
		}
	}
}

func TestParseRemote(t *testing.T) {
	for in, want := range map[string]Origin{
		"git@github.com:Acme-Corp/api.git":          {"github.com", "Acme-Corp", "api"},
		"https://github.com/acme-corp/api":          {"github.com", "acme-corp", "api"},
		"https://x:tok@GHE.acme.io:8443/team/a.git": {"ghe.acme.io", "team", "a"},
		"ssh://git@ghe.acme.io/team/a.git":          {"ghe.acme.io", "team", "a"},
		"/srv/repos/a.git":                          {},
		"file:///srv/a/b":                           {},
		"https://github.com/only":                   {},
	} {
		if got := ParseRemote(in); got != want {
			t.Errorf("%s = %+v, want %+v", in, got, want)
		}
	}
	if s := (Origin{"github.com", "a", "b"}).String(); s != "a/b" {
		t.Error(s)
	}
	if s := (Origin{"ghe.acme.io", "a", "b"}).String(); s != "ghe.acme.io/a/b" {
		t.Error(s)
	}
}

func TestResolve(t *testing.T) {
	c := sample()
	gh := func(owner string) Origin { return Origin{"github.com", owner, "r"} }
	for _, tc := range []struct {
		project string
		o       Origin
		id, why string
	}{
		{"dotfiles", gh("someone"), "work", ByOverride},
		{"side", gh("acme-corp"), "personal", ByOverride},
		{"api", gh("ACME-corp"), "work", ByRule},
		{"tool", Origin{"ghe.acme.io", "x", "y"}, "bot", ByRule},
		{"bx", gh("dinhhuy258"), "personal", ByDefault},
		{"ws", Origin{}, "personal", ByDefault},
	} {
		r := c.Resolve(tc.project, tc.o)
		if !r.OK || r.Account.ID != tc.id || r.Reason != tc.why {
			t.Errorf("%s: %+v, want %s by %s", tc.project, r, tc.id, tc.why)
		}
	}
	c.Default = ""
	if r := c.Resolve("bx", gh("dinhhuy258")); r.OK {
		t.Errorf("no default: %+v", r)
	}
}

func TestValidate(t *testing.T) {
	if err := sample().Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mut := range map[string]func(*Config){
		"dup id":        func(c *Config) { c.Accounts = append(c.Accounts, Account{ID: "work", Login: "x"}) },
		"bad id":        func(c *Config) { c.Accounts[0].ID = "a b" },
		"no login":      func(c *Config) { c.Accounts[0].Login = " " },
		"newline":       func(c *Config) { c.Accounts[0].Email = "a\nb" },
		"bad host":      func(c *Config) { c.Accounts[0].Host = "a/b" },
		"bad default":   func(c *Config) { c.Default = "nope" },
		"bad rule":      func(c *Config) { c.Rules[0].Owner = "acme" },
		"rule account":  func(c *Config) { c.Rules[0].Account = "nope" },
		"override acct": func(c *Config) { c.Overrides["x"] = "nope" },
	} {
		c := sample()
		c.Accounts = slices.Clone(c.Accounts)
		mut(&c)
		if err := c.Validate(); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestLoadSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "git-accounts.json")
	if c, err := Load(path); err != nil || !c.Empty() {
		t.Fatalf("missing: %+v %v", c, err)
	}
	if err := Save(path, sample()); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil || len(c.Accounts) != 3 || c.Overrides["dotfiles"] != "work" || c.Default != "personal" {
		t.Fatalf("%+v %v", c, err)
	}
	bad := sample()
	bad.Default = "nope"
	if err := Save(path, bad); err == nil {
		t.Fatal("saved a bad config")
	}
	if err := os.WriteFile(path, []byte(`{"accounts":[{"id":"a"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "no gh login") {
		t.Fatalf("load bad: %v", err)
	}
}

func TestParseLogins(t *testing.T) {
	out := `{"hosts":{"github.com":[{"state":"success","active":true,"host":"github.com","login":"b","scopes":"repo"},
		{"state":"error","host":"github.com","login":"a"}],"GHE.acme.io":[{"state":"success","login":"c"}]}}`
	ls, err := ParseLogins([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	want := []Login{{"ghe.acme.io", "c", "", true}, {"github.com", "a", "", false}, {"github.com", "b", "repo", true}}
	if !slices.Equal(ls, want) {
		t.Fatalf("%+v", ls)
	}
	if !HasLogin(ls, Account{Login: "B"}) || HasLogin(ls, Account{Login: "a"}) || !HasLogin(ls, Account{Login: "c", Host: "ghe.acme.io"}) {
		t.Fatal("HasLogin")
	}
}

func TestEnv(t *testing.T) {
	c := sample()
	env := strings.Join(Env(c.Accounts[1], "tok", "/home/u"), "\n")
	for _, want := range []string{"BX_ACCOUNT=work", "GH_HOST=github.com", "GH_TOKEN=tok", "GIT_AUTHOR_EMAIL=huy@acme.io", "GIT_COMMITTER_EMAIL=huy@acme.io",
		"GIT_SSH_COMMAND=ssh -i /home/u/.ssh/id_acme -o IdentitiesOnly=yes", "GIT_CONFIG_COUNT=2",
		"GIT_CONFIG_KEY_0=credential.https://github.com.helper", "GIT_CONFIG_VALUE_0=", "GIT_CONFIG_VALUE_1=!gh auth git-credential"} {
		if !strings.Contains(env, want+"\n") && !strings.HasSuffix(env, want) {
			t.Errorf("missing %q in\n%s", want, env)
		}
	}
	if strings.Contains(env, "GIT_AUTHOR_NAME") {
		t.Error("an empty name must keep git's own")
	}
	env = strings.Join(Env(c.Accounts[2], "tok", "/home/u"), "\n")
	for _, want := range []string{"GH_ENTERPRISE_TOKEN=tok", "GH_HOST=ghe.acme.io", "GIT_CONFIG_COUNT=5", "user.signingkey", "GIT_CONFIG_VALUE_4=ssh"} {
		if !strings.Contains(env, want) {
			t.Errorf("missing %q in\n%s", want, env)
		}
	}
	if q := shellQuote("/a b/it's"); q != `'/a b/it'\''s'` {
		t.Error(q)
	}
}
