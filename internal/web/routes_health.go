package web

import "net/http"

// RegisterHealthRoute wires /healthz for the container's HEALTHCHECK. No
// auth, no DB access — a process that can still accept a request is
// healthy enough to keep serving.
func (d Deps) RegisterHealthRoute(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}
