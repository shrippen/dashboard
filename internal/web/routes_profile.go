package web

import (
	"net/http"
	"strconv"

	"dashboard/internal/enums"
	"dashboard/internal/services/accounts"
	"dashboard/internal/services/boards"
)

// RegisterProfileRoutes wires the personal settings page (/me/profile):
// name, locale, colour mode, start board, theme, search engine.
func (d Deps) RegisterProfileRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /me/profile", d.handleProfilePage)
	mux.HandleFunc("POST /me/profile", d.handleProfileSave)
}

func (d Deps) profilePage(w http.ResponseWriter, ctx Ctx, status int, extra map[string]any) {
	profile, err := accounts.GetProfile(d.DB, ctx.Who)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	myBoards, err := boards.Visible(d.DB, ctx.Who)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	values := map[string]any{"Profile": profile, "Boards": myBoards}
	for k, v := range extra {
		values[k] = v
	}
	_ = d.Page(w, ctx, "profile", status, values)
}

func (d Deps) handleProfilePage(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	d.profilePage(w, ctx, http.StatusOK, nil)
}

func (d Deps) handleProfileSave(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	name := r.FormValue("name")
	locale := enums.Locale(r.FormValue("locale"))
	colorMode := enums.ColorMode(r.FormValue("color_mode"))
	searchEngine := r.FormValue("search_engine")
	changes := accounts.ProfileChanges{Name: &name, Locale: &locale, ColorMode: &colorMode, SearchEngine: &searchEngine}

	if raw := r.FormValue("start_board_id"); raw != "" {
		id, parseErr := strconv.ParseInt(raw, 10, 64)
		if parseErr == nil {
			changes.StartBoardID = ptr(&id)
		}
	} else {
		changes.StartBoardID = ptr[*int64](nil)
	}

	if err := accounts.UpdateProfile(d.DB, ctx.Who, changes); err != nil {
		d.profilePage(w, ctx, http.StatusBadRequest, map[string]any{"Error": err.Error()})
		return
	}
	http.Redirect(w, r, "/me/profile", http.StatusSeeOther)
}

// ptr is a small generic address-of helper for optional-field structs like
// accounts.ProfileChanges, which needs **T to distinguish "leave the field
// alone" (nil) from "set it to nil" (non-nil pointer to a nil value).
func ptr[T any](v T) *T { return &v }
