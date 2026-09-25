package web

import (
	"net/http"
	"strconv"
	"strings"

	"dashboard/internal/services/themes"
)

// RegisterThemeRoutes wires the theme stylesheet endpoint.
func (d Deps) RegisterThemeRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /theme/{idcss}", d.handleThemeCSS)
}

// handleThemeCSS serves one theme's rendered stylesheet. No auth: it's a
// <link> target, cached hard by the version in its own query string.
func (d Deps) handleThemeCSS(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimSuffix(r.PathValue("idcss"), ".css")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	css, _, err := themes.Stylesheet(d.DB, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Write([]byte(css))
}
