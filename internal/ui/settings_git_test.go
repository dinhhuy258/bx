package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/dinhhuy258/bx/internal/accounts"
	"github.com/dinhhuy258/bx/internal/settings"
)

// gitSettings is the sample screen on its git section, with two overrides.
func gitSettings() *Settings {
	s := sampleSettings()
	s.SetGitOverrides([]GitOverride{{Project: "infra", Base: "release/1.4"}, {Project: "api-server", Base: "develop"}})
	s.Key("Tab")
	return s
}

func TestSettingsGitSectionIsTheClients(t *testing.T) {
	s := sampleSettings()
	var ids []string
	for _, sec := range s.sections {
		ids = append(ids, sec.ID+" "+sec.Title)
	}
	if got := strings.Join(ids, ", "); got != "claude Claude, git Git, runs Runs, mcp MCP, notifications Notifications" {
		t.Fatalf("sections = %s", got)
	}
	for _, sec := range settings.Sections {
		if sec.ID == gitSection {
			t.Fatalf("the server's registry must not hold the git section: %v", settings.Sections)
		}
	}
	s.Load(settings.Sections, nil) // a second load adds it once
	if len(s.sections) != 5 {
		t.Fatalf("%d sections after a reload", len(s.sections))
	}
	if s.GitShown() {
		t.Fatal("the Claude section opens first")
	}
	if p, add := baseRow(s); p != "" || add {
		t.Fatalf("base row outside the section = %q %v", p, add)
	}
}

func TestSettingsGitRowsAndKeys(t *testing.T) {
	s := gitSettings()
	if !s.GitShown() {
		t.Fatal("Tab opens the git section")
	}
	if p, add := baseRow(s); p != "api-server" || add {
		t.Fatalf("first row = %q %v, want api-server (sorted)", p, add)
	}
	if s.Key("Enter") != SettingsGitChoose {
		t.Fatal("Enter on an override chooses, it does not save")
	}
	if s.Key("BSpace") != SettingsGitRemove {
		t.Fatal("Backspace on an override removes it")
	}
	if s.Key("r") != SettingsAccountsReload {
		t.Fatal("r rechecks gh's logins")
	}
	for _, k := range []string{"l", "h", "Space", "s"} {
		if got := s.Key(k); got != SettingsKeep {
			t.Fatalf("%s in the git section = %v", k, got)
		}
	}
	if len(s.Changes()) != 0 {
		t.Fatalf("h/l must not edit the hidden fields: %v", s.Changes())
	}
	s.Key("j")
	if p, _ := baseRow(s); p != "infra" {
		t.Fatalf("j → %q", p)
	}
	s.Key("Down")
	if p, add := baseRow(s); p != "" || !add {
		t.Fatalf("last row = %q %v, want the add row", p, add)
	}
	if s.Key("BSpace") != SettingsKeep {
		t.Fatal("Backspace on the add row does nothing")
	}
	if s.Key("Enter") != SettingsGitChoose {
		t.Fatal("Enter on the add row chooses")
	}
	s.Key("k")
	s.Key("Up")
	s.Key("k")
	if p, _ := baseRow(s); p != "api-server" {
		t.Fatalf("k → %q", p)
	}
	if s.Key("Esc") != SettingsClose {
		t.Fatal("Esc closes")
	}
	s.Key("Tab")
	if s.GitShown() || s.fld != 0 {
		t.Fatal("Tab goes back to Claude, on its first field")
	}
}

func TestSettingsGitCursorFollowsItsRow(t *testing.T) {
	s := gitSettings()
	s.Key("j") // infra
	s.SetGitOverrides([]GitOverride{{Project: "web", Base: "main"}, {Project: "infra", Base: "release/1.4"}, {Project: "api-server", Base: "develop"}})
	if p, _ := baseRow(s); p != "infra" {
		t.Fatalf("cursor on %q after an update", p)
	}
	s.Key("j")
	s.Key("j") // the add row
	s.SetGitOverrides([]GitOverride{{Project: "a", Base: "main"}, {Project: "web", Base: "main"}, {Project: "infra", Base: "release/1.4"}, {Project: "api-server", Base: "develop"}})
	if _, add := baseRow(s); !add {
		t.Fatal("the cursor stays on the add row")
	}
	s.Key("k") // web
	s.SetGitOverrides([]GitOverride{{Project: "a", Base: "main"}})
	if _, add := baseRow(s); !add {
		t.Fatal("its row is gone: the cursor is kept in range")
	}
	s.SyncFailed("offline") // a sync reply does not move the git cursor
	s.Load(nil, nil)
	if _, add := baseRow(s); !add {
		t.Fatal("a sync reply or a load moved the git cursor")
	}
}

func TestSettingsGitEmpty(t *testing.T) {
	s := sampleSettings()
	s.Key("Tab")
	got := s.Render(120, 30, true, "")
	for _, want := range []string{"Base branches", gitEmpty, "› + add override", "GitHub accounts", accountsEmpty} {
		if !strings.Contains(got, want) {
			t.Fatalf("no %q:\n%s", want, got)
		}
	}
	if s.Key("BSpace") != SettingsKeep || s.Key("Enter") != SettingsGitChoose {
		t.Fatal("with no override the add row is first")
	}
}

// sampleAccounts is the mockup's accounts: oss-bot's login is not in gh.
func sampleAccounts() GitAccounts {
	gh := func(owner, repo string) accounts.Origin {
		return accounts.Origin{Host: "github.com", Owner: owner, Repo: repo}
	}
	return GitAccounts{
		Config: accounts.Config{
			Default: "personal",
			Accounts: []accounts.Account{
				{ID: "personal", Login: "dinhhuy258", Name: "Huy Duong", Email: "alicuche@gmail.com"},
				{ID: "work", Login: "huy-acme", Name: "Huy Duong", Email: "huy.duong@acme.io", SSHKey: "~/.ssh/id_ed25519_acme"},
				{ID: "oss-bot", Login: "acme-release", Host: "ghe.acme.io", Name: "acme-release", Email: "release@acme.io"},
			},
			Rules:     []accounts.Rule{{Owner: "acme-corp/*", Account: "work"}, {Owner: "acme-infra/*", Account: "work"}, {Owner: "ghe.acme.io/*", Account: "oss-bot"}},
			Overrides: map[string]string{"dotfiles": "work", "side-project": "personal"},
		},
		Logins: []accounts.Login{{Host: "github.com", Login: "dinhhuy258", OK: true}, {Host: "github.com", Login: "huy-acme", OK: true, Scopes: "repo, read:org, workflow"}},
		Projects: []ProjectAccount{
			{Project: "api-server", Origin: gh("acme-corp", "api-server")},
			{Project: "dotfiles", Origin: gh("acme-corp", "dotfiles")},
			{Project: "side-project", Origin: gh("acme-corp", "side-project")},
		},
	}
}

func TestSettingsGitGolden(t *testing.T) {
	s := gitSettings()
	s.SetAccounts(sampleAccounts())
	s.Key("j") // infra
	golden(t, "settings-git-120", s.Render(120, 40, true, ""), 120)
	for range 10 {
		s.Key("j")
	}
	if r, _ := s.GitRow(); r.Kind != RowAccountOverride || r.Key != "dotfiles" {
		t.Fatalf("row %+v", r)
	}
	golden(t, "settings-git-override-120", s.Render(120, 40, true, ""), 120)
	golden(t, "settings-git-override-40", s.Render(40, 30, true, ""), 40)
	e := sampleSettings()
	e.Key("Tab")
	golden(t, "settings-git-empty-40", e.Render(40, 30, true, ""), 40)
}

func TestSettingsGitAccountForm(t *testing.T) {
	s := gitSettings()
	x := sampleAccounts()
	s.SetAccounts(x)
	s.OpenAccountForm(x.Config.Accounts[1], false, false)
	for _, k := range []string{"j", "j", "j"} {
		s.Key(k)
	}
	if key, label, v, ok := s.AccountFormText(); !ok || key != formEmail || label != "Commit email" || v != "huy.duong@acme.io" {
		t.Fatalf("text field %d %q %q %v", key, label, v, ok)
	}
	if s.Key("Enter") != SettingsAccountText || s.Key("C-s") != SettingsAccountSave || s.Key("Esc") != SettingsAccountBack {
		t.Fatal("form keys")
	}
	golden(t, "settings-git-account-120", s.Render(120, 30, true, ""), 120)
	if s.AccountFormChanged() {
		t.Fatal("no edit yet")
	}
	s.SetAccountFormText(formEmail, "x@acme.io")
	s.Key("k")
	s.Key("k") // gh login
	s.Key("l")
	a, _, _, orig, _ := s.AccountForm()
	if !s.AccountFormChanged() || a.Email != "x@acme.io" || a.Login == "huy-acme" || orig != "work" {
		t.Fatalf("form %+v", a)
	}
	s.CloseAccountForm()
	if r, _ := s.GitRow(); r.Kind != RowBase {
		t.Fatalf("the rows are back: %+v", r)
	}
}

func TestSettingsRuleMatches(t *testing.T) {
	s := gitSettings()
	s.SetAccounts(sampleAccounts())
	if n := len(s.RuleMatches("acme-corp/*")); n != 3 {
		t.Fatal(n)
	}
	if n := len(s.RuleMatches("nope")); n != 0 {
		t.Fatal(n)
	}
}

// Thirty overrides scroll with the cursor; the header stays.
func TestSettingsGitWindow(t *testing.T) {
	s := sampleSettings()
	var list []GitOverride
	for i := range 30 {
		list = append(list, GitOverride{Project: fmt.Sprintf("p%02d", i), Base: "develop"})
	}
	s.SetGitOverrides(list)
	s.Key("Tab")
	for range 20 {
		s.Key("j")
	}
	for _, w := range []int{120, 40} {
		got := s.Render(w, 16, true, "")
		if lines := strings.Split(got, "\n"); len(lines) != 16 {
			t.Fatalf("width %d: %d lines", w, len(lines))
		}
		for _, want := range []string{"› p20", "more"} {
			if !strings.Contains(got, want) {
				t.Fatalf("width %d: no %q:\n%s", w, want, got)
			}
		}
	}
}

func TestSettingsGitBar(t *testing.T) {
	s := gitSettings()
	if got := s.SettingsGitBar(false); got != "j/k move · ⏎ choose · ⌫ remove · r recheck gh · tab section · esc close" {
		t.Fatal(got)
	}
	if got := s.SettingsGitBar(true); got != "⏎ choose · ⌫ remove · esc" {
		t.Fatal(got)
	}
	s.Key("j")
	s.Key("j")
	if got := s.SettingsGitBar(false); got != "j/k move · ⏎ add · r recheck gh · tab section · esc close" {
		t.Fatal(got)
	}
}

// baseRow is the base override row under the cursor: its project, or add on its add row.
func baseRow(s *Settings) (project string, add bool) {
	r, _ := s.GitRow()
	switch r.Kind {
	case RowBase:
		return r.Key, false
	case RowBaseAdd:
		return "", true
	}
	return "", false
}
