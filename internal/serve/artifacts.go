package serve

import (
	"errors"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dinhhuy258/bx/internal/api"
)

// listArtifacts lists the .html pages at the top of a session's artifacts folder, newest
// first, with their file server links. No folder yet is an empty list.
func (s *Server) listArtifacts(w http.ResponseWriter, r *http.Request) {
	p, num, ok := s.session(w, r)
	if !ok {
		return
	}
	entries, err := os.ReadDir(s.App.ArtifactsDir(p.Name, num))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		httpError(w, http.StatusInternalServerError, err)
		return
	}
	link := s.App.ArtifactsLink(p.Name, num)
	files := []api.Artifact{}
	for _, e := range entries {
		name := e.Name()
		if !e.Type().IsRegular() || strings.HasPrefix(name, ".") || !strings.EqualFold(filepath.Ext(name), ".html") {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		f := api.Artifact{Name: name, Size: fi.Size(), Modified: fi.ModTime()}
		if link != "" {
			f.URL = link + "/" + url.PathEscape(name)
		}
		files = append(files, f)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Modified.After(files[j].Modified) })
	writeJSON(w, api.ArtifactList{Files: files})
}
