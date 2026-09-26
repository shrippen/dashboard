package web

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"net/http"
	"sync"
)

//go:embed static
var staticFiles embed.FS

// RegisterStaticRoutes serves vendored assets (htmx, ...) so board pages
// can lazy-load widget fragments without a CDN dependency.
func (d Deps) RegisterStaticRoutes(mux *http.ServeMux) {
	mux.Handle("GET /static/", cacheStatic(http.FileServerFS(staticFiles)))
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

// assetVersion is a hash over all static files: it changes with every
// release that touches them, e.g. /static/andon.css?v=3f2a9c01d4.
var assetVersion = sync.OnceValue(func() string {
	h := sha256.New()
	_ = fs.WalkDir(staticFiles, "static", func(path string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return err
		}
		body, err := staticFiles.ReadFile(path)
		if err != nil {
			return err
		}
		h.Write([]byte(path))
		h.Write(body)
		return nil
	})
	return hex.EncodeToString(h.Sum(nil))[:assetVersionLen]
})

const assetVersionLen = 10

// asset is the template func for a static file's versioned URL.
func asset(name string) string {
	return "/static/" + name + "?v=" + assetVersion()
}

// cacheStatic lets browsers keep versioned files for good, so a page change
// reads CSS and JS from the cache; plain URLs revalidate every time.
func cacheStatic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("v") {
			w.Header().Set("Cache-Control", cacheForever)
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		next.ServeHTTP(w, r)
	})
}
