package serve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/dinhhuy258/bx/internal/api"
	"github.com/dinhhuy258/bx/internal/claude"
	"github.com/dinhhuy258/bx/internal/notify"
	"github.com/dinhhuy258/bx/internal/prwatch"
	"github.com/dinhhuy258/bx/internal/state"
	"github.com/dinhhuy258/bx/internal/ui"
)

// The PR poller's pace: it ticks every PRTick, looks at a story every prFast while one of its
// sessions works or a check runs, else every prSlow; a story whose origin head moved (a push)
// or that was kicked is looked at on the next tick.
var (
	PRTick = 30 * time.Second
	prFast = 60 * time.Second
	prSlow = 5 * time.Minute
)

// prBatch is how many branches one GraphQL call asks about. Each costs up to about 16k nodes
// (3 PRs × 100 checks + 100 threads × 50 comments); GitHub refuses a query over 500k.
const prBatch = 10

// prLook is what one look found for a target: its PR, the gh login that asked, and whether
// the call failed (then pr.Missing is the error).
type prLook struct {
	pr     ui.PRInfo
	me     string
	failed bool
}

// prTarget is one repository of a story: its worktree, branch and GitHub repository.
type prTarget struct {
	project, story, rel, dir, branch, slug, head string
	missing                                      string
}

func prKey(project, story string) string { return project + "/" + story }

// WatchPRs runs the PR poller until ctx is done.
func (s *Server) WatchPRs(ctx context.Context) {
	t := time.NewTicker(PRTick)
	defer t.Stop()
	s.pollPRs(ctx, nil)
	for {
		select {
		case <-ctx.Done():
			return
		case k := <-s.prKick:
			s.pollPRs(ctx, map[string]bool{k: true})
		case <-t.C:
			s.pollPRs(ctx, nil)
		}
	}
}

// kickPR asks the poller to look at a story now, without blocking.
func (s *Server) kickPR(project, story string) {
	select {
	case s.prKick <- prKey(project, story):
	default:
	}
}

// forgetPR drops the badge and the schedule of a closed story.
func (s *Server) forgetPR(project, story string) {
	s.prMu.Lock()
	defer s.prMu.Unlock()
	delete(s.prBadges[project], story)
	delete(s.prNext, prKey(project, story))
}

// pollPRs looks at every due story (and the forced ones), one GraphQL call per GitHub repo.
func (s *Server) pollPRs(ctx context.Context, force map[string]bool) {
	s.prPoll.Lock()
	defer s.prPoll.Unlock()
	names, _ := state.ListProjects(filepath.Join(s.Cfg.Root, "state"))
	now := time.Now()
	var due []prTarget
	stories := map[string][]prTarget{} // by prKey
	var order []string                 // the keys of stories, in the order of due
	for _, name := range names {
		p, err := s.App.LoadProject(name)
		if err != nil {
			continue
		}
		for story := range p.Stories {
			k := prKey(name, story)
			ts := s.prTargets(p, story)
			old, _ := prwatch.Load(s.Cfg.PRFile(name, story))
			moved := false
			for _, t := range ts {
				if t.head != "" && old.Heads[t.rel] != t.head {
					moved = true
				}
			}
			s.prMu.Lock()
			next, seen := s.prNext[k]
			s.prMu.Unlock()
			if force[k] || moved || !seen || !now.Before(next) {
				stories[k] = ts
				order = append(order, k)
				due = append(due, ts...)
			}
		}
	}
	if len(due) == 0 {
		return
	}
	// group the pushed targets by GitHub repo: one call each
	bySlug := map[string][]int{}
	for i, t := range due {
		if t.missing == "" {
			bySlug[strings.ToLower(t.slug)] = append(bySlug[strings.ToLower(t.slug)], i)
		}
	}
	got := make([]prLook, len(due))
	for _, all := range bySlug {
		for len(all) > 0 { // at most prBatch branches a call, to stay under GitHub's node limit
			idx := all[:min(prBatch, len(all))]
			all = all[len(idx):]
			branches := make([]string, len(idx))
			for j, i := range idx {
				branches[j] = due[i].branch
			}
			first := due[idx[0]]
			cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			login, found, err := s.App.PRLook(cctx, first.dir, first.slug, branches)
			cancel()
			if err != nil {
				s.Log("pr %s: %v", first.slug, err)
			}
			for j, i := range idx {
				t := due[i]
				if err != nil {
					got[i] = prLook{pr: ui.PRInfo{Missing: err.Error()}, failed: true}
					continue
				}
				got[i] = prLook{me: login, pr: prwatch.Choose(found[j], t.slug, func(oid string) bool { return oid != "" && s.App.Git.IsAncestor(t.dir, oid, "HEAD") })}
			}
		}
	}
	for i, t := range due {
		if t.missing != "" {
			got[i].pr = ui.PRInfo{Missing: t.missing}
		}
		got[i].pr.Repo = t.rel
	}
	// save per story, diff, notify
	at := 0
	for _, k := range order {
		ts := stories[k]
		project, story, _ := strings.Cut(k, "/")
		prs := got[at : at+len(ts)]
		at += len(ts)
		s.savePRLook(project, story, ts, prs, now)
	}
}

// prTargets are the repositories of a story with their branch and GitHub repository, or why
// a repository is skipped (missing).
func (s *Server) prTargets(p *state.Project, story string) []prTarget {
	st := p.Stories[story]
	var out []prTarget
	add := func(rel, dir string) {
		t := prTarget{project: p.Name, story: story, rel: rel, dir: dir}
		if _, err := os.Stat(dir); err != nil {
			t.missing = "repo missing"
			out = append(out, t)
			return
		}
		b, err := s.App.Git.CurrentBranch(dir)
		ref := "refs/remotes/origin/" + b
		if err != nil || b == "HEAD" || !s.App.Git.RefExists(dir, ref) {
			t.missing = "not pushed"
			out = append(out, t)
			return
		}
		t.branch, t.slug = b, s.App.Git.OriginSlug(dir)
		if t.slug == "" {
			t.missing = "not on GitHub"
		}
		t.head, _ = s.App.Git.RevParse(dir, ref)
		out = append(out, t)
	}
	if p.IsWorkspace() {
		for _, r := range st.Repos {
			add(r.Rel, filepath.Join(s.App.Dir(p, story), r.Rel))
		}
	} else {
		add("", s.App.Dir(p, story))
	}
	return out
}

// savePRLook stores a story's look, refreshes its badge, schedules the next look and sends
// the notifications for what changed. A repository whose look failed keeps its last good PR,
// and its old head so the next tick looks again. A story closed while gh ran is left alone.
func (s *Server) savePRLook(project, story string, ts []prTarget, looks []prLook, now time.Time) {
	path := s.Cfg.PRFile(project, story)
	s.write.Lock()
	if p, err := s.App.LoadProject(project); err != nil || p.Stories[story] == nil {
		s.write.Unlock()
		s.forgetPR(project, story)
		return
	}
	old, err := prwatch.Load(path)
	if err != nil {
		s.Log("%v", err)
	}
	byRepo := map[string]ui.PRInfo{}
	for _, p := range old.PRs {
		byRepo[p.Repo] = p
	}
	me, failed, asked := old.Me, false, false
	prs := make([]ui.PRInfo, len(looks))
	next := old
	next.Heads = map[string]string{}
	for i, l := range looks {
		rel := ts[i].rel
		if l.failed {
			failed = true
			if o := byRepo[rel]; o.Has() {
				prs[i], next.Heads[rel] = o, old.Heads[rel]
				continue
			}
		} else if !asked && l.me != "" {
			me, asked = l.me, true // the first login of this look: a story's repos run as its project's account
		}
		prs[i], next.Heads[rel] = prwatch.Carry(byRepo[rel], l.pr), ts[i].head
	}
	next.Me, next.CheckedAt, next.PRs = me, now.Unix(), prs
	if err := prwatch.Save(path, next); err != nil {
		s.Log("pr %s/%s: %v", project, story, err)
	}
	s.write.Unlock()

	b := prwatch.Badge(prs, me)
	every := prSlow
	if failed || b.Pending || s.storyBusy(project, story) {
		every = prFast
	}
	s.prMu.Lock()
	if s.prBadges[project] == nil {
		s.prBadges[project] = map[string]ui.PRBadge{}
	}
	s.prBadges[project][story] = b
	s.prNext[prKey(project, story)] = now.Add(every)
	s.prMu.Unlock()

	for _, p := range prs {
		for _, e := range prwatch.Events(byRepo[p.Repo], p, me, prwatch.Where{Story: story, Repo: p.Repo}) {
			s.notifyPR(project, story, p.Repo, e)
		}
	}
}

// storyBusy reports whether a session of the story is not idle (from the 1 s agents cache).
func (s *Server) storyBusy(project, story string) bool {
	p, err := s.App.LoadProject(project)
	if err != nil {
		return false
	}
	agents, _ := s.liveAgents()
	by := claude.IndexByUUID(agents)
	for _, n := range p.SessionsOf(story) {
		if a, ok := by[p.Sessions[n].UUID]; ok && a.State() != claude.Idle {
			return true
		}
	}
	return false
}

// notifyPR posts one PR event to ntfy when notifications and PR notifications are on.
func (s *Server) notifyPR(project, story, repo string, e prwatch.Event) {
	st := s.App.Settings
	if st == nil || st.Get("notifications.enabled") == "off" || st.Get("notifications.prs") == "off" {
		return
	}
	u := st.Get("notifications.ntfy_url")
	if u == "" {
		return
	}
	base, err := notify.ClickBase(st.Get("notifications.open_url"), s.Cfg.Serve.Listen, ResolveListen)
	if err != nil {
		s.Log("pr notify: %v", err)
	}
	click := ""
	if base != "" {
		click = notify.PRURL(base, project, story, repo)
	}
	if err := notify.Post(u, e.Title, e.Body, click); err != nil {
		s.Log("pr notify: %v", err)
	}
}

// prBadgesOf is the badges of a project's stories, loaded from the PR files the first time.
func (s *Server) prBadgesOf(project string) map[string]ui.PRBadge {
	s.prMu.Lock()
	_, ok := s.prBadges[project]
	s.prMu.Unlock()
	if !ok {
		p, err := s.App.LoadProject(project)
		if err != nil {
			return nil
		}
		b := map[string]ui.PRBadge{}
		for story := range p.Stories {
			if f, err := prwatch.Load(s.Cfg.PRFile(project, story)); err == nil && len(f.PRs) > 0 {
				b[story] = prwatch.Badge(f.PRs, f.Me)
			}
		}
		s.prMu.Lock()
		if _, ok := s.prBadges[project]; !ok { // the poller may have filled it meanwhile
			s.prBadges[project] = b
		}
		s.prMu.Unlock()
	}
	// copied under the lock: the poller writes the same map
	s.prMu.Lock()
	defer s.prMu.Unlock()
	out := make(map[string]ui.PRBadge, len(s.prBadges[project]))
	for k, v := range s.prBadges[project] {
		if v.Count > 0 {
			out[k] = v
		}
	}
	return out
}

func (s *Server) prRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/projects/{name}/stories/{story}/pr", s.getPR)
	mux.HandleFunc("POST /api/projects/{name}/stories/{story}/pr/refresh", s.refreshPR)
	mux.HandleFunc("POST /api/projects/{name}/stories/{story}/pr/send", s.sendPR)
}

func (s *Server) replyPR(w http.ResponseWriter, project, story string) {
	f, err := prwatch.Load(s.Cfg.PRFile(project, story))
	if err != nil {
		httpError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, f.Data())
}

func (s *Server) getPR(w http.ResponseWriter, r *http.Request) {
	p, story, err := s.loadStory(r)
	if err != nil {
		httpError(w, statusOf(err), err)
		return
	}
	s.replyPR(w, p.Name, story)
}

// refreshPR looks at the story now and answers with the new look.
func (s *Server) refreshPR(w http.ResponseWriter, r *http.Request) {
	p, story, err := s.loadStory(r)
	if err != nil {
		httpError(w, statusOf(err), err)
		return
	}
	s.pollPRs(r.Context(), map[string]bool{prKey(p.Name, story): true})
	s.replyPR(w, p.Name, story)
}

// sendPR pastes and submits a Fix CI or Address prompt for the PR of req.Repo into a session
// of the story, then stamps the sent threads. One send per story at a time (shared with review).
func (s *Server) sendPR(w http.ResponseWriter, r *http.Request) {
	var req api.PRSend
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, fmt.Errorf("decode body: %w", err))
		return
	}
	done, ok := s.oneSend(r.PathValue("name") + "/" + r.PathValue("story"))
	if !ok {
		httpError(w, http.StatusConflict, errors.New("a send to this story is in progress"))
		return
	}
	defer done()

	p, story, err := s.loadStory(r)
	if err != nil {
		httpError(w, statusOf(err), err)
		return
	}
	f, err := prwatch.Load(s.Cfg.PRFile(p.Name, story))
	if err != nil {
		httpError(w, http.StatusInternalServerError, err)
		return
	}
	var pr ui.PRInfo
	for _, x := range f.Data().PRs {
		if x.Repo == req.Repo && x.Has() {
			pr = x
		}
	}
	if !pr.Has() {
		httpError(w, http.StatusNotFound, fmt.Errorf("no pull request for %s", story))
		return
	}
	dir := s.App.Dir(p, story)
	if req.Repo != "" {
		dir = filepath.Join(dir, req.Repo)
	}
	var prompt, title, what string
	switch req.Kind {
	case "fix":
		if !pr.Failed() {
			httpError(w, http.StatusConflict, errors.New("no failed check"))
			return
		}
		logs := map[int64]string{}
		for _, c := range pr.FailedChecks() {
			if c.RunID == 0 || logs[c.RunID] != "" {
				continue
			}
			if out, err := s.App.FailedLog(r.Context(), dir, c.RunID); err == nil {
				logs[c.RunID] = out
			} else {
				s.Log("pr log %d: %v", c.RunID, err)
			}
		}
		branch, _ := s.App.Git.CurrentBranch(dir)
		prompt = prwatch.FixPrompt(story, branch, pr, logs, s.setting("claude.fix_ci"))
		title, what = "Fix CI", "CI fix"
	case "address":
		if len(req.Threads) == 0 {
			httpError(w, http.StatusConflict, errors.New("pick a comment first"))
			return
		}
		prompt = prwatch.AddressPrompt(story, pr, req.Threads, s.setting("claude.address"))
		title, what = "Review comments", "Comments"
	default:
		httpError(w, http.StatusBadRequest, fmt.Errorf("kind %q: want fix or address", req.Kind))
		return
	}

	s.write.Lock()
	p, err = s.App.LoadProject(p.Name)
	var pl sendPlan
	if err == nil {
		pl, err = s.pickSession(p, story, api.ReviewSend{Session: req.Session, New: req.New}, title)
	}
	s.write.Unlock()
	if err != nil {
		httpError(w, reviewStatus(err), err)
		return
	}
	pl.prompt = prompt
	if !s.deliver(w, r, pl, true, what) {
		return
	}
	msg := fmt.Sprintf("Sent the failed checks of #%d to [%d]", pr.Number, pl.num)
	if req.Kind == "address" {
		msg = fmt.Sprintf("Sent %s to [%d]", plural(len(req.Threads), "comment", "comments"), pl.num)
		s.write.Lock()
		f, err := prwatch.Load(s.Cfg.PRFile(p.Name, story))
		if err == nil {
			f.MarkSent(req.Threads, time.Now().Unix())
			err = prwatch.Save(s.Cfg.PRFile(p.Name, story), f)
		}
		s.write.Unlock()
		if err != nil {
			httpError(w, http.StatusInternalServerError, fmt.Errorf("sent to [%d] but couldn't mark the comments sent: %w", pl.num, err))
			return
		}
	}
	if pl.verb != "" {
		msg = fmt.Sprintf("%s [%d] and %s", pl.verb, pl.num, strings.ToLower(msg[:1])+msg[1:])
	}
	writeJSON(w, api.Result{Num: pl.num, Message: msg})
}

func (s *Server) setting(key string) string {
	if s.App.Settings == nil {
		return ""
	}
	return s.App.Settings.Get(key)
}

// plural is "1 comment" or "2 comments".
func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}
