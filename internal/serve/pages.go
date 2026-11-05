package serve

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/dinhhuy258/bx/internal/api"
)

// pages tracks, by Claude session uuid, the pages Claude wrote and the open streams to tell.
// It lives in memory: a restart forgets the pages nobody opened, which the artifacts list
// still has. Every method is called with Server.mu held.
type pages struct {
	names map[string]map[string]bool                // the names announced, for "New page" vs "Updated"
	last  map[string][]byte                         // the page frame no client has opened or dismissed yet
	subs  map[string]map[*stream]func(frame []byte) // the open streams of the session: write a text frame
}

func newPages() pages {
	return pages{names: map[string]map[string]bool{}, last: map[string][]byte{}, subs: map[string]map[*stream]func([]byte){}}
}

// wrote records a page Claude wrote and returns its frame and the streams to send it to. The
// page is new only on the first write of its name that the hook saw as a new file.
func (p *pages) wrote(uuid string, page api.Artifact, isNew bool) ([]byte, []func([]byte)) {
	if p.names[uuid] == nil {
		p.names[uuid] = map[string]bool{}
	}
	isNew = isNew && !p.names[uuid][page.Name]
	p.names[uuid][page.Name] = true
	frame, _ := json.Marshal(api.Control{Type: "page", Page: &page, New: isNew})
	p.last[uuid] = frame
	return frame, p.writers(uuid, nil)
}

// seen forgets the session's page once a client opened or dismissed it, and returns the other
// streams of the session, to tell them.
func (p *pages) seen(uuid string, from *stream) []func([]byte) {
	if _, ok := p.last[uuid]; !ok {
		return nil
	}
	delete(p.last, uuid)
	return p.writers(uuid, from)
}

// subscribe adds a stream of the session and returns the page it has not seen yet, nil if none.
func (p *pages) subscribe(uuid string, st *stream, send func([]byte)) []byte {
	if p.subs[uuid] == nil {
		p.subs[uuid] = map[*stream]func([]byte){}
	}
	p.subs[uuid][st] = send
	return p.last[uuid]
}

func (p *pages) unsubscribe(uuid string, st *stream) {
	delete(p.subs[uuid], st)
	if len(p.subs[uuid]) == 0 {
		delete(p.subs, uuid)
	}
}

// writers are the senders of the session's open streams, all but skip.
func (p *pages) writers(uuid string, skip *stream) []func([]byte) {
	var out []func([]byte)
	for st, send := range p.subs[uuid] {
		if st != skip {
			out = append(out, send)
		}
	}
	return out
}

// pageSeenFrame tells the other clients of a session that its page was opened or dismissed.
var pageSeenFrame, _ = json.Marshal(api.Control{Type: "page_seen"})

// pageHook serves POST /api/hooks/page from `bx page-hook`: Claude wrote or edited a page. The
// server trusts only the session id and the path: the file must be a page at the top of that
// session's artifacts folder, and the link is built here. The page then goes to the session's
// open streams, and to the next one that opens until a client has seen it.
func (s *Server) pageHook(w http.ResponseWriter, r *http.Request) {
	var h api.PageHook
	if err := json.NewDecoder(r.Body).Decode(&h); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	project, num, name, ok := pageOf(s.Cfg.ArtifactsDir(), h.Path)
	if !ok || h.SessionID == "" {
		httpError(w, http.StatusBadRequest, errors.New("not a page of a session"))
		return
	}
	p, err := s.load(project)
	if err != nil {
		httpError(w, statusOf(err), err)
		return
	}
	if se := p.Sessions[num]; se == nil || se.UUID != h.SessionID {
		httpError(w, http.StatusNotFound, errors.New("no such session"))
		return
	}
	fi, err := os.Lstat(filepath.Join(s.App.ArtifactsDir(p.Name, num), name))
	if err != nil || !fi.Mode().IsRegular() {
		httpError(w, http.StatusNotFound, errors.New("no such page"))
		return
	}
	page := api.Artifact{Name: name, Size: fi.Size(), Modified: fi.ModTime()}
	if link := s.App.ArtifactsLink(p.Name, num); link != "" {
		page.URL = link + "/" + url.PathEscape(name)
	}
	s.mu.Lock()
	frame, sends := s.pages.wrote(h.SessionID, page, h.New)
	s.mu.Unlock()
	for _, send := range sends {
		send(frame)
	}
	w.WriteHeader(http.StatusNoContent)
}

// pageOf splits a page's path under the artifacts root into <project>/<num>/<name>. The name
// must be a page (IsPageName). A root reached through a symlink matches too.
func pageOf(root, path string) (project string, num int, name string, ok bool) {
	if !filepath.IsAbs(path) {
		return "", 0, "", false
	}
	path = filepath.Clean(path)
	rel, err := filepath.Rel(root, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		rr, err1 := filepath.EvalSymlinks(root)
		rd, err2 := filepath.EvalSymlinks(filepath.Dir(path))
		if err1 != nil || err2 != nil {
			return "", 0, "", false
		}
		if rel, err = filepath.Rel(rr, filepath.Join(rd, filepath.Base(path))); err != nil {
			return "", 0, "", false
		}
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) != 3 || parts[0] == "" || parts[0] == ".." {
		return "", 0, "", false
	}
	n, err := strconv.Atoi(parts[1])
	name = parts[2]
	if err != nil || n <= 0 || !IsPageName(name) {
		return "", 0, "", false
	}
	return parts[0], n, name, true
}

// subscribePages adds an open stream of the Claude session uuid and sends it the page it has not
// seen yet. send writes one text frame to the stream's socket.
func (s *Server) subscribePages(uuid string, st *stream, send func([]byte)) {
	s.mu.Lock()
	frame := s.pages.subscribe(uuid, st, send)
	s.mu.Unlock()
	if frame != nil {
		send(frame)
	}
}

func (s *Server) unsubscribePages(uuid string, st *stream) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pages.unsubscribe(uuid, st)
}

// pageSeen is a client's page_seen: forget the page and close it on the other clients.
func (s *Server) pageSeen(uuid string, from *stream) {
	s.mu.Lock()
	sends := s.pages.seen(uuid, from)
	s.mu.Unlock()
	for _, send := range sends {
		send(pageSeenFrame)
	}
}
