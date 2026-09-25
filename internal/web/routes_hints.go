package web

import (
	"net/http"
	"strconv"

	"dashboard/internal/enums"
	"dashboard/internal/services/hints"
)

// RegisterHintRoutes wires the hints overview page and snooze/ack/reopen actions.
func (d Deps) RegisterHintRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /hints", d.handleHintsPage)
	mux.HandleFunc("POST /hints/{id}/ack", d.handleHintAct(hints.ActionAck))
	mux.HandleFunc("POST /hints/{id}/snooze", d.handleHintAct(hints.ActionSnooze))
	mux.HandleFunc("POST /hints/{id}/reopen", d.handleHintAct(hints.ActionReopen))
}

const defaultSnoozeDays = 7

func (d Deps) handleHintsPage(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	found, err := hints.Active(d.DB, ctx.Who, enums.SeverityInfo, nil, 0)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = d.Page(w, ctx, "hints", http.StatusOK, map[string]any{"Hints": found})
}

func (d Deps) handleHintAct(action hints.Action) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, err := d.Require(r)
		if err != nil {
			d.handleAuthError(w, r, err)
			return
		}
		if err := d.checkCSRF(r, ctx.CSRF); err != nil {
			d.handleAuthError(w, r, err)
			return
		}
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		days, _ := strconv.Atoi(r.FormValue("days"))
		if days <= 0 {
			days = defaultSnoozeDays
		}
		if err := hints.Act(d.DB, ctx.Who, id, action, days); err != nil {
			d.handleBoardError(w, r, err)
			return
		}
		http.Redirect(w, r, "/hints", http.StatusSeeOther)
	}
}
