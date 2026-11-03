package ui

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/dinhhuy258/bx/internal/accounts"
)

// gitSection is the ID of the settings section of the base branch overrides and the GitHub
// accounts. The server does not send it: Load adds it after the claude section, so the phone
// never shows it.
const gitSection = "git"

// The add rows of the git section.
const (
	gitAdd         = "+ add override"
	accountAdd     = "+ add account"
	ruleAdd        = "+ add rule"
	accountOverAdd = "+ add project override"
)

// gitEmpty is shown above the add row when no project has a base override.
const gitEmpty = "No overrides. New stories start from each repo's default branch."

// accountsEmpty is shown above the add row when no account is set.
const accountsEmpty = "No accounts. Sessions use gh's active login and your git config."

// GitOverride is a project whose new stories start from Base instead of the remote default.
type GitOverride struct {
	Project, Base string
}

// GitRowKind is what a row of the git section is.
type GitRowKind int

const (
	RowBase               GitRowKind = iota // a base branch override (Key is the project)
	RowBaseAdd                              // + add override
	RowAccount                              // a GitHub account (Key is its id)
	RowAccountAdd                           // + add account
	RowRule                                 // an account rule (Index is its place)
	RowRuleAdd                              // + add rule
	RowAccountOverride                      // a project's account override (Key is the project)
	RowAccountOverrideAdd                   // + add project override
)

// GitRow is a row of the git section.
type GitRow struct {
	Kind  GitRowKind
	Key   string
	Index int
}

// Add reports whether the row is an add row.
func (r GitRow) Add() bool {
	switch r.Kind {
	case RowBaseAdd, RowAccountAdd, RowRuleAdd, RowAccountOverrideAdd:
		return true
	}
	return false
}

// SetGitOverrides shows the projects with a base override, sorted by name. The cursor stays on
// its row.
func (s *Settings) SetGitOverrides(list []GitOverride) {
	s.keepGitRow(func() {
		s.git = slices.Clone(list)
		slices.SortFunc(s.git, func(a, b GitOverride) int { return strings.Compare(a.Project, b.Project) })
	})
}

// SetAccounts shows the GitHub accounts, rules and overrides from the server. The cursor stays
// on its row, and an open account form takes the new logins.
func (s *Settings) SetAccounts(x GitAccounts) {
	s.keepGitRow(func() { s.acc, s.accErr = x, "" })
}

// SetAccountsError shows why the accounts could not be read or saved.
func (s *Settings) SetAccountsError(err string) { s.accErr = err }

// Accounts is what the GitHub block shows.
func (s *Settings) Accounts() GitAccounts { return s.acc }

// keepGitRow runs change and puts the cursor back on the row it was on: the same kind and key,
// or the same add row, else the same place.
func (s *Settings) keepGitRow(change func()) {
	before, ok := s.GitRow()
	change()
	if !ok {
		return
	}
	rows := s.gitRows()
	i := slices.IndexFunc(rows, func(r GitRow) bool {
		return r.Kind == before.Kind && r.Key == before.Key && (r.Kind != RowRule || r.Index == before.Index)
	})
	if i < 0 && !before.Add() { // its row is gone: the add row of its block
		i = slices.IndexFunc(rows, func(r GitRow) bool { return r.Kind == before.Kind+1 })
	}
	if i >= 0 {
		s.fld = i
		return
	}
	s.fld = min(s.fld, len(rows)-1)
}

// GitShown reports whether the git section is open.
func (s *Settings) GitShown() bool {
	return s.sec < len(s.sections) && s.sections[s.sec].ID == gitSection
}

// gitRows are the rows of the git section, top to bottom.
func (s *Settings) gitRows() []GitRow {
	var rows []GitRow
	for _, o := range s.git {
		rows = append(rows, GitRow{Kind: RowBase, Key: o.Project})
	}
	rows = append(rows, GitRow{Kind: RowBaseAdd})
	for _, a := range s.acc.Config.Accounts {
		rows = append(rows, GitRow{Kind: RowAccount, Key: a.ID})
	}
	rows = append(rows, GitRow{Kind: RowAccountAdd})
	for i := range s.acc.Config.Rules {
		rows = append(rows, GitRow{Kind: RowRule, Index: i})
	}
	rows = append(rows, GitRow{Kind: RowRuleAdd})
	for _, p := range s.AccountOverrides() {
		rows = append(rows, GitRow{Kind: RowAccountOverride, Key: p})
	}
	return append(rows, GitRow{Kind: RowAccountOverrideAdd})
}

// AccountOverrides are the projects with an account override, sorted.
func (s *Settings) AccountOverrides() []string {
	var out []string
	for p := range s.acc.Config.Overrides {
		out = append(out, p)
	}
	slices.Sort(out)
	return out
}

// GitRow is the row under the cursor; false when the section is not open.
func (s *Settings) GitRow() (GitRow, bool) {
	if !s.GitShown() {
		return GitRow{}, false
	}
	rows := s.gitRows()
	return rows[min(s.fld, len(rows)-1)], true
}

// GitChoice is the base override row under the cursor: its project, or add on its add row.
// Both are empty off those rows.
func (s *Settings) GitChoice() (project string, add bool) {
	r, ok := s.GitRow()
	switch {
	case !ok:
		return "", false
	case r.Kind == RowBase:
		return r.Key, false
	case r.Kind == RowBaseAdd:
		return "", true
	}
	return "", false
}

// gitKey handles a key in the git section: j/k move, Enter chooses, Backspace removes the row
// under the cursor, K/J move a rule up or down, r reloads the accounts, Tab goes to the next
// section and Esc closes. The account form has its own keys.
func (s *Settings) gitKey(key string) SettingsResult {
	if s.form != nil {
		return s.formKey(key)
	}
	row, _ := s.GitRow()
	switch key {
	case "Esc", "C-c":
		return SettingsClose
	case "Tab":
		s.sec, s.fld = (s.sec+1)%len(s.sections), 0
	case "j", "Down", "C-j":
		s.fld = min(s.fld+1, len(s.gitRows())-1)
	case "k", "Up", "C-k":
		s.fld = max(s.fld-1, 0)
	case "Enter":
		return SettingsGitChoose
	case "BSpace":
		if !row.Add() {
			return SettingsGitRemove
		}
	case "K":
		if row.Kind == RowRule && row.Index > 0 {
			return SettingsRuleUp
		}
	case "J":
		if row.Kind == RowRule && row.Index < len(s.acc.Config.Rules)-1 {
			return SettingsRuleDown
		}
	case "r":
		return SettingsAccountsReload
	}
	return SettingsKeep
}

// MoveCursorToRule puts the cursor on rule i, after a reorder.
func (s *Settings) MoveCursorToRule(i int) {
	if j := slices.Index(s.gitRows(), GitRow{Kind: RowRule, Index: i}); j >= 0 {
		s.fld = j
	}
}

// gitLines is the git section fitted to w and to at most h lines: the Base branches block and
// the GitHub accounts block, windowed around the cursor, under the section's title and help
// unless narrow (the tabs name it).
func (s *Settings) gitLines(w, h int, narrow bool, pal palette) []string {
	if s.form != nil {
		return s.formLines(w, h, pal)
	}
	var lines []string
	at := -1 // the line of the cursor
	row := 0
	put := func(text string) { lines = append(lines, truncate(text, w)) }
	item := func(cells ...string) {
		cursor := " "
		if row == s.fld {
			at = len(lines)
			cursor = pal.blue.Render("›")
			cells[0] = pal.bold.Render(cells[0])
		}
		put(" " + cursor + " " + strings.Join(cells, "  "))
		row++
	}
	add := func(label string) {
		if row == s.fld {
			at = len(lines)
			put(" " + pal.blue.Render("›") + " " + pal.bold.Render(label))
		} else {
			put("   " + label)
		}
		row++
	}
	heading := func(title string) {
		put(" " + pal.bold.Render(title) + " " + pal.dim.Render(strings.Repeat("─", max(w-runeWidth(title)-3, 0))))
	}
	sub := func(text string) { put(" " + pal.dim.Render(text)) }

	var head []string
	if !narrow {
		head = append(head, truncate(pal.bold.Render("Git"), w))
		head = append(head, wrap("Base branches and GitHub accounts. For new and resumed sessions, and bx's own fetch and clone.", w, pal.dim)...)
		head = append(head, "")
	}
	heading("Base branches")
	sub("New stories start from the repo's default branch, except:")
	put("")
	if len(s.git) == 0 {
		for _, l := range wrap(gitEmpty, w-3, pal.dim) {
			put("   " + l)
		}
	}
	nameW := 0
	for _, o := range s.git {
		nameW = max(nameW, runeWidth(o.Project))
	}
	nameW = max(min(nameW, w/2), 1)
	for _, o := range s.git {
		item(padTo(truncate(o.Project, nameW), nameW), o.Base)
	}
	add(gitAdd)
	put("")
	put("")
	heading("GitHub accounts")
	sub("The gh login, commit name and email each project uses.")
	if s.accErr != "" {
		put(" " + pal.red.Render("✗ "+s.accErr))
	}
	put("")
	lines, _, at = s.accountLines(lines, row, at, w, pal)
	return append(head, window(lines, max(at, 0), max(h-len(head), 3), pal)...)
}

// accountLines adds the accounts, rules and project overrides to lines, the rows numbered from
// row on; at is the line of the cursor.
func (s *Settings) accountLines(lines []string, row, at, w int, pal palette) ([]string, int, int) {
	c := s.acc.Config
	wide := w >= 80
	put := func(text string) { lines = append(lines, truncate(text, w)) }
	line := func(text string, add bool) {
		cursor := " "
		if row == s.fld {
			at, cursor = len(lines), pal.blue.Render("›")
			if add {
				text = pal.bold.Render(text)
			}
		}
		put(" " + cursor + " " + text)
		row++
	}
	what := "accounts · from gh auth status"
	if s.acc.LoginError != "" {
		what = "accounts · gh auth status failed: " + s.acc.LoginError
	}
	put(" " + pal.dim.Render(what))
	if len(c.Accounts) == 0 {
		for _, l := range wrap(accountsEmpty, w-3, pal.dim) {
			put("   " + l)
		}
	}
	idW, loginW, hostW := 0, 0, 0
	for _, a := range c.Accounts {
		idW, loginW, hostW = max(idW, runeWidth(a.ID)), max(loginW, runeWidth(a.Login)), max(hostW, runeWidth(a.HostOrDefault()))
	}
	for _, a := range c.Accounts {
		id := padTo(a.ID, idW)
		if row == s.fld {
			id = pal.bold.Render(id)
		}
		state := pal.green.Render("✓")
		if s.acc.LoginError == "" && !accounts.HasLogin(s.acc.Logins, a) {
			state = pal.red.Render("✗ not logged in to gh")
		}
		if a.ID == c.Default {
			state += " " + pal.dim.Render("default")
		}
		text := id + "  " + padTo(a.Login, loginW)
		if wide {
			text += "  " + pal.dim.Render(padTo(a.HostOrDefault(), hostW)) + "  " + padTo(identity(a), 34)
		}
		line(text+"  "+state, false)
	}
	line(accountAdd, true)
	put("")
	put(" " + pal.dim.Render("rules · by origin owner · first match wins"))
	ownerW := 0
	for _, r := range c.Rules {
		ownerW = max(ownerW, runeWidth(r.Owner))
	}
	for _, r := range c.Rules {
		owner := padTo(r.Owner, ownerW)
		if row == s.fld {
			owner = pal.bold.Render(owner)
		}
		line(owner+"  → "+r.Account, false)
	}
	line(ruleAdd, true)
	put("")
	over := s.AccountOverrides()
	put(" " + pal.dim.Render(fmt.Sprintf("project overrides · %d of %d projects", len(over), len(s.acc.Projects))))
	projW := 0
	for _, p := range over {
		projW = max(projW, runeWidth(p))
	}
	for _, p := range over {
		name := padTo(p, projW)
		if row == s.fld {
			name = pal.bold.Render(name)
		}
		text := name + "  → " + c.Overrides[p]
		if note := s.overrideNote(p); note != "" && wide {
			text = name + "  → " + padTo(c.Overrides[p], idW) + "  " + pal.dim.Render(note)
		}
		line(text, false)
	}
	line(accountOverAdd, true)
	return lines, row, at
}

// identity is "Name <email>" of an account, as much of it as is set.
func identity(a accounts.Account) string {
	switch {
	case a.Name != "" && a.Email != "":
		return a.Name + " <" + a.Email + ">"
	case a.Email != "":
		return "<" + a.Email + ">"
	}
	return a.Name
}

// overrideNote says what project would use without its override.
func (s *Settings) overrideNote(project string) string {
	i := slices.IndexFunc(s.acc.Projects, func(p ProjectAccount) bool { return p.Project == project })
	if i < 0 {
		return ""
	}
	o := s.acc.Projects[i].Origin
	c := s.acc.Config
	c.Overrides = without(c.Overrides, project)
	r := c.Resolve(project, o)
	origin := o.String()
	if origin == "" {
		origin = "no origin"
	} else {
		origin = "origin " + origin
	}
	switch {
	case !r.OK:
		return origin
	case r.Account.ID == s.acc.Config.Overrides[project]:
		return origin + " would use " + r.Account.ID + " anyway"
	}
	return origin + " · else " + r.Account.ID
}

// without is m without key.
func without(m map[string]string, key string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		if k != key {
			out[k] = v
		}
	}
	return out
}

// RuleMatches are the projects whose origin owner would match owner, and the account each
// uses now.
func (s *Settings) RuleMatches(owner string) []ProjectAccount {
	p, err := accounts.ParseOwner(owner)
	if err != nil {
		return nil
	}
	var out []ProjectAccount
	for _, pa := range s.acc.Projects {
		if p.Match(pa.Origin.Host, pa.Origin.Owner) {
			out = append(out, pa)
		}
	}
	return out
}

// SettingsGitBar is the key bar of the settings screen while the git section is open: the
// keys of the row under the cursor, or of the account form.
func (s *Settings) SettingsGitBar(narrow bool) string {
	if s.form != nil {
		if narrow {
			return "⏎ edit · ^s save · esc"
		}
		return "j/k move · h/l change · ⏎ type · ctrl-s save · esc back"
	}
	row, _ := s.GitRow()
	if narrow {
		if row.Add() {
			return "⏎ add · r · esc"
		}
		return "⏎ choose · ⌫ remove · esc"
	}
	keys := []string{"j/k move", "⏎ choose", "⌫ remove"}
	switch {
	case row.Add():
		keys = []string{"j/k move", "⏎ add"}
	case row.Kind == RowAccount:
		keys = []string{"j/k move", "⏎ edit", "⌫ remove"}
	case row.Kind == RowRule:
		keys = append(keys, "K/J reorder")
	}
	return strings.Join(append(keys, "r recheck gh", "tab section", "esc close"), " · ")
}

// accountForm is the account being edited in the git section, over its rows.
type accountForm struct {
	acct, orig      accounts.Account
	isDefault, odef bool
	isNew           bool
	fld             int
}

// The fields of the account form, in order.
const (
	formID = iota
	formLogin
	formName
	formEmail
	formSSH
	formSigning
	formDefault
	formFields
)

var formLabels = [formFields]string{"Label", "gh login", "Commit name", "Commit email", "SSH key", "Signing key", "Default"}

var formHelp = [formFields]string{
	"Label: the account's name in bx, in rules and overrides. Letters, digits, - and _",
	"gh login: the gh account whose token sessions get. Add one with gh auth login, then r",
	"Commit name: the author and committer name of every commit. Empty = your git config",
	"Commit email: the author and committer email of every commit. Empty = your git config",
	"SSH key: the key for git@ and ssh:// origins, e.g. ~/.ssh/id_ed25519_work. Empty = ssh's own",
	"Signing key: signs every commit; a .pub file signs with SSH. Empty = no signing from bx",
	"Default: the account of every project no rule or override names",
}

// OpenAccountForm opens the account form on a: a new account, or one being edited.
func (s *Settings) OpenAccountForm(a accounts.Account, isNew, isDefault bool) {
	s.form = &accountForm{acct: a, orig: a, isDefault: isDefault, odef: isDefault, isNew: isNew}
}

// CloseAccountForm goes back to the rows.
func (s *Settings) CloseAccountForm() { s.form = nil }

// AccountForm is the account being edited, whether it is the default, whether it is new, and
// its id before the edit; ok is false when no form is open.
func (s *Settings) AccountForm() (a accounts.Account, isDefault, isNew bool, origID string, ok bool) {
	if s.form == nil {
		return accounts.Account{}, false, false, "", false
	}
	return s.form.acct, s.form.isDefault, s.form.isNew, s.form.orig.ID, true
}

// AccountFormChanged reports whether the open form has edits.
func (s *Settings) AccountFormChanged() bool {
	return s.form != nil && (s.form.isNew || s.form.acct != s.form.orig || s.form.isDefault != s.form.odef)
}

// AccountFormText is the text field under the cursor of the form: its key, label and value.
func (s *Settings) AccountFormText() (key int, label, value string, ok bool) {
	if s.form == nil {
		return 0, "", "", false
	}
	switch f := s.form.fld; f {
	case formID, formName, formEmail, formSSH, formSigning:
		return f, formLabels[f], s.formValue(f), true
	}
	return 0, "", "", false
}

// SetAccountFormText sets text field key of the form.
func (s *Settings) SetAccountFormText(key int, v string) {
	if s.form == nil {
		return
	}
	a := &s.form.acct
	switch key {
	case formID:
		a.ID = v
	case formName:
		a.Name = v
	case formEmail:
		a.Email = v
	case formSSH:
		a.SSHKey = v
	case formSigning:
		a.SigningKey = v
	}
}

func (s *Settings) formValue(f int) string {
	a := s.form.acct
	switch f {
	case formID:
		return a.ID
	case formLogin:
		return a.Login
	case formName:
		return a.Name
	case formEmail:
		return a.Email
	case formSSH:
		return a.SSHKey
	case formSigning:
		return a.SigningKey
	case formDefault:
		if s.form.isDefault {
			return "yes"
		}
		return "no"
	}
	return ""
}

// formLogins are the logins the gh login field steps through: gh's, and the account's own
// when gh does not have it.
func (s *Settings) formLogins() []accounts.Login {
	ls := slices.Clone(s.acc.Logins)
	a := s.form.acct
	if !slices.ContainsFunc(ls, func(l accounts.Login) bool {
		return strings.EqualFold(l.Login, a.Login) && strings.EqualFold(l.Host, a.HostOrDefault())
	}) {
		ls = append([]accounts.Login{{Login: a.Login, Host: a.HostOrDefault()}}, ls...)
	}
	return ls
}

// formKey handles a key in the account form: j/k field, h/l the login or default, Enter types
// a text field, C-s saves and Esc goes back.
func (s *Settings) formKey(key string) SettingsResult {
	f := s.form
	switch key {
	case "Esc", "C-c":
		return SettingsAccountBack
	case "C-s":
		return SettingsAccountSave
	case "j", "Down", "C-j", "Tab":
		f.fld = min(f.fld+1, formFields-1)
	case "k", "Up", "C-k":
		f.fld = max(f.fld-1, 0)
	case "h", "Left", "l", "Right":
		d := 1
		if key == "h" || key == "Left" {
			d = -1
		}
		switch f.fld {
		case formLogin:
			ls := s.formLogins()
			i := slices.IndexFunc(ls, func(l accounts.Login) bool {
				return strings.EqualFold(l.Login, f.acct.Login) && strings.EqualFold(l.Host, f.acct.HostOrDefault())
			})
			l := ls[(max(i, 0)+d+len(ls))%len(ls)]
			f.acct.Login, f.acct.Host = l.Login, l.Host
			if strings.EqualFold(l.Host, accounts.DefaultHost) {
				f.acct.Host = ""
			}
		case formDefault:
			f.isDefault = !f.isDefault
		}
	case "Enter":
		if _, _, _, ok := s.AccountFormText(); ok {
			return SettingsAccountText
		}
		return SettingsAccountSave
	}
	return SettingsKeep
}

// formLines draws the account form: its fields, what a session of the account starts with, and
// the help of the field under the cursor.
func (s *Settings) formLines(w, h int, pal palette) []string {
	f := s.form
	title := "Git › account " + f.acct.ID
	if f.isNew {
		title = "Git › new account"
	}
	out := []string{truncate(pal.bold.Render(title), w)}
	host := f.acct.HostOrDefault()
	sub := "gh login " + f.acct.Login + " on " + host
	i := slices.IndexFunc(s.acc.Logins, func(l accounts.Login) bool {
		return strings.EqualFold(l.Login, f.acct.Login) && strings.EqualFold(l.Host, host)
	})
	switch {
	case i >= 0 && s.acc.Logins[i].Scopes != "":
		sub += " · token scopes " + s.acc.Logins[i].Scopes
	case i < 0 && s.acc.LoginError == "":
		sub += " · not logged in to gh"
	}
	out = append(out, wrap(sub, w, pal.dim)...)
	out = append(out, "")
	labelW := 0
	for _, l := range formLabels {
		labelW = max(labelW, runeWidth(l))
	}
	for k := range formFields {
		cursor, label := " ", padTo(formLabels[k], labelW)
		v := s.formValue(k)
		choice := k == formLogin || k == formDefault
		if choice {
			if k == formLogin {
				v = padTo(v, 18) + "  " + pal.dim.Render(host)
			}
			v = pal.dim.Render("◂ ") + v + pal.dim.Render(" ▸")
		} else if v == "" {
			v = pal.dim.Render("not set")
		}
		if k == f.fld {
			cursor, label = pal.blue.Render("›"), pal.bold.Render(label)
		}
		out = append(out, truncate(" "+cursor+" "+label+"  "+v, w))
	}
	if w >= 60 {
		out = append(out, "", truncate(" "+pal.dim.Render("A session in a "+cmp.Or(f.acct.ID, "new")+" project starts with"), w))
		envW := 26
		row := func(k, v string) { out = append(out, truncate("   "+padTo(k, envW)+v, w)) }
		row("GH_TOKEN", pal.dim.Render("gh auth token --user "+f.acct.Login))
		if id := identity(f.acct); id != "" {
			row("GIT_AUTHOR / COMMITTER", id)
		}
		row("credential helper", pal.dim.Render("!gh auth git-credential  (https origins)"))
		if f.acct.SSHKey != "" {
			row("GIT_SSH_COMMAND", pal.dim.Render("ssh -i "+f.acct.SSHKey+" -o IdentitiesOnly=yes"))
		}
		if f.acct.SigningKey != "" {
			row("commit signing", pal.dim.Render(f.acct.SigningKey))
		}
	}
	out = append(out, "")
	out = append(out, wrap(formHelp[f.fld], w, pal.dim)...)
	if len(out) > h {
		out = out[:max(h, 1)]
	}
	return out
}
