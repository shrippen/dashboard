package web

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"dashboard/internal/services/access"
	"dashboard/internal/services/billing"

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

// RegisterBillingRoutes wires invoice drafts and the tax year package.
func (d Deps) RegisterBillingRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /billing", d.handleBillingPage)
	mux.HandleFunc("POST /billing/draft", d.handleBillingDraft)
	mux.HandleFunc("GET /billing/export", d.handleBillingExport)
}

func (d Deps) billingPage(w http.ResponseWriter, r *http.Request, ctx Ctx, status int, extra map[string]any) {
	drafts, err := billing.Candidates(r.Context(), d.DB, ctx.Who)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	year := time.Now().Year()
	values := map[string]any{"Drafts": drafts, "Spaces": access.EditableSpaces(ctx.Who), "Years": []int{year, year - 1}}
	for k, v := range extra {
		values[k] = v
	}
	_ = d.Page(w, ctx, "billing", status, values)
}

func (d Deps) handleBillingPage(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	d.billingPage(w, r, ctx, http.StatusOK, map[string]any{"Created": r.URL.Query().Get("created")})
}

func (d Deps) handleBillingDraft(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	space, _ := strconv.ParseInt(r.FormValue("space_id"), 10, 64)
	customer, _ := strconv.ParseInt(r.FormValue("customer_id"), 10, 64)
	mode := billing.KeepSheets
	if r.FormValue("mark_exported") != "" {
		mode = billing.MarkSheets
	}
	number, err := billing.Create(r.Context(), d.DB, ctx.Who, space, customer, mode, ClientIP(r))
	if err != nil {
		d.billingPage(w, r, ctx, http.StatusBadRequest, map[string]any{"Error": errKey(err)})
		return
	}
	http.Redirect(w, r, "/billing?created="+url.QueryEscape(number), http.StatusSeeOther)
}

func (d Deps) handleBillingExport(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	space, _ := strconv.ParseInt(r.FormValue("space_id"), 10, 64)
	year, _ := strconv.Atoi(r.FormValue("year"))
	name, blob, err := billing.Export(r.Context(), d.DB, ctx.Who, space, year, ClientIP(r))
	if err != nil {
		d.billingPage(w, r, ctx, http.StatusBadRequest, map[string]any{"Error": errKey(err)})
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Write(blob)
}
