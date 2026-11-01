// Package web is the mobile web client of bx: static files embedded in the binary and served
// by bx serve at /. It talks to the same JSON API and stream WebSocket as the TUI.
package web

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed static
var files embed.FS

// csp allows only this server. xterm.js adds <style> elements, hence 'unsafe-inline' for styles.
// frame-ancestors 'none' stops other pages from framing the app: a framed copy is same-origin,
// so the origin guard could not tell its requests apart.
const csp = "default-src 'self'; connect-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; frame-ancestors 'none'"

// Handler serves the app. no-cache makes the browser revalidate, so a deploy shows up on reload.
func Handler() http.Handler {
	sub, err := fs.Sub(files, "static")
	if err != nil {
		panic(err) // the embed directive guarantees "static" exists
	}
	files := http.FileServerFS(sub)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", csp)
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Cache-Control", "no-cache")
		files.ServeHTTP(w, r)
	})
}
