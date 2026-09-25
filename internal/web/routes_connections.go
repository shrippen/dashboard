package web

import (
	"net/http"
	"strconv"

	"dashboard/internal/enums"
	"dashboard/internal/services/access"
	"dashboard/internal/services/connections"
	"dashboard/internal/services/hooks"
	"dashboard/internal/services/porting"
)

// RegisterConnectionRoutes wires the connections list/create/edit/delete/test pages.
func (d Deps) RegisterConnectionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /connections", d.handleConnectionsList)
	mux.HandleFunc("GET /connections/new", d.handleConnectionNewForm)
	mux.HandleFunc("POST /connections", d.handleConnectionCreate)
	mux.HandleFunc("GET /connections/{id}/edit", d.handleConnectionEditForm)
	mux.HandleFunc("POST /connections/{id}/edit", d.handleConnectionUpdate)
	mux.HandleFunc("POST /connections/{id}/delete", d.handleConnectionDelete)
	mux.HandleFunc("POST /connections/{id}/test", d.handleConnectionTest)
}

func (d Deps) handleConnectionsList(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	list, err := connections.Listing(d.DB, ctx.Who, enums.RightView)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = d.Page(w, ctx, "connections", http.StatusOK, map[string]any{
		"Connections": list, "Services": serviceOptions,
	})
}

var serviceOptions = enums.Services

func (d Deps) handleConnectionNewForm(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	spaces := access.EditableSpaces(ctx.Who)
	_ = d.Page(w, ctx, "connection_form", http.StatusOK, map[string]any{
		"Spaces": spaces, "Services": serviceOptions, "IsNew": true,
	})
}

func (d Deps) handleConnectionCreate(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	spaceID, _ := strconv.ParseInt(r.FormValue("space_id"), 10, 64)
	tls := connections.TLSVerify
	if r.FormValue("tls") == "skip" {
		tls = connections.TLSSkip
	}
	mode := enums.CredentialMode(r.FormValue("mode"))
	if mode == "" {
		mode = enums.CredentialShared
	}

	id, err := connections.Create(d.DB, ctx.Who, spaceID, enums.ServiceType(r.FormValue("service")),
		r.FormValue("name"), r.FormValue("url"), mode, r.FormValue("secret"), tls, nil)
	if err != nil {
		_ = d.Page(w, ctx, "connection_form", http.StatusBadRequest, map[string]any{
			"Spaces": access.EditableSpaces(ctx.Who), "Services": serviceOptions, "IsNew": true, "Error": err.Error(),
		})
		return
	}
	http.Redirect(w, r, "/connections/"+strconv.FormatInt(id, 10)+"/edit", http.StatusSeeOther)
}

func (d Deps) handleConnectionEditForm(w http.ResponseWriter, r *http.Request) {
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
	conn, err := connections.Get(d.DB, ctx.Who, id)
	if err != nil {
		d.handleBoardError(w, r, err)
		return
	}
	values := map[string]any{
		"Conn": conn, "Services": serviceOptions, "IsNew": false,
		"OptionsYAML": porting.DumpMap(conn.Options), "Error": r.URL.Query().Get("error"),
	}
	if hooks.Accepts(conn.Service) {
		values["HookURL"], _ = hooks.URL(d.Settings.BaseURL, conn.ID)
	}
	_ = d.Page(w, ctx, "connection_form", http.StatusOK, values)
}

func (d Deps) handleConnectionUpdate(w http.ResponseWriter, r *http.Request) {
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
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	tls := connections.TLSVerify
	if r.FormValue("tls") == "skip" {
		tls = connections.TLSSkip
	}
	mode := enums.CredentialMode(r.FormValue("mode"))
	if mode == "" {
		mode = enums.CredentialShared
	}
	var secret *string
	if s := r.FormValue("secret"); s != "" {
		secret = &s
	}

	if err := connections.Update(d.DB, ctx.Who, id, r.FormValue("name"), r.FormValue("url"), mode, secret, tls, nil); err != nil {
		conn, _ := connections.Get(d.DB, ctx.Who, id)
		_ = d.Page(w, ctx, "connection_form", http.StatusBadRequest, map[string]any{
			"Conn": conn, "Services": serviceOptions, "IsNew": false,
			"OptionsYAML": porting.DumpMap(conn.Options), "Error": errKey(err),
		})
		return
	}
	http.Redirect(w, r, "/connections", http.StatusSeeOther)
}

func (d Deps) handleConnectionDelete(w http.ResponseWriter, r *http.Request) {
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
	if err := connections.Delete(d.DB, ctx.Who, id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/connections", http.StatusSeeOther)
}

func (d Deps) handleConnectionTest(w http.ResponseWriter, r *http.Request) {
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
	result, err := connections.Test(r.Context(), d.DB, ctx.Who, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	conn, _ := connections.Get(d.DB, ctx.Who, id)
	_ = d.Page(w, ctx, "connection_form", http.StatusOK, map[string]any{
		"Conn": conn, "Services": serviceOptions, "IsNew": false,
		"OptionsYAML": porting.DumpMap(conn.Options), "Error": r.URL.Query().Get("error"), "TestResult": result,
	})
}
