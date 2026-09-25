package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"dashboard/internal/services/hooks"
)

const hookBodyMax = 16 << 10

// RegisterHookRoutes wires inbound webhooks (no session: the URL carries
// its own signature).
func (d Deps) RegisterHookRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /hooks/{id}/{sig}", d.handleHook)
}

// hookBody is what a PG Back Web webhook is configured to send, e.g.
// {"event": "execution_failed", "name": "kimai"}. Query parameters of
// the same names work too.
type hookBody struct {
	Event string `json:"event"`
	Name  string `json:"name"`
}

func (d Deps) handleHook(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	var body hookBody
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, hookBodyMax)).Decode(&body)
	if body.Event == "" {
		body.Event = r.URL.Query().Get("event")
	}
	if body.Name == "" {
		body.Name = r.URL.Query().Get("name")
	}

	if err := hooks.Receive(d.DB, id, r.PathValue("sig"), body.Event, body.Name); err != nil {
		if errors.Is(err, hooks.ErrRejected) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
