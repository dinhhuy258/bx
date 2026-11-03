package app

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/dinhhuy258/bx/internal/accounts"
)

// withAccounts gives the app of newApp a work account for acme-corp/* and a fake gh that knows
// only the login huy-acme. The repo's origin is acme-corp/api.
func withAccounts(t *testing.T, a *App) *[]string {
	t.Helper()
	c := exec.Command("git", "remote", "add", "origin", "git@github.com:acme-corp/api.git")
	c.Dir = a.Cfg.ProjectDir("home-network")
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	err := a.SaveAccounts(accounts.Config{
		Default: "personal",
		Accounts: []accounts.Account{
			{ID: "personal", Login: "me", Email: "me@x.dev"},
			{ID: "work", Login: "huy-acme", Name: "Huy", Email: "huy@acme.io"},
		},
		Rules: []accounts.Rule{{Owner: "acme-corp/*", Account: "work"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var gh []string
	a.GH = func(ctx context.Context, dir string, args ...string) (string, error) {
		gh = append(gh, strings.Join(args, " "))
		if strings.Join(args[:2], " ") == "auth token" && args[len(args)-1] == "huy-acme" {
			return "tok-work\n", nil
		}
		return "", errors.New("no such login")
	}
	return &gh
}

func TestSessionAndShellRunAsTheProjectAccount(t *testing.T) {
	a, f, p := newApp(t)
	gh := withAccounts(t, a)
	if _, err := a.NewChat(p, ""); err != nil {
		t.Fatal(err)
	}
	got := calls(f)
	for _, want := range []string{"-e BX_ACCOUNT=work", "-e GH_TOKEN=tok-work", "-e GIT_AUTHOR_EMAIL=huy@acme.io", "-e GIT_CONFIG_COUNT=2"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if strings.Contains(got, "claude --session-id fixed-uuid tok-work") || strings.Count(got, "tok-work") != 1 {
		t.Errorf("the token must only be in the env:\n%s", got)
	}
	if _, err := a.NewShell(p, ""); err != nil || !strings.Contains(calls(f), "-e GIT_PAGER=cat -e BX_ACCOUNT=work") {
		t.Fatalf("shell %v:\n%s", err, calls(f))
	}
	if n := len(*gh); n != 1 {
		t.Errorf("the token is cached; gh ran %d times: %v", n, *gh)
	}
}

func TestSessionRefusesAnAccountGhDoesNotKnow(t *testing.T) {
	a, f, p := newApp(t)
	withAccounts(t, a)
	c, _ := a.Accounts()
	c.Overrides = map[string]string{"home-network": "personal"}
	if err := a.SaveAccounts(c); err != nil {
		t.Fatal(err)
	}
	_, err := a.NewChat(p, "")
	if err == nil || !strings.Contains(err.Error(), "account personal: gh has no login me on github.com · run gh auth login --hostname github.com") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(calls(f), "new-session") || len(p.Sessions) != 0 {
		t.Fatalf("started anyway:\n%s %+v", calls(f), p.Sessions)
	}
}

func TestNoAccountsLeavesSessionsAlone(t *testing.T) {
	a, f, p := newApp(t)
	if _, err := a.NewChat(p, ""); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(calls(f), " -e ") {
		t.Fatalf("env without accounts:\n%s", calls(f))
	}
	if env := a.envForDir(context.Background(), p.Path); env != nil {
		t.Fatalf("env %v", env)
	}
}

func TestEnvForDirFindsTheProject(t *testing.T) {
	a, _, p := newApp(t)
	withAccounts(t, a)
	if got := a.projectOfDir(a.Cfg.WorktreeDir("home-network", "s1") + "/api"); got != "home-network" {
		t.Fatal(got)
	}
	if got := a.projectOfDir(p.Path + "/sub"); got != "home-network" {
		t.Fatal(got)
	}
	if got := a.projectOfDir("/elsewhere"); got != "" {
		t.Fatal(got)
	}
	env := strings.Join(a.envForDir(context.Background(), p.Path), " ")
	if !strings.Contains(env, "BX_ACCOUNT=work") {
		t.Fatalf("env %s", env)
	}
	if o := a.ProjectOrigin(p); o.String() != "acme-corp/api" {
		t.Fatalf("origin %+v", o)
	}
}
