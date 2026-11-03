package client

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/dinhhuy258/bx/internal/accounts"
	"github.com/dinhhuy258/bx/internal/api"
	"github.com/dinhhuy258/bx/internal/ui"
)

// accountsSettings opens settings › git with two gh logins and the given config.
func accountsSettings(t *testing.T, c accounts.Config) *harness {
	t.Helper()
	h := start(t, 120, 40, "running", "running")
	h.be.acc = api.GitAccounts{
		Config: c,
		Logins: []accounts.Login{{Host: "github.com", Login: "dinhhuy258", OK: true}, {Host: "github.com", Login: "huy-acme", OK: true, Scopes: "repo"}},
		Projects: []ui.ProjectAccount{
			{Project: "home-network", Origin: accounts.Origin{Host: "github.com", Owner: "acme-corp", Repo: "net"}},
			{Project: "lab", Origin: accounts.Origin{Host: "github.com", Owner: "dinhhuy258", Repo: "lab"}},
		},
	}
	h.press("P", "Tab")
	return h
}

// gotoRow moves the cursor of the git section to the first row of kind.
func gotoRow(t *testing.T, h *harness, kind ui.GitRowKind) {
	t.Helper()
	for range 30 {
		h.press("k")
	}
	for range 30 {
		if r, _ := h.m.settings.GitRow(); r.Kind == kind {
			return
		}
		h.press("j")
	}
	t.Fatalf("no row of kind %d", kind)
}

func TestSettingsGitAddsAnAccount(t *testing.T) {
	h := accountsSettings(t, accounts.Config{})
	v := ansi.Strip(h.m.View().Content)
	for _, want := range []string{"Base branches", "GitHub accounts", "No accounts. Sessions use gh's active login", "+ add account", "+ add rule", "project overrides · 0 of 2 projects"} {
		if !strings.Contains(v, want) {
			t.Fatalf("no %q:\n%s", want, v)
		}
	}
	gotoRow(t, h, ui.RowAccountAdd)
	h.press("Enter")
	if h.m.pickKind != pickAccountLogin || h.m.picker == nil {
		t.Fatal("Enter on + add account opens the gh logins")
	}
	h.typeText("acme")
	h.press("Enter")
	a, def, isNew, _, ok := h.m.settings.AccountForm()
	if !ok || !isNew || !def || a.ID != "huy-acme" || a.Login != "huy-acme" {
		t.Fatalf("form = %+v def=%v new=%v ok=%v", a, def, isNew, ok)
	}
	v = ansi.Strip(h.m.View().Content)
	if !strings.Contains(v, "Git › new account") || !strings.Contains(v, "token scopes repo") || !strings.Contains(v, "ctrl-s save") {
		t.Fatalf("form view:\n%s", v)
	}
	h.press("Enter") // Label
	h.m.prompt.Input = ""
	h.typeText("work")
	h.press("Enter")
	h.press("j", "j", "j", "Enter") // Commit email
	h.typeText("huy@acme.io")
	h.press("Enter")
	h.press("C-s")
	if len(h.be.accSaves) != 1 {
		t.Fatalf("saves = %+v", h.be.accSaves)
	}
	c := h.be.accSaves[0]
	if len(c.Accounts) != 1 || c.Accounts[0] != (accounts.Account{ID: "work", Login: "huy-acme", Email: "huy@acme.io"}) || c.Default != "work" {
		t.Fatalf("saved %+v", c)
	}
	if _, _, _, _, open := h.m.settings.AccountForm(); open {
		t.Fatal("a save closes the form")
	}
	if v = ansi.Strip(h.m.View().Content); !strings.Contains(v, "work  huy-acme") || !strings.Contains(v, "✓ default") {
		t.Fatalf("account row:\n%s", v)
	}
}

func TestSettingsGitAccountFormEscAsks(t *testing.T) {
	h := accountsSettings(t, accounts.Config{Accounts: []accounts.Account{{ID: "work", Login: "huy-acme"}}})
	gotoRow(t, h, ui.RowAccount)
	h.press("Enter", "Esc")
	if _, _, _, _, open := h.m.settings.AccountForm(); open {
		t.Fatal("Esc with no edit goes back")
	}
	h.press("Enter", "j", "l") // gh login → the next login
	if a, _, _, _, _ := h.m.settings.AccountForm(); a.Login == "huy-acme" {
		t.Fatal("l steps the login")
	}
	h.press("Esc")
	if !strings.Contains(ansi.Strip(h.m.View().Content), "Discard account changes?") {
		t.Fatal("Esc with edits asks")
	}
	h.press("y")
	if _, _, _, _, open := h.m.settings.AccountForm(); open || len(h.be.accSaves) != 0 {
		t.Fatal("y discards without saving")
	}
}

func TestSettingsGitRulesAndOverrides(t *testing.T) {
	h := accountsSettings(t, accounts.Config{
		Default:  "personal",
		Accounts: []accounts.Account{{ID: "personal", Login: "dinhhuy258"}, {ID: "work", Login: "huy-acme"}},
	})
	gotoRow(t, h, ui.RowRuleAdd)
	h.press("Enter")
	h.typeText("acme-corp")
	h.press("Enter")
	if h.m.pickKind != pickRuleAccount || !strings.Contains(h.m.picker.Title, "acme-corp/* → account · matches 1 project") {
		t.Fatalf("rule picker: %+v", h.m.picker)
	}
	h.typeText("work")
	h.press("Enter")
	if c := h.be.acc.Config; len(c.Rules) != 1 || c.Rules[0] != (accounts.Rule{Owner: "acme-corp/*", Account: "work"}) {
		t.Fatalf("rules %+v", c.Rules)
	}
	gotoRow(t, h, ui.RowAccountOverrideAdd)
	h.press("Enter")
	h.typeText("lab")
	h.press("Enter")
	h.typeText("work")
	h.press("Enter")
	if got := h.be.acc.Config.Overrides["lab"]; got != "work" {
		t.Fatalf("override %q", got)
	}
	if v := ansi.Strip(h.m.View().Content); !strings.Contains(v, "lab  → work") || !strings.Contains(v, "origin dinhhuy258/lab · else personal") {
		t.Fatalf("override row:\n%s", v)
	}
	gotoRow(t, h, ui.RowAccount)
	h.press("j", "BSpace") // work
	if v := ansi.Strip(h.m.View().Content); !strings.Contains(v, "Remove account work?") || !strings.Contains(v, "Its 1 rule and 1 project override go too.") {
		t.Fatalf("remove asks:\n%s", v)
	}
	h.press("y")
	if c := h.be.acc.Config; len(c.Accounts) != 1 || len(c.Rules) != 0 || len(c.Overrides) != 0 || c.Default != "personal" {
		t.Fatalf("after remove %+v", c)
	}
}

func TestSettingsGitReordersRules(t *testing.T) {
	h := accountsSettings(t, accounts.Config{
		Accounts: []accounts.Account{{ID: "work", Login: "huy-acme"}},
		Rules:    []accounts.Rule{{Owner: "a/*", Account: "work"}, {Owner: "b/*", Account: "work"}},
	})
	gotoRow(t, h, ui.RowRule)
	h.press("K") // the first rule cannot go up
	if len(h.be.accSaves) != 0 {
		t.Fatal("K on the first rule saved")
	}
	h.press("J")
	if c := h.be.acc.Config; c.Rules[0].Owner != "b/*" {
		t.Fatalf("rules %+v", c.Rules)
	}
	if r, _ := h.m.settings.GitRow(); r.Kind != ui.RowRule || r.Index != 1 {
		t.Fatalf("the cursor follows the rule: %+v", r)
	}
}

func TestAccountID(t *testing.T) {
	for in, want := range map[string]string{"huy-acme": "huy-acme", "a.b": "a-b", "--": "account", "Ünï": "n"} {
		if got := accountID(in); got != want {
			t.Errorf("%s = %s, want %s", in, got, want)
		}
	}
}
