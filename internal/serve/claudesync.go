package serve

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"

	"github.com/dinhhuy258/bx/internal/api"
	"github.com/dinhhuy258/bx/internal/claude"
	"github.com/dinhhuy258/bx/internal/claudesync"
)

const (
	maxPlanBody  = 8 << 20  // a manifest: paths and hashes
	maxApplyBody = 28 << 20 // 20 MB of files, base64, JSON
)

// syncRecord holds the paths bx synced.
func (s *Server) syncRecord() string { return filepath.Join(s.Cfg.Root, "claude-sync.json") }

// claudeSyncPlan compares the Mac's manifest with the server's files and the record.
func (s *Server) claudeSyncPlan(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxPlanBody)
	var req api.SyncPlanRequest
	if !decodeSync(w, r, &req, "plan request over 8 MB") {
		return
	}
	dir := claude.ConfigDir()
	if dir == "" {
		httpError(w, 500, errors.New("no Claude config dir on the server"))
		return
	}
	mac := make([]claudesync.File, 0, len(req.Files))
	for _, f := range req.Files {
		if err := claudesync.ValidPath(f.Path); err != nil {
			httpError(w, 400, err)
			return
		}
		mac = append(mac, claudesync.File{Path: f.Path, SHA256: f.SHA256, Exec: f.Exec})
	}
	server, err := claudesync.ScanServer(dir)
	if err != nil {
		httpError(w, 500, err)
		return
	}
	record, err := claudesync.LoadRecord(s.syncRecord())
	if err != nil {
		httpError(w, 500, err)
		return
	}
	items := []api.SyncItem{}
	for _, it := range claudesync.MakePlan(dir, mac, server, record) {
		items = append(items, api.SyncItem{Name: it.Name, Status: it.Status, Add: it.Add, Update: it.Update, Delete: it.Delete, Error: it.Error})
	}
	writeJSON(w, api.SyncPlan{Dir: dir, Items: items})
}

// claudeSyncApply writes the Mac's adds and updates, deletes, and saves the record.
func (s *Server) claudeSyncApply(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxApplyBody)
	var req api.SyncApplyRequest
	if !decodeSync(w, r, &req, "sync request over 28 MB (about 20 MB of files once base64-encoded)") {
		return
	}
	dir := claude.ConfigDir()
	if dir == "" {
		httpError(w, 500, errors.New("no Claude config dir on the server"))
		return
	}
	writes := make([]claudesync.Write, 0, len(req.Files))
	for _, f := range req.Files {
		writes = append(writes, claudesync.Write{Path: f.Path, Exec: f.Exec, Content: f.Content})
	}
	s.write.Lock()
	defer s.write.Unlock()
	res, err := claudesync.Apply(dir, s.syncRecord(), req.Items, req.Paths, writes, req.Delete)
	if err != nil {
		code := 500
		switch {
		case errors.Is(err, claudesync.ErrTooLarge):
			code = http.StatusRequestEntityTooLarge
		case errors.Is(err, claudesync.ErrInvalid):
			code = http.StatusBadRequest
		}
		s.Log("claude sync: %d added, %d updated, %d deleted, then: %v", res.Added, res.Updated, res.Deleted, err)
		httpError(w, code, err)
		return
	}
	s.Log("claude sync: %d added, %d updated, %d deleted", res.Added, res.Updated, res.Deleted)
	writeJSON(w, api.SyncResult{Added: res.Added, Updated: res.Updated, Deleted: res.Deleted})
}

// decodeSync decodes the body into v; a body over its MaxBytesReader is a 413 saying tooBig.
func decodeSync(w http.ResponseWriter, r *http.Request, v any, tooBig string) bool {
	err := json.NewDecoder(r.Body).Decode(v)
	var mb *http.MaxBytesError
	switch {
	case errors.As(err, &mb):
		httpError(w, http.StatusRequestEntityTooLarge, errors.New(tooBig))
		return false
	case err != nil:
		httpError(w, 400, fmt.Errorf("decode body: %w", err))
		return false
	}
	return true
}
