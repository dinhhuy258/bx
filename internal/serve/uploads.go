package serve

import (
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	"github.com/dinhhuy258/bx/internal/api"
	"github.com/dinhhuy258/bx/internal/uploads"
)

// maxUploadBody caps an upload request: one file of at most 20 MB, base64, in JSON.
// JSON keeps the same-origin guard's application/json rule without a route exception.
const maxUploadBody = 28 << 20

// uploadFile saves one file sent from the phone for a session and replies with its path,
// which the web client puts in the message box for Claude to read.
func (s *Server) uploadFile(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBody)
	var req api.UploadRequest
	if !decodeSync(w, r, &req, "upload over 28 MB (one file of at most 20 MB)") {
		return
	}
	num, err := strconv.Atoi(r.PathValue("num"))
	if err != nil {
		httpError(w, http.StatusNotFound, fmt.Errorf("no session %q", r.PathValue("num")))
		return
	}
	p, err := s.load(r.PathValue("name"))
	if err != nil {
		httpError(w, statusOf(err), err)
		return
	}
	if p.Sessions[num] == nil {
		httpError(w, http.StatusNotFound, fmt.Errorf("no session #%d", num))
		return
	}
	path, err := uploads.Save(s.Cfg.UploadsDir(), p.Name, num, req.Name, req.Content, time.Now())
	if err != nil {
		code := http.StatusInternalServerError
		switch {
		case errors.Is(err, uploads.ErrTooLarge):
			code = http.StatusRequestEntityTooLarge
		case errors.Is(err, uploads.ErrInvalid):
			code = http.StatusBadRequest
		}
		httpError(w, code, err)
		return
	}
	s.Log("upload: %s #%d %s (%d bytes)", p.Name, num, filepath.Base(path), len(req.Content))
	writeJSON(w, api.UploadResult{Path: path})
}
