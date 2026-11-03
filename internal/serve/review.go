package serve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/dinhhuy258/bx/internal/api"
	"github.com/dinhhuy258/bx/internal/claude"
	"github.com/dinhhuy258/bx/internal/review"
	"github.com/dinhhuy258/bx/internal/state"
)

// readyTimeout and readyPoll bound the wait for a resumed or new Claude to be idle before the
// comments are pasted. Tests shorten them.
var (
	readyTimeout = 30 * time.Second
	readyPoll    = 500 * time.Millisecond
)

const maxReviewBody = 2 << 20 // a PUT of 200 comments of 4 KB fits

func (s *Server) reviewRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/projects/{name}/stories/{story}/review", s.getReview)
	mux.HandleFunc("PUT /api/projects/{name}/stories/{story}/review", s.putReview)
	mux.HandleFunc("POST /api/projects/{name}/stories/{story}/review/send", s.sendReview)
	mux.HandleFunc("POST /api/projects/{name}/stories/{story}/review/clear-sent", s.clearSent)
}

// codeError is an error with the HTTP status it answers with.
type codeError struct {
	code int
	error
}

func (e codeError) Unwrap() error { return e.error }

func reviewStatus(err error) int {
	var ce codeError
	switch {
	case errors.As(err, &ce):
		return ce.code
	case errors.Is(err, review.ErrTooMany), errors.Is(err, review.ErrTooLong):
		return http.StatusRequestEntityTooLarge
	case errors.Is(err, review.ErrSentChanged), errors.Is(err, review.ErrUnknown):
		return http.StatusConflict
	}
	return statusOf(err)
}

func writeStatus(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// loadStory loads the request's project and checks its story exists.
func (s *Server) loadStory(r *http.Request) (*state.Project, string, error) {
	p, err := s.load(r.PathValue("name"))
	if err != nil {
		return nil, "", err
	}
	story := r.PathValue("story")
	if p.Stories[story] == nil {
		return nil, "", notFound{fmt.Errorf("no story %s", story)}
	}
	return p, story, nil
}

// readReview loads a comment file. A corrupt one was moved aside: it is logged and the
// review starts empty.
func (s *Server) readReview(path string) (review.File, error) {
	f, err := review.Load(path)
	var ce *state.CorruptError
	if errors.As(err, &ce) {
		s.Log("%v", err)
		return review.File{}, nil
	}
	return f, err
}

// reviewRepo is one repository of a story's diff: its worktree, its base and the prefix its
// paths get (a workspace repo's folder).
type reviewRepo struct{ dir, base, prefix string }

func (s *Server) reviewRepos(p *state.Project, story string) []reviewRepo {
	st := p.Stories[story]
	if p.IsWorkspace() {
		var rs []reviewRepo
		for _, r := range st.Repos {
			rs = append(rs, reviewRepo{filepath.Join(s.App.Dir(p, story), r.Rel), r.Base, r.Rel + "/"})
		}
		return rs
	}
	base := st.Base
	if base == "" { // stories made before their base was recorded
		base, _ = s.App.Git.LocalDefault(p.Path)
	}
	return []reviewRepo{{s.App.Dir(p, story), base, ""}}
}

// reviewBase names what the diff is against: the base, or "each repo's base" for a workspace
// whose repositories differ.
func reviewBase(rs []reviewRepo) string {
	if len(rs) == 0 {
		return ""
	}
	for _, r := range rs[1:] {
		if r.base != rs[0].base {
			return "each repo's base"
		}
	}
	return rs[0].base
}

// buildReview is the story's diff with f's comments placed on it. In a workspace a repo whose
// diff fails is left out with a note; a single repo's failure is the error.
func (s *Server) buildReview(ctx context.Context, p *state.Project, story string, f review.File) (api.Review, error) {
	repos := s.reviewRepos(p, story)
	out := api.Review{Base: reviewBase(repos), Files: []api.DiffFile{}, ReviewComments: api.ReviewComments{Version: f.Version}}
	var notes []string
	for _, rp := range repos {
		d, err := review.Diff(ctx, s.App.Git, rp.dir, rp.base)
		name := strings.TrimSuffix(rp.prefix, "/")
		if err != nil {
			if !p.IsWorkspace() {
				return api.Review{}, err
			}
			notes = append(notes, name+": "+err.Error())
			continue
		}
		if d.Note != "" {
			if name != "" {
				d.Note = name + ": " + d.Note
			}
			notes = append(notes, d.Note)
		}
		for _, df := range d.Files {
			df.Path = rp.prefix + df.Path
			if df.Old != "" {
				df.Old = rp.prefix + df.Old
			}
			out.Files = append(out.Files, df)
		}
		out.Total += d.Total
	}
	out.Note = strings.Join(notes, " · ")
	out.Files, out.Comments = review.Place(out.Files, f.Comments)
	return out, nil
}

func (s *Server) getReview(w http.ResponseWriter, r *http.Request) {
	p, story, err := s.loadStory(r)
	if err != nil {
		httpError(w, statusOf(err), err)
		return
	}
	f, err := s.readReview(s.Cfg.ReviewFile(p.Name, story))
	if err != nil {
		httpError(w, http.StatusInternalServerError, err)
		return
	}
	s.replyReview(w, r, p, story, f)
}

func (s *Server) replyReview(w http.ResponseWriter, r *http.Request, p *state.Project, story string, f review.File) {
	out, err := s.buildReview(r.Context(), p, story, f)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, out)
}

// editReview loads the story's comments under the write lock, lets fn change them and saves
// them with the next version.
func (s *Server) editReview(r *http.Request, fn func(review.File) (review.File, error)) (*state.Project, string, review.File, error) {
	s.write.Lock()
	defer s.write.Unlock()
	p, story, err := s.loadStory(r)
	if err != nil {
		return nil, "", review.File{}, err
	}
	path := s.Cfg.ReviewFile(p.Name, story)
	f, err := s.readReview(path)
	if err != nil {
		return nil, "", review.File{}, codeError{http.StatusInternalServerError, err}
	}
	next, err := fn(f)
	if err != nil {
		return nil, "", review.File{}, err
	}
	next.Version = f.Version + 1
	if err := review.Save(path, next); err != nil {
		return nil, "", review.File{}, codeError{http.StatusInternalServerError, err}
	}
	return p, story, next, nil
}

func (s *Server) putReview(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxReviewBody)
	var req api.ReviewComments
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			httpError(w, http.StatusRequestEntityTooLarge, errors.New("comments are over 2 MB"))
			return
		}
		httpError(w, http.StatusBadRequest, fmt.Errorf("decode body: %w", err))
		return
	}
	p, story, f, err := s.editReview(r, func(f review.File) (review.File, error) {
		if req.Version != f.Version {
			return f, codeError{http.StatusConflict, errors.New("Comments changed on another device")}
		}
		cs, err := review.Apply(f.Comments, req.Comments)
		return review.File{Comments: cs}, err
	})
	if err != nil {
		httpError(w, reviewStatus(err), err)
		return
	}
	s.replyReview(w, r, p, story, f)
}

func (s *Server) clearSent(w http.ResponseWriter, r *http.Request) {
	p, story, f, err := s.editReview(r, func(f review.File) (review.File, error) {
		return review.File{Comments: review.ClearSent(f.Comments)}, nil
	})
	if err != nil {
		httpError(w, reviewStatus(err), err)
		return
	}
	s.replyReview(w, r, p, story, f)
}

// sendPlan is what a send does, settled under the write lock: the session (resumed or started
// when verb is set), the comments and the prompt.
type sendPlan struct {
	p      *state.Project
	num    int
	uuid   string
	verb   string // "", "Resumed" or "Started"
	ids    []string
	prompt string
}

// planSend picks the session, resuming or starting it, and builds the prompt of the unsent
// comments.
func (s *Server) planSend(r *http.Request, req api.ReviewSend) (sendPlan, error) {
	s.write.Lock()
	defer s.write.Unlock()
	p, story, err := s.loadStory(r)
	if err != nil {
		return sendPlan{}, err
	}
	f, err := s.readReview(s.Cfg.ReviewFile(p.Name, story))
	if err != nil {
		return sendPlan{}, codeError{http.StatusInternalServerError, err}
	}
	unsent := review.Unsent(f.Comments)
	if len(unsent) == 0 {
		return sendPlan{}, codeError{http.StatusConflict, errors.New("Nothing to send")}
	}
	pl := sendPlan{p: p}
	if req.New {
		num, err := s.App.NewSession(p, story, "Review comments")
		if err != nil {
			return sendPlan{}, codeError{http.StatusInternalServerError, err}
		}
		s.markStarted(p.Name, num)
		pl.num, pl.verb = num, "Started"
	} else {
		if !slices.Contains(p.SessionsOf(story), req.Session) {
			return sendPlan{}, notFound{fmt.Errorf("no session [%d] in %s", req.Session, story)}
		}
		live, err := s.App.Live(p)
		if err != nil {
			return sendPlan{}, codeError{http.StatusInternalServerError, err}
		}
		pl.num = req.Session
		if _, ok := live[req.Session]; !ok {
			if err := s.App.Resume(p, req.Session); err != nil {
				return sendPlan{}, codeError{http.StatusInternalServerError, err}
			}
			s.markStarted(p.Name, req.Session)
			pl.verb = "Resumed"
		}
	}
	pl.uuid = p.Sessions[pl.num].UUID
	pl.prompt = review.Prompt(story, reviewBase(s.reviewRepos(p, story)), unsent)
	for _, c := range unsent {
		pl.ids = append(pl.ids, c.ID)
	}
	return pl, nil
}

// sendReview pastes the unsent comments into a session of the story, without Enter, then
// marks them sent. A session that is not running is resumed (or a new one started) and the
// paste waits until its Claude is idle. One send per story at a time.
func (s *Server) sendReview(w http.ResponseWriter, r *http.Request) {
	var req api.ReviewSend
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, fmt.Errorf("decode body: %w", err))
		return
	}
	key := r.PathValue("name") + "/" + r.PathValue("story")
	s.mu.Lock()
	if s.sending[key] {
		s.mu.Unlock()
		httpError(w, http.StatusConflict, errors.New("A send to this story is in progress"))
		return
	}
	s.sending[key] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.sending, key)
		s.mu.Unlock()
	}()

	pl, err := s.planSend(r, req)
	if err != nil {
		httpError(w, reviewStatus(err), err)
		return
	}
	if pl.verb != "" {
		if err := s.waitReady(r.Context(), pl.uuid); err != nil {
			writeStatus(w, http.StatusGatewayTimeout, api.Result{Num: pl.num,
				Error: fmt.Sprintf("%s [%d], but %v. Comments not sent.", pl.verb, pl.num, err)})
			return
		}
	}
	sid, err := s.App.Target(pl.p, pl.num)
	if err == nil {
		err = s.App.Tmux.Paste(sid, pl.prompt)
	}
	if err != nil {
		writeStatus(w, http.StatusBadGateway, api.Result{Num: pl.num,
			Error: fmt.Sprintf("Couldn't paste into [%d]. Comments not sent: %v", pl.num, err)})
		return
	}
	at := time.Now().Unix()
	if _, _, _, err := s.editReview(r, func(f review.File) (review.File, error) {
		return review.File{Comments: review.MarkSent(f.Comments, pl.ids, pl.num, at)}, nil
	}); err != nil {
		httpError(w, http.StatusInternalServerError, fmt.Errorf("pasted into [%d] but couldn't mark the comments sent: %w", pl.num, err))
		return
	}
	n := len(pl.ids)
	what := fmt.Sprintf("%d %s", n, map[bool]string{true: "comment", false: "comments"}[n == 1])
	msg := fmt.Sprintf("Pasted %s into [%d]", what, pl.num)
	if pl.verb != "" {
		msg = fmt.Sprintf("%s [%d] and pasted %s", pl.verb, pl.num, what)
	}
	writeJSON(w, api.Result{Num: pl.num, Message: msg})
}

// waitReady waits until the Claude of session uuid is idle: resumed or new, its input box is
// up. It asks claude agents itself, not the 1 s cache of the polls.
func (s *Server) waitReady(ctx context.Context, uuid string) error {
	ctx, cancel := context.WithTimeout(ctx, readyTimeout)
	defer cancel()
	t := time.NewTicker(readyPoll)
	defer t.Stop()
	for {
		if agents, err := s.Lister.Agents(ctx); err == nil {
			for _, a := range agents {
				if a.SessionID == uuid && a.State() == claude.Idle {
					return nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("Claude was not ready after %s", readyTimeout)
		case <-t.C:
		}
	}
}

// reviewCounts is the unsent comments of each story with some, for ui.Live.Review.
func (s *Server) reviewCounts(p *state.Project) map[string]int {
	var out map[string]int
	for story := range p.Stories {
		f, err := s.readReview(s.Cfg.ReviewFile(p.Name, story))
		if err != nil {
			continue
		}
		if n, _ := review.Counts(f.Comments); n > 0 {
			if out == nil {
				out = map[string]int{}
			}
			out[story] = n
		}
	}
	return out
}
