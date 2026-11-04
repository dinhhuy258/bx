package serve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/dinhhuy258/bx/internal/api"
	"github.com/dinhhuy258/bx/internal/review"
	"github.com/dinhhuy258/bx/internal/state"
)

// explainTimeout bounds one answer; maxRunning is how many may run at once in a story.
var explainTimeout = 3 * time.Minute

const (
	maxRunning     = 2
	maxQuestion    = 2000
	restartedError = "stopped: bx serve restarted"
)

// readExplains loads an explain file; a corrupt one is logged and starts empty.
func (s *Server) readExplains(path string) (review.ExplainFile, error) {
	f, err := review.LoadExplains(path)
	var ce *state.CorruptError
	if errors.As(err, &ce) {
		s.Log("%v", err)
		return review.ExplainFile{}, nil
	}
	return f, err
}

// liveExplains shows an explanation stored as running that no goroutine runs (bx serve
// restarted) as failed.
func (s *Server) liveExplains(es []api.Explain) []api.Explain {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := slices.Clone(es)
	for i := range out {
		if out[i].Running() && s.explaining[out[i].ID] == nil {
			out[i].State, out[i].Error = api.ExplainFailed, restartedError
		}
	}
	return out
}

// editExplains loads the story's explanations under the write lock, lets fn change them and
// saves them.
func (s *Server) editExplains(p *state.Project, story string, fn func(*review.ExplainFile) error) error {
	path := s.Cfg.ExplainFile(p.Name, story)
	f, err := s.readExplains(path)
	if err != nil {
		return codeError{http.StatusInternalServerError, err}
	}
	if err := fn(&f); err != nil {
		return err
	}
	if err := review.SaveExplains(path, f); err != nil {
		return codeError{http.StatusInternalServerError, err}
	}
	return nil
}

func checkExplain(req api.ExplainRequest) error {
	switch {
	case req.File == "" || (req.Side != "new" && req.Side != "old") || req.Line < 1:
		return errors.New("an explanation needs a file, a side (new or old) and a line")
	case req.StartLine < 0 || req.StartLine > req.Line:
		return fmt.Errorf("the lines run backwards (%s:%d-%d)", req.File, req.StartLine, req.Line)
	case len(req.Code) > review.MaxBody:
		return review.ErrTooLong
	case len(req.Question) > maxQuestion:
		return fmt.Errorf("a question is over %d bytes", maxQuestion)
	case req.Session == 0:
		return errors.New("pick a session or code only")
	}
	return nil
}

// postExplain starts an explanation and replies with the review, where it shows as running.
func (s *Server) postExplain(w http.ResponseWriter, r *http.Request) {
	var req api.ExplainRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, fmt.Errorf("decode body: %w", err))
		return
	}
	if err := checkExplain(req); err != nil {
		httpError(w, reviewStatus(codeError{http.StatusBadRequest, err}), err)
		return
	}
	s.write.Lock()
	p, story, err := s.loadStory(r)
	if err == nil && req.Session > 0 && !slices.Contains(p.SessionsOf(story), req.Session) {
		err = notFound{fmt.Errorf("no session [%d] in %s", req.Session, story)}
	}
	var e api.Explain
	if err == nil {
		e = api.Explain{ID: review.NewID(), File: req.File, Side: req.Side, StartLine: req.StartLine, Line: req.Line,
			Code: req.Code, Question: strings.TrimSpace(req.Question), State: api.ExplainRunning, Started: time.Now().Unix()}
		if e.StartLine == e.Line {
			e.StartLine = 0
		}
		if req.Session > 0 {
			e.Session, e.SessionTitle = req.Session, p.Sessions[req.Session].Title
		}
		err = s.editExplains(p, story, func(f *review.ExplainFile) error {
			running := 0
			for _, x := range s.liveExplains(f.Explains) {
				if x.Running() {
					running++
				}
			}
			if running >= maxRunning {
				return codeError{http.StatusConflict, fmt.Errorf("%d explanations are running in %s · wait for one or stop it", running, story)}
			}
			f.With = req.Session
			f.Explains = review.TrimExplains(append(f.Explains, e))
			return nil
		})
	}
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), explainTimeout)
		s.mu.Lock()
		s.explaining[e.ID] = cancel
		s.mu.Unlock()
		go s.runExplain(ctx, p, story, e)
	}
	s.write.Unlock()
	if err != nil {
		httpError(w, reviewStatus(err), err)
		return
	}
	s.reply(w, r, p, story)
}

// runExplain asks Claude and stores the answer or the error, unless the explanation was
// dismissed meanwhile.
func (s *Server) runExplain(ctx context.Context, p *state.Project, story string, e api.Explain) {
	start := time.Now()
	answer, err := s.App.Explain(ctx, p, story, max(e.Session, 0), review.ExplainPrompt(e, e.Session > 0))
	s.mu.Lock()
	cancel := s.explaining[e.ID]
	delete(s.explaining, e.ID)
	s.mu.Unlock()
	if cancel == nil {
		return // dismissed: deleteExplain removed it
	}
	cancel()
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		err = fmt.Errorf("no answer after %s", explainTimeout)
	case err != nil:
		err = errors.New(strings.TrimSpace(err.Error()))
	}
	s.write.Lock()
	defer s.write.Unlock()
	if err := s.editExplains(p, story, func(f *review.ExplainFile) error {
		for i := range f.Explains {
			if f.Explains[i].ID == e.ID {
				f.Explains[i].State, f.Explains[i].Answer, f.Explains[i].TookMS = api.ExplainDone, answer, time.Since(start).Milliseconds()
				if err != nil {
					f.Explains[i].State, f.Explains[i].Error = api.ExplainFailed, err.Error()
				}
			}
		}
		return nil
	}); err != nil {
		s.Log("explain %s: %v", e.ID, err)
	}
}

// deleteExplain stops a running explanation or dismisses an answer.
func (s *Server) deleteExplain(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.write.Lock()
	p, story, err := s.loadStory(r)
	if err == nil {
		s.mu.Lock()
		if cancel := s.explaining[id]; cancel != nil {
			delete(s.explaining, id)
			cancel()
		}
		s.mu.Unlock()
		err = s.editExplains(p, story, func(f *review.ExplainFile) error {
			f.Explains = slices.DeleteFunc(f.Explains, func(e api.Explain) bool { return e.ID == id })
			return nil
		})
	}
	s.write.Unlock()
	if err != nil {
		httpError(w, reviewStatus(err), err)
		return
	}
	s.reply(w, r, p, story)
}

// reply sends the story's review as it is now.
func (s *Server) reply(w http.ResponseWriter, r *http.Request, p *state.Project, story string) {
	f, err := s.readReview(s.Cfg.ReviewFile(p.Name, story))
	if err != nil {
		httpError(w, http.StatusInternalServerError, err)
		return
	}
	s.replyReview(w, r, p, story, f)
}
