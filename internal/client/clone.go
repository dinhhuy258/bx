package client

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/dinhhuy258/bx/internal/api"
	"github.com/dinhhuy258/bx/internal/naming"
	"github.com/dinhhuy258/bx/internal/ui"
)

// The clone picker lists one owner at a time: the gh login, then each of its organizations,
// as tabs that Tab and S-Tab switch. A tab shows the owner's latest pushed repositories (one
// page, fetched the first time the tab opens); typing filters them at once and, after a pause,
// searches GitHub for the names in that owner.

// cloneSearchDelay is the pause in typing before the clone picker searches GitHub.
const cloneSearchDelay = 300 * time.Millisecond

const cloneHelp = "type to search · tab/S-tab owner · ↑↓ move · ⏎ next: project name · esc cancel"

type (
	cloneOwnersMsg struct {
		owners []string
		err    error
	}
	// cloneReposMsg is a page of owner's repositories: the latest pushed (q == "") or a search.
	cloneReposMsg struct {
		seq      int
		owner, q string
		repos    []api.GitHubRepo
		err      error
	}
	// cloneSearchMsg ends the pause after the keystroke that bumped seq to this one.
	cloneSearchMsg struct{ seq int }
)

// cloneState is what the open clone picker knows.
type cloneState struct {
	owners []string
	latest map[string][]api.GitHubRepo // by owner; a key is set once its page has arrived
	// found is the reply of the last search, for foundOwner and foundQ
	found              []api.GitHubRepo
	foundOwner, foundQ string
	loading            bool // the open tab's latest page is on its way
	searching          string
	err                string // the last failure, shown under the list
	seq                int    // bumped on every keystroke and tab switch; older searches are dropped
}

// owner is the open tab.
func (c *cloneState) owner(p *ui.Picker) string { return c.owners[p.Tab] }

// openClone asks for the owners, then opens the picker on the last one cloned from.
func (m *Model) openClone() tea.Cmd {
	m.setFlash("loading GitHub owners…")
	be, ctx := m.be, m.ctx
	return func() tea.Msg {
		owners, err := be.GitHubOwners(ctx)
		return cloneOwnersMsg{owners, err}
	}
}

func (m *Model) gotCloneOwners(msg cloneOwnersMsg) tea.Cmd {
	switch {
	case msg.err != nil:
		m.setFlash("github: " + msg.err.Error())
		return nil
	case len(msg.owners) == 0:
		m.setFlash("github: no account to clone from (gh auth login)")
		return nil
	}
	m.setFlash("")
	p := ui.NewPicker("clone from GitHub", nil)
	p.Tabs, p.Help = msg.owners, cloneHelp
	for i, o := range msg.owners {
		if strings.EqualFold(o, m.cloneOwner) {
			p.Tab = i
		}
	}
	m.picker, m.pickKind = p, pickRepos
	m.clone = &cloneState{owners: msg.owners, latest: map[string][]api.GitHubRepo{}}
	return m.cloneShowTab()
}

// cloneOpen reports whether the clone picker is on screen.
func (m *Model) cloneOpen() bool {
	return m.picker != nil && m.pickKind == pickRepos && m.clone != nil
}

// cloneKey handles the keys the plain picker does not know: Tab and S-Tab switch the owner,
// and typing schedules a search. done says the key was taken.
func (m *Model) cloneKey(key, text string) (tea.Cmd, bool) {
	if !m.cloneOpen() {
		return nil, false
	}
	p, c := m.picker, m.clone
	switch key {
	case "Tab", "S-Tab":
		step := 1
		if key == "S-Tab" {
			step = len(c.owners) - 1
		}
		p.Tab = (p.Tab + step) % len(c.owners)
		return m.cloneShowTab(), true
	case "Enter":
		// the exact owner/repo typed is that repository, not the first row that contains it
		if r := typedRepo(p.Filter()); r != "" && !strings.EqualFold(p.Choice(), r) {
			if exact := m.cloneItem(r); exact != "" {
				return m.cloneRepo(exact), true
			}
		}
		return nil, false // the picker chooses, or finds no match: cloneTyped
	}
	before := p.Filter()
	if p.Key(key, text) != ui.PickKeep {
		return nil, false // Esc
	}
	if p.Filter() == before {
		return nil, true
	}
	return m.cloneFiltered(), true
}

// cloneItem is the listed repository that is r, ignoring case, or "".
func (m *Model) cloneItem(r string) string {
	for _, it := range m.cloneItems() {
		if strings.EqualFold(it, r) {
			return it
		}
	}
	return ""
}

// cloneFiltered follows a change of the filter: "owner/" opens that owner's tab, the list
// narrows at once, and a search waits for a pause in typing.
func (m *Model) cloneFiltered() tea.Cmd {
	if !m.cloneOpen() {
		return nil
	}
	p, c := m.picker, m.clone
	if o, _, ok := strings.Cut(p.Filter(), "/"); ok && !strings.EqualFold(o, c.owner(p)) {
		for i, x := range c.owners {
			if strings.EqualFold(x, o) {
				p.Tab = i
				return m.cloneShowTab()
			}
		}
	}
	c.seq++
	c.err = ""
	m.cloneRefresh()
	if cloneQuery(p.Filter(), c.owner(p)) == "" {
		return nil
	}
	seq := c.seq
	if m.cloneWait != nil {
		return m.cloneWait(seq)
	}
	return tea.Tick(cloneSearchDelay, func(time.Time) tea.Msg { return cloneSearchMsg{seq} })
}

// cloneShowTab lists the open tab: its latest page, fetched the first time, and a search for
// what is typed.
func (m *Model) cloneShowTab() tea.Cmd {
	p, c := m.picker, m.clone
	c.seq++
	c.err, c.searching = "", ""
	owner := c.owner(p)
	var cmds []tea.Cmd
	if _, ok := c.latest[owner]; !ok {
		c.loading = true
		cmds = append(cmds, m.cloneFetch(c.seq, owner, ""))
	} else {
		c.loading = false
	}
	if q := cloneQuery(p.Filter(), c.owner(p)); q != "" && (c.foundOwner != owner || c.foundQ != q) {
		c.searching = q
		cmds = append(cmds, m.cloneFetch(c.seq, owner, q))
	}
	m.cloneRefresh()
	return tea.Batch(cmds...)
}

// cloneSearch runs the search once typing has paused, unless a later key moved on.
func (m *Model) cloneSearch(msg cloneSearchMsg) tea.Cmd {
	if !m.cloneOpen() || msg.seq != m.clone.seq {
		return nil
	}
	p, c := m.picker, m.clone
	owner := c.owner(p)
	q := cloneQuery(p.Filter(), owner)
	if q == "" || (c.foundOwner == owner && c.foundQ == q) {
		return nil
	}
	c.searching = q
	m.cloneRefresh()
	return m.cloneFetch(msg.seq, owner, q)
}

func (m *Model) cloneFetch(seq int, owner, q string) tea.Cmd {
	be, ctx := m.be, m.ctx
	return func() tea.Msg {
		repos, err := be.GitHubRepos(ctx, owner, q)
		return cloneReposMsg{seq, owner, q, repos, err}
	}
}

// gotCloneRepos files a page. A latest page is kept whenever it comes; a search only while
// it is still the one typed.
func (m *Model) gotCloneRepos(msg cloneReposMsg) {
	if !m.cloneOpen() {
		return
	}
	p, c := m.picker, m.clone
	current := msg.owner == c.owner(p)
	if msg.q == "" {
		if msg.err == nil {
			c.latest[msg.owner] = msg.repos
		}
		if current {
			c.loading = false
		}
	} else {
		if !current || msg.q != cloneQuery(p.Filter(), c.owner(p)) {
			return
		}
		c.searching = ""
		if msg.err == nil {
			c.found, c.foundOwner, c.foundQ = msg.repos, msg.owner, msg.q
		}
	}
	if msg.err != nil && current {
		c.err = "github: " + msg.err.Error()
	}
	m.cloneRefresh()
}

// cloneItems are the open tab's repositories: the latest page, then search results not in it.
func (m *Model) cloneItems() []string {
	items, _ := m.cloneRows()
	return items
}

func (m *Model) cloneRows() ([]string, map[string]string) {
	p, c := m.picker, m.clone
	owner := c.owner(p)
	repos := c.latest[owner]
	if c.foundOwner == owner && c.foundQ == cloneQuery(p.Filter(), c.owner(p)) {
		repos = append(append([]api.GitHubRepo(nil), repos...), c.found...)
	}
	var items []string
	details := map[string]string{}
	now := time.Now()
	for _, r := range repos {
		if _, dup := details[r.Name]; dup {
			continue
		}
		items = append(items, r.Name)
		details[r.Name] = repoDetail(r, now)
	}
	return items, details
}

// repoDetail is the dim text after a repository: "🔒 Admin app · 2d".
func repoDetail(r api.GitHubRepo, now time.Time) string {
	var parts []string
	if r.Private {
		parts = append(parts, "🔒")
	}
	if r.Description != "" {
		parts = append(parts, r.Description)
	}
	if r.Pushed > 0 {
		parts = append(parts, ui.Ago(now.Sub(time.Unix(r.Pushed, 0))))
	}
	if d := strings.Join(parts, " "); d != "" {
		return d
	}
	return " " // keeps the column, so rows line up
}

// cloneRefresh redraws the list and the note under it.
func (m *Model) cloneRefresh() {
	p, c := m.picker, m.clone
	items, details := m.cloneRows()
	p.Replace(items)
	p.Details = details
	owner := c.owner(p)
	switch {
	case c.err != "":
		p.Note = c.err
	case c.loading:
		p.Note = "loading " + owner + "…"
	case c.searching != "":
		p.Note = fmt.Sprintf("searching %s for %q…", owner, c.searching)
	case p.Filter() == "" && len(items) == 0:
		p.Note = owner + " has no repositories you can see (an org with SSO needs gh authorized for it)"
	case p.Filter() == "":
		p.Note = fmt.Sprintf("latest %d pushed · type to search %s", len(items), owner)
	case p.Choice() == "" && typedRepo(p.Filter()) != "":
		p.Note = "⏎ use " + typedRepo(p.Filter()) + " · next: project name"
	case p.Choice() == "":
		p.Note = "no match in " + owner
	default:
		p.Note = ""
	}
}

// cloneTyped clones what was typed when nothing matches it, if it names a repository.
func (m *Model) cloneTyped() tea.Cmd {
	if !m.cloneOpen() {
		return nil
	}
	if r := typedRepo(m.picker.Filter()); r != "" {
		return m.cloneRepo(r)
	}
	return nil
}

// cloneRepo closes the picker and asks for the project name, the repository's by default.
func (m *Model) cloneRepo(repo string) tea.Cmd {
	m.picker, m.clone = nil, nil
	m.cloneOwner, _, _ = strings.Cut(repo, "/")
	m.openPrompt(ui.PromptCloneName, repo, "Cloning "+repo+" as", naming.ProjectName(repo))
	return nil
}

// cloneAs clones repo as project name ("" = the repository's). A name that is not valid or
// already taken asks again, with what was typed.
func (m *Model) cloneAs(repo, name string) tea.Cmd {
	if name == "" {
		name = naming.ProjectName(repo)
	}
	bad := ""
	switch {
	case !naming.ValidProjectName(name):
		bad = "project name: use lowercase letters, digits, . _ - (not first)"
	case slices.ContainsFunc(m.projects, func(p api.ProjectSummary) bool { return p.Name == name }):
		bad = "project " + name + " already exists"
	}
	if bad != "" {
		m.openPrompt(ui.PromptCloneName, repo, "Cloning "+repo+" as", name)
		m.prompt.Note = bad
		return nil
	}
	if name == naming.ProjectName(repo) {
		m.setFlash("cloning " + repo + "…")
	} else {
		m.setFlash("cloning " + repo + " as " + name + "…")
	}
	return m.create(api.NewProject{Kind: "clone", Repo: repo, Name: name})
}

// cloneQuery is the name to search owner for in what was typed: the part after "owner/", and
// nothing when it names another owner (someone/thing is cloned by name, not searched) or a URL.
func cloneQuery(filter, owner string) string {
	f := strings.TrimSpace(filter)
	if strings.Contains(f, "://") {
		return ""
	}
	if o, rest, ok := strings.Cut(f, "/"); ok {
		if !strings.EqualFold(o, owner) {
			return ""
		}
		f = rest
	}
	return strings.TrimSpace(f)
}

var (
	ownerRepoRe = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9-]*)/([A-Za-z0-9_.-]+)$`)
	repoURLRe   = regexp.MustCompile(`^(?:https?://)?github\.com/([A-Za-z0-9][A-Za-z0-9-]*)/([A-Za-z0-9_.-]+?)(?:\.git)?/?(?:[#?].*)?$`)
)

// typedRepo is the owner/repo that filter names, as owner/repo or a github.com URL, else "".
func typedRepo(filter string) string {
	f := strings.TrimSpace(filter)
	if m := repoURLRe.FindStringSubmatch(f); m != nil {
		return m[1] + "/" + m[2]
	}
	if m := ownerRepoRe.FindStringSubmatch(f); m != nil {
		return m[1] + "/" + m[2]
	}
	return ""
}
