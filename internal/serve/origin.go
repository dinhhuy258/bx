package serve

import (
	"errors"
	"mime"
	"net/http"
	"net/url"
)

// sameOrigin refuses requests a web page on another site could send from a browser on the
// tailnet: any request whose Origin is not this server, and any POST or PUT that is not JSON
// (a browser can only send JSON cross-site after a CORS preflight, which bx never answers).
// The bx TUI sends no Origin and always posts JSON.
func sameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if o := r.Header.Get("Origin"); o != "" {
			if u, err := url.Parse(o); err != nil || u.Host != r.Host {
				httpError(w, http.StatusForbidden, errors.New("cross-origin request refused"))
				return
			}
		}
		if r.Method == http.MethodPost || r.Method == http.MethodPut {
			if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
				httpError(w, http.StatusUnsupportedMediaType, errors.New("want Content-Type: application/json"))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
