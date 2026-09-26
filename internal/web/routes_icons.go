package web

import (
	"errors"
	"io"
	"mime"
	"net/http"

	"andon/internal/services/icons"
)

const (
	iconCSP     = "default-src 'none'; style-src 'unsafe-inline'"
	iconCache   = "public, max-age=86400"
	appName     = "Andon"
	appBg       = "#141312"
	maxIconForm = 1 << 20
)

// RegisterIconRoutes wires cached icons, icon upload and the PWA manifest.
func (d Deps) RegisterIconRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /icons/{key}", d.handleIcon)
	mux.HandleFunc("POST /icons/upload", d.handleIconUpload)
	mux.HandleFunc("GET /manifest.webmanifest", d.handleManifest)
}

// handleIcon serves a cached icon. The strict CSP neutralises anything an
// SVG might still carry when opened directly.
func (d Deps) handleIcon(w http.ResponseWriter, r *http.Request) {
	body, media, ok := icons.Read(r.PathValue("key"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", media)
	w.Header().Set("Content-Security-Policy", iconCSP)
	w.Header().Set("Cache-Control", iconCache)
	w.Write(body)
}

// handleIconUpload stores an uploaded icon and answers with its spec
// ("upload:<key>"), which editor.js puts into the icon field.
func (d Deps) handleIconUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxIconForm)
	if err := r.ParseMultipartForm(maxIconForm); err != nil {
		http.Error(w, "icon.invalid", http.StatusBadRequest)
		return
	}
	if _, err := d.Require(r); err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	f, header, err := r.FormFile(formFileField)
	if err != nil {
		http.Error(w, "icon.invalid", http.StatusBadRequest)
		return
	}
	defer f.Close()
	body, err := io.ReadAll(f)
	if err != nil {
		http.Error(w, "icon.invalid", http.StatusBadRequest)
		return
	}
	media, _, _ := mime.ParseMediaType(header.Header.Get("Content-Type"))
	spec, err := icons.Upload(body, media)
	if errors.Is(err, icons.ErrInvalid) {
		http.Error(w, "icon.invalid", http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte(spec))
}

func (d Deps) handleManifest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/manifest+json")
	writeJSONAs(w, map[string]any{
		"name": appName, "short_name": appName, "start_url": "/", "display": "standalone",
		"background_color": appBg, "theme_color": appBg,
		"icons": []map[string]string{{"src": "/static/icon.svg", "sizes": "any", "type": "image/svg+xml"}},
	})
}
