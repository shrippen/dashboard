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
}
