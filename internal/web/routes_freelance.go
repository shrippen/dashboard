package web

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"dashboard/internal/services/access"
	"dashboard/internal/services/assist"
	"dashboard/internal/services/billing"
	"dashboard/internal/services/mailfwd"

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
	req := timer.Request{Action: timer.Action(r.FormValue("action")), Project: num("project"), Activity: num("activity"),
		Sheet: num("sheet"), Note: strings.TrimSpace(r.FormValue("note")), Begin: r.FormValue("begin"), End: r.FormValue("end")}
	err = timer.Run(r.Context(), d.DB, ctx.Who, id, req, ClientIP(r))
	if errors.Is(err, timer.ErrBadRange) {
		d.renderKimaiNew(w, r, ctx, id, req, err.Error())
		return
	}
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

// newEntryStep rounds the add-entry form's default times (5 minutes).
const newEntryStep = 5 * time.Minute

// formTimeLayout is what a datetime-local input sends and shows.
const formTimeLayout = "2006-01-02T15:04"

// handleKimaiNew swaps a Kimai Lite tile for its add-entry form: the
// last hour, ending now.
func (d Deps) handleKimaiNew(w http.ResponseWriter, r *http.Request) {
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
	end := time.Now().Truncate(newEntryStep)
	d.renderKimaiNew(w, r, ctx, id, timer.Request{Begin: end.Add(-time.Hour).Format(formTimeLayout), End: end.Format(formTimeLayout)}, "")
}

func (d Deps) renderKimaiNew(w http.ResponseWriter, r *http.Request, ctx Ctx, id int64, req timer.Request, errKey string) {
	catalog, err := timer.Catalog(r.Context(), d.DB, ctx.Who, id)
	if errors.Is(err, timer.ErrNotTimer) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if err != nil {
		d.handleBoardError(w, r, err)
		return
	}
	_ = d.Page(w, ctx, "kimai_new", http.StatusOK, map[string]any{"PlacementID": id, "Catalog": catalog, "Req": req, "Error": errKey, "ThemeURL": ""})
}

// RegisterBillingRoutes wires invoice drafts and the tax year package.
func (d Deps) RegisterBillingRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /billing", d.handleBillingPage)
	mux.HandleFunc("POST /billing/draft", d.handleBillingDraft)
	mux.HandleFunc("GET /billing/export", d.handleBillingExport)
	mux.HandleFunc("POST /billing/mail", d.handleMailForward)
	mux.HandleFunc("POST /billing/mail/read", d.handleMailRead)
	mux.HandleFunc("POST /billing/payment", d.handlePaymentBook)
}

// handlePaymentBook books a matched bank income in Invoice Ninja.
func (d Deps) handlePaymentBook(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	space, _ := strconv.ParseInt(r.FormValue("space_id"), 10, 64)
	invoice, _ := strconv.ParseInt(r.FormValue("invoice_id"), 10, 64)
	if err := billing.Book(r.Context(), d.DB, ctx.Who, space, r.FormValue("txn"), invoice, ClientIP(r)); err != nil {
		d.billingPage(w, r, ctx, http.StatusBadRequest, map[string]any{"Error": errKey(err)})
		return
	}
	http.Redirect(w, r, "/billing?booked="+url.QueryEscape(r.FormValue("number"))+"#payments", http.StatusSeeOther)
}

func (d Deps) billingPage(w http.ResponseWriter, r *http.Request, ctx Ctx, status int, extra map[string]any) {
	drafts, err := billing.Candidates(r.Context(), d.DB, ctx.Who)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	mails, err := mailfwd.List(r.Context(), d.DB, ctx.Who)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	payments, err := billing.Payments(r.Context(), d.DB, ctx.Who)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	year := time.Now().Year()
	values := map[string]any{"Drafts": drafts, "Mails": mails, "Payments": payments, "Booked": r.URL.Query().Get("booked"), "Assist": assist.Enabled(), "Spaces": access.EditableSpaces(ctx.Who), "Years": []int{year, year - 1}}
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
	query := r.URL.Query()
	d.billingPage(w, r, ctx, http.StatusOK, map[string]any{"Created": query.Get("created"), "Forwarded": query.Get("forwarded")})
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

// handleMailForward sends one invoice mail's attachments to Paperless.
func (d Deps) handleMailForward(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	conn, _ := strconv.ParseInt(r.FormValue("conn"), 10, 64)
	uid, _ := strconv.ParseUint(r.FormValue("uid"), 10, 32)
	n, err := mailfwd.Forward(r.Context(), d.DB, ctx.Who, conn, uint32(uid), ClientIP(r))
	if err != nil {
		d.billingPage(w, r, ctx, http.StatusBadRequest, map[string]any{"Error": errKey(err)})
		return
	}
	http.Redirect(w, r, "/billing?forwarded="+strconv.Itoa(n), http.StatusSeeOther)
}

// handleMailRead lets Claude read one invoice mail's attachments.
func (d Deps) handleMailRead(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	conn, _ := strconv.ParseInt(r.FormValue("conn"), 10, 64)
	uid, _ := strconv.ParseUint(r.FormValue("uid"), 10, 32)
	if _, err := mailfwd.Read(r.Context(), d.DB, ctx.Who, conn, uint32(uid), ClientIP(r)); err != nil {
		d.billingPage(w, r, ctx, http.StatusBadRequest, map[string]any{"Error": errKey(err)})
		return
	}
	http.Redirect(w, r, "/billing#mail-"+strconv.FormatUint(uid, 10), http.StatusSeeOther)
}
