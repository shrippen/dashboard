package web

import (
	"embed"
	"net/http"
)

//go:embed static
var staticFiles embed.FS

// RegisterStaticRoutes serves vendored assets (htmx, ...) so board pages
// can lazy-load widget fragments without a CDN dependency.
func (d Deps) RegisterStaticRoutes(mux *http.ServeMux) {
	mux.Handle("GET /static/", http.FileServerFS(staticFiles))
	mux.HandleFunc("GET /sw.js", handleWorker)
}

// handleWorker serves the service worker from the root so its scope
// covers every page (offline view).
func handleWorker(w http.ResponseWriter, r *http.Request) {
	body, err := staticFiles.ReadFile("static/sw.js")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(body)
}
