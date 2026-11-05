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

// IsPageName reports whether a file at the top of a session's artifacts folder is a page: an
// .html or .md file (the file server renders Markdown) whose name does not start with a dot.
func IsPageName(name string) bool {
	ext := filepath.Ext(name)
	return !strings.HasPrefix(name, ".") && (strings.EqualFold(ext, ".html") || strings.EqualFold(ext, ".md"))
}

// listArtifacts lists the pages (.html and .md) at the top of a session's artifacts folder, newest
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
		if !e.Type().IsRegular() || !IsPageName(name) {
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
