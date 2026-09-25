package web

import (
	"errors"
	"net/http"
	"strconv"

	"dashboard/internal/services/svcdata"
	"dashboard/internal/services/timer"
)

// handleKimaiTimer starts or stops a Kimai timer from its tile and
// answers with the refreshed tile.
func (d Deps) handleKimaiTimer(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	num := func(name string) int64 {
		n, _ := strconv.ParseInt(r.FormValue(name), 10, 64)
		return n
	}
	err = timer.Run(r.Context(), d.DB, ctx.Who, id, timer.Action(r.FormValue("action")), num("project"), num("activity"), num("sheet"), ClientIP(r))
	if errors.Is(err, timer.ErrNotTimer) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if err != nil {
		d.handleBoardError(w, r, err)
		return
	}
	d.renderFragment(w, r, ctx, id, svcdata.Force)
}
