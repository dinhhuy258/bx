package client

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/dinhhuy258/bx/internal/accounts"
	"github.com/dinhhuy258/bx/internal/api"
	"github.com/dinhhuy258/bx/internal/ui"
)

// accountsMsg is the reply of a GET or PUT of the GitHub accounts; flash is said on success.
type accountsMsg struct {
	x     api.GitAccounts
	err   error
	flash string
	put   bool
}

// accountDraft is a rule or an account override between its prompt and its picker in
// settings › git: the rule at index (-1 = new) with its owner, or the project of an override.
type accountDraft struct {
	index   int
	owner   string
	project string
	logins  map[string]accounts.Login // the add-account picker's items
}

func (m *Model) loadAccounts() tea.Cmd {
	be, ctx := m.be, m.ctx
	return func() tea.Msg {
		x, err := be.GitAccounts(ctx)
		return accountsMsg{x: x, err: err}
	}
}

// saveAccounts sends the whole config; flash is said once it is saved.
func (m *Model) saveAccounts(c accounts.Config, flash string) tea.Cmd {
	if err := c.Validate(); err != nil {
		m.setFlash("accounts: " + err.Error())
		return nil
	}
	be, ctx := m.be, m.ctx
	return func() tea.Msg {
		x, err := be.SaveGitAccounts(ctx, c)
		return accountsMsg{x: x, err: err, flash: flash, put: true}
	}
}

func (m *Model) gotAccounts(msg accountsMsg) {
	if m.settings == nil {
		return
	}
	if msg.err != nil {
		if msg.put {
			m.setFlash("accounts not saved: " + msg.err.Error())
			return
		}
		m.settings.SetAccountsError(msg.err.Error())
		return
	}
	m.settings.SetAccounts(msg.x)
	if msg.put {
		m.settings.CloseAccountForm()
	}
	if msg.flash != "" {
		m.setFlash(msg.flash)
	}
}

// config is a copy of the accounts config the screen shows, safe to change.
func (m *Model) config() accounts.Config {
	c := m.settings.Accounts().Config
	c.Accounts = slices.Clone(c.Accounts)
	c.Rules = slices.Clone(c.Rules)
	over := make(map[string]string, len(c.Overrides))
	for k, v := range c.Overrides {
		over[k] = v
	}
	c.Overrides = over
	return c
}

// gitChoose is Enter on a row of the git section.
func (m *Model) gitChoose() tea.Cmd {
	row, _ := m.settings.GitRow()
	c := m.config()
	switch row.Kind {
	case ui.RowBase:
		return m.chooseOverrideBranch(row.Key)
	case ui.RowBaseAdd:
		m.openOverrideProjects()
	case ui.RowAccount:
		if a, ok := c.Account(row.Key); ok {
			m.settings.OpenAccountForm(a, false, c.Default == a.ID)
		}
	case ui.RowAccountAdd:
		m.openAccountLogins()
	case ui.RowRule:
		m.accDraft = accountDraft{index: row.Index}
		m.openPrompt(ui.PromptRuleOwner, strconv.Itoa(row.Index), "Owner", c.Rules[row.Index].Owner)
	case ui.RowRuleAdd:
		if len(c.Accounts) == 0 {
			m.setFlash("add an account first")
			return nil
		}
		m.accDraft = accountDraft{index: -1}
		m.openPrompt(ui.PromptRuleOwner, "-1", "Owner", "")
	case ui.RowAccountOverride:
		m.accDraft = accountDraft{project: row.Key}
		m.openAccountPicker(row.Key+" uses…", pickOverrideAccount)
	case ui.RowAccountOverrideAdd:
		m.openAccountOverrideProjects()
	}
	return nil
}

// gitRemove is Backspace on a row of the git section: it asks first.
func (m *Model) gitRemove() {
	row, _ := m.settings.GitRow()
	c := m.config()
	switch row.Kind {
	case ui.RowBase:
		m.ask(ui.PromptRemoveOverride, row.Key, "Remove base override?", fmt.Sprintf("New stories in %s stop starting from %s", row.Key, m.baseOverride(row.Key)), "and use the detected base again.")
	case ui.RowAccount:
		rules, over := 0, 0
		for _, r := range c.Rules {
			if r.Account == row.Key {
				rules++
			}
		}
		for _, id := range c.Overrides {
			if id == row.Key {
				over++
			}
		}
		body := []string{"bx forgets it; gh keeps the login."}
		if rules+over > 0 {
			body = append(body, fmt.Sprintf("Its %s and %s go too.", plural(rules, "rule", "rules"), plural(over, "project override", "project overrides")))
		}
		m.ask(ui.PromptRemoveAccount, row.Key, "Remove account "+row.Key+"?", body...)
	case ui.RowRule:
		r := c.Rules[row.Index]
		m.ask(ui.PromptRemoveRule, strconv.Itoa(row.Index), "Remove rule?", r.Owner+" → "+r.Account, "Its projects fall through to the next rule or the default.")
	case ui.RowAccountOverride:
		m.ask(ui.PromptRemoveAccountOverride, row.Key, "Remove account override?", row.Key+" stops using "+c.Overrides[row.Key]+".", "It falls back to the rules and the default.")
	}
}

// openAccountLogins opens the picker of gh's logins that no account uses yet.
func (m *Model) openAccountLogins() {
	x := m.settings.Accounts()
	if x.LoginError != "" {
		m.setFlash("gh auth status failed: " + x.LoginError)
		return
	}
	m.accDraft = accountDraft{logins: map[string]accounts.Login{}}
	var items []string
	for _, l := range x.Logins {
		used := slices.ContainsFunc(x.Config.Accounts, func(a accounts.Account) bool {
			return strings.EqualFold(a.Login, l.Login) && strings.EqualFold(a.HostOrDefault(), l.Host)
		})
		if used || !l.OK {
			continue
		}
		item := l.Login + " · " + l.Host
		m.accDraft.logins[item] = l
		items = append(items, item)
	}
	if len(items) == 0 {
		m.setFlash("every gh login has an account · run gh auth login in a shell (T), then r")
		return
	}
	m.picker, m.pickKind = ui.NewPicker("add account for gh login…", items), pickAccountLogin
}

// pickAccountLogin opens the form of a new account for the chosen login. The first account
// becomes the default.
func (m *Model) pickAccountLogin(choice string) {
	l, ok := m.accDraft.logins[choice]
	if !ok {
		return
	}
	a := accounts.Account{ID: accountID(l.Login), Login: l.Login}
	if l.Host != accounts.DefaultHost {
		a.Host = l.Host
	}
	c := m.config()
	for n := 2; slices.ContainsFunc(c.Accounts, func(x accounts.Account) bool { return x.ID == a.ID }); n++ {
		a.ID = accountID(l.Login) + "-" + strconv.Itoa(n)
	}
	m.settings.OpenAccountForm(a, true, len(c.Accounts) == 0)
}

// accountID is a login as an account id: letters, digits, - and _.
func accountID(login string) string {
	var b strings.Builder
	for _, r := range login {
		if r < 128 && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	id := strings.Trim(b.String(), "-_")
	if id == "" {
		return "account"
	}
	return id
}

// saveAccountForm saves the account of the open form: a new one is added, an edited one
// replaced, and a new label carried to its rules, overrides and the default.
func (m *Model) saveAccountForm() tea.Cmd {
	a, isDefault, isNew, origID, ok := m.settings.AccountForm()
	if !ok {
		return nil
	}
	c := m.config()
	if isNew {
		c.Accounts = append(c.Accounts, a)
	} else {
		i := slices.IndexFunc(c.Accounts, func(x accounts.Account) bool { return x.ID == origID })
		if i < 0 {
			m.setFlash("account " + origID + " is gone · esc, then r")
			return nil
		}
		c.Accounts[i] = a
		for i := range c.Rules {
			if c.Rules[i].Account == origID {
				c.Rules[i].Account = a.ID
			}
		}
		for p, id := range c.Overrides {
			if id == origID {
				c.Overrides[p] = a.ID
			}
		}
	}
	switch {
	case isDefault:
		c.Default = a.ID
	case c.Default == origID || c.Default == a.ID:
		c.Default = ""
	}
	return m.saveAccounts(c, "account "+a.ID+" saved · new sessions use it")
}

// accountFormBack is Esc in the account form: it asks first when the form has edits.
func (m *Model) accountFormBack() {
	if m.settings.AccountFormChanged() {
		m.ask(ui.PromptDiscardAccount, "", "Discard account changes?", "Your edits to this account are lost.")
		return
	}
	m.settings.CloseAccountForm()
}

// editAccountText opens the prompt of the form's text field under the cursor.
func (m *Model) editAccountText() {
	if key, label, value, ok := m.settings.AccountFormText(); ok {
		m.openPrompt(ui.PromptAccountText, strconv.Itoa(key), label, value)
	}
}

// openAccountPicker opens the picker of the accounts, for a rule or an override.
func (m *Model) openAccountPicker(title string, kind pickKind) {
	var items []string
	for _, a := range m.settings.Accounts().Config.Accounts {
		items = append(items, a.ID)
	}
	if len(items) == 0 {
		m.setFlash("add an account first")
		return
	}
	m.picker, m.pickKind = ui.NewPicker(title, items), kind
}

// ruleOwner is the owner typed for a rule: it is checked, then the account is picked, with
// the projects it matches in the title.
func (m *Model) ruleOwner(text string) {
	owner := strings.TrimSpace(text)
	if !strings.HasSuffix(owner, "/*") && owner != "" && !strings.Contains(owner, "*") {
		owner += "/*" // acme-corp is acme-corp/*
	}
	if _, err := accounts.ParseOwner(owner); err != nil {
		m.setFlash(err.Error())
		return
	}
	m.accDraft.owner = owner
	n := len(m.settings.RuleMatches(owner))
	m.openAccountPicker(fmt.Sprintf("%s → account · matches %s", owner, plural(n, "project", "projects")), pickRuleAccount)
}

// pickRuleAccount saves the drafted rule with the chosen account.
func (m *Model) pickRuleAccount(id string) tea.Cmd {
	c := m.config()
	r := accounts.Rule{Owner: m.accDraft.owner, Account: id}
	if i := m.accDraft.index; i >= 0 && i < len(c.Rules) {
		c.Rules[i] = r
	} else {
		c.Rules = append(c.Rules, r)
	}
	return m.saveAccounts(c, "rule "+r.Owner+" → "+id+" saved")
}

// moveRule swaps rule i with its neighbour d (-1 up, 1 down) and saves.
func (m *Model) moveRule(d int) tea.Cmd {
	row, _ := m.settings.GitRow()
	c := m.config()
	i, j := row.Index, row.Index+d
	if row.Kind != ui.RowRule || j < 0 || j >= len(c.Rules) {
		return nil
	}
	c.Rules[i], c.Rules[j] = c.Rules[j], c.Rules[i]
	m.settings.MoveCursorToRule(j)
	return m.saveAccounts(c, "")
}

// openAccountOverrideProjects opens the picker of the projects without an account override.
func (m *Model) openAccountOverrideProjects() {
	c := m.config()
	if len(c.Accounts) == 0 {
		m.setFlash("add an account first")
		return
	}
	var items []string
	for _, p := range m.projects {
		if _, ok := c.Overrides[p.Name]; !ok {
			items = append(items, p.Name)
		}
	}
	if len(items) == 0 {
		m.setFlash("every project has an account override")
		return
	}
	m.picker, m.pickKind = ui.NewPicker("account override for…", items), pickAccountProject
}

// pickOverrideAccount saves the drafted project's override.
func (m *Model) pickOverrideAccount(id string) tea.Cmd {
	c := m.config()
	c.Overrides[m.accDraft.project] = id
	return m.saveAccounts(c, m.accDraft.project+" uses "+id+" · new sessions use it")
}

// submitAccounts handles the prompts of the GitHub block; false when kind is not one of them.
func (m *Model) submitAccounts(kind ui.PromptKind, text, target string) (tea.Cmd, bool) {
	if m.settings == nil {
		return nil, kind == ui.PromptAccountText || kind == ui.PromptRuleOwner || kind == ui.PromptRemoveAccount ||
			kind == ui.PromptRemoveRule || kind == ui.PromptRemoveAccountOverride || kind == ui.PromptDiscardAccount
	}
	c := m.config()
	switch kind {
	case ui.PromptAccountText:
		key, _ := strconv.Atoi(target)
		m.settings.SetAccountFormText(key, strings.TrimSpace(text))
	case ui.PromptRuleOwner:
		m.ruleOwner(text)
	case ui.PromptDiscardAccount:
		m.settings.CloseAccountForm()
	case ui.PromptRemoveAccount:
		c.Accounts = slices.DeleteFunc(c.Accounts, func(a accounts.Account) bool { return a.ID == target })
		c.Rules = slices.DeleteFunc(c.Rules, func(r accounts.Rule) bool { return r.Account == target })
		for p, id := range c.Overrides {
			if id == target {
				delete(c.Overrides, p)
			}
		}
		if c.Default == target {
			c.Default = ""
		}
		return m.saveAccounts(c, "account "+target+" removed"), true
	case ui.PromptRemoveRule:
		i, _ := strconv.Atoi(target)
		if i < 0 || i >= len(c.Rules) {
			return nil, true
		}
		r := c.Rules[i]
		c.Rules = slices.Delete(c.Rules, i, i+1)
		return m.saveAccounts(c, "rule "+r.Owner+" removed"), true
	case ui.PromptRemoveAccountOverride:
		delete(c.Overrides, target)
		return m.saveAccounts(c, target+": account override removed"), true
	default:
		return nil, false
	}
	return nil, true
}
