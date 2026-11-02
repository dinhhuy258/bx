package serve

import (
	"net"
	"net/http"
	"os"
	"path"
	"strings"
)

// FilesPort is the artifact file server's port with files_listen = "auto".
const FilesPort = "7358"

// filesCSP runs a page's own scripts in an opaque origin: it can never call the bx API (whose
// same-origin guard refuses Origin "null"), read another origin's storage, or reach the
// listener on :7357 as same-origin.
const filesCSP = "sandbox allow-scripts allow-forms allow-popups allow-popups-to-escape-sandbox allow-modals"

// ResolveFiles turns serve.files_listen into the file server's host:port, or "" when it is
// "off". "auto" (or empty) is the API's host on FilesPort; an explicit address follows the same
// Tailscale-or-loopback rule as serve.listen.
func ResolveFiles(cfg, apiAddr string) (string, error) {
	switch cfg {
	case "off":
		return "", nil
	case "", "auto":
		host, _, err := net.SplitHostPort(apiAddr)
		if err != nil {
			return "", err
		}
		return net.JoinHostPort(host, FilesPort), nil
	}
	return ResolveListen(cfg)
}

// FilesHandler serves the session artifacts under root, read-only: GET or HEAD of
// /<project>/<num>/<file…>. Folders, dotfiles and anything outside root (by "..", or by a
// symlink, which os.Root refuses) are 404. It is the second listener of bx serve, on its own
// port, so the pages it serves are never same-origin with the API.
func FilesHandler(root string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "read-only", http.StatusMethodNotAllowed)
			return
		}
		rel := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		parts := strings.Split(rel, "/")
		if len(parts) < 3 {
			http.NotFound(w, r)
			return
		}
		for _, part := range parts {
			if strings.HasPrefix(part, ".") {
				http.NotFound(w, r)
				return
			}
		}
		rt, err := os.OpenRoot(root)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer func() { _ = rt.Close() }()
		f, err := rt.Open(rel)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer func() { _ = f.Close() }()
		fi, err := f.Stat()
		if err != nil || !fi.Mode().IsRegular() {
			http.NotFound(w, r)
			return
		}
		h := w.Header()
		h.Set("Content-Security-Policy", filesCSP)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Cache-Control", "no-store")
		h.Set("Referrer-Policy", "no-referrer")
		http.ServeContent(w, r, fi.Name(), fi.ModTime(), f)
	})
}
